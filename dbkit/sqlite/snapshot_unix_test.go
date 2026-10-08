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
