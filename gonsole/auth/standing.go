// SPDX-License-Identifier: Apache-2.0

package auth

import (
	"context"
	"fmt"

	"github.com/gopherium/gouncer/authkit/postgres"

	"github.com/gopherium/framework/gonsole"
)

// Disable returns account:disable, which disables one account.
func Disable(cfg Config) gonsole.Command {
	return standing(cfg, "account:disable", "disable one account", "disable", true)
}

// Enable returns account:enable, which enables one disabled account.
func Enable(cfg Config) gonsole.Command {
	return standing(cfg, "account:enable", "enable one disabled account", "enable", false)
}

// standing returns the command called name that sets whether one account is disabled, verb naming the change.
func standing(cfg Config, name, summary, verb string, disabled bool) gonsole.Command {
	return gonsole.Command{
		Name:       name,
		Summary:    summary,
		Args:       []string{"email"},
		Writes:     true,
		Capability: cfg.Capability,
		Run: func(ctx context.Context, call gonsole.Call) error {
			return setStanding(ctx, call, cfg, verb, disabled)
		},
	}
}

// setStanding sets whether the account the call names is disabled, never leaving no privileged account enabled.
func setStanding(ctx context.Context, call gonsole.Call, cfg Config, verb string, disabled bool) error {
	email := address(call.Args[0])
	roles, err := cfg.Roles(ctx, call)
	if err != nil {
		return err
	}
	return withStore(ctx, call, func(store *postgres.UserStore) error {
		held, err := store.UserByEmail(ctx, email)
		if err != nil {
			return err
		}
		if !call.Apply {
			_, err := fmt.Fprintf(call.Stdout, "would %s %s\n", verb, held.Email)
			return err
		}
		if err := store.SetUserDisabledUnderCover(ctx, held.ID, disabled, roles.Privileged); err != nil {
			return uncovered(held.Email, err)
		}
		_, err = fmt.Fprintf(call.Stdout, "%sd %s\n", verb, held.Email)
		return err
	})
}
