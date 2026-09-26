// SPDX-License-Identifier: Apache-2.0

package pluginkit_test

import (
	"context"
	"errors"
	"net/http"
	"reflect"
	"slices"
	"testing"
	"testing/synctest"
	"time"

	"github.com/gopherium/framework/pluginkit"
)

// stopGrace is the stop budget the tests hand Start.
const stopGrace = time.Minute

var (
	_ pluginkit.Plugin             = (*fakePlugin)(nil)
	_ pluginkit.Migrator           = (*migratingPlugin)(nil)
	_ pluginkit.RouteProvider      = (*routedPlugin)(nil)
	_ pluginkit.PublicPathProvider = (*publicPathsPlugin)(nil)
)

type publicPathsPlugin struct {
	fakePlugin
	paths []string
}

func (p *publicPathsPlugin) PublicPaths() []string {
	return p.paths
}

func TestHostCollectsPublicPaths(t *testing.T) {
	t.Parallel()

	var calls []string
	host := pluginkit.NewHost(
		&publicPathsPlugin{fakePlugin: fakePlugin{id: "hooked", calls: &calls}, paths: []string{"/webhook"}},
		&fakePlugin{id: "plain", calls: &calls},
	)

	got := host.PublicPaths()

	want := map[string][]string{"hooked": {"/webhook"}}
	if !reflect.DeepEqual(want, got) {
		t.Errorf("PublicPaths() = %v, want %v", got, want)
	}
}

type routedPlugin struct {
	fakePlugin
	handler http.Handler
}

func (r *routedPlugin) Routes() http.Handler {
	return r.handler
}

type fakePlugin struct {
	id         string
	startErr   error
	stopErr    error
	startPanic bool
	stopPanic  bool
	calls      *[]string
}

func (f *fakePlugin) ID() string {
	return f.id
}

func (f *fakePlugin) Start(_ context.Context) error {
	*f.calls = append(*f.calls, f.id+" start")
	if f.startPanic {
		panic("boom")
	}
	return f.startErr
}

func (f *fakePlugin) Stop(_ context.Context) error {
	*f.calls = append(*f.calls, f.id+" stop")
	if f.stopPanic {
		panic("boom")
	}
	return f.stopErr
}

type migratingPlugin struct {
	fakePlugin
	migrateErr   error
	migratePanic bool
}

func (m *migratingPlugin) Migrate(_ context.Context) error {
	*m.calls = append(*m.calls, m.id+" migrate")
	if m.migratePanic {
		panic("boom")
	}
	return m.migrateErr
}

func TestHostStartsInOrderAndStopsInReverse(t *testing.T) {
	t.Parallel()

	var calls []string
	host := pluginkit.NewHost(
		&fakePlugin{id: "alpha", calls: &calls},
		&fakePlugin{id: "beta", calls: &calls},
	)

	if err := host.Start(t.Context(), stopGrace); err != nil {
		t.Fatalf("Start() error = %v, want nil", err)
	}
	if err := host.Stop(t.Context()); err != nil {
		t.Fatalf("Stop() error = %v, want nil", err)
	}

	want := []string{"alpha start", "beta start", "beta stop", "alpha stop"}
	if !slices.Equal(want, calls) {
		t.Errorf("lifecycle calls = %v, want %v", calls, want)
	}
}

func TestNewHostPanicsOnDuplicateIDs(t *testing.T) {
	t.Parallel()

	defer func() {
		if recover() == nil {
			t.Fatal("NewHost() did not panic, want a duplicate id panic")
		}
	}()

	var calls []string
	pluginkit.NewHost(
		&fakePlugin{id: "feed", calls: &calls},
		&fakePlugin{id: "feed", calls: &calls},
	)
}

func TestHostCollectsRoutesFromProviders(t *testing.T) {
	t.Parallel()

	var calls []string
	handler := http.HandlerFunc(func(http.ResponseWriter, *http.Request) {})
	host := pluginkit.NewHost(
		&fakePlugin{id: "alpha", calls: &calls},
		&routedPlugin{fakePlugin: fakePlugin{id: "beta", calls: &calls}, handler: handler},
	)

	routes := host.Routes()

	if len(routes) != 1 {
		t.Fatalf("Routes() returned %d entries, want 1", len(routes))
	}
	if _, ok := routes["beta"]; !ok {
		t.Error(`Routes() has no entry for "beta", want its handler`)
	}
}

func TestHostMigratesBeforeStarting(t *testing.T) {
	t.Parallel()

	var calls []string
	host := pluginkit.NewHost(
		&fakePlugin{id: "alpha", calls: &calls},
		&migratingPlugin{fakePlugin: fakePlugin{id: "beta", calls: &calls}},
	)

	if err := host.Start(t.Context(), stopGrace); err != nil {
		t.Fatalf("Start() error = %v, want nil", err)
	}

	want := []string{"beta migrate", "alpha start", "beta start"}
	if !slices.Equal(want, calls) {
		t.Errorf("migration ordering = %v, want %v", calls, want)
	}
}

func TestHostMigrateRunsEveryMigratorWithoutStarting(t *testing.T) {
	t.Parallel()

	var calls []string
	host := pluginkit.NewHost(
		&migratingPlugin{fakePlugin: fakePlugin{id: "alpha", calls: &calls}},
		&fakePlugin{id: "beta", calls: &calls},
		&migratingPlugin{fakePlugin: fakePlugin{id: "gamma", calls: &calls}},
	)

	if err := host.Migrate(t.Context()); err != nil {
		t.Fatalf("Migrate() error = %v, want nil", err)
	}

	want := []string{"alpha migrate", "gamma migrate"}
	if !slices.Equal(want, calls) {
		t.Errorf("migrate calls = %v, want %v", calls, want)
	}
}

// callerKey keys the context value a test hands the host.
type callerKey struct{}

// contextMigrator is a migrator that records the caller value its context carries.
type contextMigrator struct {
	fakePlugin
	seen *[]any
}

// Migrate records the caller value of ctx.
func (c *contextMigrator) Migrate(ctx context.Context) error {
	*c.seen = append(*c.seen, ctx.Value(callerKey{}))
	return nil
}

func TestHostMigrateHandsTheCallerContextToEveryMigrator(t *testing.T) {
	t.Parallel()

	var calls []string
	var seen []any
	host := pluginkit.NewHost(&contextMigrator{fakePlugin: fakePlugin{id: "alpha", calls: &calls}, seen: &seen})
	ctx := context.WithValue(t.Context(), callerKey{}, "caller")

	if err := host.Migrate(ctx); err != nil {
		t.Fatalf("Migrate() error = %v, want nil", err)
	}
	if err := host.Start(ctx, stopGrace); err != nil {
		t.Fatalf("Start() error = %v, want nil", err)
	}

	if want := []any{"caller", "caller"}; !slices.Equal(want, seen) {
		t.Errorf("context values seen = %v, want %v from Migrate and from Start", seen, want)
	}
}

func TestHostMigrateWithNoPluginsDoesNothing(t *testing.T) {
	t.Parallel()

	if err := pluginkit.NewHost().Migrate(t.Context()); err != nil {
		t.Errorf("Migrate() error = %v, want nil", err)
	}
}

func TestHostMigrateStopsAtTheFirstFailure(t *testing.T) {
	t.Parallel()

	errSchema := errors.New("schema exploded")
	cases := []struct {
		name string
		lead *migratingPlugin
		err  string
	}{
		{"a failing migration", &migratingPlugin{migrateErr: errSchema}, "pluginkit: alpha migrate: schema exploded"},
		{"a panicking migration", &migratingPlugin{migratePanic: true}, "pluginkit: alpha migrate panicked: boom"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			var calls []string
			tc.lead.fakePlugin = fakePlugin{id: "alpha", calls: &calls}
			host := pluginkit.NewHost(tc.lead, &migratingPlugin{fakePlugin: fakePlugin{id: "beta", calls: &calls}})

			err := host.Migrate(t.Context())

			if err == nil || err.Error() != tc.err {
				t.Fatalf("Migrate() error = %v, want %q", err, tc.err)
			}
			if want := []string{"alpha migrate"}; !slices.Equal(want, calls) {
				t.Errorf("migrate calls = %v, want %v", calls, want)
			}
		})
	}
}

func TestHostAbortsWhenMigrationFails(t *testing.T) {
	t.Parallel()

	errSchema := errors.New("schema exploded")
	var calls []string
	host := pluginkit.NewHost(
		&fakePlugin{id: "alpha", calls: &calls},
		&migratingPlugin{fakePlugin: fakePlugin{id: "beta", calls: &calls}, migrateErr: errSchema},
	)

	startErr := host.Start(t.Context(), stopGrace)

	if !errors.Is(startErr, errSchema) {
		t.Fatalf("Start() error = %v, want %v in its chain", startErr, errSchema)
	}
	want := []string{"beta migrate"}
	if !slices.Equal(want, calls) {
		t.Errorf("abort calls = %v, want %v", calls, want)
	}
}

func TestHostRecoversMigrationPanic(t *testing.T) {
	t.Parallel()

	var calls []string
	host := pluginkit.NewHost(
		&migratingPlugin{fakePlugin: fakePlugin{id: "beta", calls: &calls}, migratePanic: true},
	)

	err := host.Start(t.Context(), stopGrace)

	if want := "pluginkit: beta migrate panicked: boom"; err == nil || err.Error() != want {
		t.Fatalf("Start() error = %v, want the migration's own %q", err, want)
	}
}

func TestHostRollsBackWhenStartFails(t *testing.T) {
	t.Parallel()

	errBoot := errors.New("boot failed")
	var calls []string
	host := pluginkit.NewHost(
		&fakePlugin{id: "alpha", calls: &calls},
		&fakePlugin{id: "beta", startErr: errBoot, calls: &calls},
		&fakePlugin{id: "gamma", calls: &calls},
	)

	startErr := host.Start(t.Context(), stopGrace)

	if !errors.Is(startErr, errBoot) {
		t.Fatalf("Start() error = %v, want %v in its chain", startErr, errBoot)
	}
	want := []string{"alpha start", "beta start", "alpha stop"}
	if !slices.Equal(want, calls) {
		t.Errorf("rollback calls = %v, want %v", calls, want)
	}
}

// stopContext is what a Stop call saw of its context while it ran.
type stopContext struct {
	err       error
	remaining time.Duration
	bounded   bool
	caller    any
}

// stopRecorder is a plugin that records what its Stop saw of its context.
type stopRecorder struct {
	fakePlugin
	stopped *stopContext
}

// Stop records the state of ctx while the call runs.
func (s *stopRecorder) Stop(ctx context.Context) error {
	deadline, bounded := ctx.Deadline()
	s.stopped = &stopContext{ctx.Err(), time.Until(deadline), bounded, ctx.Value(callerKey{})}
	return s.fakePlugin.Stop(ctx)
}

// hangingStopper is a plugin whose Stop waits for its context to end.
type hangingStopper struct {
	fakePlugin
}

// Stop waits for ctx to end and answers its error.
func (h *hangingStopper) Stop(ctx context.Context) error {
	*h.calls = append(*h.calls, h.id+" stop")
	<-ctx.Done()
	return ctx.Err()
}

// cancellingStarter is a plugin whose Start ends the startup context and fails with its error.
type cancellingStarter struct {
	fakePlugin
	cancel context.CancelFunc
}

// Start ends the startup context and answers its error.
func (c *cancellingStarter) Start(ctx context.Context) error {
	*c.calls = append(*c.calls, c.id+" start")
	c.cancel()
	<-ctx.Done()
	return ctx.Err()
}

func TestHostRollsBackUnderItsOwnStopGrace(t *testing.T) {
	t.Parallel()

	var calls []string
	ctx, cancel := context.WithCancel(context.WithValue(t.Context(), callerKey{}, "caller"))
	defer cancel()
	started := &stopRecorder{fakePlugin: fakePlugin{id: "alpha", calls: &calls}}
	failing := &cancellingStarter{fakePlugin: fakePlugin{id: "beta", calls: &calls}, cancel: cancel}
	host := pluginkit.NewHost(started, failing)

	startErr := host.Start(ctx, stopGrace)

	if !errors.Is(startErr, context.Canceled) {
		t.Fatalf("Start() error = %v, want the cancelled start in its chain", startErr)
	}
	seen := started.stopped
	if seen == nil {
		t.Fatalf("rollback calls = %v, want alpha stopped", calls)
	}
	if seen.err != nil || !seen.bounded || seen.remaining <= 0 || seen.remaining > stopGrace {
		t.Errorf("rollback Stop context = err %v, deadline in %v bounded %t, want live and within the stop grace",
			seen.err, seen.remaining, seen.bounded)
	}
	if seen.caller != "caller" {
		t.Errorf("rollback Stop context value = %v, want the caller's", seen.caller)
	}
	if want := []string{"alpha start", "beta start", "alpha stop"}; !slices.Equal(want, calls) {
		t.Errorf("rollback calls = %v, want %v", calls, want)
	}
}

func TestHostStartRefusesAStopGraceThatIsNotAboveZero(t *testing.T) {
	t.Parallel()

	for _, grace := range []time.Duration{0, -time.Second} {
		t.Run(grace.String(), func(t *testing.T) {
			t.Parallel()

			var calls []string
			host := pluginkit.NewHost(&migratingPlugin{fakePlugin: fakePlugin{id: "alpha", calls: &calls}})

			err := host.Start(t.Context(), grace)

			want := "pluginkit: the stop grace must stand above zero, got " + grace.String()
			if err == nil || err.Error() != want || len(calls) != 0 {
				t.Errorf("Start() error = %v, calls %v, want %q before anything migrates or starts", err, calls, want)
			}
		})
	}
}

func TestHostStartAcceptsTheSmallestStopGrace(t *testing.T) {
	t.Parallel()

	var calls []string
	host := pluginkit.NewHost(&fakePlugin{id: "alpha", calls: &calls})

	if err := host.Start(t.Context(), time.Nanosecond); err != nil {
		t.Fatalf("Start() error = %v, want a one nanosecond grace accepted", err)
	}

	if want := []string{"alpha start"}; !slices.Equal(want, calls) {
		t.Errorf("start calls = %v, want %v", calls, want)
	}
}

func TestHostStartReportsAFailedRollbackStop(t *testing.T) {
	t.Parallel()

	errBoot := errors.New("boot failed")
	errHalt := errors.New("halt failed")
	var calls []string
	host := pluginkit.NewHost(
		&fakePlugin{id: "alpha", stopErr: errHalt, calls: &calls},
		&fakePlugin{id: "beta", startErr: errBoot, calls: &calls},
	)

	err := host.Start(t.Context(), stopGrace)

	want := "pluginkit: beta start: boot failed\npluginkit: alpha stop: halt failed"
	if !errors.Is(err, errBoot) || !errors.Is(err, errHalt) || err.Error() != want {
		t.Errorf("Start() error = %v, want %q with both failures in its chain", err, want)
	}
}

func TestHostStartEndsAHungRollbackAtTheStopGrace(t *testing.T) {
	t.Parallel()

	synctest.Test(t, func(t *testing.T) {
		errBoot := errors.New("boot failed")
		grace := 97 * time.Minute
		var calls []string
		host := pluginkit.NewHost(
			&hangingStopper{fakePlugin: fakePlugin{id: "alpha", calls: &calls}},
			&fakePlugin{id: "beta", startErr: errBoot, calls: &calls},
		)
		began := time.Now()

		err := host.Start(t.Context(), grace)

		want := "pluginkit: beta start: boot failed\npluginkit: alpha stop: context deadline exceeded"
		if !errors.Is(err, errBoot) || !errors.Is(err, context.DeadlineExceeded) || err.Error() != want {
			t.Errorf("Start() error = %v, want %q with both failures in its chain", err, want)
		}
		if waited := time.Since(began); waited != grace {
			t.Errorf("rollback waited %v, want exactly the stop grace %v", waited, grace)
		}
	})
}

func TestHostRecoversStartPanic(t *testing.T) {
	t.Parallel()

	var calls []string
	host := pluginkit.NewHost(
		&fakePlugin{id: "alpha", calls: &calls},
		&fakePlugin{id: "beta", startPanic: true, calls: &calls},
	)

	startErr := host.Start(t.Context(), stopGrace)

	if startErr == nil {
		t.Fatal("Start() error = nil, want a recovered panic error")
	}
	want := []string{"alpha start", "beta start", "alpha stop"}
	if !slices.Equal(want, calls) {
		t.Errorf("rollback calls = %v, want %v", calls, want)
	}
}

func TestHostStopCollectsAllFailures(t *testing.T) {
	t.Parallel()

	errAlpha := errors.New("alpha refused")
	var calls []string
	host := pluginkit.NewHost(
		&fakePlugin{id: "alpha", stopErr: errAlpha, calls: &calls},
		&fakePlugin{id: "beta", stopPanic: true, calls: &calls},
	)
	if err := host.Start(t.Context(), stopGrace); err != nil {
		t.Fatalf("Start() error = %v, want nil", err)
	}

	stopErr := host.Stop(t.Context())

	if !errors.Is(stopErr, errAlpha) {
		t.Fatalf("Stop() error = %v, want %v in its chain", stopErr, errAlpha)
	}
	want := []string{"alpha start", "beta start", "beta stop", "alpha stop"}
	if !slices.Equal(want, calls) {
		t.Errorf("stop calls = %v, want %v", calls, want)
	}
}
