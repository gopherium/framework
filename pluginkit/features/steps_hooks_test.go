// SPDX-License-Identifier: Apache-2.0

package features_test

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strings"
	"testing"
	"testing/synctest"
	"time"

	"github.com/cucumber/godog"

	"github.com/gopherium/framework/pluginkit"
)

// handler is the interface an action hook calls.
type handler interface {
	Handle(ctx context.Context) error
}

// titler is the interface a filter hook calls.
type titler interface {
	Title(ctx context.Context, title string) (string, error)
}

// identified is the least a plugin offers a hook.
type identified interface {
	ID() string
}

// quoted finds every quoted name in a list such as "a", "b" and "c".
var quoted = regexp.MustCompile(`"([^"]*)"`)

// names returns the quoted names of list, in order.
func names(list string) []string {
	var found []string
	for _, match := range quoted.FindAllStringSubmatch(list, -1) {
		found = append(found, match[1])
	}
	return found
}

// scenePlugin is a plugin of a scenario, doing what the scenario's steps tell it.
type scenePlugin struct {
	id            string
	takes         time.Duration
	panics        string
	fail          func(ctx context.Context) error
	before, after string
	world         *hookWorld
}

// ID returns the plugin's id.
func (p *scenePlugin) ID() string {
	return p.id
}

// Handle records the call, takes the plugin's time, then panics or fails as told.
func (p *scenePlugin) Handle(ctx context.Context) error {
	p.world.calls = append(p.world.calls, p.id)
	p.world.late[p.id] = ctx.Err() != nil
	time.Sleep(p.takes)
	if p.panics != "" {
		panic(p.panics)
	}
	if p.fail != nil {
		return p.fail(ctx)
	}
	return nil
}

// Title handles the call, then returns title between the plugin's words, or nothing on a failure.
func (p *scenePlugin) Title(ctx context.Context, title string) (string, error) {
	if err := p.Handle(ctx); err != nil {
		return "", err
	}
	return p.before + title + p.after, nil
}

// bystander is a plugin that implements no hook interface.
type bystander struct {
	id string
}

// ID returns the plugin's id.
func (b bystander) ID() string {
	return b.id
}

// unwrapPanics is an error whose Unwrap panics.
type unwrapPanics struct{}

// Error returns the error's text.
func (unwrapPanics) Error() string {
	return "index write failed"
}

// Unwrap panics with the text unwrapped.
func (unwrapPanics) Unwrap() error {
	panic("unwrapped")
}

// hookWorld is the state of one scenario.
type hookWorld struct {
	t            *testing.T
	name         string
	budget       time.Duration
	timedFirings int
	plugins      []identified
	byID         map[string]*scenePlugin
	calls        []string
	late         map[string]bool
	failures     []pluginkit.HookFailure
	reportTakes  time.Duration
	reportPanics string
	escaped      any
	actions      *pluginkit.Hook[handler]
	title        string
}

// hookWorldKey carries a scenario's world in its context.
type hookWorldKey struct{}

// worldOf returns the world of the scenario ctx belongs to.
func worldOf(ctx context.Context) *hookWorld {
	return ctx.Value(hookWorldKey{}).(*hookWorld)
}

// plugin returns the plugin id of the scenario ctx belongs to, failing when the scenario never named it.
func plugin(ctx context.Context, id string) (*scenePlugin, error) {
	if p, ok := worldOf(ctx).byID[id]; ok {
		return p, nil
	}
	return nil, fmt.Errorf("no plugin %q in this scenario", id)
}

// settings returns the hook settings of the scenario, reporting into its failures.
func (w *hookWorld) settings() pluginkit.HookSettings {
	return pluginkit.HookSettings{Budget: w.budget, TimedFirings: w.timedFirings, Report: w.report}
}

// report records failure, takes the scenario's report time, then panics when the scenario says so.
func (w *hookWorld) report(failure pluginkit.HookFailure) {
	w.failures = append(w.failures, failure)
	time.Sleep(w.reportTakes)
	if w.reportPanics != "" {
		panic(w.reportPanics)
	}
}

// hook builds the action hook of the scenario once and returns it.
func (w *hookWorld) hook() (*pluginkit.Hook[handler], error) {
	if w.actions != nil {
		return w.actions, nil
	}
	hook, err := pluginkit.NewHook[handler](w.name, w.plugins, w.settings())
	w.actions = hook
	return hook, err
}

// onFakeClock runs fn on a fake clock, so a scenario's times pass in no real time, keeping a panic that leaves fn.
func (w *hookWorld) onFakeClock(fn func()) {
	synctest.Test(w.t, func(*testing.T) {
		defer func() {
			if escaped := recover(); escaped != nil {
				w.escaped = escaped
			}
		}()
		fn()
	})
}

// hookStats returns the counters of the action hook, failing when it never fired.
func (w *hookWorld) hookStats() (pluginkit.HookStats, error) {
	if w.actions == nil {
		return pluginkit.HookStats{}, errors.New("the hook never fired")
	}
	return w.actions.Stats(), nil
}

// statsOf returns the counters of the plugin id in the action hook.
func (w *hookWorld) statsOf(id string) (pluginkit.PluginStats, error) {
	if w.actions == nil {
		return pluginkit.PluginStats{}, errors.New("the hook never fired")
	}
	for _, stats := range w.actions.Stats().Plugins {
		if stats.ID == id {
			return stats, nil
		}
	}
	return pluginkit.PluginStats{}, fmt.Errorf("no counters for %s", id)
}

// implement registers the plugins of list, in order after any registered before, as implementing the hook name.
func implement(ctx context.Context, list, name string) {
	w := worldOf(ctx)
	w.name = name
	for _, id := range names(list) {
		p := &scenePlugin{id: id, world: w}
		w.plugins = append(w.plugins, p)
		w.byID[id] = p
	}
}

// registerBystander registers a plugin that implements no hook interface.
func registerBystander(ctx context.Context, id string) {
	w := worldOf(ctx)
	w.plugins = append(w.plugins, bystander{id: id})
}

// setBudget gives the hook a budget of text milliseconds, such as 1.9, read exactly.
func setBudget(ctx context.Context, text string) error {
	budget, err := time.ParseDuration(text + "ms")
	worldOf(ctx).budget = budget
	return err
}

// setTiming gives the hook a budget and a count of firings timed after an overrun.
func setTiming(ctx context.Context, text string, timedFirings int) error {
	worldOf(ctx).timedFirings = timedFirings
	return setBudget(ctx, text)
}

// setTakes makes the plugin id take text milliseconds on each call.
func setTakes(ctx context.Context, id, text string) error {
	p, err := plugin(ctx, id)
	if err != nil {
		return err
	}
	p.takes, err = time.ParseDuration(text + "ms")
	return err
}

// setFailure makes the plugin id fail with fail.
func setFailure(ctx context.Context, id string, fail func(ctx context.Context) error) error {
	p, err := plugin(ctx, id)
	if err == nil {
		p.fail = fail
	}
	return err
}

// failsWith makes the plugin id fail with its own error text.
func failsWith(ctx context.Context, id, text string) error {
	return setFailure(ctx, id, func(context.Context) error { return errors.New(text) })
}

// failsWithEndedContext makes the plugin id fail with the error of its ended context, or with only its text.
func failsWithEndedContext(ctx context.Context, id, what string) error {
	verb := "%w"
	if what == "text" {
		verb = "%v"
	}
	return setFailure(ctx, id, func(ctx context.Context) error { return fmt.Errorf("index write: "+verb, ctx.Err()) })
}

// failsWithPanickyUnwrap makes the plugin id fail with an error whose Unwrap panics.
func failsWithPanickyUnwrap(ctx context.Context, id string) error {
	return setFailure(ctx, id, func(context.Context) error { return unwrapPanics{} })
}

// setReportTakes makes the report take text milliseconds on each failure.
func setReportTakes(ctx context.Context, text string) error {
	takes, err := time.ParseDuration(text + "ms")
	worldOf(ctx).reportTakes = takes
	return err
}

// reportPanicsWith makes the report panic with text on each failure.
func reportPanicsWith(ctx context.Context, text string) {
	worldOf(ctx).reportPanics = text
}

// panicsWith makes the plugin id panic with text.
func panicsWith(ctx context.Context, id, text string) error {
	p, err := plugin(ctx, id)
	if err == nil {
		p.panics = text
	}
	return err
}

// putsAround makes the plugin id put text before or after the title it filters.
func putsAround(ctx context.Context, id, text, where string) error {
	p, err := plugin(ctx, id)
	if err != nil {
		return err
	}
	if where == "before" {
		p.before = text
	} else {
		p.after = text
	}
	return nil
}

// handle calls Handle on one plugin of the hook.
func handle(ctx context.Context, h handler) error {
	return h.Handle(ctx)
}

// fireTimes fires the action hook n times on the fake clock.
func fireTimes(ctx context.Context, n int) error {
	w := worldOf(ctx)
	hook, err := w.hook()
	if err != nil {
		return err
	}
	w.onFakeClock(func() {
		for range n {
			hook.Fire(context.Background(), handle)
		}
	})
	return nil
}

// fire fires the action hook once.
func fire(ctx context.Context) error {
	return fireTimes(ctx, 1)
}

// overrunOnce fires the action hook once with every call taking twice its budget.
func overrunOnce(ctx context.Context) error {
	w := worldOf(ctx)
	hook, err := w.hook()
	if err != nil {
		return err
	}
	w.onFakeClock(func() {
		hook.Fire(context.Background(), func(context.Context, handler) error {
			time.Sleep(2 * w.budget)
			return nil
		})
	})
	return nil
}

// fireWhileTaking fires the action hook once after giving each plugin of table the milliseconds beside it.
func fireWhileTaking(ctx context.Context, table *godog.Table) error {
	for _, row := range table.Rows[1:] {
		if err := setTakes(ctx, row.Cells[0].Value, row.Cells[1].Value); err != nil {
			return err
		}
	}
	return fire(ctx)
}

// filterTitle passes title through the filter hook of the scenario.
func filterTitle(ctx context.Context, title string) error {
	w := worldOf(ctx)
	hook, err := pluginkit.NewHook[titler](w.name, w.plugins, w.settings())
	if err != nil {
		return err
	}
	w.onFakeClock(func() {
		w.title = pluginkit.Filter(context.Background(), hook, title,
			func(ctx context.Context, t titler, title string) (string, error) { return t.Title(ctx, title) })
	})
	return nil
}

// calledInOrder checks that the plugins of list, and no others, were called in that order.
func calledInOrder(ctx context.Context, list string) error {
	if called, want := worldOf(ctx).calls, names(list); !slices.Equal(called, want) {
		return fmt.Errorf("called %q, want %q", called, want)
	}
	return nil
}

// notCalled checks that the plugin id was never called.
func notCalled(ctx context.Context, id string) error {
	if slices.Contains(worldOf(ctx).calls, id) {
		return fmt.Errorf("%s was called", id)
	}
	return nil
}

// calledLate checks that every plugin of list was called after the firing's deadline had passed.
func calledLate(ctx context.Context, list string) error {
	for _, id := range names(list) {
		if !worldOf(ctx).late[id] {
			return fmt.Errorf("%s was called before the deadline passed", id)
		}
	}
	return nil
}

// reportHolds checks that a reported failure has the text want, and the panicking plugin's stack only when named.
func reportHolds(ctx context.Context, want, stack string) error {
	failures := worldOf(ctx).failures
	for _, failure := range failures {
		if failure.Err.Error() == want && strings.Contains(failure.Stack, "(*scenePlugin).Handle") == (stack != "") {
			return nil
		}
	}
	return fmt.Errorf("reported %+v, want %q%s", failures, want, stack)
}

// reportMarksContextEnded checks that the failure of the plugin id is marked as caused by the ended context.
func reportMarksContextEnded(ctx context.Context, id string) error {
	failures := worldOf(ctx).failures
	for _, failure := range failures {
		if failure.ID == id && failure.ContextEnded {
			return nil
		}
	}
	return fmt.Errorf("reported %+v, want a failure of %s marked as context ended", failures, id)
}

// countedFailures checks the errors or panics counted for the plugin id.
func countedFailures(ctx context.Context, id string, want int, kind string) error {
	stats, err := worldOf(ctx).statsOf(id)
	if err != nil {
		return err
	}
	got := stats.Errors
	if kind == "panic" || kind == "panics" {
		got = stats.Panics
	}
	if got != uint64(want) {
		return fmt.Errorf("%s is counted %d %s, want %d", id, got, kind, want)
	}
	return nil
}

// countedErrorsAndEnded checks the errors and the calls whose context ended counted for the plugin id.
func countedErrorsAndEnded(ctx context.Context, id string, errs, ended int) error {
	stats, err := worldOf(ctx).statsOf(id)
	if err != nil {
		return err
	}
	if stats.Errors != uint64(errs) || stats.ContextEnded != uint64(ended) {
		return fmt.Errorf("%s is counted %+v, want %d errors and %d calls whose context ended", id, stats, errs, ended)
	}
	return nil
}

// hookOverruns checks the overruns counted on the hook.
func hookOverruns(ctx context.Context, want int) error {
	stats, err := worldOf(ctx).hookStats()
	if err == nil && stats.Overruns != uint64(want) {
		err = fmt.Errorf("the hook is counted %d overruns, want %d", stats.Overruns, want)
	}
	return err
}

// unchargedOverruns checks the timed overruns the hook counted with no plugin charged.
func unchargedOverruns(ctx context.Context, want int) error {
	stats, err := worldOf(ctx).hookStats()
	if err == nil && stats.UnchargedOverruns != uint64(want) {
		err = fmt.Errorf("the hook is counted %d timed overruns that no plugin is charged for, want %d",
			stats.UnchargedOverruns, want)
	}
	return err
}

// reportTimed checks the time the report took in the timed firings, text milliseconds such as 1.5.
func reportTimed(ctx context.Context, text string) error {
	stats, err := worldOf(ctx).hookStats()
	if err != nil {
		return err
	}
	want, err := time.ParseDuration(text + "ms")
	if err == nil && stats.TimedReportTime != want {
		err = fmt.Errorf("the report is counted %v in the timed firings, want %v", stats.TimedReportTime, want)
	}
	return err
}

// chargedOverruns checks the overruns charged to the plugin id.
func chargedOverruns(ctx context.Context, id string, want int) error {
	stats, err := worldOf(ctx).statsOf(id)
	if err == nil && stats.Overruns != uint64(want) {
		err = fmt.Errorf("%s is charged %d overruns, want %d", id, stats.Overruns, want)
	}
	return err
}

// chargedNone checks that no plugin of list is charged an overrun.
func chargedNone(ctx context.Context, list string) error {
	for _, id := range names(list) {
		if err := chargedOverruns(ctx, id, 0); err != nil {
			return err
		}
	}
	return nil
}

// noPluginCharged checks that no plugin of the hook is charged an overrun.
func noPluginCharged(ctx context.Context) error {
	for id := range worldOf(ctx).byID {
		if err := chargedOverruns(ctx, id, 0); err != nil {
			return err
		}
	}
	return nil
}

// timedIn checks that every plugin of list was timed in want firings.
func timedIn(ctx context.Context, list string, want int) error {
	for _, id := range names(list) {
		stats, err := worldOf(ctx).statsOf(id)
		if err == nil && stats.TimedCalls != uint64(want) {
			err = fmt.Errorf("%s was timed in %d firings, want %d", id, stats.TimedCalls, want)
		}
		if err != nil {
			return err
		}
	}
	return nil
}

// firingPanicked checks that a firing panicked with want, and takes that panic as expected.
func firingPanicked(ctx context.Context, want string) error {
	w := worldOf(ctx)
	if w.escaped != want {
		return fmt.Errorf("the firing panicked with %v, want %q", w.escaped, want)
	}
	w.escaped = nil
	return nil
}

// noPanicLeft fails a scenario whose firing panicked when no step expected it.
func noPanicLeft(ctx context.Context, _ *godog.Scenario, _ error) (context.Context, error) {
	if escaped := worldOf(ctx).escaped; escaped != nil {
		return ctx, fmt.Errorf("a firing panicked with %v", escaped)
	}
	return ctx, nil
}

// titleBecomes checks the title the filter returned.
func titleBecomes(ctx context.Context, want string) error {
	if got := worldOf(ctx).title; got != want {
		return fmt.Errorf("the title became %q, want %q", got, want)
	}
	return nil
}

// initializeHooks returns the steps of the hooks feature, each scenario with its own world on t's fake clock.
func initializeHooks(t *testing.T) func(*godog.ScenarioContext) {
	return func(sc *godog.ScenarioContext) {
		sc.Before(func(ctx context.Context, _ *godog.Scenario) (context.Context, error) {
			w := &hookWorld{
				t: t, budget: time.Hour, timedFirings: 20, byID: map[string]*scenePlugin{}, late: map[string]bool{},
			}
			return context.WithValue(ctx, hookWorldKey{}, w), nil
		})
		sc.After(noPanicLeft)
		givenHooks(sc)
		whenHooks(sc)
		thenHooks(sc)
	}
}

// givenHooks registers the steps that set a scenario up.
func givenHooks(sc *godog.ScenarioContext) {
	sc.Step(`^the plugins? (.+) implements? the (?:filter )?hook "([^"]*)"$`, implement)
	sc.Step(`^the plugin "([^"]*)" is registered without implementing it$`, registerBystander)
	sc.Step(`^the hook has a budget of (\S+) ms$`, setBudget)
	sc.Step(`^the hook has a budget of (\S+) ms and times each plugin for (\d+) firings after an overrun$`, setTiming)
	sc.Step(`^"([^"]*)" takes (\S+) ms$`, setTakes)
	sc.Step(`^"([^"]*)" fails with "([^"]*)"$`, failsWith)
	sc.Step(`^"([^"]*)" fails with the (error|text) of its ended context(?: but not the error itself)?$`,
		failsWithEndedContext)
	sc.Step(`^"([^"]*)" fails with an error that panics when it is unwrapped$`, failsWithPanickyUnwrap)
	sc.Step(`^the report takes (\S+) ms$`, setReportTakes)
	sc.Step(`^the report panics with "([^"]*)"$`, reportPanicsWith)
	sc.Step(`^"([^"]*)" panics with "([^"]*)"$`, panicsWith)
	sc.Step(`^"([^"]*)" puts "([^"]*)" (before|after) the title$`, putsAround)
	sc.Step(`^the hook has run over its budget once$`, overrunOnce)
}

// whenHooks registers the steps that fire a scenario's hook.
func whenHooks(sc *godog.ScenarioContext) {
	sc.Step(`^the hook fires$`, fire)
	sc.Step(`^the hook fires (\d+) times within its budget$`, fireTimes)
	sc.Step(`^the hook fires while the plugins take$`, fireWhileTaking)
	sc.Step(`^the hook filters the title "([^"]*)"$`, filterTitle)
}

// thenHooks registers the steps that check what a scenario's firing did.
func thenHooks(sc *godog.ScenarioContext) {
	sc.Step(`^(.+) are called in that order$`, calledInOrder)
	sc.Step(`^"([^"]*)" is not called$`, notCalled)
	sc.Step(`^(.+) are called after the deadline passed$`, calledLate)
	sc.Step(`^the report holds "([^"]*)"( with the stack of the panic)?$`, reportHolds)
	sc.Step(`^the report marks the failure of "([^"]*)" as caused by the ended context$`, reportMarksContextEnded)
	sc.Step(`^"([^"]*)" is counted (\d+) (errors?|panics?)$`, countedFailures)
	sc.Step(`^"([^"]*)" is counted (\d+) errors? and (\d+) calls? whose context ended$`, countedErrorsAndEnded)
	sc.Step(`^the hook is counted (\d+) overruns?$`, hookOverruns)
	sc.Step(`^"([^"]*)" is charged (\d+) overruns?$`, chargedOverruns)
	sc.Step(`^(.+) are charged no overrun$`, chargedNone)
	sc.Step(`^no plugin is charged an overrun$`, noPluginCharged)
	sc.Step(`^the hook is counted (\d+) timed overruns? that no plugin is charged for$`, unchargedOverruns)
	sc.Step(`^the report is counted (\S+) ms in the timed firings$`, reportTimed)
	sc.Step(`^(.+) are each timed in (\d+) firings$`, timedIn)
	sc.Step(`^the firing panics with "([^"]*)"$`, firingPanicked)
	sc.Step(`^the title becomes "([^"]*)"$`, titleBecomes)
}
