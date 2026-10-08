// SPDX-License-Identifier: Apache-2.0

//go:build linux || darwin

package sqlite_test

import (
	"database/sql"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"

	"github.com/gopherium/framework/dbkit"
	"github.com/gopherium/framework/dbkit/sqlite"
)

// snapshotListOnlyMode is the mode of a folder its owner may enter and write in but never list.
const snapshotListOnlyMode fs.FileMode = 0o300

// snapshotLockOpen returns a handle on a fresh file holding one table, and an empty folder for the targets.
func snapshotLockOpen(t *testing.T) (*sql.DB, string) {
	t.Helper()
	db, _ := snapshotOpen(t, testOptions())
	rulesExec(t, db, "CREATE TABLE snapshot_rows (a INTEGER NOT NULL)")
	return db, snapshotFolder(t)
}

func TestSnapshotKeepsTheJournalOfALiveDatabaseNamedAsACopy(t *testing.T) {
	t.Parallel()

	folder := snapshotFolder(t)
	live := filepath.Join(folder, "site.db"+snapshotPartial)
	db, err := sql.Open("sqlite", live)
	if err != nil {
		t.Fatalf("sql.Open() error = %v, want nil", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	rulesExec(t, db, "CREATE TABLE snapshot_rows (a INTEGER NOT NULL)")
	writer, err := db.BeginTx(t.Context(), nil)
	if err != nil {
		t.Fatalf("BeginTx() error = %v, want nil", err)
	}
	t.Cleanup(func() { _ = writer.Rollback() })
	if _, err := writer.ExecContext(t.Context(), "INSERT INTO snapshot_rows VALUES (1)"); err != nil {
		t.Fatalf("insert in the open write transaction: %v", err)
	}
	journal := live + "-journal"
	mustExist(t, journal)
	target := filepath.Join(folder, "copy.db")

	got, err := sqlite.NewSnapshotter(db).Snapshot(t.Context(), target)

	if err != nil || got.Path != target {
		t.Fatalf("Snapshot() = %+v, %v, want the path %s", got, err, target)
	}
	mustExist(t, journal)
	if err := writer.Commit(); err != nil {
		t.Errorf("Commit() of the open write transaction error = %v, want nil", err)
	}
}

func TestTheSnapshotLockIsPrivateAndKept(t *testing.T) {
	t.Parallel()

	db, folder := snapshotLockOpen(t)
	lock := filepath.Join(folder, snapshotLock)
	if _, err := sqlite.NewSnapshotter(db).Snapshot(t.Context(), filepath.Join(folder, "first.db")); err != nil {
		t.Fatalf("Snapshot() of the first target error = %v, want nil", err)
	}
	first := migrateLockInfo(t, lock)

	if _, err := sqlite.NewSnapshotter(db).Snapshot(t.Context(), filepath.Join(folder, "second.db")); err != nil {
		t.Fatalf("Snapshot() of the second target error = %v, want nil", err)
	}

	second := migrateLockInfo(t, lock)
	if got := second.Mode(); got != 0o600 {
		t.Errorf("the snapshot lock's mode = %v, want a regular file with mode 0600", got)
	}
	if !os.SameFile(first, second) {
		t.Errorf("the snapshot lock changed between snapshots, want the one file kept")
	}
}

func TestSnapshotRefusesALockPathItCannotOpen(t *testing.T) {
	t.Parallel()

	db, folder := snapshotLockOpen(t)
	lock := filepath.Join(folder, snapshotLock)
	if err := os.Mkdir(lock, 0o700); err != nil {
		t.Fatalf("Mkdir() error = %v, want nil", err)
	}

	got, err := sqlite.NewSnapshotter(db).Snapshot(t.Context(), filepath.Join(folder, "copy.db"))

	want := "dbkit: open the snapshot lock " + lock + ": "
	if !errors.Is(err, syscall.EISDIR) || !strings.HasPrefix(err.Error(), want) || got != (dbkit.Snapshot{}) {
		t.Errorf("Snapshot() = %+v, %v, want no snapshot and an error marked EISDIR starting %q", got, err, want)
	}
	snapshotMustHoldOnly(t, folder, snapshotLock)
}

func TestSnapshotNeverFollowsALinkAtTheLockPath(t *testing.T) {
	t.Parallel()

	db, folder := snapshotLockOpen(t)
	lock := filepath.Join(folder, snapshotLock)
	missing := filepath.Join(snapshotFolder(t), "missing")
	if err := os.Symlink(missing, lock); err != nil {
		t.Fatalf("Symlink() error = %v, want nil", err)
	}

	got, err := sqlite.NewSnapshotter(db).Snapshot(t.Context(), filepath.Join(folder, "copy.db"))

	want := "dbkit: open the snapshot lock " + lock + ": "
	if !errors.Is(err, syscall.ELOOP) || !strings.HasPrefix(err.Error(), want) || got != (dbkit.Snapshot{}) {
		t.Errorf("Snapshot() = %+v, %v, want no snapshot and an error marked ELOOP starting %q", got, err, want)
	}
	mustNotExist(t, missing)
	snapshotMustHoldOnly(t, folder, snapshotLock)
}

func TestSnapshotRefusesAFIFOAtTheLockPath(t *testing.T) {
	t.Parallel()

	db, folder := snapshotLockOpen(t)
	lock := filepath.Join(folder, snapshotLock)
	if err := syscall.Mkfifo(lock, 0o600); err != nil {
		t.Fatalf("Mkfifo() error = %v, want nil", err)
	}
	var got dbkit.Snapshot

	err := migrateAtOnce(t, func() error {
		var err error
		got, err = sqlite.NewSnapshotter(db).Snapshot(t.Context(), filepath.Join(folder, "copy.db"))
		return err
	})

	want := "dbkit: the snapshot lock " + lock + " must be a regular file with one link"
	if err == nil || err.Error() != want || got != (dbkit.Snapshot{}) {
		t.Errorf("Snapshot() = %+v, %v, want no snapshot and %q", got, err, want)
	}
	snapshotMustHoldOnly(t, folder, snapshotLock)
}

func TestSnapshotTakesALockFileThatAlreadyExists(t *testing.T) {
	t.Parallel()

	db, folder := snapshotLockOpen(t)
	lock := filepath.Join(folder, snapshotLock)
	if err := os.WriteFile(lock, nil, 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v, want nil", err)
	}
	before := migrateLockInfo(t, lock)
	target := filepath.Join(folder, "copy.db")

	got, err := sqlite.NewSnapshotter(db).Snapshot(t.Context(), target)

	if err != nil || got.Path != target {
		t.Fatalf("Snapshot() = %+v, %v, want the path %s", got, err, target)
	}
	if !os.SameFile(before, migrateLockInfo(t, lock)) {
		t.Errorf("the snapshot lock changed, want the existing regular file kept as the lock")
	}
	snapshotMustHoldOnly(t, folder, snapshotLock, "copy.db")
}

func TestSnapshotRefusesAFolderItCannotList(t *testing.T) {
	t.Parallel()

	if os.Geteuid() == 0 {
		t.Skip("root lists every folder")
	}
	db, folder := snapshotLockOpen(t)
	if err := os.Chmod(folder, snapshotListOnlyMode); err != nil {
		t.Fatalf("Chmod(%s) error = %v, want nil", folder, err)
	}
	t.Cleanup(func() { _ = os.Chmod(folder, 0o700) })

	got, err := sqlite.NewSnapshotter(db).Snapshot(t.Context(), filepath.Join(folder, "copy.db"))

	want := "dbkit: list the snapshot folder " + folder + ": "
	if !errors.Is(err, fs.ErrPermission) || !strings.HasPrefix(err.Error(), want) || got != (dbkit.Snapshot{}) {
		t.Errorf("Snapshot() = %+v, %v, want no snapshot and an error marked ErrPermission starting %q", got, err, want)
	}
	if err := os.Chmod(folder, 0o700); err != nil {
		t.Fatalf("Chmod(%s) error = %v, want nil", folder, err)
	}
	snapshotMustHoldOnly(t, folder, snapshotLock)
}
