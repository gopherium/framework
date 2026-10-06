// SPDX-License-Identifier: Apache-2.0

package dbkit_test

import (
	"context"
	"database/sql"
	"errors"
	"slices"
	"testing"
	"testing/synctest"

	"github.com/gopherium/framework/dbkit"
)

// shareReadSet scans every row of the current result set and returns the numbers read.
func shareReadSet(t *testing.T, rows *dbkit.Rows) []int64 {
	t.Helper()
	var got []int64
	for rows.Next() {
		var n int64
		if err := rows.Scan(&n); err != nil {
			t.Fatalf("Scan() error = %v, want nil", err)
		}
		got = append(got, n)
	}
	return got
}

func TestRowsErrIsNilAfterAFullRead(t *testing.T) {
	t.Parallel()

	share, fake := shareOpen(t, dbkit.Postgres, shareOptions(1))
	fake.answer(3)
	rows, err := share.Query(t.Context(), shareRead)
	if err != nil {
		t.Fatalf("Query() error = %v, want nil", err)
	}

	got := shareReadSet(t, rows)

	if want := []int64{1, 2, 3}; !slices.Equal(got, want) {
		t.Errorf("rows read = %v, want %v", got, want)
	}
	if err := rows.Err(); err != nil {
		t.Errorf("Err() after a full read = %v, want nil", err)
	}
	if err := rows.Close(); err != nil {
		t.Errorf("Close() after a full read = %v, want nil", err)
	}
}

func TestRowsReadEveryResultSet(t *testing.T) {
	t.Parallel()

	share, fake := shareOpen(t, dbkit.Postgres, shareOptions(1))
	fake.answer(1, 2)
	rows, err := share.Query(t.Context(), shareRead)
	if err != nil {
		t.Fatalf("Query() error = %v, want nil", err)
	}

	first := shareReadSet(t, rows)
	more := rows.NextResultSet()
	second := shareReadSet(t, rows)
	last := rows.NextResultSet()

	if !slices.Equal(first, []int64{1}) || !more || !slices.Equal(second, []int64{1, 2}) || last {
		t.Errorf("result sets = %v, %t, %v, %t, want [1], true, [1 2], false", first, more, second, last)
	}
	if err := rows.Err(); err != nil {
		t.Errorf("Err() after every result set = %v, want nil", err)
	}
}

func TestALeakedResultSetFreesItsSlotWhenItsContextEnds(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		end  func(cancel context.CancelFunc)
	}{
		{"a cancelled caller context", func(cancel context.CancelFunc) { cancel() }},
		{"the statement deadline", func(context.CancelFunc) {}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			synctest.Test(t, func(t *testing.T) {
				share, fake := shareOpen(t, dbkit.Postgres, shareOptions(1))
				ctx, cancel := context.WithCancel(t.Context())
				defer cancel()
				if _, err := share.Query(ctx, shareRead); err != nil {
					t.Fatalf("Query() error = %v, want nil", err)
				}

				c.end(cancel)

				if got := <-fake.events; got != "close "+shareRead {
					t.Fatalf("driver event = %q, want the leaked rows closed", got)
				}
				shareFree(t, share, fake)
			})
		})
	}
}

func TestALeakedRowFreesItsSlotWhenItsContextEnds(t *testing.T) {
	t.Parallel()

	synctest.Test(t, func(t *testing.T) {
		share, fake := shareOpen(t, dbkit.Postgres, shareOptions(1))
		ctx, cancel := context.WithCancel(t.Context())
		if err := share.QueryRow(ctx, shareRead).Err(); err != nil {
			t.Fatalf("QueryRow().Err() = %v, want nil", err)
		}

		cancel()

		if got := <-fake.events; got != "close "+shareRead {
			t.Fatalf("driver event = %q, want the leaked row closed", got)
		}
		shareFree(t, share, fake)
	})
}

func TestAReadCutByItsDeadlineReportsTheDeadline(t *testing.T) {
	t.Parallel()

	for range 100 {
		synctest.Test(t, func(t *testing.T) {
			share, fake := shareOpen(t, dbkit.Postgres, shareOptions(1))
			fake.answer(3)
			rows := shareMustQuery(t, share)
			if !rows.Next() {
				t.Fatalf("Next() = false before the deadline, want the first row")
			}

			if got := <-fake.events; got != "close "+shareRead {
				t.Fatalf("driver event = %q, want the rows closed at the deadline", got)
			}
			for rows.Next() {
			}

			if err := rows.Err(); !errors.Is(err, context.DeadlineExceeded) {
				t.Fatalf("Err() after a read cut by the deadline = %v, want it to match %v", err, context.DeadlineExceeded)
			}
		})
	}
}

func TestACancelledRowNeverReportsNoRows(t *testing.T) {
	t.Parallel()

	for range 100 {
		synctest.Test(t, func(t *testing.T) {
			share, fake := shareOpen(t, dbkit.Postgres, shareOptions(1))
			ctx, cancel := context.WithCancel(t.Context())
			row := share.QueryRow(ctx, shareRead)

			cancel()
			if got := <-fake.events; got != "close "+shareRead {
				t.Fatalf("driver event = %q, want the row closed", got)
			}
			synctest.Wait()

			var n int64
			if err := row.Scan(&n); errors.Is(err, sql.ErrNoRows) || !errors.Is(err, context.Canceled) {
				t.Fatalf("Scan() after a cancel = %v, want it to match %v and never %v", err, context.Canceled, sql.ErrNoRows)
			}
		})
	}
}

func TestErrIsNilAfterAnEarlyClose(t *testing.T) {
	t.Parallel()

	for range 100 {
		synctest.Test(t, func(t *testing.T) {
			share, fake := shareOpen(t, dbkit.Postgres, shareOptions(1))
			fake.answer(3, 2)
			for _, early := range []func(rows *dbkit.Rows) error{
				func(rows *dbkit.Rows) error { return rows.Close() },
				func(rows *dbkit.Rows) error {
					rows.NextResultSet()
					rows.NextResultSet()
					return nil
				},
			} {
				rows := shareMustQuery(t, share)
				if !rows.Next() {
					t.Fatalf("Next() = false, want the first row")
				}

				if err := early(rows); err != nil {
					t.Fatalf("Close() error = %v, want nil", err)
				}
				synctest.Wait()

				if err := rows.Err(); err != nil {
					t.Fatalf("Err() after the reader ended the rows early = %v, want nil", err)
				}
			}
		})
	}
}

func TestRowsScanRefusesRawBytes(t *testing.T) {
	t.Parallel()

	share, _ := shareOpen(t, dbkit.Postgres, shareOptions(1))
	rows := shareMustQuery(t, share)
	defer func() { _ = rows.Close() }()
	if !rows.Next() {
		t.Fatalf("Next() = false, want the first row")
	}

	var raw sql.RawBytes
	err := rows.Scan(&raw)

	if want := "dbkit: Scan takes no *sql.RawBytes"; err == nil || err.Error() != want {
		t.Errorf("Rows.Scan(&raw) error = %v, want %q", err, want)
	}
}

func TestRowScanMirrorsDatabaseSQL(t *testing.T) {
	t.Parallel()

	t.Run("a row", func(t *testing.T) {
		t.Parallel()
		share, fake := shareOpen(t, dbkit.Postgres, shareOptions(1))
		fake.answer(2)
		row := share.QueryRow(t.Context(), shareRead)

		var n int64
		err := row.Scan(&n)

		if err != nil || n != 1 || row.Err() != nil {
			t.Errorf("Scan() = %d, %v, Err() = %v, want 1 and nil twice", n, err, row.Err())
		}
		if !fake.saw("close", shareRead) {
			t.Errorf("driver calls = %v, want the rows closed after Scan", fake.log())
		}
	})
	t.Run("no row", func(t *testing.T) {
		t.Parallel()
		share, fake := shareOpen(t, dbkit.Postgres, shareOptions(1))
		fake.answer(0)

		var n int64
		err := share.QueryRow(t.Context(), shareRead).Scan(&n)
		if !errors.Is(err, sql.ErrNoRows) || errors.Is(err, context.Canceled) {
			t.Errorf("Scan() error = %v, want %v alone", err, sql.ErrNoRows)
		}
	})
	t.Run("a raw bytes destination", func(t *testing.T) {
		t.Parallel()
		share, fake := shareOpen(t, dbkit.Postgres, shareOptions(1))

		var raw sql.RawBytes
		err := share.QueryRow(t.Context(), shareRead).Scan(&raw)

		if want := "dbkit: Scan takes no *sql.RawBytes"; err == nil || err.Error() != want {
			t.Errorf("Scan() error = %v, want %q", err, want)
		}
		if !fake.saw("close", shareRead) {
			t.Errorf("driver calls = %v, want the rows closed after a refused Scan", fake.log())
		}
	})
	t.Run("one destination too many", func(t *testing.T) {
		t.Parallel()
		share, fake := shareOpen(t, dbkit.Postgres, shareOptions(1))

		var first, second int64
		if err := share.QueryRow(t.Context(), shareRead).Scan(&first, &second); err == nil {
			t.Errorf("Scan() error = nil, want the count of destinations refused")
		}
		if !fake.saw("close", shareRead) {
			t.Errorf("driver calls = %v, want the rows closed after a failed Scan", fake.log())
		}
	})
	t.Run("a failed query", func(t *testing.T) {
		t.Parallel()
		share, fake := shareOpen(t, dbkit.Postgres, shareOptions(1))
		fake.fail(shareRead)
		row := share.QueryRow(t.Context(), shareRead)

		var n int64
		err := row.Scan(&n)

		if row.Err() == nil || row.Err().Error() != string(fakeBroken) || !errors.Is(err, fakeBroken) {
			t.Errorf("Err() = %v, Scan() = %v, want %q from both", row.Err(), err, fakeBroken)
		}
	})
}
