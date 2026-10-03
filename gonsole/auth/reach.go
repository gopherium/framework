// SPDX-License-Identifier: Apache-2.0

package auth

import (
	"context"
	"fmt"
	"slices"

	"github.com/gopherium/gouncer"
	"github.com/gopherium/gouncer/authkit/postgres"

	"github.com/gopherium/framework/gonsole"
)

// reach is the acting account of one call and what each role carries, the zero value refusing nothing.
type reach struct {
	actor   gouncer.User
	carried map[string][]string
}

// withReach runs use over the account store and the reach of the account the call acts as.
func withReach(ctx context.Context, call gonsole.Call, cfg Config, roles Roles,
	use func(store *postgres.UserStore, within reach) error) error {
	if cfg.Capability == "" {
		return withStore(ctx, call, func(store *postgres.UserStore) error {
			return use(store, reach{})
		})
	}
	actor, err := actorOf(call)
	if err != nil {
		return err
	}
	return withStore(ctx, call, func(store *postgres.UserStore) error {
		user, err := acting(ctx, store, actor)
		if err != nil {
			return err
		}
		return use(store, reach{actor: user, carried: roles.Capabilities})
	})
}

// gives refuses a role that carries a capability the acting account's role lacks.
func (r reach) gives(role string) error {
	return r.lacking("the role "+role, role)
}

// changes refuses an account whose role carries a capability the acting account's role lacks.
func (r reach) changes(held gouncer.User) error {
	return r.lacking(fmt.Sprintf("the role %s of %s", held.Role, held.Email), held.Role)
}

// moves refuses a change to an account beyond its reach and the acting account changing its own role.
func (r reach) moves(held gouncer.User) error {
	if err := r.changes(held); err != nil {
		return err
	}
	if held.ID == r.actor.ID {
		return fmt.Errorf("the account %s cannot change its own role", held.Email)
	}
	return nil
}

// sets refuses the acting account disabling itself and a change to an account beyond its reach.
func (r reach) sets(held gouncer.User, disabled bool) error {
	if disabled && held.ID == r.actor.ID {
		return fmt.Errorf("the account %s cannot disable itself", held.Email)
	}
	return r.changes(held)
}

// lacking returns the refusal naming the first capability of role the acting account's role lacks, nil for none.
func (r reach) lacking(subject, role string) error {
	for _, capability := range r.carried[role] {
		if !slices.Contains(r.carried[r.actor.Role], capability) {
			return fmt.Errorf("%s carries %s, which the account %s lacks", subject, capability, r.actor.Email)
		}
	}
	return nil
}
