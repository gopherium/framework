// SPDX-License-Identifier: Apache-2.0

//go:build linux || darwin

package sqlite_test

import (
	"errors"
	"io/fs"
	"os"
	"strings"
	"syscall"
	"testing"
	"testing/fstest"

	"github.com/gopherium/framework/dbkit/sqlite"
)

// migrateLockOf returns the path of the lock file beside the database file at path.
func migrateLockOf(path string) string {
	return path + ".migrate.lock"
}

// migrateLockInfo returns the facts of the lock file at lock and fails the test when it is missing.
func migrateLockInfo(t *testing.T, lock string) fs.FileInfo {
	t.Helper()
	info, err := os.Lstat(lock)
	if err != nil {
		t.Fatalf("Lstat(%s) error = %v, want the lock file present", lock, err)
	}
	return info
}

func TestTheLockFileIsPrivate(t *testing.T) {
	t.Parallel()

	db, path := migrateOpen(t)

	migrateMust(t, db, migrateValid())

	info := migrateLockInfo(t, migrateLockOf(path))
	if got := info.Mode(); got != 0o600 {
		t.Errorf("the lock file's mode = %v, want a regular file with mode 0600", got)
	}
}

func TestTheLockFileIsNeverDeleted(t *testing.T) {
	t.Parallel()

	db, path := migrateOpen(t)
	lock := migrateLockOf(path)
	m := migrateValid()
	migrateMust(t, db, m)
	first := migrateLockInfo(t, lock)
	m.FS.(fstest.MapFS)["00002_first_seed.sql"] = migrateFile("INSERT INTO first_rows VALUES (1);")

	migrateMust(t, db, m)
	second := migrateLockInfo(t, lock)
	m.FS.(fstest.MapFS)["00003_broken.sql"] = migrateFile("INSERT INTO missing_rows VALUES (1);")
	failed := sqlite.Migrate(t.Context(), db, m)
	third := migrateLockInfo(t, lock)

	if failed == nil {
		t.Errorf("Migrate() with a broken migration error = nil, want it to fail")
	}
	if !os.SameFile(first, second) || !os.SameFile(first, third) {
		t.Errorf("the lock file changed between runs, want the one file kept after a run and after a failed run")
	}
	migrateMustVersions(t, db, migrateFirstTable, 0, 1, 2)
}

func TestMigrateRefusesALockPathItCannotOpen(t *testing.T) {
	t.Parallel()

	db, path := migrateOpen(t)
	lock := migrateLockOf(path)
	if err := os.Mkdir(lock, 0o700); err != nil {
		t.Fatalf("Mkdir() error = %v, want nil", err)
	}

	err := sqlite.Migrate(t.Context(), db, migrateValid())

	if want := "dbkit: open the migration lock " + lock + ": "; !errors.Is(err, syscall.EISDIR) ||
		!strings.Contains(err.Error(), want) {
		t.Errorf("Migrate() error = %v, want an error marked EISDIR holding %q", err, want)
	}
	migrateMustHoldNoTable(t, db)
}

func TestMigrateNeverFollowsALinkAtTheLockPath(t *testing.T) {
	t.Parallel()

	db, path := migrateOpen(t)
	lock := migrateLockOf(path)
	if err := os.Symlink(path, lock); err != nil {
		t.Fatalf("Symlink() error = %v, want nil", err)
	}

	err := sqlite.Migrate(t.Context(), db, migrateValid())

	if want := "dbkit: open the migration lock " + lock + ": "; !errors.Is(err, syscall.ELOOP) ||
		!strings.Contains(err.Error(), want) {
		t.Errorf("Migrate() error = %v, want an error marked ELOOP holding %q", err, want)
	}
	migrateMustHoldNoTable(t, db)
}
