// SPDX-License-Identifier: Apache-2.0

package pluginkit_test

import (
	"context"
	"errors"
	"slices"
	"testing"

	"github.com/gopherium/framework/pluginkit"
)

var _ pluginkit.Seeder = (*seedingPlugin)(nil)

type seedingPlugin struct {
	fakePlugin
	seedErr   error
	seedPanic bool
}

func (s *seedingPlugin) Seed(_ context.Context) error {
	*s.calls = append(*s.calls, s.id+" seed")
	if s.seedPanic {
		panic("boom")
	}
	return s.seedErr
}

func TestHostSeedsEverySeederInOrder(t *testing.T) {
	t.Parallel()

	var calls []string
	host := pluginkit.NewHost(
		&seedingPlugin{fakePlugin: fakePlugin{id: "alpha", calls: &calls}},
		&fakePlugin{id: "beta", calls: &calls},
		&seedingPlugin{fakePlugin: fakePlugin{id: "gamma", calls: &calls}},
	)

	if err := host.Seed(t.Context()); err != nil {
		t.Fatalf("Seed() error = %v, want nil", err)
	}

	want := []string{"alpha seed", "gamma seed"}
	if !slices.Equal(want, calls) {
		t.Errorf("seed ordering = %v, want %v", calls, want)
	}
}

func TestHostStopsSeedingAtTheFirstFailure(t *testing.T) {
	t.Parallel()

	errSeed := errors.New("seed exploded")
	var calls []string
	host := pluginkit.NewHost(
		&seedingPlugin{fakePlugin: fakePlugin{id: "alpha", calls: &calls}, seedErr: errSeed},
		&seedingPlugin{fakePlugin: fakePlugin{id: "beta", calls: &calls}},
	)

	err := host.Seed(t.Context())

	if !errors.Is(err, errSeed) {
		t.Fatalf("Seed() error = %v, want %v in its chain", err, errSeed)
	}
	if !slices.Equal([]string{"alpha seed"}, calls) {
		t.Errorf("calls = %v, want seeding to stop at the failure", calls)
	}
}

func TestHostTagsASeedPanicWithItsPlugin(t *testing.T) {
	t.Parallel()

	var calls []string
	host := pluginkit.NewHost(
		&seedingPlugin{fakePlugin: fakePlugin{id: "alpha", calls: &calls}, seedPanic: true},
	)

	err := host.Seed(t.Context())

	if err == nil {
		t.Fatal("Seed() error = nil, want the panic reported")
	}
	if err.Error() != "pluginkit: alpha seed panicked: boom" {
		t.Errorf("error = %q, want it to name the plugin and the operation", err)
	}
}

// contextSeeder is a seeder that records the caller value its context carries and whether that context can end.
type contextSeeder struct {
	fakePlugin
	seen *[]any
}

// Seed records the caller value of ctx and whether ctx can end.
func (c *contextSeeder) Seed(ctx context.Context) error {
	*c.seen = append(*c.seen, ctx.Value(callerKey{}), ctx.Done() != nil)
	return nil
}

func TestHostSeedHandsTheCallerContextToEverySeeder(t *testing.T) {
	t.Parallel()

	var calls []string
	var seen []any
	host := pluginkit.NewHost(&contextSeeder{fakePlugin: fakePlugin{id: "alpha", calls: &calls}, seen: &seen})
	ctx, cancel := context.WithCancel(context.WithValue(t.Context(), callerKey{}, "caller"))
	defer cancel()

	if err := host.Seed(ctx); err != nil {
		t.Fatalf("Seed() error = %v, want nil", err)
	}

	if want := []any{"caller", true}; !slices.Equal(want, seen) {
		t.Errorf("context seen = %v, want %v", seen, want)
	}
}

func TestHostSeedsNothingWithoutASeeder(t *testing.T) {
	t.Parallel()

	var calls []string
	host := pluginkit.NewHost(&fakePlugin{id: "alpha", calls: &calls})

	if err := host.Seed(t.Context()); err != nil {
		t.Fatalf("Seed() error = %v, want nil", err)
	}
	if len(calls) != 0 {
		t.Errorf("calls = %v, want none", calls)
	}
}
