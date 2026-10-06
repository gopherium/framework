// SPDX-License-Identifier: Apache-2.0

package dbkit_test

import (
	"context"
	"maps"
	"slices"
	"strconv"
	"sync"
	"testing"

	"github.com/gopherium/framework/dbkit"
)

// shareBudget returns a query budget of limit statements and fails the test when it cannot.
func shareBudget(t *testing.T, limit int) *dbkit.QueryBudget {
	t.Helper()
	budget, err := dbkit.NewQueryBudget(limit)
	if err != nil {
		t.Fatalf("NewQueryBudget(%d) error = %v, want nil", limit, err)
	}
	return budget
}

// shareCounted fails the test unless the counter in ctx holds exactly want.
func shareCounted(t *testing.T, ctx context.Context, want dbkit.QueryCount) {
	t.Helper()
	got, ok := dbkit.QueriesIn(ctx)
	if !ok || got.Total != want.Total || got.PastBudget != want.PastBudget || !maps.Equal(got.ByID, want.ByID) {
		t.Errorf("QueriesIn() = %+v, %t, want %+v, true", got, ok, want)
	}
}

func TestNewQueryBudgetRefusesALimitBelowOne(t *testing.T) {
	t.Parallel()

	for _, limit := range []int{0, -1} {
		budget, err := dbkit.NewQueryBudget(limit)

		want := "dbkit: the query budget limit must be 1 or more, got " + strconv.Itoa(limit)
		if err == nil || err.Error() != want || budget != nil {
			t.Errorf("NewQueryBudget(%d) = %v, %v, want nil and %q", limit, budget, err, want)
		}
	}
}

func TestNewQueryBudgetAcceptsALimitOfOne(t *testing.T) {
	t.Parallel()

	if _, err := dbkit.NewQueryBudget(1); err != nil {
		t.Errorf("NewQueryBudget(1) error = %v, want nil", err)
	}
}

func TestTheCounterCountsPerRequestAndPerID(t *testing.T) {
	t.Parallel()

	db, _ := shareHandle(t)
	first := shareNew(t, db, dbkit.Postgres, shareOptions(2))
	otherOptions := shareOptions(2)
	otherOptions.ID = shareOtherID
	second := shareNew(t, db, dbkit.Postgres, otherOptions)
	budget := shareBudget(t, 10)
	one := budget.Attach(t.Context())
	two := budget.Attach(t.Context())

	steps := []error{
		shareExec(one, first, shareRead),
		shareExec(one, first, shareWrite, 1),
		shareQuery(one, second, shareRead),
		shareQueryRow(two, second, shareRead),
		shareExec(two, first, shareRead),
	}
	tx, err := second.Begin(two)
	if err != nil {
		t.Fatalf("Begin() error = %v, want nil", err)
	}
	steps = append(steps, shareExec(two, tx, shareWrite, 1), tx.Commit())
	for i, err := range steps {
		if err != nil {
			t.Fatalf("step %d error = %v, want nil", i, err)
		}
	}

	shareCounted(t, one, dbkit.QueryCount{Total: 3, ByID: map[string]int{shareID: 2, shareOtherID: 1}})
	shareCounted(t, two, dbkit.QueryCount{Total: 3, ByID: map[string]int{shareID: 1, shareOtherID: 2}})
}

func TestNoCounterOutsideARequest(t *testing.T) {
	t.Parallel()

	share, _, seen := shareWatched(t, dbkit.Postgres)
	for _, kind := range shareKinds {
		if err := kind.run(t.Context(), share, shareRead); err != nil {
			t.Fatalf("%s() error = %v, want nil", kind.name, err)
		}
	}

	for _, s := range shareDrain(seen) {
		if s.Count != 0 || s.PastBudget {
			t.Errorf("observed Count %d and PastBudget %t, want 0 and false with no counter", s.Count, s.PastBudget)
		}
	}
	if count, ok := dbkit.QueriesIn(t.Context()); ok || count.Total != 0 || count.ByID != nil || count.PastBudget {
		t.Errorf("QueriesIn() = %+v, %t, want the zero count and false", count, ok)
	}
}

func TestPastBudgetMarksEveryStatementAboveTheLimit(t *testing.T) {
	t.Parallel()

	share, _, seen := shareWatched(t, dbkit.Postgres)
	ctx := shareBudget(t, 2).Attach(t.Context())

	var marks []string
	for range 4 {
		if err := shareExec(ctx, share, shareRead); err != nil {
			t.Fatalf("Exec() error = %v, want nil", err)
		}
		for _, s := range shareDrain(seen) {
			marks = append(marks, strconv.Itoa(s.Count)+" "+strconv.FormatBool(s.PastBudget))
		}
	}

	if want := []string{"1 false", "2 false", "3 true", "4 true"}; !slices.Equal(marks, want) {
		t.Errorf("observed counts = %v, want %v", marks, want)
	}
	shareCounted(t, ctx, dbkit.QueryCount{Total: 4, ByID: map[string]int{shareID: 4}, PastBudget: true})
}

func TestTheCounterHoldsAtTheLimit(t *testing.T) {
	t.Parallel()

	share, _ := shareOpen(t, dbkit.Postgres, shareOptions(1))
	ctx := shareBudget(t, 2).Attach(t.Context())
	for range 2 {
		if err := shareExec(ctx, share, shareRead); err != nil {
			t.Fatalf("Exec() error = %v, want nil", err)
		}
	}

	shareCounted(t, ctx, dbkit.QueryCount{Total: 2, ByID: map[string]int{shareID: 2}})
}

func TestTheCounterIsSafeForStatementsAtOnce(t *testing.T) {
	t.Parallel()

	db, _ := shareHandle(t)
	otherOptions := shareOptions(4)
	otherOptions.ID = shareOtherID
	shares := []*dbkit.Share{
		shareNew(t, db, dbkit.Postgres, shareOptions(4)),
		shareNew(t, db, dbkit.Postgres, otherOptions),
	}
	ctx := shareBudget(t, 1000).Attach(t.Context())

	var wg sync.WaitGroup
	for i := range 16 {
		wg.Go(func() {
			for range 25 {
				if err := shareExec(ctx, shares[i%2], shareRead); err != nil {
					t.Errorf("Exec() error = %v, want nil", err)
				}
			}
		})
	}
	wg.Wait()

	shareCounted(t, ctx, dbkit.QueryCount{Total: 400, ByID: map[string]int{shareID: 200, shareOtherID: 200}})
}

func TestQueriesInReturnsACopy(t *testing.T) {
	t.Parallel()

	share, _ := shareOpen(t, dbkit.Postgres, shareOptions(1))
	ctx := shareBudget(t, 10).Attach(t.Context())
	if err := shareExec(ctx, share, shareRead); err != nil {
		t.Fatalf("Exec() error = %v, want nil", err)
	}

	changed, _ := dbkit.QueriesIn(ctx)
	changed.ByID[shareID] = 99
	changed.ByID[shareOtherID] = 1

	shareCounted(t, ctx, dbkit.QueryCount{Total: 1, ByID: map[string]int{shareID: 1}})
}
