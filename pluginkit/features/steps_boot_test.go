// SPDX-License-Identifier: Apache-2.0

package features_test

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"testing"

	"github.com/cucumber/godog"

	"github.com/gopherium/framework/pluginkit"
	"github.com/gopherium/framework/pluginkit/pluginstoretest"
)

// bootPlugin is a compiled plugin of a boot scenario, failing where the scenario says.
type bootPlugin struct {
	id    string
	world *bootWorld
}

// ID returns the plugin's id.
func (p *bootPlugin) ID() string {
	return p.id
}

// Migrate applies the plugin's schema, or fails as the scenario says.
func (p *bootPlugin) Migrate(context.Context) error {
	return p.world.failure(p.world.migrateFails, p.id)
}

// MigrationVersion reports the one version every scenario plugin ships.
func (p *bootPlugin) MigrationVersion() (string, error) {
	return "1", nil
}

// Start notes the call, ends the boot's context or fails as the scenario says.
func (p *bootPlugin) Start(context.Context) error {
	p.world.startCalls = append(p.world.startCalls, p.id)
	if p.world.endsOn["starts"] == p.id {
		p.world.cancel()
	}
	return p.world.failure(p.world.startFails, p.id)
}

// Stop releases nothing.
func (p *bootPlugin) Stop(context.Context) error {
	return nil
}

// bootStore is the scenario's plugin store, noting each write and failing as the scenario says.
type bootStore struct {
	inner    pluginkit.PluginStore
	readErr  error
	writeErr error
	writes   int
}

// Records answers the records, or the failure the scenario set.
func (s *bootStore) Records(ctx context.Context) (map[string]pluginkit.PluginRecord, error) {
	if s.readErr != nil {
		return nil, s.readErr
	}
	return s.inner.Records(ctx)
}

// Activate notes the write and activates id, or answers the failure the scenario set.
func (s *bootStore) Activate(ctx context.Context, id string) error {
	s.writes++
	if s.writeErr != nil {
		return s.writeErr
	}
	return s.inner.Activate(ctx, id)
}

// Pause notes the write and pauses id, or answers the failure the scenario set.
func (s *bootStore) Pause(ctx context.Context, id, failure string) error {
	s.writes++
	if s.writeErr != nil {
		return s.writeErr
	}
	return s.inner.Pause(ctx, id, failure)
}

// bootMigrations is the scenario's migration store, failing the record of the plugins the scenario names.
type bootMigrations struct {
	world *bootWorld
}

// Versions answers that no plugin has recorded a version yet.
func (m bootMigrations) Versions(context.Context) (map[string]string, error) {
	return map[string]string{}, nil
}

// Record keeps the version, or fails as the scenario says.
func (m bootMigrations) Record(_ context.Context, id, _ string) error {
	return m.world.failure(m.world.recordFails, id)
}

// bootWorld is what one boot scenario builds and finds.
type bootWorld struct {
	t            *testing.T
	compiled     []string
	seeds        map[string]pluginkit.PluginRecord
	registerFail map[string]string
	registerBoom map[string]string
	calledAs     map[string]string
	migrateFails map[string]string
	startFails   map[string]string
	recordFails  map[string]string
	rejected     map[string]bool
	endsOn       map[string]string
	readErr      error
	writeErr     error
	cancel       context.CancelFunc
	store        *bootStore
	booted       bool
	registered   []string
	startCalls   []string
	started      []string
	err          error
}

// failure returns the error a scenario set for id in failures, or nil.
func (w *bootWorld) failure(failures map[string]string, id string) error {
	if text, ok := failures[id]; ok {
		return errors.New(text)
	}
	return nil
}

// compile lists the plugins of list as compiled.
func (w *bootWorld) compile(list string) {
	w.compiled = names(list)
}

// noRecord leaves the store empty.
func (w *bootWorld) noRecord() {}

// seed records each plugin of list in state before the boot.
func (w *bootWorld) seed(list, state string) {
	for _, id := range names(list) {
		w.seeds[id] = pluginkit.PluginRecord{ID: id, State: pluginkit.PluginState(state)}
	}
}

// pausedWith seeds the plugin id as paused with text before the boot, and checks that record after it.
func (w *bootWorld) pausedWith(id, text string) error {
	want := pluginkit.PluginRecord{ID: id, State: pluginkit.PluginPaused, LastError: text}
	if !w.booted {
		w.seeds[id] = want
		return nil
	}
	return w.holds(want)
}

// set stores text under id in failures.
func set(failures map[string]string, id, text string) {
	failures[id] = text
}

// rejectRole makes the application pause the plugin id for a role it cannot accept.
func (w *bootWorld) rejectRole(id string) {
	w.rejected[id] = true
}

// readFails makes the store fail every read with text.
func (w *bootWorld) readFails(text string) {
	w.readErr = errors.New(text)
}

// writeFails makes the store fail every write with text.
func (w *bootWorld) writeFails(text string) {
	w.writeErr = errors.New(text)
}

// entries returns the registration of each compiled plugin.
func (w *bootWorld) entries() []pluginkit.Registration[string, *bootPlugin] {
	entries := make([]pluginkit.Registration[string, *bootPlugin], 0, len(w.compiled))
	for _, id := range w.compiled {
		entries = append(entries, pluginkit.Registration[string, *bootPlugin]{ID: id, Register: w.registerOf(id)})
	}
	return entries
}

// registerOf returns the Register of the plugin id, noting the call and failing as the scenario says.
func (w *bootWorld) registerOf(id string) func(string) (*bootPlugin, error) {
	return func(string) (*bootPlugin, error) {
		w.registered = append(w.registered, id)
		if w.endsOn["registers"] == id {
			w.cancel()
		}
		if value, ok := w.registerBoom[id]; ok {
			panic(value)
		}
		if err := w.failure(w.registerFail, id); err != nil {
			return nil, err
		}
		if name, ok := w.calledAs[id]; ok {
			return &bootPlugin{id: name, world: w}, nil
		}
		return &bootPlugin{id: id, world: w}, nil
	}
}

// accept pauses each registered plugin the application rejects, and answers the ones it keeps.
func (w *bootWorld) accept(ctx context.Context, boot *pluginkit.Boot,
	registered []*bootPlugin) ([]pluginkit.Plugin, error) {
	var kept []pluginkit.Plugin
	for _, plugin := range registered {
		if !w.rejected[plugin.id] {
			kept = append(kept, plugin)
			continue
		}
		if err := boot.Pause(ctx, plugin.id, fmt.Errorf("%s declares a role it cannot accept", plugin.id)); err != nil {
			return nil, err
		}
	}
	return kept, nil
}

// bootPlugins boots the compiled plugins as an application does, noting what started and how the boot ended.
func (w *bootWorld) bootPlugins() error {
	var seeds []pluginkit.PluginRecord
	for _, record := range w.seeds {
		seeds = append(seeds, record)
	}
	w.store = &bootStore{inner: pluginstoretest.NewMemory(seeds...), readErr: w.readErr, writeErr: w.writeErr}
	w.booted = true
	ctx, cancel := context.WithCancel(w.t.Context())
	defer cancel()
	w.cancel = cancel
	w.err = w.run(ctx)
	return nil
}

// run registers, accepts and starts the plugins, answering the error that stopped the boot.
func (w *bootWorld) run(ctx context.Context) error {
	boot, err := pluginkit.NewBoot(ctx, w.store)
	if err != nil {
		return err
	}
	outcome, err := pluginkit.Register(ctx, boot, w.entries(), func(id string) string { return id })
	if err != nil {
		return err
	}
	plugins, err := w.accept(ctx, boot, outcome.Registered)
	if err != nil {
		return err
	}
	host, err := pluginkit.NewHostWithMigrationStore(bootMigrations{world: w}, nil, plugins...)
	if err != nil {
		return err
	}
	started, err := host.StartEach(ctx)
	if err != nil {
		return err
	}
	for _, plugin := range started.Started {
		w.started = append(w.started, plugin.ID)
	}
	return boot.Started(ctx, started)
}

// settled answers the error that stopped the boot, if one did.
func (w *bootWorld) settled() error {
	if w.err != nil {
		return fmt.Errorf("the boot failed: %w", w.err)
	}
	return nil
}

// startedInOrder checks that exactly the plugins of list started, in order.
func (w *bootWorld) startedInOrder(list string) error {
	if err := w.settled(); err != nil {
		return err
	}
	if want := names(list); !slices.Equal(w.started, want) {
		return fmt.Errorf("started %q, want %q", w.started, want)
	}
	return nil
}

// neverStarted checks that the Start of the plugin id was not called.
func (w *bootWorld) neverStarted(id string) error {
	if slices.Contains(w.startCalls, id) {
		return fmt.Errorf("the Start of %s ran", id)
	}
	return nil
}

// noRecordOf checks that the store holds no record of the plugin id.
func (w *bootWorld) noRecordOf(id string) error {
	records, err := w.store.inner.Records(w.t.Context())
	if err != nil {
		return err
	}
	if held, ok := records[id]; ok {
		return fmt.Errorf("the store holds %+v, want no record of %s", held, id)
	}
	return nil
}

// holds checks that the store holds want.
func (w *bootWorld) holds(want pluginkit.PluginRecord) error {
	records, err := w.store.inner.Records(w.t.Context())
	if err != nil {
		return err
	}
	if got := records[want.ID]; got != want {
		return fmt.Errorf("the store holds %+v, want %+v", got, want)
	}
	return nil
}

// recordsClean checks that the store holds the plugin id in state with no error.
func (w *bootWorld) recordsClean(id, state string) error {
	return w.holds(pluginkit.PluginRecord{ID: id, State: pluginkit.PluginState(state)})
}

// notWritten checks that the boot wrote nothing to the store.
func (w *bootWorld) notWritten() error {
	if w.store.writes > 0 {
		return fmt.Errorf("the store was written %d times, want none", w.store.writes)
	}
	return nil
}

// neverRegistered checks that the Register of the plugin id was not called.
func (w *bootWorld) neverRegistered(id string) error {
	if slices.Contains(w.registered, id) {
		return fmt.Errorf("the Register of %s ran", id)
	}
	return nil
}

// contextEnded checks that the boot reported its ended context.
func (w *bootWorld) contextEnded() error {
	if !errors.Is(w.err, context.Canceled) {
		return fmt.Errorf("the boot ended with %v, want its context's end", w.err)
	}
	return nil
}

// failedWith checks that the boot stopped with the text.
func (w *bootWorld) failedWith(text string) error {
	if w.err == nil || w.err.Error() != text {
		return fmt.Errorf("the boot ended with %v, want %q", w.err, text)
	}
	return nil
}

// givenBoot registers the steps that set the plugins, the records and the failures.
func givenBoot(sc *godog.ScenarioContext, w *bootWorld) {
	sc.Step(`^the compiled plugins (.+)$`, w.compile)
	sc.Step(`^the store holds no record$`, w.noRecord)
	sc.Step(`^the store records (.+) as (active|inactive|not installed)$`, w.seed)
	sc.Step(`^the store records "([^"]*)" as paused with "(.*)"$`, w.pausedWith)
	sc.Step(`^the Register of "([^"]*)" fails with "([^"]*)"$`, func(id, text string) { set(w.registerFail, id, text) })
	sc.Step(`^the Register of "([^"]*)" panics with "([^"]*)"$`, func(id, text string) { set(w.registerBoom, id, text) })
	sc.Step(`^the Register of "([^"]*)" answers a plugin that calls itself "([^"]*)"$`,
		func(id, name string) { set(w.calledAs, id, name) })
	sc.Step(`^the migration of "([^"]*)" fails with "([^"]*)"$`, func(id, text string) { set(w.migrateFails, id, text) })
	sc.Step(`^the Start of "([^"]*)" fails with "([^"]*)"$`, func(id, text string) { set(w.startFails, id, text) })
	sc.Step(`^the migration record of "([^"]*)" fails with "([^"]*)"$`,
		func(id, text string) { set(w.recordFails, id, text) })
	sc.Step(`^the application finds that "([^"]*)" declares a role it cannot accept$`, w.rejectRole)
	sc.Step(`^the context of the boot ends while "([^"]*)" (starts|registers)$`,
		func(id, step string) { set(w.endsOn, step, id) })
	sc.Step(`^the store fails to read with "([^"]*)"$`, w.readFails)
	sc.Step(`^the store fails to write with "([^"]*)"$`, w.writeFails)
}

// thenBoot registers the step that boots and the steps that check what the boot did.
func thenBoot(sc *godog.ScenarioContext, w *bootWorld) {
	sc.Step(`^the application boots its plugins$`, w.bootPlugins)
	sc.Step(`^((?:"[^"]*"(?:, | and )?)+) started(?:, in that order)?$`, w.startedInOrder)
	sc.Step(`^"([^"]*)" never started$`, w.neverStarted)
	sc.Step(`^the store records "([^"]*)" as (active|inactive|not installed) with no error$`, w.recordsClean)
	sc.Step(`^the store was not written$`, w.notWritten)
	sc.Step(`^the store holds no record of "([^"]*)"$`, w.noRecordOf)
	sc.Step(`^the Register of "([^"]*)" never ran$`, w.neverRegistered)
	sc.Step(`^the boot reports that its context ended$`, w.contextEnded)
	sc.Step(`^the boot fails with: (.+)$`, w.failedWith)
}

// initializeBoot gives each boot scenario a world of its own and its steps.
func initializeBoot(t *testing.T) func(*godog.ScenarioContext) {
	return func(sc *godog.ScenarioContext) {
		w := &bootWorld{
			t: t, seeds: map[string]pluginkit.PluginRecord{}, registerFail: map[string]string{},
			registerBoom: map[string]string{}, migrateFails: map[string]string{}, startFails: map[string]string{},
			recordFails: map[string]string{}, rejected: map[string]bool{}, endsOn: map[string]string{},
			calledAs: map[string]string{},
		}
		givenBoot(sc, w)
		thenBoot(sc, w)
	}
}
