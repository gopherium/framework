// SPDX-License-Identifier: Apache-2.0

package auth

import (
	"context"
	"database/sql"
	"embed"
	"fmt"
	"io/fs"

	"github.com/gopherium/gouncer/authkit/postgres"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"
	"github.com/pressly/goose/v3/database"
	"github.com/pressly/goose/v3/lock"

	"github.com/gopherium/framework/gonsole"
)

// migrationFiles holds the migrations of the command records.
//
//go:embed migrations/*.sql
var migrationFiles embed.FS

// recordsVersionTable keeps the lineage of the records migrations inside their own schema.
const recordsVersionTable = "gonsole.goose_db_version"

// Migration returns the step that applies gouncer's schema.
func Migration() gonsole.Step {
	return gonsole.Step{Name: "accounts", Run: postgres.Migrate}
}

// RecordMigration returns the step that applies the schema keeping the command records.
func RecordMigration() gonsole.Step {
	return gonsole.Step{Name: "records", Run: migrateRecords}
}

// migrateRecords applies the schema keeping the command records to the database at databaseURL.
func migrateRecords(ctx context.Context, databaseURL string) error {
	config, err := pgx.ParseConfig(databaseURL)
	if err != nil {
		return fmt.Errorf("open the database: %w", err)
	}
	db := stdlib.OpenDB(*config)
	defer func() { _ = db.Close() }()
	if _, err := db.ExecContext(ctx, "CREATE SCHEMA IF NOT EXISTS gonsole"); err != nil {
		return fmt.Errorf("create the gonsole schema: %w", err)
	}
	if _, err := recordsProvider(db).Up(ctx); err != nil {
		return fmt.Errorf("apply the records schema: %w", err)
	}
	return nil
}

// recordsProvider returns the goose provider applying the records migrations over db.
func recordsProvider(db *sql.DB) *goose.Provider {
	store := must(database.NewStore(database.DialectPostgres, recordsVersionTable))
	files := must(fs.Sub(migrationFiles, "migrations"))
	locker := must(lock.NewPostgresSessionLocker())
	return must(goose.NewProvider("", db, files, goose.WithStore(store), goose.WithSessionLocker(locker)))
}

// must returns value, panicking with err, for a value whose build cannot fail at run time.
func must[T any](value T, err error) T {
	if err != nil {
		panic(err)
	}
	return value
}
