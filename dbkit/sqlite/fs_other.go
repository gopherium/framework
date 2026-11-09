// SPDX-License-Identifier: Apache-2.0

//go:build !linux && !darwin

package sqlite

import (
	"fmt"
	"runtime"
)

// checkFolder refuses every folder on a system with no network file system check.
func checkFolder(string) error {
	return fmt.Errorf("dbkit: Open checks for a network file system on linux and darwin only, and refuses %s",
		runtime.GOOS)
}

// snapshotSystem refuses every snapshot folder on a system with no network file system check.
func snapshotSystem(string) (string, error) {
	return "", fmt.Errorf("dbkit: Snapshot checks for a network file system on linux and darwin only, and refuses %s",
		runtime.GOOS)
}

// freeSpace refuses to read the free space on a system with no free space reader.
func freeSpace(string) (int64, error) {
	return 0, fmt.Errorf("dbkit: Snapshot reads the free space on linux and darwin only, and refuses %s", runtime.GOOS)
}
