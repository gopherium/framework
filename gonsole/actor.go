// SPDX-License-Identifier: Apache-2.0

package gonsole

import (
	"context"
	"fmt"
	"runtime/debug"
)

// perform authorizes the acting account of call, migrates when cmd asks, runs cmd and records the run when it applied.
func (r *runner) perform(ctx context.Context, cmd Command, call Call) error {
	if err := r.authorize(ctx, cmd, call); err != nil {
		return err
	}
	if cmd.Migrates && call.Apply {
		if err := r.migrate(ctx, call, call.Stderr); err != nil {
			return err
		}
	}
	if err := cmd.Run(ctx, call); err != nil {
		return err
	}
	if !call.Apply {
		r.warn("dry run, nothing changed, pass -yes to apply")
		return nil
	}
	return r.record(ctx, cmd, call)
}

// authorize refuses the acting account of call when it lacks the capability cmd names.
func (r *runner) authorize(ctx context.Context, cmd Command, call Call) error {
	if cmd.Capability == "" {
		return nil
	}
	return r.program.Authorize(ctx, call, cmd.Capability)
}

// record stores the applied run of cmd when cmd names a capability, under a context the end of the run cannot cancel.
func (r *runner) record(ctx context.Context, cmd Command, call Call) error {
	if cmd.Capability == "" {
		return nil
	}
	return r.program.Record(context.WithoutCancel(ctx), call, cmd.Name)
}

// panicked is a panic recovered from the run of one command.
type panicked struct {
	command string
	value   any
	stack   []byte
}

// Error returns the line naming the command and the panic value.
func (p panicked) Error() string {
	return fmt.Sprintf("%s: panic: %v", p.command, p.value)
}

// crashes returns every panic err holds, in the order its message names them.
func crashes(err error) []panicked {
	switch e := err.(type) {
	case panicked:
		return []panicked{e}
	case interface{ Unwrap() []error }:
		var all []panicked
		for _, inner := range e.Unwrap() {
			all = append(all, crashes(inner)...)
		}
		return all
	case interface{ Unwrap() error }:
		return crashes(e.Unwrap())
	}
	return nil
}

// recoverRun turns a panic in the run of the command called name into the error err points at.
func recoverRun(name string, err *error) {
	if value := recover(); value != nil {
		*err = panicked{command: name, value: value, stack: debug.Stack()}
	}
}
