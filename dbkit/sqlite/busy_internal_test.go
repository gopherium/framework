// SPDX-License-Identifier: Apache-2.0

package sqlite

import (
	"cmp"
	"context"
	"database/sql/driver"
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"modernc.org/libc"
	sqlite3 "modernc.org/sqlite/lib"
)

const (
	// internalCount is a busy count past the first call of a wait.
	internalCount = 7
	// internalFakeHandle is a handle number no SQLite connection has.
	internalFakeHandle = 1
	// internalUpstreamMethod is the method a driver connection gains with an upstream busy handler.
	internalUpstreamMethod = "RegisterBusyHandler"
	// internalKnownMethod is a method of the driver connection the lookup must find.
	internalKnownMethod = "Ping"
	// internalTypeName is the database type name the typed rows answer.
	internalTypeName = "DECIMAL"
	// internalLength is the column length the typed rows answer.
	internalLength = 20
	// internalPrecision is the precision the typed rows answer.
	internalPrecision = 10
	// internalScale is the scale the typed rows answer.
	internalScale = 2
)

func TestEndedMarksAnErrorOnlyOnceItsContextEnded(t *testing.T) {
	t.Parallel()

	cancelled, cancel := context.WithCancel(t.Context())
	cancel()

	if got := ended(t.Context(), errStepFailed); got != errStepFailed {
		t.Errorf("ended() under a live context = %v, want the error as it is", got)
	}
	got := ended(cancelled, errStepFailed)
	if !errors.Is(got, errStepFailed) || !errors.Is(got, context.Canceled) {
		t.Errorf("ended() under a cancelled context = %v, want an error matching both the error and context.Canceled",
			got)
	}
	if want := errStepFailed.Error() + ": " + context.Canceled.Error(); got == nil || got.Error() != want {
		t.Errorf("ended() under a cancelled context = %v, want %q", got, want)
	}
}

// internalHandleConn is a driver connection with a field db and only the methods of internalConn.
type internalHandleConn struct {
	// internalConn gives the connection the methods of driver.Conn and ExecContext.
	internalConn
	// db is the handle number the connection holds.
	db uintptr
}

// internalEmbeddedConn is a driver connection whose field db comes through an embedded pointer.
type internalEmbeddedConn struct {
	// internalHandleConn gives the connection a field db, left nil.
	*internalHandleConn
}

// internalWrongHandleConn is a driver connection whose field db is no number.
type internalWrongHandleConn struct {
	// internalConn gives the connection the methods of driver.Conn and ExecContext.
	internalConn
	// db is a handle of the wrong type.
	db string
}

// internalNumberConn is a driver connection that is a number, not a struct.
type internalNumberConn int

// Prepare refuses every statement.
func (internalNumberConn) Prepare(string) (driver.Stmt, error) {
	return nil, errors.New("busy test: no statement is prepared on an internalNumberConn")
}

// Close does nothing.
func (internalNumberConn) Close() error {
	return nil
}

// Begin refuses every transaction.
func (internalNumberConn) Begin() (driver.Tx, error) {
	return nil, errors.New("busy test: no transaction runs on an internalNumberConn")
}

// internalNoHandle returns the error text of handleOf for a connection of the type named kind with no handle.
func internalNoHandle(kind string) string {
	return "dbkit: the SQLite driver connection " + kind +
		" holds no handle in its field db, so a busy wait cannot end with its context"
}

func TestHandleOfRefusesAConnectionWithoutAHandle(t *testing.T) {
	t.Parallel()

	cases := []struct {
		// name names the connection.
		name string
		// conn is the connection handleOf reads.
		conn driver.Conn
		// want is the error handleOf answers.
		want string
	}{
		{"a struct with no field db", &internalConn{}, internalNoHandle("*sqlite.internalConn")},
		{"a field db of another type", &internalWrongHandleConn{db: "site.db"},
			internalNoHandle("*sqlite.internalWrongHandleConn")},
		{"a field db of zero", &internalHandleConn{}, internalNoHandle("*sqlite.internalHandleConn")},
		{"a field db behind an embedded pointer", &internalEmbeddedConn{}, internalNoHandle("*sqlite.internalEmbeddedConn")},
		{"a value that is no struct", internalNumberConn(0),
			"dbkit: the SQLite driver connection sqlite.internalNumberConn is not a struct, so a busy wait cannot end " +
				"with its context"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()

			db, err := handleOf(c.conn)

			if db != 0 {
				t.Errorf("handleOf() handle = %d, want 0", db)
			}
			if got := fmt.Sprint(err); got != c.want {
				t.Errorf("handleOf() error = %s, want %q", got, c.want)
			}
		})
	}
}

func TestConnectFailsClosedWithoutAHandle(t *testing.T) {
	t.Parallel()

	fake := &internalConn{}
	c, _ := internalConnector(fake, nil)
	c.wait = c.waitOn

	conn, err := c.Connect(t.Context())

	if conn != nil {
		t.Errorf("Connect() connection = %v, want nil", conn)
	}
	if got, want := fmt.Sprint(err), internalNoHandle("*sqlite.internalConn"); got != want {
		t.Errorf("Connect() error = %s, want %q", got, want)
	}
	if !fake.closed {
		t.Errorf("closed = %v, want the connection closed", fake.closed)
	}
}

// internalWaitHolds reports whether busyWaits holds a wait on the handle db.
func internalWaitHolds(db uintptr) bool {
	held := false
	busyWaits.Range(func(_, v any) bool {
		held = v.(*busyWait).db == db
		return !held
	})
	return held
}

func TestWaitOnRefusesAConnectionMissingAnInterface(t *testing.T) {
	t.Parallel()

	c, _ := internalConnector(nil, nil)

	conn, err := c.waitOn(&internalHandleConn{db: internalFakeHandle})

	want := "dbkit: the SQLite driver connection *sqlite.internalHandleConn lacks an interface database/sql uses"
	if conn != nil {
		t.Errorf("waitOn() connection = %v, want nil", conn)
	}
	if got := fmt.Sprint(err); got != want {
		t.Errorf("waitOn() error = %s, want %q", got, want)
	}
	if internalWaitHolds(internalFakeHandle) {
		t.Errorf("busyWaits holds a wait on the handle %d, want SQLite never handed the number", internalFakeHandle)
	}
}

// internalRealConn returns a connection Connect opened to a fresh file, closed when the test ends.
func internalRealConn(t *testing.T) *busyConn {
	t.Helper()
	if err := ofdOnce(); err != nil {
		t.Fatalf("ofdOnce() error = %v, want nil", err)
	}
	c := newConnector(internalDriver(t), filepath.Join(t.TempDir(), "site.db"), internalOptions())
	conn, err := c.Connect(t.Context())
	if err != nil {
		t.Fatalf("Connect() error = %v, want nil", err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	waiting, ok := conn.(*busyConn)
	if !ok {
		t.Fatalf("Connect() = %T, want a *busyConn", conn)
	}
	return waiting
}

// internalHandler calls busyHandler with a fresh libc TLS for the wait of id at count.
func internalHandler(id uintptr, count int32) int32 {
	tls := libc.NewTLS()
	defer tls.Close()
	return busyHandler(tls, id, count)
}

func TestAClosedConnectionForgetsItsWait(t *testing.T) {
	t.Parallel()

	conn := internalRealConn(t)
	if _, ok := busyWaits.Load(conn.id); !ok {
		t.Fatalf("busyWaits holds no wait for the open connection %d, want its wait", conn.id)
	}

	if err := conn.Close(); err != nil {
		t.Fatalf("Close() error = %v, want nil", err)
	}

	if _, ok := busyWaits.Load(conn.id); ok {
		t.Errorf("busyWaits still holds the wait of the closed connection %d, want it forgotten", conn.id)
	}
	if got := internalHandler(conn.id, 0); got != 0 {
		t.Errorf("busyHandler() for a forgotten wait = %d, want 0 to answer busy", got)
	}
}

func TestAWaitThatStartsAboveCountZeroStartsFresh(t *testing.T) {
	t.Parallel()

	conn := internalRealConn(t)
	conn.wait.began = time.Now().Add(-internalUnreachedWait)
	conn.wait.span.Store(&callSpan{})

	got := internalHandler(conn.id, internalCount)

	if got != 1 {
		t.Errorf("busyHandler() at count %d of a call's first wait = %d, want 1 to try the lock again", internalCount,
			got)
	}
}

func TestAWaitOutsideACallKeepsItsStartPastCountZero(t *testing.T) {
	t.Parallel()

	conn := internalRealConn(t)
	conn.wait.began = time.Now().Add(-internalUnreachedWait)

	got := internalHandler(conn.id, internalCount)

	if got != 0 {
		t.Errorf("busyHandler() at count %d of a wait an hour old = %d, want 0 to answer busy", internalCount, got)
	}
}

func TestAWaitOutsideACallStartsFreshAtCountZero(t *testing.T) {
	t.Parallel()

	conn := internalRealConn(t)
	conn.wait.began = time.Now().Add(-internalUnreachedWait)

	got := internalHandler(conn.id, 0)

	if got != 1 {
		t.Errorf("busyHandler() at count 0 after a wait an hour old = %d, want 1 to try the lock again", got)
	}
}

func TestAnInterruptedHandleAnswersBusyDuringACallWhoseContextIsLive(t *testing.T) {
	t.Parallel()

	conn := internalRealConn(t)
	defer conn.wait.during(t.Context())()
	tls := libc.NewTLS()
	defer tls.Close()
	sqlite3.Xsqlite3_interrupt(tls, conn.wait.db)

	got := internalHandler(conn.id, 0)

	if got != 0 {
		t.Errorf("busyHandler() at count 0 on an interrupted handle during a live call = %d, want 0 to answer busy", got)
	}
}

// internalBusyConn is a driver connection whose every call answers a busy error.
type internalBusyConn struct {
	// driverConn is left nil.
	driverConn
	// busy is the error every call answers with.
	busy error
}

// BeginTx answers the busy error.
func (c *internalBusyConn) BeginTx(context.Context, driver.TxOptions) (driver.Tx, error) {
	return nil, c.busy
}

// ExecContext answers the busy error.
func (c *internalBusyConn) ExecContext(context.Context, string, []driver.NamedValue) (driver.Result, error) {
	return nil, c.busy
}

// QueryContext answers the busy error.
func (c *internalBusyConn) QueryContext(context.Context, string, []driver.NamedValue) (driver.Rows, error) {
	return nil, c.busy
}

// PrepareContext answers the busy error.
func (c *internalBusyConn) PrepareContext(context.Context, string) (driver.Stmt, error) {
	return nil, c.busy
}

// Ping answers the busy error.
func (c *internalBusyConn) Ping(context.Context) error {
	return c.busy
}

// internalStatementCalls returns each statement call of conn as a function of its context.
func internalStatementCalls(conn *busyConn) map[string]func(ctx context.Context) error {
	return map[string]func(ctx context.Context) error{
		"ExecContext": func(ctx context.Context) error {
			_, err := conn.ExecContext(ctx, internalOptimize, nil)
			return err
		},
		"QueryContext": func(ctx context.Context) error {
			_, err := conn.QueryContext(ctx, internalOptimize, nil)
			return err
		},
		"PrepareContext": func(ctx context.Context) error {
			_, err := conn.PrepareContext(ctx, internalOptimize)
			return err
		},
		"Ping": conn.Ping,
	}
}

func TestAStatementAfterItsContextEndedAnswersTheContextError(t *testing.T) {
	t.Parallel()

	busy := internalBusyError(t)
	conn := &busyConn{driverConn: &internalBusyConn{busy: busy}, wait: &busyWait{}}
	cancelled, cancel := context.WithCancel(t.Context())
	cancel()

	for name, call := range internalStatementCalls(conn) {
		if err := call(cancelled); err != context.Canceled {
			t.Errorf("%s under a cancelled context error = %v, want exactly context.Canceled", name, err)
		}
		if err := call(t.Context()); err != busy {
			t.Errorf("%s under a live context error = %v, want the busy error as it is", name, err)
		}
	}
}

func TestABeginAfterItsContextEndedAnswersTheContextError(t *testing.T) {
	t.Parallel()

	busy := internalBusyError(t)
	conn := &busyConn{driverConn: &internalBusyConn{busy: busy}, wait: &busyWait{}}
	cancelled, cancel := context.WithCancel(t.Context())
	cancel()

	_, err := conn.BeginTx(cancelled, driver.TxOptions{})
	_, live := conn.BeginTx(t.Context(), driver.TxOptions{})

	if err != context.Canceled {
		t.Errorf("BeginTx() under a cancelled context error = %v, want exactly context.Canceled", err)
	}
	if live != busy {
		t.Errorf("BeginTx() under a live context error = %v, want the busy error as it is", live)
	}
}

// internalRows is a result set with no rows that records its Close.
type internalRows struct {
	// closed reports that Close ran.
	closed bool
}

// Columns names no column.
func (*internalRows) Columns() []string {
	return nil
}

// Close records that it ran.
func (r *internalRows) Close() error {
	r.closed = true
	return nil
}

// Next answers the end of the rows.
func (*internalRows) Next([]driver.Value) error {
	return io.EOF
}

// internalRowsConn is a driver connection whose every query answers its rows.
type internalRowsConn struct {
	// driverConn is left nil.
	driverConn
	// rows is the result set every query answers.
	rows driver.Rows
}

// QueryContext answers the rows of the connection.
func (c *internalRowsConn) QueryContext(context.Context, string, []driver.NamedValue) (driver.Rows, error) {
	return c.rows, nil
}

// internalQuery runs a query under ctx through a wrapper whose driver answers rows, and returns what it answered.
func internalQuery(ctx context.Context, rows *internalRows) (driver.Rows, error) {
	conn := &busyConn{driverConn: &internalRowsConn{rows: rows}, wait: &busyWait{}}
	return conn.QueryContext(ctx, internalOptimize, nil)
}

func TestAQueryThatAnswersAfterItsContextEndedAnswersTheContextError(t *testing.T) {
	t.Parallel()

	cancelled, cancel := context.WithCancel(t.Context())
	cancel()
	ended := &internalRows{}

	rows, err := internalQuery(cancelled, ended)

	if rows != nil || err != context.Canceled {
		t.Errorf("QueryContext() under a cancelled context = %v, %v, want no rows and exactly context.Canceled", rows, err)
	}
	if !ended.closed {
		t.Errorf("closed = %v, want the rows of a query whose context ended closed", ended.closed)
	}
}

func TestAQueryUnderALiveContextAnswersItsRows(t *testing.T) {
	t.Parallel()

	live := &internalRows{}

	rows, err := internalQuery(t.Context(), live)

	if rows != driver.Rows(live) || err != nil {
		t.Errorf("QueryContext() under a live context = %v, %v, want its rows and nil", rows, err)
	}
	if live.closed {
		t.Errorf("closed = %v, want the rows of a live query left open", live.closed)
	}
}

// internalTypedRows is a result set with no rows that answers each column type question with a value of its own.
type internalTypedRows struct {
	// internalRows gives the rows Columns, Next and a recorded Close.
	internalRows
}

// ColumnTypeDatabaseTypeName answers internalTypeName.
func (*internalTypedRows) ColumnTypeDatabaseTypeName(int) string {
	return internalTypeName
}

// ColumnTypeLength answers internalLength.
func (*internalTypedRows) ColumnTypeLength(int) (int64, bool) {
	return internalLength, true
}

// ColumnTypeNullable answers that the column holds no NULL.
func (*internalTypedRows) ColumnTypeNullable(int) (bool, bool) {
	return false, true
}

// ColumnTypePrecisionScale answers internalPrecision and internalScale.
func (*internalTypedRows) ColumnTypePrecisionScale(int) (int64, int64, bool) {
	return internalPrecision, internalScale, true
}

// ColumnTypeScanType answers the type of a bool.
func (*internalTypedRows) ColumnTypeScanType(int) reflect.Type {
	return reflect.TypeFor[bool]()
}

// internalTypedQuery returns a wrapper on wait whose driver answers typed to every query.
func internalTypedQuery(wait *busyWait, typed *internalTypedRows) *busyConn {
	return &busyConn{driverConn: &internalRowsConn{rows: typed}, wait: wait}
}

// internalSpanDone returns the Done channel the span of wait holds, nil when it holds no span.
func internalSpanDone(wait *busyWait) <-chan struct{} {
	if s := wait.span.Load(); s != nil {
		return s.done
	}
	return nil
}

// internalSpanRows is typed rows whose Next and Close record the Done channel the span of wait holds.
type internalSpanRows struct {
	// internalTypedRows answers every column type question.
	internalTypedRows
	// wait is the wait whose span Next and Close record.
	wait *busyWait
	// nextUnder is the Done channel the span held when Next last ran.
	nextUnder <-chan struct{}
	// closedUnder is the Done channel the span held when Close ran.
	closedUnder <-chan struct{}
}

// Next records the Done channel the span of wait holds and answers the end of the rows.
func (r *internalSpanRows) Next([]driver.Value) error {
	r.nextUnder = internalSpanDone(r.wait)
	return io.EOF
}

// Close records the Done channel the span of wait holds and fails with errStepFailed.
func (r *internalSpanRows) Close() error {
	r.closedUnder = internalSpanDone(r.wait)
	return errStepFailed
}

func TestTheRowsOfAQueryHoldItsSpanOnlyDuringNextAndClose(t *testing.T) {
	t.Parallel()

	wait := &busyWait{}
	spanRows := &internalSpanRows{wait: wait}
	ctx := t.Context()

	rows, err := (&busyConn{driverConn: &internalRowsConn{rows: spanRows}, wait: wait}).QueryContext(ctx,
		internalOptimize, nil)
	if err != nil {
		t.Fatalf("QueryContext() error = %v, want nil", err)
	}
	open := wait.span.Load()
	next := rows.Next(nil)
	between := wait.span.Load()
	closed := rows.Close()

	if open != nil || between != nil {
		t.Errorf("the span while the rows are open between calls = %+v, then %+v, want none", open, between)
	}
	internalMustReadUnder(t, "the query", spanRows, ctx)
	if next != io.EOF || closed != errStepFailed {
		t.Errorf("Next() then Close() error = %v, then %v, want the driver's %v, then %v", next, closed, io.EOF,
			errStepFailed)
	}
	if s := wait.span.Load(); s != nil {
		t.Errorf("the span after Close = %+v, want none", s)
	}
}

func TestANextAfterAnotherCallStillWaitsUnderTheContextOfItsQuery(t *testing.T) {
	t.Parallel()

	wait := &busyWait{}
	spanRows := &internalSpanRows{wait: wait}
	queries := &busyConn{driverConn: &internalRowsConn{rows: spanRows}, wait: wait}
	execs := &busyConn{driverConn: &internalBusyConn{busy: errStepFailed}, wait: wait}
	ctxs := internalContexts(t, "the query", "the statement")
	rows, err := queries.QueryContext(ctxs["the query"], internalOptimize, nil)
	if err != nil {
		t.Fatalf("QueryContext() error = %v, want nil", err)
	}

	_, _ = execs.ExecContext(ctxs["the statement"], internalOptimize, nil)
	_ = rows.Next(nil)
	_ = rows.Close()

	internalMustReadUnder(t, "the query", spanRows, ctxs["the query"])
	if s := wait.span.Load(); s != nil {
		t.Errorf("the span after Close = %+v, want none", s)
	}
}

func TestEveryQueryEndsItsSpanBeforeItAnswers(t *testing.T) {
	t.Parallel()

	cancelled, cancel := context.WithCancel(t.Context())
	cancel()
	cases := []struct {
		// name names the query.
		name string
		// ctx is the context of the query.
		ctx context.Context
		// driverConn is the driver connection that answers the query.
		driverConn driverConn
	}{
		{"a query the driver failed", t.Context(), &internalBusyConn{busy: errStepFailed}},
		{"a query whose context ended", cancelled, &internalRowsConn{rows: &internalTypedRows{}}},
		{"rows lacking a column type interface", t.Context(), &internalRowsConn{rows: &internalRows{}}},
		{"rows the wrapper keeps", t.Context(), &internalRowsConn{rows: &internalTypedRows{}}},
	}
	for _, c := range cases {
		wait := &busyWait{}

		rows, _ := (&busyConn{driverConn: c.driverConn, wait: wait}).QueryContext(c.ctx, internalOptimize, nil)

		if s := wait.span.Load(); s != nil {
			t.Errorf("the span after %s = %+v, want none", c.name, s)
		}
		if rows != nil {
			_ = rows.Close()
		}
	}
}

func TestTheDriversRowsCloseUnderTheSpanOfTheirQuery(t *testing.T) {
	t.Parallel()

	cancelled, cancel := context.WithCancel(t.Context())
	cancel()
	for _, ctx := range []context.Context{t.Context(), cancelled} {
		wait := &busyWait{}
		spanRows := &internalSpanRows{wait: wait}
		conn := &busyConn{driverConn: &internalRowsConn{rows: spanRows}, wait: wait}

		rows, err := conn.QueryContext(ctx, internalOptimize, nil)
		if err == nil {
			err = rows.Close()
		}

		if spanRows.closedUnder != ctx.Done() {
			t.Errorf("the span when the driver's rows closed held %v, want the Done channel %v of their query",
				spanRows.closedUnder, ctx.Done())
		}
		if want := cmp.Or(ctx.Err(), errStepFailed); err != want {
			t.Errorf("QueryContext() then Close() error = %v, want %v as it is", err, want)
		}
		if s := wait.span.Load(); s != nil {
			t.Errorf("the span after the driver's rows closed = %+v, want none", s)
		}
	}
}

// internalColumnProbes asks rows each column type question and reports whether they answer as internalTypedRows.
var internalColumnProbes = []struct {
	// name names the interface.
	name string
	// answers reports whether rows offer the interface and answer it as internalTypedRows.
	answers func(rows driver.Rows) bool
}{
	{"driver.RowsColumnTypeDatabaseTypeName", func(rows driver.Rows) bool {
		r, ok := rows.(driver.RowsColumnTypeDatabaseTypeName)
		return ok && r.ColumnTypeDatabaseTypeName(0) == internalTypeName
	}},
	{"driver.RowsColumnTypeLength", func(rows driver.Rows) bool {
		r, ok := rows.(driver.RowsColumnTypeLength)
		if !ok {
			return false
		}
		length, known := r.ColumnTypeLength(0)
		return known && length == internalLength
	}},
	{"driver.RowsColumnTypeNullable", func(rows driver.Rows) bool {
		r, ok := rows.(driver.RowsColumnTypeNullable)
		if !ok {
			return false
		}
		nullable, known := r.ColumnTypeNullable(0)
		return known && !nullable
	}},
	{"driver.RowsColumnTypePrecisionScale", func(rows driver.Rows) bool {
		r, ok := rows.(driver.RowsColumnTypePrecisionScale)
		if !ok {
			return false
		}
		precision, scale, known := r.ColumnTypePrecisionScale(0)
		return known && precision == internalPrecision && scale == internalScale
	}},
	{"driver.RowsColumnTypeScanType", func(rows driver.Rows) bool {
		r, ok := rows.(driver.RowsColumnTypeScanType)
		return ok && r.ColumnTypeScanType(0) == reflect.TypeFor[bool]()
	}},
}

func TestTheRowsOfAQueryKeepEveryColumnTypeInterface(t *testing.T) {
	t.Parallel()

	rows, err := internalTypedQuery(&busyWait{}, &internalTypedRows{}).QueryContext(t.Context(), internalOptimize, nil)
	if err != nil {
		t.Fatalf("QueryContext() error = %v, want nil", err)
	}
	defer func() { _ = rows.Close() }()

	for _, p := range internalColumnProbes {
		if !p.answers(rows) {
			t.Errorf("%s on the rows of a query is missing or answers otherwise, want the driver's answer", p.name)
		}
	}
}

// internalDriverConn returns a connection the driver itself opened to a fresh file, closed when the test ends.
func internalDriverConn(t *testing.T) driver.Conn {
	t.Helper()
	if err := ofdOnce(); err != nil {
		t.Fatalf("ofdOnce() error = %v, want nil", err)
	}
	conn, err := internalDriver(t).Open(filepath.Join(t.TempDir(), "site.db"))
	if err != nil {
		t.Fatalf("Open() error = %v, want nil", err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	return conn
}

// internalOffers reports whether the type of conn has the method name, pointer receivers included.
func internalOffers(conn driver.Conn, name string) bool {
	_, ok := reflect.TypeOf(conn).MethodByName(name)
	return ok
}

func TestTheDriverOffersNoBusyHandlerYet(t *testing.T) {
	t.Parallel()

	raw := internalDriverConn(t)
	if _, err := handleOf(raw); err != nil {
		t.Fatalf("handleOf(%T) error = %v, want the driver's own connection", raw, err)
	}
	if !internalOffers(raw, internalKnownMethod) {
		t.Fatalf("the lookup finds no %s on %T, want it to see every method of the driver connection",
			internalKnownMethod, raw)
	}

	if internalOffers(raw, internalUpstreamMethod) {
		t.Errorf("modernc.org/sqlite now offers %s on %T, so replace the busy handler bridge in busy.go with it",
			internalUpstreamMethod, raw)
	}
}

func TestTheDriversRowsOfferEveryInterfaceTheWrapperKeepsAndNoOther(t *testing.T) {
	t.Parallel()

	rows, err := internalDriverConn(t).(driver.QueryerContext).QueryContext(t.Context(), "SELECT 1", nil)
	if err != nil {
		t.Fatalf("QueryContext() error = %v, want nil", err)
	}
	defer func() { _ = rows.Close() }()

	if _, ok := rows.(driverRows); !ok {
		t.Errorf("the driver's rows %T lack an interface of driverRows, so every query answers them unwrapped", rows)
	}
	if _, ok := rows.(driver.RowsNextResultSet); ok {
		t.Errorf("modernc.org/sqlite now offers driver.RowsNextResultSet on %T, so keep it in busyRows", rows)
	}
	if _, ok := rows.(driver.RowsColumnScanner); ok {
		t.Errorf("modernc.org/sqlite now offers driver.RowsColumnScanner on %T, so keep it in busyRows", rows)
	}
}

// internalColumnConverter is the one method of driver.ColumnConverter.
type internalColumnConverter interface {
	// ColumnConverter returns the converter of the column at idx.
	ColumnConverter(idx int) driver.ValueConverter
}

func TestTheDriversStatementOffersEveryInterfaceTheWrapperKeepsAndNoOther(t *testing.T) {
	t.Parallel()

	stmt, err := internalDriverConn(t).(driver.ConnPrepareContext).PrepareContext(t.Context(), "SELECT 1")
	if err != nil {
		t.Fatalf("PrepareContext() error = %v, want nil", err)
	}
	defer func() { _ = stmt.Close() }()

	if _, ok := stmt.(driverStmt); !ok {
		t.Errorf("the driver's statement %T lacks an interface of driverStmt, so every prepare answers it unwrapped", stmt)
	}
	if _, ok := stmt.(driver.NamedValueChecker); ok {
		t.Errorf("modernc.org/sqlite now offers driver.NamedValueChecker on %T, so keep it in busyStmt", stmt)
	}
	if _, ok := stmt.(internalColumnConverter); ok {
		t.Errorf("modernc.org/sqlite now offers driver.ColumnConverter on %T, so keep it in busyStmt", stmt)
	}
}
