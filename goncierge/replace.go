// SPDX-License-Identifier: Apache-2.0

package goncierge

// Replace swaps every grant the source made for the roles given, in one step.
func (r *Registry) Replace(source string, roles []Role) error {
	if err := checkRoles(source, roles); err != nil {
		return err
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	delete(r.grants, source)
	r.add(source, roles)
	r.index()

	return nil
}
