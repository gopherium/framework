// SPDX-License-Identifier: Apache-2.0

package sqlite_test

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/gopherium/framework/dbkit"
	"github.com/gopherium/framework/dbkit/sqlite"
)

const (
	// rulesRounds is how many read-then-write transactions each writer runs.
	rulesRounds = 100
	// rulesShortBusyTimeout is a busy timeout a test waits out on purpose.
	rulesShortBusyTimeout = 30 * time.Millisecond
	// rulesJournalSizeLimit is a journal size limit a test sets.
	rulesJournalSizeLimit = 3145728
)

// rulesOpen opens a fresh file with opts and returns the handle.
func rulesOpen(t *testing.T, opts sqlite.Options) *sql.DB {
	t.Helper()
	address, _ := testAddress(t)
	return mustOpen(t, address, opts)
}

// rulesExec runs query on db and fails the test when it cannot.
func rulesExec(t *testing.T, db *sql.DB, query string) {
	t.Helper()
	if _, err := db.ExecContext(t.Context(), query); err != nil {
		t.Fatalf("ExecContext(%q) error = %v, want nil", query, err)
	}
}

// rulesConns takes every connection of a full pool, each opened after the ones before, and closes them at cleanup.
func rulesConns(t *testing.T, db *sql.DB) []*sql.Conn {
	t.Helper()
	conns := make([]*sql.Conn, optionsMaxConns)
	for i := range conns {
		conn, err := db.Conn(t.Context())
		if err != nil {
			t.Fatalf("Conn() error = %v, want nil", err)
		}
		t.Cleanup(func() { _ = conn.Close() })
		conns[i] = conn
	}
	if got := db.Stats().OpenConnections; got != optionsMaxConns {
		t.Fatalf("Stats().OpenConnections = %d, want %d fresh connections", got, optionsMaxConns)
	}
	return conns
}

// rulesRead reads one value of query on every connection of a full pool and returns the values.
func rulesRead[T any](t *testing.T, db *sql.DB, query string) []T {
	t.Helper()
	conns := rulesConns(t, db)
	values := make([]T, len(conns))
	for i, conn := range conns {
		if err := conn.QueryRowContext(t.Context(), query).Scan(&values[i]); err != nil {
			t.Fatalf("QueryRowContext(%q) on connection %d error = %v, want nil", query, i, err)
		}
	}
	return values
}

// rulesMustAll fails the test unless every value equals want.
func rulesMustAll[T comparable](t *testing.T, query string, values []T, want T) {
	t.Helper()
	for i, got := range values {
		if got != want {
			t.Errorf("%s on connection %d = %v, want %v", query, i, got, want)
		}
	}
}

func TestForeignKeysAreOn(t *testing.T) {
	t.Parallel()

	db := rulesOpen(t, testOptions())

	rulesMustAll(t, "PRAGMA foreign_keys", rulesRead[int64](t, db, "PRAGMA foreign_keys"), 1)
}

func TestJournalModeIsWAL(t *testing.T) {
	t.Parallel()

	db := rulesOpen(t, testOptions())

	rulesMustAll(t, "PRAGMA journal_mode", rulesRead[string](t, db, "PRAGMA journal_mode"), "wal")
}

func TestBusyTimeoutIsTheOption(t *testing.T) {
	t.Parallel()

	db := rulesOpen(t, testOptions())

	rulesMustAll(t, "PRAGMA busy_timeout", rulesRead[int64](t, db, "PRAGMA busy_timeout"),
		optionsBusyTimeout.Milliseconds())
}

func TestTempStoreIsMemory(t *testing.T) {
	t.Parallel()

	db := rulesOpen(t, testOptions())

	rulesMustAll(t, "PRAGMA temp_store", rulesRead[int64](t, db, "PRAGMA temp_store"), 2)
}

func TestCacheSizeIsTheOption(t *testing.T) {
	t.Parallel()

	db := rulesOpen(t, testOptions())

	rulesMustAll(t, "PRAGMA cache_size", rulesRead[int64](t, db, "PRAGMA cache_size"), -optionsCacheSize)
}

func TestSynchronousIsTheOption(t *testing.T) {
	t.Parallel()

	for mode, want := range map[sqlite.Synchronous]int64{sqlite.SynchronousNormal: 1, sqlite.SynchronousFull: 2} {
		opts := testOptions()
		opts.Synchronous = mode

		db := rulesOpen(t, opts)

		rulesMustAll(t, "PRAGMA synchronous", rulesRead[int64](t, db, "PRAGMA synchronous"), want)
	}
}

func TestJournalSizeLimitIsTheOption(t *testing.T) {
	t.Parallel()

	opts := testOptions()
	opts.JournalSizeLimit = limitOf(rulesJournalSizeLimit)

	db := rulesOpen(t, opts)

	rulesMustAll(t, "PRAGMA journal_size_limit", rulesRead[int64](t, db, "PRAGMA journal_size_limit"),
		rulesJournalSizeLimit)
}

func TestNoJournalSizeLimitKeepsSQLitesDefault(t *testing.T) {
	t.Parallel()

	db := rulesOpen(t, testOptions())

	rulesMustAll(t, "PRAGMA journal_size_limit", rulesRead[int64](t, db, "PRAGMA journal_size_limit"), -1)
}

func TestDefensiveModeIsOn(t *testing.T) {
	t.Parallel()

	db := rulesOpen(t, testOptions())
	rulesExec(t, db, "CREATE TABLE defensive_rows (a INTEGER)")

	for i, conn := range rulesConns(t, db) {
		if _, err := conn.ExecContext(t.Context(), "PRAGMA writable_schema=ON"); err != nil {
			t.Fatalf("PRAGMA writable_schema=ON on connection %d error = %v, want nil", i, err)
		}
		_, err := conn.ExecContext(t.Context(), "UPDATE sqlite_schema SET sql = sql WHERE name = 'defensive_rows'")
		if err == nil || !strings.Contains(err.Error(), "may not be modified") {
			t.Errorf("an edit of the schema table on connection %d error = %v, want it refused", i, err)
		}
	}
}

func TestTheCapIsMaxConns(t *testing.T) {
	t.Parallel()

	db := rulesOpen(t, testOptions())
	for _, conn := range rulesConns(t, db) {
		if err := conn.Close(); err != nil {
			t.Fatalf("Close() error = %v, want nil", err)
		}
	}

	stats := db.Stats()
	if stats.MaxOpenConnections != optionsMaxConns || stats.Idle != optionsMaxConns {
		t.Errorf("Stats() = %d open at most and %d idle, want %d of each",
			stats.MaxOpenConnections, stats.Idle, optionsMaxConns)
	}
}

func TestOpenNeverConnects(t *testing.T) {
	t.Parallel()

	address, path := testAddress(t)

	db := mustOpen(t, address, testOptions())

	for _, name := range []string{path, path + "-wal", path + "-shm"} {
		mustNotExist(t, name)
	}
	if got := db.Stats().OpenConnections; got != 0 {
		t.Errorf("Stats().OpenConnections = %d, want none after Open", got)
	}
}

func TestAPlainTimeBindsAsMicroseconds(t *testing.T) {
	t.Parallel()

	db := rulesOpen(t, testOptions())
	moment := time.Date(2026, time.October, 6, 12, 30, 45, 123456789, time.UTC)

	var kind string
	var micros int64
	if err := db.QueryRowContext(t.Context(), "SELECT typeof(?), ?", moment, moment).Scan(&kind, &micros); err != nil {
		t.Fatalf("QueryRowContext() error = %v, want nil", err)
	}

	if kind != "integer" || micros != moment.UnixMicro() {
		t.Errorf("a time.Time binds as %s %d, want integer %d", kind, micros, moment.UnixMicro())
	}
}

// rulesReadThenWrite reads the counter and writes it back one higher in one transaction.
func rulesReadThenWrite(ctx context.Context, db *sql.DB) error {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	var n int64
	if err := tx.QueryRowContext(ctx, "SELECT n FROM immediate_counter").Scan(&n); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, "UPDATE immediate_counter SET n = ?", n+1); err != nil {
		return err
	}
	return tx.Commit()
}

// rulesWriter waits for start, runs rulesRounds transactions and sends the first error or nil.
func rulesWriter(ctx context.Context, db *sql.DB, start <-chan struct{}, done chan<- error) {
	<-start
	for range rulesRounds {
		if err := rulesReadThenWrite(ctx, db); err != nil {
			done <- err
			return
		}
	}
	done <- nil
}

func TestWritesStartImmediate(t *testing.T) {
	t.Parallel()

	opts := testOptions()
	opts.MaxConns = 2
	db := rulesOpen(t, opts)
	rulesExec(t, db, "CREATE TABLE immediate_counter (n INTEGER NOT NULL)")
	rulesExec(t, db, "INSERT INTO immediate_counter VALUES (0)")
	start, done := make(chan struct{}), make(chan error, 2)
	go rulesWriter(t.Context(), db, start, done)
	go rulesWriter(t.Context(), db, start, done)

	close(start)

	for range 2 {
		if err := <-done; err != nil {
			t.Errorf("a read-then-write transaction error = %v, want none busy", err)
		}
	}
	var n int64
	if err := db.QueryRowContext(t.Context(), "SELECT n FROM immediate_counter").Scan(&n); err != nil {
		t.Fatalf("QueryRowContext() error = %v, want nil", err)
	}
	if n != 2*rulesRounds {
		t.Errorf("counter = %d, want %d with no write lost", n, 2*rulesRounds)
	}
}

// rulesHoldWriter starts a write transaction that inserts one row and rolls it back at cleanup.
func rulesHoldWriter(t *testing.T, db *sql.DB, insert string) {
	t.Helper()
	writer, err := db.BeginTx(t.Context(), nil)
	if err != nil {
		t.Fatalf("BeginTx() error = %v, want nil", err)
	}
	t.Cleanup(func() { _ = writer.Rollback() })
	if _, err := writer.ExecContext(t.Context(), insert); err != nil {
		t.Fatalf("ExecContext(%q) error = %v, want nil", insert, err)
	}
}

func TestReadOnlyTransactionsNeverWaitForTheWriter(t *testing.T) {
	t.Parallel()

	db := rulesOpen(t, testOptions())
	rulesExec(t, db, "CREATE TABLE readonly_rows (a INTEGER)")
	rulesExec(t, db, "INSERT INTO readonly_rows VALUES (1)")
	rulesHoldWriter(t, db, "INSERT INTO readonly_rows VALUES (2)")

	reader, err := db.BeginTx(t.Context(), &sql.TxOptions{ReadOnly: true})
	if err != nil {
		t.Fatalf("BeginTx(ReadOnly) error = %v, want a read-only transaction while the writer holds the lock", err)
	}
	defer func() { _ = reader.Rollback() }()
	var n int64
	if err := reader.QueryRowContext(t.Context(), "SELECT count(*) FROM readonly_rows").Scan(&n); err != nil {
		t.Fatalf("QueryRowContext() error = %v, want nil", err)
	}

	if n != 1 {
		t.Errorf("count(*) = %d, want 1, the committed row only", n)
	}
}

func TestBusyPastTheTimeoutIsTheBusyClass(t *testing.T) {
	t.Parallel()

	opts := testOptions()
	opts.BusyTimeout = rulesShortBusyTimeout
	db := rulesOpen(t, opts)
	rulesExec(t, db, "CREATE TABLE busy_rows (a INTEGER)")
	rulesHoldWriter(t, db, "INSERT INTO busy_rows VALUES (1)")
	began := time.Now()

	tx, err := db.BeginTx(t.Context(), nil)

	waited := time.Since(began)
	if err == nil {
		_ = tx.Rollback()
		t.Fatal("BeginTx() error = nil, want busy while another writer holds the lock")
	}
	if got := sqlite.Classify(err); !errors.Is(got, dbkit.ErrBusy) {
		t.Errorf("Classify(%v) = %v, want the busy class", err, got)
	}
	if waited < rulesShortBusyTimeout {
		t.Errorf("BeginTx() answered after %v, want at least the busy timeout %v", waited, rulesShortBusyTimeout)
	}
}
