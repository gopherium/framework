// SPDX-License-Identifier: Apache-2.0

package gonsole_test

import (
	"context"
	"flag"
	"slices"
	"strings"
	"testing"

	"github.com/gopherium/framework/gonsole"
)

// issuing returns a program called myapp whose two report commands need flags, every step and call noted in h.
func issuing(h *hooks) gonsole.Program {
	run := func(_ context.Context, call gonsole.Call) error {
		h.note("run owner=%s title=%s apply=%t", call.Flags["owner"], call.Flags["title"], call.Apply)
		return nil
	}
	return gonsole.Program{
		Name:     "myapp",
		Env:      settings(map[string]string{"MYAPP_PRIMARY_URL": databaseAddress}),
		Database: "PRIMARY_URL",
		Migrations: []gonsole.Step{{Name: "reports", Run: func(context.Context, string) error {
			h.note("step reports")
			return nil
		}}},
		Commands: []gonsole.Command{
			{Name: "report:file", Summary: "file one report", Writes: true, Migrates: true,
				Flags: func(fs *flag.FlagSet) {
					fs.String("owner", "", "`email` address of the owner")
					fs.String("title", "", "title of the report")
				},
				Needs: []string{"owner", "title"}, Run: run},
			{Name: "report:assign", Summary: "assign one report", Capability: "manage_reports",
				Flags: func(fs *flag.FlagSet) { fs.String("owner", "", "`email` address of the new owner") },
				Needs: []string{"owner"}, Run: run},
		},
		Authorize: func(_ context.Context, call gonsole.Call, capability string) error {
			h.note("authorize %s for %s", call.Actor, capability)
			return nil
		},
		Record: func(_ context.Context, call gonsole.Call, command string) error {
			h.note("record %s ran %s", call.Actor, command)
			return nil
		},
	}
}

func TestRunRefusesARunThatLeavesANeededFlagBlank(t *testing.T) {
	t.Parallel()

	const owner = "myapp: report:file wants -owner <email>\n"
	cases := []struct {
		name string
		args []string
		want string
	}{
		{"no owner", []string{"report:file", "-title", "Q3"}, owner},
		{"an empty owner", []string{"report:file", "-owner=", "-title", "Q3"}, owner},
		{"an owner of spaces", []string{"report:file", "-owner", "  ", "-title", "Q3"}, owner},
		{"no owner on a run applied with -yes", []string{"report:file", "-yes", "-title", "Q3"}, owner},
		{"neither flag", []string{"report:file"}, owner},
		{"no title", []string{"report:file", "-owner", actingAccount}, "myapp: report:file wants -title <string>\n"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			var h hooks
			got := execute(t, issuing(&h), tc.args...)

			if got.code != gonsole.ExitMisused {
				t.Errorf("code = %d, want %d", got.code, gonsole.ExitMisused)
			}
			if firstLine(got.stderr) != tc.want {
				t.Errorf("stderr opens with %q, want %q", firstLine(got.stderr), tc.want)
			}
			if len(h.log) != 0 {
				t.Errorf("calls = %q, want no schema step and no run", h.log)
			}
		})
	}
}

func TestRunRunsACommandWhoseNeededFlagsAreSet(t *testing.T) {
	t.Parallel()

	var h hooks
	got := execute(t, issuing(&h), "report:file", "-owner", actingAccount, "-title", "Q3", "-yes")

	if got.code != gonsole.ExitDone {
		t.Fatalf("code = %d with stderr %q, want %d", got.code, got.stderr, gonsole.ExitDone)
	}
	want := []string{"step reports", "run owner=" + actingAccount + " title=Q3 apply=true"}
	if !slices.Equal(h.log, want) {
		t.Errorf("calls = %q, want %q", h.log, want)
	}
}

func TestRunAsksForTheActingAccountBeforeANeededFlag(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		args []string
		want string
	}{
		{"neither -as nor the owner", []string{"report:assign"}, "myapp: report:assign wants -as <email>\n"},
		{"-as without the owner", []string{"report:assign", "-as", actingAccount},
			"myapp: report:assign wants -owner <email>\n"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			var h hooks
			got := execute(t, issuing(&h), tc.args...)

			if got.code != gonsole.ExitMisused || firstLine(got.stderr) != tc.want {
				t.Errorf("code %d with stderr opening %q, want %d and %q", got.code, firstLine(got.stderr),
					gonsole.ExitMisused, tc.want)
			}
			if len(h.log) != 0 {
				t.Errorf("calls = %q, want no check of the acting account and no run", h.log)
			}
		})
	}
}

func TestHelpPageAnswersWithoutTheNeededFlags(t *testing.T) {
	t.Parallel()

	var h hooks
	got := execute(t, issuing(&h), "report:file", "-h")

	if got.code != gonsole.ExitDone || !strings.Contains(got.stdout, "-owner email") {
		t.Errorf("report:file -h = %d with stdout %q, want %d and the help page", got.code, got.stdout, gonsole.ExitDone)
	}
	if len(h.log) != 0 {
		t.Errorf("calls = %q, want none", h.log)
	}
}

func TestCheckRefusesANeededFlagACommandCannotNeed(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name   string
		change func(p *gonsole.Program)
		want   []string
	}{
		{"a flag the command does not declare", func(p *gonsole.Program) { p.Commands[1].Needs = []string{"owner"} },
			[]string{`gonsole: command "report:list" needs -owner, which it does not declare`}},
		{"an engine flag", func(p *gonsole.Program) {
			p.Commands[1].Writes = true
			p.Commands[1].Needs = []string{"yes"}
		}, []string{`gonsole: command "report:list" needs -yes, which it does not declare`}},
		{"a switch", func(p *gonsole.Program) {
			p.Commands[1] = flagged(p.Commands[1], "all")
			p.Commands[1].Needs = []string{"all"}
		}, []string{`gonsole: command "report:list" needs -all, which is a switch`}},
		{"flags that panic", func(p *gonsole.Program) {
			p.Commands[1].Flags = func(*flag.FlagSet) { panic("the owner flag is gone") }
			p.Commands[1].Needs = []string{"owner"}
		}, []string{`gonsole: command "report:list" panicked declaring its flags: the owner flag is gone`}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			p := sound()
			tc.change(&p)

			if got := offences(p.Check(gonsole.Loaded{})); !slices.Equal(got, tc.want) {
				t.Errorf("offences = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestCheckDropsAPluginCommandThatNeedsAFlagItDoesNotDeclare(t *testing.T) {
	t.Parallel()

	needing := echo("archive:purge")
	needing.Needs = []string{"before"}
	loaded := gonsole.Loaded{Groups: []gonsole.Group{{Namespace: "archive", Commands: []gonsole.Command{needing}}}}

	got := offences(sound().Check(loaded))

	want := []string{`gonsole: command "archive:purge" needs -before, which it does not declare`}
	if !slices.Equal(got, want) {
		t.Errorf("offences = %q, want %q", got, want)
	}
}
