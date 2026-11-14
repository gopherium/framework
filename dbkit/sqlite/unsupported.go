// SPDX-License-Identifier: Apache-2.0

package sqlite

import (
	"errors"
	"fmt"
)

// ErrUnsupportedFileSystem is the error of a snapshot whose folder is on a file system Snapshot does not support.
var ErrUnsupportedFileSystem = errors.New("dbkit: the file system of the snapshot folder does not support snapshots")

// UnsupportedFileSystemError reports a snapshot folder on a file system Snapshot does not support.
type UnsupportedFileSystemError struct {
	// Folder is the target folder of the snapshot.
	Folder string
	// FileSystem is the name of the network or FUSE file system of Folder, or "" when Err is set.
	FileSystem string
	// Err is the error of the move the file system cannot make without replacing a file, or nil when FileSystem is set.
	Err error
}

// Error returns the folder, its file system or the failed move, and the advice to write snapshots to a local folder.
func (e *UnsupportedFileSystemError) Error() string {
	if e.Err != nil {
		return fmt.Sprintf("dbkit: the file system of the snapshot folder %s cannot move a file without replacing one, "+
			"%s: %v", e.Folder, localFolderAdvice, e.Err)
	}
	return fmt.Sprintf("dbkit: the snapshot folder %s is on a network or FUSE file system (%s), %s",
		e.Folder, e.FileSystem, localFolderAdvice)
}

// Is reports whether target is ErrUnsupportedFileSystem.
func (e *UnsupportedFileSystemError) Is(target error) bool {
	return target == ErrUnsupportedFileSystem
}

// Unwrap returns the error of the failed move, or nil.
func (e *UnsupportedFileSystemError) Unwrap() error {
	return e.Err
}
