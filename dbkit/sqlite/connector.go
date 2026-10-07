// SPDX-License-Identifier: Apache-2.0

package sqlite

import (
	"context"
	"database/sql/driver"
	"errors"
	"log/slog"
	"strconv"

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
	// name is the driver's name of the file with every rule.
	name string
	// path is the absolute path of the file.
	path string
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
		driver:  drv,
		open:    drv.Open,
		name:    connectionString(path, opts),
		path:    path,
		restore: "PRAGMA busy_timeout=" + strconv.FormatInt(opts.BusyTimeout.Milliseconds(), 10),
		logger:  logger,
	}
}

// Connect opens one connection to the file, runs the optimize step on it and passes it through the path's seam.
func (c *connector) Connect(ctx context.Context) (driver.Conn, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	conn, err := c.open(c.name)
	if err != nil {
		return nil, err
	}
	if err := c.optimize(ctx, conn.(driver.ExecerContext)); err != nil {
		return nil, errors.Join(err, conn.Close())
	}
	return seam.Wrap(c.path, conn), nil
}

// Driver returns the private driver value of the handle.
func (c *connector) Driver() driver.Driver {
	return c.driver
}

// optimize runs PRAGMA optimize on conn and skips a busy answer with a note.
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
