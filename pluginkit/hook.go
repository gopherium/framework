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
	// Budget is the time one firing gives all its plugins together.
	Budget time.Duration
	// TimedFirings is how many firings after an overrun time each plugin.
	TimedFirings int
	// Report receives every error and panic of a plugin inside the firing, from any goroutine at once.
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
	Err          error
	// Stack is the stack of a panic, and empty for an error.
	Stack string
}

// HookStats are the counters of a hook, its plugins in registration order.
type HookStats struct {
	Name     string
	Firings  uint64
	Overruns uint64
	Plugins  []PluginStats
}

// PluginStats are the counters of one plugin in one hook.
type PluginStats struct {
	ID           string
	Errors       uint64
	Panics       uint64
	ContextEnded uint64
	// Overruns counts the timed firings that ran over their budget with this plugin as the slowest.
	Overruns uint64
	// TimedCalls counts the calls timed while firings were timed after an overrun.
	TimedCalls uint64
	// TimedTime is the total time of those calls.
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
		Name:     h.name,
		Firings:  h.firings.Load(),
		Overruns: h.overruns.Load(),
		Plugins:  make([]PluginStats, len(h.ids)),
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

// fire runs one firing, counting an overrun and charging it to the slowest plugin when the firing was timed.
func (h *Hook[C]) fire(ctx context.Context, call func(context.Context, C) error) {
	h.firings.Add(1)
	began := time.Now()
	ctx, cancel := context.WithDeadline(ctx, began.Add(h.settings.Budget))
	defer cancel()
	slowest := -1
	if h.takeTimedFiring() {
		slowest = h.callTimed(ctx, call)
	} else {
		for i, plugin := range h.plugins {
			h.call(ctx, i, plugin, call)
		}
	}
	if time.Since(began) <= h.settings.Budget {
		return
	}
	h.overruns.Add(1)
	if slowest >= 0 {
		h.counters[slowest].overruns.Add(1)
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

// callTimed calls every plugin in order, timing each, and returns the index of the slowest.
func (h *Hook[C]) callTimed(ctx context.Context, call func(context.Context, C) error) int {
	slowest, longest := 0, time.Duration(0)
	for i, plugin := range h.plugins {
		began := time.Now()
		h.call(ctx, i, plugin, call)
		took := time.Since(began)
		h.counters[i].timedCalls.Add(1)
		h.counters[i].timedTime.Add(int64(took))
		if took > longest {
			slowest, longest = i, took
		}
	}
	return slowest
}

// call calls one plugin, then counts and reports its error or panic.
func (h *Hook[C]) call(ctx context.Context, i int, plugin C, call func(context.Context, C) error) {
	if out := run(ctx, plugin, call); out.recovered != nil || out.err != nil {
		h.settings.Report(h.failureOf(ctx, i, out))
	}
}

// failureOf counts what went wrong in a call of plugin i and returns it as the failure Report receives.
func (h *Hook[C]) failureOf(ctx context.Context, i int, out outcome) HookFailure {
	failure := HookFailure{Hook: h.name, ID: h.ids[i]}
	counters := &h.counters[i]
	if out.recovered != nil {
		failure.Panicked, failure.Stack = true, out.stack
		failure.Err = panicError(fmt.Sprintf("pluginkit: %s %s panicked: %v", failure.ID, h.name, out.recovered))
		counters.panics.Add(1)
		return failure
	}
	failure.Err = fmt.Errorf("pluginkit: %s %s: %w", failure.ID, h.name, out.err)
	failure.ContextEnded = ctx.Err() != nil && errors.Is(out.err, ctx.Err())
	if failure.ContextEnded {
		counters.contextEnded.Add(1)
	} else {
		counters.errors.Add(1)
	}
	return failure
}

// outcome is what one call of a plugin did wrong, a panic with its stack or an error, if anything.
type outcome struct {
	recovered any
	stack     string
	err       error
}

// run calls one plugin inside a recover, answering what it panicked with and its stack, or its error.
func run[C any](ctx context.Context, plugin C, call func(context.Context, C) error) (out outcome) {
	defer func() {
		if out.recovered = recover(); out.recovered != nil {
			out.stack = string(debug.Stack())
		}
	}()
	out.err = call(ctx, plugin)
	return out
}
