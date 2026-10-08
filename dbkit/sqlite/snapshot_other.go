// SPDX-License-Identifier: Apache-2.0

//go:build !linux && !darwin

package sqlite

import (
	"fmt"
	"runtime"
)

// renameNoReplace refuses every move outside linux and darwin.
func renameNoReplace(string, string) error {
	return fmt.Errorf("dbkit: the snapshot move works on linux and darwin only, and refuses %s", runtime.GOOS)
}
