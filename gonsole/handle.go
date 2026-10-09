// SPDX-License-Identifier: Apache-2.0

package gonsole

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"sync"
)

// ErrDescribing marks the database handle a describing call asks for.
var ErrDescribing = errors.New("gonsole: a describing call opens no database")

// handle is the one database handle the parts of a run share.
type handle struct {
	// open is the program's opener, nil when the program has none.
	open func(ctx context.Context, databaseURL string) (*sql.DB, error)
	// mu guards done, db and err.
	mu sync.Mutex
	// done reports whether the handle was asked for.
	done bool
	// db is the open handle, nil before the first ask and after a failed one.
	db *sql.DB
	// err is the failure of the first ask.
	err error
}

// DB returns the run's one database handle, opened through Program.Open on first use.
func (c Call) DB(ctx context.Context) (*sql.DB, error) {
	switch {
	case c.Describe:
		return nil, ErrDescribing
	case c.handle == nil:
		return nil, errors.New("gonsole: no database handle in this call")
	}
	return c.handle.get(ctx, c)
}

// WithDB returns a copy of c whose DB answers db.
func (c Call) WithDB(db *sql.DB) Call {
	c.handle = &handle{done: true, db: db}
	return c
}

// get opens the handle on the first ask, under a context the end of ctx cannot cancel, and returns that answer.
func (h *handle) get(ctx context.Context, c Call) (*sql.DB, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if !h.done {
		h.done = true
		h.db, h.err = h.opened(context.WithoutCancel(ctx), c)
	}
	return h.db, h.err
}

// opened opens the database at the address of c through the program's opener.
func (h *handle) opened(ctx context.Context, c Call) (*sql.DB, error) {
	if h.open == nil {
		return nil, errors.New("gonsole: the program has no Open")
	}
	address, err := c.address()
	if err != nil {
		return nil, err
	}
	db, err := h.open(ctx, address)
	switch {
	case err != nil:
		return nil, fmt.Errorf("open the database: %w", err)
	case db == nil:
		return nil, errors.New("gonsole: Open answered no database")
	}
	return db, nil
}

// close closes the handle the run opened, nothing when none opened.
func (h *handle) close() error {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.db == nil {
		return nil
	}
	if err := h.db.Close(); err != nil {
		return fmt.Errorf("close the database: %w", err)
	}
	return nil
}
