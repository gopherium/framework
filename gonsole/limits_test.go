// SPDX-License-Identifier: Apache-2.0

package gonsole_test

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"os"
	"os/exec"
	"runtime"
	"runtime/debug"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/gopherium/framework/gonsole"
)

// limitsSwitch is the variable that turns the test binary into the program that prints its runtime limits.
const limitsSwitch = "GONSOLE_EXEC_LIMITS"

// limitsProgram returns a program whose limits command prints the processors and the memory limit Go holds.
func limitsProgram() gonsole.Program {
	report := func(_ context.Context, call gonsole.Call) error {
		_, err := fmt.Fprintf(call.Stdout, "procs %d memory %d\n", runtime.GOMAXPROCS(0), debug.SetMemoryLimit(-1))
		return err
	}
	return gonsole.Program{
		Name:     "myapp",
		Env:      gonsole.Env{Prefix: "MYAPP_", Getenv: os.Getenv},
		Commands: []gonsole.Command{{Name: "limits", Summary: "print the runtime limits", Run: report}},
	}
}

// goLimitEntry reports whether the environment entry sets a MYAPP_ variable, GOMAXPROCS or GOMEMLIMIT.
func goLimitEntry(entry string) bool {
	return settingEntry(entry) || strings.HasPrefix(entry, "GOMAXPROCS=") || strings.HasPrefix(entry, "GOMEMLIMIT=")
}

// reportedLimits runs the limits program in its own process over the variables and returns what it prints.
func reportedLimits(t *testing.T, variables ...string) string {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), exampleBound)
	defer cancel()
	cmd := exec.CommandContext(ctx, os.Args[0], "limits")
	cmd.Env = append(append(slices.DeleteFunc(os.Environ(), goLimitEntry), limitsSwitch+"=1"), variables...)
	out, err := cmd.Output()
	var exited *exec.ExitError
	if errors.As(err, &exited) {
		t.Fatalf("running the limits program: %v, stderr %q", err, exited.Stderr)
	}
	if err != nil {
		t.Fatalf("running the limits program: %v", err)
	}
	return string(out)
}

func TestMainHandsTheLimitsItReadsToGo(t *testing.T) {
	t.Parallel()

	got := reportedLimits(t, "MYAPP_MAX_PROCS=1", "MYAPP_MEMORY_LIMIT_MB=64")

	if want := fmt.Sprintf("procs 1 memory %d\n", 64<<20); got != want {
		t.Errorf("the program reports %q, want %q", got, want)
	}
}

func TestMainHandsGoNoLimitWhenOneCannotBeRead(t *testing.T) {
	t.Parallel()

	got := reportedLimits(t, "MYAPP_MAX_PROCS=0", "MYAPP_MEMORY_LIMIT_MB=64")

	if want := reportedLimits(t); got != want {
		t.Errorf("the program reports %q, want Go's own limits, %q", got, want)
	}
}

func TestMainKeepsATighterMemoryLimitGoAlreadyHolds(t *testing.T) {
	t.Parallel()

	got := reportedLimits(t, "GOMEMLIMIT=32MiB", "MYAPP_MEMORY_LIMIT_MB=64")

	if want := fmt.Sprintf(" memory %d\n", 32<<20); !strings.HasSuffix(got, want) {
		t.Errorf("the program reports %q, want GOMEMLIMIT kept, %q", got, want)
	}
}

func TestRuntimeLimitsReadsWhatEachLimitNames(t *testing.T) {
	t.Parallel()

	cpus := runtime.NumCPU()
	cases := map[string]struct {
		procs, memory string
		want          gonsole.RuntimeLimits
	}{
		"values padded with spaces": {" 1 ", " 256 ", gonsole.RuntimeLimits{MaxProcs: 1, MemoryLimitMB: 256}},
		"the most each allows": {strconv.Itoa(cpus), "1073741824",
			gonsole.RuntimeLimits{MaxProcs: cpus, MemoryLimitMB: 1 << 30}},
		"nothing named":     {"", "", gonsole.RuntimeLimits{}},
		"only spaces named": {"   ", "   ", gonsole.RuntimeLimits{}},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			got, err := settings(map[string]string{"MYAPP_MAX_PROCS": tc.procs, "MYAPP_MEMORY_LIMIT_MB": tc.memory}).
				RuntimeLimits()

			if err != nil || got != tc.want {
				t.Errorf("RuntimeLimits() = %+v, %v, want %+v", got, err, tc.want)
			}
		})
	}
}

func TestMainLeavesALimitItCannotReadToCheck(t *testing.T) {
	t.Parallel()

	cases := map[string]struct {
		variables []string
		args      []string
		want      result
	}{
		"a command past a limit out of range": {[]string{"MYAPP_MAX_PROCS=0"}, []string{"report:list"},
			result{code: gonsole.ExitDone, stdout: "quarterly\nyearly\n"}},
		"a command under limits": {[]string{"MYAPP_MAX_PROCS=1", "MYAPP_MEMORY_LIMIT_MB=64"}, []string{"report:list"},
			result{code: gonsole.ExitDone, stdout: "quarterly\nyearly\n"}},
		"a check of a limit out of range": {[]string{"MYAPP_MAX_PROCS=0"}, []string{"check"},
			result{code: gonsole.ExitFailed, stderr: "myapp: MYAPP_MAX_PROCS: must stand above zero, got \"0\"\n"}},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			if got := runExample(t, "", tc.variables, tc.args...); got != tc.want {
				t.Errorf("the run = %+v, want %+v", got, tc.want)
			}
		})
	}
}

func TestRuntimeLimitsRefusesALimitItCannotStand(t *testing.T) {
	t.Parallel()

	cpus := strconv.Itoa(runtime.NumCPU())
	beyond := strconv.Itoa(runtime.NumCPU() + 1)
	procs, memory := "MYAPP_MAX_PROCS", "MYAPP_MEMORY_LIMIT_MB"
	valid := map[string]string{procs: "1", memory: "64"}
	cases := map[string]struct{ key, value, want string }{
		"processors in words":       {procs, "two", `must be a whole number, got "two"`},
		"no processor":              {procs, "0", `must stand above zero, got "0"`},
		"processors below zero":     {procs, "-1", `must stand above zero, got "-1"`},
		"more processors than CPUs": {procs, beyond, `must stand at or below ` + cpus + `, got "` + beyond + `"`},
		"memory in words":           {memory, "lots", `must be a whole number, got "lots"`},
		"no memory":                 {memory, "0", `must stand above zero, got "0"`},
		"memory below zero":         {memory, "-8", `must stand above zero, got "-8"`},
		"memory past a pebibyte":    {memory, "1073741825", `must stand at or below 1073741824, got "1073741825"`},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			values := maps.Clone(valid)
			values[tc.key] = tc.value

			got, err := settings(values).RuntimeLimits()

			if want := tc.key + ": " + tc.want; err == nil || err.Error() != want {
				t.Errorf("RuntimeLimits() error = %v, want %q", err, want)
			}
			if got != (gonsole.RuntimeLimits{}) {
				t.Errorf("RuntimeLimits() = %+v, want no limit beside the error", got)
			}
		})
	}
}

func TestRuntimeLimitsNamesEveryLimitItCannotStand(t *testing.T) {
	t.Parallel()

	_, err := settings(map[string]string{"MYAPP_MAX_PROCS": "0", "MYAPP_MEMORY_LIMIT_MB": "lots"}).RuntimeLimits()

	want := `MYAPP_MAX_PROCS: must stand above zero, got "0"` + "\n" +
		`MYAPP_MEMORY_LIMIT_MB: must be a whole number, got "lots"`
	if err == nil || err.Error() != want {
		t.Errorf("RuntimeLimits() error = %v, want %q", err, want)
	}
}

func TestCheckRefusesARuntimeLimitItCannotStand(t *testing.T) {
	t.Parallel()

	p := sound()
	p.Env = settings(map[string]string{"MYAPP_MAX_PROCS": "0"})

	got := execute(t, p, "check")

	if got.code != gonsole.ExitFailed {
		t.Errorf("code = %d, want %d", got.code, gonsole.ExitFailed)
	}
	if want := "myapp: MYAPP_MAX_PROCS: must stand above zero, got \"0\"\n"; got.stderr != want {
		t.Errorf("stderr = %q, want %q", got.stderr, want)
	}
}

func TestCheckNamesARuntimeLimitBesideTheOtherSettings(t *testing.T) {
	t.Parallel()

	p := sound()
	p.Env = settings(map[string]string{"MYAPP_MAX_PROCS": "0", "MYAPP_BATCH": "many"})
	p.Validate = func(_ context.Context, call gonsole.Call) error {
		_, err := call.Env.Count("BATCH", 1)
		return err
	}

	got := execute(t, p, "check")

	want := "myapp: MYAPP_MAX_PROCS: must stand above zero, got \"0\"\n" +
		"myapp: MYAPP_BATCH: must be a whole number, got \"many\"\n"
	if got.code != gonsole.ExitFailed || got.stderr != want {
		t.Errorf("check = %d with %q, want %d with %q", got.code, got.stderr, gonsole.ExitFailed, want)
	}
}

func TestBareServeRefusesARuntimeLimitBeforeItServes(t *testing.T) {
	t.Parallel()

	served := false
	p := sound()
	p.BareServes = true
	p.Env = settings(map[string]string{"MYAPP_MAX_PROCS": "0"})
	p.Serve = func(context.Context, gonsole.Call) error {
		served = true
		return nil
	}

	got := execute(t, p)

	if got.code != gonsole.ExitFailed || served {
		t.Errorf("a bare run = %d with the server run %v, want %d and the server refused", got.code, served,
			gonsole.ExitFailed)
	}
}

func TestServeRefusesARuntimeLimitBeforeItServes(t *testing.T) {
	t.Parallel()

	served := false
	p := sound()
	p.Env = settings(map[string]string{"MYAPP_MEMORY_LIMIT_MB": "0"})
	p.Serve = func(context.Context, gonsole.Call) error {
		served = true
		return nil
	}

	got := execute(t, p, "serve")

	if got.code != gonsole.ExitFailed {
		t.Errorf("code = %d, want %d", got.code, gonsole.ExitFailed)
	}
	if want := "myapp: MYAPP_MEMORY_LIMIT_MB: must stand above zero, got \"0\"\n"; got.stderr != want {
		t.Errorf("stderr = %q, want %q", got.stderr, want)
	}
	if served {
		t.Error("the server ran, want it refused before serving")
	}
}

func TestServeRunsTheServerUnderLimitsItCanRead(t *testing.T) {
	t.Parallel()

	served := false
	p := sound()
	p.Env = settings(map[string]string{"MYAPP_MAX_PROCS": "1", "MYAPP_MEMORY_LIMIT_MB": "64"})
	p.Serve = func(context.Context, gonsole.Call) error {
		served = true
		return nil
	}

	got := execute(t, p, "serve")

	if got.code != gonsole.ExitDone {
		t.Errorf("code = %d, want %d, stderr %q", got.code, gonsole.ExitDone, got.stderr)
	}
	if !served {
		t.Error("the server never ran, want it served")
	}
}
