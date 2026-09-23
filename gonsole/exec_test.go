// SPDX-License-Identifier: Apache-2.0

package gonsole_test

import (
	"bytes"
	"errors"
	"os"
	"os/exec"
	"strings"
	"testing"

	"github.com/gopherium/framework/gonsole"
	"github.com/gopherium/framework/gonsole/internal/exampleapp"
)

// exampleSwitch is the variable that turns the test binary into the example program.
const exampleSwitch = "GONSOLE_EXEC_EXAMPLE"

func TestMain(m *testing.M) {
	if os.Getenv(exampleSwitch) == "1" {
		os.Exit(gonsole.Main(exampleapp.Program()))
	}
	os.Exit(m.Run())
}

// runExample runs the example program in its own process over args and stdin and answers its exit code and output.
func runExample(t *testing.T, stdin string, args ...string) result {
	t.Helper()
	cmd := exec.CommandContext(t.Context(), os.Args[0], args...)
	cmd.Env = append(os.Environ(), exampleSwitch+"=1")
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

func TestMainExitsWithTheCodeOfTheRun(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name   string
		stdin  string
		args   []string
		code   int
		stdout string
		stderr string
	}{
		{"a command that succeeds", "", []string{"report:list"}, gonsole.ExitDone, "quarterly\nyearly\n", ""},
		{"a command that reads its input", "sales by region\n", []string{"report:create", "Q3"}, gonsole.ExitDone,
			"created Q3: sales by region\n", ""},
		{"a command that fails", "", []string{"report:revoke", "monthly"}, gonsole.ExitFailed, "",
			"myapp: report \"monthly\" does not exist\n"},
		{"a word no command owns", "", []string{"reprot"}, gonsole.ExitMisused, "",
			"myapp: unknown command \"reprot\", run \"myapp list\" to see every command\n"},
		{"a flag no command defines", "", []string{"report:list", "-bogus"}, gonsole.ExitMisused, "",
			`myapp: report:list: flag provided but not defined: -bogus

list every report

Usage:
  myapp report:list
`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			got := runExample(t, tc.stdin, tc.args...)

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
