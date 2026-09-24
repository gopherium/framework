// SPDX-License-Identifier: Apache-2.0

package gonsole_test

import (
	"context"
	"errors"
	"flag"
	"io"
	"os"
	"slices"
	"strings"
	"testing"

	"github.com/gopherium/framework/gonsole"
)

// sound returns a program called myapp that breaks none of the naming rules.
func sound() gonsole.Program {
	return gonsole.Program{
		Name:     "myapp",
		Commands: []gonsole.Command{echo("status"), echo("report:list"), echo("report:create", "title")},
		Renamed:  map[string]string{"report new": "report:create"},
		Reserved: []string{"audit"},
	}
}

// flagged returns cmd declaring one boolean flag called name.
func flagged(cmd gonsole.Command, name string) gonsole.Command {
	cmd.Flags = func(fs *flag.FlagSet) { fs.Bool(name, false, "a flag") }
	return cmd
}

// offences returns the lines of err, none when it is nil.
func offences(err error) []string {
	if err == nil {
		return nil
	}
	return strings.Split(err.Error(), "\n")
}

func TestCheckPassesASoundProgram(t *testing.T) {
	t.Parallel()

	loaded := gonsole.Loaded{Groups: []gonsole.Group{{Namespace: "demo", Commands: []gonsole.Command{echo("demo:sync")}}}}
	p := sound()
	p.Commands = append(p.Commands, echo("report:list-all"), echo("report:q3"))

	if err := p.Check(loaded); err != nil {
		t.Errorf("Check() = %v, want nil", err)
	}
}

func TestCheckRefusesTheProgramsOwnOffences(t *testing.T) {
	t.Parallel()

	const malformed = "is malformed, want lowercase words joined by hyphens and at most one colon"
	cases := []struct {
		name   string
		change func(p *gonsole.Program)
		want   []string
	}{
		{"two commands with one name", func(p *gonsole.Program) { p.Commands = append(p.Commands, echo("report:list")) },
			[]string{`gonsole: command "report:list" is declared twice`}},
		{"a base command", func(p *gonsole.Program) { p.Commands = append(p.Commands, echo("list")) },
			[]string{`gonsole: command "list" is a base command`}},
		{"a base command the program does not offer", func(p *gonsole.Program) {
			p.Commands = append(p.Commands, echo("serve"))
		}, []string{`gonsole: command "serve" is a base command`}},
		{"a command in an engine namespace", func(p *gonsole.Program) {
			p.Commands = append(p.Commands, echo("migrate:status"))
		}, []string{`gonsole: command "migrate:status" is in the engine namespace migrate`}},
		{"a capital letter", func(p *gonsole.Program) { p.Commands = append(p.Commands, echo("Report")) },
			[]string{`gonsole: command name "Report" ` + malformed}},
		{"a capital letter after the colon", func(p *gonsole.Program) {
			p.Commands = append(p.Commands, echo("report:List"))
		}, []string{`gonsole: command name "report:List" ` + malformed}},
		{"two colons", func(p *gonsole.Program) { p.Commands = append(p.Commands, echo("report:list:all")) },
			[]string{`gonsole: command name "report:list:all" ` + malformed}},
		{"nothing after the colon", func(p *gonsole.Program) { p.Commands = append(p.Commands, echo("report:")) },
			[]string{`gonsole: command name "report:" ` + malformed}},
		{"nothing before the colon", func(p *gonsole.Program) { p.Commands = append(p.Commands, echo(":list")) },
			[]string{`gonsole: command name ":list" ` + malformed}},
		{"a leading digit", func(p *gonsole.Program) { p.Commands = append(p.Commands, echo("9report")) },
			[]string{`gonsole: command name "9report" ` + malformed}},
		{"an underscore", func(p *gonsole.Program) { p.Commands = append(p.Commands, echo("report_list")) },
			[]string{`gonsole: command name "report_list" ` + malformed}},
		{"no summary", func(p *gonsole.Program) { p.Commands[1].Summary = "" },
			[]string{`gonsole: command "report:list" has no summary`}},
		{"a summary of spaces", func(p *gonsole.Program) { p.Commands[1].Summary = "   " },
			[]string{`gonsole: command "report:list" has no summary`}},
		{"a summary of two lines", func(p *gonsole.Program) { p.Commands[1].Summary = "list\nevery report" },
			[]string{`gonsole: command "report:list" has a summary of more than one line`}},
		{"no run", func(p *gonsole.Program) { p.Commands[1].Run = nil },
			[]string{`gonsole: command "report:list" has no run`}},
		{"the flag h", func(p *gonsole.Program) { p.Commands[1] = flagged(p.Commands[1], "h") },
			[]string{`gonsole: command "report:list" declares the engine flag -h`}},
		{"the flag help", func(p *gonsole.Program) { p.Commands[1] = flagged(p.Commands[1], "help") },
			[]string{`gonsole: command "report:list" declares the engine flag -help`}},
		{"the flag yes", func(p *gonsole.Program) { p.Commands[1] = flagged(p.Commands[1], "yes") },
			[]string{`gonsole: command "report:list" declares the engine flag -yes`}},
		{"the flag json", func(p *gonsole.Program) { p.Commands[1] = flagged(p.Commands[1], "json") },
			[]string{`gonsole: command "report:list" declares the engine flag -json`}},
		{"the flag as", func(p *gonsole.Program) { p.Commands[1] = flagged(p.Commands[1], "as") },
			[]string{`gonsole: command "report:list" declares the engine flag -as`}},
		{"flags that panic", func(p *gonsole.Program) {
			p.Commands[1].Flags = func(*flag.FlagSet) { panic("the owner flag is gone") }
		}, []string{`gonsole: command "report:list" panicked declaring its flags: the owner flag is gone`}},
		{"flags that declare one flag twice", func(p *gonsole.Program) {
			p.Commands[1].Flags = func(fs *flag.FlagSet) {
				fs.Bool("all", false, "every report")
				fs.Bool("all", false, "every report")
			}
		}, []string{`gonsole: command "report:list" panicked declaring its flags: report:list flag redefined: all`}},
		{"an old spelling starting with a base command", func(p *gonsole.Program) {
			p.Renamed["list all"] = "report:list"
		}, []string{`gonsole: old spelling "list all" starts with the base command list`}},
		{"an old spelling pointing at no command", func(p *gonsole.Program) { p.Renamed["report make"] = "report:make" },
			[]string{`gonsole: old spelling "report make" points at "report:make", which is no core command`}},
		{"an old spelling pointing at a base command", func(p *gonsole.Program) { p.Renamed["report all"] = "list" },
			[]string{`gonsole: old spelling "report all" points at "list", which is no core command`}},
		{"a command named like a namespace", func(p *gonsole.Program) { p.Commands = append(p.Commands, echo("report")) },
			[]string{`gonsole: command "report" is also a namespace`}},
		{"a command named like a reserved namespace", func(p *gonsole.Program) {
			p.Commands = append(p.Commands, echo("audit"))
		}, []string{`gonsole: command "audit" is also a namespace`}},
		{"a bare run that serves without a server", func(p *gonsole.Program) { p.BareServes = true },
			[]string{`gonsole: BareServes is set without Serve`}},
		{"a capability without Authorize", func(p *gonsole.Program) {
			p.Commands[1].Capability = "export_reports"
			p.Record = func(context.Context, gonsole.Call, string) error { return nil }
		}, []string{`gonsole: command "report:list" names capability export_reports without Authorize`}},
		{"a capability without Record", func(p *gonsole.Program) {
			p.Commands[1].Capability = "export_reports"
			p.Authorize = func(context.Context, gonsole.Call, string) error { return nil }
		}, []string{`gonsole: command "report:list" names capability export_reports without Record`}},
		{"a capability without Authorize and Record", func(p *gonsole.Program) {
			p.Commands[1].Capability = "export_reports"
		}, []string{
			`gonsole: command "report:list" names capability export_reports without Authorize`,
			`gonsole: command "report:list" names capability export_reports without Record`,
		}},
		{"several offences", func(p *gonsole.Program) {
			p.Commands = append(p.Commands, echo("list"), echo("Report"))
			p.BareServes = true
		}, []string{
			`gonsole: command "list" is a base command`,
			`gonsole: command name "Report" ` + malformed,
			`gonsole: BareServes is set without Serve`,
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			p := sound()
			tc.change(&p)

			got := offences(p.Check(gonsole.Loaded{}))

			if strings.Join(got, "\n") != strings.Join(tc.want, "\n") {
				t.Errorf("Check() = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestCheckRefusesEveryBaseCommandAsAProgramCommand(t *testing.T) {
	t.Parallel()

	for _, name := range []string{"help", "list", "version", "serve", "check", "migrate", "seed"} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			bare, namespaced := sound(), sound()
			bare.Commands = append(bare.Commands, echo(name))
			namespaced.Commands = append(namespaced.Commands, echo(name+":status"))

			got := offences(errors.Join(bare.Check(gonsole.Loaded{}), namespaced.Check(gonsole.Loaded{})))

			want := []string{
				`gonsole: command "` + name + `" is a base command`,
				`gonsole: command "` + name + `:status" is in the engine namespace ` + name,
			}
			if strings.Join(got, "\n") != strings.Join(want, "\n") {
				t.Errorf("Check() = %q, want %q", got, want)
			}
		})
	}
}

func TestCheckRefusesOldSpellingsInOrder(t *testing.T) {
	t.Parallel()

	p := sound()
	p.Renamed["report make"] = "report:make"
	p.Renamed["list all"] = "report:list"
	p.Renamed["audit show"] = "audit:show"

	got := offences(p.Check(gonsole.Loaded{}))

	want := []string{
		`gonsole: old spelling "audit show" points at "audit:show", which is no core command`,
		`gonsole: old spelling "list all" starts with the base command list`,
		`gonsole: old spelling "report make" points at "report:make", which is no core command`,
	}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Errorf("Check() = %q, want %q", got, want)
	}
}

func TestCheckRefusesThePluginOffences(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name  string
		group gonsole.Group
		want  []string
	}{
		{"a plugin named like a base command", gonsole.Group{Namespace: "list",
			Commands: []gonsole.Command{echo("list:all")}},
			[]string{`gonsole: plugin list takes the name of the base command list`}},
		{"a plugin named like a base command with a command of its own offence", gonsole.Group{Namespace: "list",
			Commands: []gonsole.Command{summarized(echo("list:all"), "")}},
			[]string{
				`gonsole: plugin list takes the name of the base command list`,
				`gonsole: command "list:all" has no summary`,
			}},
		{"a plugin named like a core command", gonsole.Group{Namespace: "status",
			Commands: []gonsole.Command{echo("status:all")}},
			[]string{`gonsole: plugin status takes the name of the core command status`}},
		{"a plugin named like a core namespace", gonsole.Group{Namespace: "report",
			Commands: []gonsole.Command{echo("report:sync")}},
			[]string{`gonsole: plugin report takes the core namespace report`}},
		{"a plugin named like a reserved namespace", gonsole.Group{Namespace: "audit",
			Commands: []gonsole.Command{echo("audit:sync")}},
			[]string{`gonsole: plugin audit takes the reserved namespace audit`}},
		{"a command in another namespace", gonsole.Group{Namespace: "demo",
			Commands: []gonsole.Command{echo("other:sync")}},
			[]string{`gonsole: command "other:sync" of plugin demo is outside its namespace`}},
		{"a command without a namespace", gonsole.Group{Namespace: "demo",
			Commands: []gonsole.Command{echo("sync")}},
			[]string{`gonsole: command "sync" of plugin demo is outside its namespace`}},
		{"a command named like its plugin", gonsole.Group{Namespace: "sync",
			Commands: []gonsole.Command{echo("sync")}},
			[]string{`gonsole: command "sync" of plugin sync is outside its namespace`}},
		{"a command that needs a capability the program cannot check", gonsole.Group{Namespace: "demo",
			Commands: []gonsole.Command{{Name: "demo:sync", Summary: "sync the demo", Capability: "manage_demo",
				Run: func(context.Context, gonsole.Call) error { return nil }}}},
			[]string{
				`gonsole: command "demo:sync" names capability manage_demo without Authorize`,
				`gonsole: command "demo:sync" names capability manage_demo without Record`,
			}},
		{"two commands with one name", gonsole.Group{Namespace: "demo",
			Commands: []gonsole.Command{echo("demo:sync"), echo("demo:sync")}},
			[]string{`gonsole: command "demo:sync" is declared twice`}},
		{"a command without a summary", gonsole.Group{Namespace: "demo",
			Commands: []gonsole.Command{summarized(echo("demo:sync"), "")}},
			[]string{`gonsole: command "demo:sync" has no summary`}},
		{"a command that asks for the core schema steps", gonsole.Group{Namespace: "demo",
			Commands: []gonsole.Command{{Name: "demo:sync", Summary: "sync the demo", Migrates: true,
				Run: func(context.Context, gonsole.Call) error { return nil }}}},
			[]string{`gonsole: plugin command "demo:sync" asks for the core schema steps`}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			got := offences(sound().Check(gonsole.Loaded{Groups: []gonsole.Group{tc.group}}))

			if strings.Join(got, "\n") != strings.Join(tc.want, "\n") {
				t.Errorf("Check() = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestCheckJoinsEveryOffenceIntoOneError(t *testing.T) {
	t.Parallel()

	p := sound()
	p.Commands = append(p.Commands, echo("list"))

	err := p.Check(gonsole.Loaded{Groups: []gonsole.Group{{Namespace: "demo", Commands: []gonsole.Command{echo("sync")}}}})

	var joined interface{ Unwrap() []error }
	if !errors.As(err, &joined) || len(joined.Unwrap()) != 2 {
		t.Errorf("Check() = %v, want two joined offences", err)
	}
}

func TestRunRefusesABrokenProgramBeforeAnythingElse(t *testing.T) {
	t.Parallel()

	for _, args := range [][]string{{"status"}, {"list"}, {"-h"}, nil} {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			t.Parallel()

			p := sound()
			p.Commands = append(p.Commands, echo("list"))

			got := execute(t, p, args...)

			if got.code != gonsole.ExitFailed {
				t.Errorf("code = %d, want %d", got.code, gonsole.ExitFailed)
			}
			if want := "myapp: gonsole: command \"list\" is a base command\n"; got.stderr != want {
				t.Errorf("stderr = %q, want %q", got.stderr, want)
			}
			if got.stdout != "" {
				t.Errorf("stdout = %q, want nothing run", got.stdout)
			}
		})
	}
}

func TestCheckCommandReportsTheSettings(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name     string
		validate func(context.Context, gonsole.Call) error
		code     int
		stdout   string
		stderr   string
	}{
		{"a program without Validate", nil, gonsole.ExitDone, "settings, plugins and command names are valid\n", ""},
		{"settings that pass", func(context.Context, gonsole.Call) error { return nil }, gonsole.ExitDone,
			"settings, plugins and command names are valid\n", ""},
		{"settings that fail", func(_ context.Context, call gonsole.Call) error {
			_, window := call.Env.Duration("WINDOW", 0)
			_, batch := call.Env.Count("BATCH", 1)
			return errors.Join(window, batch)
		}, gonsole.ExitFailed, "", "myapp: MYAPP_WINDOW: must be a duration like 30s, got \"soon\"\n" +
			"myapp: MYAPP_BATCH: must be a whole number, got \"many\"\n"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			p := sound()
			p.Env = settings(map[string]string{"MYAPP_WINDOW": "soon", "MYAPP_BATCH": "many"})
			p.Validate = tc.validate

			got := execute(t, p, "check")

			if got.code != tc.code {
				t.Errorf("code = %d, want %d", got.code, tc.code)
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

func TestCheckCommandReportsThePlugins(t *testing.T) {
	t.Parallel()

	const valid = "settings, plugins and command names are valid\n"
	checked := []string{"validate", "register describe=false", "release live=true"}
	cases := []struct {
		name     string
		validate error
		groups   []gonsole.Group
		failed   error
		fail     error
		code     int
		stdout   string
		stderr   string
		log      []string
	}{
		{"plugins that load", nil, demoGroups(), nil, nil, gonsole.ExitDone, valid, "", checked},
		{"groups that break the rules", nil, []gonsole.Group{
			{Namespace: "list", Commands: []gonsole.Command{echo("list:all")}},
		}, nil, nil, gonsole.ExitFailed, "", "myapp: gonsole: plugin list takes the name of the base command list\n",
			checked},
		{"plugins that failed beside groups that break the rules", nil, []gonsole.Group{
			{Namespace: "list", Commands: []gonsole.Command{echo("list:all")}},
			{Namespace: "demo", Commands: []gonsole.Command{summarized(echo("demo:sync"), ""), echo("demo:list")}},
		}, pluginFailures, nil, gonsole.ExitFailed, "", pluginFailureLines +
			"myapp: gonsole: plugin list takes the name of the base command list\n" +
			"myapp: gonsole: command \"demo:sync\" has no summary\n", checked},
		{"a registration that fails", nil, demoGroups(), errors.New("plugin mail: no relay host"),
			errors.New("the plugin table is locked"), gonsole.ExitFailed, "", "myapp: the plugin table is locked\n",
			checked},
		{"settings that fail", errors.New("MYAPP_ADDR: must be a port, got \"web\""), demoGroups(), nil, nil,
			gonsole.ExitFailed, "", "myapp: MYAPP_ADDR: must be a port, got \"web\"\n", []string{"validate"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			r := &registry{groups: tc.groups, failed: tc.failed, fail: tc.fail}
			p := plugged(r)
			p.Validate = func(context.Context, gonsole.Call) error {
				r.note("validate")
				return tc.validate
			}

			got := execute(t, p, "check")

			if got.code != tc.code || got.stdout != tc.stdout || got.stderr != tc.stderr {
				t.Errorf("check = %d, %q, %q, want %d, %q, %q", got.code, got.stdout, got.stderr, tc.code, tc.stdout,
					tc.stderr)
			}
			if !slices.Equal(r.log, tc.log) {
				t.Errorf("calls = %q, want %q", r.log, tc.log)
			}
		})
	}
}

func TestRunTurnsAPanicIntoAFailure(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name   string
		change func(p *gonsole.Program)
		line   string
	}{
		{"a run that panics", func(p *gonsole.Program) {
			p.Commands[1].Run = func(context.Context, gonsole.Call) error { panic("the report store vanished") }
		}, "myapp: report:list: panic: the report store vanished\n"},
		{"a run that panics with a misuse", func(p *gonsole.Program) {
			p.Commands[1].Run = func(context.Context, gonsole.Call) error {
				panic(gonsole.Misuse(errors.New("the report store vanished")))
			}
		}, "myapp: report:list: panic: the report store vanished\n"},
		{"an account check that panics", func(p *gonsole.Program) {
			p.Commands[1].Capability = "export_reports"
			p.Authorize = func(context.Context, gonsole.Call, string) error { panic("the role table vanished") }
			p.Record = func(context.Context, gonsole.Call, string) error { return nil }
		}, "myapp: report:list: panic: the role table vanished\n"},
		{"a record that panics", func(p *gonsole.Program) {
			p.Commands[1].Capability = "export_reports"
			p.Authorize = func(context.Context, gonsole.Call, string) error { return nil }
			p.Record = func(context.Context, gonsole.Call, string) error { panic("the record table vanished") }
		}, "myapp: report:list: panic: the record table vanished\n"},
		{"a flag value that panics while it is read", func(p *gonsole.Program) {
			p.Commands[1].Flags = func(fs *flag.FlagSet) { fs.Var(fragile{}, "since", "the first day") }
		}, "myapp: report:list: panic: the calendar vanished\n"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			p := sound()
			tc.change(&p)
			args := []string{"report:list", "-since", "monday"}
			if p.Commands[1].Flags == nil {
				args = args[:1]
			}
			if p.Commands[1].Capability != "" {
				args = append(args, "-as", actingAccount)
			}

			got := execute(t, p, args...)

			if got.code != gonsole.ExitFailed {
				t.Errorf("code = %d, want %d", got.code, gonsole.ExitFailed)
			}
			stack, opened := strings.CutPrefix(got.stderr, tc.line)
			if !opened {
				t.Errorf("stderr opens with %q, want %q", firstLine(got.stderr), tc.line)
			}
			if !strings.HasPrefix(stack, "goroutine ") {
				t.Errorf("after the panic line = %q, want the raw stack", firstLine(stack))
			}
		})
	}
}

// fragile is a flag value whose every read panics.
type fragile struct{}

// String returns the empty text of the value.
func (fragile) String() string {
	return ""
}

// Set panics.
func (fragile) Set(string) error {
	panic("the calendar vanished")
}

func TestRunRefusesFlagsThatPanicOnlyWhenTheRunDeclaresThem(t *testing.T) {
	t.Parallel()

	for _, args := range [][]string{{"report:list"}, {"report:list", "-h"}, {"help", "report:list"}} {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			t.Parallel()

			declared := 0
			p := sound()
			p.Commands[1].Flags = func(*flag.FlagSet) {
				declared++
				if declared > 1 {
					panic("the owner flag is gone")
				}
			}

			got := execute(t, p, args...)

			if got.code != gonsole.ExitFailed {
				t.Errorf("code = %d, want %d", got.code, gonsole.ExitFailed)
			}
			want := "myapp: gonsole: command \"report:list\" panicked declaring its flags: the owner flag is gone\n"
			if got.stderr != want {
				t.Errorf("stderr = %q, want %q", got.stderr, want)
			}
			if got.stdout != "" {
				t.Errorf("stdout = %q, want nothing", got.stdout)
			}
		})
	}
}

func TestCheckWritesNothingToTheProcessStderr(t *testing.T) {
	read, write, err := os.Pipe()
	if err != nil {
		t.Fatalf("os.Pipe() error = %v", err)
	}
	saved := os.Stderr
	os.Stderr = write
	p := sound()
	p.Commands[1].Flags = func(fs *flag.FlagSet) {
		fs.Bool("all", false, "every report")
		fs.Bool("all", false, "every report")
	}

	_ = p.Check(gonsole.Loaded{})

	os.Stderr = saved
	if err := write.Close(); err != nil {
		t.Fatalf("closing the pipe: %v", err)
	}
	leaked, err := io.ReadAll(read)
	if err != nil {
		t.Fatalf("reading the pipe: %v", err)
	}
	if len(leaked) != 0 {
		t.Errorf("process stderr = %q, want nothing", leaked)
	}
}

// offending returns a plugin group, demo, holding demo:ok, one command for each offence and demo:twice declared twice.
func offending() gonsole.Group {
	idle := echo("demo:idle")
	idle.Run = nil
	fragile := echo("demo:fragile")
	fragile.Flags = func(*flag.FlagSet) { panic("the flag table vanished") }
	schema := echo("demo:schema")
	schema.Migrates = true
	again := echo("demo:twice")
	again.Run = func(_ context.Context, call gonsole.Call) error {
		_, err := io.WriteString(call.Stdout, "the second demo:twice\n")
		return err
	}
	return gonsole.Group{Namespace: "demo", Commands: []gonsole.Command{
		echo("demo:ok"), echo("demo:Sync"), summarized(echo("demo:quiet"), ""),
		summarized(echo("demo:split"), "sync\nthe demo"), idle, flagged(echo("demo:loud"), "yes"), fragile, schema,
		echo("other:x"), echo("sync"), echo("demo:twice"), again, summarized(echo("demo:gone"), ""), echo("demo:gone"),
	}}
}

func TestRunDropsThePluginCommandsThatBreakARule(t *testing.T) {
	t.Parallel()

	cases := []struct {
		args   []string
		code   int
		stdout string
		stderr string
	}{
		{[]string{"demo:ok"}, gonsole.ExitDone, "demo:ok\n", ""},
		{[]string{"demo:twice"}, gonsole.ExitDone, "demo:twice\n", ""},
		{[]string{"demo:Sync"}, gonsole.ExitMisused, "", unknownLine("demo:Sync")},
		{[]string{"demo:quiet"}, gonsole.ExitFailed, "", "myapp: gonsole: command \"demo:quiet\" has no summary\n"},
		{[]string{"demo:quiet", "-h"}, gonsole.ExitFailed, "",
			"myapp: gonsole: command \"demo:quiet\" has no summary\n"},
		{[]string{"help", "demo:quiet"}, gonsole.ExitFailed, "",
			"myapp: gonsole: command \"demo:quiet\" has no summary\n"},
		{[]string{"sync"}, gonsole.ExitFailed, "",
			"myapp: gonsole: command \"sync\" of plugin demo is outside its namespace\n"},
		{[]string{"demo:gone"}, gonsole.ExitFailed, "",
			"myapp: gonsole: command \"demo:gone\" has no summary\nmyapp: gonsole: command \"demo:gone\" is declared twice\n"},
		{[]string{"demo:split"}, gonsole.ExitFailed, "",
			"myapp: gonsole: command \"demo:split\" has a summary of more than one line\n"},
		{[]string{"demo:idle"}, gonsole.ExitFailed, "", "myapp: gonsole: command \"demo:idle\" has no run\n"},
		{[]string{"demo:loud"}, gonsole.ExitFailed, "",
			"myapp: gonsole: command \"demo:loud\" declares the engine flag -yes\n"},
		{[]string{"demo:fragile"}, gonsole.ExitFailed, "",
			"myapp: gonsole: command \"demo:fragile\" panicked declaring its flags: the flag table vanished\n"},
		{[]string{"demo:schema"}, gonsole.ExitFailed, "",
			"myapp: gonsole: plugin command \"demo:schema\" asks for the core schema steps\n"},
		{[]string{"other:x"}, gonsole.ExitFailed, "",
			"myapp: gonsole: command \"other:x\" of plugin demo is outside its namespace\n"},
		{[]string{"demo:nope"}, gonsole.ExitMisused, "",
			"myapp: unknown command \"demo:nope\", want demo:ok or demo:twice\n"},
	}
	for _, tc := range cases {
		t.Run(strings.Join(tc.args, " "), func(t *testing.T) {
			t.Parallel()

			got := execute(t, plugged(&registry{groups: []gonsole.Group{offending()}}), tc.args...)

			if got.code != tc.code || got.stdout != tc.stdout || got.stderr != tc.stderr {
				t.Errorf("run = %d, %q, %q, want %d, %q, %q", got.code, got.stdout, got.stderr, tc.code, tc.stdout,
					tc.stderr)
			}
		})
	}
}

func TestRunDropsAPluginCommandTheProgramCannotGuard(t *testing.T) {
	t.Parallel()

	cases := []struct {
		args   []string
		code   int
		stdout string
		stderr string
	}{
		{[]string{"demo:move", "-as", actingAccount}, gonsole.ExitFailed, "",
			"myapp: gonsole: command \"demo:move\" names capability manage_demo without Authorize\n" +
				"myapp: gonsole: command \"demo:move\" names capability manage_demo without Record\n"},
		{[]string{"demo:sync", "-yes"}, gonsole.ExitDone, "sync apply=true\n", ""},
	}
	for _, tc := range cases {
		t.Run(strings.Join(tc.args, " "), func(t *testing.T) {
			t.Parallel()

			p := plugged(&registry{groups: demoGroups()})
			p.Authorize, p.Record = nil, nil

			got := execute(t, p, tc.args...)

			if got.code != tc.code || got.stdout != tc.stdout || got.stderr != tc.stderr {
				t.Errorf("run = %d, %q, %q, want %d, %q, %q", got.code, got.stdout, got.stderr, tc.code, tc.stdout,
					tc.stderr)
			}
		})
	}
}

func TestRunAnswersAGroupLeftWithoutCommandsAsNoGroup(t *testing.T) {
	t.Parallel()

	for _, name := range []string{"demo:x", "demo"} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			r := &registry{groups: []gonsole.Group{{Namespace: "demo", Commands: []gonsole.Command{echo("demo:Sync")}}}}

			got := execute(t, plugged(r), name)

			if got.code != gonsole.ExitMisused || got.stderr != unknownLine(name) {
				t.Errorf("run = %d, %q, want %d, %q", got.code, got.stderr, gonsole.ExitMisused, unknownLine(name))
			}
		})
	}
}

func TestRunMergesTwoGroupsOfOneNamespace(t *testing.T) {
	t.Parallel()

	again := echo("demo:a")
	again.Run = func(_ context.Context, call gonsole.Call) error {
		_, err := io.WriteString(call.Stdout, "the second demo:a\n")
		return err
	}
	cases := []struct {
		args   []string
		code   int
		stdout string
		stderr string
	}{
		{[]string{"demo:a"}, gonsole.ExitDone, "demo:a\n", ""},
		{[]string{"demo:b"}, gonsole.ExitDone, "demo:b\n", ""},
		{[]string{"demo:x"}, gonsole.ExitMisused, "", "myapp: unknown command \"demo:x\", want demo:a or demo:b\n"},
	}
	for _, tc := range cases {
		t.Run(strings.Join(tc.args, " "), func(t *testing.T) {
			t.Parallel()

			r := &registry{groups: []gonsole.Group{
				{Namespace: "demo", Commands: []gonsole.Command{echo("demo:a")}},
				{Namespace: "demo", Commands: []gonsole.Command{again, echo("demo:b")}},
			}}

			got := execute(t, plugged(r), tc.args...)

			if got.code != tc.code || got.stdout != tc.stdout || got.stderr != tc.stderr {
				t.Errorf("run = %d, %q, %q, want %d, %q, %q", got.code, got.stdout, got.stderr, tc.code, tc.stdout,
					tc.stderr)
			}
		})
	}
}
