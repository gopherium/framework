// SPDX-License-Identifier: Apache-2.0

package pluginkit_test

import (
	"context"
	"errors"
	"testing"

	"github.com/gopherium/framework/pluginkit"
	"github.com/gopherium/framework/pluginkit/pluginstoretest"
)

// countingStore is a plugin store in memory that notes each read and write, and fails them when told.
type countingStore struct {
	inner     pluginkit.PluginStore
	reads     int
	writes    []string
	readErr   error
	readPanic any
	writeErr  error
}

// Records notes the read and answers the records, or the failure it was told.
func (s *countingStore) Records(ctx context.Context) (map[string]pluginkit.PluginRecord, error) {
	s.reads++
	if s.readPanic != nil {
		panic(s.readPanic)
	}
	if s.readErr != nil {
		return nil, s.readErr
	}
	return s.inner.Records(ctx)
}

// Activate notes the write and activates id, or answers the failure it was told.
func (s *countingStore) Activate(ctx context.Context, id string) error {
	s.writes = append(s.writes, "activate "+id)
	if s.writeErr != nil {
		return s.writeErr
	}
	return s.inner.Activate(ctx, id)
}

// Pause notes the write and pauses id, or answers the failure it was told.
func (s *countingStore) Pause(ctx context.Context, id, failure string) error {
	s.writes = append(s.writes, "pause "+id+": "+failure)
	if s.writeErr != nil {
		return s.writeErr
	}
	return s.inner.Pause(ctx, id, failure)
}

// assertErrorFree stops t when err is set.
func assertErrorFree(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatalf("error = %v, want nil", err)
	}
}

// assertErrorText checks that err has exactly the text want.
func assertErrorText(t *testing.T, err error, want string) {
	t.Helper()
	if err == nil || err.Error() != want {
		t.Errorf("error = %v, want %q", err, want)
	}
}

// storeOf returns a counting store holding records.
func storeOf(records ...pluginkit.PluginRecord) *countingStore {
	return &countingStore{inner: pluginstoretest.NewMemory(records...)}
}

// record returns the record of id in state, with no error.
func record(id string, state pluginkit.PluginState) pluginkit.PluginRecord {
	return pluginkit.PluginRecord{ID: id, State: state}
}

// newBoot reads store into a new boot, stopping t when it cannot.
func newBoot(t *testing.T, store pluginkit.PluginStore) *pluginkit.Boot {
	t.Helper()
	boot, err := pluginkit.NewBoot(t.Context(), store)
	if err != nil {
		t.Fatalf("NewBoot() error = %v, want nil", err)
	}
	return boot
}

// held returns every record store holds.
func held(t *testing.T, store *countingStore) map[string]pluginkit.PluginRecord {
	t.Helper()
	records, err := store.inner.Records(t.Context())
	if err != nil {
		t.Fatalf("Records() error = %v, want nil", err)
	}
	return records
}

// registerAll registers entries on boot, stopping t when the boot fails.
func registerAll(t *testing.T, boot *pluginkit.Boot,
	entries ...pluginkit.Registration[listedDeps, *listedPlugin]) pluginkit.RegisterOutcome[*listedPlugin] {
	t.Helper()
	var calls []string
	outcome, err := pluginkit.Register(t.Context(), boot, entries, depsOf(&calls))
	if err != nil {
		t.Fatalf("Register() error = %v, want nil", err)
	}
	return outcome
}

func TestNewBootReadsTheRecordsOnceForAWholeBoot(t *testing.T) {
	t.Parallel()

	var calls []string
	store := storeOf(record("events", pluginkit.PluginActive))
	boot := newBoot(t, store)
	registerAll(t, boot, entry("events", &calls, succeeds))

	err := boot.Started(t.Context(), pluginkit.StartOutcome{Started: []pluginkit.StartedPlugin{{ID: "events"}}})

	assertErrorFree(t, err)
	wantEqual(t, "reads", store.reads, 1)
}

func TestNewBootNamesAStoreThatCannotRead(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		store *countingStore
		want  string
	}{
		"an error": {&countingStore{readErr: errors.New("connection dropped")},
			"pluginkit: read the plugin records: connection dropped"},
		"a panic": {&countingStore{readPanic: "boom"}, "pluginkit: read the plugin records panicked: boom"},
	}
	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			boot, err := pluginkit.NewBoot(t.Context(), tc.store)

			assertErrorText(t, err, tc.want)
			if boot != nil {
				t.Errorf("NewBoot() = %v, want no boot", boot)
			}
		})
	}
}

func TestRegisterRecordsAPluginItHasNeverSeenAsActive(t *testing.T) {
	t.Parallel()

	var calls []string
	store := storeOf()

	outcome := registerAll(t, newBoot(t, store), entry("events", &calls, succeeds), entry("feed", &calls, succeeds))

	wantEqual(t, "registered", registeredIDs(outcome), []string{"events", "feed"})
	wantEqual(t, "writes", store.writes, []string{"activate events", "activate feed"})
	wantEqual(t, "records", held(t, store), map[string]pluginkit.PluginRecord{
		"events": record("events", pluginkit.PluginActive), "feed": record("feed", pluginkit.PluginActive),
	})
}

func TestRegisterLeavesOutInactiveAndNotInstalledPlugins(t *testing.T) {
	t.Parallel()

	var calls []string
	store := storeOf(record("events", pluginkit.PluginActive), record("feed", pluginkit.PluginInactive),
		record("gallery", pluginkit.PluginNotInstalled))

	outcome := registerAll(t, newBoot(t, store),
		entry("events", &calls, succeeds), entry("feed", &calls, succeeds), entry("gallery", &calls, succeeds))

	wantEqual(t, "registered", registeredIDs(outcome), []string{"events"})
	wantEqual(t, "calls", calls, []string{"register events"})
	wantEqual(t, "writes", store.writes, []string(nil))
}

func TestRegisterTriesAPausedPluginAgainWithoutWriting(t *testing.T) {
	t.Parallel()

	var calls []string
	store := storeOf(record("events", pluginkit.PluginActive),
		pluginkit.PluginRecord{ID: "feed", State: pluginkit.PluginPaused, LastError: "a setting is missing"})

	outcome := registerAll(t, newBoot(t, store), entry("events", &calls, succeeds), entry("feed", &calls, succeeds))

	wantEqual(t, "registered", registeredIDs(outcome), []string{"events", "feed"})
	wantEqual(t, "writes", store.writes, []string(nil))
}

func TestRegisterPausesEachPluginThatFailsToRegister(t *testing.T) {
	t.Parallel()

	var calls []string
	store := storeOf(record("events", pluginkit.PluginActive), record("feed", pluginkit.PluginActive),
		record("gallery", pluginkit.PluginActive))

	outcome := registerAll(t, newBoot(t, store), entry("events", &calls, succeeds),
		entry("feed", &calls, answers(errors.New("the title is too long"))), entry("gallery", &calls, panicsWith("boom")))

	wantEqual(t, "registered", registeredIDs(outcome), []string{"events"})
	wantEqual(t, "writes", store.writes, []string{
		"pause feed: pluginkit: feed register: the title is too long",
		"pause gallery: pluginkit: gallery register panicked: boom",
	})
}

func TestRegisterNamesAStoreThatCannotWrite(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		store   *countingStore
		answer  func() error
		want    string
		calling []string
	}{
		"recording a new plugin": {storeOf(), succeeds, "pluginkit: activate feed: connection dropped", nil},
		"pausing a failed plugin": {storeOf(record("feed", pluginkit.PluginActive)),
			answers(errors.New("the title is too long")), "pluginkit: pause feed: connection dropped",
			[]string{"deps feed", "register feed"}},
	}
	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			var calls []string
			boot := newBoot(t, tc.store)
			tc.store.writeErr = errors.New("connection dropped")

			_, err := pluginkit.Register(t.Context(), boot,
				[]pluginkit.Registration[listedDeps, *listedPlugin]{entry("feed", &calls, tc.answer)}, depsOf(&calls))

			assertErrorText(t, err, tc.want)
			wantEqual(t, "calls", calls, tc.calling)
		})
	}
}

func TestBootPausePausesAPluginForTheApplicationsReason(t *testing.T) {
	t.Parallel()

	store := storeOf(record("forum", pluginkit.PluginActive))

	err := newBoot(t, store).Pause(t.Context(), "forum", errors.New("forum declares a role it cannot accept"))

	assertErrorFree(t, err)
	wantEqual(t, "records", held(t, store), map[string]pluginkit.PluginRecord{"forum": {
		ID: "forum", State: pluginkit.PluginPaused, LastError: "forum declares a role it cannot accept",
	}})
}

func TestStartedPausesEachPluginThatFailedToMigrateOrStart(t *testing.T) {
	t.Parallel()

	store := storeOf(record("feed", pluginkit.PluginActive), record("gallery", pluginkit.PluginActive))

	err := newBoot(t, store).Started(t.Context(), pluginkit.StartOutcome{Failed: []pluginkit.Failure{
		{ID: "feed", Step: pluginkit.StepMigrate, Err: errors.New("pluginkit: feed migrate: schema exploded")},
		{ID: "gallery", Step: pluginkit.StepStart, Err: errors.New("pluginkit: gallery start: no route table")},
	}})

	assertErrorFree(t, err)
	wantEqual(t, "writes", store.writes, []string{
		"pause feed: pluginkit: feed migrate: schema exploded",
		"pause gallery: pluginkit: gallery start: no route table",
	})
}

func TestStartedLeavesAFailedMigrationRecordAlone(t *testing.T) {
	t.Parallel()

	store := storeOf(record("feed", pluginkit.PluginActive), record("gallery", pluginkit.PluginActive))

	err := newBoot(t, store).Started(t.Context(), pluginkit.StartOutcome{
		Started: []pluginkit.StartedPlugin{{ID: "feed"}},
		Failed: []pluginkit.Failure{
			{ID: "feed", Step: pluginkit.StepRecord, Err: errors.New("pluginkit: feed record: ledger locked")},
			{ID: "gallery", Step: pluginkit.StepStart, Err: errors.New("pluginkit: gallery start: no route table")},
		},
	})

	assertErrorFree(t, err)
	wantEqual(t, "writes", store.writes, []string{"pause gallery: pluginkit: gallery start: no route table"})
}

func TestStartedActivatesAPausedPluginThatStarted(t *testing.T) {
	t.Parallel()

	store := storeOf(record("events", pluginkit.PluginActive),
		pluginkit.PluginRecord{ID: "feed", State: pluginkit.PluginPaused, LastError: "a setting is missing"})

	err := newBoot(t, store).Started(t.Context(), pluginkit.StartOutcome{
		Started: []pluginkit.StartedPlugin{{ID: "events"}, {ID: "feed"}},
	})

	assertErrorFree(t, err)
	wantEqual(t, "writes", store.writes, []string{"activate feed"})
	wantEqual(t, "feed", held(t, store)["feed"], record("feed", pluginkit.PluginActive))
}

func TestStartedWritesNothingWhenTheContextEnded(t *testing.T) {
	t.Parallel()

	store := storeOf(record("feed", pluginkit.PluginActive))
	boot := newBoot(t, store)
	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	err := boot.Started(ctx, pluginkit.StartOutcome{Failed: []pluginkit.Failure{
		{ID: "feed", Step: pluginkit.StepMigrate, Err: ctx.Err()},
	}})

	assertErrorText(t, err, "pluginkit: the boot's context ended: context canceled")
	if !errors.Is(err, context.Canceled) {
		t.Errorf("Started() error = %v, want it to wrap the context's error", err)
	}
	wantEqual(t, "writes", store.writes, []string(nil))
}

func TestStartedNamesAStoreThatCannotWrite(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		store   *countingStore
		outcome pluginkit.StartOutcome
		want    string
	}{
		"a pause": {storeOf(record("feed", pluginkit.PluginActive)), pluginkit.StartOutcome{Failed: []pluginkit.Failure{
			{ID: "feed", Step: pluginkit.StepStart, Err: errors.New("pluginkit: feed start: no route table")},
		}}, "pluginkit: pause feed: connection dropped"},
		"an activation": {storeOf(pluginkit.PluginRecord{ID: "feed", State: pluginkit.PluginPaused}),
			pluginkit.StartOutcome{Started: []pluginkit.StartedPlugin{{ID: "feed"}}},
			"pluginkit: activate feed: connection dropped"},
	}
	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			boot := newBoot(t, tc.store)
			tc.store.writeErr = errors.New("connection dropped")

			err := boot.Started(t.Context(), tc.outcome)

			assertErrorText(t, err, tc.want)
		})
	}
}

func TestABootWhereNothingChangesWritesNothing(t *testing.T) {
	t.Parallel()

	var calls []string
	store := storeOf(record("events", pluginkit.PluginActive), record("feed", pluginkit.PluginActive))
	boot := newBoot(t, store)
	registerAll(t, boot, entry("events", &calls, succeeds), entry("feed", &calls, succeeds))

	err := boot.Started(t.Context(), pluginkit.StartOutcome{
		Started: []pluginkit.StartedPlugin{{ID: "events"}, {ID: "feed"}},
	})

	assertErrorFree(t, err)
	wantEqual(t, "writes", store.writes, []string(nil))
}
