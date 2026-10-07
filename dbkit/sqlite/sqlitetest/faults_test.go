// SPDX-License-Identifier: Apache-2.0

package sqlitetest_test

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/gopherium/framework/dbkit"
	"github.com/gopherium/framework/dbkit/sqlite"
	"github.com/gopherium/framework/dbkit/sqlite/sqlitetest"
)

const (
	// testBusyTimeout is a busy timeout no uncontended test waits out.
	testBusyTimeout = 5 * time.Second
	// testCacheSize is the page cache in kibibytes the tests pass.
	testCacheSize = 1024
	// testMaxConns is the connection cap the tests pass.
	testMaxConns = 3
	// testShortBusyTimeout is a busy timeout a test waits out on purpose.
	testShortBusyTimeout = 30 * time.Millisecond
	// faultSchema creates the table the fault tests write to.
	faultSchema = "CREATE TABLE fault_rows (a INTEGER NOT NULL)"
	// faultCount counts the rows of the fault table.
	faultCount = "SELECT count(*) FROM fault_rows"
	// faultChosen is the statement the statement tests choose to fail.
	faultChosen = "SELECT count(*) FROM fault_rows WHERE a = 1"
	// faultInsert writes one row to the fault table.
	faultInsert = "INSERT INTO fault_rows VALUES (1)"
	// faultUnparsable is a statement the driver refuses to prepare.
	faultUnparsable = "SELEC count(*) FROM fault_rows"
	// faultEcho answers its one argument.
	faultEcho = "SELECT ?"
	// echoed is the argument the tests pass to faultEcho.
	echoed int64 = 7
)

// errInjected is the error a chosen fault answers with.
var errInjected = errors.New("sqlitetest test: the fault answered on purpose")

// testOptions returns options with every required value set.
func testOptions() sqlite.Options {
	return sqlite.Options{
		BusyTimeout: testBusyTimeout,
		CacheSize:   testCacheSize,
		MaxConns:    testMaxConns,
		Synchronous: sqlite.SynchronousNormal,
	}
}

// execer is a handle, a connection or a transaction a statement runs on.
type execer interface {
	// ExecContext runs a statement that returns no rows.
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
}

// mustExec runs query on on and fails the test when it cannot.
func mustExec(t *testing.T, on execer, query string) {
	t.Helper()
	if _, err := on.ExecContext(t.Context(), query); err != nil {
		t.Fatalf("ExecContext(%q) error = %v, want nil", query, err)
	}
}

// querier is a handle or a transaction a statement runs on in every way database/sql offers.
type querier interface {
	execer
	// QueryContext runs a query that returns rows.
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
	// QueryRowContext runs a query that returns at most one row.
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
	// PrepareContext prepares a statement.
	PrepareContext(ctx context.Context, query string) (*sql.Stmt, error)
}

// statementWay is one way database/sql sends a statement to the driver.
type statementWay struct {
	// name names the way.
	name string
	// run sends query on on and returns its error.
	run func(ctx context.Context, on querier, query string) error
}

// statementWays sends a statement as an Exec, a Query, a QueryRow and a Prepare.
var statementWays = []statementWay{
	{"Exec", func(ctx context.Context, on querier, query string) error {
		_, err := on.ExecContext(ctx, query)
		return err
	}},
	{"Query", func(ctx context.Context, on querier, query string) error {
		rows, err := on.QueryContext(ctx, query)
		if err != nil {
			return err
		}
		return rows.Close()
	}},
	{"QueryRow", func(ctx context.Context, on querier, query string) error {
		var n int64
		return on.QueryRowContext(ctx, query).Scan(&n)
	}},
	{"Prepare", func(ctx context.Context, on querier, query string) error {
		stmt, err := on.PrepareContext(ctx, query)
		if err != nil {
			return err
		}
		return stmt.Close()
	}},
}

// mustBegin starts a transaction on db and fails the test when it cannot.
func mustBegin(t *testing.T, db *sql.DB) *sql.Tx {
	t.Helper()
	tx, err := db.BeginTx(t.Context(), nil)
	if err != nil {
		t.Fatalf("BeginTx() error = %v, want nil", err)
	}
	return tx
}

// mustCount fails the test unless the fault table holds want rows.
func mustCount(t *testing.T, db *sql.DB, want int64) {
	t.Helper()
	var got int64
	if err := db.QueryRowContext(t.Context(), faultCount).Scan(&got); err != nil || got != want {
		t.Errorf("%s = %d, %v, want %d", faultCount, got, err, want)
	}
}

// faultOpen returns a handle with faults on a fresh file that holds the fault table.
func faultOpen(t *testing.T, faults *sqlitetest.Faults) *sql.DB {
	t.Helper()
	db := sqlitetest.OpenWithFaults(t, testOptions(), faults)
	mustExec(t, db, faultSchema)
	return db
}

func TestFaultFailsTheChosenCommit(t *testing.T) {
	t.Parallel()

	faults := &sqlitetest.Faults{}
	db := faultOpen(t, faults)
	faults.FailNextCommit(errInjected)
	tx := mustBegin(t, db)
	mustExec(t, tx, faultInsert)

	err := tx.Commit()

	if !errors.Is(err, errInjected) {
		t.Errorf("Commit() error = %v, want the injected error", err)
	}
	mustCount(t, db, 0)
	next := mustBegin(t, db)
	mustExec(t, next, "INSERT INTO fault_rows VALUES (2)")
	if err := next.Commit(); err != nil {
		t.Errorf("the next Commit() error = %v, want nil once the fault answered", err)
	}
	mustCount(t, db, 1)
}

func TestFaultFailsTheChosenStatementInsideATransaction(t *testing.T) {
	t.Parallel()

	for _, way := range statementWays {
		t.Run(way.name, func(t *testing.T) {
			t.Parallel()
			faults := &sqlitetest.Faults{}
			db := faultOpen(t, faults)
			faults.FailStatement(faultChosen, errInjected)
			tx := mustBegin(t, db)
			defer func() { _ = tx.Rollback() }()

			err := way.run(t.Context(), tx, faultChosen)

			if !errors.Is(err, errInjected) {
				t.Errorf("%s(%q) in a transaction error = %v, want the injected error", way.name, faultChosen, err)
			}
			mustExec(t, tx, faultInsert)
			if err := tx.Commit(); err != nil {
				t.Errorf("Commit() error = %v, want the transaction to outlive the fault", err)
			}
			mustCount(t, db, 1)
		})
	}
}

func TestFaultFailsTheChosenStatementOutsideATransaction(t *testing.T) {
	t.Parallel()

	for _, way := range statementWays {
		t.Run(way.name, func(t *testing.T) {
			t.Parallel()
			faults := &sqlitetest.Faults{}
			db := faultOpen(t, faults)
			faults.FailStatement(faultChosen, errInjected)

			err := way.run(t.Context(), db, faultChosen)

			if !errors.Is(err, errInjected) {
				t.Errorf("%s(%q) error = %v, want the injected error", way.name, faultChosen, err)
			}
			mustExec(t, db, faultInsert)
			mustCount(t, db, 1)
		})
	}
}

func TestAnUnchosenStatementRunsUntouched(t *testing.T) {
	t.Parallel()

	unchosen := []string{
		faultChosen + " ",
		" " + faultChosen,
		strings.ToLower(faultChosen),
		"SELECT count(*) FROM fault_rows WHERE a = 2",
	}
	for _, way := range statementWays {
		t.Run(way.name, func(t *testing.T) {
			t.Parallel()
			faults := &sqlitetest.Faults{}
			db := faultOpen(t, faults)
			faults.FailStatement(faultChosen, errInjected)
			tx := mustBegin(t, db)
			defer func() { _ = tx.Rollback() }()

			for _, query := range unchosen {
				if err := way.run(t.Context(), db, query); err != nil {
					t.Errorf("%s(%q) error = %v, want it untouched", way.name, query, err)
				}
				if err := way.run(t.Context(), tx, query); err != nil {
					t.Errorf("%s(%q) in a transaction error = %v, want it untouched", way.name, query, err)
				}
			}
		})
	}
}

// mustPrepareChosen prepares the chosen statement on on, closes it at cleanup and fails the test when it cannot.
func mustPrepareChosen(t *testing.T, on querier) *sql.Stmt {
	t.Helper()
	stmt, err := on.PrepareContext(t.Context(), faultChosen)
	if err != nil {
		t.Fatalf("PrepareContext(%q) error = %v, want nil", faultChosen, err)
	}
	t.Cleanup(func() { _ = stmt.Close() })
	return stmt
}

// mustConn takes one connection of db, closes it at cleanup and fails the test when it cannot.
func mustConn(t *testing.T, db *sql.DB) *sql.Conn {
	t.Helper()
	conn, err := db.Conn(t.Context())
	if err != nil {
		t.Fatalf("Conn() error = %v, want nil", err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	return conn
}

// mustBeginUntilCleanup starts a transaction on db, rolls it back at cleanup and fails the test when it cannot.
func mustBeginUntilCleanup(t *testing.T, db *sql.DB) *sql.Tx {
	t.Helper()
	tx := mustBegin(t, db)
	t.Cleanup(func() { _ = tx.Rollback() })
	return tx
}

// preparedWay is one way a prepared statement runs.
type preparedWay struct {
	// name names the way.
	name string
	// run runs stmt and returns its error.
	run func(ctx context.Context, stmt *sql.Stmt) error
}

// preparedWays runs a prepared statement as an Exec, a Query and a QueryRow.
var preparedWays = []preparedWay{
	{"Exec", func(ctx context.Context, stmt *sql.Stmt) error {
		_, err := stmt.ExecContext(ctx)
		return err
	}},
	{"Query", func(ctx context.Context, stmt *sql.Stmt) error {
		rows, err := stmt.QueryContext(ctx)
		if err != nil {
			return err
		}
		return rows.Close()
	}},
	{"QueryRow", func(ctx context.Context, stmt *sql.Stmt) error {
		var n int64
		return stmt.QueryRowContext(ctx).Scan(&n)
	}},
}

// preparation is one place a statement is prepared before its fault.
type preparation struct {
	// name names the place.
	name string
	// prepare prepares the chosen statement on db in that place.
	prepare func(t *testing.T, db *sql.DB) *sql.Stmt
}

// preparations prepares the chosen statement on the handle, inside a transaction and on the handle for a transaction.
var preparations = []preparation{
	{"on the handle", func(t *testing.T, db *sql.DB) *sql.Stmt {
		return mustPrepareChosen(t, db)
	}},
	{"inside a transaction", func(t *testing.T, db *sql.DB) *sql.Stmt {
		return mustPrepareChosen(t, mustBeginUntilCleanup(t, db))
	}},
	{"on the handle and moved into a transaction", func(t *testing.T, db *sql.DB) *sql.Stmt {
		stmt := mustPrepareChosen(t, db)
		moved := mustBeginUntilCleanup(t, db).StmtContext(t.Context(), stmt)
		t.Cleanup(func() { _ = moved.Close() })
		return moved
	}},
}

func TestFaultFailsAStatementPreparedBeforeIt(t *testing.T) {
	t.Parallel()

	for _, place := range preparations {
		for _, way := range preparedWays {
			t.Run(place.name+" "+way.name, func(t *testing.T) {
				t.Parallel()
				faults := &sqlitetest.Faults{}
				stmt := place.prepare(t, faultOpen(t, faults))
				if err := way.run(t.Context(), stmt); err != nil {
					t.Fatalf("%s before the fault error = %v, want nil", way.name, err)
				}
				faults.FailStatement(faultChosen, errInjected)

				err := way.run(t.Context(), stmt)

				if !errors.Is(err, errInjected) {
					t.Errorf("%s of a statement prepared %s error = %v, want the injected error", way.name, place.name, err)
				}
			})
		}
	}
}

func TestFaultFailsAPreparedStatementOnItsFirstAndASecondConnection(t *testing.T) {
	t.Parallel()

	faults := &sqlitetest.Faults{}
	db := faultOpen(t, faults)
	stmt := mustPrepareChosen(t, db)
	faults.FailStatement(faultChosen, errInjected)
	var n int64

	first := stmt.QueryRowContext(t.Context()).Scan(&n)
	mustConn(t, db)
	second := stmt.QueryRowContext(t.Context()).Scan(&n)

	if !errors.Is(first, errInjected) {
		t.Errorf("QueryRow on the connection the statement was prepared on error = %v, want the injected error", first)
	}
	if !errors.Is(second, errInjected) {
		t.Errorf("QueryRow on a second connection error = %v, want the injected error", second)
	}
	if got := db.Stats().OpenConnections; got != 2 {
		t.Errorf("Stats().OpenConnections = %d, want 2 with the first one held", got)
	}
}

func TestAStatementTheDriverCannotPrepareKeepsTheDriversError(t *testing.T) {
	t.Parallel()

	conn := mustConn(t, faultOpen(t, &sqlitetest.Faults{}))
	var stmt driver.Stmt

	err := conn.Raw(func(raw any) error {
		var prepareErr error
		stmt, prepareErr = raw.(driver.ConnPrepareContext).PrepareContext(t.Context(), faultUnparsable)
		return prepareErr
	})

	if err == nil || !strings.Contains(err.Error(), "syntax error") {
		t.Errorf("PrepareContext(%q) error = %v, want the driver's syntax error", faultUnparsable, err)
	}
	if stmt != nil {
		t.Errorf("PrepareContext(%q) statement = %T, want none beside the error", faultUnparsable, stmt)
	}
}

// mustTakeEvery takes every connection of a full pool, each opened after the ones before, and closes them at cleanup.
func mustTakeEvery(t *testing.T, db *sql.DB) []*sql.Conn {
	t.Helper()
	conns := make([]*sql.Conn, testMaxConns)
	for i := range conns {
		conns[i] = mustConn(t, db)
	}
	if got := db.Stats().OpenConnections; got != testMaxConns {
		t.Fatalf("Stats().OpenConnections = %d, want %d fresh connections", got, testMaxConns)
	}
	return conns
}

// mustRead returns the one value query answers on conn and fails the test when it cannot.
func mustRead[T any](t *testing.T, conn *sql.Conn, query string, args ...any) T {
	t.Helper()
	var value T
	if err := conn.QueryRowContext(t.Context(), query, args...).Scan(&value); err != nil {
		t.Fatalf("%s error = %v, want nil", query, err)
	}
	return value
}

// mustKeepTheRules fails the test unless connection i of a handle from testOptions carries every rule.
func mustKeepTheRules(t *testing.T, i int, conn *sql.Conn) {
	t.Helper()
	pragmas := map[string]int64{
		"PRAGMA foreign_keys": 1,
		"PRAGMA busy_timeout": testBusyTimeout.Milliseconds(),
		"PRAGMA temp_store":   2,
		"PRAGMA cache_size":   -testCacheSize,
		"PRAGMA synchronous":  1,
	}
	for query, want := range pragmas {
		if got := mustRead[int64](t, conn, query); got != want {
			t.Errorf("%s on connection %d = %d, want %d", query, i, got, want)
		}
	}
	if got := mustRead[string](t, conn, "PRAGMA journal_mode"); got != "wal" {
		t.Errorf("PRAGMA journal_mode on connection %d = %q, want wal", i, got)
	}
	moment := time.Date(2026, time.October, 6, 12, 30, 45, 123456789, time.UTC)
	if got := mustRead[string](t, conn, "SELECT typeof(?)", moment); got != "integer" {
		t.Errorf("a time.Time on connection %d binds as %s, want integer", i, got)
	}
	mustExec(t, conn, "PRAGMA writable_schema=ON")
	_, err := conn.ExecContext(t.Context(), "UPDATE sqlite_schema SET sql = sql WHERE name = 'fault_rows'")
	if err == nil || !strings.Contains(err.Error(), "may not be modified") {
		t.Errorf("an edit of the schema table on connection %d error = %v, want it refused", i, err)
	}
}

// errMissing is what a probe answers when the raw connection lacks its interface.
var errMissing = errors.New("sqlitetest test: the raw connection lacks the interface")

// driverProbe calls one interface the driver's connection offers to database/sql on a raw connection.
type driverProbe struct {
	// name names the interface.
	name string
	// probe calls the interface on raw and returns its error, or errMissing.
	probe func(ctx context.Context, raw any) error
}

// driverProbes calls every interface the driver's connection offers to database/sql.
var driverProbes = []driverProbe{
	{"driver.ConnBeginTx", func(ctx context.Context, raw any) error {
		c, ok := raw.(driver.ConnBeginTx)
		if !ok {
			return errMissing
		}
		tx, err := c.BeginTx(ctx, driver.TxOptions{})
		if err != nil {
			return err
		}
		return tx.Rollback()
	}},
	{"driver.ConnPrepareContext", func(ctx context.Context, raw any) error {
		c, ok := raw.(driver.ConnPrepareContext)
		if !ok {
			return errMissing
		}
		stmt, err := c.PrepareContext(ctx, faultCount)
		if err != nil {
			return err
		}
		return stmt.Close()
	}},
	{"driver.ExecerContext", func(ctx context.Context, raw any) error {
		c, ok := raw.(driver.ExecerContext)
		if !ok {
			return errMissing
		}
		_, err := c.ExecContext(ctx, faultInsert, nil)
		return err
	}},
	{"driver.QueryerContext", func(ctx context.Context, raw any) error {
		c, ok := raw.(driver.QueryerContext)
		if !ok {
			return errMissing
		}
		rows, err := c.QueryContext(ctx, faultCount, nil)
		if err != nil {
			return err
		}
		return rows.Close()
	}},
	{"driver.Pinger", func(ctx context.Context, raw any) error {
		c, ok := raw.(driver.Pinger)
		if !ok {
			return errMissing
		}
		return c.Ping(ctx)
	}},
	{"driver.SessionResetter", func(ctx context.Context, raw any) error {
		c, ok := raw.(driver.SessionResetter)
		if !ok {
			return errMissing
		}
		return c.ResetSession(ctx)
	}},
	{"driver.Validator", func(_ context.Context, raw any) error {
		c, ok := raw.(driver.Validator)
		if !ok {
			return errMissing
		}
		if !c.IsValid() {
			return errors.New("sqlitetest test: IsValid() = false")
		}
		return nil
	}},
}

func TestTheWrappedConnectionKeepsEveryDriverInterface(t *testing.T) {
	t.Parallel()

	conn := mustConn(t, faultOpen(t, &sqlitetest.Faults{}))

	for _, p := range driverProbes {
		if err := conn.Raw(func(raw any) error { return p.probe(t.Context(), raw) }); err != nil {
			t.Errorf("%s on the wrapped connection: %v, want it offered and answering", p.name, err)
		}
	}
}

// errStmtMissing is what a probe answers when the raw statement lacks its interface.
var errStmtMissing = errors.New("sqlitetest test: the raw statement lacks the interface")

// stmtProbe calls one interface the driver's statement offers to database/sql on a prepared faultEcho.
type stmtProbe struct {
	// name names the interface.
	name string
	// probe calls the interface on stmt and returns its error, or errStmtMissing.
	probe func(ctx context.Context, stmt driver.Stmt) error
}

// echoArgs passes echoed as the one named argument of faultEcho.
var echoArgs = []driver.NamedValue{{Ordinal: 1, Value: echoed}}

// stmtProbes calls every interface the driver's statement offers to database/sql.
var stmtProbes = []stmtProbe{
	{"driver.Stmt NumInput", func(_ context.Context, stmt driver.Stmt) error {
		if n := stmt.NumInput(); n != -1 {
			return fmt.Errorf("NumInput() = %d, want the driver's -1", n)
		}
		return nil
	}},
	{"driver.StmtExecContext", func(ctx context.Context, stmt driver.Stmt) error {
		s, ok := stmt.(driver.StmtExecContext)
		if !ok {
			return errStmtMissing
		}
		_, err := s.ExecContext(ctx, echoArgs)
		return err
	}},
	{"driver.StmtQueryContext", func(ctx context.Context, stmt driver.Stmt) error {
		s, ok := stmt.(driver.StmtQueryContext)
		if !ok {
			return errStmtMissing
		}
		got, err := firstValue(s.QueryContext(ctx, echoArgs))
		if err == nil && got != echoed {
			return fmt.Errorf("QueryContext(%d) = %v, want %d", echoed, got, echoed)
		}
		return err
	}},
}

func TestTheWrappedStatementKeepsEveryDriverInterface(t *testing.T) {
	t.Parallel()

	conn := mustConn(t, faultOpen(t, &sqlitetest.Faults{}))

	err := conn.Raw(func(raw any) error {
		stmt, err := raw.(driver.ConnPrepareContext).PrepareContext(t.Context(), faultEcho)
		if err != nil {
			return err
		}
		defer func() { _ = stmt.Close() }()
		for _, p := range stmtProbes {
			if err := p.probe(t.Context(), stmt); err != nil {
				t.Errorf("%s on the wrapped statement: %v, want it offered and answering", p.name, err)
			}
		}
		return nil
	})

	if err != nil {
		t.Errorf("PrepareContext(%q) error = %v, want nil", faultEcho, err)
	}
}

// olderConn is the connection interface database/sql used before contexts.
type olderConn interface {
	// Prepare prepares a statement.
	Prepare(query string) (driver.Stmt, error)
	// Begin starts a transaction.
	Begin() (driver.Tx, error)
}

// olderCalls sends the chosen statement through Prepare and commits an insert through Begin on raw.
func olderCalls(raw any) error {
	c := raw.(olderConn)
	if _, err := c.Prepare(faultChosen); !errors.Is(err, errInjected) {
		return fmt.Errorf("Prepare(%q) error = %v, want the injected error", faultChosen, err)
	}
	tx, err := c.Begin()
	if err != nil {
		return fmt.Errorf("Begin() error = %w, want nil", err)
	}
	if _, err := raw.(driver.ExecerContext).ExecContext(context.Background(), faultInsert, nil); err != nil {
		return fmt.Errorf("ExecContext(%q) error = %w, want nil", faultInsert, err)
	}
	if err := tx.Commit(); !errors.Is(err, errInjected) {
		return fmt.Errorf("Commit() error = %v, want the injected error", err)
	}
	return nil
}

func TestTheOlderDriverCallsAnswerTheFaults(t *testing.T) {
	t.Parallel()

	faults := &sqlitetest.Faults{}
	db := faultOpen(t, faults)
	faults.FailStatement(faultChosen, errInjected)
	faults.FailNextCommit(errInjected)
	conn := mustConn(t, db)

	if err := conn.Raw(olderCalls); err != nil {
		t.Error(err)
	}
	mustCount(t, db, 0)
}

// olderStmt is the statement interface database/sql used before contexts.
type olderStmt interface {
	// Exec runs the statement with args.
	Exec(args []driver.Value) (driver.Result, error)
	// Query runs the statement with args.
	Query(args []driver.Value) (driver.Rows, error)
}

// firstValue returns the first column of the first row of rows, read when err is nil, and closes rows.
func firstValue(rows driver.Rows, err error) (driver.Value, error) {
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	dest := make([]driver.Value, len(rows.Columns()))
	if err := rows.Next(dest); err != nil {
		return nil, err
	}
	return dest[0], nil
}

// olderStatementCalls prepares faultEcho on raw and runs it through Exec and Query before and after faults choose it.
func olderStatementCalls(ctx context.Context, raw any, faults *sqlitetest.Faults) error {
	stmt, err := raw.(driver.ConnPrepareContext).PrepareContext(ctx, faultEcho)
	if err != nil {
		return fmt.Errorf("PrepareContext(%q) error = %w, want nil", faultEcho, err)
	}
	defer func() { _ = stmt.Close() }()
	older, args := stmt.(olderStmt), []driver.Value{echoed}
	if _, err := older.Exec(args); err != nil {
		return fmt.Errorf("Exec(%d) before the fault error = %w, want nil", echoed, err)
	}
	if got, err := firstValue(older.Query(args)); err != nil || got != echoed {
		return fmt.Errorf("Query(%d) before the fault = %v, %v, want %d", echoed, got, err, echoed)
	}
	faults.FailStatement(faultEcho, errInjected)
	if _, err := older.Exec(args); !errors.Is(err, errInjected) {
		return fmt.Errorf("Exec(%d) error = %v, want the injected error", echoed, err)
	}
	if _, err := firstValue(older.Query(args)); !errors.Is(err, errInjected) {
		return fmt.Errorf("Query(%d) error = %v, want the injected error", echoed, err)
	}
	return nil
}

func TestTheOlderStatementCallsAnswerTheFaults(t *testing.T) {
	t.Parallel()

	faults := &sqlitetest.Faults{}
	conn := mustConn(t, faultOpen(t, faults))

	err := conn.Raw(func(raw any) error { return olderStatementCalls(t.Context(), raw, faults) })

	if err != nil {
		t.Error(err)
	}
}

// faultRounds is how many statements each goroutine of the concurrency test runs.
const faultRounds = 20

// faultRace chooses a statement of its own on faults and runs it with an unchosen one on db for faultRounds rounds.
func faultRace(ctx context.Context, db *sql.DB, faults *sqlitetest.Faults, own string, start <-chan struct{}) error {
	<-start
	for range faultRounds {
		faults.FailStatement(own, errInjected)
		if _, err := db.ExecContext(ctx, own); !errors.Is(err, errInjected) {
			return fmt.Errorf("ExecContext(%q) error = %v, want the injected error", own, err)
		}
		if _, err := db.ExecContext(ctx, faultCount); err != nil {
			return fmt.Errorf("ExecContext(%q) error = %w, want it untouched", faultCount, err)
		}
	}
	return nil
}

func TestFaultsServeEveryConnectionAtOnce(t *testing.T) {
	t.Parallel()

	faults := &sqlitetest.Faults{}
	db := faultOpen(t, faults)
	start, done := make(chan struct{}), make(chan error, testMaxConns)
	for i := range testMaxConns {
		go func() { done <- faultRace(t.Context(), db, faults, fmt.Sprintf("SELECT %d", i), start) }()
	}

	close(start)

	for range testMaxConns {
		if err := <-done; err != nil {
			t.Error(err)
		}
	}
}

func TestABusyBeginKeepsItsErrorThroughTheWrapper(t *testing.T) {
	t.Parallel()

	opts := testOptions()
	opts.BusyTimeout = testShortBusyTimeout
	db := sqlitetest.OpenWithFaults(t, opts, &sqlitetest.Faults{})
	writer := mustBegin(t, db)
	defer func() { _ = writer.Rollback() }()
	began := time.Now()

	_, err := db.BeginTx(t.Context(), nil)

	waited := time.Since(began)
	if err == nil {
		t.Fatal("BeginTx() error = nil, want busy while another writer holds the lock")
	}
	if got := sqlite.Classify(err); !errors.Is(got, dbkit.ErrBusy) {
		t.Errorf("Classify(%v) = %v, want the busy class", err, got)
	}
	if waited < testShortBusyTimeout {
		t.Errorf("BeginTx() answered after %v, want at least the busy timeout %v", waited, testShortBusyTimeout)
	}
}

func TestTheRulesStayOnThroughAWrappedConnection(t *testing.T) {
	t.Parallel()

	functions, err := dbkit.NewFunctionList(dbkit.CaseFold())
	if err != nil {
		t.Fatalf("NewFunctionList() error = %v, want nil", err)
	}
	opts := testOptions()
	opts.Functions = functions
	faults := &sqlitetest.Faults{}
	db := sqlitetest.OpenWithFaults(t, opts, faults)
	mustExec(t, db, faultSchema)
	faults.FailStatement(faultChosen, errInjected)

	for i, conn := range mustTakeEvery(t, db) {
		mustKeepTheRules(t, i, conn)
		if got := mustRead[string](t, conn, "SELECT casefold('ÑANDÚ')"); got != "ñandú" {
			t.Errorf("casefold('ÑANDÚ') on connection %d = %q, want ñandú", i, got)
		}
		var n int64
		if err := conn.QueryRowContext(t.Context(), faultChosen).Scan(&n); !errors.Is(err, errInjected) {
			t.Errorf("the chosen statement on connection %d error = %v, want the injected error", i, err)
		}
	}
}
