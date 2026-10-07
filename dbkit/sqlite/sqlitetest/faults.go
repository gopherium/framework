// SPDX-License-Identifier: Apache-2.0

package sqlitetest

import (
	"context"
	"database/sql/driver"
	"errors"
	"sync"
)

// Faults holds the failures the connections of a handle from OpenWithFaults answer with.
type Faults struct {
	// mu guards statements and commit.
	mu sync.Mutex
	// statements maps the exact text of each chosen statement to the error it answers with.
	statements map[string]error
	// commit is the error the next commit answers with, or nil.
	commit error
}

// FailStatement makes every statement whose text is exactly query answer err, inside transactions too.
func (f *Faults) FailStatement(query string, err error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.statements == nil {
		f.statements = map[string]error{}
	}
	f.statements[query] = err
}

// statement returns the error chosen for the statement with the text query, or nil.
func (f *Faults) statement(query string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.statements[query]
}

// FailNextCommit makes the next commit roll its transaction back and answer err.
func (f *Faults) FailNextCommit(err error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.commit = err
}

// takeCommit returns the error the next commit answers with and clears it.
func (f *Faults) takeCommit() error {
	f.mu.Lock()
	defer f.mu.Unlock()
	err := f.commit
	f.commit = nil
	return err
}

// wrap returns c answering with the failures of f.
func (f *Faults) wrap(c driver.Conn) driver.Conn {
	return &faultConn{driverConn: c.(driverConn), faults: f}
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

// faultConn is a driver connection that answers with the failures of its faults.
type faultConn struct {
	// driverConn is the driver's own connection.
	driverConn
	// faults holds the failures the connection answers with.
	faults *Faults
}

// Prepare prepares query, or answers the error chosen for it.
func (c *faultConn) Prepare(query string) (driver.Stmt, error) {
	return c.PrepareContext(context.Background(), query)
}

// PrepareContext prepares query, or answers the error chosen for it.
func (c *faultConn) PrepareContext(ctx context.Context, query string) (driver.Stmt, error) {
	if err := c.faults.statement(query); err != nil {
		return nil, err
	}
	stmt, err := c.driverConn.PrepareContext(ctx, query)
	if err != nil {
		return nil, err
	}
	return &faultStmt{driverStmt: stmt.(driverStmt), query: query, faults: c.faults}, nil
}

// ExecContext runs query, or answers the error chosen for it.
func (c *faultConn) ExecContext(ctx context.Context, query string, args []driver.NamedValue) (driver.Result, error) {
	if err := c.faults.statement(query); err != nil {
		return nil, err
	}
	return c.driverConn.ExecContext(ctx, query, args)
}

// QueryContext runs query, or answers the error chosen for it.
func (c *faultConn) QueryContext(ctx context.Context, query string, args []driver.NamedValue) (driver.Rows, error) {
	if err := c.faults.statement(query); err != nil {
		return nil, err
	}
	return c.driverConn.QueryContext(ctx, query, args)
}

// driverStmt is every interface the driver's statement offers to database/sql.
type driverStmt interface {
	driver.Stmt
	driver.StmtExecContext
	driver.StmtQueryContext
}

// faultStmt is a driver statement that answers with the failure chosen for its text.
type faultStmt struct {
	// driverStmt is the driver's own statement.
	driverStmt
	// query is the text of the statement.
	query string
	// faults holds the failures the statement answers with.
	faults *Faults
}

// Exec runs the statement, or answers the error chosen for its text.
func (s *faultStmt) Exec(args []driver.Value) (driver.Result, error) {
	return s.ExecContext(context.Background(), named(args))
}

// Query runs the statement, or answers the error chosen for its text.
func (s *faultStmt) Query(args []driver.Value) (driver.Rows, error) {
	return s.QueryContext(context.Background(), named(args))
}

// named returns args as named values numbered from one.
func named(args []driver.Value) []driver.NamedValue {
	values := make([]driver.NamedValue, len(args))
	for i, value := range args {
		values[i] = driver.NamedValue{Ordinal: i + 1, Value: value}
	}
	return values
}

// ExecContext runs the statement, or answers the error chosen for its text.
func (s *faultStmt) ExecContext(ctx context.Context, args []driver.NamedValue) (driver.Result, error) {
	if err := s.faults.statement(s.query); err != nil {
		return nil, err
	}
	return s.driverStmt.ExecContext(ctx, args)
}

// QueryContext runs the statement, or answers the error chosen for its text.
func (s *faultStmt) QueryContext(ctx context.Context, args []driver.NamedValue) (driver.Rows, error) {
	if err := s.faults.statement(s.query); err != nil {
		return nil, err
	}
	return s.driverStmt.QueryContext(ctx, args)
}

// Begin starts a transaction whose commit answers with the next commit failure.
func (c *faultConn) Begin() (driver.Tx, error) {
	return c.BeginTx(context.Background(), driver.TxOptions{})
}

// BeginTx starts a transaction whose commit answers with the next commit failure.
func (c *faultConn) BeginTx(ctx context.Context, opts driver.TxOptions) (driver.Tx, error) {
	tx, err := c.driverConn.BeginTx(ctx, opts)
	if err != nil {
		return nil, err
	}
	return &faultTx{Tx: tx, faults: c.faults}, nil
}

// faultTx is a driver transaction whose commit answers with the next commit failure.
type faultTx struct {
	// Tx is the driver's own transaction.
	driver.Tx
	// faults holds the failures the transaction answers with.
	faults *Faults
}

// Commit rolls the transaction back and answers the next commit failure, or commits when none is set.
func (t *faultTx) Commit() error {
	if err := t.faults.takeCommit(); err != nil {
		return errors.Join(err, t.Rollback())
	}
	return t.Tx.Commit()
}
