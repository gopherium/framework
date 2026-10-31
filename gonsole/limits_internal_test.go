// SPDX-License-Identifier: Apache-2.0

package gonsole

import (
	"math"
	"runtime"
	"runtime/debug"
	"slices"
	"testing"
)

// recorder answers the limits a runtime holds and notes every limit handed to it.
type recorder struct {
	heldProcs  int
	heldMemory int64
	procs      []int
	memory     []int64
}

// runtime returns a runtime that answers the held limits and records each limit instead of applying it.
func (r *recorder) runtime() goRuntime {
	return goRuntime{
		maxProcs: func(n int) int {
			if n > 0 {
				r.procs = append(r.procs, n)
			}
			return r.heldProcs
		},
		memoryLimit: func(n int64) int64 {
			if n >= 0 {
				r.memory = append(r.memory, n)
			}
			return r.heldMemory
		},
	}
}

// unbounded returns a recorder standing for a runtime of 16 processors and no memory limit.
func unbounded() *recorder {
	return &recorder{heldProcs: 16, heldMemory: math.MaxInt64}
}

func TestApplyLeavesTheRuntimeAloneWhenNoLimitIsNamed(t *testing.T) {
	t.Parallel()

	seen := unbounded()
	RuntimeLimits{}.apply(seen.runtime())

	if seen.procs != nil || seen.memory != nil {
		t.Errorf("apply() called the runtime with %v and %v, want no call", seen.procs, seen.memory)
	}
}

func TestApplyHandsEachNamedLimitToTheRuntime(t *testing.T) {
	t.Parallel()

	cases := map[string]struct {
		limits RuntimeLimits
		procs  []int
		memory []int64
	}{
		"processors alone": {RuntimeLimits{MaxProcs: 3}, []int{3}, nil},
		"memory alone":     {RuntimeLimits{MemoryLimitMB: 256}, nil, []int64{256 << 20}},
		"both":             {RuntimeLimits{MaxProcs: 2, MemoryLimitMB: 64}, []int{2}, []int64{64 << 20}},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			seen := unbounded()
			tc.limits.apply(seen.runtime())

			if !slices.Equal(seen.procs, tc.procs) || !slices.Equal(seen.memory, tc.memory) {
				t.Errorf("apply() handed %v and %v, want %v and %v", seen.procs, seen.memory, tc.procs, tc.memory)
			}
		})
	}
}

func TestApplyKeepsWhatTheRuntimeHoldsWhenALimitIsLooser(t *testing.T) {
	t.Parallel()

	seen := &recorder{heldProcs: 2, heldMemory: 128 << 20}
	RuntimeLimits{MaxProcs: 4, MemoryLimitMB: 256}.apply(seen.runtime())

	if seen.procs != nil || seen.memory != nil {
		t.Errorf("apply() handed %v and %v, want the tighter limits Go holds kept", seen.procs, seen.memory)
	}
}

func TestApplyPinsALimitEqualToWhatTheRuntimeHolds(t *testing.T) {
	t.Parallel()

	seen := &recorder{heldProcs: 2, heldMemory: 128 << 20}
	RuntimeLimits{MaxProcs: 2, MemoryLimitMB: 128}.apply(seen.runtime())

	if !slices.Equal(seen.procs, []int{2}) || !slices.Equal(seen.memory, []int64{128 << 20}) {
		t.Errorf("apply() handed %v and %v, want [2] and [%d]", seen.procs, seen.memory, 128<<20)
	}
}

func TestApplyRuntimeLimitsHandsACleanReadToTheRuntime(t *testing.T) {
	t.Parallel()

	seen := unbounded()
	values := map[string]string{"MYAPP_MAX_PROCS": "1", "MYAPP_MEMORY_LIMIT_MB": "256"}

	applyRuntimeLimits(Env{Prefix: "MYAPP_", Getenv: func(key string) string { return values[key] }}, seen.runtime())

	if !slices.Equal(seen.procs, []int{1}) || !slices.Equal(seen.memory, []int64{256 << 20}) {
		t.Errorf("applyRuntimeLimits() handed %v and %v, want [1] and [%d]", seen.procs, seen.memory, 256<<20)
	}
}

func TestApplyRuntimeLimitsAppliesNothingFromSettingsItCannotRead(t *testing.T) {
	t.Parallel()

	seen := unbounded()
	values := map[string]string{"MYAPP_MAX_PROCS": "0", "MYAPP_MEMORY_LIMIT_MB": "256"}

	applyRuntimeLimits(Env{Prefix: "MYAPP_", Getenv: func(key string) string { return values[key] }}, seen.runtime())

	if seen.procs != nil || seen.memory != nil {
		t.Errorf("applyRuntimeLimits() handed %v and %v, want no call", seen.procs, seen.memory)
	}
}

func TestLiveRuntimeAnswersWhatTheGoRuntimeHolds(t *testing.T) {
	t.Parallel()

	live := liveRuntime()

	if got, want := live.maxProcs(0), runtime.GOMAXPROCS(0); got != want {
		t.Errorf("maxProcs(0) = %d, want %d", got, want)
	}
	if got, want := live.memoryLimit(-1), debug.SetMemoryLimit(-1); got != want {
		t.Errorf("memoryLimit(-1) = %d, want %d", got, want)
	}
}
