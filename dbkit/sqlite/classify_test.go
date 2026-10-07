// SPDX-License-Identifier: Apache-2.0

package sqlite_test

import (
	"database/sql"
	"errors"
	"runtime/debug"
	"testing"

	modernc "modernc.org/sqlite"

	"github.com/gopherium/framework/dbkit"
	"github.com/gopherium/framework/dbkit/sqlite"
)

// classifySchema creates the tables whose constraints the classify tests break.
const classifySchema = `
CREATE TABLE classify_parents (id INTEGER PRIMARY KEY);
CREATE TABLE classify_rows (
	id INTEGER PRIMARY KEY,
	code TEXT NOT NULL UNIQUE,
	parent INTEGER REFERENCES classify_parents (id),
	amount INTEGER CHECK (amount >= 0)
);
CREATE TABLE classify_guarded (a INTEGER);
CREATE TRIGGER classify_guard BEFORE INSERT ON classify_guarded BEGIN SELECT RAISE(ABORT, 'guarded table'); END;
CREATE TABLE classify_scanned (a INTEGER);
CREATE TABLE classify_kept (id INTEGER PRIMARY KEY);
CREATE TABLE classify_keepers (kept INTEGER REFERENCES classify_kept (id) ON DELETE RESTRICT ON UPDATE RESTRICT);
CREATE TABLE classify_plain (a INTEGER);
INSERT INTO classify_parents VALUES (1);
INSERT INTO classify_rows VALUES (1, 'taken', 1, 0);
INSERT INTO classify_scanned VALUES (1), (2);
INSERT INTO classify_kept VALUES (1);
INSERT INTO classify_keepers VALUES (1);
INSERT INTO classify_plain (rowid, a) VALUES (1, 1);
`

// classifyClasses lists every dbkit error class.
var classifyClasses = []error{dbkit.ErrBusy, dbkit.ErrUnique, dbkit.ErrForeignKey, dbkit.ErrNotNull, dbkit.ErrCheck}

// classifyOpen returns a handle on a fresh file holding classifySchema, with a short busy timeout.
func classifyOpen(t *testing.T) *sql.DB {
	t.Helper()
	opts := testOptions()
	opts.BusyTimeout = rulesShortBusyTimeout
	db := rulesOpen(t, opts)
	rulesExec(t, db, classifySchema)
	return db
}

// classifyExec runs query on db and returns its error.
func classifyExec(query string) func(*testing.T, *sql.DB) error {
	return func(t *testing.T, db *sql.DB) error {
		_, err := db.ExecContext(t.Context(), query)
		return err
	}
}

// classifyBusy begins a write transaction while another writer holds the lock and returns its error.
func classifyBusy(t *testing.T, db *sql.DB) error {
	rulesHoldWriter(t, db, "INSERT INTO classify_parents VALUES (2)")
	tx, err := db.BeginTx(t.Context(), nil)
	if err == nil {
		_ = tx.Rollback()
	}
	return err
}

// classifyStaleSnapshot writes from a read-only transaction after another connection committed and returns its error.
func classifyStaleSnapshot(t *testing.T, db *sql.DB) error {
	reader, err := db.BeginTx(t.Context(), &sql.TxOptions{ReadOnly: true})
	if err != nil {
		t.Fatalf("BeginTx(ReadOnly) error = %v, want nil", err)
	}
	defer func() { _ = reader.Rollback() }()
	var n int64
	if err := reader.QueryRowContext(t.Context(), "SELECT count(*) FROM classify_parents").Scan(&n); err != nil {
		t.Fatalf("QueryRowContext() error = %v, want nil", err)
	}
	rulesExec(t, db, "INSERT INTO classify_parents VALUES (3)")
	_, err = reader.ExecContext(t.Context(), "INSERT INTO classify_parents VALUES (4)")
	return err
}

// classifyLockedTable drops a table that a statement of the same transaction still reads and returns its error.
func classifyLockedTable(t *testing.T, db *sql.DB) error {
	tx, err := db.BeginTx(t.Context(), nil)
	if err != nil {
		t.Fatalf("BeginTx() error = %v, want nil", err)
	}
	defer func() { _ = tx.Rollback() }()
	rows, err := tx.QueryContext(t.Context(), "SELECT a FROM classify_scanned")
	if err != nil {
		t.Fatalf("QueryContext() error = %v, want nil", err)
	}
	defer func() { _ = rows.Close() }()
	if !rows.Next() {
		t.Fatalf("Next() = false, want the first row, error %v", rows.Err())
	}
	_, err = tx.ExecContext(t.Context(), "DROP TABLE classify_scanned")
	return err
}

// classifyMustCode fails the test unless err wraps a driver error with code.
func classifyMustCode(t *testing.T, err error, code int) {
	t.Helper()
	var driverErr *modernc.Error
	if !errors.As(err, &driverErr) || driverErr.Code() != code {
		t.Fatalf("error %v, want a driver error with code %d", err, code)
	}
}

func TestClassify(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name  string
		run   func(*testing.T, *sql.DB) error
		code  int
		class error
	}{
		{"busy", classifyBusy, 5, dbkit.ErrBusy},
		{"a stale snapshot", classifyStaleSnapshot, 517, dbkit.ErrBusy},
		{"a locked table", classifyLockedTable, 6, dbkit.ErrBusy},
		{"unique", classifyExec("INSERT INTO classify_rows (id, code) VALUES (2, 'taken')"), 2067, dbkit.ErrUnique},
		{"primary key", classifyExec("INSERT INTO classify_rows (id, code) VALUES (1, 'free')"), 1555, dbkit.ErrUnique},
		{"a repeated rowid", classifyExec("INSERT INTO classify_plain (rowid, a) VALUES (1, 2)"), 2579, dbkit.ErrUnique},
		{"a restricted delete", classifyExec("DELETE FROM classify_kept WHERE id = 1"), 1811, dbkit.ErrForeignKey},
		{"a restricted update", classifyExec("UPDATE classify_kept SET id = 2 WHERE id = 1"), 1811,
			dbkit.ErrForeignKey},
		{"foreign key", classifyExec("INSERT INTO classify_rows VALUES (3, 'orphan', 99, 0)"), 787,
			dbkit.ErrForeignKey},
		{"not null", classifyExec("INSERT INTO classify_rows (id, code) VALUES (4, NULL)"), 1299, dbkit.ErrNotNull},
		{"check", classifyExec("INSERT INTO classify_rows VALUES (5, 'negative', 1, -1)"), 275, dbkit.ErrCheck},
		{"a trigger abort", classifyExec("INSERT INTO classify_guarded VALUES (1)"), 1811, nil},
		{"a syntax error", classifyExec("SELEC 1"), 1, nil},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			err := c.run(t, classifyOpen(t))
			classifyMustCode(t, err, c.code)

			got := sqlite.Classify(err)

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
				t.Errorf("Classify().Error() = %q, want the class and the driver message", got.Error())
			}
			if again := sqlite.Classify(got); again != got {
				t.Errorf("Classify(Classify()) = %v, want the classified error unchanged", again)
			}
		})
	}
}

func TestLibcVersionIsTheModulesOwnPin(t *testing.T) {
	t.Parallel()

	info, ok := debug.ReadBuildInfo()
	if !ok {
		t.Fatal("ReadBuildInfo() = false, want the build information of the test binary")
	}
	for _, dep := range info.Deps {
		if dep.Path != "modernc.org/libc" {
			continue
		}
		if dep.Version != sqlite.LibcVersion || dep.Replace != nil {
			t.Errorf("modernc.org/libc is %s replaced by %v, want %s as pinned", dep.Version, dep.Replace,
				sqlite.LibcVersion)
		}
		return
	}
	t.Errorf("the build holds no modernc.org/libc, want %s", sqlite.LibcVersion)
}

func TestClassifyLeavesOtherErrorsAlone(t *testing.T) {
	t.Parallel()

	plain := errors.New("an error from no driver")

	if got := sqlite.Classify(plain); got != plain {
		t.Errorf("Classify(%v) = %v, want the error unchanged", plain, got)
	}
	if got := sqlite.Classify(nil); got != nil {
		t.Errorf("Classify(nil) = %v, want nil", got)
	}
}
