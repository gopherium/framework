// SPDX-License-Identifier: Apache-2.0

package auth

import (
	"context"
	"flag"

	"github.com/gopherium/gouncer/authkit"
	"github.com/gopherium/gouncer/authkit/postgres"

	"github.com/gopherium/framework/gonsole"
)

// createName is the full name of the command that creates one account under a role.
const createName = "account:create-admin"

// CreateAdmin returns account:create-admin, which creates one account under a known role.
func CreateAdmin(cfg Config) gonsole.Command {
	return gonsole.Command{
		Name:     createName,
		Summary:  "create an account under a role",
		Migrates: true,
		Flags: func(fs *flag.FlagSet) {
			fs.String("email", "", "`address` of the new account")
			fs.String("name", "", "display `name` of the new account")
			fs.String("role", "", "`role` the new account starts under")
		},
		Run: func(ctx context.Context, call gonsole.Call) error {
			return create(ctx, call, cfg)
		},
	}
}

// create creates the account the call names under its role, once the role is known.
func create(ctx context.Context, call gonsole.Call, cfg Config) error {
	err := missing(createName, call, needed{"email", "address"}, needed{"name", "name"}, needed{"role", "role"})
	if err != nil {
		return err
	}
	roles, err := cfg.Roles(ctx, call)
	if err != nil {
		return err
	}
	if err := known(roles, call.Flags["role"]); err != nil {
		return err
	}
	return withStore(ctx, call, func(store *postgres.UserStore) error {
		return authkit.CreateAdmin(ctx, store, call.Flags["email"], call.Flags["name"], call.Flags["role"],
			call.Stdin, call.Stdout)
	})
}
