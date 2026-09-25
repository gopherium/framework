// SPDX-License-Identifier: Apache-2.0

package auth

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/gopherium/gouncer"
	"github.com/gopherium/gouncer/authkit/postgres"

	"github.com/gopherium/framework/gonsole"
)

// SetRole returns account:role, which sets one account's role.
func SetRole(cfg Config) gonsole.Command {
	return gonsole.Command{
		Name:       "account:role",
		Summary:    "set one account's role",
		Args:       []string{"email", "role"},
		Writes:     true,
		Capability: cfg.Capability,
		Run: func(ctx context.Context, call gonsole.Call) error {
			return setRole(ctx, call, cfg)
		},
	}
}

// setRole gives the account the call names the role it names, once the role is known.
func setRole(ctx context.Context, call gonsole.Call, cfg Config) error {
	email, role := address(call.Args[0]), call.Args[1]
	roles, err := cfg.Roles(ctx, call)
	if err != nil {
		return err
	}
	if err := known(roles, role); err != nil {
		return err
	}
	return withStore(ctx, call, func(store *postgres.UserStore) error {
		held, err := store.UserByEmail(ctx, email)
		if err != nil {
			return err
		}
		if !call.Apply {
			_, err := fmt.Fprintf(call.Stdout, "would set %s to %s\n", held.Email, role)
			return err
		}
		if err := store.SetUserRole(ctx, held.ID, role, roles.Privileged); err != nil {
			return uncovered(held.Email, err)
		}
		_, err = fmt.Fprintf(call.Stdout, "set %s to %s\n", held.Email, role)
		return err
	})
}

// address returns typed as gouncer stores an address, trimmed and in lower case.
func address(typed string) string {
	return strings.ToLower(strings.TrimSpace(typed))
}

// uncovered returns err, worded for an operator when it refuses to remove the last privileged account.
func uncovered(email string, err error) error {
	if errors.Is(err, gouncer.ErrLastPrivileged) {
		return fmt.Errorf("%s is the last enabled privileged account", email)
	}
	return err
}
