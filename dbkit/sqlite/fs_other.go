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
