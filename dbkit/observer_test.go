// SPDX-License-Identifier: Apache-2.0

package dbkit_test

import (
	"context"
	"database/sql"
	"errors"
	"slices"
	"testing"
	"testing/synctest"
	"time"

	"github.com/gopherium/framework/dbkit"
)

// shareRefusedError is the error the refusing observer of the tests answers.
const shareRefusedError fakeError = "observer: refused"

// shareRefuseKey marks a context whose statements the refusing observer refuses.
type shareRefuseKey struct{}

// shareRefuser refuses every statement sent under a context marked with shareRefuseKey.
func shareRefuser(ctx context.Context, _ dbkit.Statement) error {
	if ctx.Value(shareRefuseKey{}) != nil {
		return shareRefusedError
	}
	return nil
}

// shareNew returns a share of db and fails the test when it cannot.
func shareNew(tb testing.TB, db *sql.DB, engine dbkit.Engine, opts dbkit.ShareOptions) *dbkit.Share {
	tb.Helper()
	share, err := dbkit.NewShare(db, engine, opts)
	if err != nil {
		tb.Fatalf("NewShare() error = %v, want nil", err)
	}
	return share
}

// shareWatched returns a share on engine whose observer sends every statement it sees on the returned channel.
func shareWatched(t *testing.T, engine dbkit.Engine) (*dbkit.Share, *fakeDriver, <-chan dbkit.Statement) {
	t.Helper()
	db, fake := shareHandle(t)
	seen := make(chan dbkit.Statement, 32)
	opts := shareOptions(1)
	opts.Observer = func(_ context.Context, s dbkit.Statement) error {
		seen <- s
		return nil
	}
	return shareNew(t, db, engine, opts), fake, seen
}

// shareDrain returns every statement already waiting on seen.
func shareDrain(seen <-chan dbkit.Statement) []dbkit.Statement {
	var got []dbkit.Statement
	for {
		select {
		case s := <-seen:
			got = append(got, s)
		default:
			return got
		}
	}
}

func TestTheObserverRunsBeforeTheDriver(t *testing.T) {
	t.Parallel()

	db, fake := shareHandle(t)
	seen := make(chan []string, 1)
	opts := shareOptions(1)
	opts.Observer = func(context.Context, dbkit.Statement) error {
		seen <- fake.log()
		return nil
	}
	share := shareNew(t, db, dbkit.Postgres, opts)
	steps := []struct {
		name string
		run  func() error
	}{
		{"Exec", func() error { return shareExec(t.Context(), share, shareRead) }},
		{"Query", func() error { return shareQuery(t.Context(), share, shareRead) }},
		{"QueryRow", func() error { return shareQueryRow(t.Context(), share, shareRead) }},
		{"Begin", func() error {
			tx, err := share.Begin(t.Context())
			if err != nil {
				return err
			}
			return tx.Commit()
		}},
	}

	for _, step := range steps {
		before := fake.log()
		if err := step.run(); err != nil {
			t.Fatalf("%s() error = %v, want nil", step.name, err)
		}

		if atObserver := <-seen; !slices.Equal(atObserver, before) {
			t.Errorf("%s: driver calls when the observer ran = %v, want %v", step.name, atObserver, before)
		}
		if after := fake.log(); len(after) <= len(before) {
			t.Errorf("%s: driver calls = %v, want the statement sent after the observer", step.name, after)
		}
	}
}

func TestARefusedStatementTakesNoSlotAndNeverReachesTheDriver(t *testing.T) {
	t.Parallel()

	synctest.Test(t, func(t *testing.T) {
		db, fake := shareHandle(t)
		opts := shareOptions(1)
		opts.Observer = shareRefuser
		share := shareNew(t, db, dbkit.Postgres, opts)
		refused := context.WithValue(shareBudget(t, 10).Attach(t.Context()), shareRefuseKey{}, true)
		held := shareMustQuery(t, share)
		start := time.Now()

		_, execErr := share.Exec(refused, shareWrite, 1)
		_, queryErr := share.Query(refused, shareWrite, 1)
		row := share.QueryRow(refused, shareWrite, 1)
		var n int64
		scanErr := row.Scan(&n)
		_, beginErr := share.Begin(refused)
		if err := held.Close(); err != nil {
			t.Fatalf("Close() error = %v, want nil", err)
		}
		tx := shareMustBegin(t, share)
		_, txErr := tx.Exec(refused, shareWrite, 1)
		if err := tx.Commit(); err != nil {
			t.Fatalf("Commit() error = %v, want nil", err)
		}

		errs := map[string]error{
			"Exec": execErr, "Query": queryErr, "QueryRow().Err": row.Err(), "QueryRow().Scan": scanErr,
			"Begin": beginErr, "Tx.Exec": txErr,
		}
		for name, err := range errs {
			if !errors.Is(err, dbkit.ErrRefused) || !errors.Is(err, shareRefusedError) {
				t.Errorf("%s() error = %v, want %v and %v", name, err, dbkit.ErrRefused, shareRefusedError)
			}
		}
		if took := time.Since(start); took != 0 {
			t.Errorf("refused statements took %v, want no wait for the held slot", took)
		}
		if want := []string{"query SELECT 1", "close SELECT 1", "begin", "commit"}; !slices.Equal(fake.log(), want) {
			t.Errorf("driver calls = %v, want %v and no refused statement", fake.log(), want)
		}
		if count, _ := dbkit.QueriesIn(refused); count.Total != 4 {
			t.Errorf("QueriesIn().Total = %d, want the 4 refused statements counted", count.Total)
		}
		shareFree(t, share, fake)
	})
}

func TestTheObserverSeesBeginAndStatementsInATransaction(t *testing.T) {
	t.Parallel()

	share, _, seen := shareWatched(t, dbkit.Postgres)
	committed := shareMustBegin(t, share)
	for _, kind := range shareKinds {
		if err := kind.run(t.Context(), committed, shareWrite, 1); err != nil {
			t.Fatalf("Tx.%s() error = %v, want nil", kind.name, err)
		}
	}
	if err := committed.Commit(); err != nil {
		t.Fatalf("Commit() error = %v, want nil", err)
	}
	rolledBack := shareMustBegin(t, share)
	if err := rolledBack.Rollback(); err != nil {
		t.Fatalf("Rollback() error = %v, want nil", err)
	}

	begin := dbkit.Statement{ID: shareID, Kind: dbkit.KindBegin, Writes: true}
	want := []dbkit.Statement{
		begin,
		{ID: shareID, Kind: dbkit.KindExec, Query: shareWrite, Writes: true, InTransaction: true},
		{ID: shareID, Kind: dbkit.KindQuery, Query: shareWrite, Writes: true, InTransaction: true},
		{ID: shareID, Kind: dbkit.KindQueryRow, Query: shareWrite, Writes: true, InTransaction: true},
		begin,
	}
	if got := shareDrain(seen); !slices.Equal(got, want) {
		t.Errorf("observed = %+v, want %+v and no Commit or Rollback", got, want)
	}
}

func TestTheObserverSeesTheRequestCountAtBegin(t *testing.T) {
	t.Parallel()

	share, _, seen := shareWatched(t, dbkit.Postgres)
	budget, err := dbkit.NewQueryBudget(1)
	if err != nil {
		t.Fatalf("NewQueryBudget(1) error = %v, want nil", err)
	}
	request := budget.Attach(t.Context())
	for range 2 {
		if _, err := share.Exec(request, shareRead); err != nil {
			t.Fatalf("Exec() error = %v, want nil", err)
		}
	}
	tx, err := share.Begin(request)
	if err != nil {
		t.Fatalf("Begin() error = %v, want nil", err)
	}
	if err := tx.Rollback(); err != nil {
		t.Fatalf("Rollback() error = %v, want nil", err)
	}

	observed := shareDrain(seen)
	want := dbkit.Statement{ID: shareID, Kind: dbkit.KindBegin, Writes: true, Count: 2, PastBudget: true}
	if len(observed) != 3 || observed[2] != want {
		t.Errorf("observed = %+v, want Begin last as %+v", observed, want)
	}
	if count, _ := dbkit.QueriesIn(request); count.Total != 2 {
		t.Errorf("QueriesIn().Total = %d, want 2 with Begin not counted", count.Total)
	}
}

func TestBeginAtTheBudgetIsNotPastIt(t *testing.T) {
	t.Parallel()

	share, _, seen := shareWatched(t, dbkit.Postgres)
	budget, err := dbkit.NewQueryBudget(1)
	if err != nil {
		t.Fatalf("NewQueryBudget(1) error = %v, want nil", err)
	}
	request := budget.Attach(t.Context())
	if _, err := share.Exec(request, shareRead); err != nil {
		t.Fatalf("Exec() error = %v, want nil", err)
	}
	tx, err := share.Begin(request)
	if err != nil {
		t.Fatalf("Begin() error = %v, want nil", err)
	}
	if err := tx.Rollback(); err != nil {
		t.Fatalf("Rollback() error = %v, want nil", err)
	}

	observed := shareDrain(seen)
	want := dbkit.Statement{ID: shareID, Kind: dbkit.KindBegin, Writes: true, Count: 1}
	if len(observed) != 2 || observed[1] != want {
		t.Errorf("observed = %+v, want Begin last as %+v", observed, want)
	}
}

func TestKindNamesItself(t *testing.T) {
	t.Parallel()

	cases := []struct {
		kind dbkit.Kind
		want string
	}{
		{dbkit.KindExec, "Exec"},
		{dbkit.KindQuery, "Query"},
		{dbkit.KindQueryRow, "QueryRow"},
		{dbkit.KindBegin, "Begin"},
		{dbkit.Kind(0), "Kind(0)"},
		{dbkit.Kind(9), "Kind(9)"},
	}
	for _, c := range cases {
		if got := c.kind.String(); got != c.want {
			t.Errorf("Kind(%d).String() = %q, want %q", int(c.kind), got, c.want)
		}
	}
}

func TestTheObserverSeesTheStatementAsWritten(t *testing.T) {
	t.Parallel()

	share, fake, seen := shareWatched(t, dbkit.SQLite)
	kinds := []dbkit.Kind{dbkit.KindExec, dbkit.KindQuery, dbkit.KindQueryRow}
	var want []dbkit.Statement
	for i, kind := range shareKinds {
		for _, text := range []string{shareSwapped, shareWrite} {
			if err := kind.run(t.Context(), share, text, "a", "b"); err != nil {
				t.Fatalf("%s(%q) error = %v, want nil", kind.name, text, err)
			}
			want = append(want, dbkit.Statement{ID: shareID, Kind: kinds[i], Query: text, Writes: text == shareWrite})
		}
	}

	if got := shareDrain(seen); !slices.Equal(got, want) {
		t.Errorf("observed = %+v, want %+v", got, want)
	}
	if !fake.saw("exec", shareSwappedOnSQLite) {
		t.Errorf("driver calls = %v, want the rewritten text sent", fake.log())
	}
}
