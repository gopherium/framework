// SPDX-License-Identifier: Apache-2.0

package goncierge

import (
	"cmp"
	"errors"
	"fmt"
	"maps"
	"regexp"
	"slices"
	"sync"
)

// ErrEmptySource reports that a grant names no source.
var ErrEmptySource = errors.New("goncierge: empty source")

// ErrInvalidName reports that a role or capability name breaks the name rule.
var ErrInvalidName = errors.New("goncierge: invalid name")

// namePattern is the name rule every role and capability follows.
var namePattern = regexp.MustCompile(`^[a-z][a-z0-9._-]{0,63}$`)

// set is a set of names.
type set = map[string]struct{}

// Registry holds the capabilities each role carries, as granted by named sources.
type Registry struct {
	mu      sync.RWMutex
	grants  map[string]map[string]set
	carried map[string]set
}

// Role is one role a source grants, with the capabilities the source gives it.
type Role struct {
	Name         string
	Capabilities []string
}

// New returns an empty registry.
func New() *Registry { return &Registry{} }

// Grant records that the source gives the role the capabilities, creating the role when it is new.
func (r *Registry) Grant(source, role string, capabilities ...string) error {
	roles := []Role{{Name: role, Capabilities: capabilities}}
	if err := checkRoles(source, roles); err != nil {
		return err
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	r.add(source, roles)
	r.index()

	return nil
}

// Revoke removes the capabilities from the source's grant to the role, or the whole grant when none is named.
func (r *Registry) Revoke(source, role string, capabilities ...string) {
	r.mu.Lock()
	defer r.mu.Unlock()

	granted, ok := r.grants[source][role]
	if !ok {
		return
	}
	for _, capability := range capabilities {
		delete(granted, capability)
	}
	if len(capabilities) == 0 {
		delete(r.grants[source], role)
	}
	r.index()
}

// Withdraw removes every grant the source made, and every role only the source created.
func (r *Registry) Withdraw(source string) {
	r.mu.Lock()
	defer r.mu.Unlock()

	delete(r.grants, source)
	r.index()
}

// Can reports whether the role carries the capability, an empty role never carrying one.
func (r *Registry) Can(role, capability string) bool {
	r.mu.RLock()
	defer r.mu.RUnlock()

	_, ok := r.carried[role][capability]

	return ok
}

// CapabilitiesOf returns the capabilities the role carries, sorted, from every source.
func (r *Registry) CapabilitiesOf(role string) []string {
	r.mu.RLock()
	defer r.mu.RUnlock()

	return slices.Sorted(maps.Keys(r.carried[role]))
}

// Roles returns every role any source created, sorted.
func (r *Registry) Roles() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()

	return slices.Sorted(maps.Keys(r.carried))
}

// Capabilities returns every capability any source granted, sorted.
func (r *Registry) Capabilities() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()

	every := set{}
	for _, capabilities := range r.carried {
		maps.Copy(every, capabilities)
	}

	return slices.Sorted(maps.Keys(every))
}

// Known reports whether any source created the role.
func (r *Registry) Known(role string) bool {
	r.mu.RLock()
	defer r.mu.RUnlock()

	_, ok := r.carried[role]

	return ok
}

// HoldersOf returns the roles that carry the capability, sorted.
func (r *Registry) HoldersOf(capability string) []string {
	r.mu.RLock()
	defer r.mu.RUnlock()

	var holders []string
	for role, capabilities := range r.carried {
		if _, ok := capabilities[capability]; ok {
			holders = append(holders, role)
		}
	}
	slices.Sort(holders)

	return holders
}

// Outranks reports whether the caller carries every capability the target carries.
func (r *Registry) Outranks(caller, target string) bool {
	r.mu.RLock()
	defer r.mu.RUnlock()

	carried, ok := r.carried[target]

	return ok && covers(r.carried[caller], carried)
}

// Grantable returns the roles the caller outranks, the one carrying most capabilities first.
func (r *Registry) Grantable(caller string) []string {
	r.mu.RLock()
	defer r.mu.RUnlock()

	held := r.carried[caller]
	sizes := map[string]int{}
	for role, carried := range r.carried {
		if covers(held, carried) {
			sizes[role] = len(carried)
		}
	}

	return slices.SortedFunc(maps.Keys(sizes), func(a, b string) int {
		return cmp.Or(cmp.Compare(sizes[b], sizes[a]), cmp.Compare(a, b))
	})
}

// add merges the roles into the source's grants.
func (r *Registry) add(source string, roles []Role) {
	for _, role := range roles {
		if r.grants == nil {
			r.grants = map[string]map[string]set{}
		}
		if r.grants[source] == nil {
			r.grants[source] = map[string]set{}
		}
		granted := r.grants[source][role.Name]
		if granted == nil {
			granted = set{}
			r.grants[source][role.Name] = granted
		}
		for _, capability := range role.Capabilities {
			granted[capability] = struct{}{}
		}
	}
}

// index rebuilds the capabilities each role carries from every source.
func (r *Registry) index() {
	carried := map[string]set{}
	for _, granted := range r.grants {
		for role, capabilities := range granted {
			if carried[role] == nil {
				carried[role] = set{}
			}
			maps.Copy(carried[role], capabilities)
		}
	}
	r.carried = carried
}

// checkRoles reports the first empty source or name outside the name rule among the roles.
func checkRoles(source string, roles []Role) error {
	if source == "" {
		return ErrEmptySource
	}
	for _, role := range roles {
		if err := checkName("role", role.Name); err != nil {
			return err
		}
		for _, capability := range role.Capabilities {
			if err := checkName("capability", capability); err != nil {
				return err
			}
		}
	}

	return nil
}

// checkName reports a name outside the name rule, naming its kind.
func checkName(kind, name string) error {
	if !namePattern.MatchString(name) {
		return fmt.Errorf("%w for a %s: %+q", ErrInvalidName, kind, name)
	}

	return nil
}

// covers reports whether held contains every name in target.
func covers(held, target set) bool {
	for name := range target {
		if _, ok := held[name]; !ok {
			return false
		}
	}

	return true
}
