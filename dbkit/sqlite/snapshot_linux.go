// SPDX-License-Identifier: Apache-2.0

package sqlite

import "golang.org/x/sys/unix"

// renameNoReplace moves the file at from to the name to, and fails with EEXIST when a file holds that name.
func renameNoReplace(from, to string) error {
	return unix.Renameat2(unix.AT_FDCWD, from, unix.AT_FDCWD, to, unix.RENAME_NOREPLACE)
}
