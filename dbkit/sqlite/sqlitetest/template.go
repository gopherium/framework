// SPDX-License-Identifier: Apache-2.0

package sqlitetest

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/gopherium/framework/dbkit/sqlite"
)

// templatePattern names the temp folder of each template.
const templatePattern = "dbkit-template-"

var (
	// errNoMigrate refuses a nil migrate function.
	errNoMigrate = errors.New("dbkit: NewTemplate needs a migrate function, got nil")
	// errClosed answers a copy of a closed template.
	errClosed = errors.New("dbkit: the template is closed")
)

// Template is a migrated and closed database file that each test opens a copy of.
type Template struct {
	// folder is the temp folder that holds the template file.
	folder string
	// opts are the options every copy opens with.
	opts sqlite.Options
	// mu guards closed and the template file while a copy reads it.
	mu sync.RWMutex
	// closed reports whether Close has run.
	closed bool
}

// NewTemplate returns a template migrated once under ctx by migrate with opts in a new file of its own temp folder.
func NewTemplate(
	ctx context.Context, opts sqlite.Options, migrate func(ctx context.Context, db *sql.DB) error,
) (tp *Template, err error) {
	if migrate == nil {
		return nil, errNoMigrate
	}
	folder, err := os.MkdirTemp("", templatePattern)
	if err != nil {
		return nil, fmt.Errorf("dbkit: make the template folder: %w", err)
	}
	defer func() {
		if tp == nil {
			err = errors.Join(err, os.RemoveAll(folder))
		}
	}()
	if err := build(ctx, folder, opts, migrate); err != nil {
		return nil, err
	}
	return &Template{folder: folder, opts: opts}, nil
}

// build migrates under ctx a new database file in folder with opts and checks it keeps no write-ahead log.
func build(
	ctx context.Context, folder string, opts sqlite.Options, migrate func(ctx context.Context, db *sql.DB) error,
) error {
	if err := migrateIn(ctx, folder, opts, migrate); err != nil {
		return err
	}
	return checkFolded(filepath.Join(folder, fileName))
}

// migrateIn migrates under ctx a new database file in folder with opts and closes its handle.
func migrateIn(
	ctx context.Context, folder string, opts sqlite.Options, migrate func(ctx context.Context, db *sql.DB) error,
) (err error) {
	opts.BaseFolder = folder
	opts.Create = true
	db, err := sqlite.Open(address, opts)
	if err != nil {
		return fmt.Errorf("dbkit: open the template database: %w", err)
	}
	defer func() {
		if err = errors.Join(err, db.Close()); err != nil {
			err = fmt.Errorf("dbkit: migrate the template database: %w", err)
		}
	}()
	if err := db.PingContext(ctx); err != nil {
		return err
	}
	return migrate(ctx, db)
}

// checkFolded returns the error for a write-ahead log still beside the closed template file at path.
func checkFolded(path string) error {
	wal := path + "-wal"
	if _, err := os.Lstat(wal); !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("dbkit: the template keeps its write-ahead log %s after its handle closed, "+
			"so migrate left a connection open", wal)
	}
	return nil
}

// Open returns a handle with every rule on a copy of the template in a fresh folder of t, closed when t ends.
func (tp *Template) Open(t testing.TB) *sql.DB {
	t.Helper()
	folder := t.TempDir()
	if err := tp.copyTo(filepath.Join(folder, fileName)); err != nil {
		t.Fatalf("dbkit: copy the template: %v", err)
	}
	return openIn(t, folder, tp.opts)
}

// OpenWithFaults returns a handle like Open whose connections answer with the failures of faults.
func (tp *Template) OpenWithFaults(t testing.TB, faults *Faults) *sql.DB {
	t.Helper()
	folder := realFolder(t, t.TempDir())
	if err := tp.copyTo(filepath.Join(folder, fileName)); err != nil {
		t.Fatalf("dbkit: copy the template: %v", err)
	}
	return openFaulty(t, folder, tp.opts, faults)
}

// copyTo copies the template file to path, or returns an error once the template is closed.
func (tp *Template) copyTo(path string) error {
	tp.mu.RLock()
	defer tp.mu.RUnlock()
	if tp.closed {
		return errClosed
	}
	return copyFile(filepath.Join(tp.folder, fileName), path)
}

// copyFile writes the bytes of the file at from to a new file at to.
func copyFile(from, to string) error {
	data, err := os.ReadFile(from)
	if err == nil {
		err = os.WriteFile(to, data, 0o600)
	}
	return err
}

// Close removes the folder of the template, and Open fails every test after it.
func (tp *Template) Close() error {
	tp.mu.Lock()
	defer tp.mu.Unlock()
	tp.closed = true
	return os.RemoveAll(tp.folder)
}
