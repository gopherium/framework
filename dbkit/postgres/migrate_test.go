// SPDX-License-Identifier: Apache-2.0

package postgres_test

import (
	"context"
	"crypto/rand"
	"database/sql"
	"errors"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"testing/fstest"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/pressly/goose/v3"
	"github.com/pressly/goose/v3/lock"

	"github.com/gopherium/framework/dbkit/postgres"
)

const (
	// migrateFirstTable is the version table of the first owner, in the default schema.
	migrateFirstTable = "first_owner_versions"
	// migrateSecondSchema is the schema of the second owner.
	migrateSecondSchema = "second_owner"
	// migrateSecondVersions is the name of the version table of the second owner inside its schema.
	migrateSecondVersions = "versions"
	// migrateSecondTable is the version table of the second owner, inside its own schema.
	migrateSecondTable = migrateSecondSchema + "." + migrateSecondVersions
	// migrateRolePassword is the password of every role the migrate tests create.
	migrateRolePassword = "role-password"
	// migrateGlobalTable is the table the Go migration in goose's global registry creates.
	migrateGlobalTable = "global_registry_rows"
	// migrateAwaitPoll is how often a test looks for the runner in pg_stat_activity.
	migrateAwaitPoll = 10 * time.Millisecond
	// migrateNotPlain starts the error of every Table that is no plain table name.
	migrateNotPlain = "dbkit: the option Table must be a lowercase plain identifier of at most 63 bytes, " +
		"alone or after a schema and a dot, got "
)

// migrateFile returns a SQL migration file whose up section is up.
func migrateFile(up string) *fstest.MapFile {
	return &fstest.MapFile{Data: []byte("-- +goose Up\n" + up + "\n")}
}

// migrateGo returns a Go migration of version that runs up in a transaction.
func migrateGo(version int64, up func(ctx context.Context, tx *sql.Tx) error) *goose.Migration {
	return goose.NewGoMigration(version, &goose.GoFunc{RunTx: up}, nil)
}

// migrateValid returns the migrations of the first owner, one SQL file that creates first_rows.
func migrateValid() postgres.Migrations {
	return postgres.Migrations{
		Table: migrateFirstTable,
		FS:    fstest.MapFS{"00001_first_rows.sql": migrateFile("CREATE TABLE first_rows (a integer NOT NULL);")},
	}
}

// migrateSecondValid returns the migrations of the second owner, one SQL file that creates rows in its schema.
func migrateSecondValid() postgres.Migrations {
	return postgres.Migrations{
		Table: migrateSecondTable,
		FS: fstest.MapFS{
			"00001_second_rows.sql": migrateFile("CREATE TABLE " + migrateSecondSchema + ".rows (a integer NOT NULL);"),
		},
	}
}

// migrateMust runs Migrate with m on db and fails the test when it cannot.
func migrateMust(t *testing.T, db *sql.DB, m postgres.Migrations) {
	t.Helper()
	if err := postgres.Migrate(t.Context(), db, m); err != nil {
		t.Fatalf("Migrate(%s) error = %v, want nil", m.Table, err)
	}
}

// migrateVersions returns the versions table records as applied on db, in the order they were applied.
func migrateVersions(t *testing.T, db *sql.DB, table string) []int64 {
	t.Helper()
	rows, err := db.QueryContext(t.Context(), "SELECT version_id FROM "+table+" WHERE is_applied ORDER BY id")
	if err != nil {
		t.Fatalf("read the versions of %s: %v", table, err)
	}
	defer func() { _ = rows.Close() }()
	var versions []int64
	for rows.Next() {
		var version int64
		if err := rows.Scan(&version); err != nil {
			t.Fatalf("scan a version of %s: %v", table, err)
		}
		versions = append(versions, version)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("read the versions of %s: %v", table, err)
	}
	return versions
}

// migrateMustVersions fails the test unless table on db records exactly want as applied, in order.
func migrateMustVersions(t *testing.T, db *sql.DB, table string, want ...int64) {
	t.Helper()
	if got := migrateVersions(t, db, table); !slices.Equal(got, want) {
		t.Errorf("applied versions of %s = %v, want %v", table, got, want)
	}
}

// migrateCount returns the number of rows of table on db.
func migrateCount(t *testing.T, db *sql.DB, table string) int64 {
	t.Helper()
	var n int64
	if err := db.QueryRowContext(t.Context(), "SELECT count(*) FROM "+table).Scan(&n); err != nil {
		t.Fatalf("count the rows of %s: %v", table, err)
	}
	return n
}

// migrateTableExists reports whether the table named name exists on db.
func migrateTableExists(t *testing.T, db *sql.DB, name string) bool {
	t.Helper()
	var exists bool
	if err := db.QueryRowContext(t.Context(), "SELECT to_regclass($1) IS NOT NULL", name).Scan(&exists); err != nil {
		t.Fatalf("look for %s: %v", name, err)
	}
	return exists
}

// migrateMustRead returns the one value query answers on db and fails the test when it cannot.
func migrateMustRead[T any](t *testing.T, db *sql.DB, query string, args ...any) T {
	t.Helper()
	var value T
	if err := db.QueryRowContext(t.Context(), query, args...).Scan(&value); err != nil {
		t.Fatalf("QueryRow(%q) error = %v, want nil", query, err)
	}
	return value
}

// migrateSchemaExists reports whether db has a schema named schema.
func migrateSchemaExists(t *testing.T, db *sql.DB, schema string) bool {
	t.Helper()
	return migrateMustRead[bool](t, db, "SELECT EXISTS (SELECT FROM pg_namespace WHERE nspname = $1)", schema)
}

// migrateAsServer returns a connection to the database of address as the test server's own user.
func migrateAsServer(t *testing.T, address string) *pgx.Conn {
	t.Helper()
	parsed, err := url.Parse(address)
	if err != nil {
		t.Fatal("a database address does not parse, want a URL")
	}
	server := openServer(t)
	parsed.User = url.UserPassword(server.User, server.Password)
	conn, err := pgx.Connect(t.Context(), parsed.String())
	if err != nil {
		t.Fatal("connect as the server's own user failed, want a connection")
	}
	t.Cleanup(func() { _ = conn.Close(context.Background()) })
	return conn
}

// migrateAdminExec runs query through admin and fails the test when it cannot.
func migrateAdminExec(t *testing.T, admin *pgx.Conn, query string) {
	t.Helper()
	if _, err := admin.Exec(t.Context(), query); err != nil {
		t.Fatalf("Exec(%q) error = %v, want nil", query, err)
	}
}

// migrateRole returns a new login role, dropped when the test ends, and the address of the database of address as it.
func migrateRole(t *testing.T, admin *pgx.Conn, address string) (role, roleAddress string) {
	t.Helper()
	role = "dbkit_migrate_" + strings.ToLower(rand.Text())
	migrateAdminExec(t, admin, "CREATE ROLE "+role+" LOGIN PASSWORD '"+migrateRolePassword+"'")
	t.Cleanup(func() {
		_, owned := admin.Exec(context.Background(), "DROP OWNED BY "+role)
		_, dropped := admin.Exec(context.Background(), "DROP ROLE "+role)
		if err := errors.Join(owned, dropped); err != nil {
			t.Errorf("drop the role of the test: %v", err)
		}
	})
	parsed, err := url.Parse(address)
	if err != nil {
		t.Fatal("a database address does not parse, want a URL")
	}
	parsed.User = url.UserPassword(role, migrateRolePassword)
	return role, parsed.String()
}

// migrateRoleHandle returns the view of a handle on roleAddress, whose role may not create schemas in its database.
func migrateRoleHandle(t *testing.T, roleAddress string) *sql.DB {
	t.Helper()
	db := mustOpen(t, roleAddress, openSmallestCap).DB
	query := "SELECT has_database_privilege(current_user, current_database(), 'CREATE')"
	if migrateMustRead[bool](t, db, query) {
		t.Fatal("the role of the test may create schemas, want no CREATE right on the database")
	}
	return db
}

func TestMigrateCreatesTheAbsentSchemaOfItsTable(t *testing.T) {
	t.Parallel()

	db := openFreshHandle(t, openSmallestCap).DB

	migrateMust(t, db, migrateSecondValid())

	lookup := "SELECT EXISTS (SELECT FROM pg_tables WHERE schemaname = $1 AND tablename = $2)"
	if !migrateMustRead[bool](t, db, lookup, migrateSecondSchema, migrateSecondVersions) {
		t.Errorf("no table %s in the schema %s, want the version table there", migrateSecondVersions,
			migrateSecondSchema)
	}
	migrateMustVersions(t, db, migrateSecondTable, 0, 1)
}

func TestMigrateCreatesNoSchemaForAnUnqualifiedTable(t *testing.T) {
	t.Parallel()

	db := openFreshHandle(t, openSmallestCap).DB
	count := "SELECT count(*) FROM pg_namespace"
	before := migrateMustRead[int64](t, db, count)

	migrateMust(t, db, migrateValid())

	if after := migrateMustRead[int64](t, db, count); after != before {
		t.Errorf("the database holds %d schemas after Migrate, want the %d it held before", after, before)
	}
	migrateMustVersions(t, db, migrateFirstTable, 0, 1)
}

func TestMigrateRunsAsARoleWithNoCreateRightOnTheDatabase(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name    string
		prepare func(role string) []string
	}{
		{"a role that owns the schema", func(role string) []string {
			return []string{"CREATE SCHEMA " + migrateSecondSchema + " AUTHORIZATION " + role}
		}},
		{"a role that may create in the schema", func(role string) []string {
			return []string{
				"CREATE SCHEMA " + migrateSecondSchema,
				"GRANT USAGE, CREATE ON SCHEMA " + migrateSecondSchema + " TO " + role,
			}
		}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			address := openFresh(t)
			admin := migrateAsServer(t, address)
			role, roleAddress := migrateRole(t, admin, address)
			for _, statement := range c.prepare(role) {
				migrateAdminExec(t, admin, statement)
			}
			db := migrateRoleHandle(t, roleAddress)

			migrateMust(t, db, migrateSecondValid())

			migrateMustVersions(t, db, migrateSecondTable, 0, 1)
		})
	}
}

func TestMigrateNamesTheSchemaItCannotCreate(t *testing.T) {
	t.Parallel()

	address := openFresh(t)
	admin := migrateAsServer(t, address)
	_, roleAddress := migrateRole(t, admin, address)
	db := migrateRoleHandle(t, roleAddress)

	err := postgres.Migrate(t.Context(), db, migrateSecondValid())

	var serverErr *pgconn.PgError
	want := "dbkit: create the schema " + migrateSecondSchema + ": "
	if !errors.As(err, &serverErr) || serverErr.Code != "42501" || !strings.Contains(err.Error(), want) {
		t.Errorf("Migrate() error = %v, want the server's 42501 after %q", err, want)
	}
	if migrateSchemaExists(t, db, migrateSecondSchema) {
		t.Errorf("the schema %s exists after Migrate failed, want it absent", migrateSecondSchema)
	}
}

func TestTwoRunsAtOnceCreateTheSchemaAndApplyEveryMigrationOnce(t *testing.T) {
	t.Parallel()

	address := openFresh(t)
	var goRuns atomic.Int32
	second := func() postgres.Migrations {
		m := migrateSecondValid()
		m.Go = []*goose.Migration{migrateGo(2, func(ctx context.Context, tx *sql.Tx) error {
			goRuns.Add(1)
			_, err := tx.ExecContext(ctx, "INSERT INTO "+migrateSecondSchema+".rows VALUES (1)")
			return err
		})}
		return m
	}
	handles := []*postgres.Handle{mustOpen(t, address, openSmallestCap), mustOpen(t, address, openSmallestCap)}
	ran := make(chan error, len(handles))

	for _, h := range handles {
		m := second()
		go func() { ran <- postgres.Migrate(t.Context(), h.DB, m) }()
	}

	for range handles {
		if err := <-ran; err != nil {
			t.Errorf("Migrate() error = %v, want both runs to succeed", err)
		}
	}
	db := handles[0].DB
	migrateMustVersions(t, db, migrateSecondTable, 0, 1, 2)
	if got := goRuns.Load(); got != 1 {
		t.Errorf("the Go migration ran %d times, want once", got)
	}
	if got := migrateCount(t, db, migrateSecondSchema+".rows"); got != 1 {
		t.Errorf("%s.rows holds %d rows, want 1", migrateSecondSchema, got)
	}
}

func TestMigrateRefusesABadOption(t *testing.T) {
	t.Parallel()

	identifier := migrateNotPlain
	longest := strings.Repeat("a", 63)
	cases := []struct {
		name   string
		change func(*postgres.Migrations)
		want   string
	}{
		{"a missing table", func(m *postgres.Migrations) { m.Table = "" },
			"dbkit: the option Table must name the version table"},
		{"a table with a space", func(m *postgres.Migrations) { m.Table = "first owner" },
			identifier + `"first owner"`},
		{"a table in double quotes", func(m *postgres.Migrations) { m.Table = `"first_owner_versions"` },
			identifier + `"\"first_owner_versions\""`},
		{"a table with a single quote", func(m *postgres.Migrations) { m.Table = "first_owner'versions" },
			identifier + `"first_owner'versions"`},
		{"a table in upper case", func(m *postgres.Migrations) { m.Table = "First_Owner_Versions" },
			identifier + `"First_Owner_Versions"`},
		{"a schema in upper case", func(m *postgres.Migrations) { m.Table = "Second_Owner.versions" },
			identifier + `"Second_Owner.versions"`},
		{"a table that starts with a digit", func(m *postgres.Migrations) { m.Table = "1_owner_versions" },
			identifier + `"1_owner_versions"`},
		{"a table with a hyphen", func(m *postgres.Migrations) { m.Table = "first-owner-versions" },
			identifier + `"first-owner-versions"`},
		{"a table with a letter outside ASCII", func(m *postgres.Migrations) { m.Table = "versión" },
			identifier + `"versión"`},
		{"a table with a trailing newline", func(m *postgres.Migrations) { m.Table = "first_owner_versions\n" },
			identifier + `"first_owner_versions\n"`},
		{"a table past 63 bytes", func(m *postgres.Migrations) { m.Table = longest + "a" },
			identifier + `"` + longest + `a"`},
		{"a schema past 63 bytes", func(m *postgres.Migrations) { m.Table = longest + "a.versions" },
			identifier + `"` + longest + `a.versions"`},
		{"a table after two schemas", func(m *postgres.Migrations) { m.Table = "site.second_owner.versions" },
			identifier + `"site.second_owner.versions"`},
		{"an empty schema", func(m *postgres.Migrations) { m.Table = ".versions" },
			identifier + `".versions"`},
		{"an empty table after a schema", func(m *postgres.Migrations) { m.Table = "second_owner." },
			identifier + `"second_owner."`},
		{"a table named by the reserved keyword user", func(m *postgres.Migrations) { m.Table = "user" },
			identifier + `"user"`},
		{"a table named by the reserved keyword order", func(m *postgres.Migrations) { m.Table = "order" },
			identifier + `"order"`},
		{"a table named by a reserved keyword that may name a function or type",
			func(m *postgres.Migrations) { m.Table = "left" }, identifier + `"left"`},
		{"a schema named by a reserved keyword", func(m *postgres.Migrations) { m.Table = "select.versions" },
			identifier + `"select.versions"`},
		{"a table named by a reserved keyword after a schema",
			func(m *postgres.Migrations) { m.Table = "versions.table" }, identifier + `"versions.table"`},
		{"a schema with the system prefix", func(m *postgres.Migrations) { m.Table = "pg_owner.versions" },
			identifier + `"pg_owner.versions"`},
		{"a nil Go migration", func(m *postgres.Migrations) {
			m.Go = []*goose.Migration{migrateGo(2, func(context.Context, *sql.Tx) error { return nil }), nil}
		}, "dbkit: the option Go holds nil at index 1"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			h := mustOpen(t, openUnreachable(t), openMaxConns)
			m := migrateValid()
			c.change(&m)

			err := postgres.Migrate(t.Context(), h.DB, m)

			if err == nil || err.Error() != c.want {
				t.Errorf("Migrate() error = %v, want %q", err, c.want)
			}
			if made := h.Pool.Stat().NewConnsCount(); made != 0 {
				t.Errorf("Migrate() opened %d connections, want none before the options pass", made)
			}
		})
	}
}

func TestMigrateAcceptsTheLongestNames(t *testing.T) {
	t.Parallel()

	longest := strings.Repeat("a", 63)
	db := openFreshHandle(t, openMaxConns).DB
	m := migrateValid()
	m.Table = longest + "." + longest

	for range 2 {
		migrateMust(t, db, m)
	}

	migrateMustVersions(t, db, m.Table, 0, 1)
}

func TestMigrateAcceptsANamePostgreSQLTakesUnquoted(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name  string
		table string
	}{
		{"a table named by a keyword that is not reserved", "version"},
		{"a table named by a keyword that may name no function or type", "values"},
		{"a table named by a keyword that is not reserved after a schema", migrateSecondSchema + ".version"},
		{"a table named by a keyword that may name no function or type after a schema",
			migrateSecondSchema + ".between"},
		{"a schema named by a keyword that may name no function or type", "between." + migrateSecondVersions},
		{"a table with the system prefix and no schema", "pg_versions"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			db := openFreshHandle(t, openSmallestCap).DB
			m := migrateValid()
			m.Table = c.table

			for range 2 {
				migrateMust(t, db, m)
			}

			migrateMustVersions(t, db, c.table, 0, 1)
		})
	}
}

// migrateKeywords returns every keyword of the server of db, and whether the server reserves it.
func migrateKeywords(t *testing.T, db *sql.DB) map[string]bool {
	t.Helper()
	rows, err := db.QueryContext(t.Context(), "SELECT word, catcode IN ('R', 'T') FROM pg_get_keywords()")
	if err != nil {
		t.Fatalf("read the keywords of the server: %v", err)
	}
	defer func() { _ = rows.Close() }()
	keywords := map[string]bool{}
	for rows.Next() {
		var word string
		var reserved bool
		if err := rows.Scan(&word, &reserved); err != nil {
			t.Fatalf("scan a keyword of the server: %v", err)
		}
		keywords[word] = reserved
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("read the keywords of the server: %v", err)
	}
	return keywords
}

func TestMigrateRefusesExactlyTheKeywordsTheServerReserves(t *testing.T) {
	t.Parallel()

	keywords := migrateKeywords(t, openFreshHandle(t, openSmallestCap).DB)
	h := mustOpen(t, openUnreachable(t), openMaxConns)
	ended, end := context.WithCancel(t.Context())
	end()

	for word, reserved := range keywords {
		for _, table := range []string{word, word + "." + migrateSecondVersions, migrateSecondSchema + "." + word} {
			m := migrateValid()
			m.Table = table

			err := postgres.Migrate(ended, h.DB, m)

			if reserved && (err == nil || err.Error() != migrateNotPlain+strconv.Quote(table)) {
				t.Errorf("Migrate(%q) error = %v, want the option refused, as the server reserves %s", table, err, word)
			}
			if !reserved && !errors.Is(err, context.Canceled) {
				t.Errorf("Migrate(%q) error = %v, want the ended context, as the server does not reserve %s",
					table, err, word)
			}
		}
	}
	if made := h.Pool.Stat().NewConnsCount(); made != 0 {
		t.Errorf("Migrate() opened %d connections, want none on an ended context", made)
	}
}

func TestMigrateRefusesANilHandle(t *testing.T) {
	t.Parallel()

	err := postgres.Migrate(t.Context(), nil, migrateValid())

	if want := "dbkit: Migrate needs a database handle, got nil"; err == nil || err.Error() != want {
		t.Errorf("Migrate() error = %v, want %q", err, want)
	}
}

func TestMigrateAppliesEveryMigrationOnce(t *testing.T) {
	t.Parallel()

	db := openFreshHandle(t, openSmallestCap).DB
	var firstRuns, secondRuns int
	first := postgres.Migrations{
		Table: migrateFirstTable,
		FS: fstest.MapFS{
			"00001_first_rows.sql": migrateFile("CREATE TABLE first_rows (a integer NOT NULL);"),
			"00002_first_seed.sql": migrateFile("INSERT INTO first_rows VALUES (1);"),
		},
		Go: []*goose.Migration{migrateGo(3, func(ctx context.Context, tx *sql.Tx) error {
			firstRuns++
			_, err := tx.ExecContext(ctx, "INSERT INTO first_rows VALUES (2)")
			return err
		})},
	}
	second := postgres.Migrations{
		Table: migrateSecondTable,
		FS: fstest.MapFS{
			"00001_second_rows.sql": migrateFile("CREATE TABLE " + migrateSecondSchema + ".rows (a integer NOT NULL);"),
		},
		Go: []*goose.Migration{goose.NewGoMigration(2, &goose.GoFunc{RunDB: func(ctx context.Context, db *sql.DB) error {
			secondRuns++
			_, err := db.ExecContext(ctx, "INSERT INTO "+migrateSecondSchema+".rows VALUES (1)")
			return err
		}}, nil)},
	}

	for range 2 {
		migrateMust(t, db, first)
		migrateMust(t, db, second)
	}

	migrateMustVersions(t, db, migrateFirstTable, 0, 1, 2, 3)
	migrateMustVersions(t, db, migrateSecondTable, 0, 1, 2)
	if firstRuns != 1 || secondRuns != 1 {
		t.Errorf("the Go migrations ran %d and %d times, want once each", firstRuns, secondRuns)
	}
	if got := migrateCount(t, db, "first_rows"); got != 2 {
		t.Errorf("first_rows holds %d rows, want 2", got)
	}
	if got := migrateCount(t, db, migrateSecondSchema+".rows"); got != 1 {
		t.Errorf("%s.rows holds %d rows, want 1", migrateSecondSchema, got)
	}
}

func TestMigrateNamesTheOwnerInARunnerError(t *testing.T) {
	t.Parallel()

	t.Run("no migrations at all", func(t *testing.T) {
		t.Parallel()
		h := mustOpen(t, openUnreachable(t), openMaxConns)
		m := migrateValid()
		m.FS = fstest.MapFS{}

		err := postgres.Migrate(t.Context(), h.DB, m)

		prefix := "dbkit: build the migration runner of " + migrateFirstTable + ": "
		if !errors.Is(err, goose.ErrNoMigrations) || !strings.HasPrefix(err.Error(), prefix) {
			t.Errorf("Migrate() error = %v, want goose.ErrNoMigrations after %q", err, prefix)
		}
	})
	t.Run("a SQL migration that fails", func(t *testing.T) {
		t.Parallel()
		db := openFreshHandle(t, openMaxConns).DB
		m := migrateValid()
		m.FS = fstest.MapFS{"00001_broken.sql": migrateFile("CREATE TABLE broken_rows (a integer NOT NULL;")}

		err := postgres.Migrate(t.Context(), db, m)

		var partial *goose.PartialError
		prefix := "dbkit: run the migrations of " + migrateFirstTable + ": "
		if !errors.As(err, &partial) || !strings.HasPrefix(err.Error(), prefix) {
			t.Errorf("Migrate() error = %v, want goose's partial error after %q", err, prefix)
		}
		migrateMustVersions(t, db, migrateFirstTable, 0)
	})
}

// migrateGlobalUp creates the table of the Go migration in goose's global registry.
func migrateGlobalUp(ctx context.Context, tx *sql.Tx) error {
	_, err := tx.ExecContext(ctx, "CREATE TABLE "+migrateGlobalTable+" (a integer)")
	return err
}

// migrateRegisterGlobally puts one Go migration in goose's global registry until the test ends.
func migrateRegisterGlobally(t *testing.T) {
	t.Helper()
	if err := goose.SetGlobalMigrations(migrateGo(90001, migrateGlobalUp)); err != nil {
		t.Fatalf("SetGlobalMigrations() error = %v, want nil", err)
	}
	t.Cleanup(goose.ResetGlobalMigrations)
}

func TestRunnerIgnoresTheGlobalRegistry(t *testing.T) {
	migrateRegisterGlobally(t)
	control := openFreshHandle(t, openMaxConns).DB
	provider, err := goose.NewProvider(goose.DialectPostgres, control, nil, goose.WithTableName("control_versions"))
	if err != nil {
		t.Fatalf("NewProvider() error = %v, want nil", err)
	}
	if _, err := provider.Up(t.Context()); err != nil {
		t.Fatalf("Up() error = %v, want nil", err)
	}
	if !migrateTableExists(t, control, migrateGlobalTable) {
		t.Fatalf("a plain goose provider left %s absent, want the global migration registered", migrateGlobalTable)
	}
	db := openFreshHandle(t, openMaxConns).DB

	migrateMust(t, db, migrateValid())

	if migrateTableExists(t, db, migrateGlobalTable) {
		t.Errorf("Migrate() created %s, want the global registry ignored", migrateGlobalTable)
	}
	migrateMustVersions(t, db, migrateFirstTable, 0, 1)
}

// migrateHoldLock returns a connection to address that holds goose's default advisory lock.
func migrateHoldLock(t *testing.T, address string) *pgx.Conn {
	t.Helper()
	conn, err := pgx.Connect(t.Context(), address)
	if err != nil {
		t.Fatalf("connect the lock holder: %v", err)
	}
	t.Cleanup(func() { _ = conn.Close(context.Background()) })
	if _, err := conn.Exec(t.Context(), "SELECT pg_advisory_lock($1)", lock.DefaultLockID); err != nil {
		t.Fatalf("take goose's lock: %v", err)
	}
	return conn
}

// migrateAwaitTry returns once another session of the database of holder has tried goose's lock.
func migrateAwaitTry(t *testing.T, holder *pgx.Conn, ran <-chan error) {
	t.Helper()
	lookup := "SELECT EXISTS (SELECT FROM pg_stat_activity WHERE datname = current_database()" +
		" AND pid <> pg_backend_pid() AND query LIKE '%pg_try_advisory_lock%')"
	ticker := time.NewTicker(migrateAwaitPoll)
	defer ticker.Stop()
	for {
		var tried bool
		if err := holder.QueryRow(t.Context(), lookup).Scan(&tried); err != nil {
			t.Fatalf("look for the runner in pg_stat_activity: %v", err)
		}
		if tried {
			return
		}
		select {
		case err := <-ran:
			t.Fatalf("Migrate() = %v before it was seen trying goose's lock", err)
		case <-ticker.C:
		}
	}
}

func TestMigrateWaitsWhileAnotherSessionHoldsTheLock(t *testing.T) {
	t.Parallel()

	address := openFresh(t)
	h := mustOpen(t, address, openSmallestCap)
	holder := migrateHoldLock(t, address)
	ran := make(chan error, 1)

	go func() { ran <- postgres.Migrate(t.Context(), h.DB, migrateValid()) }()

	migrateAwaitTry(t, holder, ran)
	var absent bool
	lookup := "SELECT to_regclass('first_rows') IS NULL"
	if err := holder.QueryRow(t.Context(), lookup).Scan(&absent); err != nil || !absent {
		t.Errorf("while the runner waits, first_rows absent = %v, %v, want no migration applied", absent, err)
	}
	if _, err := holder.Exec(t.Context(), "SELECT pg_advisory_unlock($1)", lock.DefaultLockID); err != nil {
		t.Fatalf("release goose's lock: %v", err)
	}
	if err := <-ran; err != nil {
		t.Fatalf("Migrate() error = %v, want nil once the lock is free", err)
	}
	migrateMustVersions(t, h.DB, migrateFirstTable, 0, 1)
}
