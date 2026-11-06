// SPDX-License-Identifier: Apache-2.0

package pluginkit_test

import (
	"context"
	"errors"
	"fmt"
	"runtime"
	"testing"
	"testing/synctest"
	"time"

	"github.com/gopherium/framework/pluginkit"
)

// budget is the time one firing of the test hooks gives all their plugins.
const budget = 2 * time.Millisecond

// timedHook builds a hook with budget that times each plugin for timedFirings firings after an overrun.
func timedHook(t *testing.T, timedFirings int, plugins ...identified) (*pluginkit.Hook[greeter], *reports) {
	t.Helper()
	r := &reports{}
	return greetHook(t, settingsOf(budget, timedFirings, r), plugins...), r
}

// overrunOnce fires hook once with a plugin that runs past the budget.
func overrunOnce(t *testing.T, hook *pluginkit.Hook[greeter]) {
	t.Helper()
	hook.Fire(t.Context(), func(context.Context, greeter) error {
		time.Sleep(2 * budget)
		return nil
	})
}

// fireTimes fires hook n times.
func fireTimes(t *testing.T, hook *pluginkit.Hook[greeter], n int) {
	t.Helper()
	for range n {
		hook.Fire(t.Context(), greet)
	}
}

func TestAFiringRunsUnderOneDeadlineOfItsBudget(t *testing.T) {
	t.Parallel()

	synctest.Test(t, func(t *testing.T) {
		var afterStart []time.Duration
		hook, _ := timedHook(t, 20, &hookPlugin{id: "alpha", takes: budget / 2, log: &callLog{}},
			&hookPlugin{id: "beta", log: &callLog{}})
		began := time.Now()

		hook.Fire(t.Context(), func(ctx context.Context, g greeter) error {
			deadline, ok := ctx.Deadline()
			if !ok {
				t.Error("a plugin got a context with no deadline")
			}
			afterStart = append(afterStart, deadline.Sub(began))
			return g.Greet(ctx)
		})

		wantEqual(t, "deadlines after the start", afterStart, []time.Duration{budget, budget})
	})
}

func TestThePluginsAfterASlowOneAreStillCalled(t *testing.T) {
	t.Parallel()

	synctest.Test(t, func(t *testing.T) {
		log := &callLog{}
		hook, r := timedHook(t, 20, &hookPlugin{id: "analytics", takes: 5 * time.Millisecond, log: log},
			&hookPlugin{id: "feed", log: log})

		hook.Fire(t.Context(), greet)

		wantEqual(t, "called", log.called(), []string{"analytics", "feed"})
		wantEqual(t, "called after the deadline", log.late, []bool{false, true})
		if stats := hook.Stats(); stats.Overruns != 1 {
			t.Errorf("Stats().Overruns = %d, want 1", stats.Overruns)
		}
		if got := r.all(); len(got) != 0 {
			t.Errorf("reported %+v, want nothing, since no plugin failed", got)
		}
	})
}

func TestAFiringThatRunsPastItsOwnDeadlineIsAnOverrun(t *testing.T) {
	t.Parallel()

	const firings = 100
	hook := greetHook(t, settingsOf(20*time.Microsecond, 20, &reports{}), &hookPlugin{id: "alpha", log: &callLog{}})

	for range firings {
		hook.Fire(t.Context(), func(ctx context.Context, _ greeter) error {
			deadline, _ := ctx.Deadline()
			for !time.Now().After(deadline) {
				runtime.Gosched()
			}
			return nil
		})
	}

	if got := hook.Stats().Overruns; got != firings {
		t.Errorf("Stats().Overruns = %d, want %d, one for each firing that ran past its deadline", got, firings)
	}
}

func TestACallerDeadlineBeforeTheBudgetIsNoOverrun(t *testing.T) {
	t.Parallel()

	synctest.Test(t, func(t *testing.T) {
		ctx, cancel := context.WithTimeout(t.Context(), 500*time.Microsecond)
		defer cancel()
		hook, _ := timedHook(t, 20, &hookPlugin{id: "alpha", takes: 1500 * time.Microsecond, log: &callLog{}})

		hook.Fire(ctx, greet)

		if got := hook.Stats().Overruns; got != 0 {
			t.Errorf("Stats().Overruns = %d, want 0, since the firing stayed inside its budget", got)
		}
	})
}

func TestAFiringThatTakesExactlyItsBudgetIsNoOverrun(t *testing.T) {
	t.Parallel()

	synctest.Test(t, func(t *testing.T) {
		hook, _ := timedHook(t, 20, &hookPlugin{id: "alpha", takes: budget, log: &callLog{}})

		hook.Fire(t.Context(), greet)

		if stats := hook.Stats(); stats.Overruns != 0 {
			t.Errorf("Stats().Overruns = %d, want 0", stats.Overruns)
		}
	})
}

func TestAnUntimedOverrunChargesNoPlugin(t *testing.T) {
	t.Parallel()

	synctest.Test(t, func(t *testing.T) {
		hook, _ := timedHook(t, 20, &hookPlugin{id: "analytics", takes: 3 * time.Millisecond, log: &callLog{}})

		hook.Fire(t.Context(), greet)

		wantEqual(t, "Stats()", hook.Stats(), pluginkit.HookStats{
			Name: "content.changed", Firings: 1, Overruns: 1, Plugins: []pluginkit.PluginStats{{ID: "analytics"}},
		})
	})
}

func TestTheFiringsAfterAnOverrunTimeEachPlugin(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		timedFirings, fires int
		alpha, beta         time.Duration
		want                []pluginkit.PluginStats
	}{
		"for one firing when the setting says one": {1, 3, budget / 4, budget / 2, []pluginkit.PluginStats{
			{ID: "alpha", TimedCalls: 1, TimedTime: budget / 4},
			{ID: "beta", TimedCalls: 1, TimedTime: budget / 2},
		}},
		"for as many firings as the setting says": {3, 5, budget / 4, budget / 2, []pluginkit.PluginStats{
			{ID: "alpha", TimedCalls: 3, TimedTime: 3 * budget / 4},
			{ID: "beta", TimedCalls: 3, TimedTime: 3 * budget / 2},
		}},
		"charging an overrun between equal plugins to the first": {20, 1, budget, budget, []pluginkit.PluginStats{
			{ID: "alpha", Overruns: 1, TimedCalls: 1, TimedTime: budget},
			{ID: "beta", TimedCalls: 1, TimedTime: budget},
		}},
	}
	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			synctest.Test(t, func(t *testing.T) {
				hook, _ := timedHook(t, tc.timedFirings, &hookPlugin{id: "alpha", takes: tc.alpha, log: &callLog{}},
					&hookPlugin{id: "beta", takes: tc.beta, log: &callLog{}})
				overrunOnce(t, hook)

				fireTimes(t, hook, tc.fires)

				wantEqual(t, "Stats().Plugins", hook.Stats().Plugins, tc.want)
			})
		})
	}
}

func TestAnOverrunInsideTheWindowOpensItAgain(t *testing.T) {
	t.Parallel()

	synctest.Test(t, func(t *testing.T) {
		slow := &hookPlugin{id: "alpha", log: &callLog{}}
		hook, _ := timedHook(t, 3, slow)
		overrunOnce(t, hook)
		fireTimes(t, hook, 1)
		slow.takes = 2 * budget
		fireTimes(t, hook, 1)
		slow.takes = 0

		fireTimes(t, hook, 5)

		wantEqual(t, "alpha's counters", statsByID(hook)["alpha"], pluginkit.PluginStats{
			ID: "alpha", Overruns: 1, TimedCalls: 5, TimedTime: 2 * budget,
		})
	})
}

func TestATimedOverrunIsChargedToTheSlowestPlugin(t *testing.T) {
	t.Parallel()

	synctest.Test(t, func(t *testing.T) {
		hook, _ := timedHook(t, 20,
			&hookPlugin{id: "reviews", takes: 500 * time.Microsecond, log: &callLog{}},
			&hookPlugin{id: "analytics", takes: 1200 * time.Microsecond, log: &callLog{}},
			&hookPlugin{id: "feed", takes: 900 * time.Microsecond, log: &callLog{}})
		overrunOnce(t, hook)

		fireTimes(t, hook, 1)

		wantEqual(t, "Stats()", hook.Stats(), pluginkit.HookStats{
			Name: "content.changed", Firings: 2, Overruns: 2, Plugins: []pluginkit.PluginStats{
				{ID: "reviews", TimedCalls: 1, TimedTime: 500 * time.Microsecond},
				{ID: "analytics", Overruns: 1, TimedCalls: 1, TimedTime: 1200 * time.Microsecond},
				{ID: "feed", TimedCalls: 1, TimedTime: 900 * time.Microsecond},
			},
		})
	})
}

func TestATimedOverrunIsChargedToTheSlowestOfThatFiringOnly(t *testing.T) {
	t.Parallel()

	synctest.Test(t, func(t *testing.T) {
		alpha := &hookPlugin{id: "alpha", takes: 1500 * time.Microsecond, log: &callLog{}}
		beta := &hookPlugin{id: "beta", log: &callLog{}}
		hook, _ := timedHook(t, 20, alpha, beta)
		overrunOnce(t, hook)
		fireTimes(t, hook, 1)
		alpha.takes, beta.takes = 600*time.Microsecond, 1600*time.Microsecond

		fireTimes(t, hook, 1)

		wantEqual(t, "Stats().Plugins", hook.Stats().Plugins, []pluginkit.PluginStats{
			{ID: "alpha", TimedCalls: 2, TimedTime: 2100 * time.Microsecond},
			{ID: "beta", Overruns: 1, TimedCalls: 2, TimedTime: 1600 * time.Microsecond},
		})
	})
}

func TestAFiringAfterAnOverrunKeepsTheOrderTheDeadlineAndTheRecover(t *testing.T) {
	t.Parallel()

	synctest.Test(t, func(t *testing.T) {
		log := &callLog{}
		hook, r := timedHook(t, 20, &hookPlugin{id: "analytics", takes: 3 * time.Millisecond, log: log},
			&hookPlugin{id: "search", fail: func(ctx context.Context) error { return ctx.Err() }, log: log},
			&hookPlugin{id: "feed", fail: failWith(errors.New("feed unavailable")), log: log},
			&hookPlugin{id: "seo", panics: "boom", log: log})
		overrunOnce(t, hook)

		hook.Fire(t.Context(), greet)

		wantEqual(t, "called", log.called(), []string{"analytics", "search", "feed", "seo"})
		wantEqual(t, "called after the deadline", log.late, []bool{false, true, true, true})
		wantEqual(t, "Stats().Plugins", hook.Stats().Plugins, []pluginkit.PluginStats{
			{ID: "analytics", Overruns: 1, TimedCalls: 1, TimedTime: 3 * time.Millisecond},
			{ID: "search", ContextEnded: 1, TimedCalls: 1},
			{ID: "feed", Errors: 1, TimedCalls: 1},
			{ID: "seo", Panics: 1, TimedCalls: 1},
		})
		wantEqual(t, "reported", shapesOf(r.all()), []failureShape{
			{
				Hook: "content.changed", ID: "search", ContextEnded: true,
				Err: "pluginkit: search content.changed: context deadline exceeded",
			},
			{Hook: "content.changed", ID: "feed", Err: "pluginkit: feed content.changed: feed unavailable"},
			{
				Hook: "content.changed", ID: "seo", Err: "pluginkit: seo content.changed panicked: boom",
				Panicked: true, Stacked: true,
			},
		})
	})
}

// endedErrorCase is what one plugin returns after the firing's deadline, the counters and the report that follow.
type endedErrorCase struct {
	fail     func(ctx context.Context) error
	counters pluginkit.PluginStats
	failure  failureShape
}

// endedErrorCases lists the errors a plugin can return once the deadline has passed.
func endedErrorCases() map[string]endedErrorCase {
	ended := pluginkit.PluginStats{ID: "search", ContextEnded: 1}
	failed := pluginkit.PluginStats{ID: "search", Errors: 1}
	return map[string]endedErrorCase{
		"the context's error": {
			fail: func(ctx context.Context) error { return ctx.Err() }, counters: ended,
			failure: failureShape{ContextEnded: true, Err: "context deadline exceeded"},
		},
		"the context's error wrapped": {
			fail:     func(ctx context.Context) error { return fmt.Errorf("index write: %w", ctx.Err()) },
			counters: ended, failure: failureShape{ContextEnded: true, Err: "index write: context deadline exceeded"},
		},
		"an error of its own": {
			fail: failWith(errors.New("index unavailable")), counters: failed,
			failure: failureShape{Err: "index unavailable"},
		},
		"the context's text without the error": {
			fail:     func(ctx context.Context) error { return fmt.Errorf("index write: %v", ctx.Err()) },
			counters: failed, failure: failureShape{Err: "index write: context deadline exceeded"},
		},
	}
}

func TestAnErrorAfterTheDeadlineCountsAsContextEndedOnlyWhenItIsTheContextsError(t *testing.T) {
	t.Parallel()

	for name, tc := range endedErrorCases() {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			synctest.Test(t, func(t *testing.T) {
				hook, r := timedHook(t, 20, &hookPlugin{id: "analytics", takes: 5 * time.Millisecond, log: &callLog{}},
					&hookPlugin{id: "search", fail: tc.fail, log: &callLog{}})

				hook.Fire(t.Context(), greet)

				wantEqual(t, "search's counters", statsByID(hook)["search"], tc.counters)
				want := tc.failure
				want.Hook, want.ID, want.Err = "content.changed", "search", "pluginkit: search content.changed: "+want.Err
				wantEqual(t, "reported", shapesOf(r.all()), []failureShape{want})
			})
		})
	}
}

func TestTheContextsErrorBeforeTheDeadlineCountsAsAnError(t *testing.T) {
	t.Parallel()

	synctest.Test(t, func(t *testing.T) {
		hook, _ := timedHook(t, 20, &hookPlugin{id: "search", fail: failWith(context.DeadlineExceeded), log: &callLog{}})

		hook.Fire(t.Context(), greet)

		if search := statsByID(hook)["search"]; search.Errors != 1 || search.ContextEnded != 0 {
			t.Errorf("search's counters = %+v, want one error, since the context had not ended", search)
		}
	})
}

func TestTheCallersCanceledContextCountsAsContextEnded(t *testing.T) {
	t.Parallel()

	synctest.Test(t, func(t *testing.T) {
		ctx, cancel := context.WithCancel(t.Context())
		cancel()
		hook, r := timedHook(t, 20, &hookPlugin{id: "search", fail: func(ctx context.Context) error { return ctx.Err() },
			log: &callLog{}})

		hook.Fire(ctx, greet)

		failure := onlyFailure(t, r)
		wantEqual(t, "reported", shapesOf([]pluginkit.HookFailure{failure}), []failureShape{{
			Hook: "content.changed", ID: "search", Err: "pluginkit: search content.changed: context canceled",
			ContextEnded: true,
		}})
		if !errors.Is(failure.Err, context.Canceled) {
			t.Errorf("reported error = %v, want context.Canceled in its chain", failure.Err)
		}
		if hook.Stats().Overruns != 0 {
			t.Errorf("Stats().Overruns = %d, want 0, since no time passed", hook.Stats().Overruns)
		}
	})
}
