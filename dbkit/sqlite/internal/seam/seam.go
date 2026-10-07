// SPDX-License-Identifier: Apache-2.0

// Package seam keeps a connection wrapper per database path.
package seam

import (
	"database/sql/driver"
	"path/filepath"
	"sync"
)

// wrappers holds the wrapper of each cleaned database path.
var wrappers sync.Map

// Set registers wrap for every new connection to the database at path.
func Set(path string, wrap func(driver.Conn) driver.Conn) {
	wrappers.Store(filepath.Clean(path), wrap)
}

// Clear removes the wrapper of the database at path.
func Clear(path string) {
	wrappers.Delete(filepath.Clean(path))
}

// Wrap returns c passed through the wrapper of the database at path, or c when none is set.
func Wrap(path string, c driver.Conn) driver.Conn {
	wrap, ok := wrappers.Load(filepath.Clean(path))
	if !ok {
		return c
	}
	return wrap.(func(driver.Conn) driver.Conn)(c)
}
