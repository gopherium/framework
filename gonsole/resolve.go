// SPDX-License-Identifier: Apache-2.0

package gonsole

import (
	"context"
	"fmt"
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

// dispatch runs the command args name.
func (r *runner) dispatch(ctx context.Context, args []string) error {
	word, rest := head(r.rename(args))
	cmd, err := r.find(word)
	if err != nil {
		return err
	}
	return r.invoke(ctx, cmd, rest)
}

// head splits args into the first word and the rest.
func head(args []string) (string, []string) {
	if len(args) == 0 {
		return "", nil
	}
	return args[0], args[1:]
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
