// SPDX-License-Identifier: Apache-2.0

package pgtest

import (
	"context"
	"database/sql"
	"errors"

	"github.com/peterldowns/pgtestdb"
)

var (
	// errNoHash refuses a migrator built with an empty hash.
	errNoHash = errors.New("dbkit: the migrator needs a hash, got none")
	// errNoMigrate refuses a migrator built with a nil migrate function.
	errNoMigrate = errors.New("dbkit: the migrator needs a migrate function, got nil")
)

// migrator is a pgtestdb migrator built from a hash and a migrate function.
type migrator struct {
	// hash names the template the migrate function builds.
	hash string
	// migrate applies the migrations to a new template.
	migrate func(ctx context.Context, db *sql.DB) error
}

// Migrator returns a pgtestdb migrator that builds its template with migrate once per hash.
func Migrator(hash string, migrate func(ctx context.Context, db *sql.DB) error) pgtestdb.Migrator {
	return migrator{hash: hash, migrate: migrate}
}

// Hash returns the hash the migrator was built with, or an error when its hash or migrate function is missing.
func (m migrator) Hash() (string, error) {
	switch {
	case m.hash == "":
		return "", errNoHash
	case m.migrate == nil:
		return "", errNoMigrate
	}
	return m.hash, nil
}

// Migrate applies the migrations to the new template db.
func (m migrator) Migrate(ctx context.Context, db *sql.DB, _ pgtestdb.Config) error {
	return m.migrate(ctx, db)
}
