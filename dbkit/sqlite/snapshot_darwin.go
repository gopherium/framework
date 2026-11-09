// SPDX-License-Identifier: Apache-2.0

package sqlite

import "golang.org/x/sys/unix"

// noReplaceUnsupported is the error of a file system that cannot move a file without replacing one.
const noReplaceUnsupported = unix.ENOTSUP

// renameNoReplace moves the file at from to the name to, and fails with EEXIST when a file holds that name.
func renameNoReplace(from, to string) error {
	return unix.RenamexNp(from, to, unix.RENAME_EXCL)
}
