// SPDX-License-Identifier: Apache-2.0

package gonsole_test

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"os"
	"os/exec"
	"slices"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/gopherium/framework/gonsole"
	"github.com/gopherium/framework/gonsole/internal/exampleapp"
)

// exampleSwitch is the variable that turns the test binary into the example program.
const exampleSwitch = "GONSOLE_EXEC_EXAMPLE"

// handleSwitch is the variable that turns the test binary into the example program that migrates on one handle.
const handleSwitch = "GONSOLE_EXEC_HANDLE"

func TestMain(m *testing.M) {
	if os.Getenv(exampleSwitch) == "1" {
		os.Exit(gonsole.Main(exampleapp.Program(os.Getenv)))
	}
	if os.Getenv(handleSwitch) == "1" {
		os.Exit(gonsole.Main(exampleapp.HandleProgram(os.Getenv)))
	}
	if os.Getenv(limitsSwitch) == "1" {
		os.Exit(gonsole.Main(limitsProgram()))
	}
	os.Exit(m.Run())
}

// settingEntry reports whether the environment entry sets a MYAPP_ variable or PORT.
func settingEntry(entry string) bool {
	return strings.HasPrefix(entry, "MYAPP_") || strings.HasPrefix(entry, "PORT=")
}

// exampleEnvironment returns the environment of the example program, no inherited setting and variables added.
func exampleEnvironment(variables ...string) []string {
	inherited := slices.DeleteFunc(os.Environ(), settingEntry)
	return append(append(inherited, exampleSwitch+"=1"), variables...)
}

// exampleBound is how long a test lets the example program run before it kills it.
const exampleBound = 10 * time.Second

// exampleCommand returns the example program over args and extra variables, killed once exampleBound passes.
func exampleCommand(t *testing.T, variables []string, args ...string) *exec.Cmd {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), exampleBound)
	t.Cleanup(cancel)
	cmd := exec.CommandContext(ctx, os.Args[0], args...)
	cmd.Env = exampleEnvironment(variables...)
	return cmd
}

// launch starts cmd and returns the lines of its stderr.
func launch(t *testing.T, cmd *exec.Cmd) *bufio.Scanner {
	t.Helper()
	stderr, err := cmd.StderrPipe()
	if err != nil {
		t.Fatalf("piping stderr: %v", err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatalf("starting the example program: %v", err)
	}
	return bufio.NewScanner(stderr)
}

// untilExit returns every line left in lines.
func untilExit(lines *bufio.Scanner) []string {
	var left []string
	for lines.Scan() {
		left = append(left, lines.Text())
	}
	return left
}

// signalled sends signal to the started cmd and returns the lines it writes to stderr until it exits.
func signalled(t *testing.T, cmd *exec.Cmd, lines *bufio.Scanner, signal os.Signal) []string {
	t.Helper()
	if err := cmd.Process.Signal(signal); err != nil {
		t.Fatalf("signalling: %v", err)
	}
	return untilExit(lines)
}

// runExample runs the example program in its own process over args, stdin and extra variables and answers its output.
func runExample(t *testing.T, stdin string, variables []string, args ...string) result {
	t.Helper()
	cmd := exampleCommand(t, variables, args...)
	cmd.Stdin = strings.NewReader(stdin)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	var exited *exec.ExitError
	if err := cmd.Run(); err != nil && !errors.As(err, &exited) {
		t.Fatalf("running the example program: %v", err)
	}
	return result{code: cmd.ProcessState.ExitCode(), stdout: stdout.String(), stderr: stderr.String()}
}

// importing is the line report:import writes to stderr before it reads its input.
const importing = "reading the reports to import from the input"

// exampleListing is the listing the example program prints.
const exampleListing = `myapp

Usage:
  myapp <command> [flags] [arguments]

` + intro + `
Available commands:
  check          check every setting, every plugin and every command name
  help           print the help of one command
  list           list every command
  migrate        apply every schema step
  seed           store the demo data
  serve          run the server
  version        print the version
 demo            plugin
  demo:sync      sync the demo
 report
  report:create  create a report
  report:import  import one report per line of the input
  report:list    list every report
  report:revoke  revoke one report
`

func TestExamplePluginsDescribeTheDemoGroup(t *testing.T) {
	t.Parallel()

	p := exampleapp.Program(func(string) string { return "" })

	loaded, err := p.Plugins(t.Context(), gonsole.Call{Describe: true, Env: gonsole.Env{Prefix: "MYAPP_"}})

	if err != nil {
		t.Fatalf("Plugins() = %v, want nil", err)
	}
	if loaded.Failed != nil {
		t.Fatalf("Plugins() failing %v, want nil", loaded.Failed)
	}
	if !slices.Equal(namespaces(loaded.Groups), []string{"demo"}) {
		t.Fatalf("Plugins() = %v, want demo", namespaces(loaded.Groups))
	}
	if err := p.Check(loaded); err != nil {
		t.Errorf("Check() = %v, want nil", err)
	}
}

func TestMainExitsWithTheCodeOfTheRun(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name      string
		stdin     string
		variables []string
		args      []string
		code      int
		stdout    string
		stderr    string
	}{
		{"a command that succeeds", "", nil, []string{"report:list"}, gonsole.ExitDone, "quarterly\nyearly\n", ""},
		{"a migration that reads its setting", "", []string{"MYAPP_DATABASE_URL=" + databaseAddress},
			[]string{"migrate"}, gonsole.ExitDone, "migrated reports\nmigrated plugins\n", ""},
		{"a migration without its setting", "", nil, []string{"migrate"}, gonsole.ExitFailed, "",
			"myapp: MYAPP_DATABASE_URL is required\n"},
		{"a write that reads its input", "sales by region\n", nil, []string{"report:create", "-yes", "Q3"},
			gonsole.ExitDone, "created Q3: sales by region\n", ""},
		{"a dry run of a write", "sales by region\n", nil, []string{"report:create", "Q3"}, gonsole.ExitDone,
			"would create Q3: sales by region\n", "myapp: dry run, nothing changed, pass -yes to apply\n"},
		{"a write that reads every line of its input", "quarterly\nyearly\n", nil, []string{"report:import", "-yes"},
			gonsole.ExitDone, "imported 2 reports\n", importing + "\n"},
		{"an import of names made of several words", "Q3 sales\n\nQ4 plan\n", nil, []string{"report:import", "-yes"},
			gonsole.ExitDone, "imported 2 reports\n", importing + "\n"},
		{"a command that answers a document", "", nil, []string{"report:list", "-json"}, gonsole.ExitDone, `{
  "reports": [
    "quarterly",
    "yearly"
  ]
}
`, ""},
		{"a command that fails", "", nil, []string{"report:revoke", "monthly"}, gonsole.ExitFailed, "",
			"myapp: report \"monthly\" does not exist\n"},
		{"a plugin write", "", []string{"MYAPP_DATABASE_URL=" + databaseAddress}, []string{"demo:sync", "-yes"},
			gonsole.ExitDone, "synced the demo\n", ""},
		{"a dry run of a plugin write", "", []string{"MYAPP_DATABASE_URL=" + databaseAddress}, []string{"demo:sync"},
			gonsole.ExitDone, "would sync the demo\n", dryRunNotice},
		{"a plugin write without its setting", "", nil, []string{"demo:sync"}, gonsole.ExitFailed, "",
			"myapp: MYAPP_DATABASE_URL is required\n"},
		{"the help of a plugin command without its setting", "", nil, []string{"demo:sync", "-h"}, gonsole.ExitDone,
			syncPage, ""},
		{"the listing without the database setting", "", nil, []string{"list"}, gonsole.ExitDone, exampleListing, ""},
		{"a command past a runtime limit out of range", "", []string{"MYAPP_MAX_PROCS=0"}, []string{"report:list"},
			gonsole.ExitDone, "quarterly\nyearly\n", ""},
		{"a command under runtime limits", "", []string{"MYAPP_MAX_PROCS=1", "MYAPP_MEMORY_LIMIT_MB=64"},
			[]string{"report:list"}, gonsole.ExitDone, "quarterly\nyearly\n", ""},
		{"a check of a runtime limit out of range", "", []string{"MYAPP_MAX_PROCS=0"}, []string{"check"},
			gonsole.ExitFailed, "", "myapp: MYAPP_MAX_PROCS: must stand above zero, got \"0\"\n"},
		{"a name no plugin command owns", "", []string{"MYAPP_DATABASE_URL=" + databaseAddress},
			[]string{"demo:nope"}, gonsole.ExitMisused, "", "myapp: unknown command \"demo:nope\", want demo:sync\n"},
		{"a namespace no plugin owns", "", []string{"MYAPP_DATABASE_URL=" + databaseAddress}, []string{"nope:x"},
			gonsole.ExitMisused, "", "myapp: unknown command \"nope:x\", want a command in demo\n"},
		{"a name no command owns", "", nil, []string{"reprot"}, gonsole.ExitMisused, "",
			"myapp: unknown command \"reprot\", run \"myapp list\" to see every command\n"},
		{"a flag no command defines", "", nil, []string{"report:list", "-bogus"}, gonsole.ExitMisused, "",
			`myapp: report:list: flag provided but not defined: -bogus

list every report

Usage:
  myapp report:list [flags]

Flags:
  -json
    	answer one JSON document
`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			got := runExample(t, tc.stdin, tc.variables, tc.args...)

			if got.code != tc.code {
				t.Errorf("code = %d, want %d, stderr %q", got.code, tc.code, got.stderr)
			}
			if got.stdout != tc.stdout {
				t.Errorf("stdout = %q, want %q", got.stdout, tc.stdout)
			}
			if got.stderr != tc.stderr {
				t.Errorf("stderr = %q, want %q", got.stderr, tc.stderr)
			}
		})
	}
}

func TestMainServesUntilASignalEndsTheRun(t *testing.T) {
	t.Parallel()

	for _, signal := range []os.Signal{syscall.SIGTERM, os.Interrupt} {
		t.Run(signal.String(), func(t *testing.T) {
			t.Parallel()

			cmd := exampleCommand(t, []string{"MYAPP_ADDR=127.0.0.1:0"}, "serve")
			lines := launch(t, cmd)
			address := listeningAddress(t, lines)

			body := get(t, address)
			rest := signalled(t, cmd, lines, signal)
			err := cmd.Wait()

			if err != nil {
				t.Errorf("exit = %v, want 0", err)
			}
			if body != "quarterly\nyearly\n" {
				t.Errorf("body = %q, want the report names", body)
			}
			if !slices.ContainsFunc(rest, func(line string) bool { return strings.Contains(line, "shutting down") }) {
				t.Errorf("stderr after the signal = %q, want a shutting down line", rest)
			}
		})
	}
}

func TestMainEndsAnIdleServeWithExitZero(t *testing.T) {
	t.Parallel()

	got := runExample(t, "", []string{"MYAPP_ADDR=127.0.0.1:0", "MYAPP_IDLE_EXIT=200ms"}, "serve")

	if got.code != gonsole.ExitDone {
		t.Errorf("code = %d, want 0 within %v, stderr %q", got.code, exampleBound, got.stderr)
	}
	idle := strings.Index(got.stderr, `msg="no connection for the idle exit" idle_exit=200ms`)
	if down := strings.Index(got.stderr, `msg="shutting down"`); idle < 0 || down < idle {
		t.Errorf("stderr = %q, want the idle line before the shutting down line", got.stderr)
	}
}

// importHeldOpen starts report:import -yes with its input held open and returns it and its exit once it reads.
func importHeldOpen(t *testing.T) (*exec.Cmd, <-chan error) {
	t.Helper()
	cmd := exampleCommand(t, nil, "report:import", "-yes")
	stdin, err := cmd.StdinPipe()
	if err != nil {
		t.Fatalf("piping stdin: %v", err)
	}
	t.Cleanup(func() { _ = stdin.Close() })
	lines := launch(t, cmd)
	if !lines.Scan() || lines.Text() != importing {
		t.Fatalf("stderr opens with %q, want %q before any signal", lines.Text(), importing)
	}
	exited := make(chan error, 1)
	go func() {
		untilExit(lines)
		exited <- cmd.Wait()
	}()
	return cmd, exited
}

// killedBySignal reports whether a signal ended the process state describes.
func killedBySignal(state *os.ProcessState) bool {
	status, known := state.Sys().(syscall.WaitStatus)
	return known && status.Signaled()
}

func TestMainLetsASecondSignalEndACommandThatIgnoresTheFirst(t *testing.T) {
	t.Parallel()

	cmd, exited := importHeldOpen(t)

	_ = cmd.Process.Signal(os.Interrupt)
	select {
	case err := <-exited:
		t.Fatalf("the first signal ended the program with %v, want the run cancelled only", err)
	case <-time.After(200 * time.Millisecond):
	}
	_ = cmd.Process.Signal(os.Interrupt)

	select {
	case <-exited:
		if !killedBySignal(cmd.ProcessState) {
			t.Errorf("state = %v, want the second signal to end the program", cmd.ProcessState)
		}
	case <-time.After(5 * time.Second):
		_ = cmd.Process.Kill()
		t.Errorf("the second signal left the program running")
	}
}

// listeningAddress reads lines until the server says it listens and returns the address it names.
func listeningAddress(t *testing.T, lines *bufio.Scanner) string {
	t.Helper()
	for lines.Scan() {
		if _, address, found := strings.Cut(lines.Text(), "msg=listening addr="); found {
			return address
		}
	}
	t.Fatalf("the example program never said it listens")
	return ""
}
