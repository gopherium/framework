// SPDX-License-Identifier: Apache-2.0

package sqlite_test

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	modernc "modernc.org/sqlite"

	"github.com/gopherium/framework/dbkit"
	"github.com/gopherium/framework/dbkit/sqlite"
	"github.com/gopherium/framework/dbkit/sqlite/sqlitetest"
)

const (
	// snapshotSeedRows is how many rows a seeded database starts with.
	snapshotSeedRows = 2000
	// snapshotWriterCount is how many writers write while a snapshot runs.
	snapshotWriterCount = 2
	// snapshotShortBusyTimeout is a busy timeout a busy checkpoint waits out on purpose.
	snapshotShortBusyTimeout = 25 * time.Millisecond
	// snapshotHeldWriterBusyTimeout is a busy timeout a snapshot that waits for a held write lock runs out of.
	snapshotHeldWriterBusyTimeout = 500 * time.Millisecond
	// snapshotOwnerOnlyMode is the mode of a live file only its owner reads and writes.
	snapshotOwnerOnlyMode fs.FileMode = 0o600
	// snapshotChildSource names the environment switch that makes the test binary snapshot the database at its value.
	snapshotChildSource = "DBKIT_TEST_SNAPSHOT_SOURCE"
	// snapshotChildTarget names the environment variable that holds the target of a snapshot child.
	snapshotChildTarget = "DBKIT_TEST_SNAPSHOT_TARGET"
	// snapshotChildStage names the environment variable that holds the stage a snapshot child holds in.
	snapshotChildStage = "DBKIT_TEST_SNAPSHOT_STAGE"
	// snapshotStageCopy is the stage a snapshot child holds in while VACUUM INTO writes its copy.
	snapshotStageCopy = "copy"
	// snapshotStageCheck is the stage a snapshot child holds in while PRAGMA quick_check reads its copy.
	snapshotStageCheck = "check"
	// snapshotHolding is the line a snapshot child prints once it holds.
	snapshotHolding = "dbkit test: the snapshot child holds"
)

// snapshotPass answers 1 for every call of snapshot_gate.
func snapshotPass([]driver.Value) (driver.Value, error) {
	return int64(1), nil
}

// snapshotGate is a SQL function that holds its first call made while armed until the gate opens or the test ends.
type snapshotGate struct {
	// armed reports that the next call is held.
	armed atomic.Bool
	// reached is closed once a held call began.
	reached chan struct{}
	// opened is closed once the gate opens.
	opened chan struct{}
	// ended is closed once the test ends.
	ended <-chan struct{}
	// hold holds the first armed call once.
	hold sync.Once
	// open closes opened once.
	open sync.Once
}

// snapshotNewGate returns a closed gate, which no longer holds a call once the test ends.
func snapshotNewGate(t *testing.T) *snapshotGate {
	t.Helper()
	return &snapshotGate{reached: make(chan struct{}), opened: make(chan struct{}), ended: t.Context().Done()}
}

// call answers 1, and holds the first armed call until the gate opens or the test ends.
func (g *snapshotGate) call([]driver.Value) (driver.Value, error) {
	if g.armed.Load() {
		g.hold.Do(func() {
			close(g.reached)
			select {
			case <-g.opened:
			case <-g.ended:
			}
		})
	}
	return int64(1), nil
}

// release opens the gate.
func (g *snapshotGate) release() {
	g.open.Do(func() { close(g.opened) })
}

// snapshotFunctions returns a function list holding snapshot_gate, which answers through call.
func snapshotFunctions(t *testing.T, call func([]driver.Value) (driver.Value, error)) *dbkit.FunctionList {
	t.Helper()
	list, err := dbkit.NewFunctionList(dbkit.Function{Name: "snapshot_gate", Args: 1, Deterministic: true, Call: call})
	if err != nil {
		t.Fatalf("NewFunctionList() error = %v, want nil", err)
	}
	return list
}

// snapshotFolder returns a fresh folder with every link resolved.
func snapshotFolder(t *testing.T) string {
	t.Helper()
	folder, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatalf("EvalSymlinks() error = %v, want nil", err)
	}
	return folder
}

// snapshotOpen returns a handle with opts on a fresh file, and the file's path.
func snapshotOpen(t *testing.T, opts sqlite.Options) (*sql.DB, string) {
	t.Helper()
	path := filepath.Join(snapshotFolder(t), "site.db")
	return mustOpen(t, "sqlite:"+path, opts), path
}

// snapshotSeed creates the tables of the snapshot tests on db and fills them with snapshotSeedRows rows.
func snapshotSeed(t *testing.T, db *sql.DB) {
	t.Helper()
	for _, query := range []string{
		"CREATE TABLE snapshot_rows (id INTEGER PRIMARY KEY, body BLOB NOT NULL)",
		"CREATE TABLE snapshot_count (n INTEGER NOT NULL)",
		"CREATE TABLE snapshot_gates (a INTEGER NOT NULL CHECK (snapshot_gate(a)))",
		"INSERT INTO snapshot_gates VALUES (1)",
		"WITH RECURSIVE seq(i) AS (SELECT 1 UNION ALL SELECT i + 1 FROM seq WHERE i < " +
			strconv.Itoa(snapshotSeedRows) + ") INSERT INTO snapshot_rows (body) SELECT randomblob(1024) FROM seq",
		"INSERT INTO snapshot_count SELECT count(*) FROM snapshot_rows",
	} {
		rulesExec(t, db, query)
	}
}

// snapshotWrite adds one row and counts it in one transaction.
func snapshotWrite(ctx context.Context, db *sql.DB) error {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, "INSERT INTO snapshot_rows (body) VALUES (randomblob(64))"); err != nil {
		return errors.Join(err, tx.Rollback())
	}
	if _, err := tx.ExecContext(ctx, "UPDATE snapshot_count SET n = n + 1"); err != nil {
		return errors.Join(err, tx.Rollback())
	}
	return tx.Commit()
}

// snapshotWriters are goroutines that write to one database until they are stopped.
type snapshotWriters struct {
	// committed counts the transactions the writers committed.
	committed atomic.Int64
	// first is closed once a writer committed.
	first chan struct{}
	// firstOnce closes first once.
	firstOnce sync.Once
	// stopped is closed to stop the writers.
	stopped chan struct{}
	// stopOnce closes stopped once.
	stopOnce sync.Once
	// wg waits for the writers.
	wg sync.WaitGroup
	// mu guards err.
	mu sync.Mutex
	// err is the first error a writer met.
	err error
}

// snapshotStartWriters starts snapshotWriterCount writers on db and returns once one of them committed.
func snapshotStartWriters(t *testing.T, db *sql.DB) *snapshotWriters {
	t.Helper()
	w := &snapshotWriters{first: make(chan struct{}), stopped: make(chan struct{})}
	for range snapshotWriterCount {
		w.wg.Go(func() { w.write(db) })
	}
	t.Cleanup(func() { _ = w.stop() })
	select {
	case <-w.first:
	case <-w.ended():
		t.Fatalf("the writers ended with %v before any commit", w.stop())
	}
	return w
}

// write commits one transaction after another on db until the writers stop or a write fails.
func (w *snapshotWriters) write(db *sql.DB) {
	for {
		select {
		case <-w.stopped:
			return
		default:
		}
		if err := snapshotWrite(context.Background(), db); err != nil {
			w.mu.Lock()
			w.err = errors.Join(w.err, err)
			w.mu.Unlock()
			return
		}
		w.committed.Add(1)
		w.firstOnce.Do(func() { close(w.first) })
	}
}

// ended returns a channel closed once every writer returned.
func (w *snapshotWriters) ended() <-chan struct{} {
	ended := make(chan struct{})
	go func() {
		w.wg.Wait()
		close(ended)
	}()
	return ended
}

// stop stops the writers, waits for them and returns the first error they met.
func (w *snapshotWriters) stop() error {
	w.stopOnce.Do(func() { close(w.stopped) })
	w.wg.Wait()
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.err
}

// snapshotResult is what one call of Snapshot returned.
type snapshotResult struct {
	// snapshot is the snapshot Snapshot returned.
	snapshot dbkit.Snapshot
	// err is the error Snapshot returned.
	err error
}

// snapshotStart runs Snapshot of target on db in a goroutine, which the end of the test waits for.
func snapshotStart(t *testing.T, db *sql.DB, target string) <-chan snapshotResult {
	t.Helper()
	result := make(chan snapshotResult, 1)
	done := make(chan struct{})
	go func() {
		defer close(done)
		snapshot, err := sqlite.NewSnapshotter(db).Snapshot(context.Background(), target)
		result <- snapshotResult{snapshot: snapshot, err: err}
	}()
	t.Cleanup(func() { <-done })
	return result
}

// snapshotOne returns one value of query on db and fails the test when it cannot.
func snapshotOne[T any](t *testing.T, db *sql.DB, query string) T {
	t.Helper()
	var value T
	if err := db.QueryRowContext(t.Context(), query).Scan(&value); err != nil {
		t.Fatalf("QueryRowContext(%q) error = %v, want nil", query, err)
	}
	return value
}

// snapshotStat returns the facts of the file at path and fails the test when it cannot.
func snapshotStat(t *testing.T, path string) fs.FileInfo {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("Stat(%s) error = %v, want the file present", path, err)
	}
	return info
}

// snapshotSize returns the size of the file at path and fails the test when it cannot.
func snapshotSize(t *testing.T, path string) int64 {
	t.Helper()
	return snapshotStat(t, path).Size()
}

// snapshotMustKeep fails the test unless path still names the file before describes.
func snapshotMustKeep(t *testing.T, path string, before fs.FileInfo) {
	t.Helper()
	if after, err := os.Stat(path); err != nil || !os.SameFile(before, after) {
		t.Errorf("Stat(%s) after the snapshot error = %v, want the live database file kept", path, err)
	}
}

// snapshotOutput is the output of a snapshot child, which reports when the child prints snapshotHolding.
type snapshotOutput struct {
	// mu guards text.
	mu sync.Mutex
	// text is everything the child printed.
	text strings.Builder
	// holding is closed once text holds the snapshotHolding line.
	holding chan struct{}
	// once closes holding once.
	once sync.Once
}

// Write keeps p and closes holding once the output holds the snapshotHolding line.
func (o *snapshotOutput) Write(p []byte) (int, error) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.text.Write(p)
	if strings.Contains(o.text.String(), snapshotHolding+"\n") {
		o.once.Do(func() { close(o.holding) })
	}
	return len(p), nil
}

// String returns everything the child printed.
func (o *snapshotOutput) String() string {
	o.mu.Lock()
	defer o.mu.Unlock()
	return o.text.String()
}

// snapshotChild is a run of the test binary that snapshots one database and holds inside one stage of the snapshot.
type snapshotChild struct {
	// cmd is the running test binary.
	cmd *exec.Cmd
	// stdin is the write end of the child's standard input.
	stdin io.WriteCloser
	// output is what the child printed.
	output *snapshotOutput
	// ended is closed once the child was reaped.
	ended chan struct{}
	// err is how the child ended, read once ended is closed.
	err error
}

// snapshotStartChild starts a child that snapshots source to target and returns once it holds in stage.
func snapshotStartChild(t *testing.T, source, target, stage string) *snapshotChild {
	t.Helper()
	output := &snapshotOutput{holding: make(chan struct{})}
	cmd := exec.Command(os.Args[0], "-test.run=^TestSnapshotChildProcess$")
	cmd.Env = append(os.Environ(), snapshotChildSource+"="+source, snapshotChildTarget+"="+target,
		snapshotChildStage+"="+stage)
	cmd.Stdout, cmd.Stderr = output, output
	stdin, err := cmd.StdinPipe()
	if err != nil {
		t.Fatalf("StdinPipe() error = %v, want nil", err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatalf("Start() error = %v, want nil", err)
	}
	c := &snapshotChild{cmd: cmd, stdin: stdin, output: output, ended: make(chan struct{})}
	go func() {
		c.err = cmd.Wait()
		close(c.ended)
	}()
	t.Cleanup(func() { _ = c.kill() })
	select {
	case <-output.holding:
	case <-c.ended:
		t.Fatalf("the snapshot child ended with %v before it held in the stage %s\n%s", c.err, stage, output)
	}
	return c
}

// snapshotHoldChanges makes every connection db opens from now on run hold before each row it changes outside main.
func snapshotHoldChanges(db *sql.DB, hold func()) {
	db.Driver().(*modernc.Driver).RegisterConnectionHook(func(conn modernc.ExecQuerierContext, _ string) error {
		conn.(modernc.HookRegisterer).RegisterPreUpdateHook(func(change modernc.SQLitePreUpdateData) {
			if change.DatabaseName != "main" {
				hold()
			}
		})
		return nil
	})
}

// kill ends the child with SIGKILL and returns how it ended.
func (c *snapshotChild) kill() error {
	_ = c.cmd.Process.Signal(syscall.SIGKILL)
	<-c.ended
	return c.err
}

// snapshotMustBeKilled fails the test unless err reports an end by SIGKILL.
func snapshotMustBeKilled(t *testing.T, err error) {
	t.Helper()
	var exit *exec.ExitError
	if !errors.As(err, &exit) {
		t.Fatalf("the snapshot child ended with %v, want SIGKILL", err)
	}
	if status, ok := exit.Sys().(syscall.WaitStatus); !ok || status.Signal() != syscall.SIGKILL {
		t.Fatalf("the snapshot child ended with %v, want SIGKILL", exit)
	}
}

// snapshotMustRefuse fails the test unless a snapshot of db to target answers no snapshot and the error want.
func snapshotMustRefuse(t *testing.T, db *sql.DB, target, want string) {
	t.Helper()
	got, err := sqlite.NewSnapshotter(db).Snapshot(t.Context(), target)
	if err == nil || err.Error() != want || got != (dbkit.Snapshot{}) {
		t.Errorf("Snapshot(%q) = %+v, %v, want no snapshot and %q", target, got, err, want)
	}
}

// snapshotMustBeEmpty fails the test unless folder holds nothing.
func snapshotMustBeEmpty(t *testing.T, folder string) {
	t.Helper()
	snapshotMustHoldOnly(t, folder)
}

// snapshotMustHoldOnly fails the test unless folder holds exactly the names want, in their sorted order.
func snapshotMustHoldOnly(t *testing.T, folder string, want ...string) {
	t.Helper()
	entries, err := os.ReadDir(folder)
	if err != nil {
		t.Fatalf("ReadDir(%s) error = %v, want nil", folder, err)
	}
	names := make([]string, 0, len(entries))
	for _, entry := range entries {
		names = append(names, entry.Name())
	}
	if !slices.Equal(names, want) {
		t.Errorf("ReadDir(%s) = %q, want %q", folder, names, want)
	}
}

// snapshotHoldReader begins a read transaction on db that keeps its view of the write-ahead log until the test ends.
func snapshotHoldReader(t *testing.T, db *sql.DB, table string) {
	t.Helper()
	tx, err := db.BeginTx(t.Context(), &sql.TxOptions{ReadOnly: true})
	if err != nil {
		t.Fatalf("BeginTx() error = %v, want nil", err)
	}
	t.Cleanup(func() { _ = tx.Rollback() })
	var n int64
	if err := tx.QueryRowContext(t.Context(), "SELECT count(*) FROM "+table).Scan(&n); err != nil {
		t.Fatalf("read %s in the held transaction: %v", table, err)
	}
}

// snapshotOpenCopy returns a handle with opts on the copy at target.
func snapshotOpenCopy(t *testing.T, target string, opts sqlite.Options) *sql.DB {
	t.Helper()
	opts.Create = false
	return mustOpen(t, "sqlite:"+target, opts)
}

func TestSnapshotIsAConsistentCopy(t *testing.T) {
	t.Parallel()

	gate := snapshotNewGate(t)
	opts := testOptions()
	opts.Functions = snapshotFunctions(t, gate.call)
	db, path := snapshotOpen(t, opts)
	snapshotSeed(t, db)
	target := filepath.Join(snapshotFolder(t), "copy.db")
	writers := snapshotStartWriters(t, db)
	gate.armed.Store(true)
	before := writers.committed.Load()

	result := snapshotStart(t, db, target)

	select {
	case <-gate.reached:
	case got := <-result:
		t.Fatalf("Snapshot() = %+v, %v before it checked the copy through the handle's functions", got.snapshot, got.err)
	}
	if err := writers.stop(); err != nil {
		t.Fatalf("a writer failed with %v, want every write to commit while the snapshot runs", err)
	}
	atCheck := writers.committed.Load()
	gate.release()
	got := <-result
	if got.err != nil {
		t.Fatalf("Snapshot() error = %v, want nil", got.err)
	}
	t.Logf("the checkpoint after the snapshot took %v, and %d writes committed during the copy",
		got.snapshot.Checkpoint.Took, atCheck-before)
	if atCheck <= before {
		t.Errorf("the writers committed %d transactions while the database was copied, want at least one",
			atCheck-before)
	}
	want := dbkit.Checkpoint{Took: got.snapshot.Checkpoint.Took}
	if got.snapshot.Path != target || got.snapshot.Checkpoint != want || want.Took <= 0 {
		t.Errorf("Snapshot() = %+v, want the path %s and a checkpoint that emptied the log", got.snapshot, target)
	}
	if size := snapshotSize(t, path+"-wal"); size != 0 {
		t.Errorf("the write-ahead log holds %d bytes after the checkpoint, want 0", size)
	}
	copied := snapshotOpenCopy(t, target, opts)
	if report := snapshotOne[string](t, copied, "PRAGMA integrity_check"); report != "ok" {
		t.Errorf("PRAGMA integrity_check on the copy = %q, want ok", report)
	}
	rows := snapshotOne[int64](t, copied, "SELECT count(*) FROM snapshot_rows")
	counted := snapshotOne[int64](t, copied, "SELECT n FROM snapshot_count")
	if rows != counted || rows < snapshotSeedRows+before || rows > snapshotSeedRows+atCheck {
		t.Errorf("the copy holds %d rows and counts %d, want one count from %d to %d",
			rows, counted, snapshotSeedRows+before, snapshotSeedRows+atCheck)
	}
}

func TestSnapshotCopiesWhileAWriterHoldsTheWriteLock(t *testing.T) {
	t.Parallel()

	gate := snapshotNewGate(t)
	opts := testOptions()
	opts.BusyTimeout = snapshotHeldWriterBusyTimeout
	opts.Functions = snapshotFunctions(t, gate.call)
	db, _ := snapshotOpen(t, opts)
	snapshotSeed(t, db)
	writer, err := db.BeginTx(t.Context(), nil)
	if err != nil {
		t.Fatalf("BeginTx() error = %v, want nil", err)
	}
	t.Cleanup(func() { _ = writer.Rollback() })
	if _, err := writer.ExecContext(t.Context(), "INSERT INTO snapshot_rows (body) VALUES (randomblob(64))"); err != nil {
		t.Fatalf("insert in the held write transaction: %v", err)
	}
	target := filepath.Join(snapshotFolder(t), "copy.db")
	gate.armed.Store(true)

	result := snapshotStart(t, db, target)

	select {
	case <-gate.reached:
	case got := <-result:
		t.Fatalf("Snapshot() = %+v, %v while a writer held the write lock, want the copy checked first",
			got.snapshot, got.err)
	}
	if err := writer.Commit(); err != nil {
		t.Fatalf("Commit() of the held write transaction error = %v, want nil", err)
	}
	gate.release()
	got := <-result
	if got.err != nil || got.snapshot.Path != target {
		t.Fatalf("Snapshot() = %+v, %v, want the path %s", got.snapshot, got.err, target)
	}
	copied := snapshotOpenCopy(t, target, opts)
	if rows := snapshotOne[int64](t, copied, "SELECT count(*) FROM snapshot_rows"); rows != snapshotSeedRows {
		t.Errorf("the copy holds %d rows, want the %d seed rows without the row of the held writer",
			rows, snapshotSeedRows)
	}
}

func TestSnapshotChildProcess(t *testing.T) {
	source := os.Getenv(snapshotChildSource)
	if source == "" {
		return
	}
	stage := os.Getenv(snapshotChildStage)
	var held sync.Once
	hold := func() {
		held.Do(func() {
			fmt.Println(snapshotHolding)
			_, _ = io.Copy(io.Discard, os.Stdin)
		})
	}
	opts := testOptions()
	opts.Create = false
	opts.Functions = snapshotFunctions(t, func([]driver.Value) (driver.Value, error) {
		if stage == snapshotStageCheck {
			hold()
		}
		return int64(1), nil
	})
	db := mustOpen(t, "sqlite:"+source, opts)
	if stage == snapshotStageCopy {
		snapshotHoldChanges(db, hold)
	}

	snapshot, err := sqlite.NewSnapshotter(db).Snapshot(context.Background(), os.Getenv(snapshotChildTarget))

	t.Fatalf("Snapshot() in the snapshot child = %+v, %v, want the parent to kill the child first", snapshot, err)
}

func TestSnapshotLeavesNoTargetAfterAKill(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name   string
		stage  string
		atKill []string
	}{
		{name: "while the copy is written", stage: snapshotStageCopy,
			atKill: []string{"earlier.db", "killed.db.partial", "killed.db.partial-journal"}},
		{name: "while the copy is checked", stage: snapshotStageCheck,
			atKill: []string{"earlier.db", "killed.db.partial"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			opts := testOptions()
			opts.Functions = snapshotFunctions(t, snapshotPass)
			db, path := snapshotOpen(t, opts)
			snapshotSeed(t, db)
			rulesExec(t, db, "CREATE VIEW snapshot_view AS SELECT n FROM snapshot_count")
			folder := snapshotFolder(t)
			if _, err := sqlite.NewSnapshotter(db).Snapshot(t.Context(), filepath.Join(folder, "earlier.db")); err != nil {
				t.Fatalf("Snapshot() before the kill error = %v, want nil", err)
			}
			target := filepath.Join(folder, "killed.db")
			child := snapshotStartChild(t, path, target, tc.stage)
			snapshotMustHoldOnly(t, folder, tc.atKill...)

			err := child.kill()

			snapshotMustBeKilled(t, err)
			snapshotMustHoldOnly(t, folder, tc.atKill...)
			t.Logf("the killed snapshot left a copy of %d bytes", snapshotSize(t, target+".partial"))
			next := filepath.Join(folder, "next.db")
			got, err := sqlite.NewSnapshotter(db).Snapshot(t.Context(), next)
			if err != nil || got.Path != next {
				t.Fatalf("Snapshot() after the kill = %+v, %v, want the path %s", got, err, next)
			}
			snapshotMustHoldOnly(t, folder, "earlier.db", "next.db")
		})
	}
}

func TestSnapshotKeepsALiveDatabaseNamedLikeAStaleCopy(t *testing.T) {
	t.Parallel()

	folder := snapshotFolder(t)
	live := filepath.Join(folder, "site.db.partial")
	db := mustOpen(t, "sqlite:"+live, testOptions())
	rulesExec(t, db, "CREATE TABLE snapshot_rows (a INTEGER NOT NULL)")
	before := snapshotStat(t, live)
	stale := filepath.Join(folder, "stale.db.partial")
	if err := os.WriteFile(stale, []byte("stale bytes"), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v, want nil", err)
	}
	target := filepath.Join(folder, "copy.db")

	got, err := sqlite.NewSnapshotter(db).Snapshot(t.Context(), target)

	if err != nil || got.Path != target {
		t.Fatalf("Snapshot() = %+v, %v, want the path %s", got, err, target)
	}
	snapshotMustKeep(t, live, before)
	mustNotExist(t, stale)
}

func TestSnapshotKeepsALiveDatabaseNamedAsItsCopy(t *testing.T) {
	t.Parallel()

	folder := snapshotFolder(t)
	target := filepath.Join(folder, "site.db")
	live := target + ".partial"
	db := mustOpen(t, "sqlite:"+live, testOptions())
	rulesExec(t, db, "CREATE TABLE snapshot_rows (a INTEGER NOT NULL)")
	before := snapshotStat(t, live)

	got, err := sqlite.NewSnapshotter(db).Snapshot(t.Context(), target)

	want := "dbkit: create the snapshot copy " + live + ": "
	if !errors.Is(err, fs.ErrExist) || !strings.HasPrefix(err.Error(), want) || got != (dbkit.Snapshot{}) {
		t.Errorf("Snapshot() = %+v, %v, want no snapshot and an error marked ErrExist starting %q", got, err, want)
	}
	snapshotMustKeep(t, live, before)
	mustNotExist(t, target)
}

func TestSnapshotIsNoWiderThanTheLiveFile(t *testing.T) {
	t.Parallel()

	db, path := snapshotOpen(t, testOptions())
	rulesExec(t, db, "CREATE TABLE snapshot_rows (a INTEGER NOT NULL)")
	if err := os.Chmod(path, snapshotOwnerOnlyMode); err != nil {
		t.Fatalf("Chmod(%s) error = %v, want nil", path, err)
	}
	target := filepath.Join(snapshotFolder(t), "copy.db")

	if _, err := sqlite.NewSnapshotter(db).Snapshot(t.Context(), target); err != nil {
		t.Fatalf("Snapshot() error = %v, want nil", err)
	}

	if got := snapshotStat(t, target).Mode().Perm(); got != snapshotOwnerOnlyMode {
		t.Errorf("the snapshot has the mode %v, want the mode %v of the live file", got, snapshotOwnerOnlyMode)
	}
}

func TestSnapshotRefusesALiveFileThatIsGone(t *testing.T) {
	t.Parallel()

	db, path := snapshotOpen(t, testOptions())
	rulesExec(t, db, "CREATE TABLE snapshot_rows (a INTEGER NOT NULL)")
	if err := os.Remove(path); err != nil {
		t.Fatalf("Remove(%s) error = %v, want nil", path, err)
	}
	folder := snapshotFolder(t)

	got, err := sqlite.NewSnapshotter(db).Snapshot(t.Context(), filepath.Join(folder, "copy.db"))

	want := "dbkit: read the database file " + path + ": "
	if !errors.Is(err, fs.ErrNotExist) || !strings.HasPrefix(err.Error(), want) || got != (dbkit.Snapshot{}) {
		t.Errorf("Snapshot() = %+v, %v, want no snapshot and an error marked ErrNotExist starting %q", got, err, want)
	}
	snapshotMustBeEmpty(t, folder)
}

func TestSnapshotRefusesAnExistingTarget(t *testing.T) {
	t.Parallel()

	t.Run("an empty file", func(t *testing.T) {
		t.Parallel()
		db, _ := snapshotOpen(t, testOptions())
		target := filepath.Join(snapshotFolder(t), "empty.db")
		if err := os.WriteFile(target, nil, 0o600); err != nil {
			t.Fatalf("WriteFile() error = %v, want nil", err)
		}

		snapshotMustRefuse(t, db, target, "dbkit: the snapshot target "+target+" already exists")

		if size := snapshotSize(t, target); size != 0 {
			t.Errorf("the empty target holds %d bytes after the refused snapshot, want 0", size)
		}
		mustNotExist(t, target+".partial")
	})
	t.Run("a file holding data", func(t *testing.T) {
		t.Parallel()
		db, _ := snapshotOpen(t, testOptions())
		target := filepath.Join(snapshotFolder(t), "kept.db")
		if err := os.WriteFile(target, []byte("kept bytes"), 0o600); err != nil {
			t.Fatalf("WriteFile() error = %v, want nil", err)
		}

		snapshotMustRefuse(t, db, target, "dbkit: the snapshot target "+target+" already exists")

		if data, err := os.ReadFile(target); err != nil || string(data) != "kept bytes" {
			t.Errorf("the target holds %d bytes, %v after the refused snapshot, want its own %d bytes",
				len(data), err, len("kept bytes"))
		}
		mustNotExist(t, target+".partial")
	})
	t.Run("a link to a missing file", func(t *testing.T) {
		t.Parallel()
		db, _ := snapshotOpen(t, testOptions())
		folder := snapshotFolder(t)
		target := filepath.Join(folder, "link.db")
		missing := filepath.Join(folder, "missing.db")
		if err := os.Symlink(missing, target); err != nil {
			t.Fatalf("Symlink() error = %v, want nil", err)
		}

		snapshotMustRefuse(t, db, target, "dbkit: the snapshot target "+target+" already exists")

		if got, err := os.Readlink(target); err != nil || got != missing {
			t.Errorf("the target links to %q, %v after the refused snapshot, want %q", got, err, missing)
		}
		mustNotExist(t, missing)
	})
}

func TestSnapshotRefusesAFileURITarget(t *testing.T) {
	t.Parallel()

	db, _ := snapshotOpen(t, testOptions())
	folder := snapshotFolder(t)
	target := "file:" + filepath.Join(folder, "copy.db")

	snapshotMustRefuse(t, db, target, "dbkit: the snapshot target must be a plain path, not a file: URI, got "+
		strconv.Quote(target))

	snapshotMustBeEmpty(t, folder)
}

func TestSnapshotRefusesARelativeTarget(t *testing.T) {
	t.Parallel()

	db, _ := snapshotOpen(t, testOptions())
	folder := snapshotFolder(t)
	wd, err := os.Getwd()
	if err != nil {
		t.Fatalf("Getwd() error = %v, want nil", err)
	}
	target, err := filepath.Rel(wd, filepath.Join(folder, "copy.db"))
	if err != nil {
		t.Fatalf("Rel() error = %v, want nil", err)
	}

	snapshotMustRefuse(t, db, target, "dbkit: the snapshot target must be an absolute path, got "+
		strconv.Quote(target))

	snapshotMustBeEmpty(t, folder)
}

func TestSnapshotRefusesATargetUnderAFile(t *testing.T) {
	t.Parallel()

	db, _ := snapshotOpen(t, testOptions())
	plain := filepath.Join(snapshotFolder(t), "plain")
	if err := os.WriteFile(plain, nil, 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v, want nil", err)
	}

	got, err := sqlite.NewSnapshotter(db).Snapshot(t.Context(), filepath.Join(plain, "copy.db"))

	if !errors.Is(err, syscall.ENOTDIR) || !strings.HasPrefix(err.Error(), "dbkit: check the snapshot target: ") ||
		got != (dbkit.Snapshot{}) {
		t.Errorf("Snapshot() = %+v, %v, want no snapshot and the check error marked ENOTDIR", got, err)
	}
}

func TestSnapshotRefusesAMissingFolder(t *testing.T) {
	t.Parallel()

	db, _ := snapshotOpen(t, testOptions())
	missing := filepath.Join(snapshotFolder(t), "missing")

	got, err := sqlite.NewSnapshotter(db).Snapshot(t.Context(), filepath.Join(missing, "copy.db"))

	want := "dbkit: list the snapshot folder " + missing + ": "
	if !errors.Is(err, fs.ErrNotExist) || !strings.HasPrefix(err.Error(), want) || got != (dbkit.Snapshot{}) {
		t.Errorf("Snapshot() = %+v, %v, want no snapshot and an error marked ErrNotExist starting %q", got, err, want)
	}
	mustNotExist(t, missing)
}

func TestSnapshotStopsAtAStaleCopyItCannotRemove(t *testing.T) {
	t.Parallel()

	db, _ := snapshotOpen(t, testOptions())
	folder := snapshotFolder(t)
	stale := filepath.Join(folder, "stale.db.partial")
	if err := os.MkdirAll(filepath.Join(stale, "inside"), 0o700); err != nil {
		t.Fatalf("MkdirAll() error = %v, want nil", err)
	}
	target := filepath.Join(folder, "copy.db")

	got, err := sqlite.NewSnapshotter(db).Snapshot(t.Context(), target)

	want := "dbkit: remove the stale snapshot copy " + stale + ": "
	if err == nil || !strings.HasPrefix(err.Error(), want) || got != (dbkit.Snapshot{}) {
		t.Errorf("Snapshot() = %+v, %v, want no snapshot and an error starting %q", got, err, want)
	}
	mustNotExist(t, target)
	mustExist(t, filepath.Join(stale, "inside"))
}

func TestSnapshotRefusesANilHandle(t *testing.T) {
	t.Parallel()

	folder := snapshotFolder(t)

	snapshotMustRefuse(t, nil, filepath.Join(folder, "copy.db"),
		"dbkit: NewSnapshotter needs a database handle, got nil")

	snapshotMustBeEmpty(t, folder)
}

func TestSnapshotRefusesATargetHoldingAQuestionMark(t *testing.T) {
	t.Parallel()

	db, _ := snapshotOpen(t, testOptions())
	folder := snapshotFolder(t)
	target := filepath.Join(folder, "copy?.db")

	snapshotMustRefuse(t, db, target, "dbkit: the snapshot target must hold no ?, got "+strconv.Quote(target))

	snapshotMustBeEmpty(t, folder)
}

func TestSnapshotRefusesATargetTheNextSnapshotWouldRemove(t *testing.T) {
	t.Parallel()

	for _, suffix := range []string{".partial", ".partial-journal"} {
		t.Run("a name ending with "+suffix, func(t *testing.T) {
			t.Parallel()
			db, _ := snapshotOpen(t, testOptions())
			folder := snapshotFolder(t)
			target := filepath.Join(folder, "copy"+suffix)

			snapshotMustRefuse(t, db, target, "dbkit: the snapshot target must not end with "+suffix+", got "+
				strconv.Quote(target))

			snapshotMustBeEmpty(t, folder)
		})
	}
}

func TestABusyCheckpointReportsAndKeepsTheCopy(t *testing.T) {
	t.Parallel()

	opts := testOptions()
	opts.BusyTimeout = snapshotShortBusyTimeout
	db, _ := snapshotOpen(t, opts)
	rulesExec(t, db, "CREATE TABLE snapshot_rows (id INTEGER PRIMARY KEY, body BLOB NOT NULL)")
	rulesExec(t, db, "INSERT INTO snapshot_rows (body) VALUES (randomblob(64))")
	snapshotHoldReader(t, db, "snapshot_rows")
	rulesExec(t, db, "INSERT INTO snapshot_rows (body) VALUES (randomblob(64))")
	target := filepath.Join(snapshotFolder(t), "copy.db")

	got, err := sqlite.NewSnapshotter(db).Snapshot(t.Context(), target)

	if err != nil || got.Path != target {
		t.Fatalf("Snapshot() = %+v, %v, want the path %s and no error while a reader holds the log", got, err, target)
	}
	t.Logf("the busy checkpoint took %v", got.Checkpoint.Took)
	if c := got.Checkpoint; !c.Busy || c.LogFrames < 1 || c.CheckpointedFrames >= c.LogFrames {
		t.Errorf("Checkpoint = %+v, want busy with frames left behind the reader", c)
	}
	copied := snapshotOpenCopy(t, target, opts)
	if rows := snapshotOne[int64](t, copied, "SELECT count(*) FROM snapshot_rows"); rows != 2 {
		t.Errorf("the copy holds %d rows, want both rows written before the snapshot", rows)
	}
	mustNotExist(t, target+".partial")
}

func TestAFailedQuickCheckLeavesNoTarget(t *testing.T) {
	t.Parallel()

	db, _ := snapshotOpen(t, testOptions())
	rulesExec(t, db, "CREATE TABLE snapshot_checked (a INTEGER NOT NULL CHECK (a > 0))")
	conn, err := db.Conn(t.Context())
	if err != nil {
		t.Fatalf("Conn() error = %v, want nil", err)
	}
	for _, query := range []string{
		"PRAGMA ignore_check_constraints=ON",
		"INSERT INTO snapshot_checked VALUES (-1)",
		"PRAGMA ignore_check_constraints=OFF",
	} {
		if _, err := conn.ExecContext(t.Context(), query); err != nil {
			t.Fatalf("ExecContext(%q) error = %v, want nil", query, err)
		}
	}
	if err := conn.Close(); err != nil {
		t.Fatalf("Close() error = %v, want nil", err)
	}
	folder := snapshotFolder(t)
	target := filepath.Join(folder, "copy.db")

	snapshotMustRefuse(t, db, target, "dbkit: the snapshot copy "+target+
		".partial failed PRAGMA quick_check: CHECK constraint failed in snapshot_checked")

	snapshotMustBeEmpty(t, folder)
}

func TestAFailedCopyLeavesNoTarget(t *testing.T) {
	t.Parallel()

	failed := errors.New("snapshot test: the copy failed on purpose")
	faults := &sqlitetest.Faults{}
	faults.FailStatement("VACUUM INTO ?", failed)
	db := sqlitetest.OpenWithFaults(t, testOptions(), faults)
	folder := snapshotFolder(t)
	target := filepath.Join(folder, "copy.db")

	got, err := sqlite.NewSnapshotter(db).Snapshot(t.Context(), target)

	want := "dbkit: copy the database to " + target + ".partial: " + failed.Error()
	if !errors.Is(err, failed) || err.Error() != want || got != (dbkit.Snapshot{}) {
		t.Errorf("Snapshot() = %+v, %v, want no snapshot and %q", got, err, want)
	}
	snapshotMustBeEmpty(t, folder)
}

func TestAFailedPathReadLeavesNoTarget(t *testing.T) {
	t.Parallel()

	failed := errors.New("snapshot test: the path read failed on purpose")
	faults := &sqlitetest.Faults{}
	faults.FailStatement("SELECT file FROM pragma_database_list WHERE name = 'main'", failed)
	db := sqlitetest.OpenWithFaults(t, testOptions(), faults)
	folder := snapshotFolder(t)

	got, err := sqlite.NewSnapshotter(db).Snapshot(t.Context(), filepath.Join(folder, "copy.db"))

	want := "dbkit: read the database path: " + failed.Error()
	if !errors.Is(err, failed) || err.Error() != want || got != (dbkit.Snapshot{}) {
		t.Errorf("Snapshot() = %+v, %v, want no snapshot and %q", got, err, want)
	}
	snapshotMustBeEmpty(t, folder)
}

func TestAFailedCheckpointKeepsTheCopy(t *testing.T) {
	t.Parallel()

	failed := errors.New("snapshot test: the checkpoint failed on purpose")
	faults := &sqlitetest.Faults{}
	faults.FailStatement("PRAGMA wal_checkpoint(TRUNCATE)", failed)
	db := sqlitetest.OpenWithFaults(t, testOptions(), faults)
	rulesExec(t, db, "CREATE TABLE snapshot_rows (a INTEGER NOT NULL)")
	target := filepath.Join(snapshotFolder(t), "copy.db")

	got, err := sqlite.NewSnapshotter(db).Snapshot(t.Context(), target)

	want := "dbkit: checkpoint the write-ahead log after the snapshot " + target + ": " + failed.Error()
	if !errors.Is(err, failed) || err.Error() != want || got.Path != target {
		t.Errorf("Snapshot() = %+v, %v, want the path %s and %q", got, err, target, want)
	}
	copied := snapshotOpenCopy(t, target, testOptions())
	if n := snapshotOne[int64](t, copied, "SELECT count(*) FROM snapshot_rows"); n != 0 {
		t.Errorf("the copy holds %d rows, want its empty table", n)
	}
}

func TestACancelledSnapshotLeavesNoTarget(t *testing.T) {
	t.Parallel()

	db, _ := snapshotOpen(t, testOptions())
	folder := snapshotFolder(t)
	target := filepath.Join(folder, "copy.db")
	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	got, err := sqlite.NewSnapshotter(db).Snapshot(ctx, target)

	want := "dbkit: read the database path: " + context.Canceled.Error()
	if !errors.Is(err, context.Canceled) || err.Error() != want || got != (dbkit.Snapshot{}) {
		t.Errorf("Snapshot() = %+v, %v, want no snapshot and %q", got, err, want)
	}
	snapshotMustBeEmpty(t, folder)
}
