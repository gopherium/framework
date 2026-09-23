// SPDX-License-Identifier: Apache-2.0

package gonsole_test

import (
	"bytes"
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"strings"
	"testing"

	"github.com/gopherium/framework/gonsole"
)

// result is what one run of a program answers.
type result struct {
	code   int
	stdout string
	stderr string
}

// execute runs p over args in process and answers its exit code and output.
func execute(t *testing.T, p gonsole.Program, args ...string) result {
	t.Helper()
	var stdout, stderr bytes.Buffer
	code := p.Run(t.Context(), args, strings.NewReader(""), &stdout, &stderr)
	return result{code: code, stdout: stdout.String(), stderr: stderr.String()}
}

// single returns a program called myapp whose only command is cmd.
func single(cmd gonsole.Command) gonsole.Program {
	return gonsole.Program{Name: "myapp", Commands: []gonsole.Command{cmd}}
}

// answering returns a command called report:list whose run answers err.
func answering(err error) gonsole.Command {
	return gonsole.Command{
		Name:    "report:list",
		Summary: "list every report",
		Run:     func(context.Context, gonsole.Call) error { return err },
	}
}

func TestExitCodesKeepTheirValues(t *testing.T) {
	t.Parallel()

	if gonsole.ExitDone != 0 || gonsole.ExitFailed != 1 || gonsole.ExitMisused != 2 {
		t.Errorf("exit codes = %d, %d, %d, want 0, 1, 2", gonsole.ExitDone, gonsole.ExitFailed, gonsole.ExitMisused)
	}
}

func TestMisuseKeepsTheMessageAndMarksTheError(t *testing.T) {
	t.Parallel()

	cause := errors.New(`unknown format "pdf"`)
	err := gonsole.Misuse(cause)

	if err.Error() != cause.Error() {
		t.Errorf("Error() = %q, want %q", err.Error(), cause.Error())
	}
	if !errors.Is(err, gonsole.ErrMisused) {
		t.Errorf("errors.Is(err, ErrMisused) = false, want true")
	}
	if !errors.Is(err, cause) {
		t.Errorf("errors.Is(err, cause) = false, want true")
	}
}

func TestMisuseOfNilIsNil(t *testing.T) {
	t.Parallel()

	if err := gonsole.Misuse(nil); err != nil {
		t.Errorf("Misuse(nil) = %v, want nil", err)
	}
}

func TestRunAnswersTheExitCodeTheCommandEarns(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name   string
		err    error
		code   int
		stderr string
	}{
		{"a command that succeeds", nil, gonsole.ExitDone, ""},
		{"a command that fails", errors.New("report store is down"), gonsole.ExitFailed, "myapp: report store is down\n"},
		{
			"a command that reports misuse",
			gonsole.Misuse(errors.New(`unknown format "pdf"`)),
			gonsole.ExitMisused,
			`myapp: unknown format "pdf"

list every report

Usage:
  myapp report:list
`,
		},
		{"a command that wraps the help error", fmt.Errorf("report:list: %w", flag.ErrHelp), gonsole.ExitDone, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			got := execute(t, single(answering(tc.err)), "report:list")

			if got.code != tc.code {
				t.Errorf("code = %d, want %d", got.code, tc.code)
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

func TestRunHandsTheCommandTheContextAndTheStreams(t *testing.T) {
	t.Parallel()

	type key struct{}
	ctx := context.WithValue(t.Context(), key{}, "carried")
	p := single(gonsole.Command{
		Name:    "report:list",
		Summary: "list every report",
		Run: func(ctx context.Context, call gonsole.Call) error {
			if ctx.Value(key{}) != "carried" {
				return errors.New("the context lost its value")
			}
			read, err := io.ReadAll(call.Stdin)
			if err != nil {
				return err
			}
			if _, err := fmt.Fprintf(call.Stdout, "read %s\n", read); err != nil {
				return err
			}
			_, err = fmt.Fprintln(call.Stderr, "listing")
			return err
		},
	})
	var stdout, stderr bytes.Buffer

	code := p.Run(ctx, []string{"report:list"}, strings.NewReader("quarterly"), &stdout, &stderr)

	if code != gonsole.ExitDone {
		t.Errorf("code = %d, want %d, stderr %q", code, gonsole.ExitDone, stderr.String())
	}
	if stdout.String() != "read quarterly\n" {
		t.Errorf("stdout = %q, want the input echoed", stdout.String())
	}
	if stderr.String() != "listing\n" {
		t.Errorf("stderr = %q, want the progress line", stderr.String())
	}
}
