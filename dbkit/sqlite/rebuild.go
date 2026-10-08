// SPDX-License-Identifier: Apache-2.0

package sqlite

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"fmt"
)

const (
	// foreignKeysOff switches foreign key enforcement off for a connection.
	foreignKeysOff = "PRAGMA foreign_keys=OFF"
	// foreignKeysOn switches foreign key enforcement on for a connection.
	foreignKeysOn = "PRAGMA foreign_keys=ON"
	// foreignKeys reads whether a connection enforces foreign keys.
	foreignKeys = "PRAGMA foreign_keys"
	// foreignKeyCheck lists every row whose foreign key finds no parent row.
	foreignKeyCheck = "PRAGMA foreign_key_check"
)

// Rebuild runs steps for a goose RunDB migration in one transaction with foreign keys off, unless done answers true.
func Rebuild(ctx context.Context, db *sql.DB, done func(context.Context, *sql.Tx) (bool, error),
	steps func(context.Context, *sql.Tx) error) (err error) {
	if err := checkRebuild(db, done, steps); err != nil {
		return err
	}
	conn, err := db.Conn(ctx)
	if err != nil {
		return fmt.Errorf("dbkit: take a connection for the rebuild: %w", err)
	}
	defer func() {
		if restoreErr := restoreForeignKeys(ctx, conn); restoreErr != nil {
			err = errors.Join(err, restoreErr)
		}
	}()
	if _, err := conn.ExecContext(ctx, foreignKeysOff); err != nil {
		return fmt.Errorf("dbkit: switch foreign keys off for the rebuild: %w", err)
	}
	return rebuildOn(ctx, conn, done, steps)
}

// checkRebuild returns the error for the first argument of Rebuild that is nil.
func checkRebuild(db *sql.DB, done func(context.Context, *sql.Tx) (bool, error),
	steps func(context.Context, *sql.Tx) error) error {
	switch {
	case db == nil:
		return errors.New("dbkit: Rebuild needs a database handle, got nil")
	case done == nil:
		return errors.New("dbkit: Rebuild needs a done check, got nil")
	case steps == nil:
		return errors.New("dbkit: Rebuild needs its steps, got nil")
	}
	return nil
}

// rebuildOn runs the write transaction of a rebuild on conn.
func rebuildOn(ctx context.Context, conn *sql.Conn, done func(context.Context, *sql.Tx) (bool, error),
	steps func(context.Context, *sql.Tx) error) error {
	tx, err := conn.BeginTx(context.WithoutCancel(ctx), nil)
	if err != nil {
		return fmt.Errorf("dbkit: begin the rebuild: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	inPlace, err := done(ctx, tx)
	if err != nil {
		return fmt.Errorf("dbkit: check whether the rebuild already ran: %w", err)
	}
	if inPlace {
		return nil
	}
	if err := steps(ctx, tx); err != nil {
		return fmt.Errorf("dbkit: run the rebuild steps: %w", err)
	}
	if err := checkForeignKeys(ctx, tx); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("dbkit: commit the rebuild: %w", err)
	}
	return nil
}

// checkForeignKeys returns the error for the first row of tx whose foreign key finds no parent row.
func checkForeignKeys(ctx context.Context, tx *sql.Tx) error {
	var table, parent string
	var rowid, fkid any
	err := tx.QueryRowContext(ctx, foreignKeyCheck).Scan(&table, &rowid, &parent, &fkid)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		return nil
	case err != nil:
		return fmt.Errorf("dbkit: check the foreign keys after the rebuild: %w", err)
	}
	return fmt.Errorf("dbkit: PRAGMA foreign_key_check found a row of %s with no parent row in %s", table, parent)
}

// restoreForeignKeys switches foreign keys back on for conn and closes it, or discards it when they stay off.
func restoreForeignKeys(ctx context.Context, conn *sql.Conn) error {
	if err := switchOn(context.WithoutCancel(ctx), conn); err != nil {
		_ = conn.Raw(badConn)
		return err
	}
	return conn.Close()
}

// switchOn switches foreign keys on for conn and returns an error unless they read back as on.
func switchOn(ctx context.Context, conn *sql.Conn) error {
	if _, err := conn.ExecContext(ctx, foreignKeysOn); err != nil {
		return fmt.Errorf("dbkit: switch foreign keys back on after the rebuild: %w", err)
	}
	var on int64
	if err := conn.QueryRowContext(ctx, foreignKeys).Scan(&on); err != nil {
		return fmt.Errorf("dbkit: read foreign keys back after the rebuild: %w", err)
	}
	if on != 1 {
		return fmt.Errorf("dbkit: foreign keys read back as %d after the rebuild, want 1", on)
	}
	return nil
}

// badConn answers driver.ErrBadConn for every connection.
func badConn(any) error {
	return driver.ErrBadConn
}
