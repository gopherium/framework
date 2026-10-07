// SPDX-License-Identifier: Apache-2.0

package sqlitetest_test

import (
	"database/sql"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gopherium/framework/dbkit/sqlite/sqlitetest"
)

// mainFile returns the path of the main database file of db.
func mainFile(t *testing.T, db *sql.DB) string {
	t.Helper()
	var path string
	if err := db.QueryRowContext(t.Context(), "SELECT file FROM pragma_database_list WHERE name = 'main'").
		Scan(&path); err != nil {
		t.Fatalf("pragma_database_list error = %v, want nil", err)
	}
	return path
}

func TestOpenGivesEachCallAFreshFile(t *testing.T) {
	t.Parallel()

	first := sqlitetest.Open(t, testOptions())
	second := sqlitetest.Open(t, testOptions())
	mustExec(t, first, faultSchema)

	var tables int64
	err := second.QueryRowContext(t.Context(), "SELECT count(*) FROM sqlite_schema").Scan(&tables)

	if err != nil || tables != 0 {
		t.Errorf("tables in the second file = %d, %v, want none from the first", tables, err)
	}
	if mainFile(t, first) == mainFile(t, second) {
		t.Errorf("both handles open %s, want a file each", mainFile(t, first))
	}
}

func TestOpenClosesTheHandleWhenTheTestEnds(t *testing.T) {
	t.Parallel()

	var db *sql.DB
	t.Run("a test", func(t *testing.T) {
		db = sqlitetest.Open(t, testOptions())
		mustExec(t, db, faultSchema)
	})

	if err := db.PingContext(t.Context()); err == nil || err.Error() != "sql: database is closed" {
		t.Errorf("PingContext() after the test ended error = %v, want the handle closed", err)
	}
}

func TestOpenServesASubtestNameWithAHashOrAPercent(t *testing.T) {
	t.Parallel()

	for _, name := range []string{"repeated", "repeated", "fifty%"} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			plain := sqlitetest.Open(t, testOptions())
			wrapped := sqlitetest.OpenWithFaults(t, testOptions(), &sqlitetest.Faults{})

			mustExec(t, plain, faultSchema)
			mustExec(t, wrapped, faultSchema)
		})
	}
}

func TestOpenTakesItsOwnFolderAndCreate(t *testing.T) {
	t.Parallel()

	elsewhere := filepath.Join(t.TempDir(), "elsewhere")
	opts := testOptions()
	opts.BaseFolder = elsewhere
	opts.Create = false

	db := sqlitetest.Open(t, opts)

	mustExec(t, db, faultSchema)
	if path := mainFile(t, db); strings.HasPrefix(path, elsewhere) {
		t.Errorf("the database file is %s, want it outside the caller's base folder", path)
	}
}
