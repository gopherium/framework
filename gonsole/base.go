// SPDX-License-Identifier: Apache-2.0

package gonsole

import (
	"context"
	"io"
)

// base returns the commands the engine owns in every program.
func (r *runner) base() []Command {
	list := func(_ context.Context, call Call) error {
		_, err := io.WriteString(call.Stdout, r.listing())
		return err
	}
	return []Command{
		{Name: "help", Summary: "print the help of one command", Run: list},
		{Name: "list", Summary: "list every command", Run: list},
	}
}
