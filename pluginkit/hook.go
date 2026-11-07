// SPDX-License-Identifier: Apache-2.0

package pluginkit

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"runtime/debug"
	"sync/atomic"
	"time"
)

// HookSettings are the values a hook runs under, each one required.
type HookSettings struct {
	// Budget is the time one firing gives all its plugins together, the time of Report included.
	Budget time.Duration
	// TimedFirings is how many firings after an overrun time each plugin.
	TimedFirings int
	// Report receives every error and panic of a plugin inside the firing, from any goroutine at once, and must not panic.
	Report func(HookFailure)
}

// HookFailure is one error or panic of one plugin in one firing of a hook.
type HookFailure struct {
	Hook string
	ID   string
	// Panicked is true for a panic the hook recovered.
	Panicked bool
	// ContextEnded is true for an error that matches the firing's context after that context ended.
	ContextEnded bool
	// Err names the plugin and the hook, and for an error wraps the plugin's error, whose methods are the plugin's code.
	Err error
	// Stack is the stack of a panic, and empty for an error.
	Stack string
}

// HookStats are the counters of a hook, its plugins in registration order.
type HookStats struct {
	Name     string
	Firings  uint64
	Overruns uint64
	// UnchargedOverruns counts the timed firings that ran over their budget while their plugins together kept it.
	UnchargedOverruns uint64
	// TimedReportTime is the total time Report took in the timed firings.
	TimedReportTime time.Duration
	Plugins         []PluginStats
}

// PluginStats are the counters of one plugin in one hook.
type PluginStats struct {
	ID           string
	Errors       uint64
	Panics       uint64
	ContextEnded uint64
	// Overruns counts the timed firings whose plugins together ran over the budget, with this plugin as the slowest.
	Overruns uint64
	// TimedCalls counts the calls timed while firings were timed after an overrun.
	TimedCalls uint64
	// TimedTime is the total time of those calls, without the time Report took.
	TimedTime time.Duration
}

// pluginCounters are the live counters of one plugin in one hook.
type pluginCounters struct {
	errors, panics, contextEnded, overruns, timedCalls atomic.Uint64
	timedTime                                          atomic.Int64
}

// Hook calls, in registration order, the plugins that implement the interface C.
type Hook[C any] struct {
	name     string
	settings HookSettings
	ids      []string
	plugins  []C
	counters []pluginCounters
	firings  atomic.Uint64
	overruns atomic.Uint64
	timed    atomic.Int64

	uncharged  atomic.Uint64
	reportTime atomic.Int64
}

// NewHook returns the hook called name over the plugins that implement C, refusing what it cannot run under.
func NewHook[C any, P interface{ ID() string }](name string, plugins []P, settings HookSettings) (*Hook[C], error) {
	if err := checkHook(name, settings, reflect.TypeFor[C]()); err != nil {
		return nil, err
	}
	hook := &Hook[C]{name: name, settings: settings}
	seen := make(map[string]struct{}, len(plugins))
	for _, p := range plugins {
		id := p.ID()
		if _, ok := seen[id]; ok {
			return nil, fmt.Errorf("pluginkit: hook %s: duplicate id %q", name, id)
		}
		seen[id] = struct{}{}
		if plugin, ok := any(p).(C); ok {
			hook.ids = append(hook.ids, id)
			hook.plugins = append(hook.plugins, plugin)
		}
	}
	hook.counters = make([]pluginCounters, len(hook.plugins))
	return hook, nil
}

// checkHook refuses a hook name, settings or interface type a hook cannot run under.
func checkHook(name string, settings HookSettings, hookType reflect.Type) error {
	if name == "" {
		return errors.New("pluginkit: the hook name is empty")
	}
	if settings.Budget <= 0 {
		return fmt.Errorf("pluginkit: hook %s: the budget must stand above zero, got %v", name, settings.Budget)
	}
	if settings.TimedFirings <= 0 {
		return fmt.Errorf("pluginkit: hook %s: the timed firings must stand above zero, got %d",
			name, settings.TimedFirings)
	}
	if settings.Report == nil {
		return fmt.Errorf("pluginkit: hook %s: the report is nil", name)
	}
	if hookType.Kind() != reflect.Interface {
		return fmt.Errorf("pluginkit: hook %s: %v is not an interface", name, hookType)
	}
	return nil
}

// Fire calls call on every plugin of the hook in order, under one budget, past any plugin's error or panic.
func (h *Hook[C]) Fire(ctx context.Context, call func(ctx context.Context, plugin C) error) {
	if len(h.plugins) == 0 {
		return
	}
	h.fire(ctx, call)
}

// Filter passes value through every plugin of hook in order, keeping the incoming value on an error or a panic.
func Filter[C, V any](ctx context.Context, hook *Hook[C], value V,
	call func(ctx context.Context, plugin C, value V) (V, error)) V {
	if len(hook.plugins) == 0 {
		return value
	}
	hook.fire(ctx, func(ctx context.Context, plugin C) error {
		next, err := call(ctx, plugin, value)
		if err == nil {
			value = next
		}
		return err
	})
	return value
}

// Stats returns the counters of the hook and of each of its plugins.
func (h *Hook[C]) Stats() HookStats {
	stats := HookStats{
		Name:              h.name,
		Firings:           h.firings.Load(),
		Overruns:          h.overruns.Load(),
		UnchargedOverruns: h.uncharged.Load(),
		TimedReportTime:   time.Duration(h.reportTime.Load()),
		Plugins:           make([]PluginStats, len(h.ids)),
	}
	for i, id := range h.ids {
		c := &h.counters[i]
		stats.Plugins[i] = PluginStats{
			ID:           id,
			Errors:       c.errors.Load(),
			Panics:       c.panics.Load(),
			ContextEnded: c.contextEnded.Load(),
			Overruns:     c.overruns.Load(),
			TimedCalls:   c.timedCalls.Load(),
			TimedTime:    time.Duration(c.timedTime.Load()),
		}
	}
	return stats
}

// fire runs one firing, counting an overrun, charged to the slowest plugin when timed plugins ran over together.
func (h *Hook[C]) fire(ctx context.Context, call func(context.Context, C) error) {
	h.firings.Add(1)
	began := time.Now()
	ctx, cancel := context.WithDeadline(ctx, began.Add(h.settings.Budget))
	defer cancel()
	timed, slowest := h.takeTimedFiring(), -1
	if timed {
		slowest = h.callTimed(ctx, call)
	} else {
		for i, plugin := range h.plugins {
			h.report(i, run(ctx, plugin, call))
		}
	}
	if time.Since(began) <= h.settings.Budget {
		return
	}
	h.overruns.Add(1)
	if slowest >= 0 {
		h.counters[slowest].overruns.Add(1)
	} else if timed {
		h.uncharged.Add(1)
	}
	h.timed.Store(int64(h.settings.TimedFirings))
}

// takeTimedFiring reports whether this firing is one of those timed after an overrun, using one up.
func (h *Hook[C]) takeTimedFiring() bool {
	for {
		left := h.timed.Load()
		if left <= 0 {
			return false
		}
		if h.timed.CompareAndSwap(left, left-1) {
			return true
		}
	}
}

// callTimed calls and times every plugin in order, returning the slowest, or -1 when together they kept the budget.
func (h *Hook[C]) callTimed(ctx context.Context, call func(context.Context, C) error) int {
	slowest, longest, total := 0, time.Duration(0), time.Duration(0)
	for i, plugin := range h.plugins {
		began := time.Now()
		out := run(ctx, plugin, call)
		took := time.Since(began)
		total += took
		h.counters[i].timedCalls.Add(1)
		h.counters[i].timedTime.Add(int64(took))
		if took > longest {
			slowest, longest = i, took
		}
		h.reportTime.Add(int64(h.report(i, out)))
	}
	if total <= h.settings.Budget {
		return -1
	}
	return slowest
}

// report counts and reports what went wrong in one call of plugin i, if anything did, returning the time Report took.
func (h *Hook[C]) report(i int, out *outcome) time.Duration {
	if out == nil {
		return 0
	}
	failure := h.failureOf(i, out)
	began := time.Now()
	h.settings.Report(failure)
	return time.Since(began)
}

// failureOf counts what went wrong in a call of plugin i and returns it as the failure Report receives.
func (h *Hook[C]) failureOf(i int, out *outcome) HookFailure {
	failure := HookFailure{Hook: h.name, ID: h.ids[i]}
	counters := &h.counters[i]
	named := "pluginkit: " + failure.ID + " " + h.name
	if out.panicked {
		failure.Panicked, failure.Stack = true, out.stack
		failure.Err = panicError(named + " panicked: " + out.text)
		counters.panics.Add(1)
		return failure
	}
	failure.Err = &pluginError{text: named + ": " + out.text, err: out.err}
	failure.ContextEnded = out.ended
	if failure.ContextEnded {
		counters.contextEnded.Add(1)
	} else {
		counters.errors.Add(1)
	}
	return failure
}

// outcome is what went wrong in one call of a plugin, a panic with its stack or an error, with its text.
type outcome struct {
	panicked bool
	stack    string
	text     string
	err      error
	// ended is true for an error that matches the firing's context after that context ended.
	ended bool
}

// run calls one plugin inside a recover, answering nil when the call succeeded, else what went wrong.
func run[C any](ctx context.Context, plugin C, call func(context.Context, C) error) (out *outcome) {
	defer func() {
		if recovered := recover(); recovered != nil {
			out = &outcome{panicked: true, stack: string(debug.Stack()), text: describe(recovered)}
		}
	}()
	if err := call(ctx, plugin); err != nil {
		return &outcome{err: err, text: describe(err), ended: ctx.Err() != nil && errors.Is(err, ctx.Err())}
	}
	return nil
}

// describe returns v as fmt prints it, or its type when printing it panics.
func describe(v any) (text string) {
	defer func() {
		if recover() != nil {
			text = fmt.Sprintf("%T whose text panicked", v)
		}
	}()
	return fmt.Sprint(v)
}

// pluginError is a plugin's error with the plugin and the hook named before its text.
type pluginError struct {
	text string
	err  error
}

// Error returns the error's text.
func (e *pluginError) Error() string {
	return e.text
}

// Unwrap returns the plugin's own error.
func (e *pluginError) Unwrap() error {
	return e.err
}
