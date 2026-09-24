// SPDX-License-Identifier: Apache-2.0

package gonsole_test

import (
	"context"
	"errors"
	"fmt"
	"slices"
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

// unknownLine returns the line of a name no command owns.
func unknownLine(name string) string {
	return fmt.Sprintf("myapp: unknown command %q, run \"myapp list\" to see every command\n", name)
}

// demoLine returns the line of a name in the demo namespace that no demo command owns.
func demoLine(name string) string {
	return fmt.Sprintf("myapp: unknown command %q, want demo:list, demo:move or demo:sync\n", name)
}

// dryRun is the line a dry run of myapp ends with on stderr.
const dryRun = "myapp: dry run, nothing changed, pass -yes to apply\n"

// ranPlugins is the log of a run that registered the plugins to run a command.
var ranPlugins = []string{"register describe=false", "release live=true"}

// describedPlugins is the log of a run that registered the plugins to describe them.
var describedPlugins = []string{"register describe=true", "release live=true"}

// syncPage is the help page of the demo:sync command demoGroups declares.
const syncPage = `sync the demo

Usage:
  myapp demo:sync [flags]

Flags:
  -yes
    	apply the change, a dry run without it
`

// movePage is the help page of the demo:move command demoGroups declares.
const movePage = `move the demo

Usage:
  myapp demo:move [flags]

Flags:
  -as email
    	email address of the account acting
`

// tenancyGroup returns a plugin group, tenancy, offering tenancy:list.
func tenancyGroup() gonsole.Group {
	return gonsole.Group{Namespace: "tenancy", Commands: []gonsole.Command{echo("tenancy:list")}}
}

func TestRunRunsThePluginCommandTheLineNames(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name   string
		args   []string
		stdout string
		stderr string
		log    []string
	}{
		{"a dry run of a write", []string{"demo:sync"}, "sync apply=false\n", dryRun, ranPlugins},
		{"an applied write", []string{"demo:sync", "-yes"}, "sync apply=true\n", "", ranPlugins},
		{"a read", []string{"demo:list"}, "synced\n", "", ranPlugins},
		{"a read that answers a document", []string{"demo:list", "-json"}, "{\n  \"demo\": [\n    \"synced\"\n  ]\n}\n",
			"", ranPlugins},
		{"a command that acts", []string{"demo:move", "-as", actingAccount}, "moved as " + actingAccount + "\n", "",
			[]string{
				"register describe=false", "authorize " + actingAccount + " for manage_demo",
				"record " + actingAccount + " ran demo:move", "release live=true",
			}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			r := &registry{groups: demoGroups()}

			got := execute(t, plugged(r), tc.args...)

			if got.code != gonsole.ExitDone || got.stdout != tc.stdout || got.stderr != tc.stderr {
				t.Errorf("run = %d, %q, %q, want 0, %q, %q", got.code, got.stdout, got.stderr, tc.stdout, tc.stderr)
			}
			if !slices.Equal(r.log, tc.log) {
				t.Errorf("calls = %q, want %q", r.log, tc.log)
			}
		})
	}
}

func TestRunChecksTheActingAccountOfAPluginCommand(t *testing.T) {
	t.Parallel()

	refused := errors.New("maria.perez@example.com lacks manage_demo")
	cases := []struct {
		name   string
		args   []string
		refuse error
		code   int
		stderr string
		log    []string
	}{
		{"no acting account", []string{"demo:move"}, nil, gonsole.ExitMisused,
			"myapp: demo:move wants -as <email>\n\n" + movePage, ranPlugins},
		{"an account that lacks the capability", []string{"demo:move", "-as", actingAccount}, refused,
			gonsole.ExitFailed, "myapp: " + refused.Error() + "\n", []string{
				"register describe=false", "authorize " + actingAccount + " for manage_demo", "release live=true",
			}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			r := &registry{groups: demoGroups(), refuse: tc.refuse}

			got := execute(t, plugged(r), tc.args...)

			if got.code != tc.code || got.stdout != "" || got.stderr != tc.stderr {
				t.Errorf("run = %d, %q, %q, want %d, nothing, %q", got.code, got.stdout, got.stderr, tc.code, tc.stderr)
			}
			if !slices.Equal(r.log, tc.log) {
				t.Errorf("calls = %q, want %q", r.log, tc.log)
			}
		})
	}
}

func TestRunNamesTheCommandsOfAPluginNamespace(t *testing.T) {
	t.Parallel()

	cases := []struct {
		args []string
		line string
		log  []string
	}{
		{[]string{"demo:nope"}, demoLine("demo:nope"), ranPlugins},
		{[]string{"demo"}, demoLine("demo"), describedPlugins},
		{[]string{"help", "demo"}, demoLine("demo"), describedPlugins},
		{[]string{"demo", "-h"}, demoLine("demo"), describedPlugins},
	}
	for _, tc := range cases {
		t.Run(strings.Join(tc.args, " "), func(t *testing.T) {
			t.Parallel()

			r := &registry{groups: demoGroups()}

			got := execute(t, plugged(r), tc.args...)

			if got.code != gonsole.ExitMisused || got.stderr != tc.line {
				t.Errorf("run = %d, %q, want %d, %q", got.code, got.stderr, gonsole.ExitMisused, tc.line)
			}
			if !slices.Equal(r.log, tc.log) {
				t.Errorf("calls = %q, want %q", r.log, tc.log)
			}
		})
	}
}

func TestRunAnswersANamespaceNoPluginOwns(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name   string
		groups []gonsole.Group
		failed error
		fail   error
		code   int
		stderr string
	}{
		{"plugins in other namespaces", []gonsole.Group{tenancyGroup(), demoGroups()[0]}, nil, nil,
			gonsole.ExitMisused, "myapp: unknown command \"audit:list\", want a command in demo or tenancy\n"},
		{"plugins in one other namespace", demoGroups(), nil, nil, gonsole.ExitMisused,
			"myapp: unknown command \"audit:list\", want a command in demo\n"},
		{"no plugin command", nil, nil, nil, gonsole.ExitMisused, unknownLine("audit:list")},
		{"plugins that failed", demoGroups(), errors.Join(
			errors.New("plugin billing: no signing key"), errors.New("plugin mail: no relay host")), nil,
			gonsole.ExitFailed, "myapp: plugin billing: no signing key\nmyapp: plugin mail: no relay host\n"},
		{"a registration that fails", demoGroups(), nil, errors.New("the plugin table is locked"),
			gonsole.ExitFailed, "myapp: the plugin table is locked\n"},
		{"a registration that fails beside plugins that failed", demoGroups(),
			errors.New("plugin mail: no relay host"), errors.New("the plugin table is locked"), gonsole.ExitFailed,
			"myapp: the plugin table is locked\n"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			r := &registry{groups: tc.groups, failed: tc.failed, fail: tc.fail}

			got := execute(t, plugged(r), "audit:list")

			if got.code != tc.code || got.stderr != tc.stderr {
				t.Errorf("run = %d, %q, want %d, %q", got.code, got.stderr, tc.code, tc.stderr)
			}
			if !slices.Equal(r.log, ranPlugins) {
				t.Errorf("calls = %q, want %q", r.log, ranPlugins)
			}
		})
	}
}

func TestRunKeepsTheLinesOfNamesNoPluginMayOwn(t *testing.T) {
	t.Parallel()

	pluginsCommand := "myapp: unknown command %q, want report:plugins\n"
	cases := []struct {
		args   []string
		stderr string
	}{
		{[]string{"status:x"}, unknownLine("status:x")},
		{[]string{"migrate:status"}, unknownLine("migrate:status")},
		{[]string{"serve"}, unknownLine("serve")},
		{[]string{"audit:list"}, unknownLine("audit:list")},
		{[]string{"-v"}, unknownLine("-v")},
		{[]string{"Demo:sync"}, unknownLine("Demo:sync")},
		{[]string{"demo:"}, unknownLine("demo:")},
		{[]string{"demo:Sync"}, unknownLine("demo:Sync")},
		{[]string{"demo:sync:all"}, unknownLine("demo:sync:all")},
		{[]string{"demo:Sync", "-h"}, unknownLine("demo:Sync")},
		{[]string{"help", "demo:Sync"}, unknownLine("demo:Sync")},
		{[]string{"report"}, fmt.Sprintf(pluginsCommand, "report")},
		{[]string{"report:delete"}, fmt.Sprintf(pluginsCommand, "report:delete")},
	}
	for _, tc := range cases {
		t.Run(strings.Join(tc.args, " "), func(t *testing.T) {
			t.Parallel()

			r := &registry{groups: []gonsole.Group{
				demoGroups()[0], {Namespace: "audit", Commands: []gonsole.Command{echo("audit:list")}},
			}}
			p := plugged(r)
			p.Reserved = []string{"audit"}

			got := execute(t, p, tc.args...)

			if got.code != gonsole.ExitMisused || got.stderr != tc.stderr {
				t.Errorf("run = %d, %q, want %d, %q", got.code, got.stderr, gonsole.ExitMisused, tc.stderr)
			}
			if len(r.log) != 0 {
				t.Errorf("calls = %q, want none", r.log)
			}
		})
	}
}

func TestRunDescribesThePluginsForANameWithoutANamespace(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name   string
		failed error
		fail   error
	}{
		{"plugins that loaded", nil, nil},
		{"plugins that failed", errors.New("plugin billing: no signing key"), nil},
		{"a registration that fails", nil, errors.New("the plugin table is locked")},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			r := &registry{groups: demoGroups(), failed: tc.failed, fail: tc.fail}

			got := execute(t, plugged(r), "reprot")

			if got.code != gonsole.ExitMisused || got.stderr != unknownLine("reprot") {
				t.Errorf("run = %d, %q, want %d, %q", got.code, got.stderr, gonsole.ExitMisused, unknownLine("reprot"))
			}
			if !slices.Equal(r.log, describedPlugins) {
				t.Errorf("calls = %q, want %q", r.log, describedPlugins)
			}
		})
	}
}

func TestRunWarnsOfFailedPluginsBeforeAPluginCommand(t *testing.T) {
	t.Parallel()

	billing, mail := errors.New("plugin billing: no signing key"), errors.New("plugin mail: no relay host")
	warnings := "myapp: warning: plugin billing: no signing key\nmyapp: warning: plugin mail: no relay host\n"
	cases := []struct {
		name   string
		args   []string
		failed error
		code   int
		stderr string
	}{
		{"a run", []string{"demo:sync"}, errors.Join(billing, mail), gonsole.ExitDone, warnings + dryRun},
		{"a failure of two lines", []string{"demo:sync"},
			errors.Join(errors.Join(billing, mail), errors.New("plugin chat: no token\nand no webhook")),
			gonsole.ExitDone,
			warnings + "myapp: warning: plugin chat: no token\nmyapp: warning: and no webhook\n" + dryRun},
		{"a misused run", []string{"demo:sync", "-bogus"}, errors.Join(billing, mail), gonsole.ExitMisused,
			warnings + "myapp: demo:sync: flag provided but not defined: -bogus\n\n" + syncPage},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			got := execute(t, plugged(&registry{groups: demoGroups(), failed: tc.failed}), tc.args...)

			if got.code != tc.code || got.stderr != tc.stderr {
				t.Errorf("run = %d, %q, want %d, %q", got.code, got.stderr, tc.code, tc.stderr)
			}
		})
	}
}

func TestHelpOfAPluginCommandDescribesThePlugins(t *testing.T) {
	t.Parallel()

	cases := []struct {
		args []string
		page string
	}{
		{[]string{"demo:sync", "-h"}, syncPage},
		{[]string{"help", "demo:sync"}, syncPage},
		{[]string{"help", "demo:move"}, movePage},
	}
	for _, tc := range cases {
		t.Run(strings.Join(tc.args, " "), func(t *testing.T) {
			t.Parallel()

			r := &registry{groups: demoGroups(), failed: errors.New("plugin billing: no signing key")}
			p := plugged(r)
			p.Migrations = []gonsole.Step{{Name: "reports", Run: func(context.Context, string) error {
				r.note("migrate reports")
				return nil
			}}}
			p.Lock = func(context.Context, string) (func(context.Context) error, error) {
				r.note("lock")
				return func(context.Context) error { return nil }, nil
			}

			got := execute(t, p, tc.args...)

			if got.code != gonsole.ExitDone || got.stdout != tc.page || got.stderr != "" {
				t.Errorf("help = %d, %q, %q, want 0, %q and no warning", got.code, got.stdout, got.stderr, tc.page)
			}
			if !slices.Equal(r.log, describedPlugins) {
				t.Errorf("calls = %q, want %q", r.log, describedPlugins)
			}
		})
	}
}

func TestHelpOfAPluginCommandFailsWhenItsPluginDidNotLoad(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name   string
		groups []gonsole.Group
		failed error
		fail   error
		stderr string
	}{
		{"a registration that fails", demoGroups(), nil, errors.New("the plugin table is locked"),
			"myapp: the plugin table is locked\n"},
		{"a plugin that failed", []gonsole.Group{tenancyGroup()}, errors.Join(
			errors.New("plugin demo: no signing key"), errors.New("plugin mail: no relay host")), nil,
			"myapp: plugin demo: no signing key\nmyapp: plugin mail: no relay host\n"},
		{"a registration that fails beside a plugin that failed", demoGroups(),
			errors.New("plugin mail: no relay host"), errors.New("the plugin table is locked"),
			"myapp: the plugin table is locked\n"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			r := &registry{groups: tc.groups, failed: tc.failed, fail: tc.fail}

			got := execute(t, plugged(r), "demo:sync", "-h")

			if got.code != gonsole.ExitFailed || got.stdout != "" || got.stderr != tc.stderr {
				t.Errorf("help = %d, %q, %q, want %d, nothing, %q", got.code, got.stdout, got.stderr,
					gonsole.ExitFailed, tc.stderr)
			}
			if !slices.Equal(r.log, describedPlugins) {
				t.Errorf("calls = %q, want %q", r.log, describedPlugins)
			}
		})
	}
}

func TestRunFailsAPluginCommandWhenTheRegistrationFails(t *testing.T) {
	t.Parallel()

	r := &registry{groups: demoGroups(), fail: errors.New("the plugin table is locked")}

	got := execute(t, plugged(r), "demo:sync", "-yes")

	if want := "myapp: the plugin table is locked\n"; got.code != gonsole.ExitFailed || got.stdout != "" ||
		got.stderr != want {
		t.Errorf("run = %d, %q, %q, want %d, nothing, %q", got.code, got.stdout, got.stderr, gonsole.ExitFailed, want)
	}
	if !slices.Equal(r.log, ranPlugins) {
		t.Errorf("calls = %q, want %q", r.log, ranPlugins)
	}
}

func TestRunNeverMigratesForAPluginCommand(t *testing.T) {
	t.Parallel()

	r := &registry{groups: demoGroups()}
	p := plugged(r)
	p.Migrations = []gonsole.Step{{Name: "reports", Run: func(context.Context, string) error {
		r.note("migrate reports")
		return nil
	}}}
	p.Lock = func(context.Context, string) (func(context.Context) error, error) {
		r.note("lock")
		return func(context.Context) error { return nil }, nil
	}

	got := execute(t, p, "demo:sync", "-yes")

	if got.code != gonsole.ExitDone || !slices.Equal(r.log, ranPlugins) {
		t.Errorf("run = %d, calls = %q, want 0, %q", got.code, r.log, ranPlugins)
	}
}

func TestRunKeepsPluginAndCoreCommandsBesideACollidingGroup(t *testing.T) {
	t.Parallel()

	cases := []struct {
		args   []string
		code   int
		stdout string
		stderr string
	}{
		{[]string{"demo:sync", "-yes"}, gonsole.ExitDone, "sync apply=true\n", ""},
		{[]string{"status"}, gonsole.ExitDone, "status\n", ""},
		{[]string{"list:all"}, gonsole.ExitMisused, "", unknownLine("list:all")},
		{[]string{"nope:x"}, gonsole.ExitMisused, "", "myapp: unknown command \"nope:x\", want a command in demo\n"},
	}
	for _, tc := range cases {
		t.Run(strings.Join(tc.args, " "), func(t *testing.T) {
			t.Parallel()

			r := &registry{groups: []gonsole.Group{
				{Namespace: "list", Commands: []gonsole.Command{echo("list:all")}}, demoGroups()[0],
			}}

			got := execute(t, plugged(r), tc.args...)

			if got.code != tc.code || got.stdout != tc.stdout || got.stderr != tc.stderr {
				t.Errorf("run = %d, %q, %q, want %d, %q, %q", got.code, got.stdout, got.stderr, tc.code, tc.stdout,
					tc.stderr)
			}
		})
	}
}

func TestRunAddsAReleaseFailureToAPluginMisuse(t *testing.T) {
	t.Parallel()

	r := &registry{groups: demoGroups(), lost: errors.New("the pool would not close")}

	got := execute(t, plugged(r), "demo:nope")

	want := demoLine("demo:nope") + "myapp: release the plugins: the pool would not close\n"
	if got.code != gonsole.ExitMisused || got.stderr != want {
		t.Errorf("run = %d, %q, want %d, %q", got.code, got.stderr, gonsole.ExitMisused, want)
	}
}

func TestRunTurnsARegistrationPanicIntoAFailureOfAPluginCommand(t *testing.T) {
	t.Parallel()

	for _, args := range [][]string{{"demo:sync"}, {"demo:sync", "-h"}, {"reprot"}} {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			t.Parallel()

			p := plugged(&registry{})
			p.Plugins = func(context.Context, gonsole.Call) (gonsole.Loaded, error) {
				panic("the plugin table vanished")
			}

			got := execute(t, p, args...)

			const line = "myapp: plugins: panic: the plugin table vanished\n"
			stack, opened := strings.CutPrefix(got.stderr, line)
			if got.code != gonsole.ExitFailed || !opened || !strings.HasPrefix(stack, "goroutine ") {
				t.Errorf("run = %d, %q, want %d, %q and the stack", got.code, firstLine(got.stderr),
					gonsole.ExitFailed, line)
			}
		})
	}
}
