// SPDX-License-Identifier: Apache-2.0

package pgtest_test

import (
	"strconv"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/peterldowns/pgtestdb"

	"github.com/gopherium/framework/dbkit/postgres/pgtest"
)

// urlNeedsEscaping holds every character a URL user, password or path must escape, and the quotes SQL must.
const urlNeedsEscaping = ":@/?#% +'\"\\"

func TestURLKeepsEveryPartPgxReads(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		cfg  pgtestdb.Config
	}{
		{"plain parts", pgtestdb.Config{Host: "db", Port: "5432", User: "plain_user", Password: "plain_password",
			Database: "plain_database", Options: "sslmode=disable"}},
		{"parts that need escaping", pgtestdb.Config{Host: "db", Port: "5432", User: "user" + urlNeedsEscaping,
			Password: "password" + urlNeedsEscaping, Database: "database" + urlNeedsEscaping, Options: "sslmode=disable"}},
		{"an IPv6 host", pgtestdb.Config{Host: "::1", Port: "5434", User: "plain_user", Password: "plain_password",
			Database: "plain_database", Options: "sslmode=disable"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()

			got, err := pgconn.ParseConfig(pgtest.URL(c.cfg))

			if err != nil {
				t.Fatal("ParseConfig(URL()) failed, want the address to parse")
			}
			if got.Host != c.cfg.Host || strconv.Itoa(int(got.Port)) != c.cfg.Port {
				t.Errorf("URL() reads back host %q and port %d, want %q and %s", got.Host, got.Port, c.cfg.Host, c.cfg.Port)
			}
			if got.User != c.cfg.User || got.Password != c.cfg.Password || got.Database != c.cfg.Database {
				t.Errorf("URL() reads back user %q, a password of %d bytes and database %q, want %q, %d bytes and %q",
					got.User, len(got.Password), got.Database, c.cfg.User, len(c.cfg.Password), c.cfg.Database)
			}
			if got.TLSConfig != nil {
				t.Error("URL() lost the option sslmode=disable")
			}
		})
	}
}

func TestAPasswordThatNeedsEscapingConnectsThroughURL(t *testing.T) {
	t.Parallel()

	cfg := serverConfig(t)
	cfg.User = serverName("pgtest role" + urlNeedsEscaping)
	cfg.Password = "password" + urlNeedsEscaping
	cfg.Database = serverName("pgtest database" + urlNeedsEscaping)
	serverExec(t, "CREATE ROLE %I LOGIN PASSWORD %L", cfg.User, cfg.Password)
	t.Cleanup(func() { serverExec(t, "DROP ROLE %I", cfg.User) })
	serverExec(t, "CREATE DATABASE %I OWNER %I", cfg.Database, cfg.User)
	t.Cleanup(func() { serverExec(t, "DROP DATABASE %I", cfg.Database) })

	conn, err := pgx.Connect(t.Context(), pgtest.URL(cfg))

	if err != nil {
		t.Fatal("Connect(URL()) failed, want a connection as the role that needs escaping")
	}
	defer func() { _ = conn.Close(t.Context()) }()
	var user, database string
	if err := conn.QueryRow(t.Context(), "SELECT current_user, current_database()").Scan(&user, &database); err != nil {
		t.Fatalf("QueryRow() error = %v, want nil", err)
	}
	if user != cfg.User || database != cfg.Database {
		t.Errorf("connected as %q to %q, want %q to %q", user, database, cfg.User, cfg.Database)
	}
}
