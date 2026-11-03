// SPDX-License-Identifier: Apache-2.0

package pluginkit_test

import (
	"context"
	"errors"
	"slices"
	"testing"
	"time"

	"github.com/gopherium/framework/pluginkit"
)

// cancellingMigrator is a plugin whose Migrate ends the startup context and succeeds.
type cancellingMigrator struct {
	fakePlugin
	cancel context.CancelFunc
}

// Migrate ends the startup context.
func (c *cancellingMigrator) Migrate(context.Context) error {
	*c.calls = append(*c.calls, c.id+" migrate")
	c.cancel()
	return nil
}

// cancellingVersionedMigrator is a cancelling migrator whose schema reports a version.
type cancellingVersionedMigrator struct {
	cancellingMigrator
}

// MigrationVersion returns a version no store has recorded.
func (c *cancellingVersionedMigrator) MigrationVersion() (string, error) {
	return "1", nil
}

// succeedingCanceller is a plugin whose Start ends the startup context and succeeds.
type succeedingCanceller struct {
	fakePlugin
	cancel context.CancelFunc
}

// Start ends the startup context.
func (s *succeedingCanceller) Start(context.Context) error {
	*s.calls = append(*s.calls, s.id+" start")
	s.cancel()
	return nil
}

// contextEnded returns the failure of the plugin id that waited for step when the context was cancelled.
func contextEnded(id string, step pluginkit.Step) failureWant {
	return failureWant{id, step, false, "pluginkit: " + id + " " + string(step) + ": context canceled"}
}

func TestStartEachStillRecordsAMigrationThatEndedTheContext(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name      string
		recordErr error
		failed    []failureWant
	}{
		{"a record that works", nil, []failureWant{contextEnded("alpha", pluginkit.StepStart)}},
		{"a record that fails", errStoreOffline, []failureWant{
			{"alpha", pluginkit.StepRecord, false, "pluginkit: alpha record: store offline"},
			contextEnded("alpha", pluginkit.StepStart),
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			var calls []string
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			store := &memoryStore{versions: map[string]string{}, recordErr: tc.recordErr, calls: &calls}
			alpha := &cancellingVersionedMigrator{
				cancellingMigrator{fakePlugin: fakePlugin{id: "alpha", calls: &calls}, cancel: cancel},
			}

			outcome := startEach(ctx, t, storeHost(t, store, nil, alpha))

			wantOutcome(t, outcome, nil, tc.failed)
			wantCalls(t, calls, []string{"versions", "alpha migrate", "record alpha 1"})
		})
	}
}

func TestStartEachReportsTheRestWhenTheContextEnds(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name    string
		plugins func(c *[]string, cancel context.CancelFunc) []pluginkit.Plugin
		calls   []string
		started []string
		failed  []failureWant
	}{
		{"during a start", func(c *[]string, cancel context.CancelFunc) []pluginkit.Plugin {
			return []pluginkit.Plugin{
				plain("alpha", c),
				&cancellingStarter{fakePlugin: fakePlugin{id: "beta", calls: c}, cancel: cancel},
				migrating("gamma", c),
			}
		}, []string{"gamma migrate", "alpha start", "beta start"}, []string{"alpha"}, []failureWant{
			contextEnded("beta", pluginkit.StepStart), contextEnded("gamma", pluginkit.StepStart),
		}},
		{"during a start that still succeeds", func(c *[]string, cancel context.CancelFunc) []pluginkit.Plugin {
			return []pluginkit.Plugin{
				&succeedingCanceller{fakePlugin: fakePlugin{id: "alpha", calls: c}, cancel: cancel},
				plain("beta", c),
			}
		}, []string{"alpha start"}, []string{"alpha"}, []failureWant{contextEnded("beta", pluginkit.StepStart)}},
		{"during a migration", func(c *[]string, cancel context.CancelFunc) []pluginkit.Plugin {
			return []pluginkit.Plugin{
				&cancellingMigrator{fakePlugin: fakePlugin{id: "alpha", calls: c}, cancel: cancel},
				migrating("beta", c),
				plain("gamma", c),
			}
		}, []string{"alpha migrate"}, nil, []failureWant{
			contextEnded("alpha", pluginkit.StepStart),
			contextEnded("beta", pluginkit.StepMigrate),
			contextEnded("gamma", pluginkit.StepMigrate),
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			var calls []string
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			host := pluginkit.NewHost(tc.plugins(&calls, cancel)...)

			outcome := startEach(ctx, t, host)

			wantOutcome(t, outcome, tc.started, tc.failed)
			wantCalls(t, calls, tc.calls)
			for _, failure := range outcome.Failed {
				if !errors.Is(failure.Err, context.Canceled) {
					t.Errorf("failure of %s = %v, want the context's error in its chain", failure.ID, failure.Err)
				}
			}
		})
	}
}

func TestStartEachCallsNoPluginOnAContextAlreadyEnded(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithDeadline(t.Context(), time.Now().Add(-time.Minute))
	defer cancel()
	var calls []string
	store := &memoryStore{versions: map[string]string{}, calls: &calls}
	lookup := func(pluginkit.Plugin) (pluginkit.Migrator, bool) {
		calls = append(calls, "lookup")
		return nil, false
	}
	host := storeHost(t, store, lookup, versioned("alpha", "1", &calls), plain("beta", &calls))

	outcome := startEach(ctx, t, host)

	wantOutcome(t, outcome, nil, []failureWant{
		{"alpha", pluginkit.StepMigrate, false, "pluginkit: alpha migrate: context deadline exceeded"},
		{"beta", pluginkit.StepMigrate, false, "pluginkit: beta migrate: context deadline exceeded"},
	})
	wantCalls(t, calls, []string{"versions"})
	for _, failure := range outcome.Failed {
		if !errors.Is(failure.Err, context.DeadlineExceeded) {
			t.Errorf("failure of %s = %v, want the context's error in its chain", failure.ID, failure.Err)
		}
	}
}

func TestStartEachReportsTheContextsErrorOverItsCause(t *testing.T) {
	t.Parallel()

	errShutdown := errors.New("the operator asked to stop")
	ctx, cancel := context.WithCancelCause(t.Context())
	cancel(errShutdown)
	var calls []string
	host := pluginkit.NewHost(plain("alpha", &calls))

	outcome := startEach(ctx, t, host)

	wantOutcome(t, outcome, nil, []failureWant{contextEnded("alpha", pluginkit.StepMigrate)})
	if !errors.Is(outcome.Failed[0].Err, context.Canceled) {
		t.Errorf("failure = %v, want the context's error in its chain", outcome.Failed[0].Err)
	}
}

func TestHostStartLeavesAnEndedContextToThePlugins(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name  string
		host  func(t *testing.T, calls *[]string) *pluginkit.Host
		calls []string
	}{
		{"with no store", func(_ *testing.T, c *[]string) *pluginkit.Host {
			return pluginkit.NewHost(migrating("alpha", c), plain("beta", c))
		}, []string{"alpha migrate", "alpha start", "beta start"}},
		{"with a store", func(t *testing.T, c *[]string) *pluginkit.Host {
			store := &memoryStore{versions: map[string]string{}, calls: c}
			return storeHost(t, store, nil, versioned("alpha", "1", c), plain("beta", c))
		}, []string{"versions", "alpha version", "alpha migrate", "record alpha 1", "alpha start", "beta start"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			var calls []string
			ctx, cancel := context.WithCancel(t.Context())
			cancel()

			if err := tc.host(t, &calls).Start(ctx, stopGrace); err != nil {
				t.Fatalf("Start() error = %v, want nil from plugins that ignore their context", err)
			}

			wantCalls(t, calls, tc.calls)
		})
	}
}

// contextStarter is a plugin that records the caller value its Start context carries.
type contextStarter struct {
	fakePlugin
	seen *[]any
}

// Start records the caller value of ctx.
func (c *contextStarter) Start(ctx context.Context) error {
	*c.seen = append(*c.seen, ctx.Value(callerKey{}))
	return nil
}

func TestStartEachHandsTheCallerContextToEveryStep(t *testing.T) {
	t.Parallel()

	var calls []string
	var seen []any
	host, err := pluginkit.NewHostWithMigrationStore(&contextStore{seen: &seen}, nil,
		&versionedContextMigrator{contextMigrator{fakePlugin: fakePlugin{id: "alpha", calls: &calls}, seen: &seen}},
		&contextStarter{fakePlugin: fakePlugin{id: "beta", calls: &calls}, seen: &seen})
	if err != nil {
		t.Fatalf("NewHostWithMigrationStore() error = %v, want nil", err)
	}

	startEach(context.WithValue(t.Context(), callerKey{}, "caller"), t, host)

	if want := []any{"caller", "caller", "caller", "caller"}; !slices.Equal(want, seen) {
		t.Errorf("context values seen = %v, want %v from the read, the migration, the record and the start", seen, want)
	}
}

// endableStore is a migration store that records, for each context it gets, whether that context can end.
type endableStore struct {
	seen *[]bool
}

// Versions records whether ctx can end and returns no versions.
func (s *endableStore) Versions(ctx context.Context) (map[string]string, error) {
	*s.seen = append(*s.seen, ctx.Done() != nil)
	return map[string]string{}, nil
}

// Record records whether ctx can end.
func (s *endableStore) Record(ctx context.Context, _, _ string) error {
	*s.seen = append(*s.seen, ctx.Done() != nil)
	return nil
}

// endablePlugin is a versioned migrator that records, for each context it gets, whether that context can end.
type endablePlugin struct {
	fakePlugin
	seen *[]bool
}

// Migrate records whether ctx can end.
func (e *endablePlugin) Migrate(ctx context.Context) error {
	*e.seen = append(*e.seen, ctx.Done() != nil)
	return nil
}

// MigrationVersion returns a version no store has recorded.
func (e *endablePlugin) MigrationVersion() (string, error) {
	return "1", nil
}

// Start records whether ctx can end.
func (e *endablePlugin) Start(ctx context.Context) error {
	*e.seen = append(*e.seen, ctx.Done() != nil)
	return nil
}

func TestStartEachHandsEveryStepAContextThatCanEnd(t *testing.T) {
	t.Parallel()

	var calls []string
	var seen []bool
	host, err := pluginkit.NewHostWithMigrationStore(&endableStore{seen: &seen}, nil,
		&endablePlugin{fakePlugin: fakePlugin{id: "alpha", calls: &calls}, seen: &seen})
	if err != nil {
		t.Fatalf("NewHostWithMigrationStore() error = %v, want nil", err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()

	startEach(ctx, t, host)

	if want := []bool{true, true, true, true}; !slices.Equal(want, seen) {
		t.Errorf("can end = %v, want %v from the read, the migration, the record and the start", seen, want)
	}
}
