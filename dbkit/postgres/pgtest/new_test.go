// SPDX-License-Identifier: Apache-2.0

package pgtest_test

import (
	"context"
	"database/sql"
	"fmt"
	"runtime"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/jackc/pgx/v5"
	"github.com/peterldowns/pgtestdb"

	"github.com/gopherium/framework/dbkit/postgres"
	"github.com/gopherium/framework/dbkit/postgres/pgtest"
)

const (
	// newSecret is a part of every password the address tests write that no message may hold.
	newSecret = "secretword"
	// newSecretUser is a user the address tests write that no message may hold.
	newSecretUser = "secretuser"
	// newSecretHost is a loopback host the address tests write that no message may hold.
	newSecretHost = "127.0.0.1"
	// newSecretDatabase is a database the address tests write that no message may hold.
	newSecretDatabase = "secretdatabase"
	// newKeptOption is the application_name the server address sets and every database of New keeps.
	newKeptOption = "pgtest_kept_option"
	// newBareAt is the message of an address with a bare @ before its host.
	newBareAt = "dbkit: refused database address: the user or password holds a bare @, write each one as %40"
	// newUnparsable is the message of an address pgx cannot parse.
	newUnparsable = "dbkit: refused database address: pgx cannot parse the address or one of its settings"
	// newNotURL is the message of an address New cannot read as a URL.
	newNotURL = "dbkit: refused database address: pgtest needs a postgres:// or postgresql:// address it can parse"
	// newColonHost is the message of an address whose host holds a colon.
	newColonHost = "dbkit: refused database address: pgtest needs a host with no colon, " +
		"so an IPv6 literal must be given as a host name"
	// newQueryPassword is the message of an address whose query sets password.
	newQueryPassword = "dbkit: refused database address: the query sets password, " +
		"write the server password before the @ instead"
	// newQuerySSLPassword is the message of an address whose query sets sslpassword.
	newQuerySSLPassword = "dbkit: refused database address: the query sets sslpassword, set PGSSLPASSWORD instead"
)

// newMigrator returns a migrator of its own hash whose template holds new_rows with one row.
func newMigrator() pgtestdb.Migrator {
	migrations := postgres.Migrations{
		Table: "new_versions",
		FS: fstest.MapFS{"00001_new_rows.sql": &fstest.MapFile{
			Data: []byte("-- +goose Up\nCREATE TABLE new_rows (a integer NOT NULL);\nINSERT INTO new_rows VALUES (1);\n"),
		}},
	}
	return pgtest.Migrator(serverName("new-hash-"), func(ctx context.Context, db *sql.DB) error {
		return postgres.Migrate(ctx, db, migrations)
	})
}

// newRows returns the number of rows of new_rows on the database at address.
func newRows(t *testing.T, address string) int {
	t.Helper()
	conn, err := pgx.Connect(t.Context(), address)
	if err != nil {
		t.Fatal("connect to a database New returned failed, want a connection")
	}
	defer func() { _ = conn.Close(context.Background()) }()
	var n int
	if err := conn.QueryRow(t.Context(), "SELECT count(*) FROM new_rows").Scan(&n); err != nil {
		t.Fatalf("count the rows of new_rows: %v", err)
	}
	return n
}

func TestNewGivesEachTestAFreshMigratedDatabase(t *testing.T) {
	t.Parallel()

	migrator := newMigrator()
	address := serverAddressWith(t, "application_name", newKeptOption)

	first := pgtest.New(t, address, migrator)
	serverDropTemplateAtEnd(t, serverDatabase(t, first))
	second := pgtest.New(t, address, migrator)

	if first == second {
		t.Fatal("New() returned one address twice, want a fresh database each time")
	}
	conn, err := pgx.Connect(t.Context(), first)
	if err != nil {
		t.Fatal("connect to the first database failed, want a connection")
	}
	var application string
	if err := conn.QueryRow(t.Context(), "SELECT current_setting('application_name')").Scan(&application); err != nil {
		t.Fatalf("read the application_name of the first database: %v", err)
	}
	if application != newKeptOption {
		t.Errorf("the first database runs with application_name %q, want %q from the server address",
			application, newKeptOption)
	}
	if _, err := conn.Exec(t.Context(), "INSERT INTO new_rows VALUES (2)"); err != nil {
		t.Fatalf("insert into the first database: %v", err)
	}
	if err := conn.Close(t.Context()); err != nil {
		t.Fatalf("Close() error = %v, want nil", err)
	}
	if got := newRows(t, first); got != 2 {
		t.Errorf("the first database holds %d rows, want the migrated row and the inserted one", got)
	}
	if got := newRows(t, second); got != 1 {
		t.Errorf("the second database holds %d rows, want only the migrated row", got)
	}
}

// newFatal is a testing.TB that records what the test would print and ends its goroutine on a fatal call.
type newFatal struct {
	testing.TB
	// message is what Fatal or Fatalf was called with.
	message string
	// output is every message Fatal, Fatalf and Logf were called with.
	output []string
}

// Fatal records args and ends the calling goroutine.
func (f *newFatal) Fatal(args ...any) {
	f.fail(fmt.Sprint(args...))
}

// Fatalf records the message of format and args and ends the calling goroutine.
func (f *newFatal) Fatalf(format string, args ...any) {
	f.fail(fmt.Sprintf(format, args...))
}

// Logf records the message of format and args.
func (f *newFatal) Logf(format string, args ...any) {
	f.output = append(f.output, fmt.Sprintf(format, args...))
}

// fail records message as the fatal message and ends the calling goroutine.
func (f *newFatal) fail(message string) {
	f.message = message
	f.output = append(f.output, message)
	runtime.Goexit()
}

// newRun returns the recorder New printed through once it ended on address.
func newRun(t *testing.T, address string) *newFatal {
	t.Helper()
	recorder := &newFatal{TB: t}
	done := make(chan struct{})
	go func() {
		defer close(done)
		pgtest.New(recorder, address, newMigrator())
	}()
	<-done
	return recorder
}

func TestNewRefusesAnAddressItCannotUse(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name    string
		address string
		want    string
	}{
		{"a bare at", "postgres://u:" + newSecret + "@ss@localhost:5434/postgres", newBareAt},
		{"a keyword and value address", "host=localhost user=u password=" + newSecret, newNotURL},
		{"an address with another scheme", "mysql://u:" + newSecret + "@localhost/postgres", newUnparsable},
		{"a URL that does not parse", "postgres://u:" + newSecret + "@localhost:port/postgres", newUnparsable},
		{"an IPv6 literal host", "postgres://u:" + newSecret + "@[::1]:5434/postgres", newColonHost},
		{"an IPv6 literal host with no port", "postgres://u:" + newSecret + "@[::1]/postgres", newColonHost},
		{"an IPv6 literal host with a zone", "postgres://u:" + newSecret + "@[fe80::1%25eth0]:5434/postgres", newColonHost},
		{"an IPv6 address with no brackets", "postgres://u:" + newSecret + "@::1:5434/postgres", newColonHost},
		{"a list of hosts with ports", "postgres://u:" + newSecret + "@localhost:5434,localhost:5434/postgres",
			newColonHost},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()

			recorder := newRun(t, c.address)

			if recorder.message != c.want {
				t.Errorf("New() failed the test with %q, want %q", recorder.message, c.want)
			}
			if strings.Contains(strings.Join(recorder.output, "\n"), newSecret) {
				t.Error("New() printed part of the password, want none")
			}
		})
	}
}

func TestNewRefusesAQueryKeyThatMovesTheTestDatabases(t *testing.T) {
	t.Parallel()

	server := "postgres://" + newSecretUser + ":" + newSecret + "@" + newSecretHost + ":1/" + newSecretDatabase +
		"?sslmode=disable&"
	for _, c := range []struct {
		key   string
		value string
	}{
		{"user", "moved_user"}, {"host", "moved.example.com"}, {"port", "2"}, {"dbname", "moved_database"},
		{"database", "moved_database"},
	} {
		t.Run("a query "+c.key, func(t *testing.T) {
			t.Parallel()

			recorder := newRun(t, server+c.key+"="+c.value)

			want := "dbkit: refused database address: the query sets " + c.key + ", give it before the ? instead"
			if recorder.message != want {
				t.Errorf("New() failed the test with %q, want %q", recorder.message, want)
			}
			if strings.Contains(strings.Join(recorder.output, "\n"), newSecret) {
				t.Error("New() printed part of the password, want none")
			}
		})
	}
}

func TestNewPrintsNoPartOfAnAddressWithAQueryPassword(t *testing.T) {
	t.Parallel()

	server := "postgres://" + newSecretUser + "@" + newSecretHost + ":1/" + newSecretDatabase + "?"
	cases := []struct {
		name  string
		query string
		want  string
	}{
		{"a query password and a bad sslmode", "password=" + newSecret + "&sslmode=disabled", newUnparsable},
		{"a query sslpassword and a bad sslmode", "sslpassword=" + newSecret + "&sslmode=disabled", newUnparsable},
		{"a query password and a bad connect_timeout", "password=" + newSecret + "&connect_timeout=soon",
			newUnparsable},
		{"a query password", "sslmode=disable&password=" + newSecret, newQueryPassword},
		{"a query password with an escaped key", "sslmode=disable&pass%77ord=" + newSecret, newQueryPassword},
		{"a query sslpassword", "sslmode=disable&sslpassword=" + newSecret, newQuerySSLPassword},
	}
	parts := []struct {
		value string
		name  string
	}{
		{newSecret, "the password"},
		{newSecretUser, "the user"},
		{newSecretHost, "the host"},
		{newSecretDatabase, "the database"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()

			recorder := newRun(t, server+c.query)

			if recorder.message != c.want {
				t.Errorf("New() failed the test with %q, want %q", recorder.message, c.want)
			}
			printed := strings.Join(recorder.output, "\n")
			for _, part := range parts {
				if strings.Contains(printed, part.value) {
					t.Errorf("New() printed %s of the address, want no part of it", part.name)
				}
			}
		})
	}
}
