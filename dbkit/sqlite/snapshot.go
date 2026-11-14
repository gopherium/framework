// SPDX-License-Identifier: Apache-2.0

package sqlite

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/gopherium/framework/dbkit"
)

const (
	// vacuumInto copies the database into the file named by its one argument.
	vacuumInto = "VACUUM INTO ?"
	// copyCheck reads the quick_check report and the page count of a copy in one row.
	copyCheck = "SELECT (SELECT group_concat(quick_check, ', ') FROM pragma_quick_check), " +
		"(SELECT page_count FROM pragma_page_count)"
	// walCheckpoint checkpoints the write-ahead log and truncates it.
	walCheckpoint = "PRAGMA wal_checkpoint(TRUNCATE)"
	// partialSuffix ends the name a snapshot gives its copy until the copy is checked and moved to its target.
	partialSuffix = ".dbkit-snapshot.partial"
	// journalSuffix ends the name of the rollback journal SQLite keeps beside a database file.
	journalSuffix = "-journal"
	// partialJournalSuffix ends the name of the rollback journal SQLite keeps beside a copy while it writes it.
	partialJournalSuffix = partialSuffix + journalSuffix
	// quickCheckPassed is the one report PRAGMA quick_check gives a sound database.
	quickCheckPassed = "ok"
	// snapshotLockFile is the name of the lock file a snapshot holds in its target folder.
	snapshotLockFile = ".dbkit-snapshot.lock"
	// snapshotLockName names the snapshot lock in its errors.
	snapshotLockName = "snapshot lock"
	// localFolderAdvice tells the owner of a refused snapshot folder what to do instead.
	localFolderAdvice = "write snapshots to a local folder, then copy them off the machine"
	// ownerReadWrite holds the owner's read and write permission bits.
	ownerReadWrite fs.FileMode = 0o600
)

var (
	// ErrSnapshotRunning is the error of a snapshot into a folder another snapshot is writing into.
	ErrSnapshotRunning = errors.New("dbkit: another snapshot is writing into this folder")
	// ErrSnapshotExists is the error of a snapshot whose target a file already holds.
	ErrSnapshotExists = errors.New("dbkit: the snapshot target already exists")
	// ErrSnapshotUnsynced is the error of a snapshot whose copy is in place but whose folder failed to sync.
	ErrSnapshotUnsynced = errors.New("dbkit: the snapshot is in place but its folder failed to sync")
)

var (
	// syncFile writes the data of an open file to its disk.
	syncFile = (*os.File).Sync
	// rename moves a file to a new name, and fails when a file holds that name.
	rename = renameNoReplace
	// errNoSnapshotHandle refuses a snapshot of a nil handle.
	errNoSnapshotHandle = errors.New("dbkit: NewSnapshotter needs a database handle, got nil")
)

// SnapshotOptions configures the snapshots of one Snapshotter.
type SnapshotOptions struct {
	// Margin is the bytes a snapshot leaves free in its target folder beside the copy, 0 or more.
	Margin int64
	// Mode narrows the permission bits of each copy below the live file's and keeps the owner's read and write, or is 0.
	Mode fs.FileMode
}

// check returns the error for options Snapshot refuses.
func (o SnapshotOptions) check() error {
	switch {
	case o.Margin < 0:
		return fmt.Errorf("dbkit: the snapshot option Margin must be 0 or more, got %d", o.Margin)
	case o.Mode&^fs.ModePerm != 0:
		return fmt.Errorf("dbkit: the snapshot option Mode must hold permission bits only, got %v", o.Mode)
	case o.Mode != 0 && o.Mode&ownerReadWrite != ownerReadWrite:
		return fmt.Errorf("dbkit: the snapshot option Mode must keep the owner's read and write bits, got %v", o.Mode)
	}
	return nil
}

// copyMode returns the permission bits of a copy of a live file with the mode live.
func (o SnapshotOptions) copyMode(live fs.FileMode) fs.FileMode {
	if o.Mode == 0 {
		return live.Perm()
	}
	return live.Perm() & o.Mode
}

// snapshotter writes snapshots of the database of one handle.
type snapshotter struct {
	// db is the handle whose database is copied.
	db *sql.DB
	// opts configures each snapshot.
	opts SnapshotOptions
}

// NewSnapshotter returns a Snapshotter of the database of db with opts.
func NewSnapshotter(db *sql.DB, opts SnapshotOptions) dbkit.Snapshotter {
	return &snapshotter{db: db, opts: opts}
}

// Snapshot moves a checked copy of the database to target and checkpoints the log, with Path set once target holds it.
func (s *snapshotter) Snapshot(ctx context.Context, target string) (dbkit.Snapshot, error) {
	if s.db == nil {
		return dbkit.Snapshot{}, errNoSnapshotHandle
	}
	if err := s.opts.check(); err != nil {
		return dbkit.Snapshot{}, err
	}
	if err := s.copyLocked(ctx, target); err != nil {
		return dbkit.Snapshot{}, err
	}
	if err := syncPath(filepath.Dir(target)); err != nil {
		return dbkit.Snapshot{Path: target}, fmt.Errorf("%w: %s: %w", ErrSnapshotUnsynced, target, err)
	}
	c, err := s.checkpoint(ctx)
	if err != nil {
		err = fmt.Errorf("dbkit: checkpoint the write-ahead log after the snapshot %s: %w", target, err)
	}
	return dbkit.Snapshot{Path: target, Checkpoint: c}, err
}

// copyLocked moves a checked copy of the database to target while it holds the snapshot lock of the target's folder.
func (s *snapshotter) copyLocked(ctx context.Context, target string) (err error) {
	partial, locker, err := s.prepare(ctx, target)
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, locker.unlock()) }()
	if err := s.writeCopy(ctx, partial, target); err != nil {
		return errors.Join(err, os.Remove(partial))
	}
	return nil
}

// prepare checks target and its folders, takes the snapshot lock and starts the copy, and returns the copy's path.
func (s *snapshotter) prepare(ctx context.Context, target string) (string, *fileLocker, error) {
	if err := checkTarget(target); err != nil {
		return "", nil, err
	}
	folder := filepath.Dir(target)
	if err := checkFolders(folder); err != nil {
		return "", nil, err
	}
	path, live, err := s.liveFile(ctx)
	if err != nil {
		return "", nil, err
	}
	locker := &fileLocker{name: snapshotLockName, path: filepath.Join(folder, snapshotLockFile), database: path}
	if err := locker.tryLock(ErrSnapshotRunning); err != nil {
		return "", nil, err
	}
	partial, err := s.startCopy(ctx, target, live)
	if err != nil {
		return "", nil, errors.Join(err, locker.unlock())
	}
	return partial, locker, nil
}

// startCopy clears the stale copies beside target, checks the free space, creates the empty copy and returns its path.
func (s *snapshotter) startCopy(ctx context.Context, target string, live fs.FileInfo) (string, error) {
	folder := filepath.Dir(target)
	if err := removePartials(folder, live); err != nil {
		return "", err
	}
	if err := s.checkSpace(ctx, folder); err != nil {
		return "", err
	}
	partial := target + partialSuffix
	if err := createEmpty(partial, s.opts.copyMode(live.Mode())); err != nil {
		return "", fmt.Errorf("dbkit: create the snapshot copy %s: %w", partial, err)
	}
	return partial, nil
}

// liveFile returns the path and the facts of the main database file of the handle.
func (s *snapshotter) liveFile(ctx context.Context) (string, fs.FileInfo, error) {
	var path string
	if err := s.db.QueryRowContext(ctx, mainPath).Scan(&path); err != nil {
		return "", nil, fmt.Errorf("dbkit: read the database path: %w", err)
	}
	info, err := os.Stat(path)
	if err != nil {
		return "", nil, fmt.Errorf("dbkit: read the database file %s: %w", path, err)
	}
	return path, info, nil
}

// checkpoint runs PRAGMA wal_checkpoint(TRUNCATE) and returns its row with how long it ran.
func (s *snapshotter) checkpoint(ctx context.Context) (dbkit.Checkpoint, error) {
	var c dbkit.Checkpoint
	began := time.Now()
	err := s.db.QueryRowContext(ctx, walCheckpoint).Scan(&c.Busy, &c.LogFrames, &c.CheckpointedFrames)
	c.Took = time.Since(began)
	return c, err
}

// writeCopy copies the database to partial, checks the copy and moves it to target.
func (s *snapshotter) writeCopy(ctx context.Context, partial, target string) error {
	if _, err := s.db.ExecContext(ctx, vacuumInto, partial); err != nil {
		return fmt.Errorf("dbkit: copy the database to %s: %w", partial, err)
	}
	if err := checkCopy(ctx, s.db.Driver(), partial); err != nil {
		return err
	}
	if err := syncPath(partial); err != nil {
		return fmt.Errorf("dbkit: sync the snapshot copy %s: %w", partial, err)
	}
	err := rename(partial, target)
	switch {
	case errors.Is(err, fs.ErrExist):
		return existingTarget(target)
	case errors.Is(err, noReplaceUnsupported):
		return &UnsupportedFileSystemError{Folder: filepath.Dir(target), Err: err}
	case err != nil:
		return fmt.Errorf("dbkit: move the snapshot copy to %s: %w", target, err)
	}
	return nil
}

// existingTarget returns the error for a target a file already holds.
func existingTarget(target string) error {
	return fmt.Errorf("%w: %s", ErrSnapshotExists, target)
}

// checkTarget returns the error for a target Snapshot refuses.
func checkTarget(target string) error {
	switch {
	case strings.HasPrefix(target, "file:"):
		return fmt.Errorf("dbkit: the snapshot target must be a plain path, not a file: URI, got %q", target)
	case !filepath.IsAbs(target):
		return fmt.Errorf("dbkit: the snapshot target must be an absolute path, got %q", target)
	case target != filepath.Clean(target):
		return fmt.Errorf("dbkit: the snapshot target must be a clean path, got %q", target)
	case strings.Contains(target, "?"):
		return fmt.Errorf("dbkit: the snapshot target must hold no ?, got %q", target)
	case strings.HasSuffix(target, partialSuffix):
		return fmt.Errorf("dbkit: the snapshot target must not end with %s, got %q", partialSuffix, target)
	case strings.HasSuffix(target, partialJournalSuffix):
		return fmt.Errorf("dbkit: the snapshot target must not end with %s, got %q", partialJournalSuffix, target)
	}
	_, err := os.Lstat(target)
	switch {
	case err == nil:
		return existingTarget(target)
	case !errors.Is(err, fs.ErrNotExist):
		return fmt.Errorf("dbkit: check the snapshot target: %w", err)
	}
	return nil
}

// checkFolders returns the error for a snapshot folder Snapshot refuses, or for a folder above it as root.
func checkFolders(folder string) error {
	if err := checkRootFolder(folder); err != nil {
		return err
	}
	name, err := snapshotSystem(folder)
	switch {
	case err != nil:
		return fmt.Errorf("dbkit: read the file system of the snapshot folder %s: %w", folder, err)
	case name != "":
		return &UnsupportedFileSystemError{Folder: folder, FileSystem: name}
	}
	return nil
}

// removePartials removes the regular copies and copy journals in folder, never the live database or its journal.
func removePartials(folder string, live fs.FileInfo) error {
	entries, err := os.ReadDir(folder)
	if err != nil {
		return fmt.Errorf("dbkit: list the snapshot folder %s: %w", folder, err)
	}
	for _, entry := range entries {
		name := entry.Name()
		if !strings.HasSuffix(name, partialSuffix) && !strings.HasSuffix(name, partialJournalSuffix) {
			continue
		}
		path := filepath.Join(folder, name)
		if info, err := entry.Info(); err != nil || !info.Mode().IsRegular() || isLive(path, info, live) {
			continue
		}
		if err := os.Remove(path); err != nil {
			return fmt.Errorf("dbkit: remove the stale snapshot copy %s: %w", path, err)
		}
	}
	return nil
}

// isLive reports whether the file at path with the facts info is the live database file or its rollback journal.
func isLive(path string, info, live fs.FileInfo) bool {
	if os.SameFile(info, live) {
		return true
	}
	database, ok := strings.CutSuffix(path, journalSuffix)
	if !ok {
		return false
	}
	databaseInfo, err := os.Stat(database)
	return err == nil && os.SameFile(databaseInfo, live)
}

// checkCopy runs PRAGMA quick_check on the copy at path through drv.
func checkCopy(ctx context.Context, drv driver.Driver, path string) (err error) {
	conn, err := drv.Open(path)
	if err != nil {
		return fmt.Errorf("dbkit: open the snapshot copy %s: %w", path, err)
	}
	defer func() { err = errors.Join(err, conn.Close()) }()
	report, pages, err := readCheck(ctx, conn.(driver.QueryerContext))
	switch {
	case err != nil:
		return fmt.Errorf("dbkit: check the snapshot copy %s: %w", path, err)
	case pages < 1:
		return fmt.Errorf("dbkit: the snapshot copy %s holds no pages", path)
	case report != quickCheckPassed:
		return fmt.Errorf("dbkit: the snapshot copy %s failed PRAGMA quick_check: %s", path, report)
	}
	return nil
}

// readCheck returns the quick_check report and the page count of the database of conn.
func readCheck(ctx context.Context, conn driver.QueryerContext) (report string, pages int64, err error) {
	rows, err := conn.QueryContext(ctx, copyCheck, nil)
	if err != nil {
		return "", 0, err
	}
	defer func() { err = errors.Join(err, rows.Close()) }()
	values := make([]driver.Value, len(rows.Columns()))
	err = rows.Next(values)
	report, _ = values[0].(string)
	pages, _ = values[1].(int64)
	return report, pages, err
}

// createEmpty creates an empty file at path with the permission bits perm, and fails when a file holds that name.
func createEmpty(path string, perm fs.FileMode) error {
	f, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE|os.O_EXCL, perm)
	if err != nil {
		return err
	}
	return f.Close()
}

// syncPath writes the file or folder at path to its disk.
func syncPath(path string) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	return errors.Join(syncFile(f), f.Close())
}
