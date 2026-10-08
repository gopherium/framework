// SPDX-License-Identifier: Apache-2.0

package postgres_test

import (
	"context"
	"database/sql"
	"errors"
	"testing"

	"github.com/jackc/pgx/v5/pgconn"

	"github.com/gopherium/framework/dbkit"
	"github.com/gopherium/framework/dbkit/postgres"
)

// classifySchema creates the tables whose constraints and row locks the classify tests break.
const classifySchema = `
CREATE TABLE classify_parents (id integer PRIMARY KEY);
CREATE TABLE classify_rows (
	id integer PRIMARY KEY,
	code text NOT NULL UNIQUE,
	parent integer REFERENCES classify_parents (id),
	amount integer CHECK (amount >= 0)
);
CREATE TABLE classify_locks (id integer PRIMARY KEY, n integer NOT NULL);
INSERT INTO classify_parents VALUES (1);
INSERT INTO classify_rows VALUES (1, 'taken', 1, 0);
INSERT INTO classify_locks VALUES (1, 0), (2, 0);
`

// classifyClasses lists every dbkit error class.
var classifyClasses = []error{dbkit.ErrBusy, dbkit.ErrUnique, dbkit.ErrForeignKey, dbkit.ErrNotNull, dbkit.ErrCheck}

// classifyOpen returns the view of a handle on a fresh database holding classifySchema.
func classifyOpen(t *testing.T) *sql.DB {
	t.Helper()
	db := openFreshHandle(t, openMaxConns).DB
	classifyMustExec(t, db, classifySchema)
	return db
}

// classifyExecer runs a statement, as a handle and a transaction both do.
type classifyExecer interface {
	// ExecContext runs query and returns its result.
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
}

// classifyMustExec runs query on db and fails the test when it cannot.
func classifyMustExec(t *testing.T, db classifyExecer, query string) {
	t.Helper()
	if _, err := db.ExecContext(t.Context(), query); err != nil {
		t.Fatalf("Exec(%q) error = %v, want nil", query, err)
	}
}

// classifyBegin begins a transaction on db with opts and rolls it back when the test ends.
func classifyBegin(t *testing.T, db *sql.DB, opts *sql.TxOptions) *sql.Tx {
	t.Helper()
	tx, err := db.BeginTx(t.Context(), opts)
	if err != nil {
		t.Fatalf("BeginTx() error = %v, want nil", err)
	}
	t.Cleanup(func() { _ = tx.Rollback() })
	return tx
}

// classifyExec runs query on db and returns its error.
func classifyExec(query string) func(*testing.T, *sql.DB) error {
	return func(t *testing.T, db *sql.DB) error {
		_, err := db.ExecContext(t.Context(), query)
		return err
	}
}

// classifyHoldRow begins a transaction on db that holds the row lock of the first classify_locks row.
func classifyHoldRow(t *testing.T, db *sql.DB) {
	t.Helper()
	classifyMustExec(t, classifyBegin(t, db, nil), "SELECT n FROM classify_locks WHERE id = 1 FOR UPDATE")
}

// classifyNoWait asks at once for a row lock another transaction holds and returns its error.
func classifyNoWait(t *testing.T, db *sql.DB) error {
	classifyHoldRow(t, db)
	_, err := db.ExecContext(t.Context(), "SELECT n FROM classify_locks WHERE id = 1 FOR UPDATE NOWAIT")
	return err
}

// classifyLockTimeout waits past lock_timeout for a row lock another transaction holds and returns its error.
func classifyLockTimeout(t *testing.T, db *sql.DB) error {
	classifyHoldRow(t, db)
	tx := classifyBegin(t, db, nil)
	classifyMustExec(t, tx, "SET LOCAL lock_timeout = '1ms'")
	_, err := tx.ExecContext(t.Context(), "UPDATE classify_locks SET n = 1 WHERE id = 1")
	return err
}

// classifyStatementTimeout runs a statement past statement_timeout and returns its error.
func classifyStatementTimeout(t *testing.T, db *sql.DB) error {
	tx := classifyBegin(t, db, nil)
	classifyMustExec(t, tx, "SET LOCAL statement_timeout = '1ms'")
	_, err := tx.ExecContext(t.Context(), "SELECT pg_sleep(60)")
	return err
}

// classifyDeadlock runs two transactions that each wait for the row the other holds and returns the error of one.
func classifyDeadlock(t *testing.T, db *sql.DB) error {
	first, second := classifyBegin(t, db, nil), classifyBegin(t, db, nil)
	classifyMustExec(t, first, "UPDATE classify_locks SET n = 1 WHERE id = 1")
	classifyMustExec(t, second, "UPDATE classify_locks SET n = 1 WHERE id = 2")
	answers := make(chan error, 2)
	for tx, query := range map[*sql.Tx]string{
		first:  "UPDATE classify_locks SET n = 2 WHERE id = 2",
		second: "UPDATE classify_locks SET n = 2 WHERE id = 1",
	} {
		go func() {
			_, err := tx.ExecContext(t.Context(), query)
			answers <- err
		}()
	}
	one, other := <-answers, <-answers
	if (one == nil) == (other == nil) {
		t.Fatalf("the two transactions answered %v and %v, want exactly one stopped by the deadlock", one, other)
	}
	return errors.Join(one, other)
}

// classifySerialization updates a row a repeatable read transaction saw before another commit changed it.
func classifySerialization(t *testing.T, db *sql.DB) error {
	reader := classifyBegin(t, db, &sql.TxOptions{Isolation: sql.LevelRepeatableRead})
	var n int
	if err := reader.QueryRowContext(t.Context(), "SELECT n FROM classify_locks WHERE id = 1").Scan(&n); err != nil {
		t.Fatalf("QueryRowContext() error = %v, want nil", err)
	}
	classifyMustExec(t, db, "UPDATE classify_locks SET n = 5 WHERE id = 1")
	_, err := reader.ExecContext(t.Context(), "UPDATE classify_locks SET n = 6 WHERE id = 1")
	return err
}

// classifyMustCode fails the test unless err wraps a server error with code.
func classifyMustCode(t *testing.T, err error, code string) {
	t.Helper()
	var serverErr *pgconn.PgError
	if !errors.As(err, &serverErr) || serverErr.Code != code {
		t.Fatalf("error %v, want a server error with code %s", err, code)
	}
}

func TestClassify(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name  string
		run   func(*testing.T, *sql.DB) error
		code  string
		class error
	}{
		{"unique", classifyExec("INSERT INTO classify_rows (id, code) VALUES (2, 'taken')"), "23505", dbkit.ErrUnique},
		{"primary key", classifyExec("INSERT INTO classify_rows (id, code) VALUES (1, 'free')"), "23505",
			dbkit.ErrUnique},
		{"foreign key", classifyExec("INSERT INTO classify_rows VALUES (3, 'orphan', 99, 0)"), "23503",
			dbkit.ErrForeignKey},
		{"a delete a foreign key stops", classifyExec("DELETE FROM classify_parents WHERE id = 1"), "23503",
			dbkit.ErrForeignKey},
		{"not null", classifyExec("INSERT INTO classify_rows (id, code) VALUES (4, NULL)"), "23502", dbkit.ErrNotNull},
		{"check", classifyExec("INSERT INTO classify_rows VALUES (5, 'negative', 1, -1)"), "23514", dbkit.ErrCheck},
		{"a row lock asked for with NOWAIT", classifyNoWait, "55P03", dbkit.ErrBusy},
		{"a row lock past lock_timeout", classifyLockTimeout, "55P03", dbkit.ErrBusy},
		{"a deadlock", classifyDeadlock, "40P01", dbkit.ErrBusy},
		{"a serialization failure", classifySerialization, "40001", dbkit.ErrBusy},
		{"a statement past statement_timeout", classifyStatementTimeout, "57014", nil},
		{"a syntax error", classifyExec("SELEC 1"), "42601", nil},
		{"a raised exception", classifyExec("DO $$ BEGIN RAISE EXCEPTION 'guarded'; END $$"), "P0001", nil},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			err := c.run(t, classifyOpen(t))
			classifyMustCode(t, err, c.code)

			got := postgres.Classify(err)

			classifyMustCode(t, got, c.code)
			for _, class := range classifyClasses {
				if want := class == c.class; errors.Is(got, class) != want {
					t.Errorf("errors.Is(Classify(%v), %v) = %v, want %v", err, class, !want, want)
				}
			}
			if c.class == nil && got != err {
				t.Errorf("Classify(%v) = %v, want the error unchanged", err, got)
			}
			if c.class != nil && got.Error() != c.class.Error()+": "+err.Error() {
				t.Errorf("Classify().Error() = %q, want the class and the server message", got.Error())
			}
			if again := postgres.Classify(got); again != got {
				t.Errorf("Classify(Classify()) = %v, want the classified error unchanged", again)
			}
		})
	}
}

func TestClassifyLeavesOtherErrorsAlone(t *testing.T) {
	t.Parallel()

	plain := errors.New("an error from no server")

	if got := postgres.Classify(plain); got != plain {
		t.Errorf("Classify(%v) = %v, want the error unchanged", plain, got)
	}
	if got := postgres.Classify(nil); got != nil {
		t.Errorf("Classify(nil) = %v, want nil", got)
	}
}
