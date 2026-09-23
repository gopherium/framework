// SPDX-License-Identifier: Apache-2.0

package gonsole

import "context"

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

// record stores the applied run of cmd when cmd names a capability.
func (r *runner) record(ctx context.Context, cmd Command, call Call) error {
	if cmd.Capability == "" {
		return nil
	}
	return r.program.Record(ctx, call, cmd.Name)
}
