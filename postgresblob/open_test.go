// SPDX-License-Identifier: Apache-2.0

package postgresblob_test

import (
	"context"
	"crypto/rand"
	"net/url"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/gopherium/framework/postgresblob"
)

// serverExec runs statement on the database postgres of the test server.
func serverExec(t testing.TB, statement string) {
	t.Helper()
	conn, err := pgx.Connect(context.Background(), server)
	if err != nil {
		t.Fatalf("connect to the test server: %v", err)
	}
	defer func() { _ = conn.Close(context.Background()) }()
	if _, err := conn.Exec(context.Background(), statement); err != nil {
		t.Fatalf("%s: %v", statement, err)
	}
}

// asciiDatabase returns the address of a new database whose encoding is SQL_ASCII, dropped when the test ends.
func asciiDatabase(t *testing.T) string {
	t.Helper()
	name := "postgresblob_ascii_" + strings.ToLower(rand.Text())
	ident := pgx.Identifier{name}.Sanitize()
	serverExec(t, "CREATE DATABASE "+ident+" ENCODING 'SQL_ASCII' LOCALE 'C' TEMPLATE template0")
	t.Cleanup(func() { serverExec(t, "DROP DATABASE "+ident+" WITH (FORCE)") })
	address, err := url.Parse(server)
	if err != nil {
		t.Fatalf("parse the test server address: %v", err)
	}
	address.Path = "/" + name
	return address.String()
}

func TestOpenBucketRefusesOptionsOutOfRange(t *testing.T) {
	t.Parallel()
	pool := freshDB(t).pool
	largest := strconv.Itoa(postgresblob.MaxChunkSize)
	tests := map[string]struct {
		opts postgresblob.Options
		want []string
	}{
		"a negative chunk size": {opts: postgresblob.Options{ChunkSize: -1}, want: []string{"ChunkSize", largest}},
		"a chunk size past MaxChunkSize": {
			opts: postgresblob.Options{ChunkSize: postgresblob.MaxChunkSize + 1},
			want: []string{"ChunkSize", largest},
		},
		"a negative size cap": {opts: postgresblob.Options{MaxSize: -1}, want: []string{"MaxSize"}},
		"a negative cleanup timeout": {
			opts: postgresblob.Options{CleanupTimeout: -time.Second},
			want: []string{"CleanupTimeout"},
		},
	}

	for name, tt := range tests {
		b, err := postgresblob.OpenBucket(t.Context(), pool, &tt.opts)
		if !mentions(err, tt.want...) {
			t.Errorf("OpenBucket() with %s = %v, %v, want an error that names %q", name, b, err, tt.want)
		}
	}
}

func TestOpenBucketTakesTheLargestChunkSize(t *testing.T) {
	t.Parallel()
	pool := freshDB(t).pool

	b, err := postgresblob.OpenBucket(t.Context(), pool,
		&postgresblob.Options{Schema: testSchema, ChunkSize: postgresblob.MaxChunkSize})

	if err != nil {
		t.Fatalf("OpenBucket() with a chunk size of MaxChunkSize = %v, want a bucket", err)
	}
	_ = b.Close()
}

func TestOpenBucketNeverClosesItsPool(t *testing.T) {
	t.Parallel()
	pool := freshDB(t).pool
	b, err := postgresblob.OpenBucket(t.Context(), pool, &postgresblob.Options{Schema: testSchema})
	if err != nil {
		t.Fatalf("OpenBucket() = %v, want a bucket", err)
	}

	if err := b.Close(); err != nil {
		t.Fatalf("Close() = %v, want nil", err)
	}

	if err := pool.Ping(t.Context()); err != nil {
		t.Errorf("Ping() on the pool after the bucket closed = %v, want nil", err)
	}
}

func TestOpenBucketAndSweepRefuseANilPool(t *testing.T) {
	t.Parallel()

	b, openErr := postgresblob.OpenBucket(t.Context(), nil, nil)
	n, sweepErr := postgresblob.Sweep(t.Context(), nil, postgresblob.SweepOptions{OlderThan: time.Hour, Limit: 1})

	if !mentions(openErr, "pool") {
		t.Errorf("OpenBucket() with a nil pool = %v, %v, want an error that names the pool", b, openErr)
	}
	if !mentions(sweepErr, "pool") {
		t.Errorf("Sweep() with a nil pool = %d, %v, want an error that names the pool", n, sweepErr)
	}
}

func TestOpenBucketRefusesADatabaseWhoseEncodingIsNotUTF8(t *testing.T) {
	t.Parallel()
	pool := poolOn(t, asciiDatabase(t))

	b, err := postgresblob.OpenBucket(t.Context(), pool, &postgresblob.Options{Schema: testSchema})

	if !mentions(err, "SQL_ASCII", "UTF8") {
		t.Errorf("OpenBucket() on a SQL_ASCII database = %v, %v, want an error that names SQL_ASCII and UTF8", b, err)
	}
}
