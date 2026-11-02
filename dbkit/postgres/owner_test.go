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
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/gopherium/framework/dbkit/postgres"
)

const (
	// ownerPassword is the password of the role every hand-over test names.
	ownerPassword = "first-password"
	// ownerOtherPassword is the password a later hand-over sets.
	ownerOtherPassword = "second-password"
	// ownerFreeWait is how long a test waits for the one connection a failed hand-over must give back.
	ownerFreeWait = 5 * time.Second
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
	"CREATE TYPE core.plain",
	"CREATE FUNCTION core.plain_in(cstring) RETURNS core.plain LANGUAGE internal STRICT AS 'int4in'",
	"CREATE FUNCTION core.plain_out(core.plain) RETURNS cstring LANGUAGE internal STRICT AS 'int4out'",
	"CREATE TYPE core.plain (INPUT = core.plain_in, OUTPUT = core.plain_out, LIKE = integer)",
	"CREATE FOREIGN DATA WRAPPER owner_wrapper",
	"CREATE SERVER owner_server FOREIGN DATA WRAPPER owner_wrapper",
	"CREATE FOREIGN TABLE core.far (id integer) SERVER owner_server",
}

// ownerReachers maps each way a role can reach the server to the statements that give role that way.
var ownerReachers = map[string]func(role, middle string) []string{
	"runs server programs": func(role, _ string) []string {
		return []string{"CREATE ROLE " + role, "GRANT pg_execute_server_program TO " + role}
	},
	"reads server files": func(role, _ string) []string {
		return []string{"CREATE ROLE " + role, "GRANT pg_read_server_files TO " + role}
	},
	"writes server files": func(role, _ string) []string {
		return []string{"CREATE ROLE " + role, "GRANT pg_write_server_files TO " + role}
	},
	"runs server programs through another role": func(role, middle string) []string {
		return []string{
			"CREATE ROLE " + role, "CREATE ROLE " + middle,
			"GRANT pg_execute_server_program TO " + middle, "GRANT " + middle + " TO " + role,
		}
	},
	"holds the membership without inheriting it": func(role, _ string) []string {
		return []string{"CREATE ROLE " + role + " NOINHERIT", "GRANT pg_execute_server_program TO " + role}
	},
	"reads server files without inheriting that": func(role, _ string) []string {
		return []string{"CREATE ROLE " + role + " NOINHERIT", "GRANT pg_read_server_files TO " + role}
	},
	"writes server files without inheriting that": func(role, _ string) []string {
		return []string{"CREATE ROLE " + role + " NOINHERIT", "GRANT pg_write_server_files TO " + role}
	},
	"runs server programs with no right to set that role": func(role, _ string) []string {
		return []string{"CREATE ROLE " + role, "GRANT pg_execute_server_program TO " + role + " WITH SET FALSE"}
	},
	"creates roles": func(role, _ string) []string {
		return []string{"CREATE ROLE " + role + " CREATEROLE"}
	},
	"belongs to a superuser": func(role, middle string) []string {
		return []string{"CREATE ROLE " + role, "CREATE ROLE " + middle + " SUPERUSER", "GRANT " + middle + " TO " + role}
	},
	"belongs to a role that creates roles": func(role, middle string) []string {
		return []string{"CREATE ROLE " + role, "CREATE ROLE " + middle + " CREATEROLE", "GRANT " + middle + " TO " + role}
	},
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

// ownerRunsAs returns address with role as the role its connections run as.
func ownerRunsAs(t *testing.T, address, role string) string {
	t.Helper()
	parsed, err := url.Parse(address)
	if err != nil {
		t.Fatal("a database address does not parse, want a URL")
	}
	query := parsed.Query()
	query.Set("role", role)
	parsed.RawQuery = query.Encode()
	return parsed.String()
}

// ownerPool lets role log in and returns a pool that logs in to the database of site as role.
func ownerPool(t *testing.T, site ownerSite, role string) *sql.DB {
	t.Helper()
	migrateAdminExec(t, site.admin, "ALTER ROLE "+role+" LOGIN PASSWORD '"+ownerPassword+"'")
	return mustOpen(t, ownerAddress(t, site.address, role, ownerPassword), openSmallestCap).DB
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

// ownerHold locks table in a transaction of site that ends with the test.
func ownerHold(t *testing.T, site ownerSite, table string) {
	t.Helper()
	holder, err := site.admin.Begin(t.Context())
	if err != nil {
		t.Fatalf("begin the transaction that holds the table: %v", err)
	}
	t.Cleanup(func() { _ = holder.Rollback(context.Background()) })
	if _, err := holder.Exec(t.Context(), "LOCK TABLE "+table+" IN ACCESS EXCLUSIVE MODE"); err != nil {
		t.Fatalf("hold the table: %v", err)
	}
}

// ownerWantUndone fails t unless handing site to role fails with prefix, returns no steps and leaves no role behind.
func ownerWantUndone(t *testing.T, site ownerSite, role, prefix string) {
	t.Helper()
	steps, err := postgres.HandOver(t.Context(), site.db, postgres.Owner{Role: role, Password: ownerPassword})
	if err == nil || !strings.HasPrefix(err.Error(), prefix) {
		t.Fatalf("HandOver() = %v, want an error that starts with %q", err, prefix)
	}
	if steps != nil {
		t.Errorf("a failed HandOver() returned %q, want no steps", steps)
	}
	if strings.Contains(err.Error(), ownerPassword) {
		t.Errorf("HandOver() error = %v, want no password in it", err)
	}
	if ownerRoleExists(t, site, role) {
		t.Error("the role of a failed hand-over exists, want every step undone")
	}
}

func TestHandOverUndoesEveryStepWhenOneFails(t *testing.T) {
	t.Parallel()

	site := ownerFresh(t, "CREATE TABLE public.versions (id bigint)")
	role := ownerRole(t, site)
	migrateAdminExec(t, site.admin, "ALTER DATABASE "+ownerDatabase(t, site)+" SET lock_timeout = '100ms'")
	site.db.SetMaxOpenConns(1)
	ownerHold(t, site, "public.versions")

	ownerWantUndone(t, site, role, "dbkit: hand over the database: ")

	ctx, cancel := context.WithTimeout(t.Context(), ownerFreeWait)
	defer cancel()
	if _, err := postgres.OwnerSteps(ctx, site.db, role); err != nil {
		t.Errorf("OwnerSteps() on the one connection after a failed hand-over = %v, want it free again", err)
	}
}

func TestHandOverReportsACommitTheServerRefusesAndReturnsNoSteps(t *testing.T) {
	t.Parallel()

	site := ownerFresh(t,
		"CREATE TABLE public.parents (id integer PRIMARY KEY)",
		"CREATE TABLE public.marks (id integer REFERENCES public.parents DEFERRABLE INITIALLY DEFERRED)",
		`CREATE FUNCTION public.mark() RETURNS event_trigger LANGUAGE plpgsql AS
			'BEGIN INSERT INTO public.marks VALUES (1); END'`,
		"CREATE EVENT TRIGGER marks ON ddl_command_end EXECUTE FUNCTION public.mark()",
	)
	role := ownerRole(t, site)

	ownerWantUndone(t, site, role, "dbkit: commit the hand-over: ")
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

	if !errors.Is(handed, postgres.ErrNotPlainRole) || !errors.Is(planned, postgres.ErrNotPlainRole) {
		t.Errorf("HandOver() = %v and OwnerSteps() = %v, want ErrNotPlainRole from both", handed, planned)
	}
	var owner string
	query := "SELECT pg_get_userbyid(relowner) FROM pg_class WHERE oid = 'public.versions'::regclass"
	if err := site.admin.QueryRow(t.Context(), query).Scan(&owner); err != nil || owner != super {
		t.Errorf("the table belongs to %q, %v, want it left with %q", owner, err, super)
	}
}

func TestHandOverAndOwnerStepsRefuseARoleThatCanReachTheServer(t *testing.T) {
	t.Parallel()

	for name, prepare := range ownerReachers {
		site := ownerFresh(t)
		role, middle := ownerRole(t, site), ownerRole(t, site)
		for _, statement := range prepare(role, middle) {
			migrateAdminExec(t, site.admin, statement)
		}

		_, handed := postgres.HandOver(t.Context(), site.db, postgres.Owner{Role: role, Password: ownerPassword})
		_, planned := postgres.OwnerSteps(t.Context(), site.db, role)

		if !errors.Is(handed, postgres.ErrNotPlainRole) || !errors.Is(planned, postgres.ErrNotPlainRole) {
			t.Errorf("a role that %s: HandOver() = %v and OwnerSteps() = %v, want ErrNotPlainRole from both",
				name, handed, planned)
		}
		if _, err := ownerLogIn(t, site, role, ownerPassword); err == nil {
			t.Errorf("a role that %s logs in, want it left as it was", name)
		}
	}
}

func TestReachesServerAnswersTrueOnTheConnectionOfARoleThatCanReachTheServer(t *testing.T) {
	t.Parallel()

	site := ownerFresh(t)
	for name, prepare := range ownerReachers {
		role, middle := ownerRole(t, site), ownerRole(t, site)
		for _, statement := range prepare(role, middle) {
			migrateAdminExec(t, site.admin, statement)
		}

		reaches, err := postgres.ReachesServer(t.Context(), ownerPool(t, site, role))

		if err != nil || !reaches {
			t.Errorf("a role that %s: ReachesServer() = %v, %v, want true", name, reaches, err)
		}
	}
}

func TestReachesServerAnswersTrueForASuperuser(t *testing.T) {
	t.Parallel()

	site := ownerFresh(t)

	reaches, err := postgres.ReachesServer(t.Context(), site.db)

	if err != nil || !reaches {
		t.Errorf("ReachesServer() as a superuser = %v, %v, want true", reaches, err)
	}
}

func TestReachesServerAnswersFalseForARoleThatCannotReachTheServer(t *testing.T) {
	t.Parallel()

	site := ownerFresh(t)
	for name, attributes := range map[string]string{
		"holds no rights":                "",
		"creates databases":              " CREATEDB",
		"belongs to a role off the list": " IN ROLE pg_monitor",
	} {
		role := ownerRole(t, site)
		migrateAdminExec(t, site.admin, "CREATE ROLE "+role+attributes)

		reaches, err := postgres.ReachesServer(t.Context(), ownerPool(t, site, role))

		if err != nil || reaches {
			t.Errorf("a role that %s: ReachesServer() = %v, %v, want false", name, reaches, err)
		}
	}
}

func TestReachesServerAnswersFalseForTheRoleAHandOverLeaves(t *testing.T) {
	t.Parallel()

	site := ownerFresh(t, ownerFixture...)
	role := ownerRole(t, site)
	ownerMust(t, site, postgres.Owner{Role: role, Password: ownerPassword})

	reaches, err := postgres.ReachesServer(t.Context(), ownerPool(t, site, role))

	if err != nil || reaches {
		t.Errorf("ReachesServer() as the new owner = %v, %v, want false", reaches, err)
	}
}

func TestReachesServerAnswersForTheLoginAndNotARoleTheConnectionSet(t *testing.T) {
	t.Parallel()

	site := ownerFresh(t)
	role := ownerRole(t, site)
	migrateAdminExec(t, site.admin, "CREATE ROLE "+role)
	server := openServer(t)
	address := ownerRunsAs(t, ownerAddress(t, site.address, server.User, server.Password), role)
	db := mustOpen(t, address, openSmallestCap).DB
	var running string
	if err := db.QueryRowContext(t.Context(), "SELECT current_user").Scan(&running); err != nil || running != role {
		t.Fatalf("the connection runs as %q, %v, want %q", running, err, role)
	}

	reaches, err := postgres.ReachesServer(t.Context(), db)

	if err != nil || !reaches {
		t.Errorf("ReachesServer() on a superuser login that set a plain role = %v, %v, want true", reaches, err)
	}
}

func TestReachesServerReportsAContextThatEnded(t *testing.T) {
	t.Parallel()

	site := ownerFresh(t)
	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	reaches, err := postgres.ReachesServer(ctx, site.db)

	if !errors.Is(err, context.Canceled) || !strings.HasPrefix(err.Error(), "dbkit: read what the role can reach: ") {
		t.Errorf("ReachesServer() = %v, want the failed read wrapping context.Canceled", err)
	}
	if reaches {
		t.Error("ReachesServer() answered true on a failed read, want false")
	}
}

func TestHandOverAcceptsARoleNameOfTheLongestLengthTheServerKeeps(t *testing.T) {
	t.Parallel()

	site := ownerFresh(t)
	role := strings.Repeat("r", 53) + strings.ToLower(rand.Text())[:10]
	ownerDropsRole(t, site, role)

	ownerMust(t, site, postgres.Owner{Role: role, Password: ownerPassword})

	if again := ownerMust(t, site, postgres.Owner{Role: role, Password: ownerPassword}); len(again) != 0 {
		t.Errorf("a second HandOver() for a name of %d bytes ran %q, want nothing", len(role), again)
	}
}

func TestOwnerStepsAcceptsARoleNameOfOneByte(t *testing.T) {
	t.Parallel()

	site := ownerFresh(t)

	steps, err := postgres.OwnerSteps(t.Context(), site.db, "r")

	if err != nil || len(steps) == 0 {
		t.Errorf("OwnerSteps() = %q, %v, want the steps for a name of one byte", steps, err)
	}
}

func TestHandOverAndOwnerStepsRefuseARoleNameTheServerCannotKeep(t *testing.T) {
	t.Parallel()

	for name, role := range map[string]string{
		"no bytes":            "",
		"64 ASCII bytes":      strings.Repeat("r", 64),
		"32 two-byte letters": strings.Repeat("é", 32),
	} {
		site := ownerFresh(t)

		_, handed := postgres.HandOver(t.Context(), site.db, postgres.Owner{Role: role})
		_, planned := postgres.OwnerSteps(t.Context(), site.db, role)

		if !errors.Is(handed, postgres.ErrRoleName) || !errors.Is(planned, postgres.ErrRoleName) {
			t.Errorf("a name of %s: HandOver() = %v and OwnerSteps() = %v, want ErrRoleName from both", name, handed, planned)
		}
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
