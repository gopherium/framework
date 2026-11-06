// SPDX-License-Identifier: Apache-2.0

package pluginkit_test

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gopherium/framework/pluginkit"
)

// greeter is the interface the test hooks call.
type greeter interface {
	Greet(ctx context.Context) error
}

// titler is the interface the test filters call.
type titler interface {
	Title(ctx context.Context, title string) (string, error)
}

// identified is the least a plugin offers a hook.
type identified interface {
	ID() string
}

// callLog records which plugins a firing called, in order, and whether each saw its context ended.
type callLog struct {
	mu   sync.Mutex
	ids  []string
	late []bool
}

// add records a call of the plugin id under ctx.
func (l *callLog) add(ctx context.Context, id string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.ids = append(l.ids, id)
	l.late = append(l.late, ctx.Err() != nil)
}

// called returns the ids of every call, in order.
func (l *callLog) called() []string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return slices.Clone(l.ids)
}

// hookPlugin is a plugin whose Greet and Title take its time, then panic or fail as its fields say.
type hookPlugin struct {
	id     string
	takes  time.Duration
	panics any
	fail   func(ctx context.Context) error
	suffix string
	log    *callLog
}

// ID returns the plugin's id.
func (p *hookPlugin) ID() string {
	return p.id
}

// Greet records the call, takes the plugin's time, then panics or fails as told.
func (p *hookPlugin) Greet(ctx context.Context) error {
	p.log.add(ctx, p.id)
	time.Sleep(p.takes)
	if p.panics != nil {
		panic(p.panics)
	}
	if p.fail != nil {
		return p.fail(ctx)
	}
	return nil
}

// Title greets, then returns title with the plugin's suffix, beside any failure.
func (p *hookPlugin) Title(ctx context.Context, title string) (string, error) {
	err := p.Greet(ctx)
	return title + p.suffix, err
}

// bystander is a plugin that implements no hook interface.
type bystander struct {
	id  string
	log *callLog
}

// ID returns the plugin's id.
func (b bystander) ID() string {
	return b.id
}

// failWith returns a failure that always answers err.
func failWith(err error) func(context.Context) error {
	return func(context.Context) error { return err }
}

// reports collects every failure a hook reports, from any goroutine.
type reports struct {
	mu       sync.Mutex
	failures []pluginkit.HookFailure
}

// add records one failure.
func (r *reports) add(failure pluginkit.HookFailure) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.failures = append(r.failures, failure)
}

// all returns every failure reported so far.
func (r *reports) all() []pluginkit.HookFailure {
	r.mu.Lock()
	defer r.mu.Unlock()
	return slices.Clone(r.failures)
}

// settingsOf returns hook settings with budget and timed firings, reporting into r.
func settingsOf(budget time.Duration, timedFirings int, r *reports) pluginkit.HookSettings {
	return pluginkit.HookSettings{Budget: budget, TimedFirings: timedFirings, Report: r.add}
}

// greetHook builds the hook content.changed of greeters over plugins, stopping the test when it is refused.
func greetHook(t *testing.T, settings pluginkit.HookSettings, plugins ...identified) *pluginkit.Hook[greeter] {
	t.Helper()
	hook, err := pluginkit.NewHook[greeter]("content.changed", plugins, settings)
	if err != nil {
		t.Fatalf("NewHook() error = %v, want nil", err)
	}
	return hook
}

// greet calls Greet on one plugin of a hook.
func greet(ctx context.Context, g greeter) error {
	return g.Greet(ctx)
}

// retitle calls Title on one plugin of a filter.
func retitle(ctx context.Context, t titler, title string) (string, error) {
	return t.Title(ctx, title)
}

// statsByID returns the counters of every plugin of a hook, keyed by id.
func statsByID(hook *pluginkit.Hook[greeter]) map[string]pluginkit.PluginStats {
	byID := make(map[string]pluginkit.PluginStats)
	for _, plugin := range hook.Stats().Plugins {
		byID[plugin.ID] = plugin
	}
	return byID
}

// wantEqual checks that got deeply equals want, naming what was compared.
func wantEqual(t *testing.T, what string, got, want any) {
	t.Helper()
	if !reflect.DeepEqual(got, want) {
		t.Errorf("%s = %+v, want %+v", what, got, want)
	}
}

// failureShape is a reported failure with its error as text and its stack only as present or absent.
type failureShape struct {
	Hook, ID, Err          string
	Panicked, ContextEnded bool
	Stacked                bool
}

// shapesOf returns the shape of each failure, in order.
func shapesOf(failures []pluginkit.HookFailure) []failureShape {
	shapes := make([]failureShape, len(failures))
	for i, f := range failures {
		shapes[i] = failureShape{
			Hook: f.Hook, ID: f.ID, Err: f.Err.Error(), Panicked: f.Panicked, ContextEnded: f.ContextEnded,
			Stacked: f.Stack != "",
		}
	}
	return shapes
}

// onlyFailure returns the one failure r holds, stopping the test when it holds another number.
func onlyFailure(t *testing.T, r *reports) pluginkit.HookFailure {
	t.Helper()
	failures := r.all()
	if len(failures) != 1 {
		t.Fatalf("reported %d failures, want 1", len(failures))
	}
	return failures[0]
}

func TestNewHookKeepsOnlyImplementersInRegistrationOrder(t *testing.T) {
	t.Parallel()

	log := &callLog{}
	hook := greetHook(t, settingsOf(time.Hour, 20, &reports{}),
		&hookPlugin{id: "gamma", log: log}, bystander{id: "theme", log: log}, &hookPlugin{id: "alpha", log: log})

	hook.Fire(t.Context(), greet)

	wantEqual(t, "called", log.called(), []string{"gamma", "alpha"})
	wantEqual(t, "Stats()", hook.Stats(), pluginkit.HookStats{
		Name: "content.changed", Firings: 1,
		Plugins: []pluginkit.PluginStats{{ID: "gamma"}, {ID: "alpha"}},
	})
}

func TestNewHookRefusesWhatItCannotRun(t *testing.T) {
	t.Parallel()

	r := &reports{}
	tests := map[string]struct {
		name     string
		settings pluginkit.HookSettings
		plugins  []identified
		want     string
	}{
		"an empty name": {"", settingsOf(time.Hour, 20, r), nil, "pluginkit: the hook name is empty"},
		"a zero budget": {
			"content.changed", settingsOf(0, 20, r), nil,
			"pluginkit: hook content.changed: the budget must stand above zero, got 0s",
		},
		"a negative budget": {
			"content.changed", settingsOf(-time.Millisecond, 20, r), nil,
			"pluginkit: hook content.changed: the budget must stand above zero, got -1ms",
		},
		"zero timed firings": {
			"content.changed", settingsOf(time.Hour, 0, r), nil,
			"pluginkit: hook content.changed: the timed firings must stand above zero, got 0",
		},
		"negative timed firings": {
			"content.changed", settingsOf(time.Hour, -1, r), nil,
			"pluginkit: hook content.changed: the timed firings must stand above zero, got -1",
		},
		"a nil report": {
			"content.changed", pluginkit.HookSettings{Budget: time.Hour, TimedFirings: 20}, nil,
			"pluginkit: hook content.changed: the report is nil",
		},
		"two plugins with one id": {
			"content.changed", settingsOf(time.Hour, 20, r),
			[]identified{bystander{id: "alpha"}, &hookPlugin{id: "beta"}, &hookPlugin{id: "alpha"}},
			`pluginkit: hook content.changed: duplicate id "alpha"`,
		},
	}
	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			hook, err := pluginkit.NewHook[greeter](tc.name, tc.plugins, tc.settings)

			if err == nil || err.Error() != tc.want {
				t.Errorf("NewHook() error = %v, want %q", err, tc.want)
			}
			if hook != nil {
				t.Errorf("NewHook() = %v, want nil", hook)
			}
		})
	}
}

func TestNewHookRefusesATypeThatIsNotAnInterface(t *testing.T) {
	t.Parallel()

	hook, err := pluginkit.NewHook[*hookPlugin]("content.changed", []identified{}, settingsOf(time.Hour, 20, &reports{}))

	want := "pluginkit: hook content.changed: *pluginkit_test.hookPlugin is not an interface"
	if err == nil || err.Error() != want {
		t.Errorf("NewHook() error = %v, want %q", err, want)
	}
	if hook != nil {
		t.Errorf("NewHook() = %v, want nil", hook)
	}
}

func TestNewHookTakesAnyAsTheInterface(t *testing.T) {
	t.Parallel()

	var ids []string
	hook, err := pluginkit.NewHook[any]("content.changed",
		[]identified{&hookPlugin{id: "alpha"}, bystander{id: "theme"}}, settingsOf(time.Hour, 20, &reports{}))
	if err != nil {
		t.Fatalf("NewHook() error = %v, want nil", err)
	}

	hook.Fire(t.Context(), func(_ context.Context, plugin any) error {
		ids = append(ids, plugin.(identified).ID())
		return nil
	})

	if !slices.Equal(ids, []string{"alpha", "theme"}) {
		t.Errorf("called = %q, want every plugin", ids)
	}
}

func TestNewHookTakesTheHostsPluginList(t *testing.T) {
	t.Parallel()

	var calls []string
	hook, err := pluginkit.NewHook[greeter]("content.changed",
		[]pluginkit.Plugin{&fakePlugin{id: "alpha", calls: &calls}}, settingsOf(time.Hour, 20, &reports{}))
	if err != nil {
		t.Fatalf("NewHook() error = %v, want nil", err)
	}

	if got := hook.Stats().Plugins; len(got) != 0 {
		t.Errorf("Stats().Plugins = %+v, want none, since the plugin is no greeter", got)
	}
}

func TestAFiringWithNoPluginAllocatesNothingAndCountsNothing(t *testing.T) {
	r := &reports{}
	hook := greetHook(t, settingsOf(time.Hour, 20, r), bystander{id: "theme"})
	filter, err := pluginkit.NewHook[titler]("page.title", []identified{bystander{id: "theme"}},
		settingsOf(time.Hour, 20, r))
	if err != nil {
		t.Fatalf("NewHook() error = %v, want nil", err)
	}
	ctx := t.Context()

	allocs := testing.AllocsPerRun(100, func() {
		hook.Fire(ctx, func(ctx context.Context, g greeter) error { return g.Greet(ctx) })
		if got := pluginkit.Filter(ctx, filter, "Hello", retitle); got != "Hello" {
			t.Errorf("Filter() = %q, want the title unchanged", got)
		}
	})

	if allocs != 0 {
		t.Errorf("a firing with no plugin allocated %v times, want 0", allocs)
	}
	if got := hook.Stats().Firings + filter.Stats().Firings; got != 0 {
		t.Errorf("firings counted = %d, want 0", got)
	}
}

func TestFireGoesOnPastAnErrorAndCountsIt(t *testing.T) {
	t.Parallel()

	errIndex := errors.New("index unavailable")
	log, r := &callLog{}, &reports{}
	hook := greetHook(t, settingsOf(time.Hour, 20, r),
		&hookPlugin{id: "alpha", log: log}, &hookPlugin{id: "beta", fail: failWith(errIndex), log: log},
		&hookPlugin{id: "gamma", log: log})

	hook.Fire(t.Context(), greet)

	wantEqual(t, "called", log.called(), []string{"alpha", "beta", "gamma"})
	failure := onlyFailure(t, r)
	wantEqual(t, "reported", shapesOf([]pluginkit.HookFailure{failure}), []failureShape{
		{Hook: "content.changed", ID: "beta", Err: "pluginkit: beta content.changed: index unavailable"},
	})
	if !errors.Is(failure.Err, errIndex) {
		t.Errorf("reported error = %v, want the plugin's error in its chain", failure.Err)
	}
	wantEqual(t, "beta's counters", statsByID(hook)["beta"], pluginkit.PluginStats{ID: "beta", Errors: 1})
}

func TestFireRecoversAPanicCountsItAndReportsItsStack(t *testing.T) {
	t.Parallel()

	log, r := &callLog{}, &reports{}
	hook := greetHook(t, settingsOf(time.Hour, 20, r),
		&hookPlugin{id: "alpha", log: log}, &hookPlugin{id: "beta", panics: "boom", log: log},
		&hookPlugin{id: "gamma", log: log})

	hook.Fire(t.Context(), greet)

	wantEqual(t, "called", log.called(), []string{"alpha", "beta", "gamma"})
	failure := onlyFailure(t, r)
	wantEqual(t, "reported", shapesOf([]pluginkit.HookFailure{failure}), []failureShape{{
		Hook: "content.changed", ID: "beta", Err: "pluginkit: beta content.changed panicked: boom",
		Panicked: true, Stacked: true,
	}})
	if !strings.Contains(failure.Stack, "(*hookPlugin).Greet") {
		t.Errorf("reported stack = %q, want the stack of the panic", failure.Stack)
	}
	wantEqual(t, "beta's counters", statsByID(hook)["beta"], pluginkit.PluginStats{ID: "beta", Panics: 1})
}

func TestFilterPassesTheValueThroughEveryPluginPastOnesThatFail(t *testing.T) {
	t.Parallel()

	tests := map[string]*hookPlugin{
		"no failure": {id: "middle", suffix: " and more"},
		"an error":   {id: "middle", suffix: " and more", fail: failWith(errors.New("no title"))},
		"a panic":    {id: "middle", suffix: " and more", panics: "boom"},
	}
	wants := map[string]string{"no failure": "Hello | Example and more | Home", "an error": "Hello | Example | Home",
		"a panic": "Hello | Example | Home"}
	for name, middle := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			log := &callLog{}
			middle.log = log
			hook, err := pluginkit.NewHook[titler]("page.title", []identified{
				&hookPlugin{id: "brand", suffix: " | Example", log: log}, middle,
				&hookPlugin{id: "suffix", suffix: " | Home", log: log},
			}, settingsOf(time.Hour, 20, &reports{}))
			if err != nil {
				t.Fatalf("NewHook() error = %v, want nil", err)
			}

			got := pluginkit.Filter(t.Context(), hook, "Hello", retitle)

			if got != wants[name] {
				t.Errorf("Filter() = %q, want %q", got, wants[name])
			}
			if called := log.called(); !slices.Equal(called, []string{"brand", "middle", "suffix"}) {
				t.Errorf("called = %q, want every plugin in order", called)
			}
		})
	}
}

func TestConcurrentFiringsCountEveryFailure(t *testing.T) {
	t.Parallel()

	const firings = 50
	r := &reports{}
	hook := greetHook(t, settingsOf(time.Hour, 20, r),
		&hookPlugin{id: "alpha", log: &callLog{}},
		&hookPlugin{id: "beta", fail: failWith(errors.New("index unavailable")), log: &callLog{}},
		&hookPlugin{id: "gamma", panics: "boom", log: &callLog{}})

	var wg sync.WaitGroup
	for range firings {
		wg.Go(func() { hook.Fire(context.Background(), greet) })
	}
	wg.Wait()

	wantEqual(t, "Stats()", hook.Stats(), pluginkit.HookStats{
		Name: "content.changed", Firings: firings,
		Plugins: []pluginkit.PluginStats{{ID: "alpha"}, {ID: "beta", Errors: firings}, {ID: "gamma", Panics: firings}},
	})
	if got := len(r.all()); got != 2*firings {
		t.Errorf("reported %d failures, want %d", got, 2*firings)
	}
}

func BenchmarkFireWithFiftyPlugins(b *testing.B) {
	plugins := make([]identified, 50)
	for i := range plugins {
		plugins[i] = &hookPlugin{id: fmt.Sprintf("plugin-%02d", i), log: &callLog{}}
	}
	hook, err := pluginkit.NewHook[greeter]("content.changed", plugins, settingsOf(time.Hour, 20, &reports{}))
	if err != nil {
		b.Fatalf("NewHook() error = %v, want nil", err)
	}
	ctx := context.Background()

	for b.Loop() {
		hook.Fire(ctx, func(context.Context, greeter) error { return nil })
	}
}
