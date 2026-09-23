// SPDX-License-Identifier: Apache-2.0

package gonsole

import (
	"context"
	"flag"
	"fmt"
	"io"
	"strings"
)

// invoke reads args against cmd's flags and arguments and runs it.
func (r *runner) invoke(ctx context.Context, cmd Command, args []string) error {
	fs := flag.NewFlagSet(cmd.Name, flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	if cmd.Flags != nil {
		cmd.Flags(fs)
	}
	positional, err := parse(fs, args)
	if err != nil {
		return Misuse(fmt.Errorf("%s: %w", cmd.Name, err))
	}
	if err := arity(cmd, positional); err != nil {
		return err
	}
	return cmd.Run(ctx, Call{Args: positional, Stdin: r.stdin, Stdout: r.stdout, Stderr: r.stderr})
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
