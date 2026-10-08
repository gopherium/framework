// SPDX-License-Identifier: Apache-2.0

package pgtest_test

import (
	"context"
	"database/sql"
	"errors"
	"testing"

	"github.com/peterldowns/pgtestdb"

	"github.com/gopherium/framework/dbkit/postgres/pgtest"
)

// migratorTable is the table every migrate function of these tests creates.
const migratorTable = "migrator_rows"

// migratorCreate creates migratorTable on db.
func migratorCreate(ctx context.Context, db *sql.DB) error {
	_, err := db.ExecContext(ctx, "CREATE TABLE "+migratorTable+" (a integer NOT NULL)")
	return err
}

func TestMigratorRefusesAMissingHashOrMigrate(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name     string
		migrator pgtestdb.Migrator
		want     string
	}{
		{"a missing hash", pgtest.Migrator("", migratorCreate), "dbkit: the migrator needs a hash, got none"},
		{"a nil migrate function", pgtest.Migrator("nil-migrate-hash", nil),
			"dbkit: the migrator needs a migrate function, got nil"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()

			hash, err := c.migrator.Hash()

			if err == nil || err.Error() != c.want || hash != "" {
				t.Errorf("Hash() = %q, %v, want no hash and %q", hash, err, c.want)
			}
		})
	}
}

func TestMigratorHashIsTheHashItWasBuiltWith(t *testing.T) {
	t.Parallel()

	hash, err := pgtest.Migrator("built-with-hash", migratorCreate).Hash()

	if err != nil || hash != "built-with-hash" {
		t.Errorf("Hash() = %q, %v, want %q and nil", hash, err, "built-with-hash")
	}
}

func TestMigratorMigrateReturnsTheErrorOfMigrate(t *testing.T) {
	t.Parallel()

	failed := errors.New("the migrate function failed")
	m := pgtest.Migrator("failing-hash", func(context.Context, *sql.DB) error { return failed })

	if err := m.Migrate(t.Context(), nil, pgtestdb.Config{}); !errors.Is(err, failed) {
		t.Errorf("Migrate() error = %v, want %v", err, failed)
	}
}

func TestTheMigratorRunsOncePerHash(t *testing.T) {
	t.Parallel()

	runs := 0
	counted := func(ctx context.Context, db *sql.DB) error {
		runs++
		return migratorCreate(ctx, db)
	}
	first := pgtest.Migrator(serverName("first-hash-"), counted)
	second := pgtest.Migrator(serverName("second-hash-"), counted)

	for i, m := range []pgtestdb.Migrator{first, first, second, second} {
		instance := pgtestdb.Custom(t, serverConfig(t), m)
		if i%2 == 0 {
			serverDropTemplateAtEnd(t, instance.Database)
		}
		if !serverHolds(t, pgtest.URL(*instance), "SELECT to_regclass('"+migratorTable+"') IS NOT NULL") {
			t.Errorf("instance %d lacks %s, want every instance cut from the migrated template", i, migratorTable)
		}
	}

	if runs != 2 {
		t.Errorf("the migrate function ran %d times, want once for each of the two hashes", runs)
	}
}
