// SPDX-License-Identifier: Apache-2.0

package postgres_test

import (
	"context"
	"crypto/rand"
	"database/sql"
	"errors"
	"net/url"
	"slices"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"

	"github.com/gopherium/framework/dbkit/postgres"
)

const (
	// ownerPassword is the password of the role every hand-over test names.
	ownerPassword = "first-password"
	// ownerOtherPassword is the password a later hand-over sets.
	ownerOtherPassword = "second-password"
	// ownerOthers lists what a role does not own among the database's schemas, relations, routines and types.
	ownerOthers = `
SELECT 'schema ' || nspname FROM pg_namespace
WHERE nspname = 'core' AND pg_get_userbyid(nspowner) <> $1
UNION ALL
SELECT 'relation ' || relname FROM pg_class
WHERE relnamespace IN ('core'::regnamespace, 'public'::regnamespace) AND pg_get_userbyid(relowner) <> $1
UNION ALL
SELECT 'routine ' || proname FROM pg_proc
WHERE pronamespace = 'core'::regnamespace AND pg_get_userbyid(proowner) <> $1
UNION ALL
SELECT 'type ' || typname FROM pg_type
WHERE typnamespace = 'core'::regnamespace AND pg_get_userbyid(typowner) <> $1
UNION ALL
SELECT 'database ' || datname FROM pg_database
WHERE datname = current_database() AND pg_get_userbyid(datdba) <> $1`
)

// ownerFixture creates one object of every kind a hand-over moves.
var ownerFixture = []string{
	"CREATE SCHEMA core",
	"CREATE TABLE public.versions (id bigint)",
	"CREATE TABLE core.serials (id bigserial PRIMARY KEY, note text)",
	"CREATE TABLE core.identities (id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY)",
	"CREATE TABLE core.parts (id bigint PRIMARY KEY) PARTITION BY RANGE (id)",
	"CREATE TABLE core.parts_one PARTITION OF core.parts FOR VALUES FROM (0) TO (10)",
	"CREATE VIEW core.names AS SELECT note FROM core.serials",
	"CREATE MATERIALIZED VIEW core.kept AS SELECT note FROM core.serials",
	"CREATE SEQUENCE core.free",
	"CREATE FUNCTION core.one(integer, text) RETURNS integer LANGUAGE sql AS 'SELECT 1'",
	"CREATE PROCEDURE core.nothing() LANGUAGE sql AS 'SELECT 1'",
	"CREATE AGGREGATE core.total(integer) (SFUNC = int4pl, STYPE = integer)",
	"CREATE TYPE core.state AS ENUM ('draft')",
	"CREATE TYPE core.pair AS (first integer, second integer)",
	"CREATE TYPE core.span AS RANGE (SUBTYPE = integer)",
	"CREATE DOMAIN core.word AS text",
	"CREATE FOREIGN DATA WRAPPER owner_wrapper",
	"CREATE SERVER owner_server FOREIGN DATA WRAPPER owner_wrapper",
	"CREATE FOREIGN TABLE core.far (id integer) SERVER owner_server",
}

// ownerSite is a fresh database reached as the test server's own user.
type ownerSite struct {
	address string
	admin   *pgx.Conn
	db      *sql.DB
}

// ownerFresh returns a fresh database holding statements, reached as the test server's own user.
func ownerFresh(t *testing.T, statements ...string) ownerSite {
	t.Helper()
	address := openFresh(t)
	admin := migrateAsServer(t, address)
	for _, statement := range statements {
		migrateAdminExec(t, admin, statement)
	}
	return ownerSite{address: address, admin: admin, db: mustOpen(t, ownerAddress(t, address, openServer(t).User,
		openServer(t).Password), openSmallestCap).DB}
}

// ownerAddress returns address with its user and password replaced.
func ownerAddress(t *testing.T, address, user, password string) string {
	t.Helper()
	parsed, err := url.Parse(address)
	if err != nil {
		t.Fatal("a database address does not parse, want a URL")
	}
	parsed.User = url.UserPassword(user, password)
	return parsed.String()
}

// ownerRole returns a name no role holds yet, whose role goes when the test ends.
func ownerRole(t *testing.T, site ownerSite) string {
	t.Helper()
	role := "dbkit_owner_" + strings.ToLower(rand.Text())
	ownerDropsRole(t, site, role)
	return role
}

// ownerDropsRole gives what role owns back to the database's first owner and drops role when the test ends.
func ownerDropsRole(t *testing.T, site ownerSite, role string) {
	t.Helper()
	parsed, err := url.Parse(site.address)
	if err != nil {
		t.Fatal("a database address does not parse, want a URL")
	}
	name := pgx.Identifier{role}.Sanitize()
	t.Cleanup(func() {
		ctx := context.Background()
		var exists bool
		found := site.admin.QueryRow(ctx, "SELECT EXISTS (SELECT FROM pg_roles WHERE rolname = $1)", role).Scan(&exists)
		if found != nil || !exists {
			return
		}
		_, given := site.admin.Exec(ctx, "REASSIGN OWNED BY "+name+" TO "+parsed.User.Username())
		_, dropped := site.admin.Exec(ctx, "DROP ROLE "+name)
		if err := errors.Join(given, dropped); err != nil {
			t.Errorf("drop the role of the test: %v", err)
		}
	})
}

// ownerMust hands the database of site to owner and fails the test when it cannot.
func ownerMust(t *testing.T, site ownerSite, owner postgres.Owner) []string {
	t.Helper()
	steps, err := postgres.HandOver(t.Context(), site.db, owner)
	if err != nil {
		t.Fatalf("HandOver() error = %v, want nil", err)
	}
	return steps
}

// ownerLeftOut returns what role does not own in the database of site.
func ownerLeftOut(t *testing.T, site ownerSite, role string) []string {
	t.Helper()
	rows, err := site.admin.Query(t.Context(), ownerOthers, role)
	if err != nil {
		t.Fatalf("list what the role does not own: %v", err)
	}
	left, err := pgx.CollectRows(rows, pgx.RowTo[string])
	if err != nil {
		t.Fatalf("read what the role does not own: %v", err)
	}
	return left
}

// ownerLogIn connects to the database of site as role with password.
func ownerLogIn(t *testing.T, site ownerSite, role, password string) (*pgx.Conn, error) {
	t.Helper()
	conn, err := pgx.Connect(t.Context(), ownerAddress(t, site.address, role, password))
	if err != nil {
		return nil, err
	}
	t.Cleanup(func() { _ = conn.Close(context.Background()) })
	return conn, nil
}

// ownerRoleExists reports whether a role named role exists.
func ownerRoleExists(t *testing.T, site ownerSite, role string) bool {
	t.Helper()
	var exists bool
	query := "SELECT EXISTS (SELECT FROM pg_roles WHERE rolname = $1)"
	if err := site.admin.QueryRow(t.Context(), query, role).Scan(&exists); err != nil {
		t.Fatalf("look for the role: %v", err)
	}
	return exists
}

func TestHandOverGivesARoleTheDatabaseAndAllItHolds(t *testing.T) {
	t.Parallel()

	site := ownerFresh(t, ownerFixture...)
	role := ownerRole(t, site)

	ownerMust(t, site, postgres.Owner{Role: role, Password: ownerPassword})

	if left := ownerLeftOut(t, site, role); len(left) != 0 {
		t.Errorf("the role does not own %q, want it to own everything", left)
	}
}

func TestHandOverLeavesARoleThatLogsInAndChangesTheSchema(t *testing.T) {
	t.Parallel()

	site := ownerFresh(t, ownerFixture...)
	role := ownerRole(t, site)
	ownerMust(t, site, postgres.Owner{Role: role, Password: ownerPassword})

	conn, err := ownerLogIn(t, site, role, ownerPassword)
	if err != nil {
		t.Fatalf("log in as the role: %v", err)
	}

	var super bool
	query := "SELECT rolsuper FROM pg_roles WHERE rolname = current_user"
	if err := conn.QueryRow(t.Context(), query).Scan(&super); err != nil || super {
		t.Errorf("the role is a superuser = %v, %v, want a plain role", super, err)
	}
	for _, statement := range []string{
		"ALTER TABLE core.serials ADD COLUMN added integer",
		"CREATE TABLE core.later (id integer)",
		"CREATE SCHEMA later",
		"ALTER TYPE core.state ADD VALUE 'published'",
		"ALTER SEQUENCE core.free RESTART",
		"DROP FUNCTION core.one(integer, text)",
		"INSERT INTO core.serials (note) VALUES ('kept')",
	} {
		if _, err := conn.Exec(t.Context(), statement); err != nil {
			t.Errorf("Exec(%q) as the role = %v, want nil", statement, err)
		}
	}
}

func TestHandOverQuotesANameAndAPasswordThatNeedIt(t *testing.T) {
	t.Parallel()

	site := ownerFresh(t, `CREATE SCHEMA "odd name"`, `CREATE TABLE "odd name"."odd table" (id integer)`)
	role := `Odd "role" ` + strings.ToLower(rand.Text())
	ownerDropsRole(t, site, role)
	password := `it's a \ "password"`

	ownerMust(t, site, postgres.Owner{Role: role, Password: password})

	if _, err := ownerLogIn(t, site, role, password); err != nil {
		t.Errorf("log in as the role: %v", err)
	}
	var owner string
	query := `SELECT pg_get_userbyid(relowner) FROM pg_class WHERE oid = '"odd name"."odd table"'::regclass`
	if err := site.admin.QueryRow(t.Context(), query).Scan(&owner); err != nil || owner != role {
		t.Errorf("the table belongs to %q, %v, want %q", owner, err, role)
	}
}

func TestHandOverLeavesNothingToDoOnASecondRun(t *testing.T) {
	t.Parallel()

	site := ownerFresh(t, ownerFixture...)
	role := ownerRole(t, site)
	ownerMust(t, site, postgres.Owner{Role: role, Password: ownerPassword})

	steps, err := postgres.OwnerSteps(t.Context(), site.db, role)
	if err != nil || len(steps) != 0 {
		t.Errorf("OwnerSteps() after a hand-over = %q, %v, want nothing to do", steps, err)
	}
	if again := ownerMust(t, site, postgres.Owner{Role: role, Password: ownerPassword}); len(again) != 0 {
		t.Errorf("a second HandOver() ran %q, want nothing", again)
	}
}

func TestHandOverSetsTheNewPasswordOfARoleThatExists(t *testing.T) {
	t.Parallel()

	site := ownerFresh(t)
	role := ownerRole(t, site)
	ownerMust(t, site, postgres.Owner{Role: role, Password: ownerPassword})

	ownerMust(t, site, postgres.Owner{Role: role, Password: ownerOtherPassword})

	if _, err := ownerLogIn(t, site, role, ownerOtherPassword); err != nil {
		t.Errorf("log in with the new password: %v", err)
	}
	if _, err := ownerLogIn(t, site, role, ownerPassword); err == nil {
		t.Error("the old password still logs in, want it replaced")
	}
}

func TestHandOverKeepsThePasswordWhenTheOwnerNamesNone(t *testing.T) {
	t.Parallel()

	site := ownerFresh(t)
	role := ownerRole(t, site)
	ownerMust(t, site, postgres.Owner{Role: role, Password: ownerPassword})

	ownerMust(t, site, postgres.Owner{Role: role})

	if _, err := ownerLogIn(t, site, role, ownerPassword); err != nil {
		t.Errorf("log in with the password the role had: %v", err)
	}
}

func TestHandOverNeverReturnsThePassword(t *testing.T) {
	t.Parallel()

	site := ownerFresh(t, ownerFixture...)
	role := ownerRole(t, site)

	steps := ownerMust(t, site, postgres.Owner{Role: role, Password: ownerPassword})

	if strings.Contains(strings.Join(steps, "\n"), ownerPassword) {
		t.Errorf("HandOver() returned %q, want no step that holds the password", steps)
	}
}

func TestHandOverLetsARoleWithNoLoginLogIn(t *testing.T) {
	t.Parallel()

	site := ownerFresh(t)
	role := ownerRole(t, site)
	migrateAdminExec(t, site.admin, "CREATE ROLE "+role+" NOLOGIN")

	ownerMust(t, site, postgres.Owner{Role: role, Password: ownerPassword})

	if _, err := ownerLogIn(t, site, role, ownerPassword); err != nil {
		t.Errorf("log in as a role that had no login: %v", err)
	}
}

func TestHandOverLeavesWhatAnExtensionHolds(t *testing.T) {
	t.Parallel()

	site := ownerFresh(t, "CREATE EXTENSION pg_trgm", "CREATE EXTENSION pg_buffercache", "CREATE EXTENSION cube",
		"CREATE EXTENSION earthdistance", "CREATE TABLE public.versions (id bigint)")
	role := ownerRole(t, site)

	steps := ownerMust(t, site, postgres.Owner{Role: role})

	want := []string{
		"CREATE ROLE " + role + " LOGIN",
		"ALTER DATABASE " + ownerDatabase(t, site) + " OWNER TO " + role,
		"ALTER TABLE public.versions OWNER TO " + role,
	}
	if !slices.Equal(steps, want) {
		t.Errorf("HandOver() ran %q, want %q", steps, want)
	}
}

// ownerDatabase returns the name of the database of site.
func ownerDatabase(t *testing.T, site ownerSite) string {
	t.Helper()
	var name string
	if err := site.admin.QueryRow(t.Context(), "SELECT current_database()").Scan(&name); err != nil {
		t.Fatalf("read the name of the database: %v", err)
	}
	return name
}

func TestHandOverUndoesEveryStepWhenOneFails(t *testing.T) {
	t.Parallel()

	site := ownerFresh(t, "CREATE TABLE public.versions (id bigint)")
	role := ownerRole(t, site)
	holder, err := site.admin.Begin(t.Context())
	if err != nil {
		t.Fatalf("begin the transaction that holds the table: %v", err)
	}
	defer func() { _ = holder.Rollback(context.Background()) }()
	if _, err := holder.Exec(t.Context(), "LOCK TABLE public.versions IN ACCESS EXCLUSIVE MODE"); err != nil {
		t.Fatalf("hold the table: %v", err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), openShortWait)
	defer cancel()

	_, err = postgres.HandOver(ctx, site.db, postgres.Owner{Role: role, Password: ownerPassword})

	if err == nil {
		t.Fatal("HandOver() on a table another transaction holds = nil, want the failure of the step")
	}
	if strings.Contains(err.Error(), ownerPassword) {
		t.Errorf("HandOver() error = %v, want no password in it", err)
	}
	if ownerRoleExists(t, site, role) {
		t.Error("the role of a failed hand-over exists, want every step undone")
	}
}

func TestHandOverAndOwnerStepsRefuseAConnectionThatIsNoSuperuser(t *testing.T) {
	t.Parallel()

	site := ownerFresh(t)
	role := ownerRole(t, site)
	plain := mustOpen(t, site.address, openSmallestCap).DB

	_, handed := postgres.HandOver(t.Context(), plain, postgres.Owner{Role: role})
	_, planned := postgres.OwnerSteps(t.Context(), plain, role)

	if !errors.Is(handed, postgres.ErrNotSuperuser) || !errors.Is(planned, postgres.ErrNotSuperuser) {
		t.Errorf("HandOver() = %v and OwnerSteps() = %v, want ErrNotSuperuser from both", handed, planned)
	}
	if ownerRoleExists(t, site, role) {
		t.Error("the role exists after a refused hand-over, want nothing done")
	}
}

func TestHandOverAndOwnerStepsRefuseARoleThatIsASuperuser(t *testing.T) {
	t.Parallel()

	site := ownerFresh(t, "CREATE TABLE public.versions (id bigint)")
	super := openServer(t).User

	_, handed := postgres.HandOver(t.Context(), site.db, postgres.Owner{Role: super})
	_, planned := postgres.OwnerSteps(t.Context(), site.db, super)

	if !errors.Is(handed, postgres.ErrSuperuserRole) || !errors.Is(planned, postgres.ErrSuperuserRole) {
		t.Errorf("HandOver() = %v and OwnerSteps() = %v, want ErrSuperuserRole from both", handed, planned)
	}
	var owner string
	query := "SELECT pg_get_userbyid(relowner) FROM pg_class WHERE oid = 'public.versions'::regclass"
	if err := site.admin.QueryRow(t.Context(), query).Scan(&owner); err != nil || owner != super {
		t.Errorf("the table belongs to %q, %v, want it left with %q", owner, err, super)
	}
}

func TestHandOverAndOwnerStepsReportAContextThatEnded(t *testing.T) {
	t.Parallel()

	site := ownerFresh(t)
	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	_, handed := postgres.HandOver(ctx, site.db, postgres.Owner{Role: "dbkit_owner_unused"})
	_, planned := postgres.OwnerSteps(ctx, site.db, "dbkit_owner_unused")

	if !errors.Is(handed, context.Canceled) || !errors.Is(planned, context.Canceled) {
		t.Errorf("HandOver() = %v and OwnerSteps() = %v, want context.Canceled from both", handed, planned)
	}
}

func TestHandOverReportsAReadTheServerRefuses(t *testing.T) {
	t.Parallel()

	site := ownerFresh(t)

	_, err := postgres.HandOver(t.Context(), site.db, postgres.Owner{Role: "held\x00byte"})

	if err == nil || !strings.HasPrefix(err.Error(), "dbkit: read the owner steps: ") {
		t.Errorf("HandOver() with a NUL byte in the role = %v, want the failed read", err)
	}
}

func TestOwnerStepsNamesEachStepInOrderAndChangesNothing(t *testing.T) {
	t.Parallel()

	site := ownerFresh(t,
		"CREATE SCHEMA core",
		"CREATE TABLE core.serials (id bigserial PRIMARY KEY)",
		"CREATE VIEW core.names AS SELECT id FROM core.serials",
		"CREATE SEQUENCE core.free",
		"CREATE FUNCTION core.one(integer, text) RETURNS integer LANGUAGE sql AS 'SELECT 1'",
		"CREATE DOMAIN core.word AS text",
	)
	role := ownerRole(t, site)

	steps, err := postgres.OwnerSteps(t.Context(), site.db, role)

	to := " OWNER TO " + role
	want := []string{
		"CREATE ROLE " + role + " LOGIN",
		"ALTER DATABASE " + ownerDatabase(t, site) + to,
		"ALTER SCHEMA core" + to,
		"ALTER SEQUENCE core.free" + to,
		"ALTER TABLE core.serials" + to,
		"ALTER VIEW core.names" + to,
		"ALTER ROUTINE core.one(integer, text)" + to,
		"ALTER TYPE core.word" + to,
	}
	if err != nil || !slices.Equal(steps, want) {
		t.Errorf("OwnerSteps() = %q, %v, want %q", steps, err, want)
	}
	if ownerRoleExists(t, site, role) {
		t.Error("OwnerSteps() created the role, want nothing changed")
	}
}
