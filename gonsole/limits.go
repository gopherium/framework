// SPDX-License-Identifier: Apache-2.0

package gonsole

import (
	"errors"
	"runtime"
	"runtime/debug"
)

// maxMemoryLimitMB is the most megabytes of 1048576 bytes a memory limit may name.
const maxMemoryLimitMB = 1 << 30

// RuntimeLimits are the processor and memory limits a program hands the Go runtime, zero leaving Go its own.
type RuntimeLimits struct {
	// MaxProcs bounds the processors that run Go code at once, the MAX_PROCS setting.
	MaxProcs int
	// MemoryLimitMB is the memory in megabytes of 1048576 bytes Go tries to stay under, the MEMORY_LIMIT_MB setting.
	MemoryLimitMB int
}

// RuntimeLimits returns the MAX_PROCS and MEMORY_LIMIT_MB settings, MAX_PROCS at most the number of CPUs.
func (e Env) RuntimeLimits() (RuntimeLimits, error) {
	procs, procsErr := e.Count("MAX_PROCS", 0, AtMost(int64(runtime.NumCPU())))
	memory, memoryErr := e.Count("MEMORY_LIMIT_MB", 0, AtMost(maxMemoryLimitMB))
	if err := errors.Join(procsErr, memoryErr); err != nil {
		return RuntimeLimits{}, err
	}
	return RuntimeLimits{MaxProcs: procs, MemoryLimitMB: memory}, nil
}

// goRuntime holds the calls that hand limits to the Go runtime.
type goRuntime struct {
	maxProcs    func(int) int
	memoryLimit func(int64) int64
}

// liveRuntime returns the Go runtime the process runs on.
func liveRuntime() goRuntime {
	return goRuntime{maxProcs: runtime.GOMAXPROCS, memoryLimit: debug.SetMemoryLimit}
}

// apply hands rt each limit above zero that is no looser than the one rt holds.
func (l RuntimeLimits) apply(rt goRuntime) {
	if l.MaxProcs > 0 && l.MaxProcs <= rt.maxProcs(0) {
		rt.maxProcs(l.MaxProcs)
	}
	if memory := int64(l.MemoryLimitMB) << 20; memory > 0 && memory <= rt.memoryLimit(-1) {
		rt.memoryLimit(memory)
	}
}

// applyRuntimeLimits hands the limits env names to rt, and none when either cannot be read.
func applyRuntimeLimits(env Env, rt goRuntime) {
	if read, err := env.RuntimeLimits(); err == nil {
		read.apply(rt)
	}
}
