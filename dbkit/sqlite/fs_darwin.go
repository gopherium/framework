// SPDX-License-Identifier: Apache-2.0

package sqlite

import (
	"fmt"

	"golang.org/x/sys/unix"
)

// statfs reads the file system facts of the file or folder at path.
var statfs = unix.Statfs

// networkSystems holds each network file system Open refuses, by its Statfs_t.Fstypename.
var networkSystems = map[string]bool{"nfs": true, "smbfs": true, "afpfs": true, "webdav": true}

// checkFolder returns an error when the file or folder at path is on a network or unreadable file system.
func checkFolder(path string) error {
	var st unix.Statfs_t
	if err := statfs(path, &st); err != nil {
		return fmt.Errorf("dbkit: read the file system of %s: %w", path, err)
	}
	if name := unix.ByteSliceToString(st.Fstypename[:]); networkSystems[name] {
		return fmt.Errorf("dbkit: %s is on a network file system (%s), where SQLite locks are unreliable, "+
			"keep the database on a local disk", path, name)
	}
	return nil
}
