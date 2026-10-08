// SPDX-License-Identifier: Apache-2.0

//go:build linux || darwin

package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"github.com/pressly/goose/v3"
	"golang.org/x/sys/unix"
)

const (
	// lockHolderPath names the environment switch that makes the test binary hold the lock of the database at its value.
	lockHolderPath = "DBKIT_TEST_LOCK_HOLDER"
	// lockHeld is the line a lock holder prints once it holds the migration lock.
	lockHeld = "dbkit test: the migration lock is held"
	// lockTable is the version table the lock tests migrate.
	lockTable = "lock_versions"
	// lockHolderTable is the table the migration of a lock holder creates.
	lockHolderTable = "lock_holder_rows"
	// lockHolderVersions is the version table of a holder that shares the waiter's handle.
	lockHolderVersions = "lock_holder_versions"
	// lockNewOwnerTable is the version table of an owner that has never run.
	lockNewOwnerTable = "lock_new_owner_versions"
	// lockLongWait is a lock wait no lock test waits out.
	lockLongWait = time.Minute
	// lockShortWait is a lock wait a test waits out on purpose.
	lockShortWait = 50 * time.Millisecond
	// lockPoll is how often a waiting run of the lock tests tries the lock.
	lockPoll = 2 * time.Millisecond
	// lockRootUID is the user id of root.
	lockRootUID = 0
	// lockServiceUID is the user id of a service user that is not root.
	lockServiceUID = 1000
)

const (
	// lockGroupWritableMode is the mode of a folder its group may write in.
	lockGroupWritableMode fs.FileMode = 0o770
	// lockOthersWritableMode is the mode of a folder others may write in.
	lockOthersWritableMode fs.FileMode = 0o707
	// lockStickyOthersWritableMode is the mode of a folder others may write in, with the sticky bit.
	lockStickyOthersWritableMode = fs.ModeSticky | 0o777
)

// lockOutput is the output of a lock holder, which reports when the holder prints lockHeld.
type lockOutput struct {
	// mu guards text.
	mu sync.Mutex
	// text is everything the holder printed.
	text strings.Builder
	// held is closed once text holds the lockHeld line.
	held chan struct{}
	// once closes held once.
	once sync.Once
}

// Write keeps p and closes held once the output holds the lockHeld line.
func (o *lockOutput) Write(p []byte) (int, error) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.text.Write(p)
	if strings.Contains(o.text.String(), lockHeld+"\n") {
		o.once.Do(func() { close(o.held) })
	}
	return len(p), nil
}

// String returns everything the holder printed.
func (o *lockOutput) String() string {
	o.mu.Lock()
	defer o.mu.Unlock()
	return o.text.String()
}

// lockHolder is a run of the test binary that holds the migration lock of one database.
type lockHolder struct {
	// cmd is the running test binary.
	cmd *exec.Cmd
	// stdin ends the holder's migration when closed.
	stdin io.WriteCloser
	// output is what the holder printed.
	output *lockOutput
	// ended is closed once the holder was reaped.
	ended chan struct{}
	// err is how the holder ended, read once ended is closed.
	err error
}

// lockStartHolder starts a holder of the migration lock of the database at path and returns once it holds the lock.
func lockStartHolder(t *testing.T, path string) *lockHolder {
	t.Helper()
	output := &lockOutput{held: make(chan struct{})}
	cmd := exec.Command(os.Args[0], "-test.run=^TestMigrationLockHolderProcess$")
	cmd.Env = append(os.Environ(), lockHolderPath+"="+path)
	cmd.Stdout, cmd.Stderr = output, output
	stdin, err := cmd.StdinPipe()
	if err != nil {
		t.Fatalf("StdinPipe() error = %v, want nil", err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatalf("Start() error = %v, want nil", err)
	}
	h := &lockHolder{cmd: cmd, stdin: stdin, output: output, ended: make(chan struct{})}
	go func() {
		h.err = cmd.Wait()
		close(h.ended)
	}()
	t.Cleanup(func() { _ = h.kill() })
	select {
	case <-output.held:
	case <-h.ended:
		t.Fatalf("the lock holder ended with %v before it held the lock\n%s", h.err, output)
	}
	return h
}

// release closes the holder's standard input, which ends its migration, and returns how the holder ended.
func (h *lockHolder) release() error {
	_ = h.stdin.Close()
	<-h.ended
	return h.err
}

// kill ends the holder with SIGKILL and returns how it ended.
func (h *lockHolder) kill() error {
	_ = h.cmd.Process.Signal(syscall.SIGKILL)
	<-h.ended
	return h.err
}

// lockOpen returns a handle on a fresh file in a folder with every link resolved, and the file's path.
func lockOpen(t *testing.T) (*sql.DB, string) {
	t.Helper()
	folder, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatalf("EvalSymlinks() error = %v, want nil", err)
	}
	path := filepath.Join(folder, "site.db")
	db, err := Open("sqlite:"+path, internalOptions())
	if err != nil {
		t.Fatalf("Open() error = %v, want nil", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := db.PingContext(t.Context()); err != nil {
		t.Fatalf("PingContext() error = %v, want nil", err)
	}
	return db, path
}

// lockMigrations returns the migrations of the lock tests, whose one Go migration counts its runs in runs.
func lockMigrations(runs *atomic.Int32, wait time.Duration) Migrations {
	up := &goose.GoFunc{RunTx: func(context.Context, *sql.Tx) error {
		runs.Add(1)
		return nil
	}}
	return Migrations{Table: lockTable, Go: []*goose.Migration{goose.NewGoMigration(1, up, nil)},
		LockWait: wait, LockPoll: lockPoll}
}

// lockTries puts in place of flock one that sends the answer of every try to the returned channel, until the test ends.
func lockTries(t *testing.T) <-chan error {
	t.Helper()
	tries := make(chan error, 1024)
	kept := flock
	flock = func(fd, how int) error {
		err := kept(fd, how)
		select {
		case tries <- err:
		default:
		}
		return err
	}
	t.Cleanup(func() { flock = kept })
	return tries
}

// lockRun is a run of Migrate in a goroutine.
type lockRun struct {
	// cancel cancels the context of the run.
	cancel context.CancelFunc
	// done is closed once the run returned.
	done chan struct{}
	// err is the error of the run, read once done is closed.
	err error
}

// lockGo runs Migrate with m on db in a goroutine, which the end of the test waits for.
func lockGo(t *testing.T, db *sql.DB, m Migrations) *lockRun {
	t.Helper()
	ctx, cancel := context.WithCancel(t.Context())
	run := &lockRun{cancel: cancel, done: make(chan struct{})}
	go func() {
		defer close(run.done)
		run.err = Migrate(ctx, db, m)
	}()
	t.Cleanup(func() { <-run.done })
	return run
}

// lockStart runs Migrate with m on db in a goroutine, which the end of the test waits for after it kills holder.
func lockStart(t *testing.T, db *sql.DB, m Migrations, holder *lockHolder) *lockRun {
	t.Helper()
	run := lockGo(t, db, m)
	t.Cleanup(func() { _ = holder.kill() })
	return run
}

// wait returns the error of the run once it returned.
func (r *lockRun) wait() error {
	<-r.done
	return r.err
}

// firstTry returns the answer of the first try of the lock, and fails the test when the run returned before any try.
func (r *lockRun) firstTry(t *testing.T, tries <-chan error) error {
	t.Helper()
	select {
	case err := <-tries:
		return err
	case <-r.done:
	}
	select {
	case err := <-tries:
		return err
	default:
		t.Fatalf("Migrate() returned %v before it tried the migration lock", r.err)
		return nil
	}
}

// lockTried returns the answer of the first try of the lock by a run that returned, and fails the test when none came.
func lockTried(t *testing.T, tries <-chan error) error {
	t.Helper()
	select {
	case err := <-tries:
		return err
	default:
		t.Fatal("Migrate() returned before it tried the migration lock")
		return nil
	}
}

// lockHolderCommitted reports whether lockHolderTable exists on db.
func lockHolderCommitted(t *testing.T, db *sql.DB) bool {
	t.Helper()
	var n int64
	query := "SELECT count(*) FROM sqlite_schema WHERE name = ?"
	if err := db.QueryRowContext(t.Context(), query, lockHolderTable).Scan(&n); err != nil {
		t.Fatalf("look for %s: %v", lockHolderTable, err)
	}
	return n == 1
}

// lockMustApplyOnce fails the test unless the version table on db records version 1 as applied once.
func lockMustApplyOnce(t *testing.T, db *sql.DB) {
	t.Helper()
	var n int64
	query := "SELECT count(*) FROM " + lockTable + " WHERE version_id = 1 AND is_applied"
	if err := db.QueryRowContext(t.Context(), query).Scan(&n); err != nil || n != 1 {
		t.Errorf("%s records version 1 %d times, %v, want once", lockTable, n, err)
	}
}

func TestMigrationLockHolderProcess(t *testing.T) {
	path := os.Getenv(lockHolderPath)
	if path == "" {
		return
	}
	db, err := Open("sqlite:"+path, internalOptions())
	if err != nil {
		t.Fatalf("Open() in the lock holder error = %v, want nil", err)
	}
	defer func() { _ = db.Close() }()
	hold := &goose.GoFunc{RunTx: func(ctx context.Context, tx *sql.Tx) error {
		fmt.Println(lockHeld)
		if _, err := io.Copy(io.Discard, os.Stdin); err != nil {
			return err
		}
		_, err := tx.ExecContext(ctx, "CREATE TABLE "+lockHolderTable+" (a INTEGER)")
		return err
	}}
	m := Migrations{Table: lockTable, Go: []*goose.Migration{goose.NewGoMigration(1, hold, nil)},
		LockWait: lockLongWait, LockPoll: lockPoll}

	if err := Migrate(context.Background(), db, m); err != nil {
		t.Fatalf("Migrate() in the lock holder error = %v, want nil", err)
	}
}

func TestMigrationLockHoldsAcrossProcesses(t *testing.T) {
	t.Run("a second process waits until the first releases", func(t *testing.T) {
		db, path := lockOpen(t)
		holder := lockStartHolder(t, path)
		tries := lockTries(t)
		var runs atomic.Int32
		run := lockStart(t, db, lockMigrations(&runs, lockLongWait), holder)
		if err := run.firstTry(t, tries); !errors.Is(err, unix.EWOULDBLOCK) {
			t.Fatalf("the first try of the waiting run = %v, want EWOULDBLOCK while the holder holds the lock", err)
		}

		if err := holder.release(); err != nil {
			t.Fatalf("the holder ended with %v, want a clean end\n%s", err, holder.output)
		}

		if err := run.wait(); err != nil {
			t.Fatalf("Migrate() error = %v, want nil once the holder released the lock", err)
		}
		if got := runs.Load(); got != 0 {
			t.Errorf("the waiting run applied version 1 %d times, want none after the holder applied it", got)
		}
		if !lockHolderCommitted(t, db) {
			t.Errorf("%s is absent, want the holder's migration committed", lockHolderTable)
		}
		lockMustApplyOnce(t, db)
	})
	t.Run("the first run of a new owner waits for a holder inside a write transaction", func(t *testing.T) {
		db, path := lockOpen(t)
		holder := lockStartHolder(t, path)
		tries := lockTries(t)
		var runs atomic.Int32
		m := lockMigrations(&runs, lockLongWait)
		m.Table = lockNewOwnerTable
		run := lockStart(t, db, m, holder)
		if err := run.firstTry(t, tries); !errors.Is(err, unix.EWOULDBLOCK) {
			t.Fatalf("the first try of the new owner = %v, want EWOULDBLOCK while the holder holds the lock", err)
		}

		if err := holder.release(); err != nil {
			t.Fatalf("the holder ended with %v, want a clean end\n%s", err, holder.output)
		}

		if err := run.wait(); err != nil {
			t.Fatalf("Migrate() of the new owner error = %v, want nil once the holder released the lock", err)
		}
		if got := runs.Load(); got != 1 {
			t.Errorf("the new owner applied version 1 %d times, want once", got)
		}
		if !lockHolderCommitted(t, db) {
			t.Errorf("%s is absent, want the holder's migration committed", lockHolderTable)
		}
	})
	t.Run("the lock is gone after the holder is killed", func(t *testing.T) {
		db, path := lockOpen(t)
		holder := lockStartHolder(t, path)

		err := holder.kill()

		var exit *exec.ExitError
		if !errors.As(err, &exit) {
			t.Fatalf("the holder ended with %v, want SIGKILL", err)
		}
		if status, ok := exit.Sys().(syscall.WaitStatus); !ok || status.Signal() != syscall.SIGKILL {
			t.Fatalf("the holder ended with %v, want SIGKILL", exit)
		}
		tries := lockTries(t)
		var runs atomic.Int32
		if err := Migrate(t.Context(), db, lockMigrations(&runs, lockLongWait)); err != nil {
			t.Fatalf("Migrate() error = %v, want nil after the holder was killed", err)
		}
		if err := lockTried(t, tries); err != nil {
			t.Errorf("the first try after the kill = %v, want the lock free", err)
		}
		if got := runs.Load(); got != 1 {
			t.Errorf("the run after the kill applied version 1 %d times, want once", got)
		}
		if lockHolderCommitted(t, db) {
			t.Errorf("%s exists, want the killed holder's migration rolled back", lockHolderTable)
		}
		lockMustApplyOnce(t, db)
	})
	t.Run("a waiter gives up after the lock wait", func(t *testing.T) {
		db, path := lockOpen(t)
		holder := lockStartHolder(t, path)
		var runs atomic.Int32
		began := time.Now()

		err := Migrate(t.Context(), db, lockMigrations(&runs, lockShortWait))

		waited := time.Since(began)
		want := "dbkit: the migration lock " + path + ".migrate.lock stayed held past the lock wait of 50ms"
		if err == nil || !strings.HasSuffix(err.Error(), want) {
			t.Errorf("Migrate() error = %v, want an error ending %q", err, want)
		}
		if waited < lockShortWait {
			t.Errorf("Migrate() gave up after %v, want at least the lock wait %v", waited, lockShortWait)
		}
		if got := runs.Load(); got != 0 {
			t.Errorf("the run that gave up applied version 1 %d times, want none", got)
		}
		if err := holder.release(); err != nil {
			t.Errorf("the holder ended with %v, want a clean end\n%s", err, holder.output)
		}
	})
	t.Run("a cancelled context ends a waiting lock", func(t *testing.T) {
		db, path := lockOpen(t)
		holder := lockStartHolder(t, path)
		tries := lockTries(t)
		var runs atomic.Int32
		run := lockStart(t, db, lockMigrations(&runs, lockLongWait), holder)
		if err := run.firstTry(t, tries); !errors.Is(err, unix.EWOULDBLOCK) {
			t.Fatalf("the first try of the waiting run = %v, want EWOULDBLOCK while the holder holds the lock", err)
		}

		run.cancel()

		err := run.wait()
		want := "dbkit: wait for the migration lock " + path + ".migrate.lock: context canceled"
		if !errors.Is(err, context.Canceled) || !strings.HasSuffix(err.Error(), want) {
			t.Errorf("Migrate() error = %v, want context.Canceled in an error ending %q", err, want)
		}
		if got := runs.Load(); got != 0 {
			t.Errorf("the cancelled run applied version 1 %d times, want none", got)
		}
	})
}

// lockSharedHolder returns migrations whose RunDB migration closes inside, waits for proceed, then writes through db.
func lockSharedHolder(inside, proceed chan struct{}) Migrations {
	write := &goose.GoFunc{RunDB: func(ctx context.Context, db *sql.DB) error {
		close(inside)
		select {
		case <-proceed:
		case <-ctx.Done():
			return context.Cause(ctx)
		}
		_, err := db.ExecContext(ctx, "CREATE TABLE "+lockHolderTable+" (a INTEGER)")
		return err
	}}
	return Migrations{Table: lockHolderVersions, Go: []*goose.Migration{goose.NewGoMigration(1, write, nil)},
		LockWait: lockLongWait, LockPoll: lockPoll}
}

func TestAWaitingRunnerNeverStarvesTheHolder(t *testing.T) {
	db, _ := lockOpen(t)
	inside, proceed := make(chan struct{}), make(chan struct{})
	holder := lockGo(t, db, lockSharedHolder(inside, proceed))
	select {
	case <-inside:
	case <-holder.done:
		t.Fatalf("the holder returned %v before its RunDB migration ran", holder.err)
	}
	tries := lockTries(t)
	var runs atomic.Int32
	waiter := lockGo(t, db, lockMigrations(&runs, lockLongWait))
	if err := waiter.firstTry(t, tries); !errors.Is(err, unix.EWOULDBLOCK) {
		t.Fatalf("the first try of the waiting run = %v, want EWOULDBLOCK while the holder holds the lock", err)
	}

	close(proceed)

	if err := holder.wait(); err != nil {
		t.Errorf("the holder's Migrate() error = %v, want nil", err)
	}
	if err := waiter.wait(); err != nil {
		t.Errorf("the waiter's Migrate() error = %v, want nil once the holder released the lock", err)
	}
	if got := db.Stats().WaitCount; got != 0 {
		t.Errorf("the handle waited %d times for a free connection, want never at MaxConns 2", got)
	}
	if got := runs.Load(); got != 1 {
		t.Errorf("the waiter applied version 1 %d times, want once", got)
	}
	if !lockHolderCommitted(t, db) {
		t.Errorf("%s is absent, want the holder's RunDB migration committed", lockHolderTable)
	}
}

// lockAs puts in place of geteuid one that answers uid, until the test ends.
func lockAs(t *testing.T, uid int) {
	t.Helper()
	kept := geteuid
	geteuid = func() int { return uid }
	t.Cleanup(func() { geteuid = kept })
}

// lockOwner returns the user and group of the file at path.
func lockOwner(t *testing.T, path string) (int, int) {
	t.Helper()
	var st unix.Stat_t
	if err := unix.Stat(path, &st); err != nil {
		t.Fatalf("Stat(%s) error = %v, want nil", path, err)
	}
	return int(st.Uid), int(st.Gid)
}

// lockRegroup gives the file at path another group of the test user, and returns the group it had and the new one.
func lockRegroup(t *testing.T, path string) (int, int) {
	t.Helper()
	_, given := lockOwner(t, path)
	groups, err := os.Getgroups()
	if err != nil {
		t.Fatalf("Getgroups() error = %v, want nil", err)
	}
	for _, group := range groups {
		if group != given {
			if err := os.Chown(path, -1, group); err != nil {
				t.Fatalf("Chown(%s, -1, %d) error = %v, want nil", path, group, err)
			}
			return given, group
		}
	}
	t.Skipf("the test user belongs to no group other than %d", given)
	return given, given
}

func TestTheLockFileOfARunAsRootBelongsToTheDatabaseOwner(t *testing.T) {
	t.Run("a run as root asks for the owner and group of the database file", func(t *testing.T) {
		db, path := lockOpen(t)
		lockRegroup(t, path)
		lockAs(t, lockRootUID)
		var runs atomic.Int32

		if err := Migrate(t.Context(), db, lockMigrations(&runs, lockLongWait)); err != nil {
			t.Fatalf("Migrate() error = %v, want nil", err)
		}

		uid, gid := lockOwner(t, path)
		if lockUID, lockGID := lockOwner(t, path+lockSuffix); lockUID != uid || lockGID != gid {
			t.Errorf("the lock file belongs to %d:%d, want %d:%d, the owner of the database file",
				lockUID, lockGID, uid, gid)
		}
	})
	t.Run("a run as another user asks for nothing", func(t *testing.T) {
		db, path := lockOpen(t)
		given, group := lockRegroup(t, path)
		lockAs(t, lockServiceUID)
		var runs atomic.Int32

		if err := Migrate(t.Context(), db, lockMigrations(&runs, lockLongWait)); err != nil {
			t.Fatalf("Migrate() error = %v, want nil", err)
		}

		if _, gid := lockOwner(t, path+lockSuffix); gid != given {
			t.Errorf("the lock file's group = %d, want %d as a new file gets it, never the database file's %d",
				gid, given, group)
		}
	})
	t.Run("a run as root fails when the database file is gone", func(t *testing.T) {
		db, path := lockOpen(t)
		if err := os.Remove(path); err != nil {
			t.Fatalf("Remove() error = %v, want nil", err)
		}
		lockAs(t, lockRootUID)
		var runs atomic.Int32

		err := Migrate(t.Context(), db, lockMigrations(&runs, lockLongWait))

		want := "dbkit: give the migration lock " + path + lockSuffix + " the owner of " + path + ": "
		if !errors.Is(err, os.ErrNotExist) || !strings.Contains(err.Error(), want) {
			t.Errorf("Migrate() error = %v, want an error marked ErrNotExist holding %q", err, want)
		}
		if got := runs.Load(); got != 0 {
			t.Errorf("the run applied version 1 %d times, want none", got)
		}
	})
}

func TestAFailedLockCallEndsTheWait(t *testing.T) {
	db, path := lockOpen(t)
	kept := flock
	flock = func(int, int) error { return unix.ENOLCK }
	t.Cleanup(func() { flock = kept })
	var runs atomic.Int32

	err := Migrate(t.Context(), db, lockMigrations(&runs, lockLongWait))

	want := "dbkit: take the migration lock " + path + ".migrate.lock: " + unix.ENOLCK.Error()
	if !errors.Is(err, unix.ENOLCK) || !strings.HasSuffix(err.Error(), want) {
		t.Errorf("Migrate() error = %v, want ENOLCK in an error ending %q", err, want)
	}
	if got := runs.Load(); got != 0 {
		t.Errorf("the run applied version 1 %d times, want none", got)
	}
}

func TestAFailedSnapshotLockCallStopsTheSnapshot(t *testing.T) {
	db, folder := internalSnapshotOpen(t)
	kept := flock
	flock = func(int, int) error { return unix.ENOLCK }
	t.Cleanup(func() { flock = kept })

	got, err := NewSnapshotter(db).Snapshot(t.Context(), filepath.Join(folder, "copy.db"))

	want := "dbkit: take the snapshot lock " + filepath.Join(folder, snapshotLockFile) + ": " + unix.ENOLCK.Error()
	if !errors.Is(err, unix.ENOLCK) || err.Error() != want || got.Path != "" {
		t.Errorf("Snapshot() = %+v, %v, want no snapshot and %q", got, err, want)
	}
	internalMustHoldOnly(t, folder, snapshotLockFile)
}

// lockLinked makes a regular file in a fresh folder, links it again at lock so it has two links, and returns its path.
func lockLinked(t *testing.T, lock string) string {
	t.Helper()
	linked := filepath.Join(internalFolder(t), "linked")
	if err := os.WriteFile(linked, []byte("linked bytes"), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v, want nil", err)
	}
	if err := os.Link(linked, lock); err != nil {
		t.Fatalf("Link() error = %v, want nil", err)
	}
	return linked
}

// lockNeverTried fails the test when a run that returned tried the lock.
func lockNeverTried(t *testing.T, tries <-chan error) {
	t.Helper()
	select {
	case err := <-tries:
		t.Errorf("the run tried the lock and got %v, want no try", err)
	default:
	}
}

// lockMustKeepOwner fails the test unless the file at path still belongs to uid and gid.
func lockMustKeepOwner(t *testing.T, path string, uid, gid int) {
	t.Helper()
	if gotUID, gotGID := lockOwner(t, path); gotUID != uid || gotGID != gid {
		t.Errorf("%s belongs to %d:%d, want its own %d:%d", path, gotUID, gotGID, uid, gid)
	}
}

// lockRuns are the users a run of a lock test runs as.
var lockRuns = []struct {
	// name names the user.
	name string
	// uid is the effective user id of the run.
	uid int
}{
	{name: "a run as root", uid: lockRootUID},
	{name: "a run as another user", uid: lockServiceUID},
}

func TestAHardLinkAtTheMigrationLockIsRefused(t *testing.T) {
	for _, tc := range lockRuns {
		t.Run(tc.name, func(t *testing.T) {
			db, path := lockOpen(t)
			lockRegroup(t, path)
			lock := path + lockSuffix
			linked := lockLinked(t, lock)
			uid, gid := lockOwner(t, linked)
			lockAs(t, tc.uid)
			tries := lockTries(t)
			var runs atomic.Int32

			err := Migrate(t.Context(), db, lockMigrations(&runs, lockLongWait))

			want := "dbkit: run the migrations of " + lockTable + ": dbkit: the migration lock " + lock +
				" must be a regular file with one link"
			if err == nil || err.Error() != want {
				t.Errorf("Migrate() error = %v, want %q", err, want)
			}
			lockMustKeepOwner(t, linked, uid, gid)
			lockNeverTried(t, tries)
			if got := runs.Load(); got != 0 {
				t.Errorf("the run applied version 1 %d times, want none", got)
			}
		})
	}
}

func TestAHardLinkAtTheSnapshotLockIsRefused(t *testing.T) {
	for _, tc := range lockRuns {
		t.Run(tc.name, func(t *testing.T) {
			db, path := lockOpen(t)
			if _, err := db.ExecContext(t.Context(), "CREATE TABLE lock_rows (a INTEGER NOT NULL)"); err != nil {
				t.Fatalf("ExecContext() error = %v, want nil", err)
			}
			lockRegroup(t, path)
			folder := internalFolder(t)
			lockRootChain(t, folder)
			lock := filepath.Join(folder, snapshotLockFile)
			linked := lockLinked(t, lock)
			uid, gid := lockOwner(t, linked)
			lockAs(t, tc.uid)
			tries := lockTries(t)

			got, err := NewSnapshotter(db).Snapshot(t.Context(), filepath.Join(folder, "copy.db"))

			want := "dbkit: the snapshot lock " + lock + " must be a regular file with one link"
			if err == nil || err.Error() != want || got.Path != "" {
				t.Errorf("Snapshot() = %+v, %v, want no snapshot and %q", got, err, want)
			}
			lockMustKeepOwner(t, linked, uid, gid)
			lockNeverTried(t, tries)
			internalMustHoldOnly(t, folder, snapshotLockFile)
		})
	}
}

func TestAFailedLockFileCheckStopsTheLock(t *testing.T) {
	db, path := lockOpen(t)
	kept := fstat
	fstat = func(int, *unix.Stat_t) error { return unix.EIO }
	t.Cleanup(func() { fstat = kept })
	tries := lockTries(t)
	var runs atomic.Int32

	err := Migrate(t.Context(), db, lockMigrations(&runs, lockLongWait))

	want := "dbkit: check the migration lock " + path + lockSuffix + ": " + unix.EIO.Error()
	if !errors.Is(err, unix.EIO) || !strings.HasSuffix(err.Error(), want) {
		t.Errorf("Migrate() error = %v, want EIO in an error ending %q", err, want)
	}
	lockNeverTried(t, tries)
	if got := runs.Load(); got != 0 {
		t.Errorf("the run applied version 1 %d times, want none", got)
	}
}

func TestTheSnapshotLockOfARunAsRootBelongsToTheDatabaseOwner(t *testing.T) {
	db, path := lockOpen(t)
	if _, err := db.ExecContext(t.Context(), "CREATE TABLE lock_rows (a INTEGER NOT NULL)"); err != nil {
		t.Fatalf("ExecContext() error = %v, want nil", err)
	}
	lockRegroup(t, path)
	lockAs(t, lockRootUID)
	folder := internalFolder(t)
	lockRootChain(t, folder)

	if _, err := NewSnapshotter(db).Snapshot(t.Context(), filepath.Join(folder, "copy.db")); err != nil {
		t.Fatalf("Snapshot() error = %v, want nil", err)
	}

	uid, gid := lockOwner(t, path)
	if lockUID, lockGID := lockOwner(t, filepath.Join(folder, snapshotLockFile)); lockUID != uid || lockGID != gid {
		t.Errorf("the snapshot lock belongs to %d:%d, want %d:%d, the owner of the database file",
			lockUID, lockGID, uid, gid)
	}
}

// lockExisting makes an empty lock file at lock in another group of the test user and returns its owner and group.
func lockExisting(t *testing.T, lock string) (int, int) {
	t.Helper()
	if err := os.WriteFile(lock, nil, 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v, want nil", err)
	}
	lockRegroup(t, lock)
	return lockOwner(t, lock)
}

func TestARunAsRootKeepsTheOwnerOfALockFileThatExists(t *testing.T) {
	t.Run("the migration lock", func(t *testing.T) {
		db, path := lockOpen(t)
		lock := path + lockSuffix
		uid, gid := lockExisting(t, lock)
		lockAs(t, lockRootUID)
		var runs atomic.Int32

		if err := Migrate(t.Context(), db, lockMigrations(&runs, lockLongWait)); err != nil {
			t.Fatalf("Migrate() error = %v, want nil", err)
		}

		lockMustKeepOwner(t, lock, uid, gid)
		if got := runs.Load(); got != 1 {
			t.Errorf("the run applied version 1 %d times, want once", got)
		}
	})
	t.Run("the snapshot lock", func(t *testing.T) {
		db, folder := internalSnapshotOpen(t)
		lockRootChain(t, folder)
		lock := filepath.Join(folder, snapshotLockFile)
		uid, gid := lockExisting(t, lock)
		lockAs(t, lockRootUID)
		target := filepath.Join(folder, "copy.db")

		if got, err := NewSnapshotter(db).Snapshot(t.Context(), target); err != nil || got.Path != target {
			t.Fatalf("Snapshot() = %+v, %v, want the path %s", got, err, target)
		}

		lockMustKeepOwner(t, lock, uid, gid)
	})
}

func TestALinkRemovedBeforeTheLockCheckKeepsTheOwnerOfTheLinkedFile(t *testing.T) {
	db, path := lockOpen(t)
	lockRegroup(t, path)
	lock := path + lockSuffix
	linked := lockLinked(t, lock)
	uid, gid := lockOwner(t, linked)
	lockAs(t, lockRootUID)
	kept := fstat
	fstat = func(fd int, st *unix.Stat_t) error {
		if err := os.Remove(lock); err != nil {
			return err
		}
		return kept(fd, st)
	}
	t.Cleanup(func() { fstat = kept })
	var runs atomic.Int32

	if err := Migrate(t.Context(), db, lockMigrations(&runs, lockLongWait)); err != nil {
		t.Fatalf("Migrate() error = %v, want nil once the extra link is gone", err)
	}

	lockMustKeepOwner(t, linked, uid, gid)
	if got := runs.Load(); got != 1 {
		t.Errorf("the run applied version 1 %d times, want once", got)
	}
}

func TestALockFileRemovedBetweenTheOpensFailsTheLock(t *testing.T) {
	db, path := lockOpen(t)
	lock := path + lockSuffix
	if err := os.WriteFile(lock, nil, 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v, want nil", err)
	}
	kept := openFile
	openFile = func(name string, flag int, perm os.FileMode) (*os.File, error) {
		if flag&os.O_CREATE == 0 {
			if err := os.Remove(name); err != nil {
				return nil, err
			}
		}
		return kept(name, flag, perm)
	}
	t.Cleanup(func() { openFile = kept })
	tries := lockTries(t)
	var runs atomic.Int32

	err := Migrate(t.Context(), db, lockMigrations(&runs, lockLongWait))

	want := "dbkit: run the migrations of " + lockTable + ": dbkit: open the migration lock " + lock + ": "
	if !errors.Is(err, os.ErrNotExist) || !strings.HasPrefix(err.Error(), want) {
		t.Errorf("Migrate() error = %v, want an error marked ErrNotExist starting %q", err, want)
	}
	lockNeverTried(t, tries)
	if got := runs.Load(); got != 0 {
		t.Errorf("the run applied version 1 %d times, want none", got)
	}
	if _, err := os.Lstat(lock); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("Lstat(%s) error = %v, want the removed lock file never made again", lock, err)
	}
}

// lockAbove returns every folder above path, up to and holding /.
func lockAbove(path string) []string {
	var above []string
	for path != "/" {
		path = filepath.Dir(path)
		above = append(above, path)
	}
	return above
}

// lockRootOwns puts in place of lstat one that answers root as the owner of each path in owned, and counts its calls.
func lockRootOwns(t *testing.T, owned ...string) *atomic.Int32 {
	t.Helper()
	calls := &atomic.Int32{}
	kept := lstat
	lstat = func(path string, st *unix.Stat_t) error {
		calls.Add(1)
		err := kept(path, st)
		if slices.Contains(owned, path) {
			st.Uid = lockRootUID
		}
		return err
	}
	t.Cleanup(func() { lstat = kept })
	return calls
}

// lockRootChain puts in place of lstat one that answers root as the owner of folder and of every folder above it.
func lockRootChain(t *testing.T, folder string) {
	t.Helper()
	lockRootOwns(t, append(lockAbove(folder), folder)...)
}

// lockSymlink makes a link at path that holds text.
func lockSymlink(t *testing.T, path, text string) {
	t.Helper()
	if err := os.Symlink(text, path); err != nil {
		t.Fatalf("Symlink(%s, %s) error = %v, want nil", text, path, err)
	}
}

// lockChmod gives the folder at path the mode.
func lockChmod(t *testing.T, path string, mode fs.FileMode) {
	t.Helper()
	if err := os.Chmod(path, mode); err != nil {
		t.Fatalf("Chmod(%s, %v) error = %v, want nil", path, mode, err)
	}
}

// lockOwnedByTheTestUser returns the error of a snapshot as root whose first failing folder is path.
func lockOwnedByTheTestUser(path string) string {
	return fmt.Sprintf("dbkit: a snapshot as root needs a folder only root can change, and %s belongs to user %d",
		path, os.Geteuid())
}

// lockWritableByOthers returns the error of a snapshot as root whose first failing folder is path, with mode.
func lockWritableByOthers(path string, mode fs.FileMode) string {
	return fmt.Sprintf("dbkit: a snapshot as root needs a folder only root can change, and %s, mode %04o, "+
		"is writable by its group or others without the sticky bit", path, lockUnixMode(mode))
}

// lockTargetWritableByOthers returns the error of a root snapshot whose target folder at path, with mode, is writable.
func lockTargetWritableByOthers(path string, mode fs.FileMode) string {
	return fmt.Sprintf("dbkit: a snapshot as root needs a folder only root can change, and the target folder %s, "+
		"mode %04o, is writable by its group or others", path, lockUnixMode(mode))
}

// lockUnixMode returns the unix mode bits of the folder mode, the sticky bit included.
func lockUnixMode(mode fs.FileMode) uint32 {
	bits := uint32(mode.Perm())
	if mode&fs.ModeSticky != 0 {
		bits |= unix.S_ISVTX
	}
	return bits
}

// lockMustRefuseAsRoot fails the test unless a snapshot of db to target answers no snapshot and want, with no lock try.
func lockMustRefuseAsRoot(t *testing.T, db *sql.DB, target, want string) {
	t.Helper()
	tries := lockTries(t)
	got, err := NewSnapshotter(db).Snapshot(t.Context(), target)
	if err == nil || err.Error() != want || got.Path != "" {
		t.Errorf("Snapshot(%q) = %+v, %v, want no snapshot and %q", target, got, err, want)
	}
	lockNeverTried(t, tries)
}

// lockMustTakeAsRoot fails the test unless a snapshot of db to target answers the path target.
func lockMustTakeAsRoot(t *testing.T, db *sql.DB, target string) {
	t.Helper()
	if got, err := NewSnapshotter(db).Snapshot(t.Context(), target); err != nil || got.Path != target {
		t.Fatalf("Snapshot(%q) = %+v, %v, want the path %s", target, got, err, target)
	}
}

func TestASnapshotAsRootRefusesAFolderAnotherUserCanChange(t *testing.T) {
	t.Run("a target folder the test user owns", func(t *testing.T) {
		db, folder := internalSnapshotOpen(t)
		lockRootOwns(t, lockAbove(folder)...)
		lockAs(t, lockRootUID)

		lockMustRefuseAsRoot(t, db, filepath.Join(folder, "copy.db"), lockOwnedByTheTestUser(folder))

		internalMustHoldOnly(t, folder)
	})
	t.Run("a root folder whose parent the test user owns", func(t *testing.T) {
		db, folder := internalSnapshotOpen(t)
		parent := filepath.Dir(folder)
		lockRootOwns(t, append(lockAbove(parent), folder)...)
		lockAs(t, lockRootUID)

		lockMustRefuseAsRoot(t, db, filepath.Join(folder, "copy.db"), lockOwnedByTheTestUser(parent))

		internalMustHoldOnly(t, folder)
	})
	for _, tc := range []struct {
		// name names the folder.
		name string
		// mode is the mode of the target folder.
		mode fs.FileMode
	}{
		{name: "a root folder its group can write in", mode: lockGroupWritableMode},
		{name: "a root folder others can write in", mode: lockOthersWritableMode},
	} {
		t.Run(tc.name, func(t *testing.T) {
			db, folder := internalSnapshotOpen(t)
			lockRootChain(t, folder)
			lockChmod(t, folder, tc.mode)
			lockAs(t, lockRootUID)

			lockMustRefuseAsRoot(t, db, filepath.Join(folder, "copy.db"), lockTargetWritableByOthers(folder, tc.mode))

			internalMustHoldOnly(t, folder)
		})
	}
	t.Run("a root target folder others can write in with the sticky bit", func(t *testing.T) {
		db, folder := internalSnapshotOpen(t)
		lockRootChain(t, folder)
		lockChmod(t, folder, lockStickyOthersWritableMode)
		lockAs(t, lockRootUID)

		lockMustRefuseAsRoot(t, db, filepath.Join(folder, "copy.db"),
			lockTargetWritableByOthers(folder, lockStickyOthersWritableMode))

		internalMustHoldOnly(t, folder)
	})
	t.Run("a root folder above the target its group can write in", func(t *testing.T) {
		db, parent := internalSnapshotOpen(t)
		folder := filepath.Join(parent, "inner")
		if err := os.Mkdir(folder, 0o700); err != nil {
			t.Fatalf("Mkdir() error = %v, want nil", err)
		}
		lockRootChain(t, folder)
		lockChmod(t, parent, lockGroupWritableMode)
		lockAs(t, lockRootUID)

		lockMustRefuseAsRoot(t, db, filepath.Join(folder, "copy.db"), lockWritableByOthers(parent, lockGroupWritableMode))

		internalMustHoldOnly(t, folder)
	})
}

func TestTheRootFolderCheckStopsAtALinkLoop(t *testing.T) {
	folder := internalFolder(t)
	lockSymlink(t, filepath.Join(folder, "loop"), "loop")
	lockRootChain(t, folder)
	lockAs(t, lockRootUID)
	path := filepath.Join(folder, "loop", "inner")

	err := checkRootFolder(path)

	want := "dbkit: check the snapshot folder " + path + ": " + unix.ELOOP.Error()
	if !errors.Is(err, unix.ELOOP) || err.Error() != want {
		t.Errorf("checkRootFolder(%s) error = %v, want %q marked ELOOP", path, err, want)
	}
}

func TestASnapshotAsRootChecksEveryFolderALinkLeadsThrough(t *testing.T) {
	t.Run("a link to a folder the test user owns", func(t *testing.T) {
		db, folder := internalSnapshotOpen(t)
		linked := internalFolder(t)
		lockSymlink(t, filepath.Join(folder, "link"), linked)
		lockRootChain(t, folder)
		lockAs(t, lockRootUID)

		lockMustRefuseAsRoot(t, db, filepath.Join(folder, "link", "copy.db"), lockOwnedByTheTestUser(linked))

		internalMustHoldOnly(t, folder, "link")
		internalMustHoldOnly(t, linked)
	})
	t.Run("a link to a root folder, in a folder the test user owns", func(t *testing.T) {
		db, folder := internalSnapshotOpen(t)
		linked := internalFolder(t)
		lockSymlink(t, filepath.Join(folder, "link"), linked)
		lockRootOwns(t, lockAbove(folder)...)
		lockRootChain(t, linked)
		lockAs(t, lockRootUID)

		lockMustRefuseAsRoot(t, db, filepath.Join(folder, "link", "copy.db"), lockOwnedByTheTestUser(folder))

		internalMustHoldOnly(t, folder, "link")
		internalMustHoldOnly(t, linked)
	})
	t.Run("a link inside the target of a link, in a folder the test user owns", func(t *testing.T) {
		db, folder := internalSnapshotOpen(t)
		between, linked := internalFolder(t), internalFolder(t)
		lockSymlink(t, filepath.Join(between, "inner"), linked)
		lockSymlink(t, filepath.Join(folder, "link"), filepath.Join(between, "inner"))
		lockRootChain(t, folder)
		lockRootChain(t, linked)
		lockAs(t, lockRootUID)

		lockMustRefuseAsRoot(t, db, filepath.Join(folder, "link", "copy.db"), lockOwnedByTheTestUser(between))

		internalMustHoldOnly(t, folder, "link")
		internalMustHoldOnly(t, between, "inner")
		internalMustHoldOnly(t, linked)
	})
}

func TestASnapshotAsRootTakesAFolderOnlyRootCanChange(t *testing.T) {
	t.Run("a root folder below a root folder others can write in with the sticky bit", func(t *testing.T) {
		db, parent := internalSnapshotOpen(t)
		folder := filepath.Join(parent, "inner")
		if err := os.Mkdir(folder, 0o700); err != nil {
			t.Fatalf("Mkdir() error = %v, want nil", err)
		}
		lockRootChain(t, folder)
		lockChmod(t, parent, lockStickyOthersWritableMode)
		lockAs(t, lockRootUID)

		lockMustTakeAsRoot(t, db, filepath.Join(folder, "copy.db"))

		internalMustHoldOnly(t, folder, snapshotLockFile, "copy.db")
	})
	for _, tc := range []struct {
		// name names the link.
		name string
		// text is what the link holds, given the folder it leads to.
		text func(linked string) string
	}{
		{name: "an absolute link to a root folder", text: func(linked string) string { return linked }},
		{name: "a relative link through .. and . to a root folder",
			text: func(linked string) string { return "../" + filepath.Base(linked) + "/." }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			db, folder := internalSnapshotOpen(t)
			linked := internalFolder(t)
			lockSymlink(t, filepath.Join(folder, "link"), tc.text(linked))
			lockRootChain(t, folder)
			lockRootChain(t, linked)
			lockAs(t, lockRootUID)

			lockMustTakeAsRoot(t, db, filepath.Join(folder, "link", "copy.db"))

			internalMustHoldOnly(t, folder, "link")
			internalMustHoldOnly(t, linked, snapshotLockFile, "copy.db")
		})
	}
}

func TestASnapshotAsRootIntoAMissingFolderFailsTheCheck(t *testing.T) {
	db, folder := internalSnapshotOpen(t)
	lockRootChain(t, folder)
	lockAs(t, lockRootUID)
	missing := filepath.Join(folder, "missing")

	lockMustRefuseAsRoot(t, db, filepath.Join(missing, "copy.db"),
		"dbkit: check the snapshot folder "+missing+": "+unix.ENOENT.Error())

	internalMustHoldOnly(t, folder)
}

func TestALinkRemovedBeforeItIsReadStopsASnapshotAsRoot(t *testing.T) {
	db, folder := internalSnapshotOpen(t)
	linked := internalFolder(t)
	link := filepath.Join(folder, "link")
	lockSymlink(t, link, linked)
	lockRootChain(t, folder)
	lockRootChain(t, linked)
	kept := lstat
	lstat = func(path string, st *unix.Stat_t) error {
		err := kept(path, st)
		if path == link {
			return errors.Join(err, os.Remove(link))
		}
		return err
	}
	t.Cleanup(func() { lstat = kept })
	lockAs(t, lockRootUID)
	tries := lockTries(t)

	got, err := NewSnapshotter(db).Snapshot(t.Context(), filepath.Join(link, "copy.db"))

	want := "dbkit: read the link " + link + ": "
	if !errors.Is(err, fs.ErrNotExist) || !strings.HasPrefix(err.Error(), want) || got.Path != "" {
		t.Errorf("Snapshot() = %+v, %v, want no snapshot and an error marked ErrNotExist starting %q", got, err, want)
	}
	lockNeverTried(t, tries)
	internalMustHoldOnly(t, folder)
	internalMustHoldOnly(t, linked)
}

func TestASnapshotAsAnotherUserReadsNoFolder(t *testing.T) {
	db, folder := internalSnapshotOpen(t)
	calls := lockRootOwns(t)
	lockAs(t, lockServiceUID)
	target := filepath.Join(folder, "copy.db")

	got, err := NewSnapshotter(db).Snapshot(t.Context(), target)

	if err != nil || got.Path != target {
		t.Fatalf("Snapshot() = %+v, %v, want the path %s", got, err, target)
	}
	if n := calls.Load(); n != 0 {
		t.Errorf("the snapshot read %d folders, want none for a run that is not root", n)
	}
	internalMustHoldOnly(t, folder, snapshotLockFile, "copy.db")
}
