// SPDX-License-Identifier: Apache-2.0

package pluginkit_test

import (
	"context"
	"errors"
	"slices"
	"testing"
	"testing/synctest"
	"time"

	"github.com/gopherium/framework/pluginkit"
)

// failureWant is what a test expects of one failure in a start outcome.
type failureWant struct {
	id       string
	step     pluginkit.Step
	panicked bool
	text     string
}

// plain returns a plugin with id that owns no schema.
func plain(id string, calls *[]string) *fakePlugin {
	return &fakePlugin{id: id, calls: calls}
}

// startEach runs StartEach on host and fails t when it answers an error of its own.
func startEach(ctx context.Context, t *testing.T, host *pluginkit.Host) pluginkit.StartOutcome {
	t.Helper()
	outcome, err := host.StartEach(ctx)
	if err != nil {
		t.Fatalf("StartEach() error = %v, want nil", err)
	}
	return outcome
}

// wantOutcome fails t unless outcome lists the plugins started and the failures failed, in that order.
func wantOutcome(t *testing.T, outcome pluginkit.StartOutcome, started []string, failed []failureWant) {
	t.Helper()
	ids := make([]string, 0, len(outcome.Started))
	for _, plugin := range outcome.Started {
		ids = append(ids, plugin.ID)
	}
	if !slices.Equal(started, ids) {
		t.Errorf("started = %v, want %v", ids, started)
	}
	got := make([]failureWant, 0, len(outcome.Failed))
	for _, failure := range outcome.Failed {
		got = append(got, failureWant{failure.ID, failure.Step, failure.Panicked, failure.Err.Error()})
	}
	if !slices.Equal(failed, got) {
		t.Errorf("failed = %+v, want %+v", got, failed)
	}
}

func TestStartEachStartsEveryPluginInRegistrationOrder(t *testing.T) {
	t.Parallel()

	var calls []string
	host := pluginkit.NewHost(plain("alpha", &calls), plain("beta", &calls), plain("gamma", &calls))

	outcome := startEach(t.Context(), t, host)

	wantOutcome(t, outcome, []string{"alpha", "beta", "gamma"}, nil)
	wantCalls(t, calls, []string{"alpha start", "beta start", "gamma start"})
}

func TestStartEachMigratesEveryPluginBeforeStartingAny(t *testing.T) {
	t.Parallel()

	var calls []string
	host := pluginkit.NewHost(plain("alpha", &calls), migrating("beta", &calls), migrating("gamma", &calls))

	outcome := startEach(t.Context(), t, host)

	wantOutcome(t, outcome, []string{"alpha", "beta", "gamma"}, nil)
	wantCalls(t, calls, []string{"beta migrate", "gamma migrate", "alpha start", "beta start", "gamma start"})
}

func TestStartEachWithNoPluginsReportsNothing(t *testing.T) {
	t.Parallel()

	outcome := startEach(t.Context(), t, pluginkit.NewHost())

	wantOutcome(t, outcome, nil, nil)
}

func TestStartEachGoesOnPastEachFailure(t *testing.T) {
	t.Parallel()

	errBoot := errors.New("boot failed")
	errSchema := errors.New("schema exploded")
	var discarded []string
	errNested := pluginkit.NewHost(&fakePlugin{id: "inner", startPanic: true, calls: &discarded}).
		Start(t.Context(), stopGrace)
	failingMigration := func(id string, c *[]string) *migratingPlugin {
		plugin := migrating(id, c)
		plugin.migrateErr = errSchema
		return plugin
	}
	cases := []struct {
		name    string
		plugins func(c *[]string) []pluginkit.Plugin
		calls   []string
		started []string
		failed  []failureWant
	}{
		{"a failed start", func(c *[]string) []pluginkit.Plugin {
			return []pluginkit.Plugin{
				plain("alpha", c), &fakePlugin{id: "beta", startErr: errBoot, calls: c}, plain("gamma", c),
			}
		}, []string{"alpha start", "beta start", "gamma start"}, []string{"alpha", "gamma"}, []failureWant{
			{"beta", pluginkit.StepStart, false, "pluginkit: beta start: boot failed"},
		}},
		{"a panicking start", func(c *[]string) []pluginkit.Plugin {
			return []pluginkit.Plugin{
				plain("alpha", c), &fakePlugin{id: "beta", startPanic: true, calls: c}, plain("gamma", c),
			}
		}, []string{"alpha start", "beta start", "gamma start"}, []string{"alpha", "gamma"}, []failureWant{
			{"beta", pluginkit.StepStart, true, "pluginkit: beta start panicked: boom"},
		}},
		{"a failed migration", func(c *[]string) []pluginkit.Plugin {
			return []pluginkit.Plugin{failingMigration("alpha", c), migrating("beta", c), plain("gamma", c)}
		}, []string{"alpha migrate", "beta migrate", "beta start", "gamma start"},
			[]string{"beta", "gamma"}, []failureWant{
				{"alpha", pluginkit.StepMigrate, false, "pluginkit: alpha migrate: schema exploded"},
			}},
		{"a panicking migration", func(c *[]string) []pluginkit.Plugin {
			panicking := migrating("alpha", c)
			panicking.migratePanic = true
			return []pluginkit.Plugin{panicking, migrating("beta", c)}
		}, []string{"alpha migrate", "beta migrate", "beta start"}, []string{"beta"}, []failureWant{
			{"alpha", pluginkit.StepMigrate, true, "pluginkit: alpha migrate panicked: boom"},
		}},
		{"failures of both steps, in registration order", func(c *[]string) []pluginkit.Plugin {
			return []pluginkit.Plugin{
				&fakePlugin{id: "alpha", startErr: errBoot, calls: c},
				failingMigration("beta", c),
				&fakePlugin{id: "gamma", startErr: errBoot, calls: c},
			}
		}, []string{"beta migrate", "alpha start", "gamma start"}, nil, []failureWant{
			{"alpha", pluginkit.StepStart, false, "pluginkit: alpha start: boot failed"},
			{"beta", pluginkit.StepMigrate, false, "pluginkit: beta migrate: schema exploded"},
			{"gamma", pluginkit.StepStart, false, "pluginkit: gamma start: boot failed"},
		}},
		{"an error holding a panic recovered further in", func(c *[]string) []pluginkit.Plugin {
			return []pluginkit.Plugin{&fakePlugin{id: "alpha", startErr: errNested, calls: c}}
		}, []string{"alpha start"}, nil, []failureWant{
			{"alpha", pluginkit.StepStart, false, "pluginkit: alpha start: pluginkit: inner start panicked: boom"},
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			var calls []string
			host := pluginkit.NewHost(tc.plugins(&calls)...)

			outcome := startEach(t.Context(), t, host)

			wantOutcome(t, outcome, tc.started, tc.failed)
			wantCalls(t, calls, tc.calls)
		})
	}
}

func TestStartEachKeepsEachCauseInItsFailure(t *testing.T) {
	t.Parallel()

	errBoot := errors.New("boot failed")
	errSchema := errors.New("schema exploded")
	var calls []string
	store := &memoryStore{versions: map[string]string{}, recordErr: errStoreOffline, calls: &calls}
	schema := versioned("alpha", "1", &calls)
	schema.migrateErr = errSchema
	host := storeHost(t, store, nil, schema, versioned("beta", "1", &calls),
		&fakePlugin{id: "gamma", startErr: errBoot, calls: &calls})

	outcome := startEach(t.Context(), t, host)

	want := []error{errSchema, errStoreOffline, errBoot}
	if len(outcome.Failed) != len(want) {
		t.Fatalf("failed = %+v, want %d failures", outcome.Failed, len(want))
	}
	for i, cause := range want {
		if !errors.Is(outcome.Failed[i].Err, cause) {
			t.Errorf("failure %d error = %v, want %v in its chain", i, outcome.Failed[i].Err, cause)
		}
	}
}

func TestStartEachWithAMigrationStore(t *testing.T) {
	t.Parallel()

	errUnreadable := errors.New("folder unreadable")
	errBoot := errors.New("boot failed")
	errSchema := errors.New("schema exploded")
	cases := []struct {
		name    string
		prepare func(store *memoryStore, alpha *versionedPlugin)
		calls   []string
		started []string
		failed  []failureWant
	}{
		{"a plugin the store has not seen", func(*memoryStore, *versionedPlugin) {},
			[]string{"versions", "alpha version", "alpha migrate", "record alpha 1", "alpha start"},
			[]string{"alpha"}, nil},
		{"a migration the store records as current", func(s *memoryStore, _ *versionedPlugin) {
			s.versions["alpha"] = "1"
		}, []string{"versions", "alpha version", "alpha start"}, []string{"alpha"}, nil},
		{"a failed record, whose plugin still starts", func(s *memoryStore, _ *versionedPlugin) {
			s.recordErr = errStoreOffline
		}, []string{"versions", "alpha version", "alpha migrate", "record alpha 1", "alpha start"},
			[]string{"alpha"}, []failureWant{
				{"alpha", pluginkit.StepRecord, false, "pluginkit: alpha record: store offline"},
			}},
		{"a panicking record, whose plugin still starts", func(s *memoryStore, _ *versionedPlugin) {
			s.recordPanic = true
		}, []string{"versions", "alpha version", "alpha migrate", "record alpha 1", "alpha start"},
			[]string{"alpha"}, []failureWant{
				{"alpha", pluginkit.StepRecord, true, "pluginkit: alpha record panicked: boom"},
			}},
		{"a failed record and then a failed start", func(s *memoryStore, p *versionedPlugin) {
			s.recordErr = errStoreOffline
			p.startErr = errBoot
		}, []string{"versions", "alpha version", "alpha migrate", "record alpha 1", "alpha start"},
			nil, []failureWant{
				{"alpha", pluginkit.StepRecord, false, "pluginkit: alpha record: store offline"},
				{"alpha", pluginkit.StepStart, false, "pluginkit: alpha start: boot failed"},
			}},
		{"a failed migration, which it does not record", func(_ *memoryStore, p *versionedPlugin) {
			p.migrateErr = errSchema
		}, []string{"versions", "alpha version", "alpha migrate"}, nil, []failureWant{
			{"alpha", pluginkit.StepMigrate, false, "pluginkit: alpha migrate: schema exploded"},
		}},
		{"an empty version", func(_ *memoryStore, p *versionedPlugin) { p.version = "" },
			[]string{"versions", "alpha version"}, nil, []failureWant{
				{"alpha", pluginkit.StepMigrate, false, "pluginkit: alpha migration version: the version is empty"},
			}},
		{"a version the plugin cannot tell", func(_ *memoryStore, p *versionedPlugin) { p.versionErr = errUnreadable },
			[]string{"versions", "alpha version"}, nil, []failureWant{
				{"alpha", pluginkit.StepMigrate, false, "pluginkit: alpha migration version: folder unreadable"},
			}},
		{"a version that panics", func(_ *memoryStore, p *versionedPlugin) { p.versionPanic = true },
			[]string{"versions", "alpha version"}, nil, []failureWant{
				{"alpha", pluginkit.StepMigrate, true, "pluginkit: alpha migration version panicked: boom"},
			}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			var calls []string
			store := &memoryStore{versions: map[string]string{}, calls: &calls}
			alpha := versioned("alpha", "1", &calls)
			tc.prepare(store, alpha)

			outcome := startEach(t.Context(), t, storeHost(t, store, nil, alpha))

			wantOutcome(t, outcome, tc.started, tc.failed)
			wantCalls(t, calls, tc.calls)
		})
	}
}

// noMapStore is a migration store whose read answers a nil map.
type noMapStore struct {
	memoryStore
}

// Versions answers a nil map, as a store with no records yet may.
func (s *noMapStore) Versions(ctx context.Context) (map[string]string, error) {
	_, err := s.memoryStore.Versions(ctx)
	return nil, err
}

func TestStartEachMigratesEveryPluginWhenTheStoreReadsNoMap(t *testing.T) {
	t.Parallel()

	var calls []string
	store := &noMapStore{memoryStore{versions: map[string]string{}, calls: &calls}}
	host, err := pluginkit.NewHostWithMigrationStore(store, nil, versioned("alpha", "1", &calls))
	if err != nil {
		t.Fatalf("NewHostWithMigrationStore() error = %v, want nil", err)
	}

	outcome := startEach(t.Context(), t, host)

	wantOutcome(t, outcome, []string{"alpha"}, nil)
	wantCalls(t, calls, []string{"versions", "alpha version", "alpha migrate", "record alpha 1", "alpha start"})
}

func TestStartEachUsesTheMigratorTheLookupGives(t *testing.T) {
	t.Parallel()

	var calls []string
	store := &memoryStore{versions: map[string]string{}, calls: &calls}
	host := storeHost(t, store, lookupFor("alpha", "4", &calls), plain("alpha", &calls), migrating("beta", &calls))

	outcome := startEach(t.Context(), t, host)

	wantOutcome(t, outcome, []string{"alpha", "beta"}, nil)
	wantCalls(t, calls, []string{
		"versions", "alpha schema migrate", "record alpha 4", "beta migrate", "alpha start", "beta start",
	})
}

func TestStartEachGoesOnPastABrokenLookup(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name     string
		lookup   pluginkit.MigratorOf
		panicked bool
		text     string
	}{
		{"a lookup that panics", func(pluginkit.Plugin) (pluginkit.Migrator, bool) { panic("boom") },
			true, "pluginkit: alpha migrator lookup panicked: boom"},
		{"a lookup that gives a nil migrator", func(pluginkit.Plugin) (pluginkit.Migrator, bool) { return nil, true },
			false, "pluginkit: alpha migrator lookup: the lookup gave a nil migrator"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			var calls []string
			store := &memoryStore{versions: map[string]string{}, calls: &calls}
			lookup := func(p pluginkit.Plugin) (pluginkit.Migrator, bool) {
				if p.ID() == "alpha" {
					return tc.lookup(p)
				}
				return nil, false
			}
			host := storeHost(t, store, lookup, versioned("alpha", "1", &calls), plain("beta", &calls))

			outcome := startEach(t.Context(), t, host)

			wantOutcome(t, outcome, []string{"beta"}, []failureWant{
				{"alpha", pluginkit.StepMigrate, tc.panicked, tc.text},
			})
			wantCalls(t, calls, []string{"versions", "beta start"})
		})
	}
}

func TestStartEachWithoutAStoreNeverAsksAVersion(t *testing.T) {
	t.Parallel()

	var calls []string
	host := pluginkit.NewHost(versioned("alpha", "1", &calls))

	outcome := startEach(t.Context(), t, host)

	wantOutcome(t, outcome, []string{"alpha"}, nil)
	wantCalls(t, calls, []string{"alpha migrate", "alpha start"})
}

func TestStartEachReturnsAStoreReadFailureAndCallsNoPlugin(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name  string
		store func(calls *[]string) *memoryStore
		text  string
		cause error
	}{
		{"a read that fails", func(c *[]string) *memoryStore {
			return &memoryStore{readErr: errStoreOffline, calls: c}
		}, "pluginkit: read the migration versions: store offline", errStoreOffline},
		{"a read that panics", func(c *[]string) *memoryStore {
			return &memoryStore{readPanic: true, calls: c}
		}, "pluginkit: read the migration versions panicked: boom", nil},
		{"a read the context ended", func(c *[]string) *memoryStore {
			return &memoryStore{readErr: context.Canceled, calls: c}
		}, "pluginkit: read the migration versions: context canceled", context.Canceled},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			var calls []string
			host := storeHost(t, tc.store(&calls), nil, versioned("alpha", "1", &calls), plain("beta", &calls))

			outcome, err := host.StartEach(t.Context())

			wantFailure(t, err, tc.text)
			if tc.cause != nil && !errors.Is(err, tc.cause) {
				t.Errorf("StartEach() error = %v, want %v in its chain", err, tc.cause)
			}
			wantOutcome(t, outcome, nil, nil)
			wantCalls(t, calls, []string{"versions"})
		})
	}
}

func TestHostStopAfterStartEachReachesEveryPlugin(t *testing.T) {
	t.Parallel()

	var calls []string
	unmigrated := migrating("gamma", &calls)
	unmigrated.migrateErr = errors.New("schema exploded")
	host := pluginkit.NewHost(
		plain("alpha", &calls),
		&fakePlugin{id: "beta", startErr: errors.New("boot failed"), calls: &calls},
		unmigrated,
	)

	startEach(t.Context(), t, host)
	wantCalls(t, calls, []string{"gamma migrate", "alpha start", "beta start"})

	if err := host.Stop(t.Context()); err != nil {
		t.Fatalf("Stop() error = %v, want nil", err)
	}
	wantCalls(t, calls, []string{
		"gamma migrate", "alpha start", "beta start", "gamma stop", "beta stop", "alpha stop",
	})
}

// slowPlugin is a plugin whose Migrate and Start each spend time.
type slowPlugin struct {
	fakePlugin
	migrateTakes time.Duration
	startTakes   time.Duration
}

// Migrate spends the time the plugin was given.
func (s *slowPlugin) Migrate(context.Context) error {
	time.Sleep(s.migrateTakes)
	return nil
}

// MigrationVersion returns a version no store has recorded.
func (s *slowPlugin) MigrationVersion() (string, error) {
	return "1", nil
}

// Start spends the time the plugin was given.
func (s *slowPlugin) Start(ctx context.Context) error {
	time.Sleep(s.startTakes)
	return s.fakePlugin.Start(ctx)
}

func TestStartEachReportsHowLongAStartTookAfterAFailedRecord(t *testing.T) {
	t.Parallel()

	synctest.Test(t, func(t *testing.T) {
		var calls []string
		store := &memoryStore{versions: map[string]string{}, recordErr: errStoreOffline, calls: &calls}
		slow := &slowPlugin{fakePlugin: fakePlugin{id: "alpha", calls: &calls}, startTakes: 3 * time.Minute}

		outcome := startEach(t.Context(), t, storeHost(t, store, nil, slow))

		want := []pluginkit.StartedPlugin{{ID: "alpha", Took: 3 * time.Minute}}
		if !slices.Equal(want, outcome.Started) {
			t.Errorf("started = %v, want %v", outcome.Started, want)
		}
	})
}

func TestStartEachReportsHowLongEachStartTook(t *testing.T) {
	t.Parallel()

	synctest.Test(t, func(t *testing.T) {
		var calls []string
		host := pluginkit.NewHost(
			&slowPlugin{
				fakePlugin:   fakePlugin{id: "alpha", calls: &calls},
				migrateTakes: 2 * time.Minute,
				startTakes:   3 * time.Minute,
			},
			&slowPlugin{
				fakePlugin: fakePlugin{id: "beta", startErr: errors.New("boot failed"), calls: &calls},
				startTakes: 7 * time.Minute,
			},
			&slowPlugin{fakePlugin: fakePlugin{id: "gamma", calls: &calls}, startTakes: 5 * time.Minute},
			plain("delta", &calls),
		)

		outcome := startEach(t.Context(), t, host)

		want := []pluginkit.StartedPlugin{
			{ID: "alpha", Took: 3 * time.Minute},
			{ID: "gamma", Took: 5 * time.Minute},
			{ID: "delta", Took: 0},
		}
		if !slices.Equal(want, outcome.Started) {
			t.Errorf("started = %v, want %v", outcome.Started, want)
		}
	})
}
