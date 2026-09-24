// SPDX-License-Identifier: Apache-2.0

package gonsole

import (
	"cmp"
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"maps"
	"slices"
	"strings"
)

// list writes the listing to w.
func (r *runner) list(ctx context.Context, w io.Writer) error {
	s, err := r.shelf(ctx)
	if err != nil {
		return err
	}
	_, err = io.WriteString(w, r.listing(s))
	return err
}

// listing returns the list of every command s holds.
func (r *runner) listing(s shelf) string {
	var b strings.Builder
	fmt.Fprintf(&b, "%s\n\nUsage:\n  %s <command> [flags] [arguments]\n\n", r.heading(), r.program.Name)
	b.WriteString("Every command answers -h. A command that offers -json answers one JSON document. ")
	b.WriteString("A command that offers -yes is a dry run until -yes.\n\nAvailable commands:\n")
	width := s.width()
	for _, name := range s.bare() {
		fmt.Fprintf(&b, "  %-*s%s\n", width, name, s.commands[name].Summary)
	}
	for _, namespace := range slices.Sorted(maps.Keys(s.namespaces)) {
		s.section(&b, namespace, width)
	}
	if len(s.missing) > 0 {
		b.WriteString("\nNot loaded:\n")
		for _, line := range s.missing {
			fmt.Fprintf(&b, "  %s\n", line)
		}
	}
	if r.program.Footer != "" {
		fmt.Fprintf(&b, "\n%s\n", r.program.Footer)
	}
	return b.String()
}

// shelf is what the listing prints: the commands, each namespace's members, the plugin namespaces and the failures.
type shelf struct {
	commands   map[string]Command
	namespaces map[string][]string
	plugins    map[string]bool
	missing    []string
}

// shelf returns the core commands and the plugin commands registered to describe them, with what failed to load.
func (r *runner) shelf(ctx context.Context) (shelf, error) {
	if _, err := r.plugins.answer(ctx, true); errors.As(err, new(panicked)) {
		return shelf{}, err
	}
	s := shelf{
		commands: maps.Clone(r.commands), namespaces: maps.Clone(r.namespaces), plugins: map[string]bool{},
		missing: r.plugins.missing(),
	}
	maps.Copy(s.commands, r.plugins.commands)
	for namespace, members := range r.plugins.namespaces {
		s.namespaces[namespace] = members
		s.plugins[namespace] = true
	}
	return s, nil
}

// section writes the line of one namespace and the lines of its commands, the name column width wide.
func (s shelf) section(b *strings.Builder, namespace string, width int) {
	if s.plugins[namespace] {
		fmt.Fprintf(b, " %-*splugin\n", width+1, namespace)
	} else {
		fmt.Fprintf(b, " %s\n", namespace)
	}
	for _, name := range s.namespaces[namespace] {
		fmt.Fprintf(b, "  %-*s%s\n", width, name, s.commands[name].Summary)
	}
}

// heading returns the line the listing opens with.
func (r *runner) heading() string {
	title := cmp.Or(r.program.Title, r.program.Name)
	if r.program.Version == "" {
		return title
	}
	return title + " Version " + r.program.Version
}

// bare returns the sorted names of the commands outside every namespace.
func (s shelf) bare() []string {
	var names []string
	for name := range s.commands {
		if !strings.Contains(name, ":") {
			names = append(names, name)
		}
	}
	slices.Sort(names)
	return names
}

// width returns the width of the listing's name column, the longest name and two spaces.
func (s shelf) width() int {
	longest := 0
	for name := range s.commands {
		longest = max(longest, len(name))
	}
	return longest + 2
}

// page returns the help page of cmd, whose flags fs holds.
func (r *runner) page(cmd Command, fs *flag.FlagSet) string {
	flagged := false
	fs.VisitAll(func(*flag.Flag) { flagged = true })
	var b strings.Builder
	fmt.Fprintf(&b, "%s\n\nUsage:\n  %s %s", cmd.Summary, r.program.Name, cmd.Name)
	if flagged {
		b.WriteString(" [flags]")
	}
	for _, arg := range cmd.Args {
		fmt.Fprintf(&b, " <%s>", arg)
	}
	b.WriteString("\n")
	if flagged {
		b.WriteString("\nFlags:\n")
		fs.SetOutput(&b)
		fs.PrintDefaults()
	}
	return b.String()
}
