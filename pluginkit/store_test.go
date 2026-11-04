// SPDX-License-Identifier: Apache-2.0

package pluginkit_test

import (
	"context"
	"errors"
	"maps"
	"slices"
	"testing"

	"github.com/gopherium/framework/pluginkit"
)

var (
	_ pluginkit.MigrationStore     = (*memoryStore)(nil)
	_ pluginkit.MigrationVersioner = (*versionedPlugin)(nil)
	_ pluginkit.MigrationVersioner = (*versionOnlyPlugin)(nil)
	_ interface {
		Migrate(context.Context) error
		Seed(context.Context) error
		Stop(context.Context) error
	} = (*pluginkit.Host)(nil)
)

// errStoreOffline is the failure a store answers when it cannot be reached.
var errStoreOffline = errors.New("store offline")

// memoryStore is a migration store held in a map, adding each call it gets to calls.
type memoryStore struct {
	versions    map[string]string
	readErr     error
	readPanic   bool
	recordErr   error
	recordPanic bool
	calls       *[]string
}

// Versions returns a copy of the recorded versions.
func (s *memoryStore) Versions(context.Context) (map[string]string, error) {
	*s.calls = append(*s.calls, "versions")
	if s.readPanic {
		panic("boom")
	}
	return maps.Clone(s.versions), s.readErr
}

// Record stores version for id.
func (s *memoryStore) Record(_ context.Context, id, version string) error {
	*s.calls = append(*s.calls, "record "+id+" "+version)
	if s.recordPanic {
		panic("boom")
	}
	if s.recordErr != nil {
		return s.recordErr
	}
	s.versions[id] = version
	return nil
}

// versionedPlugin is a migrating plugin that reports the version of its schema.
type versionedPlugin struct {
	migratingPlugin
	version      string
	versionErr   error
	versionPanic bool
}

// MigrationVersion returns the version the plugin was given.
func (v *versionedPlugin) MigrationVersion() (string, error) {
	*v.calls = append(*v.calls, v.id+" version")
	if v.versionPanic {
		panic("boom")
	}
	return v.version, v.versionErr
}

// versionOnlyPlugin reports a migration version but owns no schema.
type versionOnlyPlugin struct {
	fakePlugin
}

// MigrationVersion returns a version for a schema the plugin does not have.
func (v *versionOnlyPlugin) MigrationVersion() (string, error) {
	*v.calls = append(*v.calls, v.id+" version")
	return "1", nil
}

// schemaMigrator applies a plugin's schema on its behalf and reports its version.
type schemaMigrator struct {
	id      string
	version string
	calls   *[]string
}

// Migrate records that the schema of the plugin it stands for was applied.
func (s *schemaMigrator) Migrate(context.Context) error {
	*s.calls = append(*s.calls, s.id+" schema migrate")
	return nil
}

// MigrationVersion returns the version the migrator was given.
func (s *schemaMigrator) MigrationVersion() (string, error) {
	return s.version, nil
}

// versioned returns a migrating plugin with id whose schema reports version.
func versioned(id, version string, calls *[]string) *versionedPlugin {
	return &versionedPlugin{migratingPlugin: *migrating(id, calls), version: version}
}

// migrating returns a migrating plugin with id that reports no version.
func migrating(id string, calls *[]string) *migratingPlugin {
	return &migratingPlugin{fakePlugin: fakePlugin{id: id, calls: calls}}
}

// lookupFor returns a lookup that gives a schema migrator at version for the plugin id and nothing for any other.
func lookupFor(id, version string, calls *[]string) pluginkit.MigratorOf {
	return func(p pluginkit.Plugin) (pluginkit.Migrator, bool) {
		if p.ID() != id {
			return nil, false
		}
		return &schemaMigrator{id: id, version: version, calls: calls}, true
	}
}

// storeHost returns a host over plugins whose migrations store keeps and whose migrators lookup gives.
func storeHost(
	t *testing.T, store *memoryStore, lookup pluginkit.MigratorOf, plugins ...pluginkit.Plugin,
) *pluginkit.Host {
	t.Helper()
	host, err := pluginkit.NewHostWithMigrationStore(store, lookup, plugins...)
	if err != nil {
		t.Fatalf("NewHostWithMigrationStore() error = %v, want nil", err)
	}
	return host
}

// migrateTimes runs the migrations of host boots times and fails t on the first error.
func migrateTimes(t *testing.T, host *pluginkit.Host, boots int) {
	t.Helper()
	for range boots {
		if err := host.Migrate(t.Context()); err != nil {
			t.Fatalf("Migrate() error = %v, want nil", err)
		}
	}
}

// wantCalls fails t unless calls is want.
func wantCalls(t *testing.T, calls, want []string) {
	t.Helper()
	if !slices.Equal(want, calls) {
		t.Errorf("calls = %v, want %v", calls, want)
	}
}

// wantFailure fails t unless err has the text want.
func wantFailure(t *testing.T, err error, want string) {
	t.Helper()
	if err == nil || err.Error() != want {
		t.Errorf("error = %v, want %q", err, want)
	}
}

func TestNewHostWithMigrationStoreRefusesANilStore(t *testing.T) {
	t.Parallel()

	var calls []string
	host, err := pluginkit.NewHostWithMigrationStore(nil, nil, &fakePlugin{id: "alpha", calls: &calls})

	wantFailure(t, err, "pluginkit: the migration store is nil")
	if host != nil {
		t.Error("NewHostWithMigrationStore() returned a host for a nil store, want nil")
	}
}

func TestNewHostWithMigrationStoreRefusesDuplicateIDs(t *testing.T) {
	t.Parallel()

	var calls []string
	store := &memoryStore{calls: &calls}
	host, err := pluginkit.NewHostWithMigrationStore(store, nil,
		&fakePlugin{id: "feed", calls: &calls}, &fakePlugin{id: "feed", calls: &calls})

	wantFailure(t, err, `pluginkit: duplicate id "feed"`)
	if host != nil {
		t.Error("NewHostWithMigrationStore() returned a host for two plugins with one id, want nil")
	}
}

func TestNewHostPanicsWithTheDuplicateIDText(t *testing.T) {
	t.Parallel()

	defer func() {
		if recovered := recover(); recovered != `pluginkit: duplicate id "feed"` {
			t.Errorf("NewHost() panicked with %v, want the duplicate id text", recovered)
		}
	}()

	var calls []string
	pluginkit.NewHost(&fakePlugin{id: "feed", calls: &calls}, &fakePlugin{id: "feed", calls: &calls})
}

func TestHostWithMigrationStoreMigratesOnlyWhatIsNotCurrent(t *testing.T) {
	t.Parallel()

	one := func(plugin func(c *[]string) pluginkit.Plugin) func(c *[]string) []pluginkit.Plugin {
		return func(c *[]string) []pluginkit.Plugin { return []pluginkit.Plugin{plugin(c)} }
	}
	alphaAt := func(version string) func(c *[]string) pluginkit.Plugin {
		return func(c *[]string) pluginkit.Plugin { return versioned("alpha", version, c) }
	}
	alphaMigrating := func(c *[]string) pluginkit.Plugin { return migrating("alpha", c) }
	alphaPlain := func(c *[]string) pluginkit.Plugin { return &fakePlugin{id: "alpha", calls: c} }
	alphaVersionOnly := func(c *[]string) pluginkit.Plugin {
		return &versionOnlyPlugin{fakePlugin: fakePlugin{id: "alpha", calls: c}}
	}
	cases := []struct {
		name     string
		recorded map[string]string
		lookupID string
		plugins  func(calls *[]string) []pluginkit.Plugin
		boots    int
		want     []string
	}{
		{"plugins the store has not seen, with one read of the store", map[string]string{}, "",
			func(c *[]string) []pluginkit.Plugin {
				return []pluginkit.Plugin{versioned("alpha", "1", c), versioned("beta", "1", c)}
			}, 1, []string{
				"versions", "alpha version", "alpha migrate", "record alpha 1",
				"beta version", "beta migrate", "record beta 1",
			}},
		{"a plugin whose recorded version is current", map[string]string{"alpha": "3"}, "",
			one(alphaAt("3")), 1, []string{"versions", "alpha version"}},
		{"a plugin whose version changed", map[string]string{"alpha": "2"}, "",
			one(alphaAt("3")), 1, []string{"versions", "alpha version", "alpha migrate", "record alpha 3"}},
		{"a recorded version no plugin holds", map[string]string{"gone": "9"}, "",
			one(alphaAt("1")), 1, []string{"versions", "alpha version", "alpha migrate", "record alpha 1"}},
		{"the boot after it recorded", map[string]string{}, "", one(alphaAt("1")), 2, []string{
			"versions", "alpha version", "alpha migrate", "record alpha 1", "versions", "alpha version",
		}},
		{"a migrator with no version, on every boot", map[string]string{}, "",
			one(alphaMigrating), 2, []string{"versions", "alpha migrate", "versions", "alpha migrate"}},
		{"a plugin that is no migrator", map[string]string{}, "", one(alphaPlain), 1, []string{"versions"}},
		{"a version from a plugin that is no migrator", map[string]string{}, "",
			one(alphaVersionOnly), 1, []string{"versions"}},
		{"the lookup's migrator before the plugin's own", map[string]string{}, "alpha",
			one(alphaMigrating), 1, []string{"versions", "alpha schema migrate", "record alpha 4"}},
		{"a lookup's migrator whose version is current", map[string]string{"alpha": "4"}, "alpha",
			one(alphaPlain), 1, []string{"versions"}},
		{"the plugin's own Migrate when the lookup has none", map[string]string{}, "other",
			one(alphaMigrating), 1, []string{"versions", "alpha migrate"}},
		{"a plugin neither the lookup nor itself gives a migrator", map[string]string{}, "other",
			one(alphaPlain), 1, []string{"versions"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			var calls []string
			store := &memoryStore{versions: tc.recorded, calls: &calls}
			host := storeHost(t, store, lookupFor(tc.lookupID, "4", &calls), tc.plugins(&calls)...)

			migrateTimes(t, host, tc.boots)

			wantCalls(t, calls, tc.want)
		})
	}
}

func TestHostWithMigrationStoreStopsAtAFailureNamingThePlugin(t *testing.T) {
	t.Parallel()

	errUnreadable := errors.New("migrations folder unreadable")
	errSchema := errors.New("schema exploded")
	asked := []string{"versions", "alpha version"}
	migrated := []string{"versions", "alpha version", "alpha migrate"}
	recorded := []string{"versions", "alpha version", "alpha migrate", "record alpha 1"}
	cases := []struct {
		name  string
		fail  func(store *memoryStore, plugin *versionedPlugin)
		err   string
		cause error
		calls []string
	}{
		{"an empty version", func(_ *memoryStore, p *versionedPlugin) { p.version = "" },
			"pluginkit: alpha migration version: the version is empty", nil, asked},
		{"a version the plugin cannot tell",
			func(_ *memoryStore, p *versionedPlugin) { p.version, p.versionErr = "", errUnreadable },
			"pluginkit: alpha migration version: migrations folder unreadable", errUnreadable, asked},
		{"a version that panics", func(_ *memoryStore, p *versionedPlugin) { p.versionPanic = true },
			"pluginkit: alpha migration version panicked: boom", nil, asked},
		{"a failed migration", func(_ *memoryStore, p *versionedPlugin) { p.migrateErr = errSchema },
			"pluginkit: alpha migrate: schema exploded", errSchema, migrated},
		{"a panicking migration", func(_ *memoryStore, p *versionedPlugin) { p.migratePanic = true },
			"pluginkit: alpha migrate panicked: boom", nil, migrated},
		{"a failed record", func(s *memoryStore, _ *versionedPlugin) { s.recordErr = errStoreOffline },
			"pluginkit: alpha record: store offline", errStoreOffline, recorded},
		{"a panicking record", func(s *memoryStore, _ *versionedPlugin) { s.recordPanic = true },
			"pluginkit: alpha record panicked: boom", nil, recorded},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			var calls []string
			store := &memoryStore{versions: map[string]string{}, calls: &calls}
			lead := versioned("alpha", "1", &calls)
			tc.fail(store, lead)
			host := storeHost(t, store, nil, lead, versioned("beta", "1", &calls))

			err := host.Migrate(t.Context())

			wantFailure(t, err, tc.err)
			if tc.cause != nil && !errors.Is(err, tc.cause) {
				t.Errorf("Migrate() error = %v, want %v in its chain", err, tc.cause)
			}
			wantCalls(t, calls, tc.calls)
		})
	}
}

func TestHostWithMigrationStoreReportsAStoreReadFailureBeforeAnyMigration(t *testing.T) {
	t.Parallel()

	var calls []string
	store := &memoryStore{readErr: errStoreOffline, calls: &calls}
	host := storeHost(t, store, nil, versioned("alpha", "1", &calls), migrating("beta", &calls))

	err := host.Migrate(t.Context())

	wantFailure(t, err, "pluginkit: read the migration versions: store offline")
	if !errors.Is(err, errStoreOffline) {
		t.Errorf("Migrate() error = %v, want %v in its chain", err, errStoreOffline)
	}
	wantCalls(t, calls, []string{"versions"})
}

func TestHostWithMigrationStoreRecoversAStoreReadPanic(t *testing.T) {
	t.Parallel()

	var calls []string
	store := &memoryStore{readPanic: true, calls: &calls}
	host := storeHost(t, store, nil, versioned("alpha", "1", &calls))

	err := host.Migrate(t.Context())

	wantFailure(t, err, "pluginkit: read the migration versions panicked: boom")
	wantCalls(t, calls, []string{"versions"})
}

func TestHostWithMigrationStoreRecoversALookupPanicNamingThePlugin(t *testing.T) {
	t.Parallel()

	var calls []string
	store := &memoryStore{versions: map[string]string{}, calls: &calls}
	lookup := func(pluginkit.Plugin) (pluginkit.Migrator, bool) { panic("boom") }
	host := storeHost(t, store, lookup, versioned("alpha", "1", &calls), versioned("beta", "1", &calls))

	err := host.Migrate(t.Context())

	wantFailure(t, err, "pluginkit: alpha migrator lookup panicked: boom")
	wantCalls(t, calls, []string{"versions"})
}

func TestHostWithMigrationStoreRefusesANilMigratorNamingThePlugin(t *testing.T) {
	t.Parallel()

	var calls []string
	store := &memoryStore{versions: map[string]string{}, calls: &calls}
	lookup := func(pluginkit.Plugin) (pluginkit.Migrator, bool) { return nil, true }
	host := storeHost(t, store, lookup, plain("alpha", &calls))

	err := host.Migrate(t.Context())

	wantFailure(t, err, "pluginkit: alpha migrator lookup: the lookup gave a nil migrator")
	wantCalls(t, calls, []string{"versions"})
}

// contextStore is a migration store that adds the caller value of each context it gets to seen.
type contextStore struct {
	seen *[]any
}

// Versions records the caller value of ctx and returns no versions.
func (s *contextStore) Versions(ctx context.Context) (map[string]string, error) {
	*s.seen = append(*s.seen, ctx.Value(callerKey{}))
	return map[string]string{}, nil
}

// Record records the caller value of ctx.
func (s *contextStore) Record(ctx context.Context, _, _ string) error {
	*s.seen = append(*s.seen, ctx.Value(callerKey{}))
	return nil
}

// versionedContextMigrator is a context migrator whose schema reports a version.
type versionedContextMigrator struct {
	contextMigrator
}

// MigrationVersion returns a version no store has recorded.
func (v *versionedContextMigrator) MigrationVersion() (string, error) {
	return "1", nil
}

func TestHostWithMigrationStoreHandsTheCallerContextToTheStoreAndEveryMigrator(t *testing.T) {
	t.Parallel()

	var calls []string
	var seen []any
	host, err := pluginkit.NewHostWithMigrationStore(&contextStore{seen: &seen}, nil,
		&versionedContextMigrator{contextMigrator{fakePlugin: fakePlugin{id: "alpha", calls: &calls}, seen: &seen}},
		&contextMigrator{fakePlugin: fakePlugin{id: "beta", calls: &calls}, seen: &seen})
	if err != nil {
		t.Fatalf("NewHostWithMigrationStore() error = %v, want nil", err)
	}
	ctx := context.WithValue(t.Context(), callerKey{}, "caller")

	if err := host.Migrate(ctx); err != nil {
		t.Fatalf("Migrate() error = %v, want nil", err)
	}

	if want := []any{"caller", "caller", "caller", "caller"}; !slices.Equal(want, seen) {
		t.Errorf("context values seen = %v, want %v from the read, both migrations and the record", seen, want)
	}
}

func TestHostWithMigrationStoreStartSkipsCurrentMigrations(t *testing.T) {
	t.Parallel()

	var calls []string
	store := &memoryStore{versions: map[string]string{"alpha": "2"}, calls: &calls}
	host := storeHost(t, store, nil, versioned("alpha", "2", &calls), &fakePlugin{id: "beta", calls: &calls})

	if err := host.Start(t.Context(), stopGrace); err != nil {
		t.Fatalf("Start() error = %v, want nil", err)
	}

	wantCalls(t, calls, []string{"versions", "alpha version", "alpha start", "beta start"})
}

func TestHostWithoutAStoreNeverAsksAVersion(t *testing.T) {
	t.Parallel()

	var calls []string
	host := pluginkit.NewHost(versioned("alpha", "1", &calls), &fakePlugin{id: "beta", calls: &calls})

	if err := host.Migrate(t.Context()); err != nil {
		t.Fatalf("Migrate() error = %v, want nil", err)
	}

	wantCalls(t, calls, []string{"alpha migrate"})
}
