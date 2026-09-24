// SPDX-License-Identifier: Apache-2.0

package gonsole

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"strings"
)

// asksHelp reports whether args ask for help, with the help command first or a help flag before any double dash.
func asksHelp(args []string) bool {
	if len(args) > 0 && args[0] == "help" {
		return true
	}
	for _, arg := range args {
		if arg == "--" {
			return false
		}
		if isHelpFlag(arg) {
			return true
		}
	}
	return false
}

// subject returns the arguments of a help run before any double dash, without the help command and the help flags.
func subject(args []string) []string {
	if args[0] == "help" {
		args = args[1:]
	}
	var kept []string
	for _, arg := range args {
		if arg == "--" {
			break
		}
		if !isHelpFlag(arg) {
			kept = append(kept, arg)
		}
	}
	return kept
}

// isHelpFlag reports whether arg is one of the flags that ask for help.
func isHelpFlag(arg string) bool {
	switch arg {
	case "-h", "-help", "--h", "--help":
		return true
	}
	return false
}

// switches holds the engine flags one run of a command reads.
type switches struct {
	yes  bool
	json bool
	as   string
}

// flagSet returns a fresh flag set holding cmd's flags and the engine flags it offers, which set s.
func flagSet(cmd Command, s *switches) (*flag.FlagSet, error) {
	fs := flag.NewFlagSet(cmd.Name, flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	if cmd.Flags != nil {
		if value, panicked := declare(cmd, fs); panicked {
			return nil, fmt.Errorf("gonsole: "+flagsPanicked, cmd.Name, value)
		}
	}
	if cmd.Writes {
		fs.BoolVar(&s.yes, "yes", false, "apply the change, a dry run without it")
	}
	if cmd.JSON {
		fs.BoolVar(&s.json, "json", false, "answer one JSON document")
	}
	if cmd.Capability != "" {
		fs.StringVar(&s.as, "as", "", "`email` address of the account acting")
	}
	return fs, nil
}

// invoke reads args against cmd's flags and arguments and runs it, or prints its help page when they ask for it.
func (r *runner) invoke(ctx context.Context, cmd Command, args []string) (err error) {
	defer recoverRun(cmd.Name, &err)
	call, err := r.prepare(cmd, args)
	if errors.Is(err, flag.ErrHelp) {
		_, err = io.WriteString(r.stdout, r.page(r.reached, r.flags))
		return err
	}
	if err != nil {
		return err
	}
	return r.perform(ctx, cmd, call)
}

// prepare reads args against cmd's flags and arguments and returns the call that runs it.
func (r *runner) prepare(cmd Command, args []string) (Call, error) {
	var s switches
	fs, err := flagSet(cmd, &s)
	if err != nil {
		return Call{}, err
	}
	r.reached, r.flags = cmd, fs
	positional, err := parse(fs, args)
	if err != nil {
		return Call{}, Misuse(fmt.Errorf("%s: %w", cmd.Name, err))
	}
	if err := arity(cmd, positional); err != nil {
		return Call{}, err
	}
	if cmd.Capability != "" && s.as == "" {
		return Call{}, Misuse(fmt.Errorf("%s wants -as <email>", cmd.Name))
	}
	return Call{
		Args: positional, Stdin: r.stdin, Stdout: r.stdout, Stderr: r.stderr, Env: r.settings(),
		JSON: s.json, Apply: s.yes || !cmd.Writes, Actor: s.as, database: r.program.Database, plugins: r.plugins,
	}, nil
}

// parse sets the flags in args on fs and returns the positional arguments, flags and arguments in any order.
func parse(fs *flag.FlagSet, args []string) ([]string, error) {
	var positional []string
	for len(args) > 0 {
		if err := fs.Parse(args); err != nil {
			return nil, err
		}
		rest := fs.Args()
		if terminated(fs, args, rest) {
			return append(positional, rest...), nil
		}
		if len(rest) == 0 {
			break
		}
		positional = append(positional, rest[0])
		args = rest[1:]
	}
	return positional, nil
}

// terminated reports whether the arguments fs read from args hold the double dash that ends the flags.
func terminated(fs *flag.FlagSet, args, rest []string) bool {
	read := len(args) - len(rest)
	for i := 0; i < read; i++ {
		if args[i] == "--" {
			return true
		}
		if takesValue(fs, args[i]) {
			i++
		}
	}
	return false
}

// takesValue reports whether token, a flag fs read, takes the next argument as its value.
func takesValue(fs *flag.FlagSet, token string) bool {
	name := strings.TrimPrefix(strings.TrimPrefix(token, "-"), "-")
	if strings.Contains(name, "=") {
		return false
	}
	boolean, isBoolean := fs.Lookup(name).Value.(interface{ IsBoolFlag() bool })
	return !isBoolean || !boolean.IsBoolFlag()
}

// arity refuses positional arguments that do not match the names cmd declares.
func arity(cmd Command, positional []string) error {
	switch want := len(cmd.Args); {
	case len(positional) < want:
		return Misuse(fmt.Errorf("%s wants <%s>", cmd.Name, cmd.Args[len(positional)]))
	case len(positional) > want:
		return Misuse(fmt.Errorf("%s takes %s, got %d", cmd.Name, arguments(want), len(positional)))
	}
	return nil
}

// arguments names a count of arguments in plain English.
func arguments(n int) string {
	switch n {
	case 0:
		return "no arguments"
	case 1:
		return "1 argument"
	}
	return fmt.Sprintf("%d arguments", n)
}
