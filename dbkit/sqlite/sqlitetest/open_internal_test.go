// SPDX-License-Identifier: Apache-2.0

package sqlitetest

import (
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"testing"
	"time"

	"github.com/gopherium/framework/dbkit/sqlite"
	"github.com/gopherium/framework/dbkit/sqlite/internal/seam"
)

// recorder is a testing.TB that records its failures in place of the test's own.
type recorder struct {
	// TB is the test the recorder stands in for.
	testing.TB
	// errs lists the message of every Errorf.
	errs []string
	// fatals lists the message of every Fatalf.
	fatals []string
}

// Errorf records the message.
func (r *recorder) Errorf(format string, args ...any) {
	r.errs = append(r.errs, fmt.Sprintf(format, args...))
}

// Fatalf records the message and stops the goroutine.
func (r *recorder) Fatalf(format string, args ...any) {
	r.fatals = append(r.fatals, fmt.Sprintf(format, args...))
	runtime.Goexit()
}

// runRecorded runs helper with a fresh recorder on its own goroutine and returns the recorder once helper ends.
func runRecorded(t *testing.T, helper func(testing.TB)) *recorder {
	t.Helper()
	r := &recorder{TB: t}
	done := make(chan struct{})
	go func() {
		defer close(done)
		helper(r)
	}()
	<-done
	return r
}

// internalOptions returns options with every required value set.
func internalOptions() sqlite.Options {
	return sqlite.Options{BusyTimeout: 5 * time.Second, CacheSize: 1024, MaxConns: 2, Synchronous: sqlite.SynchronousFull}
}

// stubConn offers every interface of the driver's connection and runs nothing.
type stubConn struct {
	// driverConn is left nil.
	driverConn
}

// databaseFile returns the path of the main database file of db.
func databaseFile(t *testing.T, db *sql.DB) string {
	t.Helper()
	var path string
	if err := db.QueryRowContext(t.Context(), "SELECT file FROM pragma_database_list WHERE name = 'main'").
		Scan(&path); err != nil {
		t.Fatalf("pragma_database_list error = %v, want nil", err)
	}
	return path
}

// wrappedConn reports whether a connection of db reaches the driver through a fault wrapper.
func wrappedConn(t *testing.T, db *sql.DB) bool {
	t.Helper()
	conn, err := db.Conn(t.Context())
	if err != nil {
		t.Fatalf("Conn() error = %v, want nil", err)
	}
	defer func() { _ = conn.Close() }()
	var wrapped bool
	if err := conn.Raw(func(raw any) error {
		_, wrapped = raw.(*faultConn)
		return nil
	}); err != nil {
		t.Fatalf("Raw() error = %v, want nil", err)
	}
	return wrapped
}

func TestOpenWithFaultsClearsItsWrapperWhenTheTestEnds(t *testing.T) {
	link := filepath.Join(t.TempDir(), "link")
	if err := os.Symlink(t.TempDir(), link); err != nil {
		t.Fatalf("Symlink() error = %v, want nil", err)
	}
	roots := []struct {
		// name names the temp folder.
		name string
		// folder is the temp folder each test folder sits in.
		folder string
	}{
		{"the default temp folder", t.TempDir()},
		{"a temp folder behind a symlink", link},
	}
	stub := &stubConn{}

	for _, root := range roots {
		t.Run(root.name, func(t *testing.T) {
			t.Setenv("GOTMPDIR", root.folder)
			var path string
			t.Run("a test with faults", func(t *testing.T) {
				db := OpenWithFaults(t, internalOptions(), &Faults{})
				path = databaseFile(t, db)
				if _, ok := seam.Wrap(path, stub).(*faultConn); !ok {
					t.Errorf("Wrap() during the test gives no fault wrapper for %s, want one", path)
				}
				if !wrappedConn(t, db) {
					t.Errorf("a connection to %s reaches the driver unwrapped, want the fault wrapper", path)
				}
			})

			if got := seam.Wrap(path, stub); got != stub {
				t.Errorf("Wrap() after the test ended = %T, want no wrapper left for %s", got, path)
			}
		})
	}
}

func TestRealFolderFailsTheTestOnAMissingFolder(t *testing.T) {
	t.Parallel()

	missing := filepath.Join(t.TempDir(), "missing")
	_, resolveErr := filepath.EvalSymlinks(missing)

	r := runRecorded(t, func(tb testing.TB) { realFolder(tb, missing) })

	want := []string{"dbkit: resolve the test folder: " + resolveErr.Error()}
	if !slices.Equal(r.fatals, want) {
		t.Errorf("Fatalf messages = %q, want %q", r.fatals, want)
	}
}

// errCloseFailed is the error failingCloser answers with.
var errCloseFailed = errors.New("sqlitetest test: the close failed on purpose")

// failingCloser is a handle whose Close fails.
type failingCloser struct{}

// Close answers errCloseFailed.
func (failingCloser) Close() error {
	return errCloseFailed
}

func TestAFailedCloseFailsTheTest(t *testing.T) {
	t.Parallel()

	r := runRecorded(t, func(tb testing.TB) { closeAtEnd(tb, failingCloser{}) })

	want := []string{"dbkit: close a test database: " + errCloseFailed.Error()}
	if !slices.Equal(r.errs, want) {
		t.Errorf("Errorf messages = %q, want %q", r.errs, want)
	}
}

func TestOpenWithFaultsFailsTheTestOnABadOption(t *testing.T) {
	t.Parallel()

	r := runRecorded(t, func(tb testing.TB) { OpenWithFaults(tb, sqlite.Options{}, &Faults{}) })

	want := []string{"dbkit: open a test database: dbkit: the option BusyTimeout must be 1ms or more, got 0s"}
	if !slices.Equal(r.fatals, want) {
		t.Errorf("Fatalf messages = %q, want %q", r.fatals, want)
	}
}
