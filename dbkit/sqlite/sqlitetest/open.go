// SPDX-License-Identifier: Apache-2.0

package sqlitetest

import (
	"database/sql"
	"io"
	"path/filepath"
	"testing"

	"github.com/gopherium/framework/dbkit/sqlite"
	"github.com/gopherium/framework/dbkit/sqlite/internal/seam"
)

const (
	// fileName is the name of the database file in the folder of each test.
	fileName = "site.db"
	// address is the address of the database file, relative to the folder of each test.
	address = "sqlite:" + fileName
)

// Open returns a handle with opts on a new file in a fresh folder of t, closed when t ends.
func Open(t testing.TB, opts sqlite.Options) *sql.DB {
	t.Helper()
	return openIn(t, t.TempDir(), opts)
}

// OpenWithFaults returns a handle like Open whose connections answer with the failures of faults.
func OpenWithFaults(t testing.TB, opts sqlite.Options, faults *Faults) *sql.DB {
	t.Helper()
	return openFaulty(t, realFolder(t, t.TempDir()), opts, faults)
}

// openFaulty returns a handle with opts on the file in folder whose connections answer with the failures of faults.
func openFaulty(t testing.TB, folder string, opts sqlite.Options, faults *Faults) *sql.DB {
	t.Helper()
	path := filepath.Join(folder, fileName)
	seam.Set(path, faults.wrap)
	t.Cleanup(func() { seam.Clear(path) })
	return openIn(t, folder, opts)
}

// realFolder returns folder with every symlink resolved and fails t when it cannot.
func realFolder(t testing.TB, folder string) string {
	t.Helper()
	resolved, err := filepath.EvalSymlinks(folder)
	if err != nil {
		t.Fatalf("dbkit: resolve the test folder: %v", err)
	}
	return resolved
}

// openIn returns a handle with opts on a new file in folder, closed when t ends.
func openIn(t testing.TB, folder string, opts sqlite.Options) *sql.DB {
	t.Helper()
	opts.BaseFolder = folder
	opts.Create = true
	db, err := sqlite.Open(address, opts)
	if err != nil {
		t.Fatalf("dbkit: open a test database: %v", err)
	}
	t.Cleanup(func() { closeAtEnd(t, db) })
	return db
}

// closeAtEnd closes c and fails t when it cannot.
func closeAtEnd(t testing.TB, c io.Closer) {
	if err := c.Close(); err != nil {
		t.Errorf("dbkit: close a test database: %v", err)
	}
}
