// SPDX-License-Identifier: Apache-2.0

//go:build !linux && !darwin

package sqlite

import (
	"context"
	"fmt"
	"runtime"
)

// lock refuses the lock outside linux and darwin.
func (l *fileLocker) lock(context.Context) error {
	return l.refuse()
}

// tryLock refuses the lock outside linux and darwin.
func (l *fileLocker) tryLock(error) error {
	return l.refuse()
}

// refuse returns the error of a lock outside linux and darwin.
func (l *fileLocker) refuse() error {
	return fmt.Errorf("dbkit: the %s works on linux and darwin only, and refuses %s", l.name, runtime.GOOS)
}
