// SPDX-License-Identifier: Apache-2.0

package gonsole

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"maps"
	"regexp"
	"slices"
	"strings"
)

// baseCommands are the names the engine owns as commands and as namespaces in every program.
var baseCommands = []string{"help", "list", "version", "serve", "check", "migrate", "seed"}

// engineFlags are the names of the flags the engine owns.
var engineFlags = []string{"h", "help", "yes", "json", "as"}

// namePart matches each part of a command name.
var namePart = regexp.MustCompile(`^[a-z][a-z0-9-]*$`)

// malformed is the offence of a command name that breaks the grammar.
const malformed = "command name %q is malformed, want lowercase words joined by hyphens and at most one colon"

// flagsPanicked is the offence of a command whose Flags panics.
const flagsPanicked = "command %q panicked declaring its flags: %v"

// Check returns every offence against the naming rules in the program and the loaded plugins.
func (p Program) Check(loaded Loaded) error {
	a := newAudit(p)
	a.core()
	for _, group := range loaded.Groups {
		a.admit(group)
	}
	return errors.Join(a.offences...)
}

// audit collects the offences one Check finds.
type audit struct {
	program    Program
	offences   []error
	dropped    map[string][]error
	declared   map[string]bool
	names      map[string]bool
	namespaces map[string]bool
	reserved   map[string]bool
}

// newAudit returns an audit of p knowing the names and namespaces its commands use.
func newAudit(p Program) *audit {
	a := &audit{
		program: p, dropped: map[string][]error{}, declared: map[string]bool{}, names: map[string]bool{},
		namespaces: map[string]bool{}, reserved: map[string]bool{},
	}
	for _, cmd := range p.Commands {
		if namespace, _, namespaced := strings.Cut(cmd.Name, ":"); namespaced {
			a.namespaces[namespace] = true
		} else {
			a.names[cmd.Name] = true
		}
	}
	for _, namespace := range p.Reserved {
		a.reserved[namespace] = true
	}
	return a
}

// refuse records one offence.
func (a *audit) refuse(format string, args ...any) {
	a.offences = append(a.offences, fmt.Errorf("gonsole: "+format, args...))
}

// core refuses the offences of the program's own commands and settings.
func (a *audit) core() {
	for _, cmd := range a.program.Commands {
		a.owned(cmd)
		a.inspect(cmd)
		a.once(cmd.Name)
		a.guarded(cmd)
		a.shadows(cmd)
	}
	a.renamed()
	if a.program.BareServes && a.program.Serve == nil {
		a.refuse("BareServes is set without Serve")
	}
}

// admit returns the commands of one plugin's group that break no rule.
func (a *audit) admit(g Group) []Command {
	claimed := a.claims(g.Namespace)
	var kept []Command
	for _, cmd := range g.Commands {
		if a.command(g, cmd) && !claimed {
			kept = append(kept, cmd)
		}
	}
	return kept
}

// command refuses the offences of one command of g and reports whether it has none.
func (a *audit) command(g Group, cmd Command) bool {
	before := len(a.offences)
	a.inspect(cmd)
	a.once(cmd.Name)
	a.inside(g, cmd)
	a.guarded(cmd)
	if cmd.Migrates {
		a.refuse("plugin command %q asks for the core schema steps", cmd.Name)
	}
	if len(a.offences) == before {
		return true
	}
	a.dropped[cmd.Name] = append(a.dropped[cmd.Name], a.offences[before:]...)
	return false
}

// owned refuses a program command that takes a base command or an engine namespace.
func (a *audit) owned(cmd Command) {
	namespace, _, namespaced := strings.Cut(cmd.Name, ":")
	switch {
	case !namespaced && slices.Contains(baseCommands, cmd.Name):
		a.refuse("command %q is a base command", cmd.Name)
	case namespaced && slices.Contains(baseCommands, namespace):
		a.refuse("command %q is in the engine namespace %s", cmd.Name, namespace)
	}
}

// inspect refuses a command with a malformed name, a missing or split summary, no run, or flags it cannot declare.
func (a *audit) inspect(cmd Command) {
	if !wellFormed(cmd.Name) {
		a.refuse(malformed, cmd.Name)
	}
	switch {
	case strings.TrimSpace(cmd.Summary) == "":
		a.refuse("command %q has no summary", cmd.Name)
	case strings.Contains(cmd.Summary, "\n"):
		a.refuse("command %q has a summary of more than one line", cmd.Name)
	}
	if cmd.Run == nil {
		a.refuse("command %q has no run", cmd.Name)
	}
	a.flags(cmd)
}

// wellFormed reports whether name is one name part, or two joined by one colon.
func wellFormed(name string) bool {
	namespace, command, namespaced := strings.Cut(name, ":")
	if !namespaced {
		return namePart.MatchString(name)
	}
	return namePart.MatchString(namespace) && namePart.MatchString(command)
}

// flags refuses a command whose Flags panics or declares a flag the engine owns.
func (a *audit) flags(cmd Command) {
	if cmd.Flags == nil {
		return
	}
	fs := flag.NewFlagSet(cmd.Name, flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	if value, panicked := declare(cmd, fs); panicked {
		a.refuse(flagsPanicked, cmd.Name, value)
		return
	}
	for _, name := range engineFlags {
		if fs.Lookup(name) != nil {
			a.refuse("command %q declares the engine flag -%s", cmd.Name, name)
		}
	}
}

// declare runs cmd's Flags on fs and returns the value of a panic inside it.
func declare(cmd Command, fs *flag.FlagSet) (value any, panicked bool) {
	defer func() {
		if value = recover(); value != nil {
			panicked = true
		}
	}()
	cmd.Flags(fs)
	return nil, false
}

// once refuses a command name declared before.
func (a *audit) once(name string) {
	if a.declared[name] {
		a.refuse("command %q is declared twice", name)
		return
	}
	a.declared[name] = true
}

// guarded refuses a command that names a capability the program cannot check or record.
func (a *audit) guarded(cmd Command) {
	if cmd.Capability == "" {
		return
	}
	if a.program.Authorize == nil {
		a.refuse("command %q names capability %s without Authorize", cmd.Name, cmd.Capability)
	}
	if a.program.Record == nil {
		a.refuse("command %q names capability %s without Record", cmd.Name, cmd.Capability)
	}
}

// shadows refuses a program command without a namespace whose name is also a core or reserved namespace.
func (a *audit) shadows(cmd Command) {
	if a.namespaces[cmd.Name] || a.reserved[cmd.Name] {
		a.refuse("command %q is also a namespace", cmd.Name)
	}
}

// renamed refuses an old spelling that starts with a base command or points at no core command.
func (a *audit) renamed() {
	for _, old := range slices.Sorted(maps.Keys(a.program.Renamed)) {
		first, _, _ := strings.Cut(old, " ")
		target := a.program.Renamed[old]
		if slices.Contains(baseCommands, first) {
			a.refuse("old spelling %q starts with the base command %s", old, first)
		}
		if !a.declared[target] {
			a.refuse("old spelling %q points at %q, which is no core command", old, target)
		}
	}
}

// claims refuses a plugin whose namespace the engine or core holds and reports whether it did.
func (a *audit) claims(namespace string) bool {
	held := a.holder(namespace)
	if held != "" {
		a.refuse("%s", held)
	}
	return held != ""
}

// holder returns the offence of a plugin that takes a namespace the engine or core holds, empty for a free one.
func (a *audit) holder(namespace string) string {
	switch {
	case slices.Contains(baseCommands, namespace):
		return fmt.Sprintf("plugin %s takes the name of the base command %s", namespace, namespace)
	case a.names[namespace]:
		return fmt.Sprintf("plugin %s takes the name of the core command %s", namespace, namespace)
	case a.namespaces[namespace]:
		return fmt.Sprintf("plugin %s takes the core namespace %s", namespace, namespace)
	case a.reserved[namespace]:
		return fmt.Sprintf("plugin %s takes the reserved namespace %s", namespace, namespace)
	}
	return ""
}

// inside refuses a plugin command outside the namespace of its group.
func (a *audit) inside(g Group, cmd Command) {
	namespace, _, namespaced := strings.Cut(cmd.Name, ":")
	if !namespaced || namespace != g.Namespace {
		a.refuse("command %q of plugin %s is outside its namespace", cmd.Name, g.Namespace)
	}
}
