// SPDX-License-Identifier: Apache-2.0

package sqlitetest

import (
	"context"
	"database/sql"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"testing/fstest"
	"time"

	"github.com/gopherium/framework/dbkit"
	"github.com/gopherium/framework/dbkit/sqlite"
)

const (
	// templateSchema creates the table every copy of a template holds.
	templateSchema = "CREATE TABLE template_rows (a INTEGER NOT NULL)"
	// templateInsert writes one row to the template table.
	templateInsert = "INSERT INTO template_rows VALUES (1)"
	// templateCount counts the rows of the template table.
	templateCount = "SELECT count(*) FROM template_rows"
	// templateTests is how many tests share one template.
	templateTests = 4
	// templateLockWait is a migration lock wait no uncontended run waits out.
	templateLockWait = 5 * time.Second
	// templateLockPoll is how often a run tries the migration lock.
	templateLockPoll = 10 * time.Millisecond
)

// schemaMigration returns a migrate function that creates the template table and counts its runs in runs.
func schemaMigration(runs *int) func(context.Context, *sql.DB) error {
	return func(ctx context.Context, db *sql.DB) error {
		*runs++
		_, err := db.ExecContext(ctx, templateSchema)
		return err
	}
}

// mustTemplate returns a template migrated by migrate with opts and closes it when the test ends.
func mustTemplate(t *testing.T, opts sqlite.Options, migrate func(context.Context, *sql.DB) error) *Template {
	t.Helper()
	tp, err := NewTemplate(t.Context(), opts, migrate)
	if err != nil {
		t.Fatalf("NewTemplate() error = %v, want nil", err)
	}
	t.Cleanup(func() {
		if err := tp.Close(); err != nil {
			t.Errorf("Close() error = %v, want nil", err)
		}
	})
	return tp
}

// templateRows returns how many rows the template table of db holds and fails the test when it cannot.
func templateRows(t *testing.T, db *sql.DB) int64 {
	t.Helper()
	var n int64
	if err := db.QueryRowContext(t.Context(), templateCount).Scan(&n); err != nil {
		t.Fatalf("%s error = %v, want nil", templateCount, err)
	}
	return n
}

// templateKey marks the context NewTemplate is given.
type templateKey struct{}

func TestNewTemplateMigratesUnderItsContext(t *testing.T) {
	t.Parallel()

	given := context.WithValue(t.Context(), templateKey{}, "given")
	var seen any
	tp, err := NewTemplate(given, internalOptions(), func(ctx context.Context, db *sql.DB) error {
		seen = ctx.Value(templateKey{})
		_, err := db.ExecContext(ctx, templateSchema)
		return err
	})
	if err != nil {
		t.Fatalf("NewTemplate() error = %v, want nil", err)
	}
	t.Cleanup(func() { _ = tp.Close() })

	if seen != "given" {
		t.Errorf("migrate saw the context value %v, want the one given to NewTemplate", seen)
	}
	ended, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := NewTemplate(ended, internalOptions(), schemaMigration(new(int))); !errors.Is(err, context.Canceled) {
		t.Errorf("NewTemplate() on an ended context error = %v, want it to match %v", err, context.Canceled)
	}
}

func TestATemplateCopyAnswersFaults(t *testing.T) {
	t.Parallel()

	tp := mustTemplate(t, internalOptions(), schemaMigration(new(int)))
	chosen := errors.New("the chosen insert failed")
	faults := &Faults{}
	faults.FailStatement(templateInsert, chosen)

	db := tp.OpenWithFaults(t, faults)

	if got := templateRows(t, db); got != 0 {
		t.Errorf("a fresh copy holds %d rows, want the migrated empty table", got)
	}
	if _, err := db.ExecContext(t.Context(), templateInsert); !errors.Is(err, chosen) {
		t.Errorf("the chosen insert error = %v, want %v", err, chosen)
	}
}

func TestTemplateServesEveryTestItsOwnCopy(t *testing.T) {
	t.Parallel()

	var runs int
	tp := mustTemplate(t, internalOptions(), schemaMigration(&runs))
	copies := make(chan string, templateTests)

	t.Run("tests", func(t *testing.T) {
		for i := range templateTests {
			t.Run(strconv.Itoa(i), func(t *testing.T) {
				t.Parallel()
				db := tp.Open(t)
				if _, err := db.ExecContext(t.Context(), templateInsert); err != nil {
					t.Fatalf("ExecContext(%q) error = %v, want nil", templateInsert, err)
				}
				if got := templateRows(t, db); got != 1 {
					t.Errorf("rows in the copy = %d, want 1, its own row only", got)
				}
				copies <- databaseFile(t, db)
			})
		}
	})
	close(copies)

	if runs != 1 {
		t.Errorf("migrate ran %d times, want once for every test", runs)
	}
	seen := map[string]bool{}
	for path := range copies {
		if filepath.Dir(path) == tp.folder {
			t.Errorf("a test opened %s in the template folder, want a copy in its own folder", path)
		}
		seen[path] = true
	}
	if len(seen) != templateTests {
		t.Errorf("the tests opened %d distinct files, want %d", len(seen), templateTests)
	}
	if got := templateRows(t, tp.Open(t)); got != 0 {
		t.Errorf("rows in a copy made after every test wrote = %d, want 0 with the template untouched", got)
	}
}

func TestTemplateFileHasNoLogBesideIt(t *testing.T) {
	t.Parallel()

	migrations := sqlite.Migrations{
		Table:    "template_goose_db_version",
		FS:       fstest.MapFS{"00001_template_rows.sql": {Data: []byte("-- +goose Up\n" + templateSchema + ";\n")}},
		LockWait: templateLockWait,
		LockPoll: templateLockPoll,
	}

	tp := mustTemplate(t, internalOptions(), func(ctx context.Context, db *sql.DB) error {
		return sqlite.Migrate(ctx, db, migrations)
	})

	file := filepath.Join(tp.folder, fileName)
	if _, err := os.Stat(file); err != nil {
		t.Fatalf("Stat(%s) error = %v, want the template file", file, err)
	}
	for _, beside := range []string{file + "-wal", file + "-shm"} {
		if _, err := os.Lstat(beside); !errors.Is(err, fs.ErrNotExist) {
			t.Errorf("Lstat(%s) error = %v, want no such file beside the closed template", beside, err)
		}
	}
	if got := templateRows(t, tp.Open(t)); got != 0 {
		t.Errorf("rows in a copy of a template migrated by the runner = %d, want an empty table", got)
	}
}

// templateConns takes every connection of a full pool of db, each one fresh, and closes them at cleanup.
func templateConns(t *testing.T, db *sql.DB, maxConns int) []*sql.Conn {
	t.Helper()
	conns := make([]*sql.Conn, maxConns)
	for i := range conns {
		conn, err := db.Conn(t.Context())
		if err != nil {
			t.Fatalf("Conn() error = %v, want nil", err)
		}
		t.Cleanup(func() { _ = conn.Close() })
		conns[i] = conn
	}
	if got := db.Stats().OpenConnections; got != maxConns {
		t.Fatalf("Stats().OpenConnections = %d, want %d fresh connections", got, maxConns)
	}
	return conns
}

// templateRead returns the one value query answers on conn and fails the test when it cannot.
func templateRead[T any](t *testing.T, conn *sql.Conn, query string, args ...any) T {
	t.Helper()
	var value T
	if err := conn.QueryRowContext(t.Context(), query, args...).Scan(&value); err != nil {
		t.Fatalf("%s error = %v, want nil", query, err)
	}
	return value
}

// templateSynchronous maps each Synchronous option to the value PRAGMA synchronous reads back.
var templateSynchronous = map[sqlite.Synchronous]int64{sqlite.SynchronousNormal: 1, sqlite.SynchronousFull: 2}

// templateMustKeepTheRules fails the test unless connection i of a copy opened with opts carries every rule.
func templateMustKeepTheRules(t *testing.T, i int, conn *sql.Conn, opts sqlite.Options) {
	t.Helper()
	pragmas := map[string]int64{
		"PRAGMA foreign_keys": 1,
		"PRAGMA busy_timeout": opts.BusyTimeout.Milliseconds(),
		"PRAGMA temp_store":   2,
		"PRAGMA cache_size":   -int64(opts.CacheSize),
		"PRAGMA synchronous":  templateSynchronous[opts.Synchronous],
	}
	for query, want := range pragmas {
		if got := templateRead[int64](t, conn, query); got != want {
			t.Errorf("%s on connection %d = %d, want %d", query, i, got, want)
		}
	}
	if got := templateRead[string](t, conn, "PRAGMA journal_mode"); got != "wal" {
		t.Errorf("PRAGMA journal_mode on connection %d = %q, want wal", i, got)
	}
	moment := time.Date(2026, time.October, 6, 12, 30, 45, 123456789, time.UTC)
	if got := templateRead[string](t, conn, "SELECT typeof(?)", moment); got != "integer" {
		t.Errorf("a time.Time on connection %d binds as %s, want integer", i, got)
	}
	if got := templateRead[string](t, conn, "SELECT casefold('ÑANDÚ')"); got != "ñandú" {
		t.Errorf("casefold('ÑANDÚ') on connection %d = %q, want ñandú", i, got)
	}
	if _, err := conn.ExecContext(t.Context(), "PRAGMA writable_schema=ON"); err != nil {
		t.Fatalf("PRAGMA writable_schema=ON on connection %d error = %v, want nil", i, err)
	}
	_, err := conn.ExecContext(t.Context(), "UPDATE sqlite_schema SET sql = sql WHERE name = 'template_rows'")
	if err == nil || !strings.Contains(err.Error(), "may not be modified") {
		t.Errorf("an edit of the schema table on connection %d error = %v, want it refused", i, err)
	}
}

func TestTemplateCopyKeepsEveryRule(t *testing.T) {
	t.Parallel()

	functions, err := dbkit.NewFunctionList(dbkit.CaseFold())
	if err != nil {
		t.Fatalf("NewFunctionList() error = %v, want nil", err)
	}
	opts := internalOptions()
	opts.Functions = functions
	var runs int
	tp := mustTemplate(t, opts, schemaMigration(&runs))

	db := tp.Open(t)

	for i, conn := range templateConns(t, db, opts.MaxConns) {
		templateMustKeepTheRules(t, i, conn, opts)
	}
}

// errMigrateFailed is the error failingMigration answers with.
var errMigrateFailed = errors.New("sqlitetest test: the migration failed on purpose")

// failingMigration creates the template table and then answers errMigrateFailed.
func failingMigration(ctx context.Context, db *sql.DB) error {
	if _, err := db.ExecContext(ctx, templateSchema); err != nil {
		return err
	}
	return errMigrateFailed
}

// templateRoot points the temp folder of the process at a fresh folder for the rest of the test and returns it.
func templateRoot(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	t.Setenv("TMPDIR", root)
	return root
}

// templateLinkRoot points the temp folder of the process at a link to a fresh folder and returns the resolved folder.
func templateLinkRoot(t *testing.T) string {
	t.Helper()
	target := realFolder(t, t.TempDir())
	link := filepath.Join(t.TempDir(), "link")
	if err := os.Symlink(target, link); err != nil {
		t.Fatalf("Symlink() error = %v, want nil", err)
	}
	t.Setenv("TMPDIR", link)
	return target
}

// mustHoldNothing fails the test unless folder is empty.
func mustHoldNothing(t *testing.T, folder string) {
	t.Helper()
	entries, err := os.ReadDir(folder)
	if err != nil || len(entries) != 0 {
		t.Errorf("ReadDir(%s) = %v, %v, want an empty folder", folder, entries, err)
	}
}

func TestTemplateThatFailsLeavesNoFolderBehind(t *testing.T) {
	var runs int
	cases := []struct {
		// name names the failure.
		name string
		// opts are the options the template is built with.
		opts sqlite.Options
		// migrate is the migrate function the template is built with.
		migrate func(context.Context, *sql.DB) error
		// want is the error NewTemplate answers.
		want string
	}{
		{"a failing migrate", internalOptions(), failingMigration,
			"dbkit: migrate the template database: " + errMigrateFailed.Error()},
		{"a refused option", sqlite.Options{}, schemaMigration(&runs),
			"dbkit: open the template database: dbkit: the option BusyTimeout must be 1ms or more, got 0s"},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			root := templateRoot(t)

			tp, err := NewTemplate(t.Context(), c.opts, c.migrate)

			if tp != nil || err == nil || err.Error() != c.want {
				t.Errorf("NewTemplate() = %v, %v, want nil and %q", tp, err, c.want)
			}
			mustHoldNothing(t, root)
		})
	}
	if runs != 0 {
		t.Errorf("migrate ran %d times with a refused option, want never", runs)
	}
}

const (
	// templatePanic is the value a panicking migrate panics with.
	templatePanic = "sqlitetest test: migrate panicked on purpose"
	// templateFatal is the message a migrate that stops its goroutine fails with.
	templateFatal = "sqlitetest test: migrate stopped its goroutine on purpose"
)

func TestTemplateThatStopsInMigrateLeavesNoFolderBehind(t *testing.T) {
	cases := []struct {
		// name names the way migrate stops.
		name string
		// stop ends migrate through tb.
		stop func(tb testing.TB)
		// recovered is the value the caller of NewTemplate recovers.
		recovered any
		// fatals are the Fatalf messages the caller of NewTemplate records.
		fatals []string
	}{
		{"a panic", func(testing.TB) { panic(templatePanic) }, templatePanic, nil},
		{"a Fatalf", func(tb testing.TB) { tb.Fatalf("%s", templateFatal) }, nil, []string{templateFatal}},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			root := templateRoot(t)
			var given *sql.DB
			var recovered any

			r := runRecorded(t, func(tb testing.TB) {
				defer func() { recovered = recover() }()
				_, _ = NewTemplate(t.Context(), internalOptions(), func(ctx context.Context, db *sql.DB) error {
					given = db
					if _, err := db.ExecContext(ctx, templateSchema); err != nil {
						return err
					}
					c.stop(tb)
					return nil
				})
			})

			if recovered != c.recovered || !slices.Equal(r.fatals, c.fatals) {
				t.Errorf("the caller of NewTemplate recovered %v and recorded %q, want %v and %q",
					recovered, r.fatals, c.recovered, c.fatals)
			}
			if given == nil {
				t.Fatalf("migrate never ran, want it to run once")
			}
			if err := given.PingContext(t.Context()); err == nil || err.Error() != "sql: database is closed" {
				t.Errorf("PingContext() on the handle migrate was given error = %v, want it closed", err)
			}
			mustHoldNothing(t, root)
		})
	}
}

func TestTemplateNeedsATempFolder(t *testing.T) {
	plain := filepath.Join(t.TempDir(), "plain")
	if err := os.WriteFile(plain, nil, 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v, want nil", err)
	}
	var runs int
	cases := []struct {
		// name names the temp folder.
		name string
		// root is the temp folder of the process.
		root string
		// marked is the error NewTemplate wraps.
		marked error
		// want starts the error NewTemplate answers.
		want string
	}{
		{"a missing temp folder", filepath.Join(t.TempDir(), "missing"), fs.ErrNotExist,
			"dbkit: resolve the temp folder: "},
		{"a temp folder that is a plain file", plain, syscall.ENOTDIR,
			"dbkit: make the template folder: "},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Setenv("TMPDIR", c.root)

			tp, err := NewTemplate(t.Context(), internalOptions(), schemaMigration(&runs))

			if tp != nil || !errors.Is(err, c.marked) || !strings.HasPrefix(err.Error(), c.want) {
				t.Errorf("NewTemplate() = %v, %v, want nil and an error marked %v starting %q",
					tp, err, c.marked, c.want)
			}
		})
	}
	if runs != 0 {
		t.Errorf("migrate ran %d times without a template folder, want never", runs)
	}
}

func TestTemplateFolderBehindALinkIsResolved(t *testing.T) {
	root := templateLinkRoot(t)

	tp := mustTemplate(t, internalOptions(), schemaMigration(new(int)))

	if got := filepath.Dir(tp.folder); got != root {
		t.Errorf("the template folder sits in %s, want %s with every link resolved", got, root)
	}
	if got := templateRows(t, tp.Open(t)); got != 0 {
		t.Errorf("rows in a copy of a template behind a link = %d, want an empty table", got)
	}
}

func TestTemplateRefusesANilMigrate(t *testing.T) {
	root := templateRoot(t)

	tp, err := NewTemplate(t.Context(), internalOptions(), nil)

	want := "dbkit: NewTemplate needs a migrate function, got nil"
	if tp != nil || err == nil || err.Error() != want {
		t.Errorf("NewTemplate() = %v, %v, want nil and %q", tp, err, want)
	}
	mustHoldNothing(t, root)
}

func TestTemplateOfAMigrateThatWritesNothingServesAnEmptyDatabase(t *testing.T) {
	t.Parallel()

	tp := mustTemplate(t, internalOptions(), func(context.Context, *sql.DB) error { return nil })

	db := tp.Open(t)

	var tables int64
	if err := db.QueryRowContext(t.Context(), "SELECT count(*) FROM sqlite_schema").Scan(&tables); err != nil ||
		tables != 0 {
		t.Errorf("tables in a copy of an unwritten template = %d, %v, want none", tables, err)
	}
}

func TestTemplateRefusesAMigrateThatLeavesAConnectionOpen(t *testing.T) {
	root := templateLinkRoot(t)
	var file string

	tp, err := NewTemplate(t.Context(), internalOptions(), func(ctx context.Context, db *sql.DB) error {
		conn, err := db.Conn(ctx)
		if err != nil {
			return err
		}
		t.Cleanup(func() { _ = conn.Close() })
		if err := conn.QueryRowContext(ctx, "SELECT file FROM pragma_database_list WHERE name = 'main'").
			Scan(&file); err != nil {
			return err
		}
		_, err = conn.ExecContext(ctx, templateSchema)
		return err
	})

	want := "dbkit: the template keeps its write-ahead log " + file + "-wal after its handle closed, " +
		"so migrate left a connection open"
	if tp != nil || err == nil || err.Error() != want {
		t.Errorf("NewTemplate() = %v, %v, want nil and %q", tp, err, want)
	}
	mustHoldNothing(t, root)
}

func TestTemplateOpenAfterCloseFailsTheTest(t *testing.T) {
	t.Parallel()

	var runs int
	tp := mustTemplate(t, internalOptions(), schemaMigration(&runs))
	if err := tp.Close(); err != nil {
		t.Fatalf("Close() error = %v, want nil", err)
	}

	opened := runRecorded(t, func(tb testing.TB) { tp.Open(tb) })
	faulty := runRecorded(t, func(tb testing.TB) { tp.OpenWithFaults(tb, &Faults{}) })

	want := []string{"dbkit: copy the template: dbkit: the template is closed"}
	if !slices.Equal(opened.fatals, want) {
		t.Errorf("Open() Fatalf messages = %q, want %q", opened.fatals, want)
	}
	if !slices.Equal(faulty.fatals, want) {
		t.Errorf("OpenWithFaults() Fatalf messages = %q, want %q", faulty.fatals, want)
	}
}

func TestTemplateCloseTwiceIsSafe(t *testing.T) {
	t.Parallel()

	var runs int
	tp, err := NewTemplate(t.Context(), internalOptions(), schemaMigration(&runs))
	if err != nil {
		t.Fatalf("NewTemplate() error = %v, want nil", err)
	}

	first := tp.Close()
	second := tp.Close()

	if first != nil || second != nil {
		t.Errorf("Close() twice = %v, then %v, want nil both times", first, second)
	}
	if _, err := os.Stat(tp.folder); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("Stat(%s) after Close error = %v, want the folder gone", tp.folder, err)
	}
}

func TestTemplateOpenFailsTheTestWhenTheTemplateFileIsGone(t *testing.T) {
	t.Parallel()

	var runs int
	tp := mustTemplate(t, internalOptions(), schemaMigration(&runs))
	file := filepath.Join(tp.folder, fileName)
	if err := os.Remove(file); err != nil {
		t.Fatalf("Remove(%s) error = %v, want nil", file, err)
	}
	_, readErr := os.ReadFile(file)

	r := runRecorded(t, func(tb testing.TB) { tp.Open(tb) })

	want := []string{"dbkit: copy the template: " + readErr.Error()}
	if !slices.Equal(r.fatals, want) {
		t.Errorf("Fatalf messages = %q, want %q", r.fatals, want)
	}
}

// templateCopy is what one Open of a template gave.
type templateCopy struct {
	// r recorded the failures of the Open.
	r *recorder
	// db is the handle the Open returned, or nil.
	db *sql.DB
}

func TestTemplateClosedWhileTestsOpenCopiesFailsThemCleanly(t *testing.T) {
	t.Parallel()

	var runs int
	tp := mustTemplate(t, internalOptions(), schemaMigration(&runs))
	start := make(chan struct{})
	copies := make(chan templateCopy, templateTests)
	for range templateTests {
		go func() {
			c := templateCopy{r: &recorder{TB: t}}
			defer func() { copies <- c }()
			<-start
			c.db = tp.Open(c.r)
		}()
	}
	closed := make(chan error, 1)
	go func() {
		<-start
		closed <- tp.Close()
	}()

	close(start)

	if err := <-closed; err != nil {
		t.Errorf("Close() error = %v, want nil", err)
	}
	want := []string{"dbkit: copy the template: dbkit: the template is closed"}
	for range templateTests {
		c := <-copies
		if c.db == nil && !slices.Equal(c.r.fatals, want) {
			t.Errorf("Fatalf messages of an Open during Close = %q, want a full copy or %q", c.r.fatals, want)
		}
		if c.db != nil && templateRows(t, c.db) != 0 {
			t.Errorf("a copy made during Close holds rows, want the empty template table")
		}
	}
}
