// SPDX-License-Identifier: Apache-2.0

package sqlite

import (
	"context"
	"database/sql/driver"
	"errors"
	"fmt"
	"log/slog"
	"strconv"
	"time"

	modernc "modernc.org/sqlite"

	"github.com/gopherium/framework/dbkit"
	"github.com/gopherium/framework/dbkit/sqlite/internal/seam"
)

const (
	// lowerBusyTimeout sets a connection's busy timeout to zero.
	lowerBusyTimeout = "PRAGMA busy_timeout=0"
	// optimizeStatement is the optimize every new connection runs.
	optimizeStatement = "PRAGMA optimize=0x10002"
	// optimizeSkipped is the note a busy optimize leaves.
	optimizeSkipped = "dbkit: PRAGMA optimize skipped on a new connection while the database is busy"
)

// connector opens the connections of one SQLite file through a private driver value.
type connector struct {
	// driver is the private driver value the handle reports.
	driver *modernc.Driver
	// open opens one connection by the driver's name of the file.
	open func(name string) (driver.Conn, error)
	// opening is the one slot a try of open holds until it answers, so one try runs at a time.
	opening chan struct{}
	// name is the driver's name of the file with every rule.
	name string
	// path is the absolute path of the file.
	path string
	// busyTimeout is how long a busy open keeps trying.
	busyTimeout time.Duration
	// busyPoll is how often a busy open tries again and the longest one try blocks.
	busyPoll time.Duration
	// restore sets a connection's busy timeout back to the option.
	restore string
	// logger receives the skipped optimize notes.
	logger *slog.Logger
}

// newConnector returns the connector of the file at path, opened through drv with the rules of opts.
func newConnector(drv *modernc.Driver, path string, opts Options) *connector {
	logger := opts.Logger
	if logger == nil {
		logger = slog.New(slog.DiscardHandler)
	}
	return &connector{
		driver:      drv,
		open:        drv.Open,
		opening:     make(chan struct{}, 1),
		name:        connectionString(path, opts),
		path:        path,
		busyTimeout: opts.BusyTimeout,
		busyPoll:    opts.BusyPoll,
		restore:     "PRAGMA busy_timeout=" + strconv.FormatInt(opts.BusyTimeout.Milliseconds(), 10),
		logger:      logger,
	}
}

// Connect opens one connection, waiting while busy, optimizes it and passes it to the seam.
func (c *connector) Connect(ctx context.Context) (driver.Conn, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	conn, err := c.openWaiting(ctx)
	if err != nil {
		return nil, err
	}
	if err := c.optimize(ctx, conn.(driver.ExecerContext)); err != nil {
		return nil, errors.Join(err, conn.Close())
	}
	return seam.Wrap(c.path, conn), nil
}

// openWaiting opens one connection, trying again every busy poll while busy, until the busy timeout passes or ctx ends.
func (c *connector) openWaiting(ctx context.Context) (driver.Conn, error) {
	deadline := time.NewTimer(c.busyTimeout)
	defer deadline.Stop()
	poll := time.NewTicker(c.busyPoll)
	defer poll.Stop()
	var busy error
	for {
		got, answered := c.openOnce(ctx)
		if !answered {
			return nil, gaveUp(ctx, busy)
		}
		if got.err == nil {
			return got.conn, nil
		}
		if !errors.Is(Classify(got.err), dbkit.ErrBusy) {
			return nil, got.err
		}
		busy = got.err
		poll.Reset(c.busyPoll)
		select {
		case <-ctx.Done():
			return nil, ended(ctx, busy)
		case <-deadline.C:
			return nil, busy
		case <-poll.C:
		}
	}
}

// openAnswer is what one try of open answered.
type openAnswer struct {
	// conn is the connection the try opened, nil after an error.
	conn driver.Conn
	// err is the error of the try.
	err error
}

// openOnce runs one try of open in the opening slot and reports false when ctx ended before the try answered.
func (c *connector) openOnce(ctx context.Context) (openAnswer, bool) {
	if !c.takeSlot(ctx) {
		return openAnswer{}, false
	}
	answer := make(chan openAnswer, 1)
	go c.try(answer)
	select {
	case got := <-answer:
		<-c.opening
		return got, true
	case <-ctx.Done():
		go c.leave(answer)
		return openAnswer{}, false
	}
}

// takeSlot takes the opening slot at once when it is free, or waits for it, and reports false when ctx ended first.
func (c *connector) takeSlot(ctx context.Context) bool {
	select {
	case c.opening <- struct{}{}:
		return true
	default:
	}
	select {
	case c.opening <- struct{}{}:
		return true
	case <-ctx.Done():
		return false
	}
}

// try sends on answer what one call of open answers.
func (c *connector) try(answer chan<- openAnswer) {
	conn, err := c.open(c.name)
	answer <- openAnswer{conn: conn, err: err}
}

// leave closes the connection a given-up try opened once it answers on answer, then frees the opening slot.
func (c *connector) leave(answer <-chan openAnswer) {
	if got := <-answer; got.err == nil {
		_ = got.conn.Close()
	}
	<-c.opening
}

// gaveUp returns the error of an open whose context ended before its try answered, marking the last busy error if any.
func gaveUp(ctx context.Context, busy error) error {
	if busy != nil {
		return ended(ctx, busy)
	}
	if cause := context.Cause(ctx); !errors.Is(cause, ctx.Err()) {
		return fmt.Errorf("dbkit: open the database: %w: %w", cause, ctx.Err())
	}
	return fmt.Errorf("dbkit: open the database: %w", ctx.Err())
}

// ended returns err marked with the error of ctx once ctx has ended, and err as it is before.
func ended(ctx context.Context, err error) error {
	if end := ctx.Err(); end != nil {
		return fmt.Errorf("%w: %w", err, end)
	}
	return err
}

// Driver returns the private driver value of the handle.
func (c *connector) Driver() driver.Driver {
	return c.driver
}

// optimize runs PRAGMA optimize at a zero busy timeout, skips a busy answer with a note and restores BusyTimeout.
func (c *connector) optimize(ctx context.Context, conn driver.ExecerContext) error {
	_, err := conn.ExecContext(ctx, lowerBusyTimeout, nil)
	if err == nil {
		_, err = conn.ExecContext(ctx, optimizeStatement, nil)
	}
	if _, restoreErr := conn.ExecContext(context.WithoutCancel(ctx), c.restore, nil); restoreErr != nil {
		return errors.Join(err, restoreErr)
	}
	if errors.Is(Classify(err), dbkit.ErrBusy) {
		c.logger.InfoContext(ctx, optimizeSkipped, slog.String("path", c.path), slog.Any("error", err))
		return nil
	}
	return err
}
