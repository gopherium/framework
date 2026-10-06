// SPDX-License-Identifier: Apache-2.0

package dbkit_test

import (
	"context"
	"database/sql"
	"errors"
	"runtime"
	"slices"
	"strconv"
	"testing"
	"testing/synctest"
	"time"

	"github.com/gopherium/framework/dbkit"
)

const (
	// shareID is the ID of the share under test.
	shareID = "share-under-test"
	// shareOtherID is the ID of a second share.
	shareOtherID = "other-share"
	// shareStatementTimeout is the statement timeout the tests pass.
	shareStatementTimeout = 7 * time.Second
	// shareTransactionTimeout is the transaction timeout the tests pass.
	shareTransactionTimeout = 11 * time.Minute
	// shareMaxOpen is the connection cap the application sets on its handle.
	shareMaxOpen = 3
	// shareRead is a statement that reads, with no parameter.
	shareRead = "SELECT 1"
	// shareWrite is a statement that writes, with one parameter.
	shareWrite = "INSERT INTO t VALUES ($1)"
	// shareSwapped names its parameters out of order.
	shareSwapped = "SELECT $2 || $1"
	// shareSwappedOnSQLite is shareSwapped as the SQLite driver must receive it.
	shareSwappedOnSQLite = "SELECT ?2 || ?1"
	// shareBareMark is a statement with a bare question mark parameter.
	shareBareMark = "SELECT ?"
	// shareNumberedMark is a statement with a hand-written numbered question mark parameter.
	shareNumberedMark = "SELECT ?1"
)

// shareStatements is what a share and a transaction both run.
type shareStatements interface {
	// Exec runs a statement that returns no rows.
	Exec(ctx context.Context, query string, args ...any) (sql.Result, error)
	// Query runs a query that returns rows.
	Query(ctx context.Context, query string, args ...any) (*dbkit.Rows, error)
	// QueryRow runs a query that returns at most one row.
	QueryRow(ctx context.Context, query string, args ...any) *dbkit.Row
}

// shareKind is one way to send a statement and run it to its end.
type shareKind struct {
	name string
	op   string
	run  func(ctx context.Context, on shareStatements, query string, args ...any) error
}

// shareKinds runs a statement as an Exec, as a Query read to its end and as a QueryRow scanned.
var shareKinds = []shareKind{
	{"Exec", "exec", shareExec},
	{"Query", "query", shareQuery},
	{"QueryRow", "query", shareQueryRow},
}

// shareExec runs query as an Exec and returns its error.
func shareExec(ctx context.Context, on shareStatements, query string, args ...any) error {
	_, err := on.Exec(ctx, query, args...)
	return err
}

// shareQuery runs query, reads every row, closes the rows and returns the first error.
func shareQuery(ctx context.Context, on shareStatements, query string, args ...any) error {
	rows, err := on.Query(ctx, query, args...)
	if err != nil {
		return err
	}
	for rows.Next() {
	}
	err = rows.Err()
	if closeErr := rows.Close(); err == nil {
		err = closeErr
	}
	return err
}

// shareQueryRow runs query as a QueryRow, scans its row and returns the error.
func shareQueryRow(ctx context.Context, on shareStatements, query string, args ...any) error {
	var n int64
	return on.QueryRow(ctx, query, args...).Scan(&n)
}

// shareOptions returns options with every required value set and the given number of slots.
func shareOptions(slots int) dbkit.ShareOptions {
	return dbkit.ShareOptions{
		ID:                 shareID,
		Slots:              slots,
		StatementTimeout:   shareStatementTimeout,
		TransactionTimeout: shareTransactionTimeout,
	}
}

// shareHandle returns a handle on a fresh fake driver, closed when the test ends.
func shareHandle(tb testing.TB) (*sql.DB, *fakeDriver) {
	tb.Helper()
	fake := fakeNew()
	db := sql.OpenDB(fake)
	tb.Cleanup(func() {
		if err := db.Close(); err != nil {
			tb.Errorf("Close() error = %v, want nil", err)
		}
	})
	return db, fake
}

// shareOpen returns a share of a fresh fake handle and the fake driver behind it.
func shareOpen(tb testing.TB, engine dbkit.Engine, opts dbkit.ShareOptions) (*dbkit.Share, *fakeDriver) {
	tb.Helper()
	db, fake := shareHandle(tb)
	share, err := dbkit.NewShare(db, engine, opts)
	if err != nil {
		tb.Fatalf("NewShare() error = %v, want nil", err)
	}
	return share, fake
}

// shareWhere is a place a test sends its statements to, given the share.
type shareWhere struct {
	name string
	on   func(t *testing.T, share *dbkit.Share) shareStatements
}

// shareWheres sends statements on the share itself and inside a transaction of it.
var shareWheres = []shareWhere{
	{"on the share", func(_ *testing.T, share *dbkit.Share) shareStatements { return share }},
	{"in a transaction", func(t *testing.T, share *dbkit.Share) shareStatements { return shareMustBegin(t, share) }},
}

// shareMustBegin starts a transaction under the test's context and fails the test when it cannot.
func shareMustBegin(t *testing.T, share *dbkit.Share) *dbkit.Tx {
	t.Helper()
	tx, err := share.Begin(t.Context())
	if err != nil {
		t.Fatalf("Begin() error = %v, want nil", err)
	}
	return tx
}

// shareMustQuery opens the rows of shareRead and fails the test when it cannot.
func shareMustQuery(t *testing.T, on shareStatements) *dbkit.Rows {
	t.Helper()
	rows, err := on.Query(t.Context(), shareRead)
	if err != nil {
		t.Fatalf("Query() error = %v, want nil", err)
	}
	return rows
}

// shareStart sends shareWrite as an Exec from a new goroutine and returns the channel its error arrives on.
func shareStart(ctx context.Context, share *dbkit.Share) <-chan error {
	done := make(chan error, 1)
	go func() {
		_, err := share.Exec(ctx, shareWrite, 1)
		done <- err
	}()
	return done
}

// shareWaiting fails the test unless the Exec that shareStart sent still waits for a slot, short of the driver.
func shareWaiting(t *testing.T, fake *fakeDriver, done <-chan error) {
	t.Helper()
	synctest.Wait()
	select {
	case err := <-done:
		t.Fatalf("Exec() returned %v while every slot was taken, want it waiting", err)
	default:
	}
	if fake.saw("exec", shareWrite) {
		t.Fatalf("driver calls = %v, want shareWrite held short of the driver", fake.log())
	}
}

// shareFree fails the test unless an Exec of shareWrite gets a slot and reaches the driver.
func shareFree(t *testing.T, share *dbkit.Share, fake *fakeDriver) {
	t.Helper()
	if _, err := share.Exec(t.Context(), shareWrite, 1); err != nil {
		t.Fatalf("Exec() error = %v, want a free slot", err)
	}
	if !fake.saw("exec", shareWrite) {
		t.Fatalf("driver calls = %v, want shareWrite received", fake.log())
	}
}

// shareSent fails the test unless the driver received op and every statement it received carried exactly text.
func shareSent(t *testing.T, fake *fakeDriver, op, text string) {
	t.Helper()
	if !fake.saw(op, text) {
		t.Fatalf("driver calls = %v, want %q", fake.log(), fakeLine(op, text))
	}
	for _, call := range fake.received() {
		if call.text != "" && call.text != text {
			t.Errorf("driver received %q, want %q byte for byte", call.text, text)
		}
	}
}

func TestNewShareRefusesAMissingOption(t *testing.T) {
	t.Parallel()

	db, fake := shareHandle(t)
	cases := []struct {
		name   string
		db     *sql.DB
		engine dbkit.Engine
		change func(*dbkit.ShareOptions)
		want   string
	}{
		{"a nil handle", nil, dbkit.Postgres, func(*dbkit.ShareOptions) {},
			"dbkit: the share needs a database handle, got nil"},
		{"no engine", db, dbkit.Engine(0), func(*dbkit.ShareOptions) {},
			"dbkit: the share engine must be postgres or sqlite, got Engine(0)"},
		{"an unknown engine", db, dbkit.Engine(3), func(*dbkit.ShareOptions) {},
			"dbkit: the share engine must be postgres or sqlite, got Engine(3)"},
		{"an empty ID", db, dbkit.SQLite, func(o *dbkit.ShareOptions) { o.ID = "" },
			"dbkit: the share option ID must not be empty"},
		{"zero slots", db, dbkit.SQLite, func(o *dbkit.ShareOptions) { o.Slots = 0 },
			"dbkit: the share option Slots must be 1 or more, got 0"},
		{"negative slots", db, dbkit.SQLite, func(o *dbkit.ShareOptions) { o.Slots = -1 },
			"dbkit: the share option Slots must be 1 or more, got -1"},
		{"a zero statement timeout", db, dbkit.Postgres, func(o *dbkit.ShareOptions) { o.StatementTimeout = 0 },
			"dbkit: the share option StatementTimeout must stand above zero, got 0s"},
		{"a negative statement timeout", db, dbkit.Postgres, func(o *dbkit.ShareOptions) { o.StatementTimeout = -1 },
			"dbkit: the share option StatementTimeout must stand above zero, got -1ns"},
		{"a zero transaction timeout", db, dbkit.Postgres, func(o *dbkit.ShareOptions) { o.TransactionTimeout = 0 },
			"dbkit: the share option TransactionTimeout must stand above zero, got 0s"},
		{"a negative transaction timeout", db, dbkit.Postgres,
			func(o *dbkit.ShareOptions) { o.TransactionTimeout = -1 },
			"dbkit: the share option TransactionTimeout must stand above zero, got -1ns"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			opts := shareOptions(1)
			c.change(&opts)

			share, err := dbkit.NewShare(c.db, c.engine, opts)

			if err == nil || err.Error() != c.want || share != nil {
				t.Errorf("NewShare() = %v, %v, want nil and %q", share, err, c.want)
			}
		})
	}
	if calls := fake.log(); len(calls) != 0 {
		t.Errorf("driver calls = %v, want none from a refused share", calls)
	}
}

func TestNewShareAcceptsTheSmallestValues(t *testing.T) {
	t.Parallel()

	db, _ := shareHandle(t)
	opts := dbkit.ShareOptions{ID: "x", Slots: 1, StatementTimeout: time.Nanosecond, TransactionTimeout: time.Nanosecond}

	if _, err := dbkit.NewShare(db, dbkit.SQLite, opts); err != nil {
		t.Errorf("NewShare() error = %v, want one slot and one nanosecond timeouts accepted", err)
	}
}

func TestShareReportsItsEngine(t *testing.T) {
	t.Parallel()

	for _, engine := range []dbkit.Engine{dbkit.Postgres, dbkit.SQLite} {
		share, _ := shareOpen(t, engine, shareOptions(1))

		if got := share.Engine(); got != engine {
			t.Errorf("Engine() = %v, want %v", got, engine)
		}
	}
}

func TestPostgresTextReachesTheDriverUnchanged(t *testing.T) {
	t.Parallel()

	for _, where := range shareWheres {
		for _, kind := range shareKinds {
			t.Run(kind.name+" "+where.name, func(t *testing.T) {
				t.Parallel()
				share, fake := shareOpen(t, dbkit.Postgres, shareOptions(1))

				if err := kind.run(t.Context(), where.on(t, share), shareSwapped, "a", "b"); err != nil {
					t.Fatalf("%s() error = %v, want nil", kind.name, err)
				}

				shareSent(t, fake, kind.op, shareSwapped)
			})
		}
	}
}

func TestSQLiteSendsTheScannedText(t *testing.T) {
	t.Parallel()

	for _, where := range shareWheres {
		for _, kind := range shareKinds {
			t.Run(kind.name+" "+where.name, func(t *testing.T) {
				t.Parallel()
				share, fake := shareOpen(t, dbkit.SQLite, shareOptions(1))

				if err := kind.run(t.Context(), where.on(t, share), shareSwapped, "a", "b"); err != nil {
					t.Fatalf("%s() error = %v, want nil", kind.name, err)
				}

				shareSent(t, fake, kind.op, shareSwappedOnSQLite)
			})
		}
	}
}

func TestPostgresPassesAQuestionMark(t *testing.T) {
	t.Parallel()

	for _, where := range shareWheres {
		for _, kind := range shareKinds {
			for _, text := range []string{shareBareMark, shareNumberedMark} {
				t.Run(kind.name+" "+where.name+" "+text, func(t *testing.T) {
					t.Parallel()
					share, fake := shareOpen(t, dbkit.Postgres, shareOptions(1))

					if err := kind.run(t.Context(), where.on(t, share), text, "a"); err != nil {
						t.Fatalf("%s(%q) error = %v, want nil", kind.name, text, err)
					}

					shareSent(t, fake, kind.op, text)
				})
			}
		}
	}
}

func TestBuildingSharesStartsNoGoroutine(t *testing.T) {
	db, fake := shareHandle(t)
	before := runtime.NumGoroutine()

	for i := range 200 {
		opts := shareOptions(1)
		opts.ID = "share-" + strconv.Itoa(i)
		shareNew(t, db, dbkit.SQLite, opts)
	}

	if after := runtime.NumGoroutine(); after > before {
		t.Errorf("goroutines after 200 shares = %d, want at most %d as before", after, before)
	}
	if calls := fake.log(); len(calls) != 0 {
		t.Errorf("driver calls = %v, want none from building shares", calls)
	}
}

func TestShareLeavesTheHandleAlone(t *testing.T) {
	t.Parallel()

	db, fake := shareHandle(t)
	db.SetMaxOpenConns(shareMaxOpen)
	engines := []dbkit.Engine{dbkit.Postgres, dbkit.SQLite}
	shares := make([]*dbkit.Share, 200)
	for i := range shares {
		opts := shareOptions(1)
		opts.ID = "share-" + strconv.Itoa(i)
		shares[i] = shareNew(t, db, engines[i%2], opts)
	}
	if calls := fake.log(); len(calls) != 0 {
		t.Fatalf("driver calls = %v, want none from building shares", calls)
	}

	for _, share := range shares {
		if err := shareExec(t.Context(), share, shareRead); err != nil {
			t.Fatalf("Exec() error = %v, want nil", err)
		}
	}

	if got := db.Stats().MaxOpenConnections; got != shareMaxOpen {
		t.Errorf("Stats().MaxOpenConnections = %d, want %d as the application set it", got, shareMaxOpen)
	}
	if got := len(fake.received()); got != len(shares) {
		t.Errorf("driver calls = %d, want one statement per share", got)
	}
}

func BenchmarkShareExec(b *testing.B) {
	db, fake := shareHandle(b)
	fake.quiet = true
	share := shareNew(b, db, dbkit.Postgres, shareOptions(1))
	ctx := b.Context()
	b.ReportAllocs()

	for b.Loop() {
		if _, err := share.Exec(ctx, shareRead); err != nil {
			b.Fatalf("Exec() error = %v, want nil", err)
		}
	}
}

func TestSQLiteRefusesAPlaceholder(t *testing.T) {
	t.Parallel()

	synctest.Test(t, func(t *testing.T) {
		share, fake, seen := shareWatched(t, dbkit.SQLite)
		ctx := shareBudget(t, 10).Attach(t.Context())
		held := shareMustQuery(t, share)
		shareDrain(seen)
		start := time.Now()

		var observed []string
		for _, text := range []string{shareBareMark, shareNumberedMark} {
			for _, kind := range shareKinds {
				if err := kind.run(ctx, share, text, 1); !errors.Is(err, dbkit.ErrPlaceholder) {
					t.Errorf("%s(%q) error = %v, want %v", kind.name, text, err, dbkit.ErrPlaceholder)
				}
				for _, s := range shareDrain(seen) {
					observed = append(observed, s.Query)
				}
			}
		}

		if took := time.Since(start); took != 0 {
			t.Errorf("refused statements took %v, want no wait for the held slot", took)
		}
		want := []string{shareBareMark, shareBareMark, shareBareMark, shareNumberedMark, shareNumberedMark, shareNumberedMark}
		if !slices.Equal(observed, want) {
			t.Errorf("observed = %v, want %v", observed, want)
		}
		if got := fake.log(); !slices.Equal(got, []string{"query " + shareRead}) {
			t.Errorf("driver calls = %v, want only the held query", got)
		}
		if count, _ := dbkit.QueriesIn(ctx); count.Total != 6 {
			t.Errorf("QueriesIn().Total = %d, want the 6 refused statements counted", count.Total)
		}
		if err := held.Close(); err != nil {
			t.Errorf("Close() error = %v, want nil", err)
		}
		tx := shareMustBegin(t, share)
		if _, err := tx.Exec(ctx, shareBareMark, 1); !errors.Is(err, dbkit.ErrPlaceholder) {
			t.Errorf("Tx.Exec(%q) error = %v, want %v", shareBareMark, err, dbkit.ErrPlaceholder)
		}
		if err := tx.Commit(); err != nil {
			t.Errorf("Commit() error = %v, want nil", err)
		}
	})
}

func TestShareHoldsNoMoreThanItsSlots(t *testing.T) {
	t.Parallel()

	synctest.Test(t, func(t *testing.T) {
		share, fake := shareOpen(t, dbkit.Postgres, shareOptions(2))
		first := shareMustQuery(t, share)
		second := shareMustQuery(t, share)

		done := shareStart(t.Context(), share)
		shareWaiting(t, fake, done)
		if err := first.Close(); err != nil {
			t.Fatalf("Close() error = %v, want nil", err)
		}

		if err := <-done; err != nil {
			t.Errorf("Exec() error = %v, want it run once a result set closed", err)
		}
		if err := second.Close(); err != nil {
			t.Errorf("Close() error = %v, want nil", err)
		}
	})
}

func TestEachKindHoldsASlotUntilItEnds(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		hold func(t *testing.T, share *dbkit.Share, fake *fakeDriver) (end func())
	}{
		{"Exec until it returns", func(t *testing.T, share *dbkit.Share, fake *fakeDriver) func() {
			gate := fake.hold(shareRead)
			done := make(chan error, 1)
			go func() {
				_, err := share.Exec(t.Context(), shareRead)
				done <- err
			}()
			synctest.Wait()
			return func() {
				close(gate)
				if err := <-done; err != nil {
					t.Errorf("Exec() error = %v, want nil", err)
				}
			}
		}},
		{"Query until Close", func(t *testing.T, share *dbkit.Share, _ *fakeDriver) func() {
			rows := shareMustQuery(t, share)
			return func() {
				if err := rows.Close(); err != nil {
					t.Errorf("Close() error = %v, want nil", err)
				}
			}
		}},
		{"Query until Next returns false", func(t *testing.T, share *dbkit.Share, _ *fakeDriver) func() {
			rows := shareMustQuery(t, share)
			return func() {
				for rows.Next() {
				}
			}
		}},
		{"Query until NextResultSet returns false", func(t *testing.T, share *dbkit.Share, _ *fakeDriver) func() {
			rows := shareMustQuery(t, share)
			return func() {
				if rows.NextResultSet() {
					t.Errorf("NextResultSet() = true, want false with one result set")
				}
			}
		}},
		{"Query until the last result set ends", func(t *testing.T, share *dbkit.Share, fake *fakeDriver) func() {
			fake.answer(1, 1)
			rows := shareMustQuery(t, share)
			for rows.Next() {
			}
			return func() {
				if !rows.NextResultSet() {
					t.Errorf("NextResultSet() = false, want the second result set")
				}
				for rows.Next() {
				}
			}
		}},
		{"QueryRow until Scan", func(t *testing.T, share *dbkit.Share, _ *fakeDriver) func() {
			row := share.QueryRow(t.Context(), shareRead)
			return func() {
				var n int64
				if err := row.Scan(&n); err != nil {
					t.Errorf("Scan() error = %v, want nil", err)
				}
			}
		}},
		{"Begin until Commit", func(t *testing.T, share *dbkit.Share, _ *fakeDriver) func() {
			tx := shareMustBegin(t, share)
			return func() {
				if err := tx.Commit(); err != nil {
					t.Errorf("Commit() error = %v, want nil", err)
				}
			}
		}},
		{"Begin until Rollback", func(t *testing.T, share *dbkit.Share, _ *fakeDriver) func() {
			tx := shareMustBegin(t, share)
			return func() {
				if err := tx.Rollback(); err != nil {
					t.Errorf("Rollback() error = %v, want nil", err)
				}
			}
		}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			synctest.Test(t, func(t *testing.T) {
				share, fake := shareOpen(t, dbkit.Postgres, shareOptions(1))
				end := c.hold(t, share, fake)

				done := shareStart(t.Context(), share)
				shareWaiting(t, fake, done)
				end()

				if err := <-done; err != nil {
					t.Errorf("Exec() error = %v, want it run once the slot came back", err)
				}
			})
		})
	}
}

func TestASlotGoesBackOnce(t *testing.T) {
	t.Parallel()

	synctest.Test(t, func(t *testing.T) {
		share, fake := shareOpen(t, dbkit.Postgres, shareOptions(1))
		rows := shareMustQuery(t, share)
		if err := errors.Join(rows.Close(), rows.Close()); err != nil {
			t.Errorf("Close() twice error = %v, want nil", err)
		}
		row := share.QueryRow(t.Context(), shareRead)
		var n int64
		if err := row.Scan(&n); err != nil {
			t.Errorf("Scan() error = %v, want nil", err)
		}
		if err := row.Scan(&n); err == nil {
			t.Errorf("second Scan() error = nil, want the closed rows reported")
		}
		committed := shareMustBegin(t, share)
		if err := committed.Commit(); err != nil {
			t.Errorf("Commit() error = %v, want nil", err)
		}
		if err := committed.Rollback(); !errors.Is(err, sql.ErrTxDone) {
			t.Errorf("Rollback() after Commit() error = %v, want %v", err, sql.ErrTxDone)
		}
		rolledBack := shareMustBegin(t, share)
		if err := rolledBack.Rollback(); err != nil {
			t.Errorf("Rollback() error = %v, want nil", err)
		}
		if err := rolledBack.Commit(); !errors.Is(err, sql.ErrTxDone) {
			t.Errorf("Commit() after Rollback() error = %v, want %v", err, sql.ErrTxDone)
		}

		gate := fake.hold(shareRead)
		first := make(chan error, 1)
		go func() {
			_, err := share.Exec(t.Context(), shareRead)
			first <- err
		}()
		synctest.Wait()
		second := shareStart(t.Context(), share)
		shareWaiting(t, fake, second)
		close(gate)

		if err := errors.Join(<-first, <-second); err != nil {
			t.Errorf("Exec() error = %v, want both run one after the other", err)
		}
	})
}

func TestTheWaitForASlotStopsAtTheDeadline(t *testing.T) {
	t.Parallel()

	synctest.Test(t, func(t *testing.T) {
		share, fake := shareOpen(t, dbkit.Postgres, shareOptions(1))
		tx := shareMustBegin(t, share)
		past, cancel := context.WithDeadline(t.Context(), time.Now().Add(-time.Second))
		defer cancel()
		start := time.Now()

		_, err := share.Exec(past, shareWrite, 1)

		if !errors.Is(err, dbkit.ErrShareFull) || !errors.Is(err, context.DeadlineExceeded) {
			t.Errorf("Exec() error = %v, want %v and %v", err, dbkit.ErrShareFull, context.DeadlineExceeded)
		}
		if took := time.Since(start); took != 0 {
			t.Errorf("Exec() took %v, want no wait", took)
		}
		if fake.saw("exec", shareWrite) {
			t.Errorf("driver calls = %v, want shareWrite never sent", fake.log())
		}
		if err := tx.Rollback(); err != nil {
			t.Errorf("Rollback() error = %v, want nil", err)
		}
	})
}

func TestTheStatementTimeoutBoundsTheWaitForASlot(t *testing.T) {
	t.Parallel()

	synctest.Test(t, func(t *testing.T) {
		share, fake := shareOpen(t, dbkit.Postgres, shareOptions(1))
		tx := shareMustBegin(t, share)
		steps := []struct {
			name string
			run  func() error
		}{
			{"Exec", func() error { return shareExec(context.Background(), share, shareWrite, 1) }},
			{"Query", func() error { return shareQuery(context.Background(), share, shareWrite, 1) }},
			{"QueryRow", func() error { return shareQueryRow(context.Background(), share, shareWrite, 1) }},
			{"Begin", func() error {
				_, err := share.Begin(context.Background())
				return err
			}},
		}

		for _, step := range steps {
			start := time.Now()
			err := step.run()

			if !errors.Is(err, dbkit.ErrShareFull) || !errors.Is(err, context.DeadlineExceeded) {
				t.Errorf("%s() error = %v, want %v and %v", step.name, err, dbkit.ErrShareFull, context.DeadlineExceeded)
			}
			if took := time.Since(start); took != shareStatementTimeout {
				t.Errorf("%s() took %v, want the statement timeout %v", step.name, took, shareStatementTimeout)
			}
		}
		if got := fake.log(); len(got) != 1 || got[0] != "begin" {
			t.Errorf("driver calls = %v, want only the first begin", got)
		}
		if err := tx.Rollback(); err != nil {
			t.Errorf("Rollback() error = %v, want nil", err)
		}
	})
}

func TestEachStatementCarriesTheDeadline(t *testing.T) {
	t.Parallel()

	synctest.Test(t, func(t *testing.T) {
		share, fake := shareOpen(t, dbkit.Postgres, shareOptions(1))
		start := time.Now()
		for _, kind := range shareKinds {
			if err := kind.run(t.Context(), share, shareRead); err != nil {
				t.Fatalf("%s() error = %v, want nil", kind.name, err)
			}
		}
		tx := shareMustBegin(t, share)
		for _, kind := range shareKinds {
			if err := kind.run(t.Context(), tx, shareRead); err != nil {
				t.Fatalf("Tx.%s() error = %v, want nil", kind.name, err)
			}
		}
		if err := tx.Commit(); err != nil {
			t.Fatalf("Commit() error = %v, want nil", err)
		}

		want := map[string]time.Time{
			"exec":  start.Add(shareStatementTimeout),
			"query": start.Add(shareStatementTimeout),
		}
		seen := 0
		for _, call := range fake.received() {
			if deadline, ok := want[call.op]; ok {
				seen++
				if !call.bounded || !call.deadline.Equal(deadline) {
					t.Errorf("%s deadline = %v, %t, want %v", call.op, call.deadline, call.bounded, deadline)
				}
			}
		}
		if seen != 6 {
			t.Errorf("driver calls = %v, want six statements", fake.log())
		}
	})
}

func TestADriverErrorGivesTheSlotBack(t *testing.T) {
	t.Parallel()

	for _, kind := range shareKinds {
		t.Run(kind.name, func(t *testing.T) {
			t.Parallel()
			synctest.Test(t, func(t *testing.T) {
				share, fake := shareOpen(t, dbkit.Postgres, shareOptions(1))
				fake.fail(shareRead)

				if err := kind.run(t.Context(), share, shareRead); !errors.Is(err, fakeBroken) {
					t.Fatalf("%s() error = %v, want %v", kind.name, err, fakeBroken)
				}

				shareFree(t, share, fake)
			})
		})
	}
}

func TestADriverErrorComesBackAsItIs(t *testing.T) {
	t.Parallel()

	for _, kind := range shareKinds {
		t.Run(kind.name, func(t *testing.T) {
			t.Parallel()
			share, fake := shareOpen(t, dbkit.Postgres, shareOptions(1))
			fake.fail(shareRead)

			err := kind.run(t.Context(), share, shareRead)

			if err == nil || err.Error() != string(fakeBroken) {
				t.Errorf("%s() error = %v, want %q unwrapped", kind.name, err, fakeBroken)
			}
		})
	}
	for _, kind := range shareKinds[1:] {
		t.Run(kind.name+" a failed row read", func(t *testing.T) {
			t.Parallel()
			share, fake := shareOpen(t, dbkit.Postgres, shareOptions(1))
			fake.failRows(shareRead)

			err := kind.run(t.Context(), share, shareRead)

			if err == nil || err.Error() != string(fakeBroken) {
				t.Errorf("%s() error = %v, want %q unwrapped", kind.name, err, fakeBroken)
			}
		})
	}
}

func TestAStatementPastItsDeadlineMatchesDeadlineExceeded(t *testing.T) {
	t.Parallel()

	for _, kind := range shareKinds {
		t.Run(kind.name, func(t *testing.T) {
			t.Parallel()
			synctest.Test(t, func(t *testing.T) {
				share, fake := shareOpen(t, dbkit.Postgres, shareOptions(1))
				fake.hold(shareRead)
				start := time.Now()

				err := kind.run(t.Context(), share, shareRead)

				if !errors.Is(err, context.DeadlineExceeded) || !errors.Is(err, fakeInterrupted) {
					t.Errorf("%s() error = %v, want the driver error matching the deadline", kind.name, err)
				}
				if took := time.Since(start); took != shareStatementTimeout {
					t.Errorf("%s() took %v, want the statement timeout %v", kind.name, took, shareStatementTimeout)
				}
			})
		})
	}
	for _, kind := range shareKinds[1:] {
		t.Run(kind.name+" a held row read", func(t *testing.T) {
			t.Parallel()
			synctest.Test(t, func(t *testing.T) {
				share, fake := shareOpen(t, dbkit.Postgres, shareOptions(1))
				fake.holdRows(shareRead)

				err := kind.run(t.Context(), share, shareRead)

				if !errors.Is(err, context.DeadlineExceeded) {
					t.Errorf("%s() error = %v, want an error matching the deadline", kind.name, err)
				}
			})
		})
	}
}
