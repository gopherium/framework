// SPDX-License-Identifier: Apache-2.0

package sqlite

import (
	"bytes"
	"database/sql"
	"database/sql/driver"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	modernc "modernc.org/sqlite"

	"github.com/gopherium/framework/dbkit"
)

const (
	// maxFunctionName is the longest function name SQLite registers, in bytes.
	maxFunctionName = 255
	// maxFunctionArgs is the most arguments a function SQLite registers takes.
	maxFunctionArgs = 1000
)

// errRelativePath refuses a relative path when no base folder is set.
var errRelativePath = errors.New("dbkit: a relative SQLite path needs the option BaseFolder")

// ofdOnce switches SQLite to OFD locks once per process and keeps the error that then fails every Open.
var ofdOnce = sync.OnceValue(func() error {
	_, err := modernc.OFDLocking(true)
	return ofdError(err)
})

var (
	// driversMu guards drivers.
	driversMu sync.Mutex
	// drivers holds the one driver value of each function list, the nil list included.
	drivers = map[*dbkit.FunctionList]*modernc.Driver{}
)

// Open returns a handle on the SQLite file of address with every connection rule on, and connects to nothing.
func Open(address string, opts Options) (*sql.DB, error) {
	if err := opts.check(); err != nil {
		return nil, err
	}
	path, err := dbkit.SQLitePath(address)
	if err != nil {
		return nil, err
	}
	if path, err = resolve(path, opts.BaseFolder); err != nil {
		return nil, err
	}
	if err := checkPlace(path); err != nil {
		return nil, err
	}
	if err := checkFile(path, opts.Create); err != nil {
		return nil, err
	}
	if err := ofdOnce(); err != nil {
		return nil, err
	}
	drv, err := driverFor(opts.Functions)
	if err != nil {
		return nil, err
	}
	db := sql.OpenDB(newConnector(drv, path, opts))
	db.SetMaxOpenConns(opts.MaxConns)
	db.SetMaxIdleConns(opts.MaxConns)
	return db, nil
}

// connectionString returns the driver's name of the file at path with every rule of opts.
func connectionString(path string, opts Options) string {
	name := fmt.Sprintf("%s?_txlock=immediate&_defensive=1&_busy_timeout=%d&_journal_mode=WAL&_foreign_keys=1"+
		"&_synchronous=%s&_time_integer_format=unix_micro&_pragma=temp_store(MEMORY)&_pragma=cache_size(-%d)",
		path, opts.BusyTimeout.Milliseconds(), opts.Synchronous.pragma(), opts.CacheSize)
	if opts.JournalSizeLimit != nil {
		name += fmt.Sprintf("&_pragma=journal_size_limit(%d)", *opts.JournalSizeLimit)
	}
	return name
}

// driverFor returns the one driver value of list, built with every function of list registered on first use.
func driverFor(list *dbkit.FunctionList) (*modernc.Driver, error) {
	driversMu.Lock()
	defer driversMu.Unlock()
	if drv, ok := drivers[list]; ok {
		return drv, nil
	}
	fns := list.All()
	if err := checkFunctions(fns); err != nil {
		return nil, err
	}
	drv := &modernc.Driver{}
	for _, fn := range fns {
		drv.MustRegisterFunction(fn.Name, &modernc.FunctionImpl{
			NArgs:         int32(fn.Args),
			Deterministic: fn.Deterministic,
			Scalar: func(_ *modernc.FunctionContext, args []driver.Value) (driver.Value, error) {
				return callFunction(fn, args)
			},
			VolatileArgs: true,
		})
	}
	drivers[list] = drv
	return drv, nil
}

// callFunction returns the result of fn on copied arguments, a time.Time as microseconds and a panic as an error.
func callFunction(fn dbkit.Function, args []driver.Value) (v driver.Value, err error) {
	defer func() {
		if r := recover(); r != nil {
			v, err = nil, fmt.Errorf("dbkit: function %q panicked: %v", fn.Name, r)
		}
	}()
	v, err = fn.Call(cloneArgs(args))
	if err != nil {
		return nil, err
	}
	if t, ok := v.(time.Time); ok {
		return dbkit.Time{Time: t}.Value()
	}
	return v, nil
}

// cloneArgs replaces each string and []byte of args with a copy and returns args.
func cloneArgs(args []driver.Value) []driver.Value {
	for i, arg := range args {
		switch a := arg.(type) {
		case string:
			args[i] = strings.Clone(a)
		case []byte:
			args[i] = bytes.Clone(a)
		}
	}
	return args
}

// checkFunctions returns the error for the first function SQLite cannot register.
func checkFunctions(fns []dbkit.Function) error {
	for i, fn := range fns {
		if strings.ContainsRune(fn.Name, 0) {
			return fmt.Errorf("dbkit: function %d has a name holding a NUL byte", i)
		}
		if len(fn.Name) > maxFunctionName {
			return fmt.Errorf("dbkit: function %d has a name of %d bytes, SQLite takes at most %d",
				i, len(fn.Name), maxFunctionName)
		}
		if fn.Args > maxFunctionArgs {
			return fmt.Errorf("dbkit: function %q takes %d arguments, SQLite takes at most %d",
				fn.Name, fn.Args, maxFunctionArgs)
		}
	}
	return nil
}

// ofdError returns nil for an answer of OFDLocking that leaves SQLite safe, and the error that fails Open otherwise.
func ofdError(err error) error {
	if err == nil || errors.Is(err, modernc.ErrOFDLockingUnavailable) {
		return nil
	}
	return fmt.Errorf("dbkit: SQLite locked a database file before Open could switch on OFD locks: %w", err)
}

// resolve returns path made absolute against base, or an error for a relative path with no base.
func resolve(path, base string) (string, error) {
	if filepath.IsAbs(path) {
		return filepath.Clean(path), nil
	}
	if base == "" {
		return "", errRelativePath
	}
	return filepath.Join(base, path), nil
}

// checkPlace returns the error for a link to a missing file, or for a database file or folder on a network file system.
func checkPlace(path string) error {
	resolved, err := filepath.EvalSymlinks(path)
	if err == nil {
		if err := checkFolder(filepath.Dir(resolved)); err != nil {
			return err
		}
		return checkFolder(resolved)
	}
	if info, linkErr := os.Lstat(path); errors.Is(err, fs.ErrNotExist) && linkErr == nil &&
		info.Mode()&fs.ModeSymlink != 0 {
		return fmt.Errorf("dbkit: the database path %s is a link to a missing file", path)
	}
	return checkFolder(filepath.Dir(path))
}

// checkFile returns the error for a database file that is missing without create, or that cannot be read.
func checkFile(path string, create bool) error {
	_, err := os.Stat(path)
	switch {
	case err == nil, create && errors.Is(err, fs.ErrNotExist):
		return nil
	case errors.Is(err, fs.ErrNotExist):
		return fmt.Errorf("dbkit: the database file %s does not exist, pass Create to make it", path)
	default:
		return fmt.Errorf("dbkit: check the database file: %w", err)
	}
}
