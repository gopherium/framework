// SPDX-License-Identifier: Apache-2.0

package gonsole_test

import (
	"context"
	"errors"
	"fmt"
	"io"
	"slices"
	"strings"
	"testing"

	"github.com/gopherium/framework/gonsole"
)

// actingAccount is the address every acting account test passes to -as.
const actingAccount = "maria.perez@example.com"

// hooks are the answers a guarded program's hooks give and the log of every hook and command call.
type hooks struct {
	refuse error
	fail   error
	lost   error
	log    []string
}

// note appends one entry to the log.
func (h *hooks) note(format string, args ...any) {
	h.log = append(h.log, fmt.Sprintf(format, args...))
}

// guarded returns a program called myapp whose commands need capabilities and whose hooks give the answers h holds.
func guarded(h *hooks) gonsole.Program {
	run := func(_ context.Context, call gonsole.Call) error {
		h.note("run %s as %s apply=%t", call.Args, call.Actor, call.Apply)
		return h.fail
	}
	return gonsole.Program{
		Name: "myapp",
		Commands: []gonsole.Command{
			{Name: "report:revoke", Summary: "revoke one report", Args: []string{"id"}, Writes: true,
				Capability: "manage_reports", Run: run},
			{Name: "report:export", Summary: "export every report", Capability: "export_reports", Run: run},
			{Name: "report:list", Summary: "list every report", Run: run},
		},
		Authorize: func(_ context.Context, call gonsole.Call, capability string) error {
			h.note("authorize %s for %s %s apply=%t", call.Actor, capability, call.Args, call.Apply)
			return h.refuse
		},
		Record: func(_ context.Context, call gonsole.Call, command string) error {
			h.note("record %s ran %s %s apply=%t", call.Actor, command, call.Args, call.Apply)
			return h.lost
		},
	}
}

func TestRunChecksAndRecordsTheActingAccount(t *testing.T) {
	t.Parallel()

	authorizedWrite := "authorize " + actingAccount + " for manage_reports [Q3] apply=true"
	authorizedDryRun := "authorize " + actingAccount + " for manage_reports [Q3] apply=false"
	ranWrite := "run [Q3] as " + actingAccount + " apply=true"
	recordedWrite := "record " + actingAccount + " ran report:revoke [Q3] apply=true"
	refused := "myapp: " + actingAccount + " lacks manage_reports\n"
	cases := []struct {
		name   string
		hooks  hooks
		args   []string
		code   int
		stderr string
		log    []string
	}{
		{
			"an applied write", hooks{}, []string{"report:revoke", "-as", actingAccount, "-yes", "Q3"},
			gonsole.ExitDone, "", []string{authorizedWrite, ranWrite, recordedWrite},
		},
		{
			"a dry run", hooks{}, []string{"report:revoke", "-as", actingAccount, "Q3"},
			gonsole.ExitDone, dryRunNotice, []string{authorizedDryRun, "run [Q3] as " + actingAccount + " apply=false"},
		},
		{
			"a refused write", hooks{refuse: errors.New(actingAccount + " lacks manage_reports")},
			[]string{"report:revoke", "-as", actingAccount, "-yes", "Q3"},
			gonsole.ExitFailed, refused, []string{authorizedWrite},
		},
		{
			"a refused dry run", hooks{refuse: errors.New(actingAccount + " lacks manage_reports")},
			[]string{"report:revoke", "-as", actingAccount, "Q3"},
			gonsole.ExitFailed, refused, []string{authorizedDryRun},
		},
		{
			"a write that fails", hooks{fail: errors.New("report store is down")},
			[]string{"report:revoke", "-as", actingAccount, "-yes", "Q3"},
			gonsole.ExitFailed, "myapp: report store is down\n", []string{authorizedWrite, ranWrite},
		},
		{
			"a write whose record is lost", hooks{lost: errors.New("the record table is missing")},
			[]string{"report:revoke", "-as", actingAccount, "-yes", "Q3"},
			gonsole.ExitFailed, "myapp: the record table is missing\n", []string{authorizedWrite, ranWrite, recordedWrite},
		},
		{
			"a read that needs a capability", hooks{}, []string{"report:export", "-as", actingAccount},
			gonsole.ExitDone, "",
			[]string{"authorize " + actingAccount + " for export_reports [] apply=true",
				"run [] as " + actingAccount + " apply=true",
				"record " + actingAccount + " ran report:export [] apply=true"},
		},
		{
			"a command that needs no capability", hooks{}, []string{"report:list"},
			gonsole.ExitDone, "", []string{"run [] as  apply=true"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			h := tc.hooks
			got := execute(t, guarded(&h), tc.args...)

			if got.code != tc.code {
				t.Errorf("code = %d, want %d, stderr %q", got.code, tc.code, got.stderr)
			}
			if got.stderr != tc.stderr {
				t.Errorf("stderr = %q, want %q", got.stderr, tc.stderr)
			}
			if !slices.Equal(h.log, tc.log) {
				t.Errorf("calls = %q, want %q", h.log, tc.log)
			}
		})
	}
}

func TestRunHandsTheHooksTheRunContext(t *testing.T) {
	t.Parallel()

	type key struct{}
	var seen []any
	var h hooks
	p := guarded(&h)
	p.Authorize = func(ctx context.Context, _ gonsole.Call, _ string) error {
		seen = append(seen, ctx.Value(key{}))
		return nil
	}
	p.Record = func(ctx context.Context, _ gonsole.Call, _ string) error {
		seen = append(seen, ctx.Value(key{}))
		return nil
	}
	ctx := context.WithValue(t.Context(), key{}, "carried")

	code := p.Run(ctx, []string{"report:revoke", "-as", actingAccount, "-yes", "Q3"}, strings.NewReader(""),
		io.Discard, io.Discard)

	if code != gonsole.ExitDone {
		t.Errorf("code = %d, want %d", code, gonsole.ExitDone)
	}
	if want := []any{"carried", "carried"}; !slices.Equal(seen, want) {
		t.Errorf("hooks saw %v, want %v", seen, want)
	}
}

func TestRunRecordsAnAppliedWriteAfterTheRunIsCancelled(t *testing.T) {
	t.Parallel()

	type key struct{}
	ctx, cancel := context.WithCancel(context.WithValue(t.Context(), key{}, "run"))
	var seen string
	var h hooks
	p := guarded(&h)
	p.Commands[0].Run = func(context.Context, gonsole.Call) error {
		cancel()
		return nil
	}
	p.Record = func(ctx context.Context, _ gonsole.Call, _ string) error {
		seen = fmt.Sprintf("%v live=%t", ctx.Value(key{}), ctx.Err() == nil)
		return ctx.Err()
	}

	code := p.Run(ctx, []string{"report:revoke", "-as", actingAccount, "-yes", "Q3"}, strings.NewReader(""),
		io.Discard, io.Discard)

	if code != gonsole.ExitDone {
		t.Errorf("code = %d, want %d", code, gonsole.ExitDone)
	}
	if seen != "run live=true" {
		t.Errorf("Record saw %q, want the run's values on a live context", seen)
	}
}

func TestRunRefusesACommandThatNeedsAnActingAccountWithoutOne(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		args []string
	}{
		{"no -as", []string{"report:revoke", "-yes", "Q3"}},
		{"an empty -as", []string{"report:revoke", "-as=", "-yes", "Q3"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			var h hooks
			got := execute(t, guarded(&h), tc.args...)

			if got.code != gonsole.ExitMisused {
				t.Errorf("code = %d, want %d", got.code, gonsole.ExitMisused)
			}
			if want := "myapp: report:revoke wants -as <email>\n"; firstLine(got.stderr) != want {
				t.Errorf("stderr opens with %q, want %q", firstLine(got.stderr), want)
			}
			if len(h.log) != 0 {
				t.Errorf("calls = %q, want none", h.log)
			}
		})
	}
}

func TestRunRefusesAnActingAccountOnACommandThatNeedsNone(t *testing.T) {
	t.Parallel()

	var h hooks
	got := execute(t, guarded(&h), "report:list", "-as", actingAccount)

	if got.code != gonsole.ExitMisused {
		t.Errorf("code = %d, want %d", got.code, gonsole.ExitMisused)
	}
	if want := "myapp: report:list: flag provided but not defined: -as\n"; firstLine(got.stderr) != want {
		t.Errorf("stderr opens with %q, want %q", firstLine(got.stderr), want)
	}
}

func TestHelpPageListsTheActingAccountFlag(t *testing.T) {
	t.Parallel()

	var h hooks
	got := execute(t, guarded(&h), "report:revoke", "-h")

	want := `revoke one report

Usage:
  myapp report:revoke [flags] <id>

Flags:
  -as email
    	email address of the account acting
  -yes
    	apply the change, a dry run without it
`
	if got.stdout != want {
		t.Errorf("stdout = %q, want %q", got.stdout, want)
	}
	if len(h.log) != 0 {
		t.Errorf("calls = %q, want none for a help page", h.log)
	}
}
