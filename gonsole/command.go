// SPDX-License-Identifier: Apache-2.0

package gonsole

import (
	"context"
	"encoding/json"
	"errors"
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
	// Migrates marks a core command the core schema steps run before.
	Migrates bool
	// Capability names the capability the acting account must hold, empty for none.
	Capability string
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
	// Env reads the program's settings.
	Env Env
	// JSON reports whether -json was passed.
	JSON bool
	// Apply reports whether the run applies its writes.
	Apply bool
	// Actor is the account the -as flag names.
	Actor string
	// database is the name of the setting that holds the database address.
	database string
}

// DatabaseURL returns the program's database address, an error naming the setting when it is empty.
func (c Call) DatabaseURL() (string, error) {
	if c.database == "" {
		return "", errors.New("gonsole: no database setting in this call")
	}
	return c.Env.Required(c.database)
}

// Step is one named schema step.
type Step struct {
	// Name is the word the step's output line names it by.
	Name string
	// Run applies the step against the database at databaseURL.
	Run func(ctx context.Context, databaseURL string) error
}

// Encode writes v to Stdout as one indented JSON document.
func (c Call) Encode(v any) error {
	encoder := json.NewEncoder(c.Stdout)
	encoder.SetIndent("", "  ")
	encoder.SetEscapeHTML(false)
	return encoder.Encode(v)
}
