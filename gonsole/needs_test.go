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

// masked is a flag value that reads as a mask whatever text the line gives it.
type masked struct{}

// String returns the mask.
func (masked) String() string {
	return "****"
}

// Set accepts any text.
func (masked) Set(string) error {
	return nil
}

// issuing returns a program called myapp whose report commands need flags, every step and call noted in h.
func issuing(h *hooks) gonsole.Program {
	run := func(_ context.Context, call gonsole.Call) error {
		h.note("run owner=%s title=%s apply=%t", call.Flags["owner"], call.Flags["title"], call.Apply)
		return nil
	}
	var at string
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
			{Name: "report:schedule", Summary: "schedule one report",
				Flags: func(fs *flag.FlagSet) {
					fs.String("owner", "nobody@example.com", "`email` address of the owner")
					fs.Duration("every", 0, "`interval` between two runs")
				},
				Needs: []string{"owner", "every"}, Run: run},
			{Name: "report:remind", Summary: "remind the owner of one report",
				Flags: func(fs *flag.FlagSet) {
					fs.Func("at", "`time` of the reminder", func(text string) error {
						at = text
						return nil
					})
				},
				Needs: []string{"at"}, Run: func(context.Context, gonsole.Call) error {
					h.note("remind at %s", at)
					return nil
				}},
			{Name: "report:seal", Summary: "seal one report",
				Flags: func(fs *flag.FlagSet) { fs.Var(masked{}, "key", "`token` the report is sealed with") },
				Needs: []string{"key"}, Run: run},
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
		{"no owner where the flag has a default", []string{"report:schedule", "-every", "1h"},
			"myapp: report:schedule wants -owner <email>\n"},
		{"no interval where the zero value is not blank", []string{"report:schedule", "-owner", actingAccount},
			"myapp: report:schedule wants -every <interval>\n"},
		{"no time on a flag that reads empty", []string{"report:remind"}, "myapp: report:remind wants -at <time>\n"},
		{"a time of spaces on a flag that reads empty", []string{"report:remind", "-at", "  "},
			"myapp: report:remind wants -at <time>\n"},
		{"no key on a flag that reads a mask", []string{"report:seal"}, "myapp: report:seal wants -key <token>\n"},
		{"an empty key on a flag that reads a mask", []string{"report:seal", "-key="},
			"myapp: report:seal wants -key <token>\n"},
		{"a key of spaces on a flag that reads a mask", []string{"report:seal", "-key", "  "},
			"myapp: report:seal wants -key <token>\n"},
		{"a time given twice, the last one empty", []string{"report:remind", "-at", "09:00", "-at="},
			"myapp: report:remind wants -at <time>\n"},
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

func TestRunHandsTheCommandEveryNeededFlagTheLineSet(t *testing.T) {
	t.Parallel()

	var h hooks
	got := execute(t, issuing(&h), "report:schedule", "-owner", actingAccount, "-every", "1h")

	if got.code != gonsole.ExitDone {
		t.Fatalf("code = %d with stderr %q, want %d", got.code, got.stderr, gonsole.ExitDone)
	}
	want := []string{"run owner=" + actingAccount + " title= apply=true"}
	if !slices.Equal(h.log, want) {
		t.Errorf("calls = %q, want %q", h.log, want)
	}
}

func TestRunReadsANeededFlagByTheTextTheLineGivesIt(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		args []string
		want []string
	}{
		{"a flag that reads empty", []string{"report:remind", "-at", "09:00"}, []string{"remind at 09:00"}},
		{"a flag that reads empty, set with an equals sign", []string{"report:remind", "-at=09:00"},
			[]string{"remind at 09:00"}},
		{"a flag that reads a mask", []string{"report:seal", "-key", "Q3-7"}, []string{"run owner= title= apply=true"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			var h hooks
			got := execute(t, issuing(&h), tc.args...)

			if got.code != gonsole.ExitDone {
				t.Fatalf("code = %d with stderr %q, want %d", got.code, got.stderr, gonsole.ExitDone)
			}
			if !slices.Equal(h.log, tc.want) {
				t.Errorf("calls = %q, want %q", h.log, tc.want)
			}
		})
	}
}

func TestRunRefusesANeededFlagWhoseTextItCannotRead(t *testing.T) {
	t.Parallel()

	var h hooks
	got := execute(t, issuing(&h), "report:schedule", "-owner", actingAccount, "-every", "soon")

	if got.code != gonsole.ExitMisused {
		t.Errorf("code = %d, want %d", got.code, gonsole.ExitMisused)
	}
	want := "myapp: report:schedule: invalid value \"soon\" for flag -every: parse error\n"
	if firstLine(got.stderr) != want {
		t.Errorf("stderr opens with %q, want %q", firstLine(got.stderr), want)
	}
	if len(h.log) != 0 {
		t.Errorf("calls = %q, want no run", h.log)
	}
}

// remindPage is the help page of the report:remind command issuing declares.
const remindPage = `remind the owner of one report

Usage:
  myapp report:remind [flags]

Flags:
  -at time
    	time of the reminder
`

func TestRunPrintsTheHelpPageOfANeededFlagAsDeclared(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name   string
		args   []string
		code   int
		stdout string
		stderr string
	}{
		{"a help flag", []string{"report:remind", "-h"}, gonsole.ExitDone, remindPage, ""},
		{"a help flag the flag package reads", []string{"report:remind", "-at", "09:00", "-h=true"},
			gonsole.ExitDone, remindPage, ""},
		{"a run that leaves the flag out", []string{"report:remind"}, gonsole.ExitMisused, "",
			"myapp: report:remind wants -at <time>\n\n" + remindPage},
		{"a run with a flag the command does not declare", []string{"report:remind", "-at", "09:00", "-late"},
			gonsole.ExitMisused, "", "myapp: report:remind: flag provided but not defined: -late\n\n" + remindPage},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			var h hooks
			got := execute(t, issuing(&h), tc.args...)

			if got.code != tc.code {
				t.Errorf("code = %d, want %d", got.code, tc.code)
			}
			if got.stdout != tc.stdout {
				t.Errorf("stdout = %q, want %q", got.stdout, tc.stdout)
			}
			if got.stderr != tc.stderr {
				t.Errorf("stderr = %q, want %q", got.stderr, tc.stderr)
			}
			if len(h.log) != 0 {
				t.Errorf("calls = %q, want none", h.log)
			}
		})
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
