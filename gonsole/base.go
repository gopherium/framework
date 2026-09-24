// SPDX-License-Identifier: Apache-2.0

package gonsole

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"io"
)

// base returns the commands the engine owns in the program.
func (r *runner) base() []Command {
	list := func(ctx context.Context, call Call) error {
		return r.list(ctx, call.Stdout)
	}
	commands := []Command{
		{Name: "help", Summary: "print the help of one command", Run: list},
		{Name: "list", Summary: "list every command", Run: list},
		{Name: "version", Summary: "print the version", JSON: true, Run: r.version},
		{Name: "check", Summary: "check every setting, every plugin and every command name", Run: r.check},
	}
	if r.program.Serve != nil {
		commands = append(commands, Command{Name: "serve", Summary: "run the server", Run: r.program.Serve})
	}
	if len(r.program.Migrations) > 0 {
		migrate := func(ctx context.Context, call Call) error { return r.migrate(ctx, call, call.Stdout) }
		commands = append(commands, Command{Name: "migrate", Summary: "apply every schema step", Run: migrate})
	}
	if r.program.Seed != nil {
		commands = append(commands, Command{Name: "seed", Summary: "store the demo data", Writes: true, Run: r.seed})
	}
	return commands
}

// check runs the program's settings check and answers that the settings and command names are valid.
func (r *runner) check(ctx context.Context, call Call) error {
	if r.program.Validate != nil {
		if err := r.program.Validate(ctx, call); err != nil {
			return err
		}
	}
	_, err := io.WriteString(call.Stdout, "settings, plugins and command names are valid\n")
	return err
}

// version prints the program's name and version, as one document with -json.
func (r *runner) version(_ context.Context, call Call) error {
	version := cmp.Or(r.program.Version, "(devel)")
	if call.JSON {
		return call.Encode(struct {
			Name    string `json:"name"`
			Version string `json:"version"`
		}{r.program.Name, version})
	}
	_, err := fmt.Fprintf(call.Stdout, "%s %s\n", r.program.Name, version)
	return err
}

// seed stores the demo data over a migrated schema, a dry run until -yes.
func (r *runner) seed(ctx context.Context, call Call) error {
	if !call.Apply {
		_, err := io.WriteString(call.Stdout, "would store the demo data\n")
		return err
	}
	if err := r.migrate(ctx, call, call.Stderr); err != nil {
		return err
	}
	if err := r.program.Seed(ctx, call); err != nil {
		return err
	}
	r.warn("demo data is for development only, never seed a production database")
	return nil
}

// migrate applies the core schema steps under the lock, writing one line per applied step to w.
func (r *runner) migrate(ctx context.Context, call Call, w io.Writer) (err error) {
	address, err := call.DatabaseURL()
	if err != nil {
		return err
	}
	release, err := r.lock(ctx, address)
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, release(context.WithoutCancel(ctx))) }()
	for _, step := range r.program.Migrations {
		if err := step.Run(ctx, address); err != nil {
			return fmt.Errorf("migrate %s: %w", step.Name, err)
		}
		if _, err := fmt.Fprintf(w, "migrated %s\n", step.Name); err != nil {
			return err
		}
	}
	return nil
}

// lock takes the program's schema lock on the database at address and returns its release, a no-op without Lock.
func (r *runner) lock(ctx context.Context, address string) (func(context.Context) error, error) {
	if r.program.Lock == nil {
		return func(context.Context) error { return nil }, nil
	}
	release, err := r.program.Lock(ctx, address)
	if err != nil {
		return nil, fmt.Errorf("lock the schema: %w", err)
	}
	return func(ctx context.Context) error {
		if err := release(ctx); err != nil {
			return fmt.Errorf("release the schema lock: %w", err)
		}
		return nil
	}, nil
}
