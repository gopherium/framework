// SPDX-License-Identifier: Apache-2.0

package gonsole_test

import (
	"bufio"
	"bytes"
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

func TestMain(m *testing.M) {
	if os.Getenv(exampleSwitch) == "1" {
		os.Exit(gonsole.Main(exampleapp.Program(os.Getenv)))
	}
	os.Exit(m.Run())
}

// exampleEnvironment returns the environment of the example program, no inherited MYAPP_ variable and variables added.
func exampleEnvironment(variables ...string) []string {
	inherited := slices.DeleteFunc(os.Environ(), func(entry string) bool { return strings.HasPrefix(entry, "MYAPP_") })
	return append(append(inherited, exampleSwitch+"=1"), variables...)
}

// runExample runs the example program in its own process over args, stdin and extra variables and answers its output.
func runExample(t *testing.T, stdin string, variables []string, args ...string) result {
	t.Helper()
	cmd := exec.CommandContext(t.Context(), os.Args[0], args...)
	cmd.Env = exampleEnvironment(variables...)
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
  report:list    list every report
  report:revoke  revoke one report
`

func TestExamplePluginsDescribeTheDemoGroup(t *testing.T) {
	t.Parallel()

	p := exampleapp.Program(func(string) string { return "" })

	loaded, err := p.Plugins(t.Context(), gonsole.Call{Describe: true, Env: gonsole.Env{Prefix: "MYAPP_"}})

	if err != nil || loaded.Failed != nil || !slices.Equal(namespaces(loaded.Groups), []string{"demo"}) {
		t.Fatalf("Plugins() = %v failing %v, %v, want demo, nil and nil", namespaces(loaded.Groups), loaded.Failed, err)
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
			gonsole.ExitDone, "would sync the demo\n", dryRun},
		{"a plugin write without its setting", "", nil, []string{"demo:sync"}, gonsole.ExitFailed, "",
			"myapp: MYAPP_DATABASE_URL is required\n"},
		{"the help of a plugin command without its setting", "", nil, []string{"demo:sync", "-h"}, gonsole.ExitDone,
			syncPage, ""},
		{"the listing without the database setting", "", nil, []string{"list"}, gonsole.ExitDone, exampleListing, ""},
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

			cmd := exec.CommandContext(t.Context(), os.Args[0], "serve")
			cmd.Env = exampleEnvironment("MYAPP_ADDR=127.0.0.1:0")
			stderr, err := cmd.StderrPipe()
			if err != nil {
				t.Fatalf("piping stderr: %v", err)
			}
			if err := cmd.Start(); err != nil {
				t.Fatalf("starting the example program: %v", err)
			}
			lines := bufio.NewScanner(stderr)
			address := listeningAddress(t, lines)

			body := get(t, address)
			if err := cmd.Process.Signal(signal); err != nil {
				t.Fatalf("signalling: %v", err)
			}
			var rest []string
			for lines.Scan() {
				rest = append(rest, lines.Text())
			}
			err = cmd.Wait()

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

func TestMainLetsASecondSignalEndACommandThatIgnoresTheFirst(t *testing.T) {
	t.Parallel()

	cmd := exec.CommandContext(t.Context(), os.Args[0], "report:create", "-yes", "Q3")
	cmd.Env = exampleEnvironment()
	stdin, err := cmd.StdinPipe()
	if err != nil {
		t.Fatalf("piping stdin: %v", err)
	}
	defer func() { _ = stdin.Close() }()
	if err := cmd.Start(); err != nil {
		t.Fatalf("starting the example program: %v", err)
	}
	exited := make(chan error, 1)
	go func() { exited <- cmd.Wait() }()
	time.Sleep(300 * time.Millisecond)

	_ = cmd.Process.Signal(os.Interrupt)
	select {
	case err := <-exited:
		t.Fatalf("the first signal ended the program with %v, want the run cancelled only", err)
	case <-time.After(200 * time.Millisecond):
	}
	_ = cmd.Process.Signal(os.Interrupt)

	select {
	case <-exited:
		status, known := cmd.ProcessState.Sys().(syscall.WaitStatus)
		if !known || !status.Signaled() {
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
