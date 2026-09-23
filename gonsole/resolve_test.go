// SPDX-License-Identifier: Apache-2.0

package gonsole_test

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/gopherium/framework/gonsole"
)

// echo returns a command called name taking args that prints its name and the arguments it received.
func echo(name string, args ...string) gonsole.Command {
	return gonsole.Command{
		Name:    name,
		Summary: "print the arguments",
		Args:    args,
		Run: func(_ context.Context, call gonsole.Call) error {
			_, err := fmt.Fprintln(call.Stdout, strings.Join(append([]string{name}, call.Args...), " "))
			return err
		},
	}
}

// reports returns a program called myapp with a status command, a report namespace and one old spelling.
func reports() gonsole.Program {
	return gonsole.Program{
		Name: "myapp",
		Commands: []gonsole.Command{
			echo("report:revoke", "id"),
			echo("status"),
			echo("report:create", "title"),
			echo("report:list"),
		},
		Renamed: map[string]string{"report new": "report:create", "report all": "report:list"},
	}
}

func TestRunRunsTheCommandTheLineNames(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name   string
		args   []string
		stdout string
		stderr string
	}{
		{"a command without a namespace", []string{"status"}, "status\n", ""},
		{"a command in a namespace", []string{"report:list"}, "report:list\n", ""},
		{"a command in a namespace with its argument", []string{"report:create", "Q3"}, "report:create Q3\n", ""},
		{
			"an old two word spelling",
			[]string{"report", "new", "Q3"},
			"report:create Q3\n",
			"myapp: \"report new\" is deprecated, use \"report:create\"\n",
		},
		{
			"an old two word spelling with nothing after it",
			[]string{"report", "all"},
			"report:list\n",
			"myapp: \"report all\" is deprecated, use \"report:list\"\n",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			got := execute(t, reports(), tc.args...)

			if got.code != gonsole.ExitDone {
				t.Errorf("code = %d, want %d, stderr %q", got.code, gonsole.ExitDone, got.stderr)
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

func TestRunRefusesALineThatNamesNoCommand(t *testing.T) {
	t.Parallel()

	everyCommand := `run "myapp list" to see every command`
	reportCommands := "want report:create, report:list or report:revoke"
	cases := []struct {
		name   string
		args   []string
		stderr string
	}{
		{"an unknown command", []string{"reprot"}, `myapp: unknown command "reprot", ` + everyCommand + "\n"},
		{"a flag in place of a command", []string{"-v"}, `myapp: unknown command "-v", ` + everyCommand + "\n"},
		{"an unknown namespace", []string{"audit:list"}, `myapp: unknown command "audit:list", ` + everyCommand + "\n"},
		{"a command used as a namespace", []string{"status:x"}, `myapp: unknown command "status:x", ` +
			everyCommand + "\n"},
		{"a namespace alone", []string{"report"}, `myapp: unknown command "report", ` + reportCommands + "\n"},
		{"an unknown command in a namespace", []string{"report:delete"}, `myapp: unknown command "report:delete", ` +
			reportCommands + "\n"},
		{"a namespace with nothing after its colon", []string{"report:"}, `myapp: unknown command "report:", ` +
			reportCommands + "\n"},
		{"an old spelling with another second word", []string{"report", "old"}, `myapp: unknown command "report", ` +
			reportCommands + "\n"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			got := execute(t, reports(), tc.args...)

			if got.code != gonsole.ExitMisused {
				t.Errorf("code = %d, want %d", got.code, gonsole.ExitMisused)
			}
			if got.stderr != tc.stderr {
				t.Errorf("stderr = %q, want %q", got.stderr, tc.stderr)
			}
			if got.stdout != "" {
				t.Errorf("stdout = %q, want empty", got.stdout)
			}
		})
	}
}

func TestRunNamesTheAlternativesOfANamespaceInPlainEnglish(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name     string
		commands []gonsole.Command
		stderr   string
	}{
		{"one command", []gonsole.Command{echo("report:list")}, "want report:list"},
		{"two commands", []gonsole.Command{echo("report:list"), echo("report:create")}, "want report:create or report:list"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			got := execute(t, gonsole.Program{Name: "myapp", Commands: tc.commands}, "report")

			want := `myapp: unknown command "report", ` + tc.stderr + "\n"
			if got.stderr != want {
				t.Errorf("stderr = %q, want %q", got.stderr, want)
			}
		})
	}
}
