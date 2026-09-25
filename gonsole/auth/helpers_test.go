// SPDX-License-Identifier: Apache-2.0

package auth_test

import (
	"context"
	"io"
	"sync"
	"testing"

	"github.com/gopherium/gouncer"
	"github.com/gopherium/gouncer/authkit/postgres"

	"github.com/gopherium/framework/gonsole"
	"github.com/gopherium/framework/gonsole/auth"
	"github.com/gopherium/framework/gonsole/testkit"
)

// vocabulary is the role vocabulary the programs in these tests declare.
var vocabulary = auth.Roles{Known: []string{"admin", "editor", "author"}, Privileged: gouncer.Roles{"admin"}}

// rolesRead counts how often a program's account commands read its roles.
type rolesRead struct {
	mu    sync.Mutex
	count int
}

// config returns a config that answers vocabulary, counts each read in reads and names capability.
func config(reads *rolesRead, capability string) auth.Config {
	return auth.Config{
		Roles: func(context.Context, gonsole.Call) (auth.Roles, error) {
			reads.mu.Lock()
			defer reads.mu.Unlock()
			reads.count++
			return vocabulary, nil
		},
		Capability: capability,
	}
}

// program returns a program called myapp over the database at address, offering commands.
func program(address string, commands ...gonsole.Command) gonsole.Program {
	return gonsole.Program{
		Name:       "myapp",
		Env:        gonsole.Env{Prefix: "MYAPP_", Getenv: testkit.Getenv(map[string]string{"MYAPP_DATABASE_URL": address})},
		Database:   "DATABASE_URL",
		Migrations: []gonsole.Step{auth.Migration()},
		Commands:   commands,
		Authorize:  func(context.Context, gonsole.Call, string) error { return nil },
		Record:     func(context.Context, gonsole.Call, string) error { return nil },
	}
}

// account returns the account the store holds at email, failing the test when it holds none.
func account(t *testing.T, store *postgres.UserStore, email string) gouncer.User {
	t.Helper()
	held, err := store.UserByEmail(t.Context(), email)
	if err != nil {
		t.Fatalf("UserByEmail(%s) = %v", email, err)
	}
	return held
}

// seeded returns the address of a migrated database holding one enabled account per role of vocabulary.
func seeded(t *testing.T) string {
	t.Helper()
	address := migrated(t)
	var accounts []auth.Account
	for _, role := range vocabulary.Known {
		accounts = append(accounts, auth.Account{
			Email: role + "@example.com", Name: role + " account", Password: demoPassword, Role: role,
		})
	}
	if err := auth.EnsureAccounts(t.Context(), storeAt(t, address), accounts, io.Discard); err != nil {
		t.Fatalf("seeding: %v", err)
	}
	return address
}
