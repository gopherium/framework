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
	// partialJournalSuffix ends the name of the rollback journal SQLite keeps beside a copy while it writes it.
	partialJournalSuffix = partialSuffix + "-journal"
	// quickCheckPassed is the one report PRAGMA quick_check gives a sound database.
	quickCheckPassed = "ok"
	// snapshotLockFile is the name of the lock file a snapshot holds in its target folder.
	snapshotLockFile = ".dbkit-snapshot.lock"
	// snapshotLockName names the snapshot lock in its errors.
	snapshotLockName = "snapshot lock"
)

// ErrSnapshotRunning is the error of a snapshot into a folder another snapshot is writing into.
var ErrSnapshotRunning = errors.New("dbkit: another snapshot is writing into this folder")

var (
	// syncFile writes the data of an open file to its disk.
	syncFile = (*os.File).Sync
	// rename moves a file to a new name, and fails when a file holds that name.
	rename = renameNoReplace
	// errNoSnapshotHandle refuses a snapshot of a nil handle.
	errNoSnapshotHandle = errors.New("dbkit: NewSnapshotter needs a database handle, got nil")
)

// snapshotter writes snapshots of the database of one handle.
type snapshotter struct {
	// db is the handle whose database is copied.
	db *sql.DB
}

// NewSnapshotter returns a Snapshotter of the database of db.
func NewSnapshotter(db *sql.DB) dbkit.Snapshotter {
	return &snapshotter{db: db}
}

// Snapshot moves a checked copy of the database to target and checkpoints the log, with Path set once target holds it.
func (s *snapshotter) Snapshot(ctx context.Context, target string) (dbkit.Snapshot, error) {
	if s.db == nil {
		return dbkit.Snapshot{}, errNoSnapshotHandle
	}
	if err := s.copyLocked(ctx, target); err != nil {
		return dbkit.Snapshot{}, err
	}
	if err := syncPath(filepath.Dir(target)); err != nil {
		return dbkit.Snapshot{Path: target}, fmt.Errorf("dbkit: sync the folder of the snapshot %s: %w", target, err)
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

// prepare checks target, takes the snapshot lock of its folder and starts its copy, and returns the copy's path.
func (s *snapshotter) prepare(ctx context.Context, target string) (string, *fileLocker, error) {
	if err := checkTarget(target); err != nil {
		return "", nil, err
	}
	path, live, err := s.liveFile(ctx)
	if err != nil {
		return "", nil, err
	}
	folder := filepath.Dir(target)
	locker := &fileLocker{name: snapshotLockName, path: filepath.Join(folder, snapshotLockFile), database: path}
	if err := locker.tryLock(ErrSnapshotRunning); err != nil {
		return "", nil, err
	}
	partial, err := startCopy(target, live)
	if err != nil {
		return "", nil, errors.Join(err, locker.unlock())
	}
	return partial, locker, nil
}

// startCopy clears the stale copies beside target and creates its empty copy, and returns the copy's path.
func startCopy(target string, live fs.FileInfo) (string, error) {
	if err := removePartials(filepath.Dir(target), live); err != nil {
		return "", err
	}
	partial := target + partialSuffix
	if err := createEmpty(partial, live.Mode().Perm()); err != nil {
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
	case err != nil:
		return fmt.Errorf("dbkit: move the snapshot copy to %s: %w", target, err)
	}
	return nil
}

// existingTarget returns the error for a target a file already holds.
func existingTarget(target string) error {
	return fmt.Errorf("dbkit: the snapshot target %s already exists", target)
}

// checkTarget returns the error for a target Snapshot refuses.
func checkTarget(target string) error {
	switch {
	case strings.HasPrefix(target, "file:"):
		return fmt.Errorf("dbkit: the snapshot target must be a plain path, not a file: URI, got %q", target)
	case !filepath.IsAbs(target):
		return fmt.Errorf("dbkit: the snapshot target must be an absolute path, got %q", target)
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

// removePartials removes regular copy and journal files in folder, except the live database and files it cannot stat.
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
		if info, err := entry.Info(); err != nil || !info.Mode().IsRegular() || os.SameFile(info, live) {
			continue
		}
		path := filepath.Join(folder, name)
		if err := os.Remove(path); err != nil {
			return fmt.Errorf("dbkit: remove the stale snapshot copy %s: %w", path, err)
		}
	}
	return nil
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
