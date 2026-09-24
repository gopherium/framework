// SPDX-License-Identifier: Apache-2.0

package gonsole_test

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"reflect"
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

// dryRunNotice is the line a dry run of myapp ends with on stderr.
const dryRunNotice = "myapp: dry run, nothing changed, pass -yes to apply\n"

// drafting returns a program whose report:create writes and prints what it would do or what it did.
func drafting() gonsole.Program {
	return single(gonsole.Command{
		Name:    "report:create",
		Summary: "create a report",
		Args:    []string{"title"},
		Writes:  true,
		Run: func(_ context.Context, call gonsole.Call) error {
			verb := "would create"
			if call.Apply {
				verb = "created"
			}
			_, err := fmt.Fprintf(call.Stdout, "%s %s\n", verb, call.Args[0])
			return err
		},
	})
}

func TestRunKeepsAWritingCommandADryRunUntilYes(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name   string
		args   []string
		stdout string
		stderr string
	}{
		{"no switch", []string{"Q3"}, "would create Q3\n", dryRunNotice},
		{"the switch before the argument", []string{"-yes", "Q3"}, "created Q3\n", ""},
		{"the switch after the argument", []string{"Q3", "-yes"}, "created Q3\n", ""},
		{"the switch turned off", []string{"-yes=false", "Q3"}, "would create Q3\n", dryRunNotice},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			got := execute(t, drafting(), append([]string{"report:create"}, tc.args...)...)

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

func TestRunPrintsNoDryRunNoticeAfterAFailure(t *testing.T) {
	t.Parallel()

	p := drafting()
	p.Commands[0].Run = func(context.Context, gonsole.Call) error { return errors.New("report store is down") }

	got := execute(t, p, "report:create", "Q3")

	if got.code != gonsole.ExitFailed {
		t.Errorf("code = %d, want %d", got.code, gonsole.ExitFailed)
	}
	if want := "myapp: report store is down\n"; got.stderr != want {
		t.Errorf("stderr = %q, want %q", got.stderr, want)
	}
}

func TestRunAppliesACommandThatDoesNotWrite(t *testing.T) {
	t.Parallel()

	cmd := echo("report:list")
	cmd.Run = func(_ context.Context, call gonsole.Call) error {
		_, err := fmt.Fprintf(call.Stdout, "apply=%t\n", call.Apply)
		return err
	}

	got := execute(t, single(cmd), "report:list")

	if got.stdout != "apply=true\n" {
		t.Errorf("stdout = %q, want apply=true", got.stdout)
	}
	if got.stderr != "" {
		t.Errorf("stderr = %q, want no dry run notice", got.stderr)
	}
}

func TestRunHandsTheCommandTheJSONSwitch(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name   string
		args   []string
		stdout string
	}{
		{"without the switch", nil, "json=false\n"},
		{"with the switch", []string{"-json"}, "json=true\n"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			cmd := echo("report:list")
			cmd.JSON = true
			cmd.Run = func(_ context.Context, call gonsole.Call) error {
				_, err := fmt.Fprintf(call.Stdout, "json=%t\n", call.JSON)
				return err
			}

			got := execute(t, single(cmd), append([]string{"report:list"}, tc.args...)...)

			if got.stdout != tc.stdout {
				t.Errorf("stdout = %q, want %q, stderr %q", got.stdout, tc.stdout, got.stderr)
			}
		})
	}
}

func TestRunRefusesAnEngineSwitchTheCommandDoesNotOffer(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name    string
		program gonsole.Program
		args    []string
		stderr  string
	}{
		{"yes on a command that does not write", single(echo("report:list")), []string{"report:list", "-yes"},
			"myapp: report:list: flag provided but not defined: -yes\n"},
		{"json on a command without a document", drafting(), []string{"report:create", "-json", "Q3"},
			"myapp: report:create: flag provided but not defined: -json\n"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			got := execute(t, tc.program, tc.args...)

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

func TestRunReadsTheTwoEngineSwitchesApart(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name   string
		args   []string
		stdout string
		stderr string
	}{
		{"the apply switch alone", []string{"-yes", "Q3"}, "json=false apply=true\n", ""},
		{"the document switch alone", []string{"-json", "Q3"}, "json=true apply=false\n", dryRunNotice},
		{"both switches", []string{"-yes", "-json", "Q3"}, "json=true apply=true\n", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			p := drafting()
			p.Commands[0].JSON = true
			p.Commands[0].Run = func(_ context.Context, call gonsole.Call) error {
				_, err := fmt.Fprintf(call.Stdout, "json=%t apply=%t\n", call.JSON, call.Apply)
				return err
			}

			got := execute(t, p, append([]string{"report:create"}, tc.args...)...)

			if got.stdout != tc.stdout {
				t.Errorf("stdout = %q, want %q", got.stdout, tc.stdout)
			}
			if got.stderr != tc.stderr {
				t.Errorf("stderr = %q, want %q", got.stderr, tc.stderr)
			}
		})
	}
}

func TestRunKeepsTheDryRunDocumentAloneOnStdout(t *testing.T) {
	t.Parallel()

	p := drafting()
	p.Commands[0].JSON = true
	p.Commands[0].Run = func(_ context.Context, call gonsole.Call) error {
		return call.Encode(map[string]any{"title": call.Args[0], "applied": call.Apply})
	}

	got := execute(t, p, "report:create", "-json", "Q3")

	want := `{
  "applied": false,
  "title": "Q3"
}
`
	if got.stdout != want {
		t.Errorf("stdout = %q, want %q", got.stdout, want)
	}
	if got.stderr != dryRunNotice {
		t.Errorf("stderr = %q, want %q", got.stderr, dryRunNotice)
	}
}

// flagsSeen are the flags each hook of one run found in its call.
type flagsSeen struct {
	authorize map[string]string
	run       map[string]string
	record    map[string]string
}

// flagging returns a program whose report:create writes, answers JSON, acts and notes the flags every hook sees in s.
func flagging(s *flagsSeen) gonsole.Program {
	p := single(gonsole.Command{
		Name: "report:create", Summary: "create a report", Args: []string{"title"}, Writes: true, JSON: true,
		Capability: "manage_reports",
		Flags: func(fs *flag.FlagSet) {
			fs.String("owner", "", "email address of the owner")
			fs.Bool("draft", false, "keep the report as a draft")
		},
		Run: func(_ context.Context, call gonsole.Call) error {
			s.run = call.Flags
			return nil
		},
	})
	p.Authorize = func(_ context.Context, call gonsole.Call, _ string) error {
		s.authorize = call.Flags
		return nil
	}
	p.Record = func(_ context.Context, call gonsole.Call, _ string) error {
		s.record = call.Flags
		return nil
	}
	return p
}

func TestCallHoldsTheCommandsOwnFlagsTheLineSet(t *testing.T) {
	t.Parallel()

	const owner = "maria.perez@example.com"
	cases := []struct {
		name string
		args []string
		want map[string]string
	}{
		{"no flag", []string{"Q3"}, map[string]string{}},
		{"a flag and a switch", []string{"-owner", owner, "-draft", "Q3"},
			map[string]string{"owner": owner, "draft": "true"}},
		{"a switch set to its default", []string{"-draft=false", "Q3"}, map[string]string{"draft": "false"}},
		{"a flag given twice", []string{"-owner", "someone@example.com", "-owner", owner, "Q3"},
			map[string]string{"owner": owner}},
		{"a flag after a double dash", []string{"--", "-owner"}, map[string]string{}},
		{"the engine flags beside a flag", []string{"-json", "-owner", owner, "Q3"}, map[string]string{"owner": owner}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			var seen flagsSeen
			args := append([]string{"report:create", "-yes", "-as", actingAccount}, tc.args...)

			got := execute(t, flagging(&seen), args...)

			if got.code != gonsole.ExitDone {
				t.Fatalf("code = %d, want %d, stderr %q", got.code, gonsole.ExitDone, got.stderr)
			}
			for hook, flags := range map[string]map[string]string{
				"Authorize": seen.authorize, "Run": seen.run, "Record": seen.record,
			} {
				if !reflect.DeepEqual(flags, tc.want) {
					t.Errorf("%s saw Flags %#v, want %#v", hook, flags, tc.want)
				}
			}
		})
	}
}
