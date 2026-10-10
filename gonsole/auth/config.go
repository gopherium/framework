// SPDX-License-Identifier: Apache-2.0

package auth

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

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
	// RecordTimeout bounds storing one record when the COMMAND_RECORD_TIMEOUT setting is empty.
	RecordTimeout time.Duration
	// RecordsLimit is how many records account:records lists when the COMMAND_RECORDS_LIMIT setting is empty.
	RecordsLimit int
	// Stores builds a call's stores and the release of what it opened, if any, nil for PostgreSQL at the database setting.
	Stores func(ctx context.Context, call gonsole.Call) (Stores, func(context.Context) error, error)
}

// Roles is one program's role vocabulary.
type Roles struct {
	// Known lists every role an account may hold.
	Known []string
	// Privileged lists the roles one enabled account must always keep.
	Privileged gouncer.Roles
	// Capabilities maps each role onto the capabilities it carries, a role left out carrying none.
	Capabilities map[string][]string
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

// withStore runs use over the account store of the call and releases it after.
func (c Config) withStore(ctx context.Context, call gonsole.Call, use func(store Accounts) error) error {
	return c.withStores(ctx, call, func(stores Stores) error {
		return use(stores.Accounts)
	})
}

// withStores runs use over the stores of the call and releases them after, even when use panics.
func (c Config) withStores(ctx context.Context, call gonsole.Call, use func(stores Stores) error) (err error) {
	if c.Stores == nil {
		return withPool(ctx, call, func(pool *pgxpool.Pool) error {
			return use(Stores{Accounts: postgres.NewUserStore(pool), Records: postgresRecords{db: pool}})
		})
	}
	stores, release, err := c.Stores(ctx, call)
	if err != nil {
		return err
	}
	if release == nil {
		return use(stores)
	}
	defer func() {
		if released := release(context.WithoutCancel(ctx)); released != nil {
			err = errors.Join(err, released)
		}
	}()
	return use(stores)
}

// withPool runs use over a pool of the program's database and closes the pool after.
func withPool(ctx context.Context, call gonsole.Call, use func(pool *pgxpool.Pool) error) error {
	address, err := call.DatabaseURL()
	if err != nil {
		return err
	}
	pool, err := pgxpool.New(ctx, address)
	if err != nil {
		return fmt.Errorf("open the database: %w", err)
	}
	defer pool.Close()
	return use(pool)
}
