// SPDX-License-Identifier: Apache-2.0

package sqlite

import (
	"context"
	"database/sql/driver"
	"fmt"
	"io"
	"reflect"
	"sync"
	"sync/atomic"
	"time"
	"unsafe"

	"modernc.org/libc"
	sqlite3 "modernc.org/sqlite/lib"
)

// busyWait is how one connection waits for a lock.
type busyWait struct {
	// db is the connection's SQLite handle.
	db uintptr
	// timeout is BusyTimeout.
	timeout time.Duration
	// poll is BusyPoll.
	poll time.Duration
	// began is when the current wait outside a call began.
	began time.Time
	// span is the driver call running on the connection, nil between calls.
	span atomic.Pointer[callSpan]
}

// callSpan is one driver call on a connection.
type callSpan struct {
	// done is the Done channel of the call's context, nil for a context that never ends.
	done <-chan struct{}
	// began is when the call's current wait began, zero until the call first waits.
	began time.Time
}

var (
	// busyWaits maps the id SQLite hands busyHandler to the wait of its connection.
	busyWaits sync.Map
	// lastWaitID is the id of the newest wait.
	lastWaitID atomic.Uintptr
)

// busyHandler is SQLite's busy callback for a dbkit connection, 1 to try the lock again and 0 to answer busy.
func busyHandler(tls *libc.TLS, id uintptr, count int32) int32 {
	v, ok := busyWaits.Load(id)
	if !ok {
		return 0
	}
	w := v.(*busyWait)
	if sqlite3.Xsqlite3_is_interrupted(tls, w.db) != 0 || !w.retry(count) {
		return 0
	}
	return 1
}

// retry waits one busy poll at most and reports whether to try the lock again.
func (w *busyWait) retry(count int32) bool {
	began, done := w.current()
	if count == 0 || began.IsZero() {
		*began = time.Now()
	}
	left := w.timeout - time.Since(*began)
	if left <= 0 {
		return false
	}
	poll := time.NewTimer(min(w.poll, left))
	defer poll.Stop()
	select {
	case <-done:
		return false
	case <-poll.C:
		return true
	}
}

// current returns the start of the running wait and the Done channel of the running call, nil outside a call.
func (w *busyWait) current() (*time.Time, <-chan struct{}) {
	if s := w.span.Load(); s != nil {
		return &s.began, s.done
	}
	return &w.began, nil
}

// busyHandlerFP is busyHandler as the function pointer SQLite calls.
var busyHandlerFP = *(*uintptr)(unsafe.Pointer(&struct {
	f func(*libc.TLS, uintptr, int32) int32
}{busyHandler}))

// handleOf returns the SQLite handle of a connection the driver opened, read from the driver's private field db.
func handleOf(conn driver.Conn) (uintptr, error) {
	v := reflect.ValueOf(conn)
	if v.Kind() == reflect.Pointer {
		v = v.Elem()
	}
	if v.Kind() != reflect.Struct {
		return 0, fmt.Errorf("dbkit: the SQLite driver connection %T is not a struct, so a busy wait cannot end "+
			"with its context", conn)
	}
	field, ok := v.Type().FieldByName("db")
	if !ok || len(field.Index) != 1 {
		return 0, fmt.Errorf("dbkit: the SQLite driver connection %T holds no handle in its field db, so a busy wait "+
			"cannot end with its context", conn)
	}
	f := v.Field(field.Index[0])
	if f.Kind() != reflect.Uintptr || f.Uint() == 0 {
		return 0, fmt.Errorf("dbkit: the SQLite driver connection %T holds no handle in its field db, so a busy wait "+
			"cannot end with its context", conn)
	}
	return uintptr(f.Uint()), nil
}

// driverConn is every interface the driver's connection offers to database/sql.
type driverConn interface {
	driver.Conn
	driver.ConnBeginTx
	driver.ConnPrepareContext
	driver.ExecerContext
	driver.QueryerContext
	driver.Pinger
	driver.SessionResetter
	driver.Validator
}

// waitOn points the busy callback of conn at busyHandler and returns conn wrapped so its waits end with their context.
func (c *connector) waitOn(conn driver.Conn) (driver.Conn, error) {
	db, err := handleOf(conn)
	if err != nil {
		return nil, err
	}
	full, ok := conn.(driverConn)
	if !ok {
		return nil, fmt.Errorf("dbkit: the SQLite driver connection %T lacks an interface database/sql uses", conn)
	}
	id := lastWaitID.Add(1)
	wait := &busyWait{db: db, timeout: c.busyTimeout, poll: c.busyPoll}
	busyWaits.Store(id, wait)
	tls := libc.NewTLS()
	defer tls.Close()
	sqlite3.Xsqlite3_busy_handler(tls, db, busyHandlerFP, id)
	return &busyConn{driverConn: full, id: id, wait: wait}, nil
}

// busyConn is a driver connection whose waits for a lock end with their context.
type busyConn struct {
	// driverConn is the driver's own connection.
	driverConn
	// id is the key of the connection's wait in busyWaits.
	id uintptr
	// wait is the connection's wait, the one busyWaits holds under id.
	wait *busyWait
}

// during hands ctx's Done channel to the busy handler for one call and returns what ends that span.
func (w *busyWait) during(ctx context.Context) func() {
	w.span.Store(&callSpan{done: ctx.Done()})
	return func() { w.span.Store(nil) }
}

// BeginTx starts a transaction, and an error after ctx ended also matches the error of ctx.
func (c *busyConn) BeginTx(ctx context.Context, opts driver.TxOptions) (driver.Tx, error) {
	defer c.wait.during(ctx)()
	tx, err := c.driverConn.BeginTx(ctx, opts)
	if err != nil {
		return nil, ended(ctx, err)
	}
	return &busyTx{Tx: tx, ctx: ctx, wait: c.wait}, nil
}

// busyTx is a driver transaction whose commit waits under the context it began with.
type busyTx struct {
	// Tx is the driver's transaction; its Rollback must finish even after ctx ends.
	driver.Tx
	// ctx is the context BeginTx was given, not database/sql's internal transaction context.
	ctx context.Context
	// wait is the wait of the connection the transaction uses.
	wait *busyWait
}

// Commit waits under the transaction's context and marks a failure with its error once it ends.
func (t *busyTx) Commit() error {
	defer t.wait.during(t.ctx)()
	if err := t.Tx.Commit(); err != nil {
		return ended(t.ctx, err)
	}
	return nil
}

// ExecContext runs a statement, and answers the error of ctx alone when it failed after ctx ended.
func (c *busyConn) ExecContext(ctx context.Context, query string, args []driver.NamedValue) (driver.Result, error) {
	defer c.wait.during(ctx)()
	r, err := c.driverConn.ExecContext(ctx, query, args)
	if err != nil {
		return nil, interrupted(ctx, err)
	}
	return r, nil
}

// QueryContext runs a query whose every step waits under ctx, and answers ctx's error alone once it ended.
func (c *busyConn) QueryContext(ctx context.Context, query string, args []driver.NamedValue) (driver.Rows, error) {
	return c.wait.query(ctx, func() (driver.Rows, error) { return c.driverConn.QueryContext(ctx, query, args) })
}

// query makes one query under a span of ctx, answers ctx's error alone once it ended, and wraps the rows it answers.
func (w *busyWait) query(ctx context.Context, run func() (driver.Rows, error)) (driver.Rows, error) {
	defer w.during(ctx)()
	rows, err := run()
	if err != nil {
		return nil, interrupted(ctx, err)
	}
	if end := ctx.Err(); end != nil {
		_ = rows.Close()
		return nil, end
	}
	return w.wrapRows(ctx, rows), nil
}

// driverRows is every interface the driver's rows offer to database/sql.
type driverRows interface {
	driver.Rows
	driver.RowsColumnTypeDatabaseTypeName
	driver.RowsColumnTypeLength
	driver.RowsColumnTypeNullable
	driver.RowsColumnTypePrecisionScale
	driver.RowsColumnTypeScanType
}

// wrapRows returns rows whose Next and Close wait under a span of ctx, or rows as they are if they lack an interface.
func (w *busyWait) wrapRows(ctx context.Context, rows driver.Rows) driver.Rows {
	full, ok := rows.(driverRows)
	if !ok {
		return rows
	}
	return &busyRows{driverRows: full, ctx: ctx, wait: w}
}

// busyRows is the driver's rows of a query whose every Next and Close waits under a span of the query's context.
type busyRows struct {
	// driverRows is the driver's own rows.
	driverRows
	// ctx is the context of the query.
	ctx context.Context
	// wait is the wait of the connection that ran the query.
	wait *busyWait
}

// Next reads the next row under the query's context, and answers its error alone when the read failed after it ended.
func (r *busyRows) Next(dest []driver.Value) error {
	defer r.wait.during(r.ctx)()
	err := r.driverRows.Next(dest)
	if err == nil || err == io.EOF {
		return err
	}
	return interrupted(r.ctx, err)
}

// Close closes the rows under the query's context, and answers its error alone when the close failed after it ended.
func (r *busyRows) Close() error {
	defer r.wait.during(r.ctx)()
	if err := r.driverRows.Close(); err != nil {
		return interrupted(r.ctx, err)
	}
	return nil
}

// PrepareContext prepares a statement, and answers the error of ctx alone when it failed after ctx ended.
func (c *busyConn) PrepareContext(ctx context.Context, query string) (driver.Stmt, error) {
	defer c.wait.during(ctx)()
	stmt, err := c.driverConn.PrepareContext(ctx, query)
	if err != nil {
		return nil, interrupted(ctx, err)
	}
	return c.wait.wrapStmt(stmt), nil
}

// driverStmt is every interface the driver's statement offers to database/sql.
type driverStmt interface {
	driver.Stmt
	driver.StmtExecContext
	driver.StmtQueryContext
}

// wrapStmt returns stmt whose every run waits under a span of its context, or stmt as it is if it lacks an interface.
func (w *busyWait) wrapStmt(stmt driver.Stmt) driver.Stmt {
	full, ok := stmt.(driverStmt)
	if !ok {
		return stmt
	}
	return &busyStmt{driverStmt: full, wait: w}
}

// busyStmt is a driver statement whose every run waits under a span of the run's context.
type busyStmt struct {
	// driverStmt is the driver's own statement.
	driverStmt
	// wait is the wait of the connection that prepared the statement.
	wait *busyWait
}

// ExecContext runs the statement, and answers the error of ctx alone when it failed after ctx ended.
func (s *busyStmt) ExecContext(ctx context.Context, args []driver.NamedValue) (driver.Result, error) {
	defer s.wait.during(ctx)()
	r, err := s.driverStmt.ExecContext(ctx, args)
	if err != nil {
		return nil, interrupted(ctx, err)
	}
	return r, nil
}

// QueryContext runs the statement as a query whose steps wait under ctx, and answers ctx's error alone once it ended.
func (s *busyStmt) QueryContext(ctx context.Context, args []driver.NamedValue) (driver.Rows, error) {
	return s.wait.query(ctx, func() (driver.Rows, error) { return s.driverStmt.QueryContext(ctx, args) })
}

// Ping checks the connection, and answers the error of ctx alone when it failed after ctx ended.
func (c *busyConn) Ping(ctx context.Context) error {
	defer c.wait.during(ctx)()
	if err := c.driverConn.Ping(ctx); err != nil {
		return interrupted(ctx, err)
	}
	return nil
}

// Close closes the connection and forgets its wait.
func (c *busyConn) Close() error {
	err := c.driverConn.Close()
	busyWaits.Delete(c.id)
	return err
}

// ended returns err marked with the error of ctx once ctx has ended, and err as it is before.
func ended(ctx context.Context, err error) error {
	if end := ctx.Err(); end != nil {
		return fmt.Errorf("%w: %w", err, end)
	}
	return err
}

// interrupted returns the error of ctx in place of err once ctx has ended, and err as it is before.
func interrupted(ctx context.Context, err error) error {
	if end := ctx.Err(); end != nil {
		return end
	}
	return err
}
