// SPDX-License-Identifier: Apache-2.0

package sqlite_test

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"testing"
	"time"

	modernc "modernc.org/sqlite"

	"github.com/gopherium/framework/dbkit"
	"github.com/gopherium/framework/dbkit/sqlite"
	"github.com/gopherium/framework/dbkit/sqlite/sqlitetest"
)

const (
	// shareStatementTimeout is a statement timeout no statement of these tests waits out.
	shareStatementTimeout = 20 * time.Second
	// shareTransactionTimeout is a transaction timeout no transaction of these tests waits out.
	shareTransactionTimeout = 40 * time.Second
	// shareShortStatementTimeout is a statement timeout a never-ending statement runs into.
	shareShortStatementTimeout = 50 * time.Millisecond
	// shareForever is a recursive query that never ends.
	shareForever = "WITH RECURSIVE forever(n) AS (SELECT 1 UNION ALL SELECT n + 1 FROM forever) " +
		"SELECT count(*) FROM forever"
)

// shareOptions returns share options named id with every required value set and one slot per connection.
func shareOptions(id string) dbkit.ShareOptions {
	return dbkit.ShareOptions{
		ID:                 id,
		Slots:              optionsMaxConns,
		StatementTimeout:   shareStatementTimeout,
		TransactionTimeout: shareTransactionTimeout,
	}
}

// shareOn returns a SQLite share of db with opts and fails the test when it cannot.
func shareOn(t *testing.T, db *sql.DB, opts dbkit.ShareOptions) *dbkit.Share {
	t.Helper()
	share, err := dbkit.NewShare(db, dbkit.SQLite, opts)
	if err != nil {
		t.Fatalf("NewShare() error = %v, want nil", err)
	}
	return share
}

// shareExecer is a share or a transaction of one.
type shareExecer interface {
	// Exec runs a statement that returns no rows.
	Exec(ctx context.Context, query string, args ...any) (sql.Result, error)
}

// shareMustExec runs query on on and fails the test when it cannot.
func shareMustExec(t *testing.T, on shareExecer, query string, args ...any) {
	t.Helper()
	if _, err := on.Exec(t.Context(), query, args...); err != nil {
		t.Fatalf("Exec(%q) error = %v, want nil", query, err)
	}
}

// shareMustRead returns the one value query answers through share and fails the test when it cannot.
func shareMustRead[T any](t *testing.T, share *dbkit.Share, query string, args ...any) T {
	t.Helper()
	var value T
	if err := share.QueryRow(t.Context(), query, args...).Scan(&value); err != nil {
		t.Fatalf("QueryRow(%q) error = %v, want nil", query, err)
	}
	return value
}

func TestShareBindsByNumber(t *testing.T) {
	t.Parallel()

	share := shareOn(t, sqlitetest.Open(t, testOptions()), shareOptions("binding-share"))
	shareMustExec(t, share, "CREATE TABLE share_pairs (first_arg TEXT NOT NULL, second_arg TEXT NOT NULL)")

	swapped := shareMustRead[string](t, share, "SELECT $2 || '-' || $1", "first", "second")
	repeated := shareMustRead[string](t, share, "SELECT $1 || $1", "repeated")
	shareMustExec(t, share, "INSERT INTO share_pairs (second_arg, first_arg) VALUES ($2, $1)", "first", "second")

	if swapped != "second-first" {
		t.Errorf("SELECT $2 || '-' || $1 = %q, want %q", swapped, "second-first")
	}
	if repeated != "repeatedrepeated" {
		t.Errorf("SELECT $1 || $1 = %q, want %q", repeated, "repeatedrepeated")
	}
	var first, second string
	err := share.QueryRow(t.Context(), "SELECT first_arg, second_arg FROM share_pairs").Scan(&first, &second)
	if err != nil {
		t.Fatalf("QueryRow() error = %v, want nil", err)
	}
	if first != "first" || second != "second" {
		t.Errorf("the inserted row reads %q, %q, want %q, %q", first, second, "first", "second")
	}
}

func TestShareSendsTheRewrittenTextToTheDriver(t *testing.T) {
	t.Parallel()

	rewritten := errors.New("the driver received the rewritten text")
	faults := &sqlitetest.Faults{}
	faults.FailStatement("SELECT ?2 || '-' || ?1", rewritten)
	share := shareOn(t, sqlitetest.OpenWithFaults(t, testOptions(), faults), shareOptions("rewrite-share"))

	var got string
	err := share.QueryRow(t.Context(), "SELECT $2 || '-' || $1", "first", "second").Scan(&got)

	if !errors.Is(err, rewritten) {
		t.Errorf("QueryRow() = %q, %v, want the fault set on the rewritten text", got, err)
	}
}

func TestShareUpsertReturning(t *testing.T) {
	t.Parallel()

	share := shareOn(t, sqlitetest.Open(t, testOptions()), shareOptions("upsert-share"))
	shareMustExec(t, share, "CREATE TABLE share_pages (id INTEGER PRIMARY KEY, slug TEXT NOT NULL UNIQUE, "+
		"title TEXT NOT NULL)")
	upsert := "INSERT INTO share_pages (slug, title) VALUES ($1, $2) " +
		"ON CONFLICT (slug) DO UPDATE SET title = excluded.title RETURNING id"

	first := shareMustRead[int64](t, share, upsert, "repeated-slug", "first title")
	second := shareMustRead[int64](t, share, upsert, "repeated-slug", "second title")

	if first != second {
		t.Errorf("the second upsert returned id %d, want the first id %d", second, first)
	}
	title := shareMustRead[string](t, share, "SELECT title FROM share_pages WHERE id = $1", first)
	if title != "second title" {
		t.Errorf("title = %q, want %q", title, "second title")
	}
	if rows := shareMustRead[int64](t, share, "SELECT count(*) FROM share_pages"); rows != 1 {
		t.Errorf("count(*) = %d, want 1", rows)
	}
}

func TestACastAfterAPlaceholderFailsLoudly(t *testing.T) {
	t.Parallel()

	share := shareOn(t, sqlitetest.Open(t, testOptions()), shareOptions("cast-share"))
	var got string

	err := share.QueryRow(t.Context(), "SELECT $1::text", "cast value").Scan(&got)

	var driverErr *modernc.Error
	if !errors.As(err, &driverErr) || got != "" {
		t.Errorf("SELECT $1::text = %q, %v, want no value and SQLite's own error", got, err)
	}
}

func TestShareBeginTakesTheWriteLock(t *testing.T) {
	t.Parallel()

	opts := testOptions()
	opts.BusyTimeout = rulesShortBusyTimeout
	db := sqlitetest.Open(t, opts)
	holder := shareOn(t, db, shareOptions("holder-share"))
	waiter := shareOn(t, db, shareOptions("waiter-share"))
	held, err := holder.Begin(t.Context())
	if err != nil {
		t.Fatalf("Begin() error = %v, want nil", err)
	}
	began := time.Now()

	tx, err := waiter.Begin(t.Context())

	waited := time.Since(began)
	if err == nil {
		_ = tx.Rollback()
		_ = held.Rollback()
		t.Fatal("Begin() from a second share error = nil, want busy while the first share holds the write lock")
	}
	if got := sqlite.Classify(err); !errors.Is(got, dbkit.ErrBusy) {
		t.Errorf("Classify(%v) = %v, want the busy class", err, got)
	}
	if waited < rulesShortBusyTimeout {
		t.Errorf("Begin() answered after %v, want at least the busy timeout %v", waited, rulesShortBusyTimeout)
	}
	if err := held.Rollback(); err != nil {
		t.Fatalf("Rollback() error = %v, want nil", err)
	}
	after, err := waiter.Begin(t.Context())
	if err != nil {
		t.Fatalf("Begin() after the first share rolled back error = %v, want nil", err)
	}
	if err := after.Rollback(); err != nil {
		t.Errorf("Rollback() error = %v, want nil", err)
	}
}

// shareForeverKinds runs shareForever through the share as an Exec, a Query read to its end and a QueryRow.
var shareForeverKinds = []struct {
	name string
	run  func(ctx context.Context, share *dbkit.Share) error
}{
	{"Exec", func(ctx context.Context, share *dbkit.Share) error {
		_, err := share.Exec(ctx, shareForever)
		return err
	}},
	{"Query", func(ctx context.Context, share *dbkit.Share) error {
		rows, err := share.Query(ctx, shareForever)
		if err != nil {
			return err
		}
		for rows.Next() {
		}
		err = rows.Err()
		_ = rows.Close()
		return err
	}},
	{"QueryRow", func(ctx context.Context, share *dbkit.Share) error {
		var n int64
		return share.QueryRow(ctx, shareForever).Scan(&n)
	}},
}

func TestAStatementPastItsDeadlineStops(t *testing.T) {
	t.Parallel()

	db := sqlitetest.Open(t, testOptions())
	short := shareOptions("deadline-share")
	short.StatementTimeout = shareShortStatementTimeout
	share := shareOn(t, db, short)
	after := shareOn(t, db, shareOptions("after-share"))
	for _, kind := range shareForeverKinds {
		t.Run(kind.name, func(t *testing.T) {
			t.Parallel()

			began := time.Now()

			err := kind.run(t.Context(), share)

			took := time.Since(began)
			if !errors.Is(err, context.DeadlineExceeded) || errors.Is(err, dbkit.ErrShareFull) {
				t.Errorf("%s of a never-ending query error = %v, want context.DeadlineExceeded from the run", kind.name, err)
			}
			if took < shareShortStatementTimeout {
				t.Errorf("%s answered after %v, want at least the statement timeout %v", kind.name, took,
					shareShortStatementTimeout)
			}
			if got := shareMustRead[int64](t, after, "SELECT $1", 1); got != 1 {
				t.Errorf("SELECT $1 after the deadline = %d, want 1", got)
			}
		})
	}
}

// shareMustKeepTheRules fails the test unless connection i of a handle from testOptions carries every rule.
func shareMustKeepTheRules(t *testing.T, i int, conn *sql.Conn) {
	t.Helper()
	pragmas := map[string]int64{
		"PRAGMA foreign_keys": 1,
		"PRAGMA busy_timeout": optionsBusyTimeout.Milliseconds(),
		"PRAGMA temp_store":   2,
		"PRAGMA cache_size":   -optionsCacheSize,
		"PRAGMA synchronous":  1,
	}
	for query, want := range pragmas {
		var got int64
		if err := conn.QueryRowContext(t.Context(), query).Scan(&got); err != nil || got != want {
			t.Errorf("%s on connection %d = %d, %v, want %d", query, i, got, err, want)
		}
	}
	var mode string
	if err := conn.QueryRowContext(t.Context(), "PRAGMA journal_mode").Scan(&mode); err != nil || mode != "wal" {
		t.Errorf("PRAGMA journal_mode on connection %d = %q, %v, want wal", i, mode, err)
	}
	if _, err := conn.ExecContext(t.Context(), "PRAGMA writable_schema=ON"); err != nil {
		t.Fatalf("PRAGMA writable_schema=ON on connection %d error = %v, want nil", i, err)
	}
	_, err := conn.ExecContext(t.Context(), "UPDATE sqlite_schema SET sql = sql WHERE name = 'share_rules'")
	if err == nil || !strings.Contains(err.Error(), "may not be modified") {
		t.Errorf("an edit of the schema table on connection %d error = %v, want it refused", i, err)
	}
}

func TestConnectionRulesHoldAfterShareStatements(t *testing.T) {
	t.Parallel()

	db := sqlitetest.Open(t, testOptions())
	share := shareOn(t, db, shareOptions("rules-share"))
	tx, err := share.Begin(t.Context())
	if err != nil {
		t.Fatalf("Begin() error = %v, want nil", err)
	}
	shareMustExec(t, tx, "CREATE TABLE share_rules (a INTEGER NOT NULL)")
	shareMustExec(t, tx, "INSERT INTO share_rules VALUES ($1)", 1)
	if err := tx.Commit(); err != nil {
		t.Fatalf("Commit() error = %v, want nil", err)
	}
	held := make([]*dbkit.Rows, optionsMaxConns)
	for i := range held {
		if held[i], err = share.Query(t.Context(), "SELECT a FROM share_rules WHERE a = $1", 1); err != nil {
			t.Fatalf("Query() error = %v, want nil", err)
		}
	}
	if got := db.Stats().OpenConnections; got != optionsMaxConns {
		t.Fatalf("Stats().OpenConnections = %d while the share holds %d result sets, want %d", got, len(held),
			optionsMaxConns)
	}
	for _, rows := range held {
		if err := rows.Close(); err != nil {
			t.Fatalf("Close() error = %v, want nil", err)
		}
	}

	for i, conn := range rulesConns(t, db) {
		shareMustKeepTheRules(t, i, conn)
	}
	if got := db.Stats().MaxOpenConnections; got != optionsMaxConns {
		t.Errorf("Stats().MaxOpenConnections = %d, want the cap %d unchanged", got, optionsMaxConns)
	}
}
