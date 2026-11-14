// SPDX-License-Identifier: Apache-2.0

package sqlite

import (
	"path/filepath"
	"testing"

	"golang.org/x/sys/unix"
)

func TestAFolderThatCannotMoveWithoutReplacingSaysSo(t *testing.T) {
	db, folder := internalSnapshotOpen(t)
	kept := rename
	rename = func(string, string) error { return unix.EINVAL }
	t.Cleanup(func() { rename = kept })

	got := internalSnapshotTo(t, db, filepath.Join(folder, "copy.db"))

	internalMustAnswer(t, got, internalAnswer{err: unix.EINVAL},
		"dbkit: the file system of the snapshot folder "+folder+" cannot move a file without replacing one, "+
			fsLocalAdvice+": "+unix.EINVAL.Error())
	fsMustBeUnsupported(t, got.err, UnsupportedFileSystemError{Folder: folder, Err: unix.EINVAL})
	internalMustHoldOnly(t, folder, snapshotLockFile)
}
