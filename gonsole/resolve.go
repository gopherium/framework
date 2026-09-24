// SPDX-License-Identifier: Apache-2.0

package gonsole

import (
	"context"
	"errors"
	"fmt"
	"io"
	"maps"
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
		return r.help(ctx, args)
	}
	if len(args) == 0 {
		return r.commandless(ctx)
	}
	args = r.rename(args)
	cmd, err := r.find(ctx, args[0], false)
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
	return r.list(ctx, r.stdout)
}

// help prints the help page of the command args name, or the listing when they name none.
func (r *runner) help(ctx context.Context, args []string) error {
	named := r.rename(subject(args))
	if len(named) == 0 {
		return r.list(ctx, r.stdout)
	}
	cmd, err := r.find(ctx, named[0], true)
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

// find returns the command called name, registering the plugins when only a plugin may own it.
func (r *runner) find(ctx context.Context, name string, describe bool) (Command, error) {
	if cmd, known := r.commands[name]; known {
		return cmd, nil
	}
	namespace, _, namespaced := strings.Cut(name, ":")
	if members := r.namespaces[namespace]; len(members) > 0 {
		return Command{}, want(name, members)
	}
	if r.barred(name, namespace) {
		return Command{}, r.unknown(name)
	}
	return r.plugin(ctx, name, describe || !namespaced)
}

// barred reports whether no plugin may own name, a malformed one or one in a namespace the engine or core holds.
func (r *runner) barred(name, namespace string) bool {
	return !wellFormed(name) || r.plugins.audit.holder(namespace) != ""
}

// plugin returns the plugin command called name, registering the plugins in describe mode when describe is set.
func (r *runner) plugin(ctx context.Context, name string, describe bool) (Command, error) {
	loaded, err := r.plugins.answer(ctx, describe)
	if cmd, kept := r.plugins.commands[name]; kept {
		if !describe {
			r.caution(loaded.Failed)
		}
		return cmd, nil
	}
	if offences, dropped := r.plugins.audit.dropped[name]; dropped {
		return Command{}, errors.Join(offences...)
	}
	return Command{}, r.stray(name, loaded, err)
}

// stray returns the error of a name no admitted plugin command owns, given what registering answered.
func (r *runner) stray(name string, loaded Loaded, err error) error {
	namespace, _, namespaced := strings.Cut(name, ":")
	switch {
	case errors.As(err, new(panicked)):
		return err
	case len(r.plugins.namespaces[namespace]) > 0:
		return want(name, r.plugins.namespaces[namespace])
	case !namespaced:
		return r.unknown(name)
	case err != nil:
		return err
	case loaded.Failed != nil:
		return loaded.Failed
	case len(r.plugins.namespaces) > 0:
		owners := alternatives(slices.Sorted(maps.Keys(r.plugins.namespaces)))
		return Misuse(fmt.Errorf("unknown command %q, want a command in %s", name, owners))
	}
	return r.unknown(name)
}

// caution warns of each line of failed.
func (r *runner) caution(failed error) {
	for _, line := range lines(failed) {
		r.warn("warning: %s", line)
	}
}

// lines returns the lines of err's message, none when err is nil.
func lines(err error) []string {
	if err == nil {
		return nil
	}
	return strings.Split(err.Error(), "\n")
}

// want returns the misuse of a name no command of a namespace owns, naming the members of that namespace.
func want(name string, members []string) error {
	return Misuse(fmt.Errorf("unknown command %q, want %s", name, alternatives(members)))
}

// unknown returns the misuse of a name no command owns.
func (r *runner) unknown(name string) error {
	return Misuse(fmt.Errorf("unknown command %q, run %q to see every command", name, r.program.Name+" list"))
}

// alternatives joins names as a list read aloud, such as a, b or c.
func alternatives(names []string) string {
	if len(names) == 1 {
		return names[0]
	}
	last := len(names) - 1
	return strings.Join(names[:last], ", ") + " or " + names[last]
}
