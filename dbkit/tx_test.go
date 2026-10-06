// SPDX-License-Identifier: Apache-2.0

package dbkit_test

import (
	"context"
	"database/sql"
	"errors"
	"testing"
	"testing/synctest"
	"time"

	"github.com/gopherium/framework/dbkit"
)

func TestStatementsInsideATransactionTakeNoSlot(t *testing.T) {
	t.Parallel()

	synctest.Test(t, func(t *testing.T) {
		share, fake := shareOpen(t, dbkit.Postgres, shareOptions(1))
		tx := shareMustBegin(t, share)
		start := time.Now()

		for _, kind := range shareKinds {
			if err := kind.run(t.Context(), tx, shareRead); err != nil {
				t.Errorf("Tx.%s() error = %v, want it run with no slot", kind.name, err)
			}
		}

		if took := time.Since(start); took != 0 {
			t.Errorf("statements in the transaction took %v, want no wait", took)
		}
		if err := tx.Commit(); err != nil {
			t.Fatalf("Commit() error = %v, want nil", err)
		}
		shareFree(t, share, fake)
	})
}

func TestALeakedTransactionFreesItsSlotWhenItsContextEnds(t *testing.T) {
	t.Parallel()

	synctest.Test(t, func(t *testing.T) {
		share, fake := shareOpen(t, dbkit.Postgres, shareOptions(1))
		ctx, cancel := context.WithCancel(t.Context())
		if _, err := share.Begin(ctx); err != nil {
			t.Fatalf("Begin() error = %v, want nil", err)
		}

		cancel()

		if got := <-fake.events; got != "rollback" {
			t.Fatalf("driver event = %q, want rollback", got)
		}
		shareFree(t, share, fake)
	})
}

func TestATransactionEndsAtItsTimeout(t *testing.T) {
	t.Parallel()

	synctest.Test(t, func(t *testing.T) {
		share, fake := shareOpen(t, dbkit.Postgres, shareOptions(1))
		tx := shareMustBegin(t, share)
		start := time.Now()

		got := <-fake.events

		if got != "rollback" {
			t.Fatalf("driver event = %q, want rollback", got)
		}
		if took := time.Since(start); took != shareTransactionTimeout {
			t.Errorf("transaction lived %v, want the transaction timeout %v", took, shareTransactionTimeout)
		}
		shareFree(t, share, fake)
		if err := tx.Commit(); !errors.Is(err, sql.ErrTxDone) {
			t.Errorf("Commit() after the timeout error = %v, want %v", err, sql.ErrTxDone)
		}
	})
}

func TestAnEndedTransactionGivesItsSlotBackOnlyAfterItsRollback(t *testing.T) {
	t.Parallel()

	ends := []struct {
		name string
		end  func(cancel context.CancelFunc)
	}{
		{"timeout", func(context.CancelFunc) {}},
		{"cancel", func(cancel context.CancelFunc) { cancel() }},
	}
	for _, e := range ends {
		synctest.Test(t, func(t *testing.T) {
			share, fake := shareOpen(t, dbkit.Postgres, shareOptions(1))
			rollback := fake.holdRollback()
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			if _, err := share.Begin(ctx); err != nil {
				t.Fatalf("%s: Begin() error = %v, want nil", e.name, err)
			}

			e.end(cancel)
			if got := <-fake.events; got != "rollback" {
				t.Fatalf("%s: driver event = %q, want rollback", e.name, got)
			}
			done := shareStart(context.Background(), share)

			shareWaiting(t, fake, done)
			close(rollback)
			if err := <-done; err != nil {
				t.Errorf("%s: Exec() after the rollback error = %v, want nil", e.name, err)
			}
		})
	}
}

func TestBeginPastTheTransactionTimeoutMatchesDeadlineExceeded(t *testing.T) {
	t.Parallel()

	synctest.Test(t, func(t *testing.T) {
		share, fake := shareOpen(t, dbkit.Postgres, shareOptions(1))
		fake.hold("")
		start := time.Now()

		tx, err := share.Begin(t.Context())

		if tx != nil || !errors.Is(err, context.DeadlineExceeded) || !errors.Is(err, fakeInterrupted) {
			t.Errorf("Begin() = %v, %v, want nil and the driver error matching the deadline", tx, err)
		}
		if took := time.Since(start); took != shareTransactionTimeout {
			t.Errorf("Begin() took %v, want the transaction timeout %v", took, shareTransactionTimeout)
		}
		shareFree(t, share, fake)
	})
}

func TestACommitAfterTheTransactionEndedNeverCommits(t *testing.T) {
	t.Parallel()

	ends := []struct {
		name string
		want error
		end  func(cancel context.CancelFunc)
	}{
		{"cancel", context.Canceled, func(cancel context.CancelFunc) { cancel() }},
		{"timeout", context.DeadlineExceeded, func(context.CancelFunc) {
			time.Sleep(shareTransactionTimeout)
			synctest.Wait()
		}},
	}
	for _, e := range ends {
		for range 20 {
			synctest.Test(t, func(t *testing.T) {
				share, fake := shareOpen(t, dbkit.Postgres, shareOptions(1))
				ctx, cancel := context.WithCancel(t.Context())
				defer cancel()
				tx, err := share.Begin(ctx)
				if err != nil {
					t.Fatalf("%s: Begin() error = %v, want nil", e.name, err)
				}

				e.end(cancel)
				err = tx.Commit()

				if !errors.Is(err, sql.ErrTxDone) || !errors.Is(err, e.want) {
					t.Fatalf("%s: Commit() error = %v, want it to match %v and %v", e.name, err, sql.ErrTxDone, e.want)
				}
				if fake.saw("commit", "") || !fake.saw("rollback", "") {
					t.Fatalf("%s: driver calls = %v, want a rollback and no commit", e.name, fake.log())
				}
				shareFree(t, share, fake)
			})
		}
	}
}

func TestACommitStopsAtTheTransactionDeadline(t *testing.T) {
	t.Parallel()

	synctest.Test(t, func(t *testing.T) {
		share, fake := shareOpen(t, dbkit.Postgres, shareOptions(1))
		fake.holdCommit()
		start := time.Now()
		tx := shareMustBegin(t, share)

		err := tx.Commit()

		if !errors.Is(err, context.DeadlineExceeded) || !errors.Is(err, fakeInterrupted) {
			t.Errorf("Commit() error = %v, want the driver error matching the deadline", err)
		}
		if took := time.Since(start); took != shareTransactionTimeout {
			t.Errorf("Commit() returned after %v, want the transaction timeout %v", took, shareTransactionTimeout)
		}
		shareFree(t, share, fake)
	})
}

func TestATimeoutRollbackStopsAfterOneStatementTimeout(t *testing.T) {
	t.Parallel()

	synctest.Test(t, func(t *testing.T) {
		share, fake := shareOpen(t, dbkit.Postgres, shareOptions(1))
		fake.holdRollback()
		start := time.Now()
		shareMustBegin(t, share)
		if got := <-fake.events; got != "rollback" {
			t.Fatalf("driver event = %q, want rollback", got)
		}

		time.Sleep(shareStatementTimeout)
		synctest.Wait()
		shareFree(t, share, fake)

		if took, want := time.Since(start), shareTransactionTimeout+shareStatementTimeout; took != want {
			t.Errorf("the slot came back after %v, want the transaction and the statement timeout %v", took, want)
		}
	})
}

func TestAStatementInsideATransactionEndsWithIt(t *testing.T) {
	t.Parallel()

	ends := []struct {
		name string
		want error
		end  func(cancel context.CancelFunc)
	}{
		{"timeout", context.DeadlineExceeded, func(context.CancelFunc) {}},
		{"cancel", context.Canceled, func(cancel context.CancelFunc) { cancel() }},
	}
	for _, e := range ends {
		synctest.Test(t, func(t *testing.T) {
			opts := shareOptions(1)
			opts.TransactionTimeout = time.Second
			share, fake := shareOpen(t, dbkit.Postgres, opts)
			fake.hold(shareRead)
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			start := time.Now()
			tx, err := share.Begin(ctx)
			if err != nil {
				t.Fatalf("%s: Begin() error = %v, want nil", e.name, err)
			}
			done := make(chan error, 1)
			go func() {
				_, err := tx.Exec(context.Background(), shareRead)
				done <- err
			}()
			synctest.Wait()

			e.end(cancel)
			err = <-done

			if !errors.Is(err, e.want) {
				t.Errorf("%s: Tx.Exec() error = %v, want it to match %v", e.name, err, e.want)
			}
			if took := time.Since(start); e.name == "timeout" && took != opts.TransactionTimeout {
				t.Errorf("%s: Tx.Exec() returned after %v, want the transaction timeout %v", e.name, took, opts.TransactionTimeout)
			}
			if got := <-fake.events; got != "rollback" {
				t.Errorf("%s: driver event = %q, want rollback", e.name, got)
			}
			shareFree(t, share, fake)
		})
	}
}

func TestASlotFreedAtTheDeadlineNeverStartsALateTransaction(t *testing.T) {
	t.Parallel()

	for range 50 {
		synctest.Test(t, func(t *testing.T) {
			share, fake := shareOpen(t, dbkit.Postgres, shareOptions(1))
			holder := shareMustBegin(t, share)
			time.AfterFunc(shareStatementTimeout, func() { _ = holder.Rollback() })

			tx, err := share.Begin(t.Context())

			if tx != nil || !errors.Is(err, context.DeadlineExceeded) {
				t.Fatalf("Begin() at its deadline = %v, %v, want nil and an error matching the deadline", tx, err)
			}
			begins := 0
			for _, line := range fake.log() {
				if line == "begin" {
					begins++
				}
			}
			if begins != 1 {
				t.Fatalf("driver calls = %v, want only the holder's begin", fake.log())
			}
		})
	}
}

func TestBeginWaitsForAConnectionWithinTheTransactionTimeout(t *testing.T) {
	t.Parallel()

	synctest.Test(t, func(t *testing.T) {
		db, fake := shareHandle(t)
		db.SetMaxOpenConns(1)
		taken, err := db.Conn(t.Context())
		if err != nil {
			t.Fatalf("Conn() error = %v, want nil", err)
		}
		share := shareNew(t, db, dbkit.Postgres, shareOptions(1))
		start := time.Now()

		tx, err := share.Begin(t.Context())

		if tx != nil || !errors.Is(err, context.DeadlineExceeded) {
			t.Errorf("Begin() = %v, %v, want nil and an error matching the deadline", tx, err)
		}
		if took := time.Since(start); took != shareTransactionTimeout {
			t.Errorf("Begin() took %v, want the transaction timeout %v", took, shareTransactionTimeout)
		}
		if fake.saw("begin", "") {
			t.Errorf("driver calls = %v, want no begin without a connection", fake.log())
		}
		if err := taken.Close(); err != nil {
			t.Fatalf("Close() error = %v, want nil", err)
		}
		shareFree(t, share, fake)
	})
}

func TestABeginThatOutlastsTheTransactionTimeoutIsRolledBack(t *testing.T) {
	t.Parallel()

	synctest.Test(t, func(t *testing.T) {
		share, fake := shareOpen(t, dbkit.Postgres, shareOptions(1))
		gate := fake.holdPast("")
		done := make(chan error, 1)
		go func() {
			_, err := share.Begin(t.Context())
			done <- err
		}()

		if got := <-fake.events; got != "context ended" {
			t.Fatalf("driver event = %q, want the begin context ended at the timeout", got)
		}
		close(gate)

		if err := <-done; !errors.Is(err, context.DeadlineExceeded) {
			t.Errorf("Begin() error = %v, want an error matching the deadline", err)
		}
		if got := <-fake.events; got != "rollback" {
			t.Errorf("driver event = %q, want rollback", got)
		}
		shareFree(t, share, fake)
	})
}

func TestABeginErrorComesBackAsItIs(t *testing.T) {
	t.Parallel()

	synctest.Test(t, func(t *testing.T) {
		share, fake := shareOpen(t, dbkit.Postgres, shareOptions(1))
		fake.fail("")

		tx, err := share.Begin(t.Context())

		if tx != nil || err == nil || err.Error() != string(fakeBroken) {
			t.Errorf("Begin() = %v, %v, want nil and %q unwrapped", tx, err, fakeBroken)
		}
		shareFree(t, share, fake)
	})
}
