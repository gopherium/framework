// SPDX-License-Identifier: Apache-2.0

package goncierge

import (
	"errors"
	"fmt"
	"slices"
	"strings"
)

// ErrOutsidePrefix reports a new capability whose name does not start with its source and a dot.
var ErrOutsidePrefix = errors.New("goncierge: new capability outside the source's prefix")

// ErrUnknownAdmin reports an administrator role that no other source created.
var ErrUnknownAdmin = errors.New("goncierge: unknown admin role")

// Rules are the declaration rules a program sets once for every source that declares roles.
type Rules struct {
	// Admin names the role that also receives every capability a source declares, empty for none.
	Admin string
}

// Declare replaces the source's grants with its roles under the rules, refusing a new capability outside its prefix.
func Declare(r *Registry, rules Rules, source string, roles []Role) error {
	if err := checkDeclaration(rules, source, roles); err != nil {
		return err
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	if rules.Admin != "" && !r.createdBesides(source, rules.Admin) {
		return fmt.Errorf("%w: %+q", ErrUnknownAdmin, rules.Admin)
	}
	if err := r.checkPrefix(source, roles); err != nil {
		return err
	}
	delete(r.grants, source)
	r.add(source, withAdmin(rules, roles))
	r.index()

	return nil
}

// checkDeclaration reports the first source, role, capability or administrator name the declaration rules refuse.
func checkDeclaration(rules Rules, source string, roles []Role) error {
	if err := checkRoles(source, roles); err != nil {
		return err
	}
	if strings.Contains(source, ".") || !namePattern.MatchString(source) {
		return fmt.Errorf("%w for a source: %+q", ErrInvalidName, source)
	}
	if rules.Admin == "" {
		return nil
	}

	return checkName("role", rules.Admin)
}

// withAdmin returns the roles plus the rules' administrator carrying every capability the roles carry.
func withAdmin(rules Rules, roles []Role) []Role {
	var every []string
	for _, role := range roles {
		every = append(every, role.Capabilities...)
	}
	if rules.Admin == "" || len(every) == 0 {
		return roles
	}

	return append(slices.Clone(roles), Role{Name: rules.Admin, Capabilities: every})
}

// checkPrefix reports the first capability of the roles that is outside the source's prefix and new to the others.
func (r *Registry) checkPrefix(source string, roles []Role) error {
	prefix := source + "."
	for _, role := range roles {
		for _, capability := range role.Capabilities {
			if !strings.HasPrefix(capability, prefix) && !r.grantedBesides(source, capability) {
				return fmt.Errorf("%w %+q: %+q", ErrOutsidePrefix, prefix, capability)
			}
		}
	}

	return nil
}

// createdBesides reports whether a source other than the one named grants the role.
func (r *Registry) createdBesides(source, role string) bool {
	for other, granted := range r.grants {
		if _, ok := granted[role]; ok && other != source {
			return true
		}
	}

	return false
}

// grantedBesides reports whether a source other than the one named grants the capability to any role.
func (r *Registry) grantedBesides(source, capability string) bool {
	for other, granted := range r.grants {
		if other == source {
			continue
		}
		for _, capabilities := range granted {
			if _, ok := capabilities[capability]; ok {
				return true
			}
		}
	}

	return false
}
