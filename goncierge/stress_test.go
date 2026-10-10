// SPDX-License-Identifier: Apache-2.0

package goncierge_test

import (
	"runtime"
	"slices"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/gopherium/framework/goncierge"
)

// grantCore grants the three core roles the stress test reads beside the toggling source.
func grantCore(t *testing.T, registry *goncierge.Registry) {
	t.Helper()

	mustGrant(t, registry, "core", "admin", "manage_users", "manage_settings", "change_others_work")
	mustGrant(t, registry, "core", "editor", "change_others_work")
	mustGrant(t, registry, "core", "author")
}

func TestTogglingASourceBesideReadersKeepsEveryAnswerWhole(t *testing.T) {
	t.Parallel()

	registry := goncierge.New()
	grantCore(t, registry)
	whole := []string{"events.manage", "events.publish"}

	var stop atomic.Bool
	var writers, readers sync.WaitGroup
	for range 8 {
		writers.Go(func() {
			for range 300 {
				_ = registry.Grant("events", "moderator", whole...)
				_ = registry.Grant("events", "admin", "events.manage")
				registry.Withdraw("events")
			}
		})
	}
	byAdmin := []string{"admin", "editor", "author"}
	byModerator := []string{"moderator", "author"}
	for range 8 {
		readers.Go(func() {
			for !stop.Load() {
				if !registry.Can("admin", "manage_users") {
					t.Error("Can(admin, manage_users) = false while the source toggles, want true")
					return
				}
				if got := registry.CapabilitiesOf("moderator"); len(got) != 0 && !slices.Equal(got, whole) {
					t.Errorf("CapabilitiesOf(moderator) = %q, want none or %q", got, whole)
					return
				}
				if got := registry.Grantable("admin"); !slices.Equal(got, byAdmin) {
					t.Errorf("Grantable(admin) = %q while the source toggles, want %q", got, byAdmin)
					return
				}
				if got := registry.Grantable("moderator"); !slices.Equal(got, byModerator[1:]) && !slices.Equal(got, byModerator) {
					t.Errorf("Grantable(moderator) = %q, want %q or %q", got, byModerator[1:], byModerator)
					return
				}
				runtime.Gosched()
			}
		})
	}
	writers.Wait()
	stop.Store(true)
	readers.Wait()

	if registry.Known("moderator") || registry.Can("admin", "events.manage") {
		t.Error("the events grants survived the last withdraw")
	}
	if !registry.Can("admin", "manage_users") {
		t.Error("Can(admin, manage_users) = false at the end, want true")
	}
}
