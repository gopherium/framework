// SPDX-License-Identifier: Apache-2.0

package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	modernc "modernc.org/sqlite"
	sqlite3 "modernc.org/sqlite/lib"

	"github.com/gopherium/framework/dbkit"
)

const (
	// internalLateTarget is what a file that appears at the target between the copy and the move holds.
	internalLateTarget = "a file written between the copy and the move"
	// internalCheckpointDeadline is the deadline of a checkpoint behind a held writer.
	internalCheckpointDeadline = 100 * time.Millisecond
	// internalWorldReadMode is the mode of a live file its owner reads and writes and everyone reads.
	internalWorldReadMode fs.FileMode = 0o644
)

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
	return internalSnapshotOpenWith(t, internalOptions())
}

// internalSnapshotOpenWith returns a handle with opts on a fresh file holding one row, and an empty target folder.
func internalSnapshotOpenWith(t *testing.T, opts Options) (*sql.DB, string) {
	t.Helper()
	db, err := Open("sqlite:"+filepath.Join(internalFolder(t), "site.db"), opts)
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

func TestTheEmptyCopyHasTheNarrowedModeBeforeItHoldsData(t *testing.T) {
	db, folder := internalSnapshotOpen(t)
	s := &snapshotter{db: db, opts: SnapshotOptions{Mode: ownerReadWrite}}
	path, _, err := s.liveFile(t.Context())
	if err != nil {
		t.Fatalf("liveFile() error = %v, want nil", err)
	}
	if err := os.Chmod(path, internalWorldReadMode); err != nil {
		t.Fatalf("Chmod(%s) error = %v, want nil", path, err)
	}
	live, err := os.Stat(path)
	if err != nil {
		t.Fatalf("Stat(%s) error = %v, want nil", path, err)
	}

	partial, err := s.startCopy(t.Context(), filepath.Join(folder, "copy.db"), live)

	if err != nil {
		t.Fatalf("startCopy() error = %v, want nil", err)
	}
	info, err := os.Lstat(partial)
	if err != nil {
		t.Fatalf("Lstat(%s) error = %v, want nil", partial, err)
	}
	if info.Mode().Perm() != ownerReadWrite || info.Size() != 0 {
		t.Errorf("the copy has the mode %v and %d bytes, want the mode %v and none", info.Mode().Perm(), info.Size(),
			ownerReadWrite)
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

func TestACheckpointEndsWithItsContext(t *testing.T) {
	t.Parallel()

	opts := internalOptions()
	opts.BusyTimeout = internalLongBusyTimeout
	db, _ := internalSnapshotOpenWith(t, opts)
	writer, err := db.BeginTx(t.Context(), nil)
	if err != nil {
		t.Fatalf("BeginTx() error = %v, want nil", err)
	}
	t.Cleanup(func() { _ = writer.Rollback() })
	if _, err := writer.ExecContext(t.Context(), "INSERT INTO snapshot_rows VALUES (2)"); err != nil {
		t.Fatalf("ExecContext() error = %v, want nil", err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), internalCheckpointDeadline)
	defer cancel()
	began := time.Now()

	_, err = (&snapshotter{db: db}).checkpoint(ctx)

	waited := time.Since(began)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("checkpoint() error = %v, want one matching context.DeadlineExceeded", err)
	}
	if waited > internalLongBusyTimeout/10 {
		t.Errorf("checkpoint() answered after %v, want within a tenth of the busy timeout %v", waited,
			internalLongBusyTimeout)
	}
}

func TestACopyWithNoPagesFailsTheCheck(t *testing.T) {
	t.Parallel()

	t.Run("an empty file", func(t *testing.T) {
		t.Parallel()
		path := filepath.Join(t.TempDir(), "copy.db"+partialSuffix)
		if err := os.WriteFile(path, nil, 0o600); err != nil {
			t.Fatalf("WriteFile() error = %v, want nil", err)
		}

		internalMustFailCheck(t, path, "dbkit: the snapshot copy "+path+" holds no pages")
	})
	t.Run("a missing file", func(t *testing.T) {
		t.Parallel()
		path := filepath.Join(t.TempDir(), "copy.db"+partialSuffix)

		internalMustFailCheck(t, path, "dbkit: the snapshot copy "+path+" holds no pages")
	})
}

func TestACopyTheDriverCannotOpenFailsTheCheck(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "missing", "copy.db"+partialSuffix)

	err := checkCopy(t.Context(), internalDriver(t), path)

	var driverErr *modernc.Error
	want := "dbkit: open the snapshot copy " + path + ": "
	if !errors.As(err, &driverErr) || !strings.HasPrefix(err.Error(), want) {
		t.Errorf("checkCopy() error = %v, want a driver error starting %q", err, want)
	}
}

func TestACopyThatIsNoDatabaseFailsTheCheck(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "copy.db"+partialSuffix)
	if err := os.WriteFile(path, []byte(strings.Repeat("not a database file ", 256)), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v, want nil", err)
	}

	err := checkCopy(t.Context(), internalDriver(t), path)

	var driverErr *modernc.Error
	want := "dbkit: check the snapshot copy " + path + ": "
	if !errors.As(err, &driverErr) {
		t.Fatalf("checkCopy() error = %v, want a driver error", err)
	}
	if driverErr.Code() != sqlite3.SQLITE_NOTADB || !strings.HasPrefix(err.Error(), want) {
		t.Errorf("checkCopy() error = %v, want SQLITE_NOTADB in an error starting %q", err, want)
	}
}

// internalAnswer is what a call of Snapshot answered.
type internalAnswer struct {
	// snap is the snapshot of the call.
	snap dbkit.Snapshot
	// err is the error of the call.
	err error
}

// internalSnapshotTo runs a snapshot of db to target and returns what it answered.
func internalSnapshotTo(t *testing.T, db *sql.DB, target string) internalAnswer {
	t.Helper()
	snap, err := NewSnapshotter(db, SnapshotOptions{}).Snapshot(t.Context(), target)
	return internalAnswer{snap: snap, err: err}
}

// internalMustAnswer fails the test unless got holds the snapshot of want and an error matching it that reads text.
func internalMustAnswer(t *testing.T, got, want internalAnswer, text string) {
	t.Helper()
	if got.snap != want.snap {
		t.Errorf("Snapshot() = %+v, want %+v", got.snap, want.snap)
	}
	if !errors.Is(got.err, want.err) {
		t.Errorf("Snapshot() error = %v, want one matching %v", got.err, want.err)
	}
	if read := fmt.Sprint(got.err); read != text {
		t.Errorf("Snapshot() error = %s, want %q", read, text)
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

	if _, err := NewSnapshotter(db, SnapshotOptions{}).Snapshot(t.Context(), target); err != nil {
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

	got := internalSnapshotTo(t, db, target)

	internalMustAnswer(t, got, internalAnswer{err: errSnapshotSyncFailed},
		"dbkit: sync the snapshot copy "+target+partialSuffix+": "+errSnapshotSyncFailed.Error())
	internalMustHoldOnly(t, folder, snapshotLockFile)
}

func TestAFailedMoveLeavesNoTarget(t *testing.T) {
	db, folder := internalSnapshotOpen(t)
	target := filepath.Join(folder, "copy.db")
	kept := rename
	rename = func(string, string) error { return errSnapshotMoveFailed }
	t.Cleanup(func() { rename = kept })

	got := internalSnapshotTo(t, db, target)

	internalMustAnswer(t, got, internalAnswer{err: errSnapshotMoveFailed},
		"dbkit: move the snapshot copy to "+target+": "+errSnapshotMoveFailed.Error())
	if errors.Is(got.err, ErrUnsupportedFileSystem) {
		t.Errorf("Snapshot() error = %v, want one that does not match ErrUnsupportedFileSystem", got.err)
	}
	internalMustHoldOnly(t, folder, snapshotLockFile)
}

func TestASecondSnapshotInTheFolderKeepsTheFirstCopy(t *testing.T) {
	db, folder := internalSnapshotOpen(t)
	first, second := filepath.Join(folder, "first.db"), filepath.Join(folder, "second.db")
	moving, proceed := make(chan struct{}), make(chan struct{})
	kept := rename
	rename = func(from, to string) error {
		if to == first {
			close(moving)
			<-proceed
		}
		return kept(from, to)
	}
	t.Cleanup(func() { rename = kept })
	result := make(chan error, 1)
	go func() {
		_, err := NewSnapshotter(db, SnapshotOptions{}).Snapshot(context.Background(), first)
		result <- err
	}()
	select {
	case <-moving:
	case err := <-result:
		t.Fatalf("the first Snapshot() returned %v before its move", err)
	}

	got := internalSnapshotTo(t, db, second)

	close(proceed)
	internalMustAnswer(t, got, internalAnswer{err: ErrSnapshotRunning},
		"dbkit: take the snapshot lock "+filepath.Join(folder, snapshotLockFile)+": "+ErrSnapshotRunning.Error())
	if err := <-result; err != nil {
		t.Errorf("the first Snapshot() error = %v, want nil", err)
	}
	internalMustHoldOnly(t, folder, snapshotLockFile, "first.db")
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

	got, err := NewSnapshotter(db, SnapshotOptions{}).Snapshot(t.Context(), target)

	if got != (dbkit.Snapshot{}) {
		t.Errorf("Snapshot() = %+v, want no snapshot", got)
	}
	if read, want := fmt.Sprint(err), "dbkit: the snapshot target already exists: "+target; read != want {
		t.Errorf("Snapshot() error = %s, want %q", read, want)
	}
	if !errors.Is(err, ErrSnapshotExists) {
		t.Errorf("Snapshot() error = %v, want one matching ErrSnapshotExists", err)
	}
	if data, err := os.ReadFile(target); err != nil || string(data) != internalLateTarget {
		t.Errorf("the target holds %d bytes, %v after the snapshot, want the %d bytes written during the copy",
			len(data), err, len(internalLateTarget))
	}
	internalMustHoldOnly(t, folder, snapshotLockFile, "copy.db")
}

func TestAFailedFolderSyncKeepsTheCopy(t *testing.T) {
	db, folder := internalSnapshotOpen(t)
	target := filepath.Join(folder, "copy.db")
	internalFailSync(t, folder)

	got := internalSnapshotTo(t, db, target)

	internalMustAnswer(t, got, internalAnswer{snap: dbkit.Snapshot{Path: target}, err: errSnapshotSyncFailed},
		ErrSnapshotUnsynced.Error()+": "+target+": "+errSnapshotSyncFailed.Error())
	if !errors.Is(got.err, ErrSnapshotUnsynced) {
		t.Errorf("Snapshot() error = %v, want one matching ErrSnapshotUnsynced", got.err)
	}
	internalMustHoldOnly(t, folder, snapshotLockFile, "copy.db")
}
