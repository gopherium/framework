// SPDX-License-Identifier: Apache-2.0

package goncierge_test

import (
	"errors"
	"slices"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/gopherium/framework/goncierge"
)

func TestReplaceSwapsEveryGrantOfTheSource(t *testing.T) {
	t.Parallel()

	registry := ladder(t)

	err := registry.Replace("events", []goncierge.Role{
		{Name: "organizer", Capabilities: []string{"events.publish"}},
		{Name: "steward"},
	})
	if err != nil {
		t.Fatalf("Replace(events) error = %v, want nil", err)
	}

	want := map[string][]string{
		"admin":     {"posts.edit", "settings.manage", "users.manage"},
		"editor":    {"posts.edit"},
		"member":    {},
		"organizer": {"events.publish"},
		"steward":   {},
	}
	if got := snapshot(registry); !sameSnapshot(got, want) {
		t.Errorf("registry after Replace(events) = %v, want %v", got, want)
	}
}

func TestReplaceWithNoRolesWithdrawsTheSource(t *testing.T) {
	t.Parallel()

	registry := ladder(t)

	if err := registry.Replace("events", nil); err != nil {
		t.Fatalf("Replace(events, nil) error = %v, want nil", err)
	}

	if got, want := registry.Roles(), []string{"admin", "editor", "member"}; !slices.Equal(got, want) {
		t.Errorf("Roles() = %q, want %q", got, want)
	}
	if registry.Can("admin", "events.manage") {
		t.Error("Can(admin, events.manage) = true after the source replaced its grants with none, want false")
	}
}

func TestReplaceMergesARoleNamedTwice(t *testing.T) {
	t.Parallel()

	registry := goncierge.New()

	err := registry.Replace("events", []goncierge.Role{
		{Name: "organizer", Capabilities: []string{"events.manage"}},
		{Name: "organizer", Capabilities: []string{"events.publish", "events.manage"}},
	})
	if err != nil {
		t.Fatalf("Replace(events) error = %v, want nil", err)
	}

	want := []string{"events.manage", "events.publish"}
	if got := registry.CapabilitiesOf("organizer"); !slices.Equal(got, want) {
		t.Errorf("CapabilitiesOf(organizer) = %q, want %q", got, want)
	}
}

func TestRefusedReplaceChangesNothing(t *testing.T) {
	t.Parallel()

	registry := ladder(t)
	before := snapshot(registry)

	for _, attempt := range []struct {
		source string
		roles  []goncierge.Role
		want   error
	}{
		{"", []goncierge.Role{{Name: "steward"}}, goncierge.ErrEmptySource},
		{"events", []goncierge.Role{{Name: "steward"}, {Name: "Organizer"}}, goncierge.ErrInvalidName},
		{
			"events",
			[]goncierge.Role{{Name: "steward", Capabilities: []string{"events.manage", "Events.Publish"}}},
			goncierge.ErrInvalidName,
		},
	} {
		if err := registry.Replace(attempt.source, attempt.roles); !errors.Is(err, attempt.want) {
			t.Errorf("Replace(%q, %v) error = %v, want %v", attempt.source, attempt.roles, err, attempt.want)
		}
	}

	if after := snapshot(registry); !sameSnapshot(before, after) {
		t.Errorf("registry after refused replaces = %v, want unchanged %v", after, before)
	}
}

func TestReplaceKeepsNoReferenceToTheCallersSlices(t *testing.T) {
	t.Parallel()

	registry := goncierge.New()
	roles := []goncierge.Role{{Name: "organizer", Capabilities: []string{"events.manage"}}}
	if err := registry.Replace("events", roles); err != nil {
		t.Fatalf("Replace(events) error = %v, want nil", err)
	}

	roles[0].Name = "steward"
	roles[0].Capabilities[0] = "events.remove"

	if got, want := snapshot(registry), map[string][]string{"organizer": {"events.manage"}}; !sameSnapshot(got, want) {
		t.Errorf("registry after the caller changed its slices = %v, want %v", got, want)
	}
}

func TestCheckBesideAReplaceSeesTheOldGrantsOrTheNew(t *testing.T) {
	t.Parallel()

	registry := goncierge.New()
	older := []goncierge.Role{
		{Name: "organizer", Capabilities: []string{"events.manage"}},
		{Name: "steward", Capabilities: []string{"events.manage"}},
	}
	newer := []goncierge.Role{
		{Name: "organizer", Capabilities: []string{"events.publish"}},
		{Name: "steward", Capabilities: []string{"events.publish"}},
	}
	if err := registry.Replace("events", older); err != nil {
		t.Fatalf("Replace(events) error = %v, want nil", err)
	}

	var done atomic.Bool
	var group sync.WaitGroup
	group.Go(func() {
		defer done.Store(true)
		for range 500 {
			_ = registry.Replace("events", newer)
			_ = registry.Replace("events", older)
		}
	})

	both := []string{"organizer", "steward"}
	for !done.Load() {
		if got := registry.Roles(); !slices.Equal(got, both) {
			t.Fatalf("Roles() = %q beside a replace, want %q", got, both)
		}
		if got := registry.HoldersOf("events.manage"); len(got) != 0 && !slices.Equal(got, both) {
			t.Fatalf("HoldersOf(events.manage) = %q beside a replace, want none or %q", got, both)
		}
	}
	group.Wait()
}
