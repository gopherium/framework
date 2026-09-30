// SPDX-License-Identifier: Apache-2.0

package auth_test

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/gopherium/framework/gonsole"
	"github.com/gopherium/framework/gonsole/auth"
	"github.com/gopherium/framework/gonsole/testkit"
)

// stored is one command record as the records table holds it.
type stored struct {
	actor, account, command, args, flags string
}

// guarded is a config naming manage_users, recording within a second.
func guarded() auth.Config {
	return auth.Config{
		Roles:         func(context.Context, gonsole.Call) (auth.Roles, error) { return vocabulary, nil },
		Capability:    "manage_users",
		RecordTimeout: time.Second,
	}
}

// recording returns a program over the database at address whose applied guarded commands cfg records.
func recording(
	address string, cfg auth.Config, settings map[string]string, commands ...gonsole.Command,
) gonsole.Program {
	values := map[string]string{"MYAPP_DATABASE_URL": address}
	for key, value := range settings {
		values[key] = value
	}
	p := program(address, commands...)
	p.Env = gonsole.Env{Prefix: "MYAPP_", Getenv: testkit.Getenv(values)}
	p.Record = auth.Record(cfg)
	return p
}

// recorded returns the address of a seeded database that also holds the records table.
func recorded(t *testing.T) string {
	t.Helper()
	address := seeded(t)
	if err := auth.RecordMigration().Run(t.Context(), address); err != nil {
		t.Fatalf("applying the records schema: %v", err)
	}
	return address
}

// records returns every record the database at address holds, oldest first.
func records(t *testing.T, address string) []stored {
	t.Helper()
	conn, err := pgx.Connect(t.Context(), address)
	if err != nil {
		t.Fatalf("connecting to %s: %v", address, err)
	}
	defer func() { _ = conn.Close(t.Context()) }()
	rows, err := conn.Query(t.Context(),
		"SELECT actor, coalesce(account_id::text, ''), command, args::text, flags::text "+
			"FROM gonsole.records ORDER BY applied_at")
	if err != nil {
		t.Fatalf("reading the records: %v", err)
	}
	held, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (stored, error) {
		var r stored
		err := row.Scan(&r.actor, &r.account, &r.command, &r.args, &r.flags)
		return r, err
	})
	if err != nil {
		t.Fatalf("reading the records: %v", err)
	}
	return held
}

// command returns a guarded command named name that writes nothing and prints done.
func command(name string) gonsole.Command {
	return gonsole.Command{
		Name: name, Summary: "do nothing", Capability: "manage_users",
		Run: func(_ context.Context, call gonsole.Call) error {
			_, err := fmt.Fprintln(call.Stdout, "done")
			return err
		},
	}
}

func TestRecordStoresTheActorTheCommandAndItsArguments(t *testing.T) {
	t.Parallel()

	address := recorded(t)
	p := recording(address, guarded(), nil, auth.Commands(guarded())...)

	got := testkit.Run(t, p, "", "account:role", "Editor@Example.com", "author", "-yes", "-as", " Admin@Example.com ")

	if got.Code != gonsole.ExitDone {
		t.Fatalf("code %d, want 0, stderr %q", got.Code, got.Stderr)
	}
	admin := account(t, storeAt(t, address), "admin@example.com")
	want := []stored{{
		actor: "admin@example.com", account: admin.ID.String(), command: "account:role",
		args: `["Editor@Example.com", "author"]`, flags: "{}",
	}}
	if held := records(t, address); fmt.Sprint(held) != fmt.Sprint(want) {
		t.Errorf("records = %v, want %v", held, want)
	}
}

func TestRecordStoresUnderEveryQueryMode(t *testing.T) {
	t.Parallel()

	for _, mode := range []string{"exec", "simple_protocol"} {
		t.Run(mode, func(t *testing.T) {
			t.Parallel()

			address := recorded(t)
			p := recording(address+"&default_query_exec_mode="+mode, guarded(), nil, auth.Commands(guarded())...)

			got := testkit.Run(t, p, "", "account:grant-role", "-role", "editor", "-yes", "-as", "admin@example.com")

			held := records(t, address)
			if got.Code != gonsole.ExitDone || len(held) != 1 || held[0].args != "[]" || held[0].flags != `{"role": "editor"}` {
				t.Errorf("code %d, records %v, stderr %q, want 0 and the grant recorded", got.Code, held, got.Stderr)
			}
		})
	}
}

func TestRecordStoresATimeOrderedID(t *testing.T) {
	t.Parallel()

	address := recorded(t)
	p := recording(address, guarded(), nil, command("report:purge"))
	testkit.Run(t, p, "", "report:purge", "-as", "admin@example.com")
	conn, err := pgx.Connect(t.Context(), address)
	if err != nil {
		t.Fatalf("connecting: %v", err)
	}
	defer func() { _ = conn.Close(t.Context()) }()
	var version string

	err = conn.QueryRow(t.Context(), "SELECT substr(id::text, 15, 1) FROM gonsole.records").Scan(&version)

	if err != nil || version != "7" {
		t.Errorf("id version = %q, %v, want 7", version, err)
	}
}

func TestRecordStoresNothingForADryRun(t *testing.T) {
	t.Parallel()

	address := recorded(t)
	p := authorizing(address, checked(), nil, auth.Commands(checked())...)

	got := testkit.Run(t, p, "", "account:role", "editor@example.com", "author", "-as", "admin@example.com")

	if held := records(t, address); got.Code != gonsole.ExitDone || len(held) != 0 {
		t.Errorf("code %d, records %v, want 0 and none, stderr %q", got.Code, held, got.Stderr)
	}
}

func TestRecordStoresAnEmptyListForACommandWithoutArguments(t *testing.T) {
	t.Parallel()

	address := recorded(t)
	p := recording(address, guarded(), nil, auth.Commands(guarded())...)

	got := testkit.Run(t, p, "", "account:grant-role", "-role", "editor", "-yes", "-as", "admin@example.com")

	held := records(t, address)
	if got.Code != gonsole.ExitDone || len(held) != 1 {
		t.Fatalf("code %d, records %v, want 0 and one record, stderr %q", got.Code, held, got.Stderr)
	}
	if held[0].args != "[]" || held[0].flags != `{"role": "editor"}` {
		t.Errorf("args %s, flags %s, want an empty list and the role flag", held[0].args, held[0].flags)
	}
}

func TestRecordStoresNoAccountForAnAddressNoAccountHolds(t *testing.T) {
	t.Parallel()

	address := recorded(t)
	p := recording(address, guarded(), nil, command("report:purge"))

	got := testkit.Run(t, p, "", "report:purge", "-as", "nobody@example.com")

	held := records(t, address)
	if got.Code != gonsole.ExitDone || len(held) != 1 || held[0].account != "" || held[0].actor != "nobody@example.com" {
		t.Errorf("code %d, records %v, want 0 and one record with no account", got.Code, held)
	}
}

func TestRecordNamesAMalformedTimeout(t *testing.T) {
	t.Parallel()

	address := recorded(t)
	p := recording(address, guarded(), map[string]string{"MYAPP_COMMAND_RECORD_TIMEOUT": "soon"}, command("report:purge"))

	got := testkit.Run(t, p, "", "report:purge", "-as", "admin@example.com")

	want := `myapp: MYAPP_COMMAND_RECORD_TIMEOUT: must be a duration like 30s, got "soon"` + "\n"
	if got.Code != gonsole.ExitFailed || got.Stderr != want || len(records(t, address)) != 0 {
		t.Errorf("code %d, stderr %q, want 1 and %q with no record", got.Code, got.Stderr, want)
	}
}

func TestRecordRefusesATimeoutThatIsNotAboveZero(t *testing.T) {
	t.Parallel()

	address := recorded(t)
	cfg := guarded()
	cfg.RecordTimeout = 0
	p := recording(address, cfg, nil, command("report:purge"))

	got := testkit.Run(t, p, "", "report:purge", "-as", "admin@example.com")

	want := "myapp: gonsole/auth: the record timeout must stand above zero, got 0s\n"
	if got.Code != gonsole.ExitFailed || got.Stderr != want {
		t.Errorf("code %d, stderr %q, want 1 and %q", got.Code, got.Stderr, want)
	}
}

func TestRecordEndsAtItsTimeout(t *testing.T) {
	t.Parallel()

	address := recorded(t)
	holder, err := pgx.Connect(t.Context(), address)
	if err != nil {
		t.Fatalf("connecting: %v", err)
	}
	defer func() { _ = holder.Close(t.Context()) }()
	if _, err := holder.Exec(t.Context(), "BEGIN; LOCK TABLE gonsole.records IN ACCESS EXCLUSIVE MODE"); err != nil {
		t.Fatalf("locking the records: %v", err)
	}
	p := recording(address, guarded(), map[string]string{"MYAPP_COMMAND_RECORD_TIMEOUT": "200ms"}, command("report:purge"))
	started := time.Now()

	got := testkit.Run(t, p, "", "report:purge", "-as", "admin@example.com")

	if got.Code != gonsole.ExitFailed || !strings.HasPrefix(got.Stderr, "myapp: record report:purge: ") {
		t.Errorf("code %d, stderr %q, want 1 naming the record", got.Code, got.Stderr)
	}
	if waited := time.Since(started); waited > 5*time.Second {
		t.Errorf("the run took %v, want about the timeout of 200ms", waited)
	}
}

func TestRecordNamesADatabaseItCannotReach(t *testing.T) {
	t.Parallel()

	p := recording("postgres://postgres@127.0.0.1:1/none?connect_timeout=1", guarded(), nil, command("report:purge"))

	got := testkit.Run(t, p, "", "report:purge", "-as", "admin@example.com")

	if got.Code != gonsole.ExitFailed || !strings.HasPrefix(got.Stderr, "myapp: record report:purge: ") {
		t.Errorf("code %d, stderr %q, want 1 naming the record", got.Code, got.Stderr)
	}
}

func TestRecordNeedsTheDatabaseSetting(t *testing.T) {
	t.Parallel()

	p := recording("", guarded(), nil, command("report:purge"))
	p.Database = ""

	got := testkit.Run(t, p, "", "report:purge", "-as", "admin@example.com")

	if want := "myapp: gonsole: no database setting in this call\n"; got.Code != gonsole.ExitFailed || got.Stderr != want {
		t.Errorf("code %d, stderr %q, want 1 and %q", got.Code, got.Stderr, want)
	}
}
