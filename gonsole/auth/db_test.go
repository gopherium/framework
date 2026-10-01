// SPDX-License-Identifier: Apache-2.0

package auth_test

import (
	"context"
	"testing"
	"time"

	"github.com/gopherium/gouncer/authkit/postgres"
	"github.com/gopherium/gouncer/authkit/postgres/testdb"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	_ "github.com/jackc/pgx/v5/stdlib"
	"github.com/peterldowns/pgtestdb"
	"github.com/pressly/goose/v3/lock"
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
	return found(t, address, "SELECT to_regclass($1) IS NOT NULL", name)
}

// found reports whether lookup answers true for values on the database at address.
func found(t *testing.T, address, lookup string, values ...any) bool {
	t.Helper()
	conn, err := pgx.Connect(t.Context(), address)
	if err != nil {
		t.Fatalf("connecting to %s: %v", address, err)
	}
	defer func() { _ = conn.Close(t.Context()) }()
	var held bool
	if err := conn.QueryRow(t.Context(), lookup, values...).Scan(&held); err != nil {
		t.Fatalf("looking for %v: %v", values, err)
	}
	return held
}

// holdMigrationLock returns a connection holding goose's migration lock on the database at address.
func holdMigrationLock(t *testing.T, address string) *pgx.Conn {
	t.Helper()
	conn, err := pgx.Connect(t.Context(), address)
	if err != nil {
		t.Fatalf("connecting to %s: %v", address, err)
	}
	t.Cleanup(func() { _ = conn.Close(context.Background()) })
	if _, err := conn.Exec(t.Context(), "SELECT pg_advisory_lock($1)", lock.DefaultLockID); err != nil {
		t.Fatalf("holding the migration lock: %v", err)
	}
	return conn
}

// awaitSession returns once a session other than holder has column like pattern, failing if ran ends first.
func awaitSession(t *testing.T, address string, holder *pgx.Conn, ran <-chan error, column, pattern string) {
	t.Helper()
	lookup := "SELECT EXISTS (SELECT FROM pg_stat_activity WHERE datname = current_database()" +
		" AND pid NOT IN (pg_backend_pid(), $1) AND " + column + " LIKE $2)"
	for !found(t, address, lookup, int32(holder.PgConn().PID()), pattern) {
		select {
		case err := <-ran:
			t.Fatalf("Run() = %v before a session had %s like %s", err, column, pattern)
		case <-time.After(10 * time.Millisecond):
		}
	}
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
