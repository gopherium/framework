// SPDX-License-Identifier: Apache-2.0

package sqlite_test

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"testing"
	"time"

	"github.com/gopherium/framework/dbkit"
	"github.com/gopherium/framework/dbkit/sqlite"
)

// busyAuxInsert writes to the attached rollback journal database without committing yet.
const busyAuxInsert = "INSERT INTO aux.aux_rows VALUES (1)"

// busyCommitRolledBack checks that the write is gone and another transaction can use the same connection.
func busyCommitRolledBack(t *testing.T, pinned *sql.Conn) {
	t.Helper()
	var n int64
	if err := pinned.QueryRowContext(t.Context(), "SELECT count(*) FROM aux.aux_rows").Scan(&n); err != nil {
		t.Fatalf("reading aux after rollback error = %v, want nil", err)
	}
	if n != 0 {
		t.Errorf("rows after rollback = %d, want 0", n)
	}
	tx, err := pinned.BeginTx(t.Context(), nil)
	if err != nil {
		t.Fatalf("BeginTx() after rollback error = %v, want a clean connection", err)
	}
	if err := tx.Rollback(); err != nil {
		t.Fatalf("Rollback() of the next transaction error = %v, want nil", err)
	}
}

// TestABusyCommitRollsBackBeforeReturning verifies a failed commit rolls back its write
// while the reader keeps its lock, leaving the connection ready for another transaction.
func TestABusyCommitRollsBackBeforeReturning(t *testing.T) {
	t.Parallel()

	opts := busyOptions(busyPoll)
	opts.BusyTimeout = rulesShortBusyTimeout
	pinned := busyAttachHeld(t, rulesOpen(t, opts), "BEGIN", busyAuxRead)
	tx, err := pinned.BeginTx(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := tx.ExecContext(t.Context(), busyAuxInsert); err != nil {
		t.Fatal(err)
	}
	began := time.Now()

	err = tx.Commit()

	if !errors.Is(sqlite.Classify(err), dbkit.ErrBusy) {
		t.Errorf("Commit() error = %v, want a busy error while the reader keeps its lock", err)
	}
	if waited := time.Since(began); waited > busyBound {
		t.Errorf("Commit() and its rollback ran for %v, want within %v", waited, busyBound)
	}
	busyCommitRolledBack(t, pinned)
}

// TestARollbackAfterCancellationStillEndsTheTransaction verifies rollback finishes
// after cancellation and leaves no pending writes or active transaction.
func TestARollbackAfterCancellationStillEndsTheTransaction(t *testing.T) {
	t.Parallel()

	pinned := busyAttachHeld(t, rulesOpen(t, busyOptions(busyPoll)), "BEGIN", busyAuxRead)
	err := pinned.Raw(func(raw any) error {
		ctx, cancel := context.WithCancel(t.Context())
		defer cancel()
		tx, err := raw.(driver.ConnBeginTx).BeginTx(ctx, driver.TxOptions{})
		if err != nil {
			return err
		}
		if _, err := raw.(driver.ExecerContext).ExecContext(ctx, busyAuxInsert, nil); err != nil {
			_ = tx.Rollback()
			return err
		}
		cancel()
		return tx.Rollback()
	})
	if err != nil {
		t.Fatalf("Rollback() after cancellation error = %v, want nil", err)
	}
	busyCommitRolledBack(t, pinned)
}

// TestABusyCommitEndsWithItsTransactionContext verifies cancellation and deadlines
// end commit lock waits at both polling intervals and leave the connection reusable.
func TestABusyCommitEndsWithItsTransactionContext(t *testing.T) {
	t.Parallel()

	ends := []struct {
		name string
		end  func(context.Context) (context.Context, context.CancelFunc)
		want error
	}{
		{"deadline", busyDeadlineOf, context.DeadlineExceeded},
		{"cancellation", busyCancelOf, context.Canceled},
	}
	for _, end := range ends {
		for _, poll := range []time.Duration{busyPoll, optionsBusyTimeout} {
			t.Run(end.name+" at poll "+poll.String(), func(t *testing.T) {
				t.Parallel()
				pinned := busyAttachHeld(t, rulesOpen(t, busyOptions(poll)), "BEGIN", busyAuxRead)
				ctx, cancel := end.end(t.Context())
				defer cancel()
				tx, err := pinned.BeginTx(ctx, nil)
				if err != nil {
					t.Fatal(err)
				}
				if _, err := tx.ExecContext(ctx, busyAuxInsert); err != nil {
					t.Fatal(err)
				}
				began := time.Now()

				err = tx.Commit()

				busyMustEnd(t, busyShape{name: "Commit", busy: true},
					busyEnd{err: err, waited: time.Since(began)}, end.want)
				busyCommitRolledBack(t, pinned)
			})
		}
	}
}
