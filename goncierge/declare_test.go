// SPDX-License-Identifier: Apache-2.0

package goncierge_test

import (
	"errors"
	"fmt"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/gopherium/framework/goncierge"
)

// mustDeclare declares the roles under the source and the rules, failing the test on an error.
func mustDeclare(
	t *testing.T, registry *goncierge.Registry, rules goncierge.Rules, source string, roles ...goncierge.Role,
) {
	t.Helper()

	if err := goncierge.Declare(registry, rules, source, roles); err != nil {
		t.Fatalf("Declare(%q, %v) error = %v, want nil", source, roles, err)
	}
}

func ExampleDeclare() {
	registry := goncierge.New()
	if err := registry.Grant("core", "admin", "manage_users"); err != nil {
		return
	}
	rules := goncierge.Rules{Admin: "admin"}

	fmt.Println(goncierge.Declare(registry, rules, "events", []goncierge.Role{
		{Name: "organizer", Capabilities: []string{"events.manage"}},
	}))
	fmt.Println(registry.CapabilitiesOf("admin"))
	fmt.Println(goncierge.Declare(registry, rules, "tickets", []goncierge.Role{
		{Name: "seller", Capabilities: []string{"sell_tickets"}},
	}))
	// Output:
	// <nil>
	// [events.manage manage_users]
	// goncierge: new capability outside the source's prefix "tickets.": "sell_tickets"
}

func TestDeclareGrantsTheSourcesRoles(t *testing.T) {
	t.Parallel()

	registry := goncierge.New()
	mustGrant(t, registry, "core", "admin", "manage_users")

	mustDeclare(t, registry, goncierge.Rules{}, "events",
		goncierge.Role{Name: "organizer", Capabilities: []string{"events.manage", "events.publish"}},
		goncierge.Role{Name: "steward"},
	)

	want := map[string][]string{
		"admin":     {"manage_users"},
		"organizer": {"events.manage", "events.publish"},
		"steward":   {},
	}
	if got := snapshot(registry); !sameSnapshot(got, want) {
		t.Errorf("registry after Declare(events) = %v, want %v", got, want)
	}
}

func TestDeclareGivesTheAdminEveryCapabilityTheSourceDeclares(t *testing.T) {
	t.Parallel()

	registry := goncierge.New()
	mustGrant(t, registry, "core", "admin", "manage_users")
	mustGrant(t, registry, "core", "editor", "edit_others")
	rules := goncierge.Rules{Admin: "admin"}

	mustDeclare(t, registry, rules, "events",
		goncierge.Role{Name: "organizer", Capabilities: []string{"events.manage"}},
		goncierge.Role{Name: "moderator", Capabilities: []string{"events.publish", "edit_others"}},
	)

	want := []string{"edit_others", "events.manage", "events.publish", "manage_users"}
	if got := registry.CapabilitiesOf("admin"); !slices.Equal(got, want) {
		t.Errorf("CapabilitiesOf(admin) = %q, want %q", got, want)
	}
	grantable := []string{"admin", "moderator", "editor", "organizer"}
	if got := registry.Grantable("admin"); !slices.Equal(got, grantable) {
		t.Errorf("Grantable(admin) = %q, want %q", got, grantable)
	}

	registry.Withdraw("events")

	if got, want := registry.CapabilitiesOf("admin"), []string{"manage_users"}; !slices.Equal(got, want) {
		t.Errorf("CapabilitiesOf(admin) = %q after the source withdrew, want %q", got, want)
	}
}

func TestDeclareWithNoCapabilitiesGivesTheAdminNothing(t *testing.T) {
	t.Parallel()

	var registry goncierge.Registry
	mustGrant(t, &registry, "core", "admin", "manage_users")

	mustDeclare(t, &registry, goncierge.Rules{Admin: "admin"}, "events", goncierge.Role{Name: "steward"})

	want := map[string][]string{"admin": {"manage_users"}, "steward": {}}
	if got := snapshot(&registry); !sameSnapshot(got, want) {
		t.Errorf("registry after Declare(events) = %v, want %v", got, want)
	}
}

func TestDeclareMergesRepeatedRolesAndTheAdmin(t *testing.T) {
	t.Parallel()

	registry := goncierge.New()
	mustGrant(t, registry, "core", "admin", "manage_users")

	mustDeclare(t, registry, goncierge.Rules{Admin: "admin"}, "events",
		goncierge.Role{Name: "organizer", Capabilities: []string{"events.manage"}},
		goncierge.Role{Name: "organizer", Capabilities: []string{"events.publish"}},
		goncierge.Role{Name: "admin", Capabilities: []string{"events.remove"}},
	)

	want := map[string][]string{
		"admin":     {"events.manage", "events.publish", "events.remove", "manage_users"},
		"organizer": {"events.manage", "events.publish"},
	}
	if got := snapshot(registry); !sameSnapshot(got, want) {
		t.Errorf("registry after Declare(events) = %v, want %v", got, want)
	}
}

func TestDeclareRefusesAnAdminNoOtherSourceCreated(t *testing.T) {
	t.Parallel()

	registry := goncierge.New()
	mustGrant(t, registry, "core", "admin", "manage_users")
	before := snapshot(registry)

	for _, roles := range [][]goncierge.Role{
		{{Name: "steward"}},
		{{Name: "organizer", Capabilities: []string{"events.manage"}}},
		{{Name: "administrator", Capabilities: []string{"events.manage"}}},
	} {
		err := goncierge.Declare(registry, goncierge.Rules{Admin: "administrator"}, "events", roles)
		if !errors.Is(err, goncierge.ErrUnknownAdmin) {
			t.Errorf("Declare(events, %v) with an admin nobody else created error = %v, want ErrUnknownAdmin", roles, err)
			continue
		}
		if want := strconv.QuoteToASCII("administrator"); !strings.Contains(err.Error(), want) {
			t.Errorf("Declare(events) error = %q, want it to name %s", err, want)
		}
	}

	if after := snapshot(registry); !sameSnapshot(before, after) {
		t.Errorf("registry after the refused declarations = %v, want unchanged %v", after, before)
	}
}

func TestDeclareRefusesASourceOutsideTheNameRule(t *testing.T) {
	t.Parallel()

	for _, source := range []string{"Events", "role editor", "events.tickets", "-events", strings.Repeat("a", 65)} {
		registry := goncierge.New()

		err := goncierge.Declare(registry, goncierge.Rules{}, source, []goncierge.Role{{Name: "steward"}})
		if !errors.Is(err, goncierge.ErrInvalidName) {
			t.Errorf("Declare(%+q) error = %v, want ErrInvalidName", source, err)
			continue
		}
		if want := "source: " + strconv.QuoteToASCII(source); !strings.Contains(err.Error(), want) {
			t.Errorf("Declare error = %q, want it to name %s", err, want)
		}
		if registry.Known("steward") {
			t.Errorf("Known(steward) = true after Declare(%+q) was refused, want false", source)
		}
	}
}

func TestCheckBesideADeclareSeesTheOldDeclarationOrTheNew(t *testing.T) {
	t.Parallel()

	registry := goncierge.New()
	mustGrant(t, registry, "core", "admin", "manage_users")
	rules := goncierge.Rules{Admin: "admin"}
	older := []goncierge.Role{
		{Name: "organizer", Capabilities: []string{"events.manage"}},
		{Name: "steward", Capabilities: []string{"events.manage"}},
	}
	newer := []goncierge.Role{
		{Name: "organizer", Capabilities: []string{"events.publish"}},
		{Name: "steward", Capabilities: []string{"events.publish"}},
	}
	mustDeclare(t, registry, rules, "events", older...)

	var stop atomic.Bool
	var group sync.WaitGroup
	writing := make(chan struct{})
	group.Go(func() {
		close(writing)
		for !stop.Load() {
			_ = goncierge.Declare(registry, rules, "events", newer)
			_ = goncierge.Declare(registry, rules, "events", older)
			runtime.Gosched()
		}
	})

	every := []string{"admin", "organizer", "steward"}
	<-writing
	for range 500 {
		if got := registry.Roles(); !slices.Equal(got, every) {
			t.Errorf("Roles() = %q beside a declaration, want %q", got, every)
			break
		}
		if got := registry.Grantable("admin"); !slices.Equal(got, every) {
			t.Errorf("Grantable(admin) = %q beside a declaration, want %q", got, every)
			break
		}
		if got := registry.HoldersOf("events.manage"); len(got) != 0 && !slices.Equal(got, every) {
			t.Errorf("HoldersOf(events.manage) = %q beside a declaration, want none or %q", got, every)
			break
		}
		runtime.Gosched()
	}
	stop.Store(true)
	group.Wait()
}

func TestDeclareAcceptsACapabilityAnotherSourceGranted(t *testing.T) {
	t.Parallel()

	registry := goncierge.New()
	mustGrant(t, registry, "core", "admin", "manage_users")
	mustDeclare(t, registry, goncierge.Rules{}, "events",
		goncierge.Role{Name: "organizer", Capabilities: []string{"events.manage", "manage_users"}},
	)

	mustDeclare(t, registry, goncierge.Rules{}, "events-tickets",
		goncierge.Role{Name: "ticketer", Capabilities: []string{"events.manage", "events-tickets.sell"}},
	)

	if got, want := registry.HoldersOf("events.manage"), []string{"organizer", "ticketer"}; !slices.Equal(got, want) {
		t.Errorf("HoldersOf(events.manage) = %q, want %q", got, want)
	}
}

func TestDeclareRefusesANewCapabilityOutsideThePrefix(t *testing.T) {
	t.Parallel()

	for _, capability := range []string{"events.manage", "sell_tickets", "tickets", "ticketsx.sell"} {
		registry := ladder(t)
		before := snapshot(registry)

		err := goncierge.Declare(registry, goncierge.Rules{Admin: "admin"}, "tickets", []goncierge.Role{
			{Name: "seller", Capabilities: []string{"tickets.sell"}},
			{Name: "ticketer", Capabilities: []string{"tickets.sell", capability, "tickets.refund"}},
		})

		if capability == "events.manage" {
			if err != nil {
				t.Errorf("Declare(tickets) with %q another source granted error = %v, want nil", capability, err)
			}
			continue
		}
		if !errors.Is(err, goncierge.ErrOutsidePrefix) {
			t.Errorf("Declare(tickets) with %q error = %v, want ErrOutsidePrefix", capability, err)
			continue
		}
		for _, named := range []string{strconv.QuoteToASCII("tickets."), strconv.QuoteToASCII(capability)} {
			if !strings.Contains(err.Error(), named) {
				t.Errorf("Declare(tickets) error = %q, want it to name %s", err, named)
			}
		}
		if after := snapshot(registry); !sameSnapshot(before, after) {
			t.Errorf("registry after the refused Declare(tickets) = %v, want unchanged %v", after, before)
		}
	}
}

func TestDeclareJudgesThePrefixAgainstOtherSourcesOnly(t *testing.T) {
	t.Parallel()

	registry := goncierge.New()
	mustGrant(t, registry, "core", "editor", "edit_others")
	organizer := goncierge.Role{Name: "organizer", Capabilities: []string{"edit_others"}}
	mustDeclare(t, registry, goncierge.Rules{}, "events", organizer)

	registry.Revoke("core", "editor", "edit_others")

	err := goncierge.Declare(registry, goncierge.Rules{}, "events", []goncierge.Role{organizer})
	if !errors.Is(err, goncierge.ErrOutsidePrefix) {
		t.Fatalf("Declare(events) again after core revoked edit_others error = %v, want ErrOutsidePrefix", err)
	}
	if !registry.Can("organizer", "edit_others") {
		t.Error("Can(organizer, edit_others) = false after a refused Declare, want the earlier declaration kept")
	}
}

func TestDeclareReplacesTheSourcesEarlierDeclaration(t *testing.T) {
	t.Parallel()

	registry := goncierge.New()
	mustGrant(t, registry, "core", "admin", "manage_users")
	rules := goncierge.Rules{Admin: "admin"}

	mustDeclare(t, registry, rules, "events",
		goncierge.Role{Name: "organizer", Capabilities: []string{"events.manage"}},
		goncierge.Role{Name: "steward", Capabilities: []string{"events.publish"}},
	)
	mustDeclare(t, registry, rules, "events",
		goncierge.Role{Name: "organizer", Capabilities: []string{"events.publish"}},
	)

	want := map[string][]string{
		"admin":     {"events.publish", "manage_users"},
		"organizer": {"events.publish"},
	}
	if got := snapshot(registry); !sameSnapshot(got, want) {
		t.Errorf("registry after the second Declare(events) = %v, want %v", got, want)
	}
}

func TestRefusedDeclareChangesNothing(t *testing.T) {
	t.Parallel()

	registry := ladder(t)
	rules := goncierge.Rules{Admin: "admin"}
	mustDeclare(t, registry, rules, "tickets", goncierge.Role{Name: "seller", Capabilities: []string{"tickets.sell"}})
	before := snapshot(registry)

	for _, attempt := range []struct {
		rules      goncierge.Rules
		source     string
		role       string
		capability string
		want       error
	}{
		{rules, "", "seller", "tickets.refund", goncierge.ErrEmptySource},
		{rules, "tickets", "Seller", "tickets.refund", goncierge.ErrInvalidName},
		{rules, "tickets", "seller", "Refund", goncierge.ErrInvalidName},
		{goncierge.Rules{Admin: "Admin"}, "tickets", "seller", "tickets.refund", goncierge.ErrInvalidName},
		{goncierge.Rules{Admin: "administrator"}, "tickets", "seller", "tickets.refund", goncierge.ErrUnknownAdmin},
		{rules, "Tickets", "seller", "tickets.refund", goncierge.ErrInvalidName},
		{rules, "tickets", "seller", "refund", goncierge.ErrOutsidePrefix},
	} {
		role := goncierge.Role{Name: attempt.role, Capabilities: []string{"tickets.sell", attempt.capability}}
		err := goncierge.Declare(registry, attempt.rules, attempt.source, []goncierge.Role{role})
		if !errors.Is(err, attempt.want) {
			t.Errorf("Declare(%q, %v) error = %v, want %v", attempt.source, role, err, attempt.want)
		}
	}

	if after := snapshot(registry); !sameSnapshot(before, after) {
		t.Errorf("registry after refused declarations = %v, want unchanged %v", after, before)
	}
}
