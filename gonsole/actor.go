// SPDX-License-Identifier: Apache-2.0

package gonsole

import "context"

// perform authorizes the acting account of call, runs cmd with it and records the run when it applied.
func (r *runner) perform(ctx context.Context, cmd Command, call Call) error {
	if cmd.Capability != "" {
		if err := r.program.Authorize(ctx, call, cmd.Capability); err != nil {
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
	if cmd.Capability == "" {
		return nil
	}
	return r.program.Record(ctx, call, cmd.Name)
}
