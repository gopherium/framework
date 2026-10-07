// SPDX-License-Identifier: Apache-2.0

package dbkit

import (
	"context"
	"database/sql"
	"fmt"
	"sync"
	"time"
)

// Tx is a transaction of a share that holds one slot until Commit, Rollback or the end of its context.
type Tx struct {
	share    *Share
	ctx      context.Context
	conn     *sql.Conn
	tx       *sql.Tx
	endBegin context.CancelFunc
	cancel   context.CancelFunc
	stop     func() bool
	once     sync.Once
}

// begin takes a connection within txCtx and starts on it a transaction that only the share rolls back.
func (s *Share) begin(txCtx context.Context, cancel context.CancelFunc) (*Tx, error) {
	conn, err := s.db.Conn(txCtx)
	if err != nil {
		return nil, ended(txCtx, err)
	}
	beginCtx, endBegin := context.WithCancel(context.WithoutCancel(txCtx))
	watch := context.AfterFunc(txCtx, endBegin)
	tx, err := conn.BeginTx(beginCtx, nil)
	if !watch() && err == nil {
		_ = tx.Rollback()
		err = txCtx.Err()
	}
	if err != nil {
		endBegin()
		_ = conn.Close()
		return nil, ended(txCtx, err)
	}
	t := &Tx{share: s, ctx: txCtx, conn: conn, tx: tx, endBegin: endBegin, cancel: cancel}
	t.stop = context.AfterFunc(txCtx, t.expire)
	return t, nil
}

// Exec runs a statement that returns no rows inside the transaction.
func (t *Tx) Exec(ctx context.Context, query string, args ...any) (sql.Result, error) {
	return t.share.exec(ctx, t, query, args)
}

// Query runs a query that returns rows inside the transaction, read within the statement timeout.
func (t *Tx) Query(ctx context.Context, query string, args ...any) (*Rows, error) {
	return t.share.query(ctx, t, KindQuery, query, args)
}

// QueryRow runs a query that returns at most one row inside the transaction, read within the statement timeout.
func (t *Tx) QueryRow(ctx context.Context, query string, args ...any) *Row {
	rows, err := t.share.query(ctx, t, KindQueryRow, query, args)
	return &Row{rows: rows, err: err}
}

// Commit commits the transaction and gives its slot back, or rolls it back once its context has ended.
func (t *Tx) Commit() error {
	return t.finish(t.tx.Commit)
}

// Rollback rolls the transaction back and gives its slot back, the first time only.
func (t *Tx) Rollback() error {
	return t.finish(t.tx.Rollback)
}

// finish ends the transaction with end within its context, or abandons it when the context has ended.
func (t *Tx) finish(end func() error) error {
	if !t.stop() {
		t.once.Do(t.abandon)
		return fmt.Errorf("%w: %w", sql.ErrTxDone, t.ctx.Err())
	}
	watch := context.AfterFunc(t.ctx, t.endBegin)
	err := ended(t.ctx, end())
	watch()
	t.once.Do(t.release)
	return err
}

// expire abandons the transaction once its context ends.
func (t *Tx) expire() {
	t.once.Do(t.abandon)
}

// abandon rolls the transaction back within one statement timeout and releases it.
func (t *Tx) abandon() {
	cut := time.AfterFunc(t.share.statementTimeout, t.endBegin)
	_ = t.tx.Rollback()
	cut.Stop()
	t.release()
}

// release returns the connection to the pool, cancels the transaction contexts and gives the slot back.
func (t *Tx) release() {
	_ = t.conn.Close()
	t.endBegin()
	t.cancel()
	t.share.release()
}
