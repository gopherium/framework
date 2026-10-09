// SPDX-License-Identifier: Apache-2.0

package auth

import (
	"context"
	"errors"
	"fmt"
	"slices"

	"github.com/gopherium/gouncer"
	"github.com/gopherium/gouncer/authkit/postgres"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/gopherium/framework/gonsole"
)

// Authorize returns the check that refuses an acting account that may not run a command naming capability.
func Authorize(cfg Config) func(ctx context.Context, call gonsole.Call, capability string) error {
	return func(ctx context.Context, call gonsole.Call, capability string) error {
		if _, err := cfg.recordTimeout(call.Env); err != nil {
			return err
		}
		roles, err := cfg.Roles(ctx, call)
		if err != nil {
			return err
		}
		return withPool(ctx, call, func(pool *pgxpool.Pool) error {
			if err := recordsHeld(ctx, postgresRecords{db: pool}); err != nil {
				return err
			}
			return may(ctx, postgres.NewUserStore(pool), roles, address(call.Actor), capability)
		})
	}
}

// acting returns the account at the address actor, an error naming the address when no account holds it.
func acting(ctx context.Context, store Accounts, actor string) (gouncer.User, error) {
	user, err := store.UserByEmail(ctx, actor)
	if errors.Is(err, gouncer.ErrUserNotFound) {
		return gouncer.User{}, fmt.Errorf("no account answers to %s", actor)
	}
	return user, err
}

// may refuses actor unless an enabled account it activated holds a role carrying capability.
func may(ctx context.Context, store Accounts, roles Roles, actor, capability string) error {
	user, err := acting(ctx, store, actor)
	switch {
	case err != nil:
		return err
	case user.Disabled:
		return fmt.Errorf("the account %s is disabled", actor)
	case !user.Confirmed:
		return fmt.Errorf("the account %s was never activated", actor)
	case user.Role == "":
		return fmt.Errorf("the account %s holds no role, so it lacks %s", actor, capability)
	case !slices.Contains(roles.Capabilities[user.Role], capability):
		return fmt.Errorf("the account %s holds the role %s, which lacks %s", actor, user.Role, capability)
	}
	return nil
}
