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
)

// runHandleExample runs the example program that migrates on one handle in its own process over args and variables.
func runHandleExample(t *testing.T, variables []string, args ...string) result {
	t.Helper()
	cmd := exec.CommandContext(t.Context(), os.Args[0], args...)
	inherited := slices.DeleteFunc(os.Environ(), func(entry string) bool { return strings.HasPrefix(entry, "MYAPP_") })
	cmd.Env = append(append(inherited, handleSwitch+"=1"), variables...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	var exited *exec.ExitError
	if err := cmd.Run(); err != nil && !errors.As(err, &exited) {
		t.Fatalf("running the example program: %v", err)
	}
	return result{code: cmd.ProcessState.ExitCode(), stdout: stdout.String(), stderr: stderr.String()}
}

func TestTheHandleExampleMigratesInItsOwnProcess(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name      string
		variables []string
		args      []string
		code      int
		stdout    string
		stderr    string
	}{
		{"a migration on one handle", []string{"MYAPP_DATABASE_URL=" + databaseAddress}, []string{"migrate"},
			gonsole.ExitDone, "migrated accounts\nmigrated reports\n", ""},
		{"a migration without its setting", nil, []string{"migrate"}, gonsole.ExitFailed, "",
			"myapp: MYAPP_DATABASE_URL is required\n"},
		{"a check that stays offline", []string{"MYAPP_DATABASE_URL=" + databaseAddress}, []string{"check"},
			gonsole.ExitDone, "settings, plugins and command names are valid\n", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			got := runHandleExample(t, tc.variables, tc.args...)

			if got.code != tc.code || got.stdout != tc.stdout || got.stderr != tc.stderr {
				t.Errorf("code %d, stdout %q, stderr %q, want %d, %q and %q",
					got.code, got.stdout, got.stderr, tc.code, tc.stdout, tc.stderr)
			}
		})
	}
}
