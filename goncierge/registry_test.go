// SPDX-License-Identifier: Apache-2.0

package goncierge_test

import (
	"errors"
	"maps"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/gopherium/framework/goncierge"
)

// mustGrant grants the capabilities to the role under the source, failing the test on an error.
func mustGrant(t *testing.T, registry *goncierge.Registry, source, role string, capabilities ...string) {
	t.Helper()

	if err := registry.Grant(source, role, capabilities...); err != nil {
		t.Fatalf("Grant(%q, %q, %q) error = %v, want nil", source, role, capabilities, err)
	}
}

// snapshot returns every role the registry holds with the capabilities it carries.
func snapshot(registry *goncierge.Registry) map[string][]string {
	held := map[string][]string{}
	for _, role := range registry.Roles() {
		held[role] = registry.CapabilitiesOf(role)
	}

	return held
}

// sameSnapshot reports whether two snapshots hold the same roles and capabilities.
func sameSnapshot(a, b map[string][]string) bool {
	return maps.EqualFunc(a, b, slices.Equal)
}

// ladder returns a registry whose roles carry four, two, one, one and no capabilities.
func ladder(t *testing.T) *goncierge.Registry {
	t.Helper()

	registry := goncierge.New()
	mustGrant(t, registry, "core", "admin", "users.manage", "settings.manage", "posts.edit")
	mustGrant(t, registry, "core", "editor", "posts.edit")
	mustGrant(t, registry, "core", "member")
	mustGrant(t, registry, "events", "admin", "events.manage")
	mustGrant(t, registry, "events", "organizer", "events.manage")
	mustGrant(t, registry, "events", "moderator", "events.manage", "posts.edit")

	return registry
}

func TestGrantedCapabilityIsCarried(t *testing.T) {
	t.Parallel()

	registry := goncierge.New()
	mustGrant(t, registry, "core", "admin", "users.manage")

	if !registry.Can("admin", "users.manage") {
		t.Error("Can(admin, users.manage) = false after the grant, want true")
	}
	if registry.Can("admin", "settings.manage") {
		t.Error("Can(admin, settings.manage) = true without a grant, want false")
	}
}

func TestEmptyRoleCarriesNothing(t *testing.T) {
	t.Parallel()

	registry := goncierge.New()
	mustGrant(t, registry, "core", "admin", "users.manage")

	if registry.Can("", "users.manage") {
		t.Error("Can(\"\", users.manage) = true, want an empty role never carrying a capability")
	}
	if got := registry.CapabilitiesOf(""); len(got) != 0 {
		t.Errorf("CapabilitiesOf(\"\") = %q, want none", got)
	}
	if registry.Known("") {
		t.Error("Known(\"\") = true, want false")
	}
}

func TestUnknownRoleCarriesNothing(t *testing.T) {
	t.Parallel()

	registry := goncierge.New()
	mustGrant(t, registry, "core", "admin", "users.manage")

	if registry.Can("editor", "users.manage") {
		t.Error("Can(editor, users.manage) = true for a role nobody created, want false")
	}
	if got := registry.CapabilitiesOf("editor"); len(got) != 0 {
		t.Errorf("CapabilitiesOf(editor) = %q, want none", got)
	}
}

func TestCapabilityGrantedByTwoSourcesStaysUntilBothWithdraw(t *testing.T) {
	t.Parallel()

	registry := goncierge.New()
	mustGrant(t, registry, "core", "admin", "events.manage")
	mustGrant(t, registry, "events", "admin", "events.manage")

	registry.Withdraw("events")
	if !registry.Can("admin", "events.manage") {
		t.Error("Can(admin, events.manage) = false after one source withdrew, want the other grant kept")
	}

	registry.Withdraw("core")
	if registry.Can("admin", "events.manage") {
		t.Error("Can(admin, events.manage) = true after both sources withdrew, want false")
	}
}

func TestRoleOnlyOneSourceCreatedGoesWithIt(t *testing.T) {
	t.Parallel()

	registry := goncierge.New()
	mustGrant(t, registry, "core", "admin", "users.manage")
	mustGrant(t, registry, "events", "organizer", "events.manage")

	registry.Withdraw("events")

	if got, want := registry.Roles(), []string{"admin"}; !slices.Equal(got, want) {
		t.Errorf("Roles() = %q after the withdraw, want %q", got, want)
	}
	if got, want := registry.Capabilities(), []string{"users.manage"}; !slices.Equal(got, want) {
		t.Errorf("Capabilities() = %q after the withdraw, want %q", got, want)
	}
}

func TestRoleTwoSourcesCreatedStaysAfterOneWithdraws(t *testing.T) {
	t.Parallel()

	registry := goncierge.New()
	mustGrant(t, registry, "core", "moderator", "posts.edit")
	mustGrant(t, registry, "events", "moderator", "events.manage")

	registry.Withdraw("events")

	if got, want := registry.Roles(), []string{"moderator"}; !slices.Equal(got, want) {
		t.Errorf("Roles() = %q, want %q", got, want)
	}
	if got, want := registry.CapabilitiesOf("moderator"), []string{"posts.edit"}; !slices.Equal(got, want) {
		t.Errorf("CapabilitiesOf(moderator) = %q, want %q", got, want)
	}
}

func TestGrantWithNoCapabilitiesDeclaresTheRole(t *testing.T) {
	t.Parallel()

	registry := goncierge.New()
	mustGrant(t, registry, "core", "member")

	if got, want := registry.Roles(), []string{"member"}; !slices.Equal(got, want) {
		t.Errorf("Roles() = %q, want %q", got, want)
	}
	if got := registry.CapabilitiesOf("member"); len(got) != 0 {
		t.Errorf("CapabilitiesOf(member) = %q, want none", got)
	}
	if !registry.Known("member") {
		t.Error("Known(member) = false, want true")
	}
}

func TestGrantRefusesAnEmptySource(t *testing.T) {
	t.Parallel()

	registry := goncierge.New()

	err := registry.Grant("", "admin", "users.manage")
	if !errors.Is(err, goncierge.ErrEmptySource) {
		t.Fatalf("Grant(\"\", admin) error = %v, want ErrEmptySource", err)
	}
	if registry.Known("admin") {
		t.Error("Known(admin) = true after a refused grant, want false")
	}
}

func TestGrantRefusesANameTheRuleRefuses(t *testing.T) {
	t.Parallel()

	refused := []string{
		"",
		"Shop manager",
		"shop manager",
		"Events.Manage",
		"ADMIN",
		"\xd0\xb0dmin",
		"admín",
		"1admin",
		"_admin",
		".admin",
		"-admin",
		"admin!",
		"admin\n",
		"admin/edit",
		strings.Repeat("a", 65),
	}

	for _, name := range refused {
		registry := goncierge.New()

		err := registry.Grant("core", name, "users.manage")
		if !errors.Is(err, goncierge.ErrInvalidName) {
			t.Errorf("Grant(core, %+q, users.manage) error = %v, want ErrInvalidName", name, err)
			continue
		}
		if want := "role: " + strconv.QuoteToASCII(name); !strings.Contains(err.Error(), want) {
			t.Errorf("Grant role error = %q, want it to name %s", err, want)
		}

		err = registry.Grant("core", "admin", "users.manage", name)
		if !errors.Is(err, goncierge.ErrInvalidName) {
			t.Errorf("Grant(core, admin, %+q) error = %v, want ErrInvalidName", name, err)
			continue
		}
		if want := "capability: " + strconv.QuoteToASCII(name); !strings.Contains(err.Error(), want) {
			t.Errorf("Grant capability error = %q, want it to name %s", err, want)
		}
	}
}

func TestGrantAcceptsEveryNameTheRuleAllows(t *testing.T) {
	t.Parallel()

	allowed := []string{
		"a",
		"a1",
		"shop_manager",
		"events.manage",
		"role-editor.manage_roles",
		"x-1.y_2",
		strings.Repeat("a", 64),
	}

	for _, name := range allowed {
		registry := goncierge.New()

		if err := registry.Grant("core", name, name); err != nil {
			t.Errorf("Grant(core, %q, %q) error = %v, want nil", name, name, err)
			continue
		}
		if !registry.Can(name, name) {
			t.Errorf("Can(%q, %q) = false after the grant, want true", name, name)
		}
	}
}

func TestGrantTakesAnySourceThatIsNotEmpty(t *testing.T) {
	t.Parallel()

	registry := goncierge.New()
	mustGrant(t, registry, "Role Editor/2", "member")

	registry.Withdraw("Role Editor/2")

	if registry.Known("member") {
		t.Error("Known(member) = true after its source withdrew, want false")
	}
}

func TestRefusedGrantChangesNothing(t *testing.T) {
	t.Parallel()

	registry := ladder(t)
	before := snapshot(registry)

	if err := registry.Grant("core", "editor", "posts.publish", "Posts.Delete"); err == nil {
		t.Fatal("Grant with one refused capability error = nil, want ErrInvalidName")
	}
	if err := registry.Grant("core", "Reviewer", "posts.edit"); err == nil {
		t.Fatal("Grant with a refused role error = nil, want ErrInvalidName")
	}
	if err := registry.Grant("", "reviewer", "posts.edit"); err == nil {
		t.Fatal("Grant with an empty source error = nil, want ErrEmptySource")
	}

	if after := snapshot(registry); !sameSnapshot(before, after) {
		t.Errorf("registry after refused grants = %v, want unchanged %v", after, before)
	}
}

func TestGrantingACapabilityTwiceListsItOnce(t *testing.T) {
	t.Parallel()

	registry := goncierge.New()
	mustGrant(t, registry, "core", "admin", "users.manage", "users.manage")
	mustGrant(t, registry, "core", "admin", "users.manage")
	mustGrant(t, registry, "events", "admin", "users.manage")

	if got, want := registry.CapabilitiesOf("admin"), []string{"users.manage"}; !slices.Equal(got, want) {
		t.Errorf("CapabilitiesOf(admin) = %q, want %q", got, want)
	}
	if got, want := registry.Capabilities(), []string{"users.manage"}; !slices.Equal(got, want) {
		t.Errorf("Capabilities() = %q, want %q", got, want)
	}
}

func TestListsAreSorted(t *testing.T) {
	t.Parallel()

	registry := ladder(t)
	everyRole := []string{"admin", "editor", "member", "moderator", "organizer"}
	everyCapability := []string{"events.manage", "posts.edit", "settings.manage", "users.manage"}

	if got := registry.Roles(); !slices.Equal(got, everyRole) {
		t.Errorf("Roles() = %q, want %q", got, everyRole)
	}
	if got := registry.Capabilities(); !slices.Equal(got, everyCapability) {
		t.Errorf("Capabilities() = %q, want %q", got, everyCapability)
	}
	if got := registry.CapabilitiesOf("admin"); !slices.Equal(got, everyCapability) {
		t.Errorf("CapabilitiesOf(admin) = %q, want %q", got, everyCapability)
	}
}

func TestReturnedSlicesAreCopies(t *testing.T) {
	t.Parallel()

	registry := ladder(t)
	before := snapshot(registry)

	for _, answer := range [][]string{
		registry.Roles(),
		registry.Capabilities(),
		registry.CapabilitiesOf("admin"),
		registry.HoldersOf("posts.edit"),
		registry.Grantable("admin"),
	} {
		for i := range answer {
			answer[i] = "changed.by_caller"
		}
	}

	if after := snapshot(registry); !sameSnapshot(before, after) {
		t.Errorf("registry after changing returned slices = %v, want unchanged %v", after, before)
	}
	if got, want := registry.Grantable("admin")[0], "admin"; got != want {
		t.Errorf("Grantable(admin)[0] = %q after changing an earlier answer, want %q", got, want)
	}
}

func TestGrantKeepsNoReferenceToTheCallersSlice(t *testing.T) {
	t.Parallel()

	registry := goncierge.New()
	capabilities := []string{"users.manage"}
	mustGrant(t, registry, "core", "admin", capabilities...)

	capabilities[0] = "settings.manage"

	if registry.Can("admin", "settings.manage") {
		t.Error("Can(admin, settings.manage) = true after the caller changed its slice, want false")
	}
}

func TestKnownFollowsTheSourcesThatCreatedTheRole(t *testing.T) {
	t.Parallel()

	registry := goncierge.New()
	if registry.Known("organizer") {
		t.Error("Known(organizer) = true before any grant, want false")
	}

	mustGrant(t, registry, "events", "organizer", "events.manage")
	if !registry.Known("organizer") {
		t.Error("Known(organizer) = false after the grant, want true")
	}

	registry.Withdraw("events")
	if registry.Known("organizer") {
		t.Error("Known(organizer) = true after its only source withdrew, want false")
	}
}

func TestRevokeOfSomeCapabilitiesKeepsTheRoleAndTheRest(t *testing.T) {
	t.Parallel()

	registry := goncierge.New()
	mustGrant(t, registry, "editor-plugin", "reviewer", "posts.edit", "posts.publish")

	registry.Revoke("editor-plugin", "reviewer", "posts.publish")

	if got, want := registry.CapabilitiesOf("reviewer"), []string{"posts.edit"}; !slices.Equal(got, want) {
		t.Errorf("CapabilitiesOf(reviewer) = %q, want %q", got, want)
	}

	registry.Revoke("editor-plugin", "reviewer", "posts.edit")

	if !registry.Known("reviewer") {
		t.Error("Known(reviewer) = false after revoking every named capability, want the empty grant kept")
	}
	if got := registry.CapabilitiesOf("reviewer"); len(got) != 0 {
		t.Errorf("CapabilitiesOf(reviewer) = %q, want none", got)
	}
}

func TestRevokeOfTheWholeGrantDropsARoleNoOtherSourceHolds(t *testing.T) {
	t.Parallel()

	registry := goncierge.New()
	mustGrant(t, registry, "editor-plugin", "reviewer", "posts.edit")
	mustGrant(t, registry, "editor-plugin", "author", "posts.write")

	registry.Revoke("editor-plugin", "reviewer")

	if registry.Known("reviewer") {
		t.Error("Known(reviewer) = true after its only grant was revoked, want false")
	}
	if got, want := registry.Roles(), []string{"author"}; !slices.Equal(got, want) {
		t.Errorf("Roles() = %q, want the source's other role kept as %q", got, want)
	}
}

func TestRevokeOfTheWholeGrantKeepsARoleAnotherSourceHolds(t *testing.T) {
	t.Parallel()

	registry := goncierge.New()
	mustGrant(t, registry, "core", "reviewer")
	mustGrant(t, registry, "editor-plugin", "reviewer", "posts.edit")

	registry.Revoke("editor-plugin", "reviewer")

	if !registry.Known("reviewer") {
		t.Error("Known(reviewer) = false, want the role kept by the other source")
	}
	if registry.Can("reviewer", "posts.edit") {
		t.Error("Can(reviewer, posts.edit) = true after the revoke, want false")
	}
}

func TestRevokeKeepsACapabilityAnotherSourceGranted(t *testing.T) {
	t.Parallel()

	registry := goncierge.New()
	mustGrant(t, registry, "core", "editor", "posts.edit")
	mustGrant(t, registry, "editor-plugin", "editor", "posts.edit", "posts.publish")

	registry.Revoke("editor-plugin", "editor", "posts.edit")
	if !registry.Can("editor", "posts.edit") {
		t.Error("Can(editor, posts.edit) = false after one source revoked it, want the other grant kept")
	}

	registry.Revoke("editor-plugin", "editor")
	if !registry.Can("editor", "posts.edit") {
		t.Error("Can(editor, posts.edit) = false after one source's whole grant went, want the other kept")
	}
}

func TestRevokeOfAMissingGrantChangesNothing(t *testing.T) {
	t.Parallel()

	registry := ladder(t)
	before := snapshot(registry)

	registry.Revoke("events", "editor")
	registry.Revoke("events", "editor", "posts.edit")
	registry.Revoke("nobody", "admin")
	registry.Revoke("core", "ghost", "users.manage")
	registry.Revoke("core", "admin", "never.granted")

	if after := snapshot(registry); !sameSnapshot(before, after) {
		t.Errorf("registry after revoking missing grants = %v, want unchanged %v", after, before)
	}
}

func TestWithdrawOfASourceThatGrantedNothingChangesNothing(t *testing.T) {
	t.Parallel()

	registry := ladder(t)
	before := snapshot(registry)

	registry.Withdraw("nobody")
	registry.Withdraw("")

	if after := snapshot(registry); !sameSnapshot(before, after) {
		t.Errorf("registry after withdrawing an unknown source = %v, want unchanged %v", after, before)
	}
}

func TestHoldersOfListsTheRolesCarryingACapability(t *testing.T) {
	t.Parallel()

	registry := ladder(t)

	holders := registry.HoldersOf("posts.edit")

	if want := []string{"admin", "editor", "moderator"}; !slices.Equal(holders, want) {
		t.Errorf("HoldersOf(posts.edit) = %q, want %q", holders, want)
	}
	if !slices.Contains(holders, "editor") || slices.Contains(holders, "organizer") {
		t.Errorf("HoldersOf(posts.edit) = %q, want it to hold editor and not organizer", holders)
	}
}

func TestHoldersOfAnUngrantedCapabilityIsEmpty(t *testing.T) {
	t.Parallel()

	registry := ladder(t)

	if got := registry.HoldersOf("never.granted"); len(got) != 0 {
		t.Errorf("HoldersOf(never.granted) = %q, want none", got)
	}
}

func TestOutranksComparesCapabilities(t *testing.T) {
	t.Parallel()

	registry := ladder(t)

	for _, pair := range []struct {
		caller, target string
		want           bool
	}{
		{"admin", "organizer", true},
		{"admin", "moderator", true},
		{"admin", "admin", true},
		{"moderator", "organizer", true},
		{"moderator", "editor", true},
		{"organizer", "admin", false},
		{"organizer", "moderator", false},
		{"editor", "organizer", false},
		{"admin", "member", true},
		{"member", "member", true},
		{"", "member", true},
		{"ghost", "member", true},
		{"", "editor", false},
		{"ghost", "editor", false},
		{"admin", "ghost", false},
		{"admin", "", false},
	} {
		if got := registry.Outranks(pair.caller, pair.target); got != pair.want {
			t.Errorf("Outranks(%q, %q) = %v, want %v", pair.caller, pair.target, got, pair.want)
		}
	}
}

func TestGrantableListsOutrankedRolesMostCapabilitiesFirst(t *testing.T) {
	t.Parallel()

	registry := ladder(t)

	for _, caller := range []struct {
		role string
		want []string
	}{
		{"admin", []string{"admin", "moderator", "editor", "organizer", "member"}},
		{"moderator", []string{"moderator", "editor", "organizer", "member"}},
		{"editor", []string{"editor", "member"}},
		{"member", []string{"member"}},
		{"", []string{"member"}},
		{"ghost", []string{"member"}},
	} {
		if got := registry.Grantable(caller.role); !slices.Equal(got, caller.want) {
			t.Errorf("Grantable(%q) = %q, want %q", caller.role, got, caller.want)
		}
	}
}

func TestGrantableIsTheSameOnEveryCall(t *testing.T) {
	t.Parallel()

	registry := goncierge.New()
	for _, role := range []string{"delta", "alpha", "echo", "charlie", "bravo"} {
		mustGrant(t, registry, "core", role, "posts.edit")
	}
	mustGrant(t, registry, "core", "chief", "posts.edit", "users.manage")

	want := []string{"chief", "alpha", "bravo", "charlie", "delta", "echo"}
	for range 50 {
		if got := registry.Grantable("chief"); !slices.Equal(got, want) {
			t.Fatalf("Grantable(chief) = %q, want %q", got, want)
		}
	}
}

func TestZeroRegistryIsEmptyAndTakesGrants(t *testing.T) {
	t.Parallel()

	var registry goncierge.Registry

	if got := registry.Roles(); len(got) != 0 {
		t.Errorf("Roles() of the zero registry = %q, want none", got)
	}
	registry.Revoke("core", "admin")
	registry.Withdraw("core")

	if err := registry.Grant("core", "admin", "users.manage"); err != nil {
		t.Fatalf("Grant on the zero registry error = %v, want nil", err)
	}
	if !registry.Can("admin", "users.manage") {
		t.Error("Can(admin, users.manage) = false on the zero registry after a grant, want true")
	}
}

func TestEveryMethodIsSafeFromSeveralGoroutines(t *testing.T) {
	t.Parallel()

	registry := ladder(t)

	var group sync.WaitGroup
	for worker := range 8 {
		source := "plugin-" + strconv.Itoa(worker)
		group.Go(func() {
			for range 200 {
				_ = registry.Grant(source, "organizer", "events.manage", "events.publish")
				registry.Revoke(source, "organizer", "events.publish")
				_ = registry.Can("organizer", "events.publish")
				_ = registry.CapabilitiesOf("organizer")
				_ = registry.Roles()
				_ = registry.Capabilities()
				_ = registry.Known("organizer")
				_ = registry.HoldersOf("events.manage")
				_ = registry.Outranks("admin", "organizer")
				_ = registry.Grantable("moderator")
				registry.Withdraw(source)
			}
		})
	}
	group.Wait()

	if got, want := registry.CapabilitiesOf("organizer"), []string{"events.manage"}; !slices.Equal(got, want) {
		t.Errorf("CapabilitiesOf(organizer) = %q after every worker withdrew, want %q", got, want)
	}
}

func TestCheckBesideAGrantSeesTheWholeGrantOrNone(t *testing.T) {
	t.Parallel()

	registry := goncierge.New()
	whole := []string{"events.manage", "events.publish", "events.remove"}

	var group sync.WaitGroup
	group.Go(func() {
		for range 500 {
			_ = registry.Grant("events", "organizer", whole...)
			registry.Revoke("events", "organizer")
		}
	})

	for range 500 {
		got := registry.CapabilitiesOf("organizer")
		if len(got) != 0 && !slices.Equal(got, whole) {
			t.Fatalf("CapabilitiesOf(organizer) = %q beside a grant, want none or %q", got, whole)
		}
	}
	group.Wait()
}
