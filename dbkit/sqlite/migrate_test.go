// SPDX-License-Identifier: Apache-2.0

package sqlite_test

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"github.com/pressly/goose/v3"

	"github.com/gopherium/framework/dbkit/sqlite"
)

const (
	// migrateLockWait is a lock wait no uncontended run waits out.
	migrateLockWait = 20 * time.Second
	// migrateLockPoll is how often a waiting run of the tests tries the lock.
	migrateLockPoll = 5 * time.Millisecond
	// migrateFirstTable is the version table of the first owner.
	migrateFirstTable = "first_owner_versions"
	// migrateSecondTable is the version table of the second owner.
	migrateSecondTable = "second_owner_versions"
)

// migrateOpen returns a handle on a fresh file in a folder with every link resolved, and the file's path.
func migrateOpen(t *testing.T) (*sql.DB, string) {
	t.Helper()
	folder, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatalf("EvalSymlinks() error = %v, want nil", err)
	}
	path := filepath.Join(folder, "site.db")
	return mustOpen(t, "sqlite:"+path, testOptions()), path
}

// migrateFile returns a SQL migration file whose up section is up.
func migrateFile(up string) *fstest.MapFile {
	return &fstest.MapFile{Data: []byte("-- +goose Up\n" + up + "\n")}
}

// migrateGo returns a Go migration of version that runs up in a transaction.
func migrateGo(version int64, up func(ctx context.Context, tx *sql.Tx) error) *goose.Migration {
	return goose.NewGoMigration(version, &goose.GoFunc{RunTx: up}, nil)
}

// migrateMust runs Migrate with m on db and fails the test when it cannot.
func migrateMust(t *testing.T, db *sql.DB, m sqlite.Migrations) {
	t.Helper()
	if err := sqlite.Migrate(t.Context(), db, m); err != nil {
		t.Fatalf("Migrate(%s) error = %v, want nil", m.Table, err)
	}
}

// migrateVersions returns the versions table records as applied on db, in the order they were applied.
func migrateVersions(t *testing.T, db *sql.DB, table string) []int64 {
	t.Helper()
	rows, err := db.QueryContext(t.Context(), "SELECT version_id FROM "+table+" WHERE is_applied ORDER BY id")
	if err != nil {
		t.Fatalf("read the versions of %s: %v", table, err)
	}
	defer func() { _ = rows.Close() }()
	var versions []int64
	for rows.Next() {
		var version int64
		if err := rows.Scan(&version); err != nil {
			t.Fatalf("scan a version of %s: %v", table, err)
		}
		versions = append(versions, version)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("read the versions of %s: %v", table, err)
	}
	return versions
}

// migrateMustVersions fails the test unless table on db records exactly want as applied, in order.
func migrateMustVersions(t *testing.T, db *sql.DB, table string, want ...int64) {
	t.Helper()
	if got := migrateVersions(t, db, table); !slices.Equal(got, want) {
		t.Errorf("applied versions of %s = %v, want %v", table, got, want)
	}
}

// migrateCount returns the number of rows of table on db.
func migrateCount(t *testing.T, db *sql.DB, table string) int64 {
	t.Helper()
	var n int64
	if err := db.QueryRowContext(t.Context(), "SELECT count(*) FROM "+table).Scan(&n); err != nil {
		t.Fatalf("count the rows of %s: %v", table, err)
	}
	return n
}

func TestMigrateAppliesEveryMigrationOnce(t *testing.T) {
	t.Parallel()

	db, _ := migrateOpen(t)
	var firstRuns, secondRuns int
	first := sqlite.Migrations{
		Table: migrateFirstTable,
		FS: fstest.MapFS{
			"00001_first_rows.sql": migrateFile("CREATE TABLE first_rows (a INTEGER NOT NULL);"),
			"00002_first_seed.sql": migrateFile("INSERT INTO first_rows VALUES (1);"),
		},
		Go: []*goose.Migration{migrateGo(3, func(ctx context.Context, tx *sql.Tx) error {
			firstRuns++
			_, err := tx.ExecContext(ctx, "INSERT INTO first_rows VALUES (2)")
			return err
		})},
		LockWait: migrateLockWait,
		LockPoll: migrateLockPoll,
	}
	second := sqlite.Migrations{
		Table: migrateSecondTable,
		FS:    fstest.MapFS{"00001_second_rows.sql": migrateFile("CREATE TABLE second_rows (a INTEGER NOT NULL);")},
		Go: []*goose.Migration{goose.NewGoMigration(2, &goose.GoFunc{RunDB: func(ctx context.Context, db *sql.DB) error {
			secondRuns++
			_, err := db.ExecContext(ctx, "INSERT INTO second_rows VALUES (1)")
			return err
		}}, nil)},
		LockWait: migrateLockWait,
		LockPoll: migrateLockPoll,
	}

	for range 2 {
		migrateMust(t, db, first)
		migrateMust(t, db, second)
	}

	migrateMustVersions(t, db, migrateFirstTable, 0, 1, 2, 3)
	migrateMustVersions(t, db, migrateSecondTable, 0, 1, 2)
	if firstRuns != 1 || secondRuns != 1 {
		t.Errorf("the Go migrations ran %d and %d times, want once each", firstRuns, secondRuns)
	}
	if got := migrateCount(t, db, "first_rows"); got != 2 {
		t.Errorf("first_rows holds %d rows, want 2", got)
	}
	if got := migrateCount(t, db, "second_rows"); got != 1 {
		t.Errorf("second_rows holds %d rows, want 1", got)
	}
}

// migrateValid returns the migrations of the first owner, one SQL file that creates first_rows.
func migrateValid() sqlite.Migrations {
	return sqlite.Migrations{
		Table:    migrateFirstTable,
		FS:       fstest.MapFS{"00001_first_rows.sql": migrateFile("CREATE TABLE first_rows (a INTEGER NOT NULL);")},
		LockWait: migrateLockWait,
		LockPoll: migrateLockPoll,
	}
}

// migrateMustHoldNoTable fails the test unless db holds no table.
func migrateMustHoldNoTable(t *testing.T, db *sql.DB) {
	t.Helper()
	var n int64
	if err := db.QueryRowContext(t.Context(), "SELECT count(*) FROM sqlite_schema").Scan(&n); err != nil || n != 0 {
		t.Errorf("sqlite_schema holds %d entries, %v, want none", n, err)
	}
}

// migrateMustBeUntouched fails the test unless db holds no table and no lock file sits beside the file at path.
func migrateMustBeUntouched(t *testing.T, db *sql.DB, path string) {
	t.Helper()
	migrateMustHoldNoTable(t, db)
	mustNotExist(t, path+".migrate.lock")
}

func TestMigrateRefusesABadOption(t *testing.T) {
	t.Parallel()

	identifier := "dbkit: the option Table must be a plain identifier of ASCII letters, digits and underscores, got "
	cases := []struct {
		name   string
		change func(*sqlite.Migrations)
		want   string
	}{
		{"a missing table", func(m *sqlite.Migrations) { m.Table = "" },
			"dbkit: the option Table must name the version table"},
		{"a table with a space", func(m *sqlite.Migrations) { m.Table = "first owner" },
			identifier + `"first owner"`},
		{"a table with a schema", func(m *sqlite.Migrations) { m.Table = "main.first_owner_versions" },
			identifier + `"main.first_owner_versions"`},
		{"a quoted table", func(m *sqlite.Migrations) { m.Table = `"first_owner_versions"` },
			identifier + `"\"first_owner_versions\""`},
		{"a table that starts with a digit", func(m *sqlite.Migrations) { m.Table = "1_owner_versions" },
			identifier + `"1_owner_versions"`},
		{"a table with a hyphen", func(m *sqlite.Migrations) { m.Table = "first-owner-versions" },
			identifier + `"first-owner-versions"`},
		{"a table with a letter outside ASCII", func(m *sqlite.Migrations) { m.Table = "versión" },
			identifier + `"versión"`},
		{"a table with a trailing newline", func(m *sqlite.Migrations) { m.Table = "first_owner_versions\n" },
			identifier + `"first_owner_versions\n"`},
		{"a table named by the keyword order", func(m *sqlite.Migrations) { m.Table = "order" },
			identifier + `"order"`},
		{"a table named by a keyword in upper case", func(m *sqlite.Migrations) { m.Table = "GROUP" },
			identifier + `"GROUP"`},
		{"a table named by a keyword in mixed case", func(m *sqlite.Migrations) { m.Table = "Table" },
			identifier + `"Table"`},
		{"a table named by the keyword values", func(m *sqlite.Migrations) { m.Table = "values" },
			identifier + `"values"`},
		{"a table named by a keyword SQLite also reads as a name", func(m *sqlite.Migrations) { m.Table = "key" },
			identifier + `"key"`},
		{"a table with the reserved prefix", func(m *sqlite.Migrations) { m.Table = "sqlite_versions" },
			identifier + `"sqlite_versions"`},
		{"a table with the reserved prefix in mixed case", func(m *sqlite.Migrations) { m.Table = "SQLite_Versions" },
			identifier + `"SQLite_Versions"`},
		{"a missing lock wait", func(m *sqlite.Migrations) { m.LockWait = 0 },
			"dbkit: the option LockWait must stand above zero, got 0s"},
		{"a negative lock wait", func(m *sqlite.Migrations) { m.LockWait = -time.Millisecond },
			"dbkit: the option LockWait must stand above zero, got -1ms"},
		{"a missing lock poll", func(m *sqlite.Migrations) { m.LockPoll = 0 },
			"dbkit: the option LockPoll must stand above zero, got 0s"},
		{"a negative lock poll", func(m *sqlite.Migrations) { m.LockPoll = -time.Millisecond },
			"dbkit: the option LockPoll must stand above zero, got -1ms"},
		{"a nil Go migration", func(m *sqlite.Migrations) {
			m.Go = []*goose.Migration{migrateGo(2, func(context.Context, *sql.Tx) error { return nil }), nil}
		}, "dbkit: the option Go holds nil at index 1"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			db, path := migrateOpen(t)
			m := migrateValid()
			c.change(&m)

			err := sqlite.Migrate(t.Context(), db, m)

			if err == nil || err.Error() != c.want {
				t.Errorf("Migrate() error = %v, want %q", err, c.want)
			}
			migrateMustBeUntouched(t, db, path)
		})
	}
}

func TestMigrateRefusesANilHandle(t *testing.T) {
	t.Parallel()

	err := sqlite.Migrate(t.Context(), nil, migrateValid())

	if want := "dbkit: Migrate needs a database handle, got nil"; err == nil || err.Error() != want {
		t.Errorf("Migrate() error = %v, want %q", err, want)
	}
}

// migrateMarkedName is the name of the SQL migration the marker tests write.
const migrateMarkedName = "00001_marked.sql"

// migrateMarked returns a SQL migration that runs line and then a VACUUM.
func migrateMarked(line string) string {
	return "-- +goose Up\n" + line + "\nVACUUM;\n"
}

// migrateMarker is a SQL migration and how goose and Migrate read it.
type migrateMarker struct {
	// name names the case.
	name string
	// file is the text of the SQL migration.
	file string
	// refused reports that Migrate refuses the file.
	refused bool
	// outside reports that goose itself runs the file outside a transaction.
	outside bool
}

// migrateMarkers holds every spelling of the marker goose reads and lines that only look like one.
var migrateMarkers = []migrateMarker{
	{"the marker as goose documents it", migrateMarked("-- +goose NO TRANSACTION"), true, true},
	{"the marker in lower case", migrateMarked("-- +goose no transaction"), true, true},
	{"the marker in mixed case", migrateMarked("-- +goose No Transaction"), true, true},
	{"the marker with no space after the dashes", migrateMarked("--+goose NO TRANSACTION"), true, true},
	{"the marker with wide spacing", migrateMarked("--    +goose    NO TRANSACTION    "), true, true},
	{"the marker with tabs", migrateMarked("--\t+goose\tNO TRANSACTION"), true, true},
	{"the marker in a file with carriage returns",
		"-- +goose Up\r\n-- +goose NO TRANSACTION\r\nVACUUM;\r\n", true, true},
	{"the marker after four dashes", migrateMarked("---- +goose NO TRANSACTION"), true, true},
	{"the marker with trailing dashes", migrateMarked("-- +goose NO TRANSACTION --"), true, true},
	{"the marker with the annotation word last", migrateMarked("-- NO TRANSACTION +goose"), true, true},
	{"the marker with dashes inside a word", migrateMarked("-- +goose NO TRANS--ACTION"), true, true},
	{"the marker below the down section",
		"-- +goose Up\nVACUUM;\n-- +goose Down\n-- +goose NO TRANSACTION\n", true, true},
	{"the marker on its own line inside a string",
		"-- +goose Up\nSELECT 'first line\n-- +goose NO TRANSACTION\nlast line';\nVACUUM;\n", true, true},
	{"the marker indented, which goose cannot parse", migrateMarked("  -- +goose NO TRANSACTION"), true, false},
	{"the marker inside a string", migrateMarked("SELECT '-- +goose NO TRANSACTION';"), false, false},
	{"a plain comment", migrateMarked("-- the next statement needs NO TRANSACTION"), false, false},
	{"a block comment", migrateMarked("/* -- +goose NO TRANSACTION */"), false, false},
	{"a longer last word", migrateMarked("-- +goose NO TRANSACTIONS"), false, false},
	{"the annotation word in the middle", migrateMarked("-- NO +goose TRANSACTION"), false, false},
	{"a second annotation word", migrateMarked("-- +goose NO TRANSACTION +goose"), false, false},
}

// migrateGooseOutside reports whether goose itself, with no dbkit check, runs fsys outside a transaction.
func migrateGooseOutside(t *testing.T, fsys fstest.MapFS) bool {
	t.Helper()
	db, _ := migrateOpen(t)
	provider, err := goose.NewProvider(goose.DialectSQLite3, db, fsys,
		goose.WithTableName("oracle_versions"), goose.WithDisableGlobalRegistry(true))
	if err != nil {
		t.Fatalf("NewProvider() error = %v, want nil", err)
	}
	_, err = provider.Up(t.Context())
	return err == nil
}

func TestRunnerRefusesANoTransactionFile(t *testing.T) {
	t.Parallel()

	for _, c := range migrateMarkers {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			fsys := fstest.MapFS{migrateMarkedName: &fstest.MapFile{Data: []byte(c.file)}}
			if got := migrateGooseOutside(t, fsys); got != c.outside {
				t.Fatalf("goose runs the file outside a transaction = %v, want %v", got, c.outside)
			}
			db, path := migrateOpen(t)
			m := migrateValid()
			m.FS = fsys

			err := sqlite.Migrate(t.Context(), db, m)

			want := "dbkit: the SQL migration " + migrateMarkedName +
				" is marked NO TRANSACTION, and Migrate runs every SQL migration in one transaction"
			switch {
			case c.refused && (err == nil || err.Error() != want):
				t.Errorf("Migrate() error = %v, want %q", err, want)
			case c.refused:
				migrateMustBeUntouched(t, db, path)
			case err == nil || strings.Contains(err.Error(), "marked NO TRANSACTION"):
				t.Errorf("Migrate() error = %v, want goose's own error with the file never refused", err)
			}
		})
	}
	t.Run("a marked file after a clean one", func(t *testing.T) {
		t.Parallel()
		db, path := migrateOpen(t)
		m := migrateValid()
		m.FS = fstest.MapFS{
			"00001_clean.sql":  migrateFile("CREATE TABLE clean_rows (a INTEGER);"),
			"00002_marked.sql": migrateFile("-- +goose NO TRANSACTION\nCREATE TABLE marked_rows (a INTEGER);"),
		}

		err := sqlite.Migrate(t.Context(), db, m)

		want := "dbkit: the SQL migration 00002_marked.sql is marked NO TRANSACTION, " +
			"and Migrate runs every SQL migration in one transaction"
		if err == nil || err.Error() != want {
			t.Errorf("Migrate() error = %v, want %q", err, want)
		}
		migrateMustBeUntouched(t, db, path)
	})
}

func TestMigrateTakesGoMigrationsAlone(t *testing.T) {
	t.Parallel()

	db, _ := migrateOpen(t)
	m := migrateValid()
	m.FS = nil
	m.Go = []*goose.Migration{migrateGo(1, func(ctx context.Context, tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, "CREATE TABLE go_rows (a INTEGER)")
		return err
	})}

	migrateMust(t, db, m)

	migrateMustVersions(t, db, migrateFirstTable, 0, 1)
	if got := migrateCount(t, db, "go_rows"); got != 0 {
		t.Errorf("go_rows holds %d rows, want the empty table", got)
	}
}

func TestMigrateKeepsTheGooseGuardOfOneConnection(t *testing.T) {
	t.Parallel()

	db, _ := migrateOpen(t)
	db.SetMaxOpenConns(1)
	m := migrateValid()
	m.FS = nil
	m.Go = []*goose.Migration{goose.NewGoMigration(1, &goose.GoFunc{RunDB: func(context.Context, *sql.DB) error {
		return nil
	}}, nil)}
	ctx, cancel := context.WithTimeout(t.Context(), migrateLockWait)
	defer cancel()

	err := sqlite.Migrate(ctx, db, m)

	if want := "potential deadlock detected"; err == nil || !strings.Contains(err.Error(), want) {
		t.Errorf("Migrate() error = %v, want goose's error holding %q", err, want)
	}
	migrateMustVersions(t, db, migrateFirstTable, 0)
}

func TestMigrateRefusesASQLMigrationItCannotRead(t *testing.T) {
	t.Parallel()

	db, path := migrateOpen(t)
	m := migrateValid()
	m.FS = fstest.MapFS{"00001_folder.sql/inner.txt": &fstest.MapFile{Data: []byte("not a migration")}}

	err := sqlite.Migrate(t.Context(), db, m)

	prefix := "dbkit: read the SQL migration 00001_folder.sql: "
	if err == nil || !strings.HasPrefix(err.Error(), prefix) {
		t.Errorf("Migrate() error = %v, want an error starting %q", err, prefix)
	}
	migrateMustBeUntouched(t, db, path)
}

// errMigrateList is the error the listing of migrateUnlistable answers with.
var errMigrateList = errors.New("migrate test: the listing failed on purpose")

// migrateUnlistable is a file system whose listing always fails.
type migrateUnlistable struct {
	fstest.MapFS
}

// Glob answers errMigrateList for every pattern.
func (migrateUnlistable) Glob(string) ([]string, error) {
	return nil, errMigrateList
}

func TestMigrateRefusesAFileSystemItCannotList(t *testing.T) {
	t.Parallel()

	db, path := migrateOpen(t)
	m := migrateValid()
	m.FS = migrateUnlistable{MapFS: fstest.MapFS{"00001_first_rows.sql": migrateFile("SELECT 1;")}}

	err := sqlite.Migrate(t.Context(), db, m)

	if want := "dbkit: list the SQL migrations: " + errMigrateList.Error(); !errors.Is(err, errMigrateList) ||
		err.Error() != want {
		t.Errorf("Migrate() error = %v, want %q", err, want)
	}
	migrateMustBeUntouched(t, db, path)
}

func TestMigrateRefusesAHandleWithNoFile(t *testing.T) {
	t.Parallel()

	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("Open() error = %v, want nil", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	err = sqlite.Migrate(t.Context(), db, migrateValid())

	if want := "dbkit: Migrate needs a database file, and the handle has none"; err == nil || err.Error() != want {
		t.Errorf("Migrate() error = %v, want %q", err, want)
	}
	var n int64
	if err := db.QueryRowContext(t.Context(), "SELECT count(*) FROM sqlite_schema").Scan(&n); err != nil || n != 0 {
		t.Errorf("sqlite_schema holds %d entries, %v, want none", n, err)
	}
}

func TestMigrateReadsThePathOnItsContext(t *testing.T) {
	t.Parallel()

	db, path := migrateOpen(t)
	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	err := sqlite.Migrate(ctx, db, migrateValid())

	if want := "dbkit: read the database path: context canceled"; !errors.Is(err, context.Canceled) ||
		err.Error() != want {
		t.Errorf("Migrate() error = %v, want %q", err, want)
	}
	migrateMustBeUntouched(t, db, path)
}

func TestMigrateNamesTheOwnerInARunnerError(t *testing.T) {
	t.Parallel()

	t.Run("no migrations at all", func(t *testing.T) {
		t.Parallel()
		db, _ := migrateOpen(t)
		m := migrateValid()
		m.FS = fstest.MapFS{}

		err := sqlite.Migrate(t.Context(), db, m)

		prefix := "dbkit: build the migration runner of " + migrateFirstTable + ": "
		if !errors.Is(err, goose.ErrNoMigrations) || !strings.HasPrefix(err.Error(), prefix) {
			t.Errorf("Migrate() error = %v, want goose.ErrNoMigrations after %q", err, prefix)
		}
	})
	t.Run("a SQL migration that fails", func(t *testing.T) {
		t.Parallel()
		db, _ := migrateOpen(t)
		m := migrateValid()
		m.FS = fstest.MapFS{"00001_broken.sql": migrateFile("CREATE TABLE broken_rows (a INTEGER NOT NULL;")}

		err := sqlite.Migrate(t.Context(), db, m)

		var partial *goose.PartialError
		prefix := "dbkit: run the migrations of " + migrateFirstTable + ": "
		if !errors.As(err, &partial) || !strings.HasPrefix(err.Error(), prefix) {
			t.Errorf("Migrate() error = %v, want goose's partial error after %q", err, prefix)
		}
		migrateMustVersions(t, db, migrateFirstTable, 0)
	})
}

// migrateGlobalTable is the table the Go migration in goose's global registry creates.
const migrateGlobalTable = "global_registry_rows"

// migrateGlobalUp creates the table of the Go migration in goose's global registry.
func migrateGlobalUp(ctx context.Context, tx *sql.Tx) error {
	_, err := tx.ExecContext(ctx, "CREATE TABLE "+migrateGlobalTable+" (a INTEGER)")
	return err
}

// migrateRegisterGlobally puts one Go migration in goose's global registry until the test ends.
func migrateRegisterGlobally(t *testing.T) {
	t.Helper()
	if err := goose.SetGlobalMigrations(migrateGo(90001, migrateGlobalUp)); err != nil {
		t.Fatalf("SetGlobalMigrations() error = %v, want nil", err)
	}
	t.Cleanup(goose.ResetGlobalMigrations)
}

// migrateGlobalTableExists reports whether the table of the global migration exists on db.
func migrateGlobalTableExists(t *testing.T, db *sql.DB) bool {
	t.Helper()
	var n int64
	lookup := "SELECT count(*) FROM sqlite_schema WHERE name = ?"
	if err := db.QueryRowContext(t.Context(), lookup, migrateGlobalTable).Scan(&n); err != nil {
		t.Fatalf("look for %s: %v", migrateGlobalTable, err)
	}
	return n == 1
}

func TestRunnerIgnoresTheGlobalRegistry(t *testing.T) {
	migrateRegisterGlobally(t)
	control, _ := migrateOpen(t)
	provider, err := goose.NewProvider(goose.DialectSQLite3, control, nil, goose.WithTableName("control_versions"))
	if err != nil {
		t.Fatalf("NewProvider() error = %v, want nil", err)
	}
	if _, err := provider.Up(t.Context()); err != nil {
		t.Fatalf("Up() error = %v, want nil", err)
	}
	if !migrateGlobalTableExists(t, control) {
		t.Fatalf("a plain goose provider left %s absent, want the global migration registered", migrateGlobalTable)
	}
	db, _ := migrateOpen(t)

	migrateMust(t, db, migrateValid())

	if migrateGlobalTableExists(t, db) {
		t.Errorf("Migrate() created %s, want the global registry ignored", migrateGlobalTable)
	}
	migrateMustVersions(t, db, migrateFirstTable, 0, 1)
}
