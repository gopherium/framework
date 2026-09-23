// SPDX-License-Identifier: Apache-2.0

package gonsole_test

import (
	"bytes"
	"errors"
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

// catalogListing is the listing catalog prints.
const catalogListing = `Myapp, a report keeper. Version 1.4.0

Usage:
  myapp <command> [flags] [arguments]

Every command answers -h.

Available commands:
  help           print the help of one command
  list           list every command
  status         print the store status
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

// listPage is the help page of the list base word.
const listPage = `list every command

Usage:
  myapp list
`

// helpPage is the help page of the help base word.
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
		{"no word", nil},
		{"the list word", []string{"list"}},
		{"the help word", []string{"help"}},
		{"a short help flag", []string{"-h"}},
		{"a long help flag", []string{"--help"}},
		{"the help word with a help flag", []string{"help", "-h"}},
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

Every command answers -h.

Available commands:
  help    print the help of one command
  list    list every command
  status  print the store status
`,
		},
		{
			"a version without a title",
			gonsole.Program{Name: "myapp", Version: "1.4.0", Commands: []gonsole.Command{status}},
			`myapp Version 1.4.0

Usage:
  myapp <command> [flags] [arguments]

Every command answers -h.

Available commands:
  help    print the help of one command
  list    list every command
  status  print the store status
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
		{"the help word", []string{"help", "report:create"}, createPage, ""},
		{"a short help flag", []string{"report:create", "-h"}, createPage, ""},
		{"a long help flag with one dash", []string{"report:create", "-help"}, createPage, ""},
		{"a short help flag with two dashes", []string{"report:create", "--h"}, createPage, ""},
		{"a long help flag", []string{"report:create", "--help"}, createPage, ""},
		{"a help flag before the name", []string{"-h", "report:create"}, createPage, ""},
		{"a help flag after arguments and flags", []string{"report:create", "Q3", "-owner", "x", "-h"}, createPage, ""},
		{"a command without flags", []string{"report:revoke", "-h"}, revokePage, ""},
		{"the list word", []string{"list", "-h"}, listPage, ""},
		{"the help word itself", []string{"help", "help"}, helpPage, ""},
		{"an old spelling", []string{"report", "new", "-h"}, createPage,
			"myapp: \"report new\" is deprecated, use \"report:create\"\n"},
		{"the help word before an old spelling", []string{"help", "report", "new"}, createPage,
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
		{"the help word", []string{"help", "reprot"},
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

func TestHelpWordCountsOnlyAsTheFirstWord(t *testing.T) {
	t.Parallel()

	got := execute(t, catalog(), "report:create", "help")

	if got.code != gonsole.ExitDone {
		t.Errorf("code = %d, want %d, stderr %q", got.code, gonsole.ExitDone, got.stderr)
	}
	if want := "title=help owner= draft=false\n"; got.stdout != want {
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
		{"the listing of no word", nil},
		{"the listing of the list word", []string{"list"}},
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
		{"a stray argument to a base word", []string{"list", "extra"},
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
