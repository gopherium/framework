// SPDX-License-Identifier: Apache-2.0

package gonsole

import (
	"context"
	"encoding/json"
	"flag"
	"io"
)

// Command is one command a program or a plugin offers.
type Command struct {
	// Name is the full name, a bare word or namespace:word in lowercase words joined by hyphens.
	Name string
	// Summary is the one line the listing prints beside the name.
	Summary string
	// Args names the positional arguments in order, each one required.
	Args []string
	// Flags declares the command's own flags, nil for none.
	Flags func(fs *flag.FlagSet)
	// Writes marks a command that writes to the database.
	Writes bool
	// JSON marks a command that answers one JSON document.
	JSON bool
	// Run does the command's work.
	Run func(ctx context.Context, call Call) error
}

// Call is what one run of a command receives.
type Call struct {
	// Args holds the positional arguments, one per name in Command.Args.
	Args []string
	// Stdin is the input a command reads, such as a password.
	Stdin io.Reader
	// Stdout is where a command writes its answer.
	Stdout io.Writer
	// Stderr is where a command writes progress and warnings.
	Stderr io.Writer
	// JSON reports whether -json was passed.
	JSON bool
	// Apply reports whether the run applies its writes.
	Apply bool
}

// Encode writes v to Stdout as one indented JSON document.
func (c Call) Encode(v any) error {
	encoder := json.NewEncoder(c.Stdout)
	encoder.SetIndent("", "  ")
	encoder.SetEscapeHTML(false)
	return encoder.Encode(v)
}
