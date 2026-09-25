// SPDX-License-Identifier: Apache-2.0

package auth

import (
	"context"
	"fmt"
	"slices"
	"strings"

	"github.com/gopherium/gouncer"
	"github.com/gopherium/gouncer/authkit/postgres"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/gopherium/framework/gonsole"
)

// Config is what a program hands its account commands.
type Config struct {
	// Roles returns the program's role vocabulary.
	Roles func(ctx context.Context, call gonsole.Call) (Roles, error)
	// Capability names the capability every account write requires, empty for none.
	Capability string
}

// Roles is one program's role vocabulary.
type Roles struct {
	// Known lists every role an account may hold.
	Known []string
	// Privileged lists the roles one enabled account must always keep.
	Privileged gouncer.Roles
}

// known returns a misuse naming the roles of roles when role is not one of them.
func known(roles Roles, role string) error {
	if slices.Contains(roles.Known, role) {
		return nil
	}
	return gonsole.Misuse(fmt.Errorf("unknown role %q, want %s", role, alternatives(roles.Known)))
}

// alternatives joins names as a list read aloud, such as a, b or c.
func alternatives(names []string) string {
	switch len(names) {
	case 0:
		return "a role the program declares"
	case 1:
		return names[0]
	}
	last := len(names) - 1
	return strings.Join(names[:last], ", ") + " or " + names[last]
}

// needed is one flag a command requires and the placeholder its help page shows.
type needed struct {
	flag  string
	value string
}

// missing returns a misuse naming the first needed flag the call leaves blank.
func missing(command string, call gonsole.Call, flags ...needed) error {
	for _, want := range flags {
		if strings.TrimSpace(call.Flags[want.flag]) == "" {
			return gonsole.Misuse(fmt.Errorf("%s wants -%s <%s>", command, want.flag, want.value))
		}
	}
	return nil
}

// withStore runs use over the account store of the program's database and closes its pool after.
func withStore(ctx context.Context, call gonsole.Call, use func(store *postgres.UserStore) error) error {
	address, err := call.DatabaseURL()
	if err != nil {
		return err
	}
	pool, err := pgxpool.New(ctx, address)
	if err != nil {
		return fmt.Errorf("open the database: %w", err)
	}
	defer pool.Close()
	return use(postgres.NewUserStore(pool))
}
