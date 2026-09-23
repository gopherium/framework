// SPDX-License-Identifier: Apache-2.0

package gonsole_test

import (
	"bytes"
	"errors"
	"os"
	"os/exec"
	"slices"
	"strings"
	"testing"

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

// runExample runs the example program in its own process over args, stdin and extra variables and answers its output.
func runExample(t *testing.T, stdin string, variables []string, args ...string) result {
	t.Helper()
	inherited := slices.DeleteFunc(os.Environ(), func(entry string) bool { return strings.HasPrefix(entry, "MYAPP_") })
	cmd := exec.CommandContext(t.Context(), os.Args[0], args...)
	cmd.Env = append(append(inherited, exampleSwitch+"=1"), variables...)
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
			[]string{"migrate"}, gonsole.ExitDone, "migrated reports\n", ""},
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
		{"a word no command owns", "", nil, []string{"reprot"}, gonsole.ExitMisused, "",
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
