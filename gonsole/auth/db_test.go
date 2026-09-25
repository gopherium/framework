// SPDX-License-Identifier: Apache-2.0

package auth_test

import (
	"testing"

	"github.com/gopherium/gouncer/authkit/postgres"
	"github.com/gopherium/gouncer/authkit/postgres/testdb"
	"github.com/jackc/pgx/v5/pgxpool"
	_ "github.com/jackc/pgx/v5/stdlib"
	"github.com/peterldowns/pgtestdb"
)

// migrated returns the address of a fresh database holding the account schema.
func migrated(t *testing.T) string {
	t.Helper()
	return pgtestdb.Custom(t, testdb.Config(), testdb.Migrator()).URL()
}

// empty returns the address of a fresh database holding no schema.
func empty(t *testing.T) string {
	t.Helper()
	return pgtestdb.Custom(t, testdb.Config(), pgtestdb.NoopMigrator{}).URL()
}

// storeAt returns an account store over the database at address, closed when the test ends.
func storeAt(t *testing.T, address string) *postgres.UserStore {
	t.Helper()
	pool, err := pgxpool.New(t.Context(), address)
	if err != nil {
		t.Fatalf("opening %s: %v", address, err)
	}
	t.Cleanup(pool.Close)
	return postgres.NewUserStore(pool)
}
