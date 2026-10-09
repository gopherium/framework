// SPDX-License-Identifier: Apache-2.0

package auth

import "github.com/gopherium/gouncer"

// RolesFrom returns the role vocabulary a registry holds, with the privileged roles the program names.
func RolesFrom(registry interface {
	Roles() []string
	CapabilitiesOf(role string) []string
}, privileged gouncer.Roles) Roles {
	known := registry.Roles()
	capabilities := make(map[string][]string, len(known))
	for _, role := range known {
		capabilities[role] = registry.CapabilitiesOf(role)
	}
	return Roles{Known: known, Privileged: privileged, Capabilities: capabilities}
}
