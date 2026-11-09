// SPDX-License-Identifier: Apache-2.0

package sqlite_test

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"regexp"
	"testing"

	"github.com/gopherium/framework/dbkit/sqlite"
	"github.com/gopherium/framework/dbkit/sqlite/sqlitetest"
)

const (
	// fpRowsTable creates a rowid table of synthetic rows.
	fpRowsTable = "CREATE TABLE fp_rows (id INTEGER PRIMARY KEY, label TEXT, amount REAL)"
	// fpRowsSeed fills fp_rows with two rows.
	fpRowsSeed = "INSERT INTO fp_rows VALUES (1, 'first', 1.5), (2, 'second', 2.5)"
	// fpSettingsTable creates a table of named settings.
	fpSettingsTable = "CREATE TABLE fp_settings (name TEXT PRIMARY KEY, value TEXT)"
	// fpSettingsSeed fills fp_settings with two kept settings, one rewritten setting and one with no name.
	fpSettingsSeed = "INSERT INTO fp_settings (rowid, name, value) VALUES " +
		"(1, 'site_name', 'synthetic'), (2, 'theme', 'plain'), (3, 'last_check', 'morning'), (4, NULL, 'unnamed')"
	// fpJobsTable creates a table a job runner rewrites by itself.
	fpJobsTable = "CREATE TABLE fp_jobs (id INTEGER PRIMARY KEY, lease TEXT)"
	// fpSchemaRead is the statement Fingerprint reads the schema with.
	fpSchemaRead = "SELECT type, name, tbl_name, ifnull(sql, '') FROM main.sqlite_schema " +
		"WHERE (name NOT LIKE 'sqlite\\_%' ESCAPE '\\' OR name = 'sqlite_sequence') ORDER BY name, type"
	// fpTableRead is the statement Fingerprint reads the table list with.
	fpTableRead = "SELECT name, wr, type FROM pragma_table_list WHERE schema = 'main' " +
		"AND type IN ('table', 'shadow', 'virtual') " +
		"AND (name NOT LIKE 'sqlite\\_%' ESCAPE '\\' OR name = 'sqlite_sequence') ORDER BY name"
	// fpColumnRead is the statement Fingerprint reads the columns of one table with.
	fpColumnRead = "SELECT name, pk, hidden FROM pragma_table_xinfo(?, 'main') ORDER BY cid"
	// fpOneTable creates a table of one column with no declared type.
	fpOneTable = "CREATE TABLE fp_one (a)"
	// fpOneRead is the statement Fingerprint reads the rows of fp_one with.
	fpOneRead = `SELECT typeof(rowid), rowid, typeof("a"), CASE typeof("a") WHEN 'text' THEN CAST("a" AS BLOB) ` +
		`ELSE "a" END FROM main."fp_one" ORDER BY rowid`
	// fpSearchTable creates a full text search table of one column.
	fpSearchTable = "CREATE VIRTUAL TABLE fp_search USING fts5(body)"
	// fpGeneratedTable creates a table with a stored and a virtual generated column.
	fpGeneratedTable = "CREATE TABLE fp_gen (a INTEGER, flag AS (a % 2) STORED, half AS (a / 2) VIRTUAL)"
)

// fpHex is the form of a fingerprint.
var fpHex = regexp.MustCompile(`^[0-9a-f]{64}$`)

// fpOpen returns a handle on a fresh file after running statements on it.
func fpOpen(t *testing.T, statements ...string) *sql.DB {
	t.Helper()
	db := rulesOpen(t, testOptions())
	for _, statement := range statements {
		rulesExec(t, db, statement)
	}
	return db
}

// fpOf returns the fingerprint of db without leave and fails the test when there is none.
func fpOf(t *testing.T, db *sql.DB, leave sqlite.Leave) string {
	t.Helper()
	fp, err := sqlite.Fingerprint(t.Context(), db, leave)
	if err != nil {
		t.Fatalf("Fingerprint() error = %v, want nil", err)
	}
	if !fpHex.MatchString(fp) {
		t.Fatalf("Fingerprint() = %q, want 64 lowercase hex digits", fp)
	}
	return fp
}

// fpMustMove fails the test unless running change on db moves its fingerprint without leave.
func fpMustMove(t *testing.T, db *sql.DB, leave sqlite.Leave, change string) {
	t.Helper()
	before := fpOf(t, db, leave)
	rulesExec(t, db, change)
	if after := fpOf(t, db, leave); after == before {
		t.Errorf("Fingerprint() after %q = %s, want it moved from %s", change, after, before)
	}
}

// fpMustStay fails the test unless running change on db keeps its fingerprint without leave.
func fpMustStay(t *testing.T, db *sql.DB, leave sqlite.Leave, change string) {
	t.Helper()
	before := fpOf(t, db, leave)
	rulesExec(t, db, change)
	if after := fpOf(t, db, leave); after != before {
		t.Errorf("Fingerprint() after %q = %s, want it kept at %s", change, after, before)
	}
}

// fpMustFail fails the test unless Fingerprint of db without leave answers no fingerprint and the error want.
func fpMustFail(t *testing.T, db *sql.DB, leave sqlite.Leave, want string) {
	t.Helper()
	fp, err := sqlite.Fingerprint(t.Context(), db, leave)
	if fmt.Sprint(err) != want || fp != "" {
		t.Errorf("Fingerprint() = %q, %v, want no fingerprint and %q", fp, err, want)
	}
}

func TestTheSameDataGivesTheSameFingerprint(t *testing.T) {
	t.Parallel()

	first := fpOpen(t, fpRowsTable, fpRowsSeed)
	second := fpOpen(t, fpRowsTable, fpRowsSeed)

	if a, b := fpOf(t, first, sqlite.Leave{}), fpOf(t, second, sqlite.Leave{}); a != b {
		t.Errorf("Fingerprint() of two files with the same data = %s and %s, want them equal", a, b)
	}
}

func TestAChangedRowMovesTheFingerprint(t *testing.T) {
	t.Parallel()

	fpMustMove(t, fpOpen(t, fpRowsTable, fpRowsSeed), sqlite.Leave{}, "UPDATE fp_rows SET label = 'changed' WHERE id = 2")
}

func TestAWithoutRowidTableHashesInKeyOrder(t *testing.T) {
	t.Parallel()

	const keyed = "CREATE TABLE fp_keyed (code TEXT PRIMARY KEY, value INTEGER) WITHOUT ROWID"
	backwards := fpOpen(t, keyed, "INSERT INTO fp_keyed VALUES ('b', 2)", "INSERT INTO fp_keyed VALUES ('a', 1)")
	forwards := fpOpen(t, keyed, "INSERT INTO fp_keyed VALUES ('a', 1)", "INSERT INTO fp_keyed VALUES ('b', 2)")

	if a, b := fpOf(t, backwards, sqlite.Leave{}), fpOf(t, forwards, sqlite.Leave{}); a != b {
		t.Errorf("Fingerprint() of the same rows written in two orders = %s and %s, want them equal", a, b)
	}
	fpMustMove(t, forwards, sqlite.Leave{}, "UPDATE fp_keyed SET value = 3 WHERE code = 'a'")
}

func TestAnInsertIntoAVirtualTableMovesTheFingerprint(t *testing.T) {
	t.Parallel()

	db := fpOpen(t, fpSearchTable)

	fpMustMove(t, db, sqlite.Leave{}, "INSERT INTO fp_search VALUES ('synthetic words')")
}

func TestAFingerprintNeverReadsAVirtualTable(t *testing.T) {
	t.Parallel()

	fpOf(t, fpOpen(t, "CREATE VIRTUAL TABLE fp_outside USING fts5(body, content='fp_missing')"), sqlite.Leave{})
}

func TestALeftOutVirtualTableKeepsTheRowsOfItsShadowTablesOut(t *testing.T) {
	t.Parallel()

	db := fpOpen(t, fpSearchTable, "CREATE TABLE fp_search_notes (body TEXT)")
	leave := sqlite.Leave{Tables: []string{"FP_SEARCH"}}

	fpMustStay(t, db, leave, "INSERT INTO fp_search VALUES ('synthetic words')")
	fpMustMove(t, db, leave, "INSERT INTO fp_search_notes VALUES ('synthetic note')")
}

func TestLeftOutRowsOfAVirtualTableFail(t *testing.T) {
	t.Parallel()

	db := fpOpen(t, fpSearchTable)

	fpMustFail(t, db, sqlite.Leave{Rows: []sqlite.LeaveRows{{Table: "fp_search", Column: "body"}}},
		`dbkit: the fingerprint cannot leave out rows of the virtual table "fp_search", leave out the whole table instead`)
}

func TestLeftOutRowsMatchAGeneratedColumn(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		// column is the generated column whose value 1 leaves a row out.
		column string
		// stay changes a left-out row and keeps it out.
		stay string
		// move changes a kept row.
		move string
	}{
		{column: "flag", stay: "UPDATE fp_gen SET a = 3 WHERE a = 1", move: "UPDATE fp_gen SET a = 4 WHERE a = 2"},
		{column: "half", stay: "UPDATE fp_gen SET a = 3 WHERE a = 2", move: "UPDATE fp_gen SET a = 0 WHERE a = 1"},
	} {
		t.Run(tc.column, func(t *testing.T) {
			t.Parallel()
			db := fpOpen(t, fpGeneratedTable, "INSERT INTO fp_gen (a) VALUES (1), (2)")
			leave := sqlite.Leave{Rows: []sqlite.LeaveRows{{Table: "fp_gen", Column: tc.column, Values: []any{1}}}}

			fpMustStay(t, db, leave, tc.stay)
			fpMustMove(t, db, leave, tc.move)
		})
	}
}

func TestAnIDTakenAndFreedMovesTheFingerprint(t *testing.T) {
	t.Parallel()

	db := fpOpen(t, "CREATE TABLE fp_counted (id INTEGER PRIMARY KEY AUTOINCREMENT, label TEXT)")
	before := fpOf(t, db, sqlite.Leave{})
	rulesExec(t, db, "INSERT INTO fp_counted (label) VALUES ('taken')")
	rulesExec(t, db, "DELETE FROM fp_counted")

	if after := fpOf(t, db, sqlite.Leave{}); after == before {
		t.Errorf("Fingerprint() after an id was taken and freed = %s, want it moved from %s", after, before)
	}
}

func TestStatisticsDoNotMoveTheFingerprint(t *testing.T) {
	t.Parallel()

	db := fpOpen(t, fpRowsTable, fpRowsSeed, "CREATE INDEX fp_rows_label ON fp_rows (label)")

	fpMustStay(t, db, sqlite.Leave{}, "ANALYZE")
	fpMustStay(t, db, sqlite.Leave{}, "PRAGMA optimize")
}

func TestANewIndexMovesTheFingerprint(t *testing.T) {
	t.Parallel()

	fpMustMove(t, fpOpen(t, fpRowsTable, fpRowsSeed), sqlite.Leave{}, "CREATE INDEX fp_rows_label ON fp_rows (label)")
}

func TestAViewRecreatedWithAnotherBodyMovesTheFingerprint(t *testing.T) {
	t.Parallel()

	db := fpOpen(t, fpRowsTable, fpRowsSeed, "CREATE VIEW fp_view AS SELECT label FROM fp_rows")
	before := fpOf(t, db, sqlite.Leave{})
	rulesExec(t, db, "DROP VIEW fp_view")
	rulesExec(t, db, "CREATE VIEW fp_view AS SELECT amount FROM fp_rows")

	if after := fpOf(t, db, sqlite.Leave{}); after == before {
		t.Errorf("Fingerprint() after the view got another body = %s, want it moved from %s", after, before)
	}
}

func TestALeftOutTableKeepsItsRowsOut(t *testing.T) {
	t.Parallel()

	db := fpOpen(t, fpRowsTable, fpRowsSeed, fpJobsTable)
	leave := sqlite.Leave{Tables: []string{"fp_jobs"}}

	fpMustStay(t, db, leave, "INSERT INTO fp_jobs (lease) VALUES ('held')")
	fpMustMove(t, db, leave, "UPDATE fp_rows SET amount = 9.5 WHERE id = 1")
}

func TestLeftOutRowsStayOut(t *testing.T) {
	t.Parallel()

	db := fpOpen(t, fpSettingsTable, fpSettingsSeed, fpRowsTable, fpRowsSeed)
	leave := sqlite.Leave{Rows: []sqlite.LeaveRows{
		{Table: "fp_settings", Column: "name", Values: []any{"last_check", "last_serials"}},
	}}

	fpMustStay(t, db, leave, "UPDATE fp_settings SET value = 'evening' WHERE name = 'last_check'")
	fpMustStay(t, db, leave, "INSERT INTO fp_settings VALUES ('last_serials', 'fresh')")
	fpMustMove(t, db, leave, "UPDATE fp_settings SET value = 'dark' WHERE name = 'theme'")
	fpMustMove(t, db, leave, "UPDATE fp_settings SET value = 'renamed' WHERE name IS NULL")
	fpMustMove(t, db, leave, "UPDATE fp_rows SET label = 'changed' WHERE id = 1")
}

func TestANullAmongTheLeftOutValuesMatchesNoRow(t *testing.T) {
	t.Parallel()

	db := fpOpen(t, fpSettingsTable, fpSettingsSeed)
	leave := sqlite.Leave{Rows: []sqlite.LeaveRows{
		{Table: "fp_settings", Column: "name", Values: []any{"last_check", nil}},
	}}

	fpMustStay(t, db, leave, "UPDATE fp_settings SET value = 'evening' WHERE name = 'last_check'")
	fpMustMove(t, db, leave, "UPDATE fp_settings SET value = 'dark' WHERE name = 'theme'")
	fpMustMove(t, db, leave, "UPDATE fp_settings SET value = 'renamed' WHERE name IS NULL")
}

func TestLeaveNamesMatchTheirTablesAndColumnsInAnyCase(t *testing.T) {
	t.Parallel()

	db := fpOpen(t, fpSettingsTable, fpSettingsSeed, fpJobsTable)
	leave := sqlite.Leave{
		Tables: []string{"FP_JOBS"},
		Rows:   []sqlite.LeaveRows{{Table: "Fp_Settings", Column: "NAME", Values: []any{"last_check"}}},
	}

	fpMustStay(t, db, leave, "INSERT INTO fp_jobs (lease) VALUES ('held')")
	fpMustStay(t, db, leave, "UPDATE fp_settings SET value = 'evening' WHERE name = 'last_check'")
	fpMustMove(t, db, leave, "UPDATE fp_settings SET value = 'dark' WHERE name = 'theme'")
}

func TestTwoLeftOutRowsOnOneTableBothStayOut(t *testing.T) {
	t.Parallel()

	db := fpOpen(t, fpSettingsTable, fpSettingsSeed)
	leave := sqlite.Leave{Rows: []sqlite.LeaveRows{
		{Table: "fp_settings", Column: "name", Values: []any{"last_check"}},
		{Table: "fp_settings", Column: "name", Values: []any{"last_serials"}},
	}}

	fpMustStay(t, db, leave, "UPDATE fp_settings SET value = 'evening' WHERE name = 'last_check'")
	fpMustStay(t, db, leave, "INSERT INTO fp_settings VALUES ('last_serials', 'fresh')")
	fpMustMove(t, db, leave, "UPDATE fp_settings SET value = 'dark' WHERE name = 'theme'")
}

func TestLeftOutRowsWithNoValuesLeaveNothingOut(t *testing.T) {
	t.Parallel()

	db := fpOpen(t, fpSettingsTable, fpSettingsSeed)
	none := sqlite.Leave{Rows: []sqlite.LeaveRows{{Table: "fp_settings", Column: "name"}}}

	if a, b := fpOf(t, db, none), fpOf(t, db, sqlite.Leave{}); a != b {
		t.Errorf("Fingerprint() leaving out no values = %s, want %s as with no leave", a, b)
	}
}

func TestALeaveNameThatMatchesNothingFails(t *testing.T) {
	t.Parallel()

	db := fpOpen(t, fpSettingsTable, fpSettingsSeed)

	fpMustFail(t, db, sqlite.Leave{Tables: []string{"fp_missing"}},
		`dbkit: the fingerprint leaves out the table "fp_missing", which the database does not hold`)
	fpMustFail(t, db, sqlite.Leave{Rows: []sqlite.LeaveRows{{Table: "fp_missing", Column: "name"}}},
		`dbkit: the fingerprint leaves out the table "fp_missing", which the database does not hold`)
	fpMustFail(t, db, sqlite.Leave{Rows: []sqlite.LeaveRows{{Table: "fp_settings", Column: "missing"}}},
		`dbkit: the fingerprint leaves out rows by the column "missing", which the table "fp_settings" does not hold`)
	fpMustFail(t, db, sqlite.Leave{
		Tables: []string{"fp_settings"},
		Rows:   []sqlite.LeaveRows{{Table: "fp_settings", Column: "missing"}},
	}, `dbkit: the fingerprint leaves out rows by the column "missing", which the table "fp_settings" does not hold`)
}

func TestALeaveNameMatchesOnlyASCIILettersInAnyCase(t *testing.T) {
	t.Parallel()

	db := fpOpen(t, `CREATE TABLE "fp_σ" (a)`)

	fpMustFail(t, db, sqlite.Leave{Tables: []string{"fp_Σ"}},
		`dbkit: the fingerprint leaves out the table "fp_Σ", which the database does not hold`)
}

func TestAFingerprintOfANilHandleFails(t *testing.T) {
	t.Parallel()

	fpMustFail(t, nil, sqlite.Leave{}, "dbkit: Fingerprint needs a database handle, got nil")
}

func TestATableNameThatNeedsQuotesIsHashed(t *testing.T) {
	t.Parallel()

	db := fpOpen(t, `CREATE TABLE "fp ""odd"" name" ("a ""b""" TEXT)`)

	fpMustMove(t, db, sqlite.Leave{}, `INSERT INTO "fp ""odd"" name" VALUES ('synthetic')`)
}

func TestValuesKeepTheirStoredTypeAndBytes(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		// name names the pair.
		name string
		// column is the column of fp_values the pair goes into.
		column string
		// first and second are two values a fingerprint must tell apart.
		first, second string
	}{
		{name: "two reals that print alike", column: "raw", first: "0.1 + 0.2", second: "0.3"},
		{name: "two texts that read as the same time", column: "at", first: "'2026-11-08 10:00:00'",
			second: "'2026-11-08T10:00:00Z'"},
		{name: "an empty text and NULL", column: "raw", first: "''", second: "NULL"},
		{name: "an integer and its text", column: "raw", first: "1", second: "'1'"},
		{name: "a text and its bytes", column: "raw", first: "'1'", second: "x'31'"},
		{name: "an empty text and an empty blob", column: "raw", first: "''", second: "x''"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			const table = "CREATE TABLE fp_values (id INTEGER PRIMARY KEY, at DATETIME, raw)"
			insert := "INSERT INTO fp_values (id, " + tc.column + ") VALUES (1, "
			first := fpOpen(t, table, insert+tc.first+")")
			second := fpOpen(t, table, insert+tc.second+")")

			if a, b := fpOf(t, first, sqlite.Leave{}), fpOf(t, second, sqlite.Leave{}); a == b {
				t.Errorf("Fingerprint() of %s and of %s = %s, want them apart", tc.first, tc.second, a)
			}
		})
	}
}

func TestTwoValuesInARowKeepTheirLengths(t *testing.T) {
	t.Parallel()

	const table = "CREATE TABLE fp_pair (x, y)"
	first := fpOpen(t, table, "INSERT INTO fp_pair VALUES ('a', 'b')")
	second := fpOpen(t, table, "INSERT INTO fp_pair VALUES ('at', x'')")

	if a, b := fpOf(t, first, sqlite.Leave{}), fpOf(t, second, sqlite.Leave{}); a == b {
		t.Errorf("Fingerprint() of the rows ('a', 'b') and ('at', x'') = %s, want them apart", a)
	}
}

func TestARowidHiddenByEveryAliasFails(t *testing.T) {
	t.Parallel()

	db := fpOpen(t, "CREATE TABLE fp_hidden (rowid TEXT, _rowid_ TEXT, oid TEXT)")

	fpMustFail(t, db, sqlite.Leave{},
		`dbkit: the fingerprint cannot order the rows of "fp_hidden", whose columns hide rowid, _rowid_ and oid`)
}

func TestARowidHiddenByOneAliasOrdersByAnother(t *testing.T) {
	t.Parallel()

	const shadowed = "CREATE TABLE fp_shadowed (rowid TEXT, label TEXT)"
	db := fpOpen(t, shadowed, "INSERT INTO fp_shadowed VALUES ('same', 'first'), ('same', 'second')")
	stored := fpOpen(t, shadowed, "INSERT INTO fp_shadowed VALUES ('b', 'x'), ('a', 'y')")
	reversed := fpOpen(t, shadowed, "INSERT INTO fp_shadowed VALUES ('a', 'y'), ('b', 'x')")

	fpMustMove(t, db, sqlite.Leave{}, "UPDATE fp_shadowed SET label = 'changed' WHERE label = 'second'")
	if a, b := fpOf(t, stored, sqlite.Leave{}), fpOf(t, reversed, sqlite.Leave{}); a == b {
		t.Errorf("Fingerprint() of the same rows stored in two orders = %s, want them apart by the real rowid", a)
	}
}

func TestAFingerprintNeverComputesAGeneratedColumn(t *testing.T) {
	t.Parallel()

	db := fpOpen(t, "CREATE TABLE fp_doc (body TEXT)", "INSERT INTO fp_doc (body) VALUES ('not json')",
		"ALTER TABLE fp_doc ADD COLUMN title AS (json_extract(body, '$.title')) VIRTUAL")

	fpOf(t, db, sqlite.Leave{})
}

func TestANewRowidMovesTheFingerprint(t *testing.T) {
	t.Parallel()

	fpMustMove(t, fpOpen(t, "CREATE TABLE fp_body (body TEXT)", "INSERT INTO fp_body VALUES ('a')"), sqlite.Leave{},
		"UPDATE fp_body SET rowid = 99")
	fpMustMove(t, fpOpen(t, "CREATE TABLE fp_shadowed (rowid TEXT, label TEXT)",
		"INSERT INTO fp_shadowed VALUES ('same', 'first')"), sqlite.Leave{}, "UPDATE fp_shadowed SET _rowid_ = 99")
}

func TestAGeneratedColumnNamedRowidOrdersByTheRealRowid(t *testing.T) {
	t.Parallel()

	const generated = "CREATE TABLE fp_genrow (a INTEGER, rowid AS (a * 10))"
	stored := fpOpen(t, generated, "INSERT INTO fp_genrow (a) VALUES (2), (1)")
	reversed := fpOpen(t, generated, "INSERT INTO fp_genrow (a) VALUES (1), (2)")

	if a, b := fpOf(t, stored, sqlite.Leave{}), fpOf(t, reversed, sqlite.Leave{}); a == b {
		t.Errorf("Fingerprint() of the same rows stored in two orders = %s, want them apart by the real rowid", a)
	}
}

func TestAFailedReadAnswersNoFingerprint(t *testing.T) {
	t.Parallel()

	for _, statement := range []string{fpSchemaRead, fpTableRead, fpColumnRead, fpOneRead} {
		t.Run(statement, func(t *testing.T) {
			t.Parallel()
			failed := errors.New("fingerprint test: the read failed on purpose")
			faults := &sqlitetest.Faults{}
			db := sqlitetest.OpenWithFaults(t, testOptions(), faults)
			rulesExec(t, db, fpOneTable)
			faults.FailStatement(statement, failed)

			fp, err := sqlite.Fingerprint(t.Context(), db, sqlite.Leave{})

			if !errors.Is(err, failed) || fp != "" {
				t.Errorf("Fingerprint() = %q, %v, want no fingerprint and an error matching %v", fp, err, failed)
			}
		})
	}
}

func TestAFingerprintUnderAnEndedContextFails(t *testing.T) {
	t.Parallel()

	db := fpOpen(t, fpRowsTable, fpRowsSeed)
	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	fp, err := sqlite.Fingerprint(ctx, db, sqlite.Leave{})

	if !errors.Is(err, context.Canceled) || fp != "" {
		t.Errorf("Fingerprint() = %q, %v, want no fingerprint and an error matching context.Canceled", fp, err)
	}
}
