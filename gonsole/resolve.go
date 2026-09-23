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

// dispatch runs the command args name, the help they ask for, or the listing when they name none.
func (r *runner) dispatch(ctx context.Context, args []string) error {
	if asksHelp(args) {
		return r.help(args)
	}
	if len(args) == 0 {
		_, err := io.WriteString(r.stdout, r.listing())
		return err
	}
	args = r.rename(args)
	cmd, err := r.find(args[0])
	if err != nil {
		return err
	}
	return r.invoke(ctx, cmd, args[1:])
}

// help prints the help page of the command args name, or the listing when they name none.
func (r *runner) help(args []string) error {
	words := r.rename(subject(args))
	if len(words) == 0 {
		_, err := io.WriteString(r.stdout, r.listing())
		return err
	}
	cmd, err := r.find(words[0])
	if err != nil {
		return err
	}
	_, err = io.WriteString(r.stdout, r.page(cmd, flagSet(cmd)))
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

// find returns the command called word.
func (r *runner) find(word string) (Command, error) {
	if cmd, known := r.commands[word]; known {
		return cmd, nil
	}
	namespace, _, _ := strings.Cut(word, ":")
	if names := r.namespaces[namespace]; len(names) > 0 {
		return Command{}, Misuse(fmt.Errorf("unknown command %q, want %s", word, alternatives(names)))
	}
	return Command{}, Misuse(fmt.Errorf("unknown command %q, run %q to see every command", word, r.program.Name+" list"))
}

// alternatives joins names as a list read aloud, such as a, b or c.
func alternatives(names []string) string {
	if len(names) == 1 {
		return names[0]
	}
	last := len(names) - 1
	return strings.Join(names[:last], ", ") + " or " + names[last]
}
