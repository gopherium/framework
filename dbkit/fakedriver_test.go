// SPDX-License-Identifier: Apache-2.0

package dbkit_test

import (
	"context"
	"database/sql/driver"
	"io"
	"slices"
	"sync"
	"time"
)

// fakeError is an error value the fake driver and the tests answer with.
type fakeError string

// Error returns the error's text.
func (e fakeError) Error() string {
	return string(e)
}

const (
	// fakeInterrupted is what a held fake call answers when its context ends first, as a driver that never wraps it.
	fakeInterrupted fakeError = "fake: interrupted"
	// fakeBroken is a driver failure that has nothing to do with a deadline.
	fakeBroken fakeError = "fake: broken"
	// fakeNoPrepare is what the fake driver answers to a prepare.
	fakeNoPrepare fakeError = "fake: no prepared statements"
)

var (
	// _ is the fake driver seen as a connector.
	_ driver.Connector = (*fakeDriver)(nil)
	// _ is the fake connection seen as one that begins transactions with a context.
	_ driver.ConnBeginTx = (*fakeConn)(nil)
	// _ is the fake connection seen as one that runs statements with a context.
	_ driver.ExecerContext = (*fakeConn)(nil)
	// _ is the fake connection seen as one that runs queries with a context.
	_ driver.QueryerContext = (*fakeConn)(nil)
	// _ is the fake result seen as one with several result sets.
	_ driver.RowsNextResultSet = (*fakeRows)(nil)
	// _ is the fake transaction.
	_ driver.Tx = (*fakeTx)(nil)
)

// fakeDriver is a database/sql connector that records every call and holds the ones a test gates.
type fakeDriver struct {
	mu       sync.Mutex
	calls    []fakeCall
	answers  map[string]fakeAnswer
	sets     []int
	events   chan string
	quiet    bool
	rollback chan struct{}
	commit   chan struct{}
}

// fakeCall is one call the fake driver received.
type fakeCall struct {
	op       string
	text     string
	deadline time.Time
	bounded  bool
}

// fakeAnswer is how the fake driver answers one statement text, the empty text standing for a transaction start.
type fakeAnswer struct {
	gate    chan struct{}
	rowGate chan struct{}
	err     error
	rowErr  error
	sets    []int
	outlast chan string
}

// fakeNew returns a fake driver that answers every query with one row holding 1.
func fakeNew() *fakeDriver {
	return &fakeDriver{answers: map[string]fakeAnswer{}, sets: []int{1}, events: make(chan string, 64)}
}

// Connect opens a fake connection.
func (f *fakeDriver) Connect(context.Context) (driver.Conn, error) {
	return &fakeConn{driver: f}, nil
}

// Driver returns the fake driver itself.
func (f *fakeDriver) Driver() driver.Driver {
	return f
}

// Open opens a fake connection, whatever the name.
func (f *fakeDriver) Open(string) (driver.Conn, error) {
	return &fakeConn{driver: f}, nil
}

// hold makes every call with text wait until the returned gate closes, or fail once its context ends.
func (f *fakeDriver) hold(text string) chan struct{} {
	gate := make(chan struct{})
	f.set(text, func(a *fakeAnswer) { a.gate = gate })
	return gate
}

// holdPast makes every call with text wait for the returned gate past the end of its context, which it reports.
func (f *fakeDriver) holdPast(text string) chan struct{} {
	gate := make(chan struct{})
	f.set(text, func(a *fakeAnswer) {
		a.gate = gate
		a.outlast = f.events
	})
	return gate
}

// holdRows makes every row read of a query with text wait until the returned gate closes or its context ends.
func (f *fakeDriver) holdRows(text string) chan struct{} {
	gate := make(chan struct{})
	f.set(text, func(a *fakeAnswer) { a.rowGate = gate })
	return gate
}

// holdRollback makes every rollback wait, once reported, until the returned gate closes or its begin context ends.
func (f *fakeDriver) holdRollback() chan struct{} {
	return f.holdEnd(&f.rollback)
}

// holdCommit makes every commit wait, once reported, until the returned gate closes or its begin context ends.
func (f *fakeDriver) holdCommit() chan struct{} {
	return f.holdEnd(&f.commit)
}

// holdEnd sets the gate at slot and returns it.
func (f *fakeDriver) holdEnd(slot *chan struct{}) chan struct{} {
	gate := make(chan struct{})
	f.mu.Lock()
	defer f.mu.Unlock()
	*slot = gate
	return gate
}

// endGate returns the gate at slot, nil when commits or rollbacks never wait.
func (f *fakeDriver) endGate(slot *chan struct{}) chan struct{} {
	f.mu.Lock()
	defer f.mu.Unlock()
	return *slot
}

// fail makes every call with text answer fakeBroken at once.
func (f *fakeDriver) fail(text string) {
	f.set(text, func(a *fakeAnswer) { a.err = fakeBroken })
}

// failRows makes every row read of a query with text answer fakeBroken.
func (f *fakeDriver) failRows(text string) {
	f.set(text, func(a *fakeAnswer) { a.rowErr = fakeBroken })
}

// answer sets how many rows each result set of every query holds.
func (f *fakeDriver) answer(sets ...int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.sets = sets
}

// set changes the answer for text.
func (f *fakeDriver) set(text string, change func(*fakeAnswer)) {
	f.mu.Lock()
	defer f.mu.Unlock()
	answer := f.answers[text]
	change(&answer)
	f.answers[text] = answer
}

// record logs one call made under ctx and returns the answer for its text with the result sets of every query.
func (f *fakeDriver) record(ctx context.Context, op, text string) fakeAnswer {
	f.mu.Lock()
	defer f.mu.Unlock()
	if !f.quiet {
		deadline, bounded := ctx.Deadline()
		f.calls = append(f.calls, fakeCall{op: op, text: text, deadline: deadline, bounded: bounded})
	}
	answer := f.answers[text]
	answer.sets = f.sets
	return answer
}

// event records a commit, a rollback or a closing of rows, and reports it on the events channel as its log line.
func (f *fakeDriver) event(op, text string) {
	f.record(context.Background(), op, text)
	select {
	case f.events <- fakeLine(op, text):
	default:
	}
}

// fakeLine renders one call as its op and text.
func fakeLine(op, text string) string {
	if text == "" {
		return op
	}
	return op + " " + text
}

// received returns every call the fake driver received, in order.
func (f *fakeDriver) received() []fakeCall {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.calls)
}

// log returns every call the fake driver received as its op and text, in order.
func (f *fakeDriver) log() []string {
	calls := f.received()
	lines := make([]string, len(calls))
	for i, call := range calls {
		lines[i] = fakeLine(call.op, call.text)
	}
	return lines
}

// saw reports whether the fake driver received op with text.
func (f *fakeDriver) saw(op, text string) bool {
	return slices.Contains(f.log(), fakeLine(op, text))
}

// wait holds a call on the gate and returns the error the call answers.
func (a fakeAnswer) wait(ctx context.Context) error {
	if a.outlast != nil {
		return a.waitPast(ctx)
	}
	if err := fakeWait(ctx, a.gate); err != nil {
		return err
	}
	return a.err
}

// waitPast holds a call on the gate even after ctx ends, reporting that end as context ended.
func (a fakeAnswer) waitPast(ctx context.Context) error {
	select {
	case <-a.gate:
	case <-ctx.Done():
		a.outlast <- "context ended"
		<-a.gate
	}
	return a.err
}

// fakeWait holds a call until gate closes and answers fakeInterrupted when ctx ends first, at once with no gate.
func fakeWait(ctx context.Context, gate chan struct{}) error {
	if gate == nil {
		return nil
	}
	select {
	case <-gate:
		return nil
	case <-ctx.Done():
		return fakeInterrupted
	}
}

// fakeConn is one connection of the fake driver.
type fakeConn struct {
	driver *fakeDriver
}

// Prepare refuses every statement.
func (c *fakeConn) Prepare(string) (driver.Stmt, error) {
	return nil, fakeNoPrepare
}

// Close closes nothing.
func (c *fakeConn) Close() error {
	return nil
}

// Begin starts a transaction with no context.
func (c *fakeConn) Begin() (driver.Tx, error) {
	return c.BeginTx(context.Background(), driver.TxOptions{})
}

// BeginTx records a transaction start and answers it as set.
func (c *fakeConn) BeginTx(ctx context.Context, _ driver.TxOptions) (driver.Tx, error) {
	if err := c.driver.record(ctx, "begin", "").wait(ctx); err != nil {
		return nil, err
	}
	return &fakeTx{driver: c.driver, ctx: ctx}, nil
}

// ExecContext records a statement and answers it as set.
func (c *fakeConn) ExecContext(ctx context.Context, query string, _ []driver.NamedValue) (driver.Result, error) {
	if err := c.driver.record(ctx, "exec", query).wait(ctx); err != nil {
		return nil, err
	}
	return driver.RowsAffected(1), nil
}

// QueryContext records a query and answers it with its result sets.
func (c *fakeConn) QueryContext(ctx context.Context, query string, _ []driver.NamedValue) (driver.Rows, error) {
	answer := c.driver.record(ctx, "query", query)
	if err := answer.wait(ctx); err != nil {
		return nil, err
	}
	return &fakeRows{driver: c.driver, ctx: ctx, text: query, answer: answer}, nil
}

// fakeRows is the result of one fake query, each row holding its own number.
type fakeRows struct {
	driver *fakeDriver
	ctx    context.Context
	text   string
	answer fakeAnswer
	set    int
	row    int
}

// Columns names the one column.
func (r *fakeRows) Columns() []string {
	return []string{"n"}
}

// Close records that the rows closed.
func (r *fakeRows) Close() error {
	r.driver.event("close", r.text)
	return nil
}

// Next reads the next row of the current result set once the row gate lets it.
func (r *fakeRows) Next(dest []driver.Value) error {
	if err := fakeWait(r.ctx, r.answer.rowGate); err != nil {
		return err
	}
	if r.answer.rowErr != nil {
		return r.answer.rowErr
	}
	if r.row == r.answer.sets[r.set] {
		return io.EOF
	}
	r.row++
	dest[0] = int64(r.row)
	return nil
}

// HasNextResultSet reports whether another result set follows.
func (r *fakeRows) HasNextResultSet() bool {
	return r.set+1 < len(r.answer.sets)
}

// NextResultSet moves to the next result set.
func (r *fakeRows) NextResultSet() error {
	if !r.HasNextResultSet() {
		return io.EOF
	}
	r.set++
	r.row = 0
	return nil
}

// fakeTx is one transaction of the fake driver, keeping the context it began under as pgx does.
type fakeTx struct {
	driver *fakeDriver
	ctx    context.Context
}

// Commit records a commit and waits on the commit gate when one is set, or until the begin context ends.
func (t *fakeTx) Commit() error {
	t.driver.event("commit", "")
	return fakeWait(t.ctx, t.driver.endGate(&t.driver.commit))
}

// Rollback records a rollback and waits on the rollback gate when one is set, or until the begin context ends.
func (t *fakeTx) Rollback() error {
	t.driver.event("rollback", "")
	return fakeWait(t.ctx, t.driver.endGate(&t.driver.rollback))
}
