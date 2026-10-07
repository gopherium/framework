// SPDX-License-Identifier: Apache-2.0

package sqlite_test

import (
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"testing"

	modernc "modernc.org/sqlite"

	"github.com/gopherium/framework/dbkit"
	"github.com/gopherium/framework/dbkit/sqlite"
)

// mustOpen opens address with opts and closes the handle when the test ends.
func mustOpen(t *testing.T, address string, opts sqlite.Options) *sql.DB {
	t.Helper()
	db, err := sqlite.Open(address, opts)
	if err != nil {
		t.Fatalf("Open() error = %v, want nil", err)
	}
	t.Cleanup(func() {
		if err := db.Close(); err != nil {
			t.Errorf("Close() error = %v, want nil", err)
		}
	})
	return db
}

// mustPing connects to db and fails the test when it cannot.
func mustPing(t *testing.T, db *sql.DB) {
	t.Helper()
	if err := db.PingContext(t.Context()); err != nil {
		t.Fatalf("PingContext() error = %v, want nil", err)
	}
}

// mustExist fails the test unless a file exists at path.
func mustExist(t *testing.T, path string) {
	t.Helper()
	if _, err := os.Stat(path); err != nil {
		t.Errorf("Stat(%s) error = %v, want the file present", path, err)
	}
}

func TestOpenRefusesABadAddress(t *testing.T) {
	t.Parallel()

	for _, address := range []string{"sqlite::memory:", "sqlite:", "sqlite:file:/srv/site.db", "mysql://db/site"} {
		db, err := sqlite.Open(address, testOptions())

		if !errors.Is(err, dbkit.ErrAddress) || db != nil {
			t.Errorf("Open(%q) = %v, %v, want nil and an error marked ErrAddress", address, db, err)
		}
	}
}

func TestRelativePathNeedsABaseFolder(t *testing.T) {
	t.Parallel()

	t.Run("no base folder", func(t *testing.T) {
		t.Parallel()
		db, err := sqlite.Open("sqlite:site.db", testOptions())

		want := "dbkit: a relative SQLite path needs the option BaseFolder"
		if err == nil || err.Error() != want || db != nil {
			t.Errorf("Open() = %v, %v, want nil and %q", db, err, want)
		}
	})
	t.Run("a relative base folder", func(t *testing.T) {
		t.Parallel()
		opts := testOptions()
		opts.BaseFolder = "data"

		db, err := sqlite.Open("sqlite:site.db", opts)

		want := `dbkit: the option BaseFolder must be an absolute path, got "data"`
		if err == nil || err.Error() != want || db != nil {
			t.Errorf("Open() = %v, %v, want nil and %q", db, err, want)
		}
	})
	t.Run("an absolute base folder", func(t *testing.T) {
		t.Parallel()
		folder := t.TempDir()
		opts := testOptions()
		opts.BaseFolder = folder

		mustPing(t, mustOpen(t, "sqlite:nested/../site.db", opts))

		mustExist(t, filepath.Join(folder, "site.db"))
	})
}

func TestMissingFileIsRefusedWithoutCreate(t *testing.T) {
	t.Parallel()

	address, path := testAddress(t)
	opts := testOptions()
	opts.Create = false

	db, err := sqlite.Open(address, opts)

	want := "dbkit: the database file " + path + " does not exist, pass Create to make it"
	if err == nil || err.Error() != want || db != nil {
		t.Errorf("Open() = %v, %v, want nil and %q", db, err, want)
	}
	mustNotExist(t, path)

	mustPing(t, mustOpen(t, address, testOptions()))

	mustPing(t, mustOpen(t, address, opts))
}

func TestOFDLockingIsOnWhereLinuxAllowsIt(t *testing.T) {
	t.Parallel()

	db := rulesOpen(t, testOptions())
	rulesExec(t, db, "CREATE TABLE ofd_rows (a INTEGER)")

	on, err := modernc.OFDLocking(true)

	t.Logf("OFDLockingEnabled() = %v on %s", modernc.OFDLockingEnabled(), runtime.GOOS)
	if (!on || err != nil) && !errors.Is(err, modernc.ErrOFDLockingUnavailable) {
		t.Errorf("OFDLocking(true) after a write = %v, %v, want OFD locks already on or unavailable", on, err)
	}
}

func TestOpenRefusesAPathUnderAFile(t *testing.T) {
	t.Parallel()

	plain := filepath.Join(t.TempDir(), "plain")
	if err := os.WriteFile(plain, nil, 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v, want nil", err)
	}

	db, err := sqlite.Open("sqlite:"+filepath.Join(plain, "site.db"), testOptions())

	if !errors.Is(err, syscall.ENOTDIR) || !strings.HasPrefix(err.Error(), "dbkit: check the database file: ") {
		t.Errorf("Open() = %v, %v, want nil and the check error marked ENOTDIR", db, err)
	}
}
