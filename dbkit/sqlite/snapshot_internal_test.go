// SPDX-License-Identifier: Apache-2.0

package sqlite

import (
	"database/sql"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	modernc "modernc.org/sqlite"
	sqlite3 "modernc.org/sqlite/lib"

	"github.com/gopherium/framework/dbkit"
)

// internalLateTarget is what a file that appears at the target between the copy and the move holds.
const internalLateTarget = "a file written between the copy and the move"

var (
	// errSnapshotSyncFailed is the error a chosen sync answers with.
	errSnapshotSyncFailed = errors.New("snapshot test: the sync failed on purpose")
	// errSnapshotMoveFailed is the error a chosen move answers with.
	errSnapshotMoveFailed = errors.New("snapshot test: the move failed on purpose")
)

// internalFolder returns a fresh folder with every link resolved.
func internalFolder(t *testing.T) string {
	t.Helper()
	folder, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatalf("EvalSymlinks() error = %v, want nil", err)
	}
	return folder
}

// internalSnapshotOpen returns a handle on a fresh file holding one row, and an empty folder for the targets.
func internalSnapshotOpen(t *testing.T) (*sql.DB, string) {
	t.Helper()
	db, err := Open("sqlite:"+filepath.Join(internalFolder(t), "site.db"), internalOptions())
	if err != nil {
		t.Fatalf("Open() error = %v, want nil", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	for _, query := range []string{
		"CREATE TABLE snapshot_rows (a INTEGER NOT NULL)",
		"INSERT INTO snapshot_rows VALUES (1)",
	} {
		if _, err := db.ExecContext(t.Context(), query); err != nil {
			t.Fatalf("ExecContext(%q) error = %v, want nil", query, err)
		}
	}
	return db, internalFolder(t)
}

// internalFailSync puts in place of syncFile one that answers errSnapshotSyncFailed for the file or folder at path.
func internalFailSync(t *testing.T, path string) {
	t.Helper()
	kept := syncFile
	syncFile = func(f *os.File) error {
		if f.Name() == path {
			return errSnapshotSyncFailed
		}
		return kept(f)
	}
	t.Cleanup(func() { syncFile = kept })
}

// internalMustHoldOnly fails the test unless folder holds exactly the names want.
func internalMustHoldOnly(t *testing.T, folder string, want ...string) {
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
		t.Errorf("ReadDir(%s) = %v, want %v", folder, names, want)
	}
}

// internalDriver returns the driver value of the nil function list.
func internalDriver(t *testing.T) *modernc.Driver {
	t.Helper()
	drv, err := driverFor(nil)
	if err != nil {
		t.Fatalf("driverFor(nil) error = %v, want nil", err)
	}
	return drv
}

// internalMustFailCheck fails the test unless checkCopy of the copy at path answers want.
func internalMustFailCheck(t *testing.T, path, want string) {
	t.Helper()
	if err := checkCopy(t.Context(), internalDriver(t), path); err == nil || err.Error() != want {
		t.Errorf("checkCopy(%s) error = %v, want %q", path, err, want)
	}
}

func TestACopyWithNoPagesFailsTheCheck(t *testing.T) {
	t.Parallel()

	t.Run("an empty file", func(t *testing.T) {
		t.Parallel()
		path := filepath.Join(t.TempDir(), "copy.db.partial")
		if err := os.WriteFile(path, nil, 0o600); err != nil {
			t.Fatalf("WriteFile() error = %v, want nil", err)
		}

		internalMustFailCheck(t, path, "dbkit: the snapshot copy "+path+" holds no pages")
	})
	t.Run("a missing file", func(t *testing.T) {
		t.Parallel()
		path := filepath.Join(t.TempDir(), "copy.db.partial")

		internalMustFailCheck(t, path, "dbkit: the snapshot copy "+path+" holds no pages")
	})
}

func TestACopyTheDriverCannotOpenFailsTheCheck(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "missing", "copy.db.partial")

	err := checkCopy(t.Context(), internalDriver(t), path)

	var driverErr *modernc.Error
	want := "dbkit: open the snapshot copy " + path + ": "
	if !errors.As(err, &driverErr) || !strings.HasPrefix(err.Error(), want) {
		t.Errorf("checkCopy() error = %v, want a driver error starting %q", err, want)
	}
}

func TestACopyThatIsNoDatabaseFailsTheCheck(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "copy.db.partial")
	if err := os.WriteFile(path, []byte(strings.Repeat("not a database file ", 256)), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v, want nil", err)
	}

	err := checkCopy(t.Context(), internalDriver(t), path)

	var driverErr *modernc.Error
	want := "dbkit: check the snapshot copy " + path + ": "
	if !errors.As(err, &driverErr) || driverErr.Code() != sqlite3.SQLITE_NOTADB || !strings.HasPrefix(err.Error(), want) {
		t.Errorf("checkCopy() error = %v, want SQLITE_NOTADB in an error starting %q", err, want)
	}
}

func TestSyncPathFailsOnAMissingFile(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "missing.db")

	if err := syncPath(path); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("syncPath(%s) error = %v, want ErrNotExist", path, err)
	}
}

func TestSnapshotSyncsTheCopyBeforeTheMoveAndTheFolderAfter(t *testing.T) {
	db, folder := internalSnapshotOpen(t)
	target := filepath.Join(folder, "copy.db")
	var steps []string
	keptSync, keptRename := syncFile, rename
	syncFile = func(f *os.File) error {
		steps = append(steps, "sync "+f.Name())
		return keptSync(f)
	}
	rename = func(from, to string) error {
		steps = append(steps, "move "+from+" to "+to)
		return keptRename(from, to)
	}
	t.Cleanup(func() { syncFile, rename = keptSync, keptRename })

	if _, err := NewSnapshotter(db).Snapshot(t.Context(), target); err != nil {
		t.Fatalf("Snapshot() error = %v, want nil", err)
	}

	partial := target + partialSuffix
	want := []string{"sync " + partial, "move " + partial + " to " + target, "sync " + folder}
	if !slices.Equal(steps, want) {
		t.Errorf("Snapshot() took the steps %q, want %q", steps, want)
	}
}

func TestAFailedCopySyncLeavesNoTarget(t *testing.T) {
	db, folder := internalSnapshotOpen(t)
	target := filepath.Join(folder, "copy.db")
	internalFailSync(t, target+partialSuffix)

	got, err := NewSnapshotter(db).Snapshot(t.Context(), target)

	want := "dbkit: sync the snapshot copy " + target + partialSuffix + ": " + errSnapshotSyncFailed.Error()
	if !errors.Is(err, errSnapshotSyncFailed) || err.Error() != want || got != (dbkit.Snapshot{}) {
		t.Errorf("Snapshot() = %+v, %v, want no snapshot and %q", got, err, want)
	}
	internalMustHoldOnly(t, folder)
}

func TestAFailedMoveLeavesNoTarget(t *testing.T) {
	db, folder := internalSnapshotOpen(t)
	target := filepath.Join(folder, "copy.db")
	kept := rename
	rename = func(string, string) error { return errSnapshotMoveFailed }
	t.Cleanup(func() { rename = kept })

	got, err := NewSnapshotter(db).Snapshot(t.Context(), target)

	want := "dbkit: move the snapshot copy to " + target + ": " + errSnapshotMoveFailed.Error()
	if !errors.Is(err, errSnapshotMoveFailed) || err.Error() != want || got != (dbkit.Snapshot{}) {
		t.Errorf("Snapshot() = %+v, %v, want no snapshot and %q", got, err, want)
	}
	internalMustHoldOnly(t, folder)
}

func TestSnapshotKeepsATargetMadeDuringTheCopy(t *testing.T) {
	db, folder := internalSnapshotOpen(t)
	target := filepath.Join(folder, "copy.db")
	kept := rename
	rename = func(from, to string) error {
		if err := os.WriteFile(to, []byte(internalLateTarget), 0o600); err != nil {
			return err
		}
		return kept(from, to)
	}
	t.Cleanup(func() { rename = kept })

	got, err := NewSnapshotter(db).Snapshot(t.Context(), target)

	want := "dbkit: the snapshot target " + target + " already exists"
	if err == nil || err.Error() != want || got != (dbkit.Snapshot{}) {
		t.Errorf("Snapshot() = %+v, %v, want no snapshot and %q", got, err, want)
	}
	if data, err := os.ReadFile(target); err != nil || string(data) != internalLateTarget {
		t.Errorf("the target holds %d bytes, %v after the snapshot, want the %d bytes written during the copy",
			len(data), err, len(internalLateTarget))
	}
	internalMustHoldOnly(t, folder, "copy.db")
}

func TestAFailedFolderSyncKeepsTheCopy(t *testing.T) {
	db, folder := internalSnapshotOpen(t)
	target := filepath.Join(folder, "copy.db")
	internalFailSync(t, folder)

	got, err := NewSnapshotter(db).Snapshot(t.Context(), target)

	want := "dbkit: sync the folder of the snapshot " + target + ": " + errSnapshotSyncFailed.Error()
	if !errors.Is(err, errSnapshotSyncFailed) || err.Error() != want || got != (dbkit.Snapshot{Path: target}) {
		t.Errorf("Snapshot() = %+v, %v, want the path %s with no checkpoint and %q", got, err, target, want)
	}
	internalMustHoldOnly(t, folder, "copy.db")
}
