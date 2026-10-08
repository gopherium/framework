// SPDX-License-Identifier: Apache-2.0

package pgtest_test

import (
	"cmp"
	"context"
	"crypto/rand"
	"net/url"
	"os"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	_ "github.com/jackc/pgx/v5/stdlib"
	"github.com/peterldowns/pgtestdb"

	"github.com/gopherium/framework/dbkit/postgres/pgtest"
)

// serverFallback is the test server the tests reach when DBKIT_TEST_POSTGRES_URL is empty.
const serverFallback = "postgres://postgres:postgres@localhost:5434/postgres?sslmode=disable"

// serverAddress returns the address of the test server.
func serverAddress() string {
	return cmp.Or(os.Getenv("DBKIT_TEST_POSTGRES_URL"), serverFallback)
}

// serverConfig returns the pgtestdb configuration of the test server, its user, password and database escaped.
func serverConfig(t *testing.T) pgtestdb.Config {
	t.Helper()
	cfg, err := pgtest.ConfigOf(serverAddress())
	if err != nil {
		t.Fatalf("the test server address is refused: %v", err)
	}
	return cfg
}

func TestServerConfigKeepsTheEscapedPartsOfTheServerAddress(t *testing.T) {
	server := url.URL{
		Scheme:   "postgres",
		User:     url.UserPassword("user"+urlNeedsEscaping, "password"+urlNeedsEscaping),
		Host:     "db:5434",
		Path:     "/database" + urlNeedsEscaping,
		RawQuery: "sslmode=disable",
	}
	t.Setenv("DBKIT_TEST_POSTGRES_URL", server.String())
	password, _ := server.User.Password()
	database := strings.TrimPrefix(server.Path, "/")

	got, err := pgconn.ParseConfig(serverConfig(t).URL())

	if err != nil {
		t.Fatal("ParseConfig() of the test server configuration failed, want it to parse")
	}
	if got.User != server.User.Username() || got.Password != password || got.Database != database {
		t.Errorf("serverConfig() reads back user %q, a password of %d bytes and database %q, want %q, %d bytes and %q",
			got.User, len(got.Password), got.Database, server.User.Username(), len(password), database)
	}
}

// serverAddressWith returns the address of the test server with the option key set to value.
func serverAddressWith(t *testing.T, key, value string) string {
	t.Helper()
	server, err := url.Parse(serverAddress())
	if err != nil {
		t.Fatal("the test server address cannot be parsed")
	}
	query := server.Query()
	query.Set(key, value)
	server.RawQuery = query.Encode()
	return server.String()
}

// serverDatabase returns the name of the database address points at.
func serverDatabase(t *testing.T, address string) string {
	t.Helper()
	parsed, err := url.Parse(address)
	if err != nil {
		t.Fatal("a database address does not parse, want a URL")
	}
	return strings.TrimPrefix(parsed.Path, "/")
}

// serverName returns prefix followed by random lowercase letters and digits.
func serverName(prefix string) string {
	return prefix + strings.ToLower(rand.Text())
}

// serverDropTemplateAtEnd drops the template pgtestdb cut the instance database from once the test ends.
func serverDropTemplateAtEnd(t *testing.T, instance string) {
	t.Helper()
	template, _, found := strings.Cut(instance, "_inst_")
	if !found {
		t.Fatalf("%q is not a pgtestdb instance database", instance)
	}
	t.Cleanup(func() {
		serverExec(t, "ALTER DATABASE %I IS_TEMPLATE false", template)
		serverExec(t, "DROP DATABASE %I", template)
	})
}

// serverHolds reports whether the database at address answers true to lookup with args.
func serverHolds(t *testing.T, address, lookup string, args ...any) bool {
	t.Helper()
	conn, err := pgx.Connect(t.Context(), address)
	if err != nil {
		t.Fatal("connect to a test database failed, want a connection")
	}
	defer func() { _ = conn.Close(context.Background()) }()
	var holds bool
	if err := conn.QueryRow(t.Context(), lookup, args...).Scan(&holds); err != nil {
		t.Fatalf("%s: %v", lookup, err)
	}
	return holds
}

// serverExec runs on the test server the statement that PostgreSQL's format builds from format and args.
func serverExec(t *testing.T, format string, args ...string) {
	t.Helper()
	ctx := context.Background()
	conn, err := pgx.Connect(ctx, serverAddress())
	if err != nil {
		t.Fatalf("connect to the test server: %v", err)
	}
	defer func() { _ = conn.Close(ctx) }()
	var statement string
	build := "SELECT format($1::text, VARIADIC $2::text[])"
	if err := conn.QueryRow(ctx, build, format, args).Scan(&statement); err != nil {
		t.Fatalf("build %q: %v", format, err)
	}
	if _, err := conn.Exec(ctx, statement); err != nil {
		t.Fatalf("%s: %v", statement, err)
	}
}
