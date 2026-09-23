// SPDX-License-Identifier: Apache-2.0

package gonsole_test

import (
	"context"
	"flag"
	"fmt"
	"testing"

	"github.com/gopherium/framework/gonsole"
)

// filing returns a program whose report:create takes a title, an owner flag and a draft switch and prints what it read.
func filing() gonsole.Program {
	var owner string
	var draft bool
	return single(gonsole.Command{
		Name:    "report:create",
		Summary: "create a report",
		Args:    []string{"title"},
		Flags: func(fs *flag.FlagSet) {
			fs.StringVar(&owner, "owner", "", "email address of the owner")
			fs.BoolVar(&draft, "draft", false, "keep the report as a draft")
		},
		Run: func(_ context.Context, call gonsole.Call) error {
			_, err := fmt.Fprintf(call.Stdout, "title=%s owner=%s draft=%t\n", call.Args[0], owner, draft)
			return err
		},
	})
}

func TestRunReadsFlagsAndArgumentsInAnyOrder(t *testing.T) {
	t.Parallel()

	const owner = "maria.perez@example.com"
	cases := []struct {
		name   string
		args   []string
		stdout string
	}{
		{"no flags", []string{"Q3"}, "title=Q3 owner= draft=false\n"},
		{"flags first", []string{"-owner", owner, "-draft", "Q3"}, "title=Q3 owner=" + owner + " draft=true\n"},
		{"the argument first", []string{"Q3", "-owner", owner, "-draft"}, "title=Q3 owner=" + owner + " draft=true\n"},
		{"the argument between flags", []string{"-draft", "Q3", "-owner", owner},
			"title=Q3 owner=" + owner + " draft=true\n"},
		{"a double dash before a dashed argument", []string{"-owner", owner, "--", "-Q3"},
			"title=-Q3 owner=" + owner + " draft=false\n"},
		{"a double dash after a switch", []string{"-draft", "--", "-Q3"}, "title=-Q3 owner= draft=true\n"},
		{"a double dash after a flag with an equals sign", []string{"-owner=" + owner, "--", "-Q3"},
			"title=-Q3 owner=" + owner + " draft=false\n"},
		{"a double dash as the value of a flag", []string{"-owner", "--", "Q3", "-draft"}, "title=Q3 owner=-- draft=true\n"},
		{"a double dash after a value that looks like a flag", []string{"-owner", "-unset", "--", "-Q3"},
			"title=-Q3 owner=-unset draft=false\n"},
		{"a double dash as the value of a double dash flag", []string{"--owner", "--", "Q3", "-draft"},
			"title=Q3 owner=-- draft=true\n"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			got := execute(t, filing(), append([]string{"report:create"}, tc.args...)...)

			if got.code != gonsole.ExitDone {
				t.Errorf("code = %d, want %d, stderr %q", got.code, gonsole.ExitDone, got.stderr)
			}
			if got.stdout != tc.stdout {
				t.Errorf("stdout = %q, want %q", got.stdout, tc.stdout)
			}
		})
	}
}

func TestRunRefusesAMalformedCommandLine(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name   string
		args   []string
		stderr string
	}{
		{"a missing argument", []string{"report:create"}, "myapp: report:create wants <title>\n"},
		{"a missing argument after a switch", []string{"report:create", "-draft"}, "myapp: report:create wants <title>\n"},
		{"a stray argument", []string{"report:create", "Q3", "Q4"}, "myapp: report:create takes 1 argument, got 2\n"},
		{"a flag after a double dash", []string{"report:create", "--", "Q3", "-draft"},
			"myapp: report:create takes 1 argument, got 2\n"},
		{"flags after a double dash that follows a switch", []string{"report:create", "-draft", "--", "-Q3", "-owner", "x"},
			"myapp: report:create takes 1 argument, got 3\n"},
		{"a flag after a double dash that follows a plain value", []string{"report:create", "-owner", "owner", "--", "Q3",
			"-draft"}, "myapp: report:create takes 1 argument, got 2\n"},
		{"a flag after a double dash that follows an unknown flag name as a value", []string{"report:create", "-owner",
			"-unset", "--", "-Q3", "-draft"}, "myapp: report:create takes 1 argument, got 2\n"},
		{"a flag after a double dash that follows a flag name as a value", []string{"report:create", "-owner", "-owner",
			"--", "Q3", "-draft"}, "myapp: report:create takes 1 argument, got 2\n"},
		{"a flag after a double dash that follows a double dash flag name as a value", []string{"report:create",
			"-owner", "--owner", "--", "Q3", "-draft"}, "myapp: report:create takes 1 argument, got 2\n"},
		{"a flag after a double dash that follows a flag with an equals sign", []string{"report:create",
			"-owner=x", "--", "Q3", "-draft"}, "myapp: report:create takes 1 argument, got 2\n"},
		{"an unknown flag", []string{"report:create", "-bogus", "Q3"},
			"myapp: report:create: flag provided but not defined: -bogus\n"},
		{"a switch given a value it cannot read", []string{"report:create", "-draft=maybe", "Q3"},
			"myapp: report:create: invalid boolean value \"maybe\" for -draft: parse error\n"},
		{"a flag missing its value", []string{"report:create", "Q3", "-owner"},
			"myapp: report:create: flag needs an argument: -owner\n"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			got := execute(t, filing(), tc.args...)

			if got.code != gonsole.ExitMisused {
				t.Errorf("code = %d, want %d", got.code, gonsole.ExitMisused)
			}
			if firstLine(got.stderr) != tc.stderr {
				t.Errorf("stderr opens with %q, want %q", firstLine(got.stderr), tc.stderr)
			}
			if got.stdout != "" {
				t.Errorf("stdout = %q, want the command never run", got.stdout)
			}
		})
	}
}

func TestRunCountsArgumentsInPlainEnglish(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name   string
		cmd    gonsole.Command
		args   []string
		stderr string
	}{
		{"none wanted", echo("report:list"), []string{"extra"}, "myapp: report:list takes no arguments, got 1\n"},
		{"two wanted, three given", echo("report:move", "id", "folder"), []string{"a", "b", "c"},
			"myapp: report:move takes 2 arguments, got 3\n"},
		{"two wanted, one given", echo("report:move", "id", "folder"), []string{"a"},
			"myapp: report:move wants <folder>\n"},
		{"two wanted, none given", echo("report:move", "id", "folder"), nil, "myapp: report:move wants <id>\n"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			got := execute(t, single(tc.cmd), append([]string{tc.cmd.Name}, tc.args...)...)

			if got.code != gonsole.ExitMisused {
				t.Errorf("code = %d, want %d", got.code, gonsole.ExitMisused)
			}
			if firstLine(got.stderr) != tc.stderr {
				t.Errorf("stderr opens with %q, want %q", firstLine(got.stderr), tc.stderr)
			}
		})
	}
}

func TestRunRefusesAFlagOnACommandWithoutFlags(t *testing.T) {
	t.Parallel()

	got := execute(t, single(echo("report:list")), "report:list", "-all")

	if got.code != gonsole.ExitMisused {
		t.Errorf("code = %d, want %d", got.code, gonsole.ExitMisused)
	}
	if want := "myapp: report:list: flag provided but not defined: -all\n"; firstLine(got.stderr) != want {
		t.Errorf("stderr opens with %q, want %q", firstLine(got.stderr), want)
	}
}

func TestRunNamesTheFlagSetAfterTheCommand(t *testing.T) {
	t.Parallel()

	var named string
	cmd := echo("report:list")
	cmd.Flags = func(fs *flag.FlagSet) { named = fs.Name() }

	execute(t, single(cmd), "report:list")

	if named != "report:list" {
		t.Errorf("flag set name = %q, want report:list", named)
	}
}
