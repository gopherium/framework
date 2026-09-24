// SPDX-License-Identifier: Apache-2.0

package gonsole_test

import (
	"bytes"
	"context"
	"errors"
	"flag"
	"slices"
	"strings"
	"testing"

	"github.com/gopherium/framework/gonsole"
)

// summarized returns cmd with summary as its summary.
func summarized(cmd gonsole.Command, summary string) gonsole.Command {
	cmd.Summary = summary
	return cmd
}

// catalog returns a program called myapp with a title, a version, a footer, two namespaces and an old spelling.
func catalog() gonsole.Program {
	return gonsole.Program{
		Name:    "myapp",
		Title:   "Myapp, a report keeper.",
		Version: "1.4.0",
		Footer:  "Read the guide at https://example.com/myapp.",
		Commands: []gonsole.Command{
			summarized(echo("status"), "print the store status"),
			summarized(echo("report:revoke", "id"), "revoke one report"),
			filing().Commands[0],
			summarized(echo("report:list"), "list every report"),
			summarized(echo("audit:export"), "export the audit trail"),
		},
		Renamed: map[string]string{"report new": "report:create"},
	}
}

// intro is the paragraph every listing prints before its commands.
const intro = "Every command answers -h. A command that offers -json answers one JSON document. " +
	"A command that offers -yes is a dry run until -yes.\n"

// catalogListing is the listing catalog prints.
const catalogListing = `Myapp, a report keeper. Version 1.4.0

Usage:
  myapp <command> [flags] [arguments]

` + intro + `
Available commands:
  check          check every setting, every plugin and every command name
  help           print the help of one command
  list           list every command
  status         print the store status
  version        print the version
 audit
  audit:export   export the audit trail
 report
  report:create  create a report
  report:list    list every report
  report:revoke  revoke one report

Read the guide at https://example.com/myapp.
`

// createPage is the help page of the report:create command filing declares.
const createPage = `create a report

Usage:
  myapp report:create [flags] <title>

Flags:
  -draft
    	keep the report as a draft
  -owner string
    	email address of the owner
`

// revokePage is the help page of the report:revoke command catalog declares.
const revokePage = `revoke one report

Usage:
  myapp report:revoke <id>
`

// listPage is the help page of the list base command.
const listPage = `list every command

Usage:
  myapp list
`

// helpPage is the help page of the help base command.
const helpPage = `print the help of one command

Usage:
  myapp help
`

// firstLine returns the first line of s with its newline.
func firstLine(s string) string {
	line, _, _ := strings.Cut(s, "\n")
	return line + "\n"
}

func TestListingShowsEveryCommandUnderItsNamespace(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		args []string
	}{
		{"no command", nil},
		{"the list command", []string{"list"}},
		{"the help command", []string{"help"}},
		{"a short help flag", []string{"-h"}},
		{"a long help flag", []string{"--help"}},
		{"the help command with a help flag", []string{"help", "-h"}},
		{"a help flag before a double dash and a name", []string{"-h", "--", "report:create"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			got := execute(t, catalog(), tc.args...)

			if got.code != gonsole.ExitDone {
				t.Errorf("code = %d, want %d, stderr %q", got.code, gonsole.ExitDone, got.stderr)
			}
			if got.stdout != catalogListing {
				t.Errorf("stdout = %q, want %q", got.stdout, catalogListing)
			}
			if got.stderr != "" {
				t.Errorf("stderr = %q, want empty", got.stderr)
			}
		})
	}
}

func TestListingLeavesOutWhatTheProgramDoesNotSet(t *testing.T) {
	t.Parallel()

	status := summarized(echo("status"), "print the store status")
	cases := []struct {
		name    string
		program gonsole.Program
		want    string
	}{
		{
			"a title without a version or a footer",
			gonsole.Program{Name: "myapp", Title: "Myapp, a report keeper.", Commands: []gonsole.Command{status}},
			`Myapp, a report keeper.

Usage:
  myapp <command> [flags] [arguments]

` + intro + `
Available commands:
  check    check every setting, every plugin and every command name
  help     print the help of one command
  list     list every command
  status   print the store status
  version  print the version
`,
		},
		{
			"a version without a title",
			gonsole.Program{Name: "myapp", Version: "1.4.0", Commands: []gonsole.Command{status}},
			`myapp Version 1.4.0

Usage:
  myapp <command> [flags] [arguments]

` + intro + `
Available commands:
  check    check every setting, every plugin and every command name
  help     print the help of one command
  list     list every command
  status   print the store status
  version  print the version
`,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			got := execute(t, tc.program, "list")

			if got.stdout != tc.want {
				t.Errorf("stdout = %q, want %q", got.stdout, tc.want)
			}
		})
	}
}

func TestHelpPrintsThePageOfTheNamedCommand(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name   string
		args   []string
		stdout string
		stderr string
	}{
		{"the help command", []string{"help", "report:create"}, createPage, ""},
		{"a short help flag", []string{"report:create", "-h"}, createPage, ""},
		{"a long help flag with one dash", []string{"report:create", "-help"}, createPage, ""},
		{"a short help flag with two dashes", []string{"report:create", "--h"}, createPage, ""},
		{"a long help flag", []string{"report:create", "--help"}, createPage, ""},
		{"a help flag before the name", []string{"-h", "report:create"}, createPage, ""},
		{"a help flag after arguments and flags", []string{"report:create", "Q3", "-owner", "x", "-h"}, createPage, ""},
		{"a command without flags", []string{"report:revoke", "-h"}, revokePage, ""},
		{"the list command", []string{"list", "-h"}, listPage, ""},
		{"the help command itself", []string{"help", "help"}, helpPage, ""},
		{"an old spelling", []string{"report", "new", "-h"}, createPage,
			"myapp: \"report new\" is deprecated, use \"report:create\"\n"},
		{"the help command before an old spelling", []string{"help", "report", "new"}, createPage,
			"myapp: \"report new\" is deprecated, use \"report:create\"\n"},
		{"a short help flag with an equals sign", []string{"report:create", "-h=false", "Q3"}, createPage, ""},
		{"a long help flag with an equals sign", []string{"report:create", "--help=1", "Q3"}, createPage, ""},
		{"a help flag after a double dash read as a value", []string{"report:create", "-owner", "--", "-h"},
			createPage, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			got := execute(t, catalog(), tc.args...)

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

func TestHelpRefusesANameNoCommandOwns(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name   string
		args   []string
		stderr string
	}{
		{"the help command", []string{"help", "reprot"},
			`myapp: unknown command "reprot", run "myapp list" to see every command` + "\n"},
		{"a help flag", []string{"reprot", "-h"},
			`myapp: unknown command "reprot", run "myapp list" to see every command` + "\n"},
		{"a namespace", []string{"help", "report"},
			`myapp: unknown command "report", want report:create, report:list or report:revoke` + "\n"},
		{"a flag before a help flag", []string{"-v", "-h"},
			`myapp: unknown command "-v", run "myapp list" to see every command` + "\n"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			got := execute(t, catalog(), tc.args...)

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

func TestHelpFlagAfterADoubleDashIsAnArgument(t *testing.T) {
	t.Parallel()

	got := execute(t, catalog(), "report:create", "--", "-h")

	if got.code != gonsole.ExitDone {
		t.Errorf("code = %d, want %d, stderr %q", got.code, gonsole.ExitDone, got.stderr)
	}
	if want := "title=-h owner= draft=false\n"; got.stdout != want {
		t.Errorf("stdout = %q, want %q", got.stdout, want)
	}
}

func TestHelpCountsOnlyAsTheFirstArgument(t *testing.T) {
	t.Parallel()

	got := execute(t, catalog(), "report:create", "help")

	if got.code != gonsole.ExitDone {
		t.Errorf("code = %d, want %d, stderr %q", got.code, gonsole.ExitDone, got.stderr)
	}
	if want := "title=help owner= draft=false\n"; got.stdout != want {
		t.Errorf("stdout = %q, want %q", got.stdout, want)
	}
}

func TestHelpPageListsTheEngineSwitchesTheCommandOffers(t *testing.T) {
	t.Parallel()

	create := gonsole.Command{
		Name:    "report:create",
		Summary: "create a report",
		Args:    []string{"title"},
		Writes:  true,
		JSON:    true,
		Run:     func(context.Context, gonsole.Call) error { return nil },
	}

	got := execute(t, single(create), "report:create", "-h")

	want := `create a report

Usage:
  myapp report:create [flags] <title>

Flags:
  -json
    	answer one JSON document
  -yes
    	apply the change, a dry run without it
`
	if got.stdout != want {
		t.Errorf("stdout = %q, want %q", got.stdout, want)
	}
}

func TestHelpPageNamesEveryArgument(t *testing.T) {
	t.Parallel()

	move := summarized(echo("report:move", "id", "folder"), "move a report into a folder")

	got := execute(t, single(move), "report:move", "-h")

	want := `move a report into a folder

Usage:
  myapp report:move <id> <folder>
`
	if got.stdout != want {
		t.Errorf("stdout = %q, want %q", got.stdout, want)
	}
}

// closedWriter is a writer whose every write fails.
type closedWriter struct{}

// Write returns an error for every write.
func (closedWriter) Write([]byte) (int, error) {
	return 0, errors.New("stdout is closed")
}

func TestRunFailsWhenTheAnswerCannotBeWritten(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		args []string
	}{
		{"the listing of no command", nil},
		{"the listing of the list command", []string{"list"}},
		{"the listing of a help flag", []string{"-h"}},
		{"a help page", []string{"report:create", "-h"}},
		{"a help page the flag package asks for", []string{"report:create", "-h=true"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			var stderr bytes.Buffer
			code := catalog().Run(t.Context(), tc.args, strings.NewReader(""), closedWriter{}, &stderr)

			if code != gonsole.ExitFailed {
				t.Errorf("code = %d, want %d", code, gonsole.ExitFailed)
			}
			if want := "myapp: stdout is closed\n"; stderr.String() != want {
				t.Errorf("stderr = %q, want %q", stderr.String(), want)
			}
		})
	}
}

func TestMisuseOfAKnownCommandEndsWithItsHelpPage(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name   string
		args   []string
		stderr string
	}{
		{"a missing argument", []string{"report:create"}, "myapp: report:create wants <title>\n\n" + createPage},
		{"an unknown flag", []string{"report:create", "-bogus", "Q3"},
			"myapp: report:create: flag provided but not defined: -bogus\n\n" + createPage},
		{"a stray argument to a base command", []string{"list", "extra"},
			"myapp: list takes no arguments, got 1\n\n" + listPage},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			got := execute(t, catalog(), tc.args...)

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

// pluggedHeading is the opening of every listing plugged prints, down to its bare commands.
const pluggedHeading = `myapp

Usage:
  myapp <command> [flags] [arguments]

` + intro + `
Available commands:
`

// pluggedListing is the listing plugged prints with the demo plugin loaded.
const pluggedListing = pluggedHeading + `  check           check every setting, every plugin and every command name
  help            print the help of one command
  list            list every command
  migrate         apply every schema step
  seed            store the demo data
  status          print the arguments
  version         print the version
 demo             plugin
  demo:list       list the demo
  demo:move       move the demo
  demo:sync       sync the demo
 report
  report:plugins  print the plugin namespaces
`

// pluggedFooter is the footer the Not loaded tests give plugged.
const pluggedFooter = "Read the guide at https://example.com/myapp."

func TestListingShowsTheNamespaceOfEveryPlugin(t *testing.T) {
	t.Parallel()

	for _, args := range [][]string{nil, {"list"}, {"help"}, {"-h"}} {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			t.Parallel()

			r := &registry{groups: demoGroups()}

			got := execute(t, plugged(r), args...)

			if got.code != gonsole.ExitDone || got.stdout != pluggedListing || got.stderr != "" {
				t.Errorf("listing = %d, %q, %q, want 0, %q, nothing", got.code, got.stdout, got.stderr, pluggedListing)
			}
			if !slices.Equal(r.log, describedPlugins) {
				t.Errorf("calls = %q, want %q", r.log, describedPlugins)
			}
		})
	}
}

func TestListingWidensTheNameColumnOnlyForALoadedPluginCommand(t *testing.T) {
	t.Parallel()

	r := &registry{groups: []gonsole.Group{{Namespace: "demo", Commands: []gonsole.Command{
		echo("demo:synchronize-everything"), summarized(echo("demo:a-dropped-command-with-the-longest-name"), ""),
	}}}}

	got := execute(t, plugged(r), "list")

	want := pluggedHeading + `  check                        check every setting, every plugin and every command name
  help                         print the help of one command
  list                         list every command
  migrate                      apply every schema step
  seed                         store the demo data
  status                       print the arguments
  version                      print the version
 demo                          plugin
  demo:synchronize-everything  print the arguments
 report
  report:plugins               print the plugin namespaces

Not loaded:
  gonsole: command "demo:a-dropped-command-with-the-longest-name" has no summary
`
	if got.code != gonsole.ExitDone || got.stdout != want {
		t.Errorf("listing = %d, %q, want 0, %q", got.code, got.stdout, want)
	}
}

func TestListingShowsWhatFailedToLoadBeforeTheFooter(t *testing.T) {
	t.Parallel()

	commands := `  check           check every setting, every plugin and every command name
  help            print the help of one command
  list            list every command
  migrate         apply every schema step
  seed            store the demo data
  status          print the arguments
  version         print the version
`
	cases := []struct {
		name   string
		groups []gonsole.Group
		failed error
		fail   error
		want   string
	}{
		{"a registration that fails", demoGroups(), errors.New("plugin mail: no relay host"),
			errors.New("the plugin table is locked\nby another run"), commands + ` report
  report:plugins  print the plugin namespaces

Not loaded:
  the plugin table is locked
  by another run
`},
		{"plugins that failed beside groups that break the rules", []gonsole.Group{
			{Namespace: "list", Commands: []gonsole.Command{echo("list:all")}},
			{Namespace: "demo", Commands: []gonsole.Command{echo("demo:sync")}},
			{Namespace: "tenancy", Commands: []gonsole.Command{echo("report:plugins")}},
		}, errors.Join(errors.New("plugin billing: no signing key"), errors.New("plugin mail: no relay host")), nil,
			commands + ` demo             plugin
  demo:sync       print the arguments
 report
  report:plugins  print the plugin namespaces

Not loaded:
  plugin billing: no signing key
  plugin mail: no relay host
  gonsole: plugin list takes the name of the base command list
  gonsole: command "report:plugins" is declared twice
  gonsole: command "report:plugins" of plugin tenancy is outside its namespace
`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			p := plugged(&registry{groups: tc.groups, failed: tc.failed, fail: tc.fail})
			p.Footer = pluggedFooter

			got := execute(t, p, "list")

			want := pluggedHeading + tc.want + "\n" + pluggedFooter + "\n"
			if got.code != gonsole.ExitDone || got.stdout != want || got.stderr != "" {
				t.Errorf("listing = %d, %q, %q, want 0, %q, nothing", got.code, got.stdout, got.stderr, want)
			}
		})
	}
}

func TestListingShowsTheSameOffencesAsCheck(t *testing.T) {
	t.Parallel()

	groups := []gonsole.Group{offending(), {Namespace: "tenancy", Commands: []gonsole.Command{echo("report:plugins")}}}
	p := plugged(&registry{groups: groups})

	got := execute(t, p, "list")

	_, block, found := strings.Cut(got.stdout, "\nNot loaded:\n")
	var want strings.Builder
	for _, offence := range offences(p.Check(gonsole.Loaded{Groups: groups})) {
		want.WriteString("  " + offence + "\n")
	}
	if !found || block != want.String() {
		t.Errorf("Not loaded = %q, want %q", block, want.String())
	}
}

func TestListingIndentsEveryLineOfAnOffence(t *testing.T) {
	t.Parallel()

	fragile := echo("demo:fragile")
	fragile.Flags = func(*flag.FlagSet) { panic("the flag table\nvanished") }
	p := plugged(&registry{groups: []gonsole.Group{{Namespace: "demo", Commands: []gonsole.Command{fragile}}}})

	got := execute(t, p, "list")

	_, block, found := strings.Cut(got.stdout, "\nNot loaded:\n")
	want := "  gonsole: command \"demo:fragile\" panicked declaring its flags: the flag table\n  vanished\n"
	if !found || block != want {
		t.Errorf("Not loaded = %q, want %q", block, want)
	}
}

func TestListingRegistersThePluginsUnderTheRunContext(t *testing.T) {
	t.Parallel()

	for _, args := range [][]string{nil, {"list"}, {"help"}} {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			t.Parallel()

			type key struct{}
			var seen any
			p := plugged(&registry{})
			p.Plugins = func(ctx context.Context, _ gonsole.Call) (gonsole.Loaded, error) {
				seen = ctx.Value(key{})
				return gonsole.Loaded{}, nil
			}

			code := p.Run(context.WithValue(t.Context(), key{}, "run"), args, strings.NewReader(""), &bytes.Buffer{},
				&bytes.Buffer{})

			if code != gonsole.ExitDone || seen != "run" {
				t.Errorf("code %d, registration saw %v, want 0 and the run context", code, seen)
			}
		})
	}
}

func TestListingFailsWhenTheRegistrationPanics(t *testing.T) {
	t.Parallel()

	for _, args := range [][]string{nil, {"list"}, {"help"}} {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			t.Parallel()

			p := plugged(&registry{})
			p.Plugins = func(context.Context, gonsole.Call) (gonsole.Loaded, error) {
				panic("the plugin table vanished")
			}

			got := execute(t, p, args...)

			const line = "myapp: plugins: panic: the plugin table vanished\n"
			stack, opened := strings.CutPrefix(got.stderr, line)
			if got.code != gonsole.ExitFailed || got.stdout != "" || !opened || !strings.HasPrefix(stack, "goroutine ") {
				t.Errorf("listing = %d, %q, %q, want %d, nothing, %q and the stack", got.code, got.stdout,
					firstLine(got.stderr), gonsole.ExitFailed, line)
			}
		})
	}
}
