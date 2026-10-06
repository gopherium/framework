// SPDX-License-Identifier: Apache-2.0

package dbkit

import (
	"context"
	"database/sql"
	"errors"
	"slices"
	"sync"
	"sync/atomic"
)

// errRawBytes is what Scan answers to a *sql.RawBytes destination.
var errRawBytes = errors.New("dbkit: Scan takes no *sql.RawBytes")

// Rows is a *sql.Rows that gives its slot back exactly once, read within the statement timeout.
type Rows struct {
	*sql.Rows
	ctx    context.Context
	cancel context.CancelFunc
	give   func()
	stop   func() bool
	once   sync.Once
	cause  atomic.Pointer[error]
	kept   atomic.Pointer[error]
	atEnd  bool
}

// newRows wraps rows read under ctx, which cancel ends and give frees, and closes them once ctx ends.
func newRows(ctx context.Context, cancel context.CancelFunc, give func(), rows *sql.Rows) *Rows {
	r := &Rows{Rows: rows, ctx: ctx, cancel: cancel, give: give}
	r.stop = context.AfterFunc(ctx, r.expire)
	return r
}

// Next prepares the next row for Scan and gives the slot back once the last result set ends.
func (r *Rows) Next() bool {
	if r.Rows.Next() {
		return true
	}
	if r.cause.Load() == nil {
		r.atEnd = true
	}
	if _, err := r.Columns(); err != nil {
		r.endRead()
	}
	return false
}

// NextResultSet moves to the next result set and gives the slot back when none follows.
func (r *Rows) NextResultSet() bool {
	if r.Rows.NextResultSet() {
		r.atEnd = false
		return true
	}
	r.endRead()
	return false
}

// Scan copies the columns of the current row into dest, refusing a *sql.RawBytes destination.
func (r *Rows) Scan(dest ...any) error {
	if slices.ContainsFunc(dest, isRawBytes) {
		return errRawBytes
	}
	return r.Rows.Scan(dest...)
}

// Close closes the rows and gives the slot back, the first time only.
func (r *Rows) Close() error {
	r.stop()
	var err error
	r.once.Do(func() {
		live := r.ctx.Err() == nil
		err = r.Rows.Close()
		if live {
			r.keep(nil)
		}
		r.release()
	})
	return err
}

// Err returns the error met while reading, marked as a deadline error when the statement deadline stopped it.
func (r *Rows) Err() error {
	if kept := r.kept.Load(); kept != nil {
		return *kept
	}
	return r.readErr()
}

// readErr returns the error of the read so far, the end of the statement context included.
func (r *Rows) readErr() error {
	err := r.Rows.Err()
	if cause := r.cause.Load(); err == nil && cause != nil && !r.atEnd {
		err = *cause
	}
	return ended(r.ctx, err)
}

// endRead closes the rows the reader came to the end of, keeping their error, and gives the slot back.
func (r *Rows) endRead() {
	r.stop()
	r.once.Do(func() {
		_ = r.Rows.Close()
		r.keep(r.readErr())
		r.release()
	})
}

// expire records the end of the statement context and closes the rows once it ends.
func (r *Rows) expire() {
	cause := r.ctx.Err()
	r.cause.Store(&cause)
	r.once.Do(func() {
		_ = r.Rows.Close()
		r.release()
	})
}

// keep fixes the error Err reports from now on.
func (r *Rows) keep(err error) {
	r.kept.Store(&err)
}

// release cancels the statement context and gives the slot back.
func (r *Rows) release() {
	r.cancel()
	r.give()
}

// Row is the result of QueryRow, holding its slot until Scan.
type Row struct {
	rows *Rows
	err  error
}

// Scan copies the first row into dest and closes the rows.
func (r *Row) Scan(dest ...any) error {
	if r.err != nil {
		return r.err
	}
	err := r.scan(dest)
	if closeErr := r.rows.Close(); err == nil {
		err = closeErr
	}
	return err
}

// scan copies the first row into dest.
func (r *Row) scan(dest []any) error {
	if slices.ContainsFunc(dest, isRawBytes) {
		return errRawBytes
	}
	if !r.rows.Next() {
		if err := r.rows.Err(); err != nil {
			return err
		}
		return sql.ErrNoRows
	}
	return r.rows.Scan(dest...)
}

// Err returns the error of the query behind the row.
func (r *Row) Err() error {
	return r.err
}

// isRawBytes reports whether dest is a *sql.RawBytes.
func isRawBytes(dest any) bool {
	_, ok := dest.(*sql.RawBytes)
	return ok
}
