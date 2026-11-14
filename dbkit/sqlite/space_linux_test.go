// SPDX-License-Identifier: Apache-2.0

package sqlite

import (
	"database/sql"
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strconv"
	"testing"

	"golang.org/x/sys/unix"

	"github.com/gopherium/framework/dbkit"
)

const (
	// spaceBlock is the block size a faked file system reports.
	spaceBlock = 4096
	// spaceMargin is a margin of two blocks.
	spaceMargin = 2 * spaceBlock
	// spacePlenty is a free count no test database comes near.
	spacePlenty = 1 << 40
	// spaceUsedQuery reads the bytes the used pages of the main database hold.
	spaceUsedQuery = "SELECT (page_count - freelist_count) * page_size " +
		"FROM pragma_page_count, pragma_freelist_count, pragma_page_size"
)

// spaceUsed returns the bytes the used pages of the database of db hold.
func spaceUsed(t *testing.T, db *sql.DB) int64 {
	t.Helper()
	var used int64
	if err := db.QueryRowContext(t.Context(), spaceUsedQuery).Scan(&used); err != nil {
		t.Fatalf("read the used bytes: %v", err)
	}
	return used
}

// spaceReporting returns a statfs reporting ext4 with blocks free, the block size bsize and the fragment size frsize.
func spaceReporting(blocks uint64, bsize, frsize uint32) func(string, *unix.Statfs_t) error {
	return func(_ string, st *unix.Statfs_t) error {
		fsSetType(&st.Type, fsLocal)
		fsSetType(&st.Bsize, bsize)
		fsSetType(&st.Frsize, frsize)
		st.Bavail = blocks
		return nil
	}
}

// spaceFree puts in place of statfs one that reports ext4 with free bytes free in folder and none anywhere else.
func spaceFree(t *testing.T, folder string, free int64) {
	t.Helper()
	there := spaceReporting(uint64(free/spaceBlock), spaceBlock, spaceBlock)
	elsewhere := spaceReporting(0, spaceBlock, spaceBlock)
	fsSwap(t, func(path string, st *unix.Statfs_t) error {
		if path == folder {
			return there(path, st)
		}
		return elsewhere(path, st)
	})
}

// spaceSnapshot runs a snapshot of db with margin to copy.db in folder.
func spaceSnapshot(t *testing.T, db *sql.DB, folder string, margin int64) (dbkit.Snapshot, error) {
	t.Helper()
	return NewSnapshotter(db, SnapshotOptions{Margin: margin}).Snapshot(t.Context(), filepath.Join(folder, "copy.db"))
}

// spaceMustFail fails the test unless err is the SpaceError want and snap is empty.
func spaceMustFail(t *testing.T, snap dbkit.Snapshot, err error, want SpaceError) {
	t.Helper()
	var space *SpaceError
	if !errors.As(err, &space) {
		t.Fatalf("Snapshot() error = %v, want a SpaceError", err)
	}
	if *space != want {
		t.Errorf("Snapshot() error = %+v, want %+v", *space, want)
	}
	if !errors.Is(err, ErrNoSpace) {
		t.Errorf("Snapshot() error = %v, want one matching ErrNoSpace", err)
	}
	for _, other := range []error{ErrUnsupportedFileSystem, ErrSnapshotRunning, ErrSnapshotExists, ErrSnapshotUnsynced} {
		if errors.Is(err, other) {
			t.Errorf("Snapshot() error = %v, want one that does not match %v", err, other)
		}
	}
	var unsupported *UnsupportedFileSystemError
	if errors.As(err, &unsupported) {
		t.Errorf("Snapshot() error = %v, want one that is no UnsupportedFileSystemError", err)
	}
	if snap != (dbkit.Snapshot{}) {
		t.Errorf("Snapshot() = %+v, want no snapshot", snap)
	}
}

func TestASnapshotWithoutSpaceNamesTheBytesNeededAndFree(t *testing.T) {
	db, folder := internalSnapshotOpen(t)
	used := spaceUsed(t, db)
	free := used + spaceMargin - spaceBlock
	spaceFree(t, folder, free)

	snap, err := spaceSnapshot(t, db, folder, spaceMargin)

	spaceMustFail(t, snap, err, SpaceError{Folder: folder, Needed: used + spaceMargin, Free: free})
	want := "dbkit: the snapshot folder " + folder + " has " + strconv.FormatInt(free, 10) +
		" bytes free, and the copy and the margin need " + strconv.FormatInt(used+spaceMargin, 10)
	if fmt.Sprint(err) != want {
		t.Errorf("Snapshot() error = %v, want %q", err, want)
	}
	internalMustHoldOnly(t, folder, snapshotLockFile)
}

func TestTheNoSpaceErrorSaysTooLittleFreeSpace(t *testing.T) {
	want := "dbkit: the snapshot folder has too little free space for the copy and the margin"

	if got := ErrNoSpace.Error(); got != want {
		t.Errorf("ErrNoSpace.Error() = %q, want %q", got, want)
	}
}

func TestASnapshotWithSpaceForTheCopyAndTheMarginLands(t *testing.T) {
	db, folder := internalSnapshotOpen(t)
	spaceFree(t, folder, spaceUsed(t, db)+spaceMargin)

	snap, err := spaceSnapshot(t, db, folder, spaceMargin)

	if err != nil || snap.Path != filepath.Join(folder, "copy.db") {
		t.Errorf("Snapshot() = %+v, %v, want the copy in place", snap, err)
	}
}

func TestAZeroMarginChecksTheCopyAlone(t *testing.T) {
	t.Run("space for the copy", func(t *testing.T) {
		db, folder := internalSnapshotOpen(t)
		spaceFree(t, folder, spaceUsed(t, db))

		snap, err := spaceSnapshot(t, db, folder, 0)

		if err != nil || snap.Path == "" {
			t.Errorf("Snapshot() = %+v, %v, want the copy in place", snap, err)
		}
	})
	t.Run("one block short", func(t *testing.T) {
		db, folder := internalSnapshotOpen(t)
		used := spaceUsed(t, db)
		spaceFree(t, folder, used-spaceBlock)

		snap, err := spaceSnapshot(t, db, folder, 0)

		spaceMustFail(t, snap, err, SpaceError{Folder: folder, Needed: used, Free: used - spaceBlock})
	})
}

func TestAMarginPastTheLargestCountNeedsTheLargestCount(t *testing.T) {
	db, folder := internalSnapshotOpen(t)
	spaceFree(t, folder, spacePlenty)

	snap, err := spaceSnapshot(t, db, folder, math.MaxInt64)

	spaceMustFail(t, snap, err, SpaceError{Folder: folder, Needed: math.MaxInt64, Free: spacePlenty})
}

func TestTheFreeSpaceIsReadOnceTheStaleCopiesAreGone(t *testing.T) {
	db, folder := internalSnapshotOpen(t)
	stale := filepath.Join(folder, "stale.db"+partialSuffix)
	if err := os.WriteFile(stale, []byte("a stale copy"), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v, want nil", err)
	}
	var staleAtRead []bool
	plenty := spaceReporting(spacePlenty/spaceBlock, spaceBlock, spaceBlock)
	fsSwap(t, func(path string, st *unix.Statfs_t) error {
		_, err := os.Lstat(stale)
		staleAtRead = append(staleAtRead, err == nil)
		return plenty(path, st)
	})

	if _, err := spaceSnapshot(t, db, folder, 0); err != nil {
		t.Fatalf("Snapshot() error = %v, want nil", err)
	}

	if len(staleAtRead) == 0 || staleAtRead[len(staleAtRead)-1] {
		t.Errorf("the stale copy was there at the reads %v, want it gone by the last", staleAtRead)
	}
}

func TestAFailedFreeSpaceReadNamesTheFolder(t *testing.T) {
	db, folder := internalSnapshotOpen(t)
	lock := filepath.Join(folder, snapshotLockFile)
	plenty := spaceReporting(spacePlenty/spaceBlock, spaceBlock, spaceBlock)
	fsSwap(t, func(path string, st *unix.Statfs_t) error {
		if _, err := os.Lstat(lock); err == nil {
			return unix.EIO
		}
		return plenty(path, st)
	})

	snap, err := spaceSnapshot(t, db, folder, 0)

	want := "dbkit: read the free space of the snapshot folder " + folder + ": " + unix.EIO.Error()
	if fmt.Sprint(err) != want {
		t.Errorf("Snapshot() error = %v, want %q", err, want)
	}
	if !errors.Is(err, unix.EIO) || snap != (dbkit.Snapshot{}) {
		t.Errorf("Snapshot() = %+v, %v, want no snapshot and an error matching EIO", snap, err)
	}
	internalMustHoldOnly(t, folder, snapshotLockFile)
}

func TestFreeSpaceCountsInTheFragmentSize(t *testing.T) {
	for _, tc := range []struct {
		// name names the case.
		name string
		// statfs is what the file system reports.
		statfs func(string, *unix.Statfs_t) error
		// want is the free count.
		want int64
	}{
		{name: "a fragment size", statfs: spaceReporting(10, 4096, 1024), want: 10240},
		{name: "no fragment size", statfs: spaceReporting(10, 4096, 0), want: 40960},
		{name: "a count past the largest", statfs: spaceReporting(math.MaxUint64, 4096, 4096), want: math.MaxInt64},
		{name: "a product past the largest", statfs: spaceReporting(math.MaxInt64/2+1, 2, 2), want: math.MaxInt64},
		{name: "a product past 64 bits", statfs: spaceReporting(1<<62, 8, 8), want: math.MaxInt64},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fsSwap(t, tc.statfs)

			got, err := freeSpace("/srv")

			if err != nil || got != tc.want {
				t.Errorf("freeSpace() = %d, %v, want %d", got, err, tc.want)
			}
		})
	}
}
