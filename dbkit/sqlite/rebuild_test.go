// SPDX-License-Identifier: Apache-2.0

package sqlite_test

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"fmt"
	"path/filepath"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"testing/fstest"
	"time"

	"github.com/pressly/goose/v3"

	"github.com/gopherium/framework/dbkit"
	"github.com/gopherium/framework/dbkit/sqlite"
	"github.com/gopherium/framework/dbkit/sqlite/internal/seam"
	"github.com/gopherium/framework/dbkit/sqlite/sqlitetest"
)

const (
	// rebuildTable is the version table of the rebuild tests.
	rebuildTable = "rebuild_versions"
	// rebuildWait is a bound on each wait of the rebuild tests that no passing run reaches.
	rebuildWait = 20 * time.Second
	// rebuildSchema creates a parent table with a text price and an index, a child table, and their rows.
	rebuildSchema = "CREATE TABLE rebuild_items (id INTEGER PRIMARY KEY, price TEXT NOT NULL);\n" +
		"CREATE INDEX rebuild_items_price_idx ON rebuild_items (price);\n" +
		"CREATE TABLE rebuild_lines (id INTEGER PRIMARY KEY, item_id INTEGER NOT NULL REFERENCES rebuild_items (id));\n" +
		"INSERT INTO rebuild_items VALUES (1, '2.50'), (2, '7.25');\n" +
		"INSERT INTO rebuild_lines VALUES (1, 1), (2, 2), (3, 2);"
	// rebuildPriceType reads the declared type of the price column of rebuild_items.
	rebuildPriceType = "SELECT type FROM pragma_table_info('rebuild_items') WHERE name = 'price'"
	// rebuildItems reads every item as its id, the type of its price and its price.
	rebuildItems = "SELECT id || ':' || typeof(price) || ':' || price FROM rebuild_items ORDER BY id"
	// rebuildBefore is what rebuildItems reads before the rebuild.
	rebuildBefore = "1:text:2.50\n2:text:7.25"
	// rebuildApplied is what rebuildItems reads once the rebuild turned every price into integer cents.
	rebuildApplied = "1:integer:250\n2:integer:725"
	// rebuildPanic is the value the panicking steps of the rebuild tests panic with.
	rebuildPanic = "rebuild test: the steps panicked on purpose"
	// rebuildIndex counts the index on the price of rebuild_items.
	rebuildIndex = "SELECT count(*) FROM sqlite_schema WHERE type = 'index' AND name = 'rebuild_items_price_idx'"
	// rebuildCancels is how many rebuilds on one handle a cancel inside the steps ends.
	rebuildCancels = 300
)

var (
	// rebuildSteps are the statements of a 12-step rebuild that turns the text price of rebuild_items into cents.
	rebuildSteps = []string{
		"CREATE TABLE rebuild_items_new (id INTEGER PRIMARY KEY, price INTEGER NOT NULL CHECK (price >= 0))",
		"INSERT INTO rebuild_items_new (id, price) SELECT id, CAST(round(price * 100) AS INTEGER) FROM rebuild_items",
		"DROP TABLE rebuild_items",
		"ALTER TABLE rebuild_items_new RENAME TO rebuild_items",
		"CREATE INDEX rebuild_items_price_idx ON rebuild_items (price)",
	}
	// rebuildRestoreFailures are the messages of each way a restore of foreign keys fails.
	rebuildRestoreFailures = []string{"switch foreign keys back on", "read foreign keys back", "foreign keys read back as"}
	// errRebuildSteps is the error the failing steps of the rebuild tests answer with.
	errRebuildSteps = errors.New("rebuild test: the steps failed on purpose")
	// errRebuildDone is the error the failing done check of the rebuild tests answers with.
	errRebuildDone = errors.New("rebuild test: the done check failed on purpose")
	// errRebuildInsert is the error goose's version insert answers with after the rebuild committed.
	errRebuildInsert = errors.New("rebuild test: the version insert failed on purpose")
	// errRebuildRestore is the error a faulty restore of foreign keys answers with.
	errRebuildRestore = errors.New("rebuild test: the restore failed on purpose")
	// errRebuildFault is the error a chosen statement or commit of a rebuild answers with.
	errRebuildFault = errors.New("rebuild test: the statement failed on purpose")
)

// rebuildPath returns the path of a database file in a fresh folder with every link resolved.
func rebuildPath(t *testing.T) string {
	t.Helper()
	folder, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatalf("EvalSymlinks() error = %v, want nil", err)
	}
	return filepath.Join(folder, "site.db")
}

// rebuildOpenAt returns a handle with opts on the file at path, migrated to the first version of the rebuild tests.
func rebuildOpenAt(t *testing.T, path string, opts sqlite.Options) *sql.DB {
	t.Helper()
	db := mustOpen(t, "sqlite:"+path, opts)
	migrateMust(t, db, rebuildFirst())
	return db
}

// rebuildOpen returns a handle with opts on a fresh file migrated to the first version of the rebuild tests.
func rebuildOpen(t *testing.T, opts sqlite.Options) *sql.DB {
	t.Helper()
	return rebuildOpenAt(t, rebuildPath(t), opts)
}

// rebuildFirst returns the migrations of the rebuild tests at their first version, a SQL file of rebuildSchema.
func rebuildFirst() sqlite.Migrations {
	return sqlite.Migrations{
		Table:    rebuildTable,
		FS:       fstest.MapFS{"00001_rebuild_items.sql": migrateFile(rebuildSchema)},
		LockWait: migrateLockWait,
		LockPoll: migrateLockPoll,
	}
}

// rebuildSecond returns rebuildFirst with a second version, a Go migration that calls Rebuild with done and steps.
func rebuildSecond(done func(context.Context, *sql.Tx) (bool, error),
	steps func(context.Context, *sql.Tx) error) sqlite.Migrations {
	m := rebuildFirst()
	m.Go = []*goose.Migration{goose.NewGoMigration(2, &goose.GoFunc{RunDB: func(ctx context.Context, db *sql.DB) error {
		return sqlite.Rebuild(ctx, db, done, steps)
	}}, nil)}
	return m
}

// rebuildDone reports whether the price of rebuild_items already holds integer cents.
func rebuildDone(ctx context.Context, tx *sql.Tx) (bool, error) {
	var kind string
	err := tx.QueryRowContext(ctx, rebuildPriceType).Scan(&kind)
	return kind == "INTEGER", err
}

// rebuildRun returns steps that run each of statements in order.
func rebuildRun(statements ...string) func(context.Context, *sql.Tx) error {
	return func(ctx context.Context, tx *sql.Tx) error {
		for _, statement := range statements {
			if _, err := tx.ExecContext(ctx, statement); err != nil {
				return err
			}
		}
		return nil
	}
}

// rebuildThen returns steps that run rebuildSteps and then answer what last answers.
func rebuildThen(last func(ctx context.Context) error) func(context.Context, *sql.Tx) error {
	steps := rebuildRun(rebuildSteps...)
	return func(ctx context.Context, tx *sql.Tx) error {
		if err := steps(ctx, tx); err != nil {
			return err
		}
		return last(ctx)
	}
}

// rebuildText returns the one text column of every row of query on db, one row per line.
func rebuildText(t *testing.T, db *sql.DB, query string) string {
	t.Helper()
	rows, err := db.QueryContext(t.Context(), query)
	if err != nil {
		t.Fatalf("QueryContext(%q) error = %v, want nil", query, err)
	}
	defer func() { _ = rows.Close() }()
	var lines []string
	for rows.Next() {
		var line string
		if err := rows.Scan(&line); err != nil {
			t.Fatalf("Scan() of %q error = %v, want nil", query, err)
		}
		lines = append(lines, line)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("Err() of %q = %v, want nil", query, err)
	}
	return strings.Join(lines, "\n")
}

// rebuildMustHold fails the test unless rebuildItems on db reads want.
func rebuildMustHold(t *testing.T, db *sql.DB, want string) {
	t.Helper()
	if got := rebuildText(t, db, rebuildItems); got != want {
		t.Errorf("rebuild_items = %q, want %q", got, want)
	}
}

// rebuildState returns the schema of db outside the optimizer's statistics and every row of both tables as text.
func rebuildState(t *testing.T, db *sql.DB) string {
	t.Helper()
	return strings.Join([]string{
		rebuildText(t, db, "SELECT type || ' ' || name || ' ' || coalesce(sql, '') FROM sqlite_schema "+
			"WHERE name NOT LIKE 'sqlite_stat%' ORDER BY name"),
		rebuildText(t, db, rebuildItems),
		rebuildText(t, db, "SELECT id || ':' || item_id FROM rebuild_lines ORDER BY id"),
	}, "\n")
}

// rebuildMustBeOn fails the test unless every connection the pool of db can hold reads foreign keys as on.
func rebuildMustBeOn(t *testing.T, db *sql.DB) {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), rebuildWait)
	defer cancel()
	for i := range db.Stats().MaxOpenConnections {
		conn, err := db.Conn(ctx)
		if err != nil {
			t.Fatalf("Conn() %d error = %v, want every connection of the pool", i, err)
		}
		defer func() { _ = conn.Close() }()
		var on int64
		if err := conn.QueryRowContext(ctx, "PRAGMA foreign_keys").Scan(&on); err != nil {
			t.Fatalf("PRAGMA foreign_keys on connection %d error = %v, want nil", i, err)
		}
		if on != 1 {
			t.Errorf("PRAGMA foreign_keys on connection %d = %d, want 1", i, on)
		}
	}
}

// rebuildMustFail fails the test unless err holds want, or is nil when want is empty, and names no failed restore.
func rebuildMustFail(t *testing.T, err error, want string) {
	t.Helper()
	switch {
	case want == "" && err != nil:
		t.Errorf("Migrate() error = %v, want nil", err)
	case want != "" && (err == nil || !strings.Contains(err.Error(), want)):
		t.Errorf("Migrate() error = %v, want an error holding %q", err, want)
	case err != nil && slices.ContainsFunc(rebuildRestoreFailures, func(message string) bool {
		return strings.Contains(err.Error(), message)
	}):
		t.Errorf("Migrate() error = %v, want the restore of foreign keys never failed", err)
	}
}

func TestRebuildRunsAtTheSmallestCap(t *testing.T) {
	t.Parallel()

	opts := testOptions()
	opts.MaxConns = 2
	db := rebuildOpen(t, opts)
	ctx, cancel := context.WithTimeout(t.Context(), rebuildWait)
	defer cancel()

	err := sqlite.Migrate(ctx, db, rebuildSecond(rebuildDone, rebuildRun(rebuildSteps...)))

	if err != nil {
		t.Fatalf("Migrate() error = %v, want the rebuild done at a cap of 2", err)
	}
	rebuildMustHold(t, db, rebuildApplied)
	migrateMustVersions(t, db, rebuildTable, 0, 1, 2)
	rebuildMustBeOn(t, db)
}

// rebuildDefensive returns an error unless defensive mode refuses an edit of the schema table inside tx.
func rebuildDefensive(ctx context.Context, tx *sql.Tx) error {
	if _, err := tx.ExecContext(ctx, "PRAGMA writable_schema=ON"); err != nil {
		return err
	}
	_, edit := tx.ExecContext(ctx, "UPDATE sqlite_schema SET sql = sql WHERE name = 'rebuild_items'")
	if _, err := tx.ExecContext(ctx, "PRAGMA writable_schema=OFF"); err != nil {
		return err
	}
	if edit == nil || !strings.Contains(edit.Error(), "may not be modified") {
		return fmt.Errorf("rebuild test: an edit of the schema table answered %v, want it refused", edit)
	}
	return nil
}

func TestRebuildRunsWithDefensiveModeOn(t *testing.T) {
	t.Parallel()

	db := rebuildOpen(t, testOptions())
	steps := rebuildRun(rebuildSteps...)
	defensive := func(ctx context.Context, tx *sql.Tx) error {
		if err := rebuildDefensive(ctx, tx); err != nil {
			return err
		}
		return steps(ctx, tx)
	}

	migrateMust(t, db, rebuildSecond(rebuildDone, defensive))

	rebuildMustHold(t, db, rebuildApplied)
	if got := rebuildText(t, db, rebuildIndex); got != "1" {
		t.Errorf("indexes named rebuild_items_price_idx = %s, want the index made again", got)
	}
	if got := rebuildText(t, db, "PRAGMA integrity_check"); got != "ok" {
		t.Errorf("PRAGMA integrity_check = %q, want ok", got)
	}
}

func TestRebuildLeavesForeignKeysOn(t *testing.T) {
	t.Parallel()

	steps := func(context.CancelFunc) func(context.Context, *sql.Tx) error { return rebuildRun(rebuildSteps...) }
	cases := []struct {
		name     string
		done     func(context.Context, *sql.Tx) (bool, error)
		steps    func(cancel context.CancelFunc) func(context.Context, *sql.Tx) error
		want     string
		items    string
		versions []int64
	}{
		{"the success path", rebuildDone, steps, "", rebuildApplied, []int64{0, 1, 2}},
		{"an error from the steps", rebuildDone, func(context.CancelFunc) func(context.Context, *sql.Tx) error {
			return rebuildThen(func(context.Context) error { return errRebuildSteps })
		}, "dbkit: run the rebuild steps: " + errRebuildSteps.Error(), rebuildBefore, []int64{0, 1}},
		{"an error from done", func(context.Context, *sql.Tx) (bool, error) {
			return false, errRebuildDone
		}, steps, "dbkit: check whether the rebuild already ran: " + errRebuildDone.Error(), rebuildBefore,
			[]int64{0, 1}},
		{"a panic in the steps", rebuildDone, func(context.CancelFunc) func(context.Context, *sql.Tx) error {
			return rebuildThen(func(context.Context) error { panic(rebuildPanic) })
		}, "panic: " + rebuildPanic, rebuildBefore, []int64{0, 1}},
		{"a cancelled context", rebuildDone, func(cancel context.CancelFunc) func(context.Context, *sql.Tx) error {
			return rebuildThen(func(ctx context.Context) error {
				cancel()
				return ctx.Err()
			})
		}, "dbkit: run the rebuild steps: context canceled", rebuildBefore, []int64{0, 1}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			db := rebuildOpen(t, testOptions())
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()

			err := sqlite.Migrate(ctx, db, rebuildSecond(c.done, c.steps(cancel)))

			rebuildMustFail(t, err, c.want)
			rebuildMustHold(t, db, c.items)
			migrateMustVersions(t, db, rebuildTable, c.versions...)
			rebuildMustBeOn(t, db)
		})
	}
}

// rebuildNext returns the driver connection the pool of db hands out next and what it reads for its foreign keys.
func rebuildNext(t *testing.T, db *sql.DB) (any, int64) {
	t.Helper()
	conn, err := db.Conn(t.Context())
	if err != nil {
		t.Fatalf("Conn() error = %v, want nil", err)
	}
	defer func() { _ = conn.Close() }()
	var raw any
	if err := conn.Raw(func(driverConn any) error {
		raw = driverConn
		return nil
	}); err != nil {
		t.Fatalf("Raw() error = %v, want nil", err)
	}
	var on int64
	if err := conn.QueryRowContext(t.Context(), "PRAGMA foreign_keys").Scan(&on); err != nil {
		t.Fatalf("PRAGMA foreign_keys error = %v, want nil", err)
	}
	return raw, on
}

func TestRebuildKeepsItsConnectionAfterACancel(t *testing.T) {
	t.Parallel()

	db := rebuildOpen(t, testOptions())
	want := "dbkit: run the rebuild steps: context canceled"

	for round := range rebuildCancels {
		before, _ := rebuildNext(t, db)
		open := db.Stats().OpenConnections
		ctx, cancel := context.WithCancel(t.Context())
		err := sqlite.Rebuild(ctx, db, rebuildDone, rebuildThen(func(ctx context.Context) error {
			cancel()
			return ctx.Err()
		}))
		cancel()

		if got := db.Stats().OpenConnections; got != open {
			t.Fatalf("Stats().OpenConnections after round %d = %d, want %d with no connection closed", round, got, open)
		}
		after, on := rebuildNext(t, db)
		if after != before || on != 1 {
			t.Fatalf("the pool after round %d hands out the rebuild's connection %t with foreign keys %d, want true and 1",
				round, after == before, on)
		}
		if err == nil || err.Error() != want {
			t.Fatalf("Rebuild() in round %d error = %v, want %q", round, err, want)
		}
	}
	rebuildMustHold(t, db, rebuildBefore)
}

func TestRebuildFindsAnOrphan(t *testing.T) {
	t.Parallel()

	db := rebuildOpen(t, testOptions())
	before := rebuildState(t, db)
	orphan := rebuildRun(append(slices.Clone(rebuildSteps), "INSERT INTO rebuild_lines VALUES (4, 3)")...)

	err := sqlite.Migrate(t.Context(), db, rebuildSecond(rebuildDone, orphan))

	want := "dbkit: PRAGMA foreign_key_check found a row of rebuild_lines with no parent row in rebuild_items"
	rebuildMustFail(t, err, want)
	if after := rebuildState(t, db); after != before {
		t.Errorf("the database after the rebuild =\n%s\nwant it unchanged =\n%s", after, before)
	}
	migrateMustVersions(t, db, rebuildTable, 0, 1)
	rebuildMustBeOn(t, db)
}

func TestRebuildNamesTheStepThatFailed(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		fail func(*sqlitetest.Faults)
		want string
	}{
		{"switching foreign keys off", func(f *sqlitetest.Faults) {
			f.FailStatement("PRAGMA foreign_keys=OFF", errRebuildFault)
		}, "dbkit: switch foreign keys off for the rebuild: "},
		{"the foreign key check", func(f *sqlitetest.Faults) {
			f.FailStatement("PRAGMA foreign_key_check", errRebuildFault)
		}, "dbkit: check the foreign keys after the rebuild: "},
		{"the commit", func(f *sqlitetest.Faults) { f.FailNextCommit(errRebuildFault) }, "dbkit: commit the rebuild: "},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			faults := &sqlitetest.Faults{}
			db := sqlitetest.OpenWithFaults(t, testOptions(), faults)
			migrateMust(t, db, rebuildFirst())
			c.fail(faults)

			err := sqlite.Migrate(t.Context(), db, rebuildSecond(rebuildDone, rebuildRun(rebuildSteps...)))

			if want := c.want + errRebuildFault.Error(); !errors.Is(err, errRebuildFault) ||
				!strings.Contains(err.Error(), want) {
				t.Errorf("Migrate() error = %v, want an error holding %q", err, want)
			}
			rebuildMustHold(t, db, rebuildBefore)
			migrateMustVersions(t, db, rebuildTable, 0, 1)
			rebuildMustBeOn(t, db)
		})
	}
}

func TestRebuildAnswersBusyWhileAnotherWriterHoldsTheLock(t *testing.T) {
	t.Parallel()

	opts := testOptions()
	opts.BusyTimeout = rulesShortBusyTimeout
	db := rebuildOpen(t, opts)
	writer, err := db.BeginTx(t.Context(), nil)
	if err != nil {
		t.Fatalf("BeginTx() error = %v, want nil", err)
	}
	if _, err := writer.ExecContext(t.Context(), "INSERT INTO rebuild_lines VALUES (4, 1)"); err != nil {
		t.Fatalf("an insert of the writer error = %v, want nil", err)
	}

	err = sqlite.Migrate(t.Context(), db, rebuildSecond(rebuildDone, rebuildRun(rebuildSteps...)))

	if rollbackErr := writer.Rollback(); rollbackErr != nil {
		t.Fatalf("Rollback() error = %v, want nil", rollbackErr)
	}
	prefix := "dbkit: begin the rebuild: "
	if !errors.Is(sqlite.Classify(err), dbkit.ErrBusy) || !strings.Contains(err.Error(), prefix) {
		t.Errorf("Migrate() error = %v, want the busy class after %q", err, prefix)
	}
	rebuildMustHold(t, db, rebuildBefore)
	migrateMustVersions(t, db, rebuildTable, 0, 1)
	rebuildMustBeOn(t, db)
}

func TestRebuildRefusesAMissingArgument(t *testing.T) {
	t.Parallel()

	db := rebuildOpen(t, testOptions())
	steps := rebuildRun(rebuildSteps...)
	cases := []struct {
		name  string
		db    *sql.DB
		done  func(context.Context, *sql.Tx) (bool, error)
		steps func(context.Context, *sql.Tx) error
		want  string
	}{
		{"a nil handle", nil, rebuildDone, steps, "dbkit: Rebuild needs a database handle, got nil"},
		{"a nil done check", db, nil, steps, "dbkit: Rebuild needs a done check, got nil"},
		{"nil steps", db, rebuildDone, nil, "dbkit: Rebuild needs its steps, got nil"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()

			err := sqlite.Rebuild(t.Context(), c.db, c.done, c.steps)

			if err == nil || err.Error() != c.want {
				t.Errorf("Rebuild() error = %v, want %q", err, c.want)
			}
			rebuildMustHold(t, db, rebuildBefore)
		})
	}
}

func TestRebuildTakesItsConnectionOnItsContext(t *testing.T) {
	t.Parallel()

	db := rebuildOpen(t, testOptions())
	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	err := sqlite.Rebuild(ctx, db, rebuildDone, rebuildRun(rebuildSteps...))

	if want := "dbkit: take a connection for the rebuild: context canceled"; !errors.Is(err, context.Canceled) ||
		err.Error() != want {
		t.Errorf("Rebuild() error = %v, want %q", err, want)
	}
	rebuildMustHold(t, db, rebuildBefore)
}

func TestRebuildRunsTwiceSafely(t *testing.T) {
	t.Parallel()

	faults := &sqlitetest.Faults{}
	db := sqlitetest.OpenWithFaults(t, testOptions(), faults)
	migrateMust(t, db, rebuildFirst())
	var answers []bool
	done := func(ctx context.Context, tx *sql.Tx) (bool, error) {
		inPlace, err := rebuildDone(ctx, tx)
		answers = append(answers, inPlace)
		return inPlace, err
	}
	m := rebuildSecond(done, rebuildRun(rebuildSteps...))
	insert := "INSERT INTO " + rebuildTable + " (version_id, is_applied) VALUES (?, ?)"
	faults.FailStatement(insert, errRebuildInsert)
	if err := sqlite.Migrate(t.Context(), db, m); !errors.Is(err, errRebuildInsert) {
		t.Fatalf("Migrate() error = %v, want the version insert to fail after the rebuild committed", err)
	}
	rebuildMustHold(t, db, rebuildApplied)
	migrateMustVersions(t, db, rebuildTable, 0, 1)
	faults.Pass(insert)

	migrateMust(t, db, m)

	rebuildMustHold(t, db, rebuildApplied)
	migrateMustVersions(t, db, rebuildTable, 0, 1, 2)
	if !slices.Equal(answers, []bool{false, true}) {
		t.Errorf("done answered %v, want false and then true", answers)
	}
	rebuildMustBeOn(t, db)
}

// rebuildDriverConn is every interface the driver's connection offers to database/sql.
type rebuildDriverConn interface {
	driver.Conn
	driver.ConnBeginTx
	driver.ConnPrepareContext
	driver.ExecerContext
	driver.QueryerContext
	driver.Pinger
	driver.SessionResetter
	driver.Validator
}

// rebuildRestoreFault is a statement of the restore of foreign keys that goes wrong after they went off.
type rebuildRestoreFault struct {
	// query is the exact text of the statement that goes wrong.
	query string
	// err is the error the statement answers with, and nil skips the statement with no error.
	err error
	// discarded counts the closes of connections that switched foreign keys off.
	discarded atomic.Int32
}

// rebuildFaultyConn is a driver connection whose restore of foreign keys goes wrong once it switched them off.
type rebuildFaultyConn struct {
	// rebuildDriverConn is the driver's own connection.
	rebuildDriverConn
	// fault is the statement that goes wrong.
	fault *rebuildRestoreFault
	// off reports that the connection switched foreign keys off.
	off bool
}

// ExecContext runs query, or answers the fault for it once foreign keys went off.
func (c *rebuildFaultyConn) ExecContext(ctx context.Context, query string,
	args []driver.NamedValue) (driver.Result, error) {
	if query == "PRAGMA foreign_keys=OFF" {
		c.off = true
	}
	if c.off && query == c.fault.query {
		return driver.RowsAffected(0), c.fault.err
	}
	return c.rebuildDriverConn.ExecContext(ctx, query, args)
}

// QueryContext runs query, or answers the fault for it once foreign keys went off.
func (c *rebuildFaultyConn) QueryContext(ctx context.Context, query string,
	args []driver.NamedValue) (driver.Rows, error) {
	if c.off && query == c.fault.query {
		return nil, c.fault.err
	}
	return c.rebuildDriverConn.QueryContext(ctx, query, args)
}

// Close counts the close of a connection that switched foreign keys off and closes it.
func (c *rebuildFaultyConn) Close() error {
	if c.off {
		c.fault.discarded.Add(1)
	}
	return c.rebuildDriverConn.Close()
}

func TestRebuildDiscardsAConnectionItCannotRestore(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name  string
		query string
		err   error
		want  string
	}{
		{"a restore that fails", "PRAGMA foreign_keys=ON", errRebuildRestore,
			"dbkit: switch foreign keys back on after the rebuild: " + errRebuildRestore.Error()},
		{"a read back that fails", "PRAGMA foreign_keys", errRebuildRestore,
			"dbkit: read foreign keys back after the rebuild: " + errRebuildRestore.Error()},
		{"a restore that never runs", "PRAGMA foreign_keys=ON", nil,
			"dbkit: foreign keys read back as 0 after the rebuild, want 1"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			path := rebuildPath(t)
			fault := &rebuildRestoreFault{query: c.query, err: c.err}
			seam.Set(path, func(conn driver.Conn) driver.Conn {
				return &rebuildFaultyConn{rebuildDriverConn: conn.(rebuildDriverConn), fault: fault}
			})
			t.Cleanup(func() { seam.Clear(path) })
			db := rebuildOpenAt(t, path, testOptions())

			err := sqlite.Migrate(t.Context(), db, rebuildSecond(rebuildDone, rebuildRun(rebuildSteps...)))

			if err == nil || !strings.Contains(err.Error(), c.want) {
				t.Errorf("Migrate() error = %v, want an error holding %q", err, c.want)
			}
			if got := fault.discarded.Load(); got != 1 {
				t.Errorf("closed connections with foreign keys switched off = %d, want the rebuild's one", got)
			}
			rebuildMustHold(t, db, rebuildApplied)
			migrateMustVersions(t, db, rebuildTable, 0, 1)
			rebuildMustBeOn(t, db)
		})
	}
}
