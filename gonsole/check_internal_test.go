// SPDX-License-Identifier: Apache-2.0

package gonsole

import (
	"context"
	"slices"
	"testing"
)

func TestBaseCommandsAreTheCommandsTheRunnerOffers(t *testing.T) {
	t.Parallel()

	r := &runner{program: Program{
		Serve:      func(context.Context, Call) error { return nil },
		Migrations: []Step{{Name: "reports", Run: func(context.Context, string) error { return nil }}},
		Seed:       func(context.Context, Call) error { return nil },
	}}
	var offered []string
	for _, cmd := range r.base() {
		offered = append(offered, cmd.Name)
	}
	slices.Sort(offered)

	want := BaseCommands()
	slices.Sort(want)
	if !slices.Equal(offered, want) {
		t.Errorf("the runner offers %q, want %q", offered, want)
	}
}
