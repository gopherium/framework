// SPDX-License-Identifier: Apache-2.0

package sqlite_test

import (
	"database/sql"
	"database/sql/driver"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/gopherium/framework/dbkit"
	"github.com/gopherium/framework/dbkit/sqlite"
)

// errFunctionRefused is the error the failing function answers with.
var errFunctionRefused = errors.New("functions test: refused on purpose")

var (
	// functionsInRange is a time inside the int64 microsecond range.
	functionsInRange = time.Date(2026, time.October, 6, 12, 30, 45, 123456000, time.UTC)
	// functionsPastRange is a time past the int64 microsecond range.
	functionsPastRange = time.Date(300000, time.January, 1, 0, 0, 0, 0, time.UTC)
)

// functionsPanic panics on every call.
func functionsPanic([]driver.Value) (driver.Value, error) {
	panic("functions test: a panic on purpose")
}

// functionsReturning returns a function that answers every call with value.
func functionsReturning(value driver.Value) func([]driver.Value) (driver.Value, error) {
	return func([]driver.Value) (driver.Value, error) {
		return value, nil
	}
}

// functionsDouble returns twice its one integer argument.
func functionsDouble(args []driver.Value) (driver.Value, error) {
	n, ok := args[0].(int64)
	if !ok {
		return nil, fmt.Errorf("functions test: double takes an integer, got %T", args[0])
	}
	return 2 * n, nil
}

// functionsCount returns how many arguments it got.
func functionsCount(args []driver.Value) (driver.Value, error) {
	return int64(len(args)), nil
}

// functionsFail answers every call with errFunctionRefused.
func functionsFail([]driver.Value) (driver.Value, error) {
	return nil, errFunctionRefused
}

// functionsList returns a checked list of fns and fails the test when it cannot.
func functionsList(t *testing.T, fns ...dbkit.Function) *dbkit.FunctionList {
	t.Helper()
	list, err := dbkit.NewFunctionList(fns...)
	if err != nil {
		t.Fatalf("NewFunctionList() error = %v, want nil", err)
	}
	return list
}

// functionsOpen opens a fresh file with list as its functions.
func functionsOpen(t *testing.T, list *dbkit.FunctionList) (*sql.DB, string) {
	t.Helper()
	address, path := testAddress(t)
	opts := testOptions()
	opts.Functions = list
	return mustOpen(t, address, opts), path
}

// functionsMustAnswer fails the test unless query with args answers want on db.
func functionsMustAnswer(t *testing.T, db *sql.DB, query string, want int64, args ...any) {
	t.Helper()
	var got int64
	if err := db.QueryRowContext(t.Context(), query, args...).Scan(&got); err != nil || got != want {
		t.Errorf("%s = %d, %v, want %d", query, got, err, want)
	}
}

// functionsMustFail fails the test unless query fails on db with an error holding text.
func functionsMustFail(t *testing.T, db *sql.DB, query, text string) {
	t.Helper()
	_, err := db.ExecContext(t.Context(), query)
	if err == nil || !strings.Contains(err.Error(), text) {
		t.Errorf("%s error = %v, want one holding %q", query, err, text)
	}
}

func TestFunctionsLiveOnTheirOwnDriver(t *testing.T) {
	t.Parallel()

	list := functionsList(t, dbkit.Function{Name: "functions_double", Args: 1, Deterministic: true,
		Call: functionsDouble})
	db, path := functionsOpen(t, list)
	mustPing(t, db)
	stray, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatalf("sql.Open() error = %v, want nil", err)
	}
	t.Cleanup(func() { _ = stray.Close() })

	functionsMustAnswer(t, db, "SELECT functions_double(21)", 42)

	functionsMustFail(t, stray, "SELECT functions_double(21)", "no such function: functions_double")
}

func TestFunctionsKeepTheirShape(t *testing.T) {
	t.Parallel()

	list := functionsList(t,
		dbkit.Function{Name: "functions_double", Args: 1, Deterministic: true, Call: functionsDouble},
		dbkit.Function{Name: "functions_count", Args: -1, Call: functionsCount},
		dbkit.Function{Name: "functions_fail", Args: 0, Call: functionsFail},
	)
	db, _ := functionsOpen(t, list)
	rulesExec(t, db, "CREATE TABLE functions_rows (a INTEGER)")

	functionsMustAnswer(t, db, "SELECT functions_count(1, 'two', NULL)", 3)
	functionsMustAnswer(t, db, "SELECT functions_count()", 0)
	functionsMustFail(t, db, "SELECT functions_double(1, 2)", "wrong number of arguments to function functions_double()")
	functionsMustFail(t, db, "SELECT functions_fail()", errFunctionRefused.Error())
	rulesExec(t, db, "CREATE INDEX functions_doubled ON functions_rows (functions_double(a))")
	functionsMustFail(t, db, "CREATE INDEX functions_counted ON functions_rows (functions_count(a))",
		"non-deterministic functions prohibited in index expressions")
}

func TestOneDriverPerFunctionList(t *testing.T) {
	t.Parallel()

	shape := dbkit.Function{Name: "functions_double", Args: 1, Deterministic: true, Call: functionsDouble}
	first, second := functionsList(t, shape), functionsList(t, shape)
	firstA, _ := functionsOpen(t, first)
	firstB, _ := functionsOpen(t, first)
	secondA, _ := functionsOpen(t, second)
	noneA, _ := functionsOpen(t, nil)
	noneB, _ := functionsOpen(t, nil)
	shared, err := sql.Open("sqlite", "never-opened.db")
	if err != nil {
		t.Fatalf("sql.Open() error = %v, want nil", err)
	}
	t.Cleanup(func() { _ = shared.Close() })

	if firstA.Driver() != firstB.Driver() {
		t.Error("two handles of one list hold two driver values, want one")
	}
	if noneA.Driver() != noneB.Driver() {
		t.Error("two handles with no list hold two driver values, want one")
	}
	distinct := []driver.Driver{firstA.Driver(), secondA.Driver(), noneA.Driver(), shared.Driver()}
	for i := range distinct {
		for j := i + 1; j < len(distinct); j++ {
			if distinct[i] == distinct[j] {
				t.Errorf("driver values %d and %d are one value, want a driver per list and never the shared one", i, j)
			}
		}
	}
}

func TestOpenRefusesAFunctionSQLiteCannotRegister(t *testing.T) {
	t.Parallel()

	longest := strings.Repeat("n", 255)
	cases := []struct {
		name string
		fn   dbkit.Function
		want string
	}{
		{"a name past 255 bytes", dbkit.Function{Name: longest + "n", Args: 1, Call: functionsCount},
			"dbkit: function 1 has a name of 256 bytes, SQLite takes at most 255"},
		{"more than 1000 arguments", dbkit.Function{Name: "functions_wide", Args: 1001, Call: functionsCount},
			`dbkit: function "functions_wide" takes 1001 arguments, SQLite takes at most 1000`},
		{"a name holding a NUL byte", dbkit.Function{Name: "functions_nul\x00name", Args: 1, Call: functionsCount},
			"dbkit: function 1 has a name holding a NUL byte"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			address, path := testAddress(t)
			opts := testOptions()
			opts.Functions = functionsList(t, dbkit.Function{Name: "functions_count", Args: -1, Call: functionsCount}, c.fn)

			db, err := sqlite.Open(address, opts)

			if err == nil || err.Error() != c.want || db != nil {
				t.Errorf("Open() = %v, %v, want nil and %q", db, err, c.want)
			}
			mustNotExist(t, path)
		})
	}
}

// functionsMustRead fails the test unless query with args answers want on db.
func functionsMustRead(t *testing.T, db *sql.DB, want, query string, args ...any) {
	t.Helper()
	var got string
	if err := db.QueryRowContext(t.Context(), query, args...).Scan(&got); err != nil || got != want {
		t.Errorf("%s = %q, %v, want %q", query, got, err, want)
	}
}

func TestAnArgumentHoldingANULReachesTheFunctionWhole(t *testing.T) {
	t.Parallel()

	db, _ := functionsOpen(t, functionsList(t, dbkit.CaseFold()))

	functionsMustRead(t, db, "6100626364", "SELECT hex(casefold(CAST(X'4100424344' AS TEXT)))")
	functionsMustRead(t, db, "a\x00bcd", "SELECT casefold(?)", "A\x00BCD")
	functionsMustRead(t, db, "6100626364", "SELECT hex(casefold(X'4100424344'))")
}

func TestAPanicInAFunctionFailsItsStatementAndFreesTheFile(t *testing.T) {
	t.Parallel()

	address, _ := testAddress(t)
	opts := testOptions()
	opts.BusyTimeout = rulesShortBusyTimeout
	opts.Functions = functionsList(t, dbkit.Function{Name: "functions_panic", Args: 0, Call: functionsPanic})
	db := mustOpen(t, address, opts)
	rulesExec(t, db, "CREATE TABLE functions_panics (a INTEGER)")
	tx, err := db.BeginTx(t.Context(), nil)
	if err != nil {
		t.Fatalf("BeginTx() error = %v, want nil", err)
	}
	if _, err := tx.ExecContext(t.Context(), "INSERT INTO functions_panics VALUES (1)"); err != nil {
		t.Fatalf("ExecContext() error = %v, want nil", err)
	}

	var got int64
	err = tx.QueryRowContext(t.Context(), "SELECT functions_panic()").Scan(&got)

	want := `dbkit: function "functions_panic" panicked: functions test: a panic on purpose`
	if err == nil || !strings.Contains(err.Error(), want) {
		t.Errorf("SELECT functions_panic() error = %v, want one holding %q", err, want)
	}
	if err := tx.Rollback(); err != nil {
		t.Errorf("Rollback() error = %v, want nil", err)
	}
	if inUse := db.Stats().InUse; inUse != 0 {
		t.Errorf("Stats().InUse = %d after the rollback, want 0", inUse)
	}
	opts.Functions = nil
	rulesExec(t, mustOpen(t, address, opts), "INSERT INTO functions_panics VALUES (2)")
}

func TestATimeAFunctionReturnsIsStoredAsMicroseconds(t *testing.T) {
	t.Parallel()

	list := functionsList(t,
		dbkit.Function{Name: "functions_in_range", Args: 0, Call: functionsReturning(functionsInRange)},
		dbkit.Function{Name: "functions_past_range", Args: 0, Call: functionsReturning(functionsPastRange)},
	)
	db, _ := functionsOpen(t, list)

	functionsMustAnswer(t, db, "SELECT functions_in_range() = ?", 1, functionsInRange)
	var got dbkit.Time
	if err := db.QueryRowContext(t.Context(), "SELECT functions_in_range()").Scan(&got); err != nil ||
		!got.Equal(functionsInRange) {
		t.Errorf("SELECT functions_in_range() = %v, %v, want %v", got.Time, err, functionsInRange)
	}
	functionsMustFail(t, db, "SELECT functions_past_range()", "falls outside the int64 microsecond range")
}

func TestTheWidestFunctionSQLiteTakesRegisters(t *testing.T) {
	t.Parallel()

	list := functionsList(t,
		dbkit.Function{Name: strings.Repeat("n", 255), Args: 1, Call: functionsCount},
		dbkit.Function{Name: "functions_wide", Args: 1000, Call: functionsCount},
	)
	db, _ := functionsOpen(t, list)

	mustPing(t, db)
}
