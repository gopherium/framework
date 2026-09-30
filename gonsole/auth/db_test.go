// SPDX-License-Identifier: Apache-2.0

package auth_test

import (
	"testing"

	"github.com/gopherium/gouncer/authkit/postgres"
	"github.com/gopherium/gouncer/authkit/postgres/testdb"
	"github.com/jackc/pgx/v5"
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

// run executes each statement on the database at address, failing the test at the first that fails.
func run(t *testing.T, address string, statements ...string) {
	t.Helper()
	conn, err := pgx.Connect(t.Context(), address)
	if err != nil {
		t.Fatalf("connecting to %s: %v", address, err)
	}
	defer func() { _ = conn.Close(t.Context()) }()
	for _, statement := range statements {
		if _, err := conn.Exec(t.Context(), statement); err != nil {
			t.Fatalf("%s: %v", statement, err)
		}
	}
}

// exists reports whether the database at address holds the table named name.
func exists(t *testing.T, address, name string) bool {
	t.Helper()
	conn, err := pgx.Connect(t.Context(), address)
	if err != nil {
		t.Fatalf("connecting to %s: %v", address, err)
	}
	defer func() { _ = conn.Close(t.Context()) }()
	var held bool
	if err := conn.QueryRow(t.Context(), "SELECT to_regclass($1) IS NOT NULL", name).Scan(&held); err != nil {
		t.Fatalf("looking for %s: %v", name, err)
	}
	return held
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
