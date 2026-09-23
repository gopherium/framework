// SPDX-License-Identifier: Apache-2.0

package gonsole

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"slices"
)

// ExitDone is the code of a finished command, a help page or a dry run.
const ExitDone = 0

// ExitFailed is the code of a command that ran and failed.
const ExitFailed = 1

// ExitMisused is the code of a command line the program cannot read.
const ExitMisused = 2

// ErrMisused marks an error the program answers with ExitMisused.
var ErrMisused = errors.New("gonsole: misused")

// Misuse returns err wrapped with ErrMisused.
func Misuse(err error) error {
	if err == nil {
		return nil
	}
	return misuse{err: err}
}

// misuse is an error the program answers with ExitMisused.
type misuse struct {
	err error
}

// Error returns the message of the wrapped error.
func (m misuse) Error() string {
	return m.err.Error()
}

// Unwrap returns ErrMisused and the wrapped error.
func (m misuse) Unwrap() []error {
	return []error{ErrMisused, m.err}
}

// Program is one executable's command line.
type Program struct {
	// Name is the executable name, the first word of every usage line and error.
	Name string
	// Title is the line the listing opens with.
	Title string
	// Version is the program's version.
	Version string
	// Footer is the text the listing closes with.
	Footer string
	// Env reads the program's settings under its prefix.
	Env Env
	// Renamed maps an old two word spelling to the full name of the command that replaced it.
	Renamed map[string]string
	// Commands are the program's own commands, each a bare word or namespace:word.
	Commands []Command
	// Authorize refuses the call's actor when that account lacks capability.
	Authorize func(ctx context.Context, call Call, capability string) error
	// Record stores one entry naming the actor and the command it applied.
	Record func(ctx context.Context, call Call, command string) error
}

// Main runs p over the process arguments and the standard streams and returns the exit code.
func Main(p Program) int {
	return p.Run(context.Background(), os.Args[1:], os.Stdin, os.Stdout, os.Stderr)
}

// Run runs the command args name and returns the exit code.
func (p Program) Run(ctx context.Context, args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	r := &runner{program: p, stdin: stdin, stdout: stdout, stderr: stderr}
	r.commands, r.namespaces = index(slices.Concat(p.Commands, r.base()))
	return r.exit(r.dispatch(ctx, args))
}

// runner is one run of a program over its streams.
type runner struct {
	program    Program
	commands   map[string]Command
	namespaces map[string][]string
	stdin      io.Reader
	stdout     io.Writer
	stderr     io.Writer
	reached    Command
	flags      *flag.FlagSet
}

// exit prints err and returns the exit code it earns.
func (r *runner) exit(err error) int {
	if err == nil || errors.Is(err, flag.ErrHelp) {
		return ExitDone
	}
	r.warn("%v", err)
	if !errors.Is(err, ErrMisused) {
		return ExitFailed
	}
	if r.flags != nil {
		_, _ = io.WriteString(r.stderr, "\n"+r.page(r.reached, r.flags))
	}
	return ExitMisused
}

// warn writes one line to stderr opened by the program name.
func (r *runner) warn(format string, args ...any) {
	_, _ = fmt.Fprintf(r.stderr, "%s: %s\n", r.program.Name, fmt.Sprintf(format, args...))
}
