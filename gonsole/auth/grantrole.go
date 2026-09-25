// SPDX-License-Identifier: Apache-2.0

package auth

import (
	"context"
	"flag"
	"fmt"

	"github.com/gopherium/gouncer/authkit/postgres"

	"github.com/gopherium/framework/gonsole"
)

// grantName is the full name of the command that gives a role to every account holding none.
const grantName = "account:grant-role"

// GrantRole returns account:grant-role, which gives a known role to every account holding none.
func GrantRole(cfg Config) gonsole.Command {
	return gonsole.Command{
		Name:       grantName,
		Summary:    "give a role to every account holding none",
		Writes:     true,
		Migrates:   true,
		Capability: cfg.Capability,
		Flags: func(fs *flag.FlagSet) {
			fs.String("role", "", "`role` to give every account holding none")
		},
		Run: func(ctx context.Context, call gonsole.Call) error {
			return grant(ctx, call, cfg)
		},
	}
}

// grant gives the role the call names to every account holding none, once the role is known.
func grant(ctx context.Context, call gonsole.Call, cfg Config) error {
	if err := missing(grantName, call, needed{"role", "role"}); err != nil {
		return err
	}
	role := call.Flags["role"]
	roles, err := cfg.Roles(ctx, call)
	if err != nil {
		return err
	}
	if err := known(roles, role); err != nil {
		return err
	}
	return withStore(ctx, call, func(store *postgres.UserStore) error {
		return grantIn(ctx, call, store, role)
	})
}

// grantIn gives role to every account in store holding none, a dry run until the call applies.
func grantIn(ctx context.Context, call gonsole.Call, store *postgres.UserStore, role string) error {
	if !call.Apply {
		count, err := roleless(ctx, store)
		if err != nil {
			return err
		}
		_, err = fmt.Fprintf(call.Stdout, "would grant %s to %s\n", role, accounts(count))
		return err
	}
	granted, err := store.GrantRoleToRoleless(ctx, role)
	if err != nil {
		return err
	}
	_, err = fmt.Fprintf(call.Stdout, "granted %s to %s\n", role, accounts(granted))
	return err
}

// roleless returns how many accounts in store hold no role.
func roleless(ctx context.Context, store *postgres.UserStore) (int64, error) {
	users, err := store.ListUsers(ctx)
	if err != nil {
		return 0, err
	}
	var count int64
	for _, user := range users {
		if user.Role == "" {
			count++
		}
	}
	return count, nil
}

// accounts names a count of accounts in plain English.
func accounts(count int64) string {
	if count == 1 {
		return "1 account"
	}
	return fmt.Sprintf("%d accounts", count)
}
