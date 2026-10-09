// SPDX-License-Identifier: Apache-2.0

package auth_test

import (
	"context"
	"maps"
	"slices"
	"testing"

	"github.com/gopherium/gouncer"

	"github.com/gopherium/framework/gonsole"
	"github.com/gopherium/framework/gonsole/auth"
	"github.com/gopherium/framework/gonsole/testkit"
)

// registry is a stand-in role registry answering from a fixed table of role to capabilities.
type registry map[string][]string

// Roles returns every role the table holds, sorted.
func (r registry) Roles() []string {
	return slices.Sorted(maps.Keys(r))
}

// CapabilitiesOf returns a copy of the capabilities the role carries, none for a role the table lacks.
func (r registry) CapabilitiesOf(role string) []string {
	return slices.Clone(r[role])
}

// capableRegistry holds the table of capable, with author declared carrying nothing.
var capableRegistry = registry{
	"admin":  capable.Capabilities["admin"],
	"editor": capable.Capabilities["editor"],
	"author": nil,
}

func TestRolesFromHoldsTheRegistryRolesAndTheGivenPrivilegedRoles(t *testing.T) {
	t.Parallel()

	roles := auth.RolesFrom(capableRegistry, gouncer.Roles{"admin"})

	if !slices.Equal(roles.Known, []string{"admin", "author", "editor"}) {
		t.Errorf("Known = %v, want every role the registry holds, in its order", roles.Known)
	}
	if !slices.Equal(roles.Privileged, gouncer.Roles{"admin"}) {
		t.Errorf("Privileged = %v, want the roles the program named", roles.Privileged)
	}
	for _, role := range []string{"admin", "author", "editor", "archivist"} {
		if got, want := roles.Capabilities[role], capableRegistry.CapabilitiesOf(role); !slices.Equal(got, want) {
			t.Errorf("Capabilities[%s] = %v, want %v as the registry answers", role, got, want)
		}
	}
}

func TestRolesFromAuthorizesAsTheHandBuiltVocabularyOfTheSameTable(t *testing.T) {
	t.Parallel()

	built := checked()
	built.Roles = func(context.Context, gonsole.Call) (auth.Roles, error) {
		return auth.RolesFrom(capableRegistry, capable.Privileged), nil
	}
	for _, actor := range []string{"admin@example.com", "editor@example.com", "author@example.com"} {
		t.Run(actor, func(t *testing.T) {
			t.Parallel()

			address := recorded(t)

			byHand := testkit.Run(t, authorizing(address, checked(), nil, command("report:purge")),
				"", "report:purge", "-as", actor)
			fromRegistry := testkit.Run(t, authorizing(address, built, nil, command("report:purge")),
				"", "report:purge", "-as", actor)

			if fromRegistry.Code != byHand.Code || fromRegistry.Stdout != byHand.Stdout ||
				fromRegistry.Stderr != byHand.Stderr {
				t.Errorf("from the registry %d %q %q, want %d %q %q as by hand", fromRegistry.Code,
					fromRegistry.Stdout, fromRegistry.Stderr, byHand.Code, byHand.Stdout, byHand.Stderr)
			}
		})
	}
}
