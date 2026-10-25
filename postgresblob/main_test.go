// SPDX-License-Identifier: Apache-2.0

package postgresblob_test

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"fmt"
	"io"
	"net/url"
	"os"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
	"gocloud.dev/blob"

	"github.com/gopherium/framework/dbkit/postgres/pgtest"
	"github.com/gopherium/framework/postgresblob"
)

const (
	// serverVariable names the environment variable that holds the address of the test server.
	serverVariable = "DBKIT_TEST_POSTGRES_URL"
	// testSchema is the schema that holds the tables of every fresh test database.
	testSchema = "files"
	// unmigratedSchema is a schema no test database holds.
	unmigratedSchema = "never_migrated"
	// poolSize is the most connections the pool of one test database holds.
	poolSize = 4
	// liveRows counts the live rows of the key its one argument names.
	liveRows = "SELECT count(*) FROM files.objects WHERE key = $1 AND state = 'live'"
	// pendingRows counts the pending rows of every key.
	pendingRows = "SELECT count(*) FROM files.objects WHERE state = 'pending'"
	// keyRows counts the rows of any state of the key its one argument names.
	keyRows = "SELECT count(*) FROM files.objects WHERE key = $1"
	// tableLookup answers whether the table its one argument names exists.
	tableLookup = "SELECT to_regclass($1) IS NOT NULL"
)

// server is the address of the test server.
var server string

// TestMain runs the tests once serverVariable holds an address pgtest takes.
func TestMain(m *testing.M) {
	server = os.Getenv(serverVariable)
	if err := pgtest.CheckAddress(server); err != nil {
		_, _ = fmt.Fprintf(os.Stderr,
			"postgresblob: the tests need %s, the address of a PostgreSQL server pgtest takes: %v\n", serverVariable, err)
		os.Exit(1)
	}
	os.Exit(m.Run())
}

// migrator builds the template of every fresh test database, with the tables in testSchema.
var migrator = pgtest.Migrator(templateHash(), func(ctx context.Context, db *sql.DB) error {
	return postgresblob.Migrate(ctx, db, testSchema)
})

// emptyMigrator builds a template that holds no table.
var emptyMigrator = pgtest.Migrator("postgresblob-empty", func(context.Context, *sql.DB) error { return nil })

// templateHash returns the hash of the statements that build the template of migrator.
func templateHash() string {
	sum := sha256.Sum256([]byte(strings.Join(postgresblob.MigrationStatements(testSchema), "\n")))
	return "postgresblob-" + hex.EncodeToString(sum[:])
}

// testDB is one fresh test database of one test.
type testDB struct {
	// t is the test the database belongs to.
	t *testing.T
	// address is the address of the database.
	address string
	// pool reaches the database.
	pool *pgxpool.Pool
}

// freshDB returns a fresh database that holds the tables in testSchema.
func freshDB(t *testing.T) testDB {
	t.Helper()
	address := pgtest.New(t, server, migrator)
	return testDB{t: t, address: address, pool: poolOn(t, address)}
}

// emptyDB returns a fresh database that holds no table.
func emptyDB(t *testing.T) testDB {
	t.Helper()
	address := pgtest.New(t, server, emptyMigrator)
	return testDB{t: t, address: address, pool: poolOn(t, address)}
}

// poolOn returns a pool of poolSize connections on address, closed when the test ends.
func poolOn(t testing.TB, address string) *pgxpool.Pool {
	t.Helper()
	config, err := pgxpool.ParseConfig(address)
	if err != nil {
		t.Fatalf("parse the test database address: %v", err)
	}
	config.MaxConns = poolSize
	pool, err := pgxpool.NewWithConfig(context.Background(), config)
	if err != nil {
		t.Fatalf("open a pool on the test database: %v", err)
	}
	t.Cleanup(pool.Close)
	return pool
}

// open returns a bucket on db with opts, in testSchema when opts names no schema, closed when the test ends.
func (db testDB) open(opts postgresblob.Options) *blob.Bucket {
	db.t.Helper()
	if opts.Schema == "" {
		opts.Schema = testSchema
	}
	b, err := postgresblob.OpenBucket(db.t.Context(), db.pool, &opts)
	if err != nil {
		db.t.Fatalf("OpenBucket() = %v, want a bucket", err)
	}
	db.t.Cleanup(func() { _ = b.Close() })
	return b
}

// count returns the count lookup answers for args on db.
func (db testDB) count(lookup string, args ...any) int {
	db.t.Helper()
	var n int
	if err := db.pool.QueryRow(context.Background(), lookup, args...).Scan(&n); err != nil {
		db.t.Fatalf("%s: %v", lookup, err)
	}
	return n
}

// hasTable reports whether db holds the table named table.
func (db testDB) hasTable(table string) bool {
	db.t.Helper()
	var held bool
	if err := db.pool.QueryRow(context.Background(), tableLookup, table).Scan(&held); err != nil {
		db.t.Fatalf("%s: %v", tableLookup, err)
	}
	return held
}

// addressWith returns address with each query parameter of params set.
func addressWith(t testing.TB, address string, params url.Values) string {
	t.Helper()
	parsed, err := url.Parse(address)
	if err != nil {
		t.Fatalf("parse the test database address: %v", err)
	}
	query := parsed.Query()
	for key, values := range params {
		query[key] = values
	}
	parsed.RawQuery = query.Encode()
	return parsed.String()
}

// keysOf returns the keys a listing of b under prefix answers, in its order.
func keysOf(t testing.TB, b *blob.Bucket, prefix string) []string {
	t.Helper()
	var keys []string
	iter := b.List(&blob.ListOptions{Prefix: prefix})
	for {
		obj, err := iter.Next(context.Background())
		if err == io.EOF {
			return keys
		}
		if err != nil {
			t.Fatalf("List().Next() = %v, want an object or io.EOF", err)
		}
		keys = append(keys, obj.Key)
	}
}
