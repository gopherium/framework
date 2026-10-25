// SPDX-License-Identifier: Apache-2.0

package postgresblob

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/pressly/goose/v3"

	"github.com/gopherium/framework/dbkit/postgres"
)

// versionTable is the goose version table each schema keeps.
const versionTable = "goose_db_version"

// Migrate creates schema, DefaultSchema when empty, and its tables on db under goose's migration lock.
func Migrate(ctx context.Context, db *sql.DB, schema string) error {
	schema = schemaOr(schema)
	err := postgres.Migrate(ctx, db, postgres.Migrations{
		Table: schema + "." + versionTable,
		Go:    []*goose.Migration{goose.NewGoMigration(1, &goose.GoFunc{RunTx: runAll(migrationStatements(schema))}, nil)},
	})
	if err != nil {
		return fmt.Errorf("postgresblob: migrate the schema %s: %w", schema, err)
	}
	return nil
}

// runAll returns a migration step that runs each of statements in its transaction.
func runAll(statements []string) func(ctx context.Context, tx *sql.Tx) error {
	return func(ctx context.Context, tx *sql.Tx) error {
		for _, statement := range statements {
			if _, err := tx.ExecContext(ctx, statement); err != nil {
				return err
			}
		}
		return nil
	}
}

// schemaOr returns schema, or DefaultSchema when schema is empty.
func schemaOr(schema string) string {
	if schema == "" {
		return DefaultSchema
	}
	return schema
}
