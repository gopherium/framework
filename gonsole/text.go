// SPDX-License-Identifier: Apache-2.0

package gonsole

import (
	"cmp"
	"flag"
	"fmt"
	"maps"
	"slices"
	"strings"
)

// listing returns the list of every command the run knows.
func (r *runner) listing() string {
	var b strings.Builder
	fmt.Fprintf(&b, "%s\n\nUsage:\n  %s <command> [flags] [arguments]\n\n", r.heading(), r.program.Name)
	b.WriteString("Every command answers -h. A command that offers -json answers one JSON document. ")
	b.WriteString("A command that offers -yes is a dry run until -yes.\n\nAvailable commands:\n")
	width := r.width()
	for _, name := range r.bare() {
		fmt.Fprintf(&b, "  %-*s%s\n", width, name, r.commands[name].Summary)
	}
	for _, namespace := range slices.Sorted(maps.Keys(r.namespaces)) {
		fmt.Fprintf(&b, " %s\n", namespace)
		for _, name := range r.namespaces[namespace] {
			fmt.Fprintf(&b, "  %-*s%s\n", width, name, r.commands[name].Summary)
		}
	}
	if r.program.Footer != "" {
		fmt.Fprintf(&b, "\n%s\n", r.program.Footer)
	}
	return b.String()
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
func (r *runner) bare() []string {
	var names []string
	for name := range r.commands {
		if !strings.Contains(name, ":") {
			names = append(names, name)
		}
	}
	slices.Sort(names)
	return names
}

// width returns the width of the listing's name column, the longest name and two spaces.
func (r *runner) width() int {
	longest := 0
	for name := range r.commands {
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
