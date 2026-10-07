// SPDX-License-Identifier: Apache-2.0

package dbkit

import (
	"errors"
	"fmt"
	"strings"
)

// sqlitePrefix starts every SQLite address.
const sqlitePrefix = "sqlite:"

// ErrAddress marks every database address EngineOf and SQLitePath refuse.
var ErrAddress = errors.New("dbkit: refused database address")

var (
	// errUnknownScheme refuses an address that starts with no accepted form.
	errUnknownScheme = refusedAddress("an address starts with postgres://, postgresql:// or sqlite:")
	// errNoSQLitePath refuses a PostgreSQL address where a SQLite path is asked for.
	errNoSQLitePath = refusedAddress("a PostgreSQL address has no SQLite path, write sqlite: and a path")
	// errEmptyPath refuses sqlite: with no path after it.
	errEmptyPath = refusedAddress("sqlite: needs a file path")
	// errDoubleSlash refuses a SQLite path that starts with two slashes.
	errDoubleSlash = refusedAddress("write sqlite: and the path with no //")
	// errMemory refuses the in-memory database name.
	errMemory = refusedAddress("sqlite::memory: keeps no file, write a file path")
	// errFileURI refuses a SQLite path written as a file: URI.
	errFileURI = refusedAddress("sqlite: takes a plain path, never a file: URI")
	// errQueryCharacters refuses a SQLite address that holds a ?, # or %.
	errQueryCharacters = refusedAddress("a sqlite: address holds no ?, # or %, write a plain path")
)

// refusedAddress returns an error marked ErrAddress that gives reason and the SQLite form to write.
func refusedAddress(reason string) error {
	return fmt.Errorf("%w: %s, such as sqlite:/srv/site/site.db", ErrAddress, reason)
}

// EngineOf returns the engine a database address names.
func EngineOf(address string) (Engine, error) {
	if isPostgres(address) {
		return Postgres, nil
	}
	if _, err := sqlitePath(address); err != nil {
		return 0, err
	}
	return SQLite, nil
}

// SQLitePath returns the file path of a SQLite address as written.
func SQLitePath(address string) (string, error) {
	if isPostgres(address) {
		return "", errNoSQLitePath
	}
	return sqlitePath(address)
}

// isPostgres reports whether address starts with a PostgreSQL scheme.
func isPostgres(address string) bool {
	return strings.HasPrefix(address, "postgres://") || strings.HasPrefix(address, "postgresql://")
}

// sqlitePath returns the path of a SQLite address, or the error for any other address.
func sqlitePath(address string) (string, error) {
	path, ok := strings.CutPrefix(address, sqlitePrefix)
	if !ok {
		return "", errUnknownScheme
	}
	if err := checkSQLitePath(address, path); err != nil {
		return "", err
	}
	return path, nil
}

// checkSQLitePath returns the error for a SQLite address and its path, or nil when both are accepted.
func checkSQLitePath(address, path string) error {
	switch {
	case path == "":
		return errEmptyPath
	case strings.HasPrefix(path, "//"):
		return errDoubleSlash
	case path == ":memory:":
		return errMemory
	case strings.HasPrefix(path, "file:"):
		return errFileURI
	case strings.ContainsAny(address, "?#%"):
		return errQueryCharacters
	}
	return nil
}
