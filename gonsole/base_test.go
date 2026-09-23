// SPDX-License-Identifier: Apache-2.0

package gonsole_test

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"io"
	"slices"
	"strings"
	"testing"

	"github.com/gopherium/framework/gonsole"
)

// databaseAddress is the database address every schema test reads from the program's database setting.
const databaseAddress = "postgres://localhost/myapp"

// demoNotice is the line a seed of myapp ends with on stderr.
const demoNotice = "myapp: demo data is for development only, never seed a production database\n"

// schema are the answers a program's schema hooks give and the log of every call they receive.
type schema struct {
	lockFails    error
	releaseFails error
	stepFails    string
	seedFails    error
	serveFails   error
	log          []string
}

// note appends one entry to the log.
func (s *schema) note(format string, args ...any) {
	s.log = append(s.log, fmt.Sprintf(format, args...))
}

// step returns the schema step called name, which fails when s names it.
func (s *schema) step(name string) gonsole.Step {
	return gonsole.Step{Name: name, Run: func(_ context.Context, databaseURL string) error {
		s.note("step %s at %s", name, databaseURL)
		if s.stepFails == name {
			return errors.New("relation already exists")
		}
		return nil
	}}
}

// keeper returns a program called myapp with a version, a server, two schema steps, a lock and a seed, all noted in s.
func keeper(s *schema) gonsole.Program {
	return gonsole.Program{
		Name:     "myapp",
		Version:  "1.4.0",
		Env:      settings(map[string]string{"MYAPP_PRIMARY_URL": databaseAddress}),
		Database: "PRIMARY_URL",
		Serve: func(_ context.Context, call gonsole.Call) error {
			address, err := call.DatabaseURL()
			s.note("serve %s at %s %v", call.Args, address, err)
			return s.serveFails
		},
		Migrations: []gonsole.Step{s.step("accounts"), s.step("reports")},
		Lock: func(ctx context.Context, databaseURL string) (func(context.Context) error, error) {
			s.note("lock %s", databaseURL)
			if err := cmp.Or(ctx.Err(), s.lockFails); err != nil {
				return nil, err
			}
			return func(ctx context.Context) error {
				s.note("release with the context live=%t", ctx.Err() == nil)
				return s.releaseFails
			}, nil
		},
		Seed: func(_ context.Context, call gonsole.Call) error {
			s.note("seed")
			if _, err := fmt.Fprintln(call.Stdout, "stored the demo reports"); err != nil {
				return err
			}
			return s.seedFails
		},
	}
}

func TestVersionPrintsTheNameAndTheVersion(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name    string
		version string
		args    []string
		stdout  string
	}{
		{"a released version", "1.4.0", nil, "myapp 1.4.0\n"},
		{"no version", "", nil, "myapp (devel)\n"},
		{"a document", "1.4.0", []string{"-json"}, `{
  "name": "myapp",
  "version": "1.4.0"
}
`},
		{"a document without a version", "", []string{"-json"}, `{
  "name": "myapp",
  "version": "(devel)"
}
`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			p := gonsole.Program{Name: "myapp", Title: "Myapp, a report keeper.", Version: tc.version}

			got := execute(t, p, append([]string{"version"}, tc.args...)...)

			if got.code != gonsole.ExitDone {
				t.Errorf("code = %d, want %d, stderr %q", got.code, gonsole.ExitDone, got.stderr)
			}
			if got.stdout != tc.stdout {
				t.Errorf("stdout = %q, want %q", got.stdout, tc.stdout)
			}
		})
	}
}

func TestListingShowsOnlyTheBaseCommandsTheProgramOffers(t *testing.T) {
	t.Parallel()

	var s schema
	got := execute(t, keeper(&s), "list")

	want := `myapp Version 1.4.0

Usage:
  myapp <command> [flags] [arguments]

` + intro + `
Available commands:
  check    check every setting, every plugin and every command name
  help     print the help of one command
  list     list every command
  migrate  apply every schema step
  seed     store the demo data
  serve    run the server
  version  print the version
`
	if got.stdout != want {
		t.Errorf("stdout = %q, want %q", got.stdout, want)
	}
}

func TestRunHidesEachBaseCommandTheProgramDoesNotOffer(t *testing.T) {
	t.Parallel()

	cases := []struct {
		command string
		leave   func(p *gonsole.Program)
	}{
		{"serve", func(p *gonsole.Program) { p.Serve = nil }},
		{"migrate", func(p *gonsole.Program) { p.Migrations = nil }},
		{"seed", func(p *gonsole.Program) { p.Seed = nil }},
	}
	for _, tc := range cases {
		t.Run(tc.command, func(t *testing.T) {
			t.Parallel()

			var s schema
			p := keeper(&s)
			tc.leave(&p)

			got := execute(t, p, tc.command)

			if got.code != gonsole.ExitMisused {
				t.Errorf("code = %d, want %d", got.code, gonsole.ExitMisused)
			}
			want := `myapp: unknown command "` + tc.command + `", run "myapp list" to see every command` + "\n"
			if got.stderr != want {
				t.Errorf("stderr = %q, want %q", got.stderr, want)
			}
		})
	}
}

func TestHelpPagesOfTheBaseCommandsShowOnlyTheirOwnSwitches(t *testing.T) {
	t.Parallel()

	cases := []struct {
		command string
		page    string
	}{
		{"version", `print the version

Usage:
  myapp version [flags]

Flags:
  -json
    	answer one JSON document
`},
		{"migrate", `apply every schema step

Usage:
  myapp migrate
`},
		{"seed", `store the demo data

Usage:
  myapp seed [flags]

Flags:
  -yes
    	apply the change, a dry run without it
`},
		{"serve", `run the server

Usage:
  myapp serve
`},
		{"check", `check every setting, every plugin and every command name

Usage:
  myapp check
`},
	}
	for _, tc := range cases {
		t.Run(tc.command, func(t *testing.T) {
			t.Parallel()

			var s schema
			got := execute(t, keeper(&s), tc.command, "-h")

			if got.stdout != tc.page {
				t.Errorf("stdout = %q, want %q", got.stdout, tc.page)
			}
		})
	}
}

func TestBaseCommandsFailWhenTheirAnswerCannotBeWritten(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		args []string
	}{
		{"the version", []string{"version"}},
		{"the version document", []string{"version", "-json"}},
		{"a seed dry run", []string{"seed"}},
		{"the check", []string{"check"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			var s schema
			var stderr strings.Builder

			code := keeper(&s).Run(t.Context(), tc.args, strings.NewReader(""), closedWriter{}, &stderr)

			if code != gonsole.ExitFailed {
				t.Errorf("code = %d, want %d", code, gonsole.ExitFailed)
			}
			if want := "myapp: stdout is closed\n"; stderr.String() != want {
				t.Errorf("stderr = %q, want %q", stderr.String(), want)
			}
		})
	}
}

func TestSchemaHooksGetTheRunContext(t *testing.T) {
	t.Parallel()

	type key struct{}
	var seen []string
	look := func(hook string, ctx context.Context) {
		seen = append(seen, fmt.Sprintf("%s %v live=%t", hook, ctx.Value(key{}), ctx.Err() == nil))
	}
	var s schema
	p := keeper(&s)
	p.Lock = func(ctx context.Context, _ string) (func(context.Context) error, error) {
		look("lock", ctx)
		return func(ctx context.Context) error {
			look("release", ctx)
			return nil
		}, nil
	}
	p.Migrations = []gonsole.Step{{Name: "accounts", Run: func(ctx context.Context, _ string) error {
		look("step", ctx)
		return nil
	}}}
	p.Seed = func(ctx context.Context, _ gonsole.Call) error {
		look("seed", ctx)
		return nil
	}
	ctx := context.WithValue(t.Context(), key{}, "run")

	code := p.Run(ctx, []string{"seed", "-yes"}, strings.NewReader(""), io.Discard, io.Discard)

	if code != gonsole.ExitDone {
		t.Errorf("code = %d, want %d", code, gonsole.ExitDone)
	}
	want := []string{"lock run live=true", "step run live=true", "release run live=true", "seed run live=true"}
	if !slices.Equal(seen, want) {
		t.Errorf("hooks saw %q, want %q", seen, want)
	}
}

func TestMigrateRefusesARunThatEndedBeforeItStarted(t *testing.T) {
	t.Parallel()

	var s schema
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	var stderr strings.Builder

	code := keeper(&s).Run(ctx, []string{"migrate"}, strings.NewReader(""), io.Discard, &stderr)

	if code != gonsole.ExitFailed {
		t.Errorf("code = %d, want %d", code, gonsole.ExitFailed)
	}
	if want := "myapp: lock the schema: context canceled\n"; stderr.String() != want {
		t.Errorf("stderr = %q, want %q", stderr.String(), want)
	}
	if want := []string{"lock " + databaseAddress}; !slices.Equal(s.log, want) {
		t.Errorf("calls = %q, want %q", s.log, want)
	}
}

func TestServeRunsTheProgramServer(t *testing.T) {
	t.Parallel()

	served := "serve [] at " + databaseAddress + " <nil>"
	cases := []struct {
		name       string
		args       []string
		bareServes bool
		serveFails error
		code       int
		stderr     string
		log        []string
	}{
		{"the serve command", []string{"serve"}, false, nil, gonsole.ExitDone, "", []string{served}},
		{"a commandless run of a program that serves", nil, true, nil, gonsole.ExitDone, "", []string{served}},
		{"a server that fails", []string{"serve"}, false, errors.New("port 8080 is taken"), gonsole.ExitFailed,
			"myapp: port 8080 is taken\n", []string{served}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			s := schema{serveFails: tc.serveFails}
			p := keeper(&s)
			p.BareServes = tc.bareServes

			got := execute(t, p, tc.args...)

			if got.code != tc.code {
				t.Errorf("code = %d, want %d, stderr %q", got.code, tc.code, got.stderr)
			}
			if got.stdout != "" || got.stderr != tc.stderr {
				t.Errorf("stdout, stderr = %q, %q, want %q, %q", got.stdout, got.stderr, "", tc.stderr)
			}
			if !slices.Equal(s.log, tc.log) {
				t.Errorf("calls = %q, want %q", s.log, tc.log)
			}
		})
	}
}

func TestMigrateAppliesEveryStepUnderTheLock(t *testing.T) {
	t.Parallel()

	locked := "lock " + databaseAddress
	accounts := "step accounts at " + databaseAddress
	reports := "step reports at " + databaseAddress
	released := "release with the context live=true"
	cases := []struct {
		name   string
		schema schema
		code   int
		stdout string
		stderr string
		log    []string
	}{
		{"every step applied", schema{}, gonsole.ExitDone, "migrated accounts\nmigrated reports\n", "",
			[]string{locked, accounts, reports, released}},
		{"a step that fails", schema{stepFails: "reports"}, gonsole.ExitFailed, "migrated accounts\n",
			"myapp: migrate reports: relation already exists\n", []string{locked, accounts, reports, released}},
		{"a lock that is refused", schema{lockFails: errors.New("the database is read only")}, gonsole.ExitFailed, "",
			"myapp: lock the schema: the database is read only\n", []string{locked}},
		{"a release that fails", schema{releaseFails: errors.New("the session ended")}, gonsole.ExitFailed,
			"migrated accounts\nmigrated reports\n", "myapp: release the schema lock: the session ended\n",
			[]string{locked, accounts, reports, released}},
		{"a step and the release that fail", schema{stepFails: "reports", releaseFails: errors.New("the session ended")},
			gonsole.ExitFailed, "migrated accounts\n",
			"myapp: migrate reports: relation already exists\nmyapp: release the schema lock: the session ended\n",
			[]string{locked, accounts, reports, released}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			s := tc.schema
			got := execute(t, keeper(&s), "migrate")

			if got.code != tc.code {
				t.Errorf("code = %d, want %d", got.code, tc.code)
			}
			if got.stdout != tc.stdout {
				t.Errorf("stdout = %q, want %q", got.stdout, tc.stdout)
			}
			if got.stderr != tc.stderr {
				t.Errorf("stderr = %q, want %q", got.stderr, tc.stderr)
			}
			if !slices.Equal(s.log, tc.log) {
				t.Errorf("calls = %q, want %q", s.log, tc.log)
			}
		})
	}
}

func TestMigrateReleasesTheLockWithALiveContextAfterTheRunEnds(t *testing.T) {
	t.Parallel()

	var s schema
	ctx, cancel := context.WithCancel(t.Context())
	p := keeper(&s)
	p.Migrations = []gonsole.Step{{Name: "accounts", Run: func(context.Context, string) error {
		cancel()
		return nil
	}}}

	code := p.Run(ctx, []string{"migrate"}, strings.NewReader(""), io.Discard, io.Discard)

	if code != gonsole.ExitDone {
		t.Errorf("code = %d, want %d", code, gonsole.ExitDone)
	}
	if want := []string{"lock " + databaseAddress, "release with the context live=true"}; !slices.Equal(s.log, want) {
		t.Errorf("calls = %q, want %q", s.log, want)
	}
}

func TestMigrateStopsAndReleasesWhenItsAnswerCannotBeWritten(t *testing.T) {
	t.Parallel()

	var s schema
	var stderr strings.Builder

	code := keeper(&s).Run(t.Context(), []string{"migrate"}, strings.NewReader(""), closedWriter{}, &stderr)

	if code != gonsole.ExitFailed {
		t.Errorf("code = %d, want %d", code, gonsole.ExitFailed)
	}
	if want := "myapp: stdout is closed\n"; stderr.String() != want {
		t.Errorf("stderr = %q, want %q", stderr.String(), want)
	}
	want := []string{"lock " + databaseAddress, "step accounts at " + databaseAddress,
		"release with the context live=true"}
	if !slices.Equal(s.log, want) {
		t.Errorf("calls = %q, want %q", s.log, want)
	}
}

func TestMigrateWithoutALockAppliesEveryStep(t *testing.T) {
	t.Parallel()

	var s schema
	p := keeper(&s)
	p.Lock = nil

	got := execute(t, p, "migrate")

	if got.stdout != "migrated accounts\nmigrated reports\n" {
		t.Errorf("stdout = %q, want both steps", got.stdout)
	}
}

func TestMigrateRefusesAMissingDatabaseSetting(t *testing.T) {
	t.Parallel()

	var s schema
	p := keeper(&s)
	p.Env = settings(nil)

	got := execute(t, p, "migrate")

	if got.code != gonsole.ExitFailed {
		t.Errorf("code = %d, want %d", got.code, gonsole.ExitFailed)
	}
	if want := "myapp: MYAPP_PRIMARY_URL is required\n"; got.stderr != want {
		t.Errorf("stderr = %q, want %q", got.stderr, want)
	}
	if len(s.log) != 0 {
		t.Errorf("calls = %q, want none", s.log)
	}
}

func TestSeedStoresTheDemoDataOnlyWithYes(t *testing.T) {
	t.Parallel()

	locked := "lock " + databaseAddress
	accounts := "step accounts at " + databaseAddress
	reports := "step reports at " + databaseAddress
	released := "release with the context live=true"
	migrated := "migrated accounts\nmigrated reports\n"
	cases := []struct {
		name   string
		schema schema
		args   []string
		code   int
		stdout string
		stderr string
		log    []string
	}{
		{"a dry run", schema{}, nil, gonsole.ExitDone, "would store the demo data\n", dryRunNotice, nil},
		{"an applied seed", schema{}, []string{"-yes"}, gonsole.ExitDone, "stored the demo reports\n",
			migrated + demoNotice, []string{locked, accounts, reports, released, "seed"}},
		{"a migration that fails", schema{stepFails: "accounts"}, []string{"-yes"}, gonsole.ExitFailed, "",
			"myapp: migrate accounts: relation already exists\n", []string{locked, accounts, released}},
		{"a seed that fails", schema{seedFails: errors.New("the demo reports clash")}, []string{"-yes"},
			gonsole.ExitFailed, "stored the demo reports\n", migrated + "myapp: the demo reports clash\n",
			[]string{locked, accounts, reports, released, "seed"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			s := tc.schema
			got := execute(t, keeper(&s), append([]string{"seed"}, tc.args...)...)

			if got.code != tc.code {
				t.Errorf("code = %d, want %d", got.code, tc.code)
			}
			if got.stdout != tc.stdout {
				t.Errorf("stdout = %q, want %q", got.stdout, tc.stdout)
			}
			if got.stderr != tc.stderr {
				t.Errorf("stderr = %q, want %q", got.stderr, tc.stderr)
			}
			if !slices.Equal(s.log, tc.log) {
				t.Errorf("calls = %q, want %q", s.log, tc.log)
			}
		})
	}
}

func TestRunMigratesBeforeACommandThatAsksForIt(t *testing.T) {
	t.Parallel()

	locked := "lock " + databaseAddress
	accounts := "step accounts at " + databaseAddress
	reports := "step reports at " + databaseAddress
	released := "release with the context live=true"
	cases := []struct {
		name   string
		schema schema
		writes bool
		args   []string
		code   int
		stderr string
		log    []string
	}{
		{"a command that does not write", schema{}, false, nil, gonsole.ExitDone,
			"migrated accounts\nmigrated reports\n", []string{locked, accounts, reports, released, "run apply=true"}},
		{"an applied write", schema{}, true, []string{"-yes"}, gonsole.ExitDone,
			"migrated accounts\nmigrated reports\n", []string{locked, accounts, reports, released, "run apply=true"}},
		{"a dry run", schema{}, true, nil, gonsole.ExitDone, dryRunNotice, []string{"run apply=false"}},
		{"a migration that fails", schema{stepFails: "accounts"}, false, nil, gonsole.ExitFailed,
			"myapp: migrate accounts: relation already exists\n", []string{locked, accounts, released}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			s := tc.schema
			p := keeper(&s)
			p.Commands = []gonsole.Command{{
				Name: "createadmin", Summary: "create an account", Migrates: true, Writes: tc.writes,
				Run: func(_ context.Context, call gonsole.Call) error {
					s.note("run apply=%t", call.Apply)
					return nil
				},
			}}

			got := execute(t, p, append([]string{"createadmin"}, tc.args...)...)

			if got.code != tc.code {
				t.Errorf("code = %d, want %d", got.code, tc.code)
			}
			if got.stderr != tc.stderr {
				t.Errorf("stderr = %q, want %q", got.stderr, tc.stderr)
			}
			if !slices.Equal(s.log, tc.log) {
				t.Errorf("calls = %q, want %q", s.log, tc.log)
			}
		})
	}
}

func TestRunChecksTheActingAccountBeforeItMigrates(t *testing.T) {
	t.Parallel()

	var s schema
	p := keeper(&s)
	p.Commands = []gonsole.Command{{
		Name: "grantrole", Summary: "give a role", Migrates: true, Capability: "manage_users",
		Run: func(context.Context, gonsole.Call) error {
			s.note("run")
			return nil
		},
	}}
	p.Authorize = func(_ context.Context, call gonsole.Call, capability string) error {
		s.note("authorize %s for %s", call.Actor, capability)
		return errors.New(call.Actor + " lacks " + capability)
	}
	p.Record = func(context.Context, gonsole.Call, string) error { return nil }

	got := execute(t, p, "grantrole", "-as", actingAccount)

	if got.code != gonsole.ExitFailed {
		t.Errorf("code = %d, want %d", got.code, gonsole.ExitFailed)
	}
	if want := []string{"authorize " + actingAccount + " for manage_users"}; !slices.Equal(s.log, want) {
		t.Errorf("calls = %q, want %q", s.log, want)
	}
}

func TestDatabaseURLNeedsACallTheEngineBuilt(t *testing.T) {
	t.Parallel()

	_, err := gonsole.Call{Env: settings(map[string]string{"MYAPP_DATABASE_URL": databaseAddress})}.DatabaseURL()

	if want := "gonsole: no database setting in this call"; errorText(err) != want {
		t.Errorf("DatabaseURL() error = %q, want %q", errorText(err), want)
	}
}

func TestDatabaseURLReadsTheProgramSetting(t *testing.T) {
	t.Parallel()

	var s schema
	p := keeper(&s)
	p.Commands = []gonsole.Command{{
		Name: "report:list", Summary: "list every report",
		Run: func(_ context.Context, call gonsole.Call) error {
			address, err := call.DatabaseURL()
			if err != nil {
				return err
			}
			_, err = fmt.Fprintln(call.Stdout, address)
			return err
		},
	}}

	got := execute(t, p, "report:list")

	if got.stdout != databaseAddress+"\n" {
		t.Errorf("stdout = %q, want the database address, stderr %q", got.stdout, got.stderr)
	}
}
