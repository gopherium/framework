// SPDX-License-Identifier: Apache-2.0

package postgresblob_test

import (
	"database/sql"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/stdlib"

	"github.com/gopherium/framework/postgresblob"
)

// sqlDB returns a database/sql view of the pool of db, closed when the test ends.
func (db testDB) sqlDB() *sql.DB {
	view := stdlib.OpenDBFromPool(db.pool)
	db.t.Cleanup(func() { _ = view.Close() })
	return view
}

func TestMigrateCreatesTheSchemaAndBothTablesOnce(t *testing.T) {
	t.Parallel()
	db := emptyDB(t)

	for run := range 2 {
		if err := postgresblob.Migrate(t.Context(), db.sqlDB(), "archive"); err != nil {
			t.Fatalf("Migrate() run %d = %v, want nil", run+1, err)
		}
	}

	for _, table := range []string{"archive.objects", "archive.chunks"} {
		if !db.hasTable(table) {
			t.Errorf("the database lacks %s after Migrate(), want it", table)
		}
	}
}

func TestMigrateAndOpenBucketShareTheDefaultSchema(t *testing.T) {
	t.Parallel()
	db := emptyDB(t)

	if err := postgresblob.Migrate(t.Context(), db.sqlDB(), ""); err != nil {
		t.Fatalf("Migrate() with no schema = %v, want nil", err)
	}
	b, err := postgresblob.OpenBucket(t.Context(), db.pool, nil)
	if err != nil {
		t.Fatalf("OpenBucket() with no options = %v, want a bucket", err)
	}
	defer func() { _ = b.Close() }()

	if err := b.WriteAll(t.Context(), "notes/report.txt", []byte("text"), nil); err != nil {
		t.Errorf("WriteAll() on the default schema = %v, want nil", err)
	}
	if !db.hasTable(postgresblob.DefaultSchema + ".objects") {
		t.Errorf("the database lacks %s.objects, want the default schema to hold the tables", postgresblob.DefaultSchema)
	}
}

func TestMigrateRefusesASchemaThatIsNotALowercasePlainName(t *testing.T) {
	t.Parallel()
	db := emptyDB(t)

	err := postgresblob.Migrate(t.Context(), db.sqlDB(), "Files")

	if err == nil || !strings.Contains(err.Error(), "Files") {
		t.Errorf("Migrate() with the schema Files = %v, want an error that names it", err)
	}
	if db.hasTable(`"Files".objects`) {
		t.Error("the database holds \"Files\".objects, want no table")
	}
}
