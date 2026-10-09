// SPDX-License-Identifier: Apache-2.0

package gonsole_test

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"slices"
	"testing"

	"github.com/gopherium/framework/gonsole"
)

// steps records the schema steps of one run and the handle each handle step receives.
type steps struct {
	// log names each step that ran, in order.
	log []string
	// handles records the handle each handle step received.
	handles handles
}

// atAddress returns the address step called name, noted in s.
func (s *steps) atAddress(name string) gonsole.Step {
	return gonsole.Step{Name: name, Run: func(_ context.Context, databaseURL string) error {
		s.log = append(s.log, fmt.Sprintf("%s at %s", name, databaseURL))
		return nil
	}}
}

// onHandle returns the handle step called name, noted in s.
func (s *steps) onHandle(name string) gonsole.Step {
	return gonsole.Step{Name: name, RunOn: func(_ context.Context, db *sql.DB) error {
		s.log = append(s.log, name+" on the handle")
		s.handles.seen = append(s.handles.seen, db)
		return nil
	}}
}

// migrating returns a program called myapp whose opener is o and whose core schema steps are migrations.
func migrating(o *opener, migrations ...gonsole.Step) gonsole.Program {
	p := handled(o)
	p.Migrations = migrations
	return p
}

func TestMigrateRunsHandleStepsOnTheOneHandle(t *testing.T) {
	t.Parallel()

	o := newOpener()
	var s steps

	got := execute(t, migrating(o, s.onHandle("accounts"), s.onHandle("reports")), "migrate")

	if got.code != gonsole.ExitDone || len(s.handles.seen) != 2 || !s.handles.same() {
		t.Errorf("code %d, handles %v, want 0 and one handle for both steps, stderr %q",
			got.code, s.handles.seen, got.stderr)
	}
	if opens := o.opens.Load(); opens != 1 {
		t.Errorf("Open ran %d times, want once", opens)
	}
}

func TestMigrateMixesAddressAndHandleSteps(t *testing.T) {
	t.Parallel()

	o := newOpener()
	var s steps

	got := execute(t, migrating(o, s.atAddress("accounts"), s.onHandle("reports"), s.atAddress("audit")), "migrate")

	want := []string{"accounts at " + databaseAddress, "reports on the handle", "audit at " + databaseAddress}
	if got.code != gonsole.ExitDone || !slices.Equal(s.log, want) {
		t.Errorf("code %d, steps %q, want 0 and %q, stderr %q", got.code, s.log, want, got.stderr)
	}
	if lines := "migrated accounts\nmigrated reports\nmigrated audit\n"; got.stdout != lines {
		t.Errorf("stdout = %q, want %q", got.stdout, lines)
	}
}

func TestMigrateWithAddressStepsOnlyOpensNothing(t *testing.T) {
	t.Parallel()

	o := newOpener()
	var s steps

	got := execute(t, migrating(o, s.atAddress("accounts"), s.atAddress("reports")), "migrate")

	if got.code != gonsole.ExitDone || o.opens.Load() != 0 {
		t.Errorf("code %d, Open ran %d times, want 0 and never, stderr %q", got.code, o.opens.Load(), got.stderr)
	}
}

func TestMigrateStopsWhenTheHandleFailsToOpen(t *testing.T) {
	t.Parallel()

	o := newOpener()
	o.fails = errors.New("unable to open database file")
	var s steps

	got := execute(t, migrating(o, s.atAddress("accounts"), s.onHandle("reports")), "migrate")

	if want := "myapp: open the database: unable to open database file\n"; got.code != gonsole.ExitFailed ||
		got.stderr != want || len(s.log) != 0 {
		t.Errorf("code %d, stderr %q, steps %q, want 1, %q and no step", got.code, got.stderr, s.log, want)
	}
}

func TestPluginsRegistrationSharesTheHandle(t *testing.T) {
	t.Parallel()

	o := newOpener()
	var s steps
	p := migrating(o, s.onHandle("reports"))
	p.Plugins = func(ctx context.Context, call gonsole.Call) (gonsole.Loaded, error) {
		_, err := s.handles.ask(ctx, call)
		return gonsole.Loaded{}, err
	}
	p.Commands = []gonsole.Command{{
		Name: "report:rebuild", Summary: "rebuild every report", Migrates: true,
		Run: func(ctx context.Context, call gonsole.Call) error {
			if _, err := call.Plugins(ctx); err != nil {
				return err
			}
			_, err := s.handles.ask(ctx, call)
			return err
		},
	}}

	got := execute(t, p, "report:rebuild")

	if got.code != gonsole.ExitDone || len(s.handles.seen) != 3 || !s.handles.same() {
		t.Errorf("code %d, handles %v, want 0 and one handle for the step, Plugins and the command, stderr %q",
			got.code, s.handles.seen, got.stderr)
	}
	if opens := o.opens.Load(); opens != 1 {
		t.Errorf("Open ran %d times, want once", opens)
	}
}

func TestApplyRunsEitherForm(t *testing.T) {
	t.Parallel()

	var s steps
	db := sql.OpenDB(&counter{})
	t.Cleanup(func() { _ = db.Close() })

	for _, step := range []gonsole.Step{s.atAddress("accounts"), s.onHandle("reports")} {
		if err := step.Apply(t.Context(), databaseAddress, db); err != nil {
			t.Fatalf("Apply() of %s error = %v, want nil", step.Name, err)
		}
	}

	want := []string{"accounts at " + databaseAddress, "reports on the handle"}
	if !slices.Equal(s.log, want) || len(s.handles.seen) != 1 || s.handles.seen[0] != db {
		t.Errorf("steps %q, handles %v, want %q and the handle passed in", s.log, s.handles.seen, want)
	}
}

func TestApplyRefusesBothAndNeither(t *testing.T) {
	t.Parallel()

	var s steps
	both := s.atAddress("reports")
	both.RunOn = s.onHandle("reports").RunOn
	cases := []struct {
		name string
		step gonsole.Step
		want string
	}{
		{"a step with both forms", both, `gonsole: step "reports" sets both Run and RunOn`},
		{"a step with neither form", gonsole.Step{Name: "reports"}, `gonsole: step "reports" sets neither Run nor RunOn`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.step.Apply(t.Context(), databaseAddress, nil)

			if errorText(err) != tc.want {
				t.Errorf("Apply() error = %q, want %q", errorText(err), tc.want)
			}
		})
	}
	if len(s.log) != 0 {
		t.Errorf("steps %q ran, want none", s.log)
	}
}
