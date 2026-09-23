// SPDX-License-Identifier: Apache-2.0

package gonsole

import (
	"context"
	"fmt"
	"io"
	"slices"
	"strings"
)

// index returns the commands by full name and the sorted command names of each namespace.
func index(commands []Command) (map[string]Command, map[string][]string) {
	byName := make(map[string]Command, len(commands))
	namespaces := map[string][]string{}
	for _, cmd := range commands {
		byName[cmd.Name] = cmd
		if namespace, _, namespaced := strings.Cut(cmd.Name, ":"); namespaced {
			namespaces[namespace] = append(namespaces[namespace], cmd.Name)
		}
	}
	for _, names := range namespaces {
		slices.Sort(names)
	}
	return byName, namespaces
}

// dispatch runs the command args name, the help they ask for, or the commandless run when they name none.
func (r *runner) dispatch(ctx context.Context, args []string) error {
	if asksHelp(args) {
		return r.help(args)
	}
	if len(args) == 0 {
		return r.commandless(ctx)
	}
	args = r.rename(args)
	cmd, err := r.find(args[0])
	if err != nil {
		return err
	}
	return r.invoke(ctx, cmd, args[1:])
}

// commandless serves when the program serves on a run that names no command, and prints the listing otherwise.
func (r *runner) commandless(ctx context.Context) error {
	if r.program.BareServes {
		return r.dispatch(ctx, []string{"serve"})
	}
	_, err := io.WriteString(r.stdout, r.listing())
	return err
}

// help prints the help page of the command args name, or the listing when they name none.
func (r *runner) help(args []string) error {
	named := r.rename(subject(args))
	if len(named) == 0 {
		_, err := io.WriteString(r.stdout, r.listing())
		return err
	}
	cmd, err := r.find(named[0])
	if err != nil {
		return err
	}
	fs, err := flagSet(cmd, &switches{})
	if err != nil {
		return err
	}
	_, err = io.WriteString(r.stdout, r.page(cmd, fs))
	return err
}

// rename returns args with an old two word spelling replaced by the name of the command that replaced it.
func (r *runner) rename(args []string) []string {
	if len(args) < 2 {
		return args
	}
	old := args[0] + " " + args[1]
	name, renamed := r.program.Renamed[old]
	if !renamed {
		return args
	}
	r.warn("%q is deprecated, use %q", old, name)
	return append([]string{name}, args[2:]...)
}

// find returns the command called name.
func (r *runner) find(name string) (Command, error) {
	if cmd, known := r.commands[name]; known {
		return cmd, nil
	}
	namespace, _, _ := strings.Cut(name, ":")
	if members := r.namespaces[namespace]; len(members) > 0 {
		return Command{}, Misuse(fmt.Errorf("unknown command %q, want %s", name, alternatives(members)))
	}
	return Command{}, Misuse(fmt.Errorf("unknown command %q, run %q to see every command", name, r.program.Name+" list"))
}

// alternatives joins names as a list read aloud, such as a, b or c.
func alternatives(names []string) string {
	if len(names) == 1 {
		return names[0]
	}
	last := len(names) - 1
	return strings.Join(names[:last], ", ") + " or " + names[last]
}
