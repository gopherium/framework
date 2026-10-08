// SPDX-License-Identifier: Apache-2.0

package postgres

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io/fs"
	"regexp"
	"slices"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/pressly/goose/v3"
	"github.com/pressly/goose/v3/database"
	"github.com/pressly/goose/v3/lock"
)

// Migrations are the migrations of one owner.
type Migrations struct {
	// Table is the version table, table or schema.table, lowercase parts of up to 63 bytes, none reserved, no pg_ schema.
	Table string
	// FS holds the owner's SQL migration files at its root, and nil means none.
	FS fs.FS
	// Go are the owner's Go migrations, each built with goose.NewGoMigration.
	Go []*goose.Migration
}

var (
	// plainName matches one or two lowercase plain identifiers of at most 63 bytes each, joined by a dot.
	plainName = regexp.MustCompile(`^[a-z_][a-z0-9_]{0,62}(\.[a-z_][a-z0-9_]{0,62})?$`)
	// reservedKeywords are the PostgreSQL 18 keywords of the categories R and T in pg_get_keywords().
	reservedKeywords = []string{
		"all", "analyse", "analyze", "and", "any", "array", "as", "asc", "asymmetric", "authorization", "binary",
		"both", "case", "cast", "check", "collate", "collation", "column", "concurrently", "constraint", "create",
		"cross", "current_catalog", "current_date", "current_role", "current_schema", "current_time",
		"current_timestamp", "current_user", "default", "deferrable", "desc", "distinct", "do", "else", "end",
		"except", "false", "fetch", "for", "foreign", "freeze", "from", "full", "grant", "group", "having", "ilike",
		"in", "initially", "inner", "intersect", "into", "is", "isnull", "join", "lateral", "leading", "left", "like",
		"limit", "localtime", "localtimestamp", "natural", "not", "notnull", "null", "offset", "on", "only", "or",
		"order", "outer", "overlaps", "placing", "primary", "references", "returning", "right", "select",
		"session_user", "similar", "some", "symmetric", "system_user", "table", "tablesample", "then", "to",
		"trailing", "true", "union", "unique", "user", "using", "variadic", "verbose", "when", "where", "window",
		"with",
	}
	// errNoHandle refuses a nil handle.
	errNoHandle = errors.New("dbkit: Migrate needs a database handle, got nil")
)

const (
	// schemaAbsent asks whether pg_namespace lacks the schema its one argument names.
	schemaAbsent = "SELECT NOT EXISTS (SELECT FROM pg_namespace WHERE nspname = $1)"
	// systemPrefix starts every schema name PostgreSQL keeps for itself.
	systemPrefix = "pg_"
)

// check returns the error for the first option Migrate refuses.
func (m Migrations) check() error {
	switch {
	case m.Table == "":
		return errors.New("dbkit: the option Table must name the version table")
	case !plainTable(m.Table):
		return fmt.Errorf("dbkit: the option Table must be a lowercase plain identifier of at most 63 bytes, "+
			"alone or after a schema and a dot, got %q", m.Table)
	}
	if i := slices.Index(m.Go, nil); i >= 0 {
		return fmt.Errorf("dbkit: the option Go holds nil at index %d", i)
	}
	return nil
}

// plainTable reports whether table is a plain name with no reserved keyword as a part and no system prefix on a schema.
func plainTable(table string) bool {
	first, second, qualified := strings.Cut(table, ".")
	if qualified && strings.HasPrefix(first, systemPrefix) {
		return false
	}
	return plainName.MatchString(table) && !slices.Contains(reservedKeywords, first) &&
		!slices.Contains(reservedKeywords, second)
}

// schemaStore is goose's own PostgreSQL store of a schema-qualified version table that creates its absent schema.
type schemaStore struct {
	// StoreExtender is goose's own PostgreSQL store of the version table.
	database.StoreExtender
	// schema is the schema of the version table.
	schema string
}

// CreateVersionTable creates the schema of the version table when pg_namespace lacks it, then the version table.
func (s schemaStore) CreateVersionTable(ctx context.Context, db database.DBTxConn) error {
	var absent bool
	err := db.QueryRowContext(ctx, schemaAbsent, s.schema).Scan(&absent)
	if err == nil && absent {
		_, err = db.ExecContext(ctx, "CREATE SCHEMA "+pgx.Identifier{s.schema}.Sanitize())
	}
	if err != nil {
		return fmt.Errorf("dbkit: create the schema %s: %w", s.schema, err)
	}
	return s.StoreExtender.CreateVersionTable(ctx, db)
}

// storeOf returns goose's own PostgreSQL store of table, wrapped in a schemaStore when table names a schema.
func storeOf(table string) database.Store {
	store := must(database.NewStore(database.DialectPostgres, table))
	schema, _, qualified := strings.Cut(table, ".")
	if !qualified {
		return store
	}
	return schemaStore{StoreExtender: store.(database.StoreExtender), schema: schema}
}

// Migrate applies every pending migration of m to db under goose's advisory lock, creating an absent schema of Table.
func Migrate(ctx context.Context, db *sql.DB, m Migrations) error {
	if db == nil {
		return errNoHandle
	}
	if err := m.check(); err != nil {
		return err
	}
	provider, err := goose.NewProvider(goose.DialectCustom, db, m.FS,
		goose.WithStore(storeOf(m.Table)),
		goose.WithDisableGlobalRegistry(true),
		goose.WithGoMigrations(m.Go...),
		goose.WithSessionLocker(must(lock.NewPostgresSessionLocker())),
	)
	if err != nil {
		return fmt.Errorf("dbkit: build the migration runner of %s: %w", m.Table, err)
	}
	if _, err := provider.Up(ctx); err != nil {
		return fmt.Errorf("dbkit: run the migrations of %s: %w", m.Table, err)
	}
	return nil
}
