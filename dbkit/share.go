// SPDX-License-Identifier: Apache-2.0

package dbkit

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// ShareOptions configures one share. Every number is required.
type ShareOptions struct {
	// ID names the share to the counter and the observer and is never parsed.
	ID string
	// Slots is how many statements, open result sets and transactions the share holds at once.
	Slots int
	// StatementTimeout is the deadline of one statement, its wait for a slot included.
	StatementTimeout time.Duration
	// TransactionTimeout is the deadline of one transaction from Begin to its end.
	TransactionTimeout time.Duration
	// Observer sees every statement and transaction start before the driver, and nil means none.
	Observer Observer
}

var (
	// ErrShareFull marks a statement that found no free slot before its deadline.
	ErrShareFull = errors.New("dbkit: no free slot in the share before the statement deadline")
	// ErrRefused marks a statement the observer refused.
	ErrRefused = errors.New("dbkit: the observer refused the statement")
	// ErrPlaceholder marks a SQLite statement with a question mark parameter, or a $N after a named parameter.
	ErrPlaceholder = errors.New("dbkit: write every parameter as $N")
)

// Share is a capped, timed and counted view of one database handle, lent to one part of an application.
type Share struct {
	db                 *sql.DB
	engine             Engine
	id                 string
	slots              chan struct{}
	statementTimeout   time.Duration
	transactionTimeout time.Duration
	observer           Observer
}

// NewShare lends a capped share of an open handle. It opens nothing and sends nothing.
func NewShare(db *sql.DB, engine Engine, opts ShareOptions) (*Share, error) {
	if db == nil {
		return nil, errors.New("dbkit: the share needs a database handle, got nil")
	}
	if engine != Postgres && engine != SQLite {
		return nil, fmt.Errorf("dbkit: the share engine must be postgres or sqlite, got %v", engine)
	}
	if err := opts.check(); err != nil {
		return nil, err
	}
	return &Share{
		db:                 db,
		engine:             engine,
		id:                 opts.ID,
		slots:              make(chan struct{}, opts.Slots),
		statementTimeout:   opts.StatementTimeout,
		transactionTimeout: opts.TransactionTimeout,
		observer:           opts.Observer,
	}, nil
}

// check refuses an option that is missing, zero or below its floor.
func (o ShareOptions) check() error {
	if o.ID == "" {
		return errors.New("dbkit: the share option ID must not be empty")
	}
	if o.Slots < 1 {
		return fmt.Errorf("dbkit: the share option Slots must be 1 or more, got %d", o.Slots)
	}
	if o.StatementTimeout <= 0 {
		return fmt.Errorf("dbkit: the share option StatementTimeout must stand above zero, got %v", o.StatementTimeout)
	}
	if o.TransactionTimeout <= 0 {
		return fmt.Errorf("dbkit: the share option TransactionTimeout must stand above zero, got %v", o.TransactionTimeout)
	}
	return nil
}

// Engine returns the engine of the share's handle.
func (s *Share) Engine() Engine {
	return s.engine
}

// Exec runs a statement that returns no rows.
func (s *Share) Exec(ctx context.Context, query string, args ...any) (sql.Result, error) {
	return s.exec(ctx, nil, query, args)
}

// Query runs a query that returns rows, read within the statement timeout.
func (s *Share) Query(ctx context.Context, query string, args ...any) (*Rows, error) {
	return s.query(ctx, nil, KindQuery, query, args)
}

// QueryRow runs a query that returns at most one row, read within the statement timeout.
func (s *Share) QueryRow(ctx context.Context, query string, args ...any) *Row {
	rows, err := s.query(ctx, nil, KindQueryRow, query, args)
	return &Row{rows: rows, err: err}
}

// Begin starts a transaction that ends at the transaction timeout at the latest.
func (s *Share) Begin(ctx context.Context) (*Tx, error) {
	total, past := currentCount(ctx)
	begin := Statement{ID: s.id, Kind: KindBegin, Writes: true, Count: total, PastBudget: past}
	if err := s.observe(ctx, begin); err != nil {
		return nil, err
	}
	if err := s.wait(ctx); err != nil {
		return nil, err
	}
	txCtx, cancel := context.WithTimeout(ctx, s.transactionTimeout)
	tx, err := s.begin(txCtx, cancel)
	if err != nil {
		cancel()
		s.release()
		return nil, err
	}
	return tx, nil
}

// runner is a handle or a transaction a statement runs on.
type runner interface {
	// ExecContext runs a statement that returns no rows.
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
	// QueryContext runs a query that returns rows.
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
}

// admit counts query, shows it to the observer and refuses a SQLite placeholder, and returns the text to send.
func (s *Share) admit(ctx context.Context, kind Kind, inTx bool, query string) (string, error) {
	found := scan(query, s.engine)
	total, past := countStatement(ctx, s.id)
	err := s.observe(ctx, Statement{
		ID:            s.id,
		Kind:          kind,
		Query:         query,
		Writes:        found.writes,
		InTransaction: inTx,
		Count:         total,
		PastBudget:    past,
	})
	if err != nil {
		return "", err
	}
	if found.placeholder {
		return "", ErrPlaceholder
	}
	return found.text, nil
}

// exec runs a statement that returns no rows on the handle, or inside tx when tx is not nil.
func (s *Share) exec(ctx context.Context, tx *Tx, query string, args []any) (sql.Result, error) {
	text, err := s.admit(ctx, KindExec, tx != nil, query)
	if err != nil {
		return nil, err
	}
	stmtCtx, cancel := s.statementContext(ctx, tx)
	defer cancel()
	give, err := s.take(stmtCtx, tx != nil)
	if err != nil {
		return nil, err
	}
	defer give()
	result, err := s.runner(tx).ExecContext(stmtCtx, text, args...)
	return result, ended(stmtCtx, err)
}

// query runs a query on the handle, or inside tx when tx is not nil, and wraps its rows.
func (s *Share) query(ctx context.Context, tx *Tx, kind Kind, query string, args []any) (*Rows, error) {
	text, err := s.admit(ctx, kind, tx != nil, query)
	if err != nil {
		return nil, err
	}
	stmtCtx, cancel := s.statementContext(ctx, tx)
	give, err := s.take(stmtCtx, tx != nil)
	if err != nil {
		cancel()
		return nil, err
	}
	rows, err := s.runner(tx).QueryContext(stmtCtx, text, args...)
	if err != nil {
		err = ended(stmtCtx, err)
		cancel()
		give()
		return nil, err
	}
	return newRows(stmtCtx, cancel, give, rows), nil
}

// runner returns the transaction tx runs on, or the handle when tx is nil.
func (s *Share) runner(tx *Tx) runner {
	if tx == nil {
		return s.db
	}
	return tx.tx
}

// statementContext returns the context of one statement, ending at the statement timeout and with tx when it is set.
func (s *Share) statementContext(ctx context.Context, tx *Tx) (context.Context, context.CancelFunc) {
	stmtCtx, cancel := context.WithTimeout(ctx, s.statementTimeout)
	if tx == nil {
		return stmtCtx, cancel
	}
	end, _ := tx.ctx.Deadline()
	bounded, cancelBounded := context.WithDeadline(stmtCtx, end)
	stop := context.AfterFunc(tx.ctx, func() {
		if errors.Is(tx.ctx.Err(), context.Canceled) {
			cancelBounded()
		}
	})
	return bounded, func() {
		stop()
		cancelBounded()
		cancel()
	}
}

// take waits under ctx for a slot unless the statement runs inside a transaction, and returns how to give it back.
func (s *Share) take(ctx context.Context, inTx bool) (func(), error) {
	if inTx {
		return func() {}, nil
	}
	if err := s.acquire(ctx); err != nil {
		return nil, err
	}
	return s.release, nil
}

// wait takes a slot within one statement timeout of ctx.
func (s *Share) wait(ctx context.Context) error {
	waitCtx, cancel := context.WithTimeout(ctx, s.statementTimeout)
	defer cancel()
	return s.acquire(waitCtx)
}

// acquire takes a slot at once when one is free, or waits for one until ctx ends.
func (s *Share) acquire(ctx context.Context) error {
	select {
	case s.slots <- struct{}{}:
		return nil
	default:
	}
	select {
	case s.slots <- struct{}{}:
		return nil
	case <-ctx.Done():
		return fmt.Errorf("%w: %w", ErrShareFull, ctx.Err())
	}
}

// release gives one slot back.
func (s *Share) release() {
	<-s.slots
}

// ended marks a driver error with the error of ctx when ctx has ended and err does not say so.
func ended(ctx context.Context, err error) error {
	end := ctx.Err()
	if err == nil || end == nil || errors.Is(err, end) {
		return err
	}
	return fmt.Errorf("%w: %w", err, end)
}
