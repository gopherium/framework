// SPDX-License-Identifier: Apache-2.0

package postgres_test

import (
	"context"
	"database/sql"
	"errors"
	"testing"
	"time"

	"github.com/gopherium/framework/dbkit"
	"github.com/gopherium/framework/dbkit/postgres"
)

const (
	// shareStatementTimeout is a statement timeout no statement of these tests waits out.
	shareStatementTimeout = 20 * time.Second
	// shareTransactionTimeout is a transaction timeout no transaction of these tests waits out.
	shareTransactionTimeout = 40 * time.Second
	// shareShortStatementTimeout is a statement timeout a sleeping statement runs into.
	shareShortStatementTimeout = 50 * time.Millisecond
	// shareSlots is the slot count of a share that holds fewer connections than its pool allows.
	shareSlots = 2
	// shareRounds is how many times the idle connection test runs each kind of statement.
	shareRounds = 10
	// shareHeld is a query whose result set stays open until the test closes it.
	shareHeld = "SELECT generate_series(1, 2)"
	// shareSleep is a statement that sleeps far past every deadline of these tests.
	shareSleep = "SELECT pg_sleep(60)"
	// shareStopWait is how long a test waits for the server to stop a statement past its deadline.
	shareStopWait = 30 * time.Second
)

// shareOptions returns share options named id with slots and every other required value set.
func shareOptions(id string, slots int) dbkit.ShareOptions {
	return dbkit.ShareOptions{
		ID:                 id,
		Slots:              slots,
		StatementTimeout:   shareStatementTimeout,
		TransactionTimeout: shareTransactionTimeout,
	}
}

// shareOn returns a PostgreSQL share of db with opts and fails the test when it cannot.
func shareOn(t *testing.T, db *sql.DB, opts dbkit.ShareOptions) *dbkit.Share {
	t.Helper()
	share, err := dbkit.NewShare(db, dbkit.Postgres, opts)
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

// shareRower is a share or a transaction of one.
type shareRower interface {
	// QueryRow runs a query that returns at most one row.
	QueryRow(ctx context.Context, query string, args ...any) *dbkit.Row
}

// shareMustRead returns the one value query answers through on and fails the test when it cannot.
func shareMustRead[T any](t *testing.T, on shareRower, query string, args ...any) T {
	t.Helper()
	var value T
	if err := on.QueryRow(t.Context(), query, args...).Scan(&value); err != nil {
		t.Fatalf("QueryRow(%q) error = %v, want nil", query, err)
	}
	return value
}

// shareMustBegin starts a transaction of share and fails the test when it cannot.
func shareMustBegin(t *testing.T, share *dbkit.Share) *dbkit.Tx {
	t.Helper()
	tx, err := share.Begin(t.Context())
	if err != nil {
		t.Fatalf("Begin() error = %v, want nil", err)
	}
	return tx
}

// shareHold returns n result sets of share left open, each closed when the test ends at the latest.
func shareHold(t *testing.T, share *dbkit.Share, n int) []*dbkit.Rows {
	t.Helper()
	held := make([]*dbkit.Rows, n)
	for i := range held {
		rows, err := share.Query(t.Context(), shareHeld)
		if err != nil {
			t.Fatalf("Query() error = %v, want nil", err)
		}
		t.Cleanup(func() { _ = rows.Close() })
		held[i] = rows
	}
	return held
}

// shareMustClose closes rows and fails the test when it cannot.
func shareMustClose(t *testing.T, rows *dbkit.Rows) {
	t.Helper()
	if err := rows.Close(); err != nil {
		t.Fatalf("Close() error = %v, want nil", err)
	}
}

// shareMustDrain reads every row of a query through share and closes it.
func shareMustDrain(t *testing.T, share *dbkit.Share, query string) {
	t.Helper()
	rows, err := share.Query(t.Context(), query)
	if err != nil {
		t.Fatalf("Query(%q) error = %v, want nil", query, err)
	}
	for rows.Next() {
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("Err() error = %v, want nil", err)
	}
	shareMustClose(t, rows)
}

func TestTheViewHoldsNoIdleConnection(t *testing.T) {
	t.Parallel()

	h := openFreshHandle(t, openMaxConns)
	share := shareOn(t, h.DB, shareOptions("idle-share", openMaxConns))
	shareMustExec(t, share, "CREATE TABLE share_entries (a integer NOT NULL)")

	for i := range shareRounds {
		shareMustExec(t, share, "INSERT INTO share_entries VALUES ($1)", i)
		shareMustRead[int64](t, share, "SELECT count(*) FROM share_entries")
		shareMustDrain(t, share, "SELECT a FROM share_entries")
		tx := shareMustBegin(t, share)
		shareMustExec(t, tx, "UPDATE share_entries SET a = a + 1 WHERE a = $1", i)
		if err := tx.Commit(); err != nil {
			t.Fatalf("Commit() error = %v, want nil", err)
		}
	}

	if stats := h.DB.Stats(); stats.Idle != 0 || stats.InUse != 0 || stats.OpenConnections != 0 {
		t.Errorf("the view holds %d idle, %d in use and %d open connections, want none", stats.Idle, stats.InUse,
			stats.OpenConnections)
	}
	if stat := h.Pool.Stat(); stat.AcquiredConns() != 0 || stat.IdleConns() == 0 {
		t.Errorf("the pool holds %d acquired and %d idle connections, want none acquired and the idle ones its own",
			stat.AcquiredConns(), stat.IdleConns())
	}
}

func TestShareOnPostgresBindsTheSameText(t *testing.T) {
	t.Parallel()

	share := shareOn(t, openFreshHandle(t, openMaxConns).DB, shareOptions("binding-share", shareSlots))
	shareMustExec(t, share, "CREATE TABLE share_pairs (first_arg text NOT NULL, second_arg text NOT NULL)")

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

func TestShareOnPostgresLeavesTheJSONBQuestionMarkToTheServer(t *testing.T) {
	t.Parallel()

	share := shareOn(t, openFreshHandle(t, openMaxConns).DB, shareOptions("jsonb-share", shareSlots))

	holds := shareMustRead[bool](t, share, `SELECT '{"a": 1}'::jsonb ? $1`, "a")

	if !holds {
		t.Error(`SELECT '{"a": 1}'::jsonb ? 'a' = false, want true`)
	}
}

func TestShareHoldsNoMoreThanItsSlotsOnThePool(t *testing.T) {
	t.Parallel()

	h := openFreshHandle(t, openMaxConns)
	share := shareOn(t, h.DB, shareOptions("slots-share", shareSlots))
	held := shareHold(t, share, shareSlots)
	acquires := h.Pool.Stat().AcquireCount()
	short, cancel := context.WithTimeout(t.Context(), openShortWait)

	_, err := share.Exec(short, "SELECT 1")

	cancel()
	if !errors.Is(err, dbkit.ErrShareFull) || !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("a third statement error = %v, want ErrShareFull and context.DeadlineExceeded", err)
	}
	if got := h.Pool.Stat().AcquireCount(); got != acquires {
		t.Errorf("the pool served %d acquires during the third statement, want none", got-acquires)
	}
	if got := h.Pool.Stat().AcquiredConns(); got != shareSlots {
		t.Errorf("Stat().AcquiredConns() = %d, want the share's %d slots under the pool's cap of %d", got,
			shareSlots, openMaxConns)
	}
	shareMustClose(t, held[0])
	shareMustExec(t, share, "SELECT 1")
	if made := h.Pool.Stat().NewConnsCount(); made != shareSlots {
		t.Errorf("Stat().NewConnsCount() = %d, want the share never past its %d slots", made, shareSlots)
	}
}

func TestShareTransactionCommitsAndRollsBack(t *testing.T) {
	t.Parallel()

	h := openFreshHandle(t, openMaxConns)
	share := shareOn(t, h.DB, shareOptions("transaction-share", shareSlots))
	shareMustExec(t, share, "CREATE TABLE share_entries (a integer NOT NULL)")

	committed := shareMustBegin(t, share)
	shareMustExec(t, committed, "INSERT INTO share_entries VALUES ($1)", 1)
	if err := committed.Commit(); err != nil {
		t.Fatalf("Commit() error = %v, want nil", err)
	}
	rolled := shareMustBegin(t, share)
	shareMustExec(t, rolled, "INSERT INTO share_entries VALUES ($1)", 2)
	inside := shareMustRead[int64](t, rolled, "SELECT coalesce(sum(a), 0) FROM share_entries")
	if err := rolled.Rollback(); err != nil {
		t.Fatalf("Rollback() error = %v, want nil", err)
	}

	if inside != 3 {
		t.Errorf("inside the second transaction sum(a) = %d, want 3", inside)
	}
	if after := shareMustRead[int64](t, share, "SELECT coalesce(sum(a), 0) FROM share_entries"); after != 1 {
		t.Errorf("after the rollback sum(a) = %d, want only the committed 1", after)
	}
	if got := h.Pool.Stat().AcquiredConns(); got != 0 {
		t.Errorf("Stat().AcquiredConns() = %d, want every transaction's connection given back", got)
	}
}

// shareSleepKinds runs shareSleep through the share as an Exec, a Query read to its end and a QueryRow.
var shareSleepKinds = []struct {
	name string
	run  func(ctx context.Context, share *dbkit.Share) error
}{
	{"Exec", func(ctx context.Context, share *dbkit.Share) error {
		_, err := share.Exec(ctx, shareSleep)
		return err
	}},
	{"Query", func(ctx context.Context, share *dbkit.Share) error {
		rows, err := share.Query(ctx, shareSleep)
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
		var slept any
		return share.QueryRow(ctx, shareSleep).Scan(&slept)
	}},
}

// shareAwaitStopped returns once no other session of the database of h runs pg_sleep, within shareStopWait.
func shareAwaitStopped(t *testing.T, h *postgres.Handle) {
	t.Helper()
	lookup := "SELECT EXISTS (SELECT FROM pg_stat_activity WHERE datname = current_database()" +
		" AND pid <> pg_backend_pid() AND state = 'active' AND query LIKE '%pg_sleep%')"
	ticker := time.NewTicker(migrateAwaitPoll)
	defer ticker.Stop()
	limit := time.NewTimer(shareStopWait)
	defer limit.Stop()
	for {
		var sleeping bool
		if err := h.Pool.QueryRow(t.Context(), lookup).Scan(&sleeping); err != nil {
			t.Fatalf("look for the sleeping statement in pg_stat_activity: %v", err)
		}
		if !sleeping {
			return
		}
		select {
		case <-limit.C:
			t.Fatalf("the server still runs the statement %v after its deadline, want it stopped", shareStopWait)
		case <-ticker.C:
		}
	}
}

func TestAStatementPastItsDeadlineStops(t *testing.T) {
	t.Parallel()

	for _, kind := range shareSleepKinds {
		t.Run(kind.name, func(t *testing.T) {
			t.Parallel()
			h := openFreshHandle(t, openMaxConns)
			short := shareOptions("deadline-share", shareSlots)
			short.StatementTimeout = shareShortStatementTimeout
			share := shareOn(t, h.DB, short)
			after := shareOn(t, h.DB, shareOptions("after-share", shareSlots))
			shareMustRead[int64](t, after, "SELECT $1::bigint", 1)
			began := time.Now()

			err := kind.run(t.Context(), share)

			took := time.Since(began)
			if !errors.Is(err, context.DeadlineExceeded) || errors.Is(err, dbkit.ErrShareFull) {
				t.Errorf("%s of a sleeping statement error = %v, want context.DeadlineExceeded from the run", kind.name,
					err)
			}
			if took < shareShortStatementTimeout {
				t.Errorf("%s answered after %v, want at least the statement timeout %v", kind.name, took,
					shareShortStatementTimeout)
			}
			shareAwaitStopped(t, h)
			if got := shareMustRead[int64](t, after, "SELECT $1::bigint", 1); got != 1 {
				t.Errorf("SELECT $1 after the deadline = %d, want 1", got)
			}
		})
	}
}

func TestThePoolKeepsItsCapUnderShares(t *testing.T) {
	t.Parallel()

	h := openFreshHandle(t, openMaxConns)
	var held []*dbkit.Rows
	for _, id := range []string{"first-holder-share", "second-holder-share"} {
		held = append(held, shareHold(t, shareOn(t, h.DB, shareOptions(id, shareSlots)), shareSlots)...)
	}
	if got := h.Pool.Stat().AcquiredConns(); got != openMaxConns {
		t.Fatalf("Stat().AcquiredConns() = %d, want the pool's cap of %d", got, openMaxConns)
	}
	waiter := shareOn(t, h.DB, shareOptions("waiter-share", shareSlots))
	short, cancel := context.WithTimeout(t.Context(), openShortWait)

	_, err := waiter.Exec(short, "SELECT 1")

	cancel()
	if !errors.Is(err, context.DeadlineExceeded) || errors.Is(err, dbkit.ErrShareFull) {
		t.Errorf("a statement of a share with free slots error = %v, want the pool's cap to hold it back", err)
	}
	stat := h.Pool.Stat()
	if stat.TotalConns() > openMaxConns || stat.MaxConns() != openMaxConns || h.Pool.Config().MaxConns != openMaxConns {
		t.Errorf("the pool holds %d connections under a cap of %d, configured %d, want at most and exactly %d",
			stat.TotalConns(), stat.MaxConns(), h.Pool.Config().MaxConns, openMaxConns)
	}
	if got := h.DB.Stats().MaxOpenConnections; got != 0 {
		t.Errorf("the view's Stats().MaxOpenConnections = %d, want no cap of its own", got)
	}
	shareMustClose(t, held[0])
	shareMustExec(t, waiter, "SELECT 1")
	if made := h.Pool.Stat().NewConnsCount(); made > openMaxConns {
		t.Errorf("Stat().NewConnsCount() = %d, want the pool never past its cap of %d", made, openMaxConns)
	}
}
