// SPDX-License-Identifier: Apache-2.0

package sqlite

import (
	"fmt"

	"golang.org/x/sys/unix"
)

// statfs reads the file system facts of the file or folder at path.
var statfs = unix.Statfs

// remoteSystem returns the Fstypename of the file system of path when macOS does not mark it local, or "" when it does.
func remoteSystem(path string) (string, error) {
	var st unix.Statfs_t
	if err := statfs(path, &st); err != nil {
		return "", err
	}
	if st.Flags&unix.MNT_LOCAL != 0 {
		return "", nil
	}
	return unix.ByteSliceToString(st.Fstypename[:]), nil
}

// checkFolder returns an error when the file or folder at path is on a network or unreadable file system.
func checkFolder(path string) error {
	name, err := remoteSystem(path)
	switch {
	case err != nil:
		return fmt.Errorf("dbkit: read the file system of %s: %w", path, err)
	case name != "":
		return fmt.Errorf("dbkit: %s is on a network file system (%s), where SQLite locks are unreliable, "+
			"keep the database on a local disk", path, name)
	}
	return nil
}

// snapshotSystem returns the Fstypename of the file system of folder when macOS does not mark it local, or "".
func snapshotSystem(folder string) (string, error) {
	return remoteSystem(folder)
}

// freeSpace returns the bytes the file system of path leaves free to a user who is not root.
func freeSpace(path string) (int64, error) {
	var st unix.Statfs_t
	if err := statfs(path, &st); err != nil {
		return 0, err
	}
	return blockBytes(st.Bavail, uint64(st.Bsize)), nil
}
