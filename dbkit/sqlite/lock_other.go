// SPDX-License-Identifier: Apache-2.0

//go:build !linux && !darwin

package sqlite

import (
	"context"
	"fmt"
	"runtime"
)

// lock refuses the migration lock outside linux and darwin.
func (l *fileLocker) lock(context.Context) error {
	return fmt.Errorf("dbkit: the migration lock works on linux and darwin only, and refuses %s", runtime.GOOS)
}
