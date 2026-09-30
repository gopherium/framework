// SPDX-License-Identifier: Apache-2.0

package auth_test

import (
	"encoding/json"
	"strconv"
	"strings"
	"testing"

	"github.com/gopherium/framework/gonsole"
	"github.com/gopherium/framework/gonsole/auth"
	"github.com/gopherium/framework/gonsole/testkit"
)

// reading returns a program over the database at address that applies guarded commands and lists their records.
func reading(address string, cfg auth.Config, settings map[string]string) gonsole.Program {
	commands := append(auth.Commands(cfg), auth.Records(cfg), command("report:purge"), command("report:export"))
	return authorizing(address, cfg, settings, commands...)
}

// limited returns a config over capable whose records listing falls back to limit.
func limited(limit int) auth.Config {
	cfg := checked()
	cfg.RecordsLimit = limit
	return cfg
}

// apply runs each command line on p as the admin, failing the test when one fails.
func apply(t *testing.T, p gonsole.Program, lines ...[]string) {
	t.Helper()
	for _, line := range lines {
		got := testkit.Run(t, p, "", append(line, "-as", "admin@example.com")...)
		if got.Code != gonsole.ExitDone {
			t.Fatalf("%v: code %d, stderr %q", line, got.Code, got.Stderr)
		}
	}
}

// lines returns the lines of text, none for an empty text.
func lines(text string) []string {
	if text == "" {
		return nil
	}
	return strings.Split(strings.TrimSuffix(text, "\n"), "\n")
}

// titled returns a guarded command named name that takes a title and prints done.
func titled(name string) gonsole.Command {
	cmd := command(name)
	cmd.Args = []string{"title"}
	return cmd
}

func TestRecordsListsTheLatestFirst(t *testing.T) {
	t.Parallel()

	address := recorded(t)
	p := reading(address, limited(50), nil)
	apply(t, p, []string{"report:purge"}, []string{"account:grant-role", "-role", "editor", "-yes"})

	got := testkit.Run(t, p, "", "account:records")

	listing := lines(got.Stdout)
	if got.Code != gonsole.ExitDone || len(listing) != 2 {
		t.Fatalf("code %d, stdout %q, want 0 and two lines, stderr %q", got.Code, got.Stdout, got.Stderr)
	}
	if !strings.HasSuffix(listing[0], "  admin@example.com  account:grant-role  -role editor") {
		t.Errorf("first line = %q, want the grant with its flag", listing[0])
	}
	if !strings.HasSuffix(listing[1], "  admin@example.com  report:purge") {
		t.Errorf("second line = %q, want the purge", listing[1])
	}
}

func TestRecordsShowsTheArgumentsAsTyped(t *testing.T) {
	t.Parallel()

	address := recorded(t)
	p := reading(address, limited(50), nil)
	apply(t, p, []string{"account:role", "Editor@Example.com", "author", "-yes"})

	got := testkit.Run(t, p, "", "account:records")

	if !strings.HasSuffix(got.Stdout, "  admin@example.com  account:role  Editor@Example.com author\n") {
		t.Errorf("stdout %q, want the role change with its arguments", got.Stdout)
	}
}

func TestRecordsAnswersOneDocument(t *testing.T) {
	t.Parallel()

	address := recorded(t)
	p := reading(address, limited(50), nil)
	apply(t, p, []string{"account:grant-role", "-role", "editor", "-yes"})
	admin := account(t, storeAt(t, address), "admin@example.com")

	got := testkit.Run(t, p, "", "account:records", "-json")

	var answer struct {
		Records []struct {
			AppliedAt string            `json:"applied_at"`
			Actor     string            `json:"actor"`
			AccountID *string           `json:"account_id"`
			Command   string            `json:"command"`
			Args      []string          `json:"args"`
			Flags     map[string]string `json:"flags"`
		} `json:"records"`
	}
	if err := json.Unmarshal([]byte(got.Stdout), &answer); err != nil || len(answer.Records) != 1 {
		t.Fatalf("stdout %q, want one record, error %v", got.Stdout, err)
	}
	held := answer.Records[0]
	if held.AppliedAt == "" || held.Actor != "admin@example.com" || held.AccountID == nil ||
		*held.AccountID != admin.ID.String() || held.Command != "account:grant-role" ||
		held.Args == nil || len(held.Args) != 0 || held.Flags["role"] != "editor" {
		t.Errorf("record = %+v, want the grant by the admin", held)
	}
}

func TestRecordsAnswersAnEmptyListWhenNoneWereApplied(t *testing.T) {
	t.Parallel()

	address := recorded(t)
	p := reading(address, limited(50), nil)

	text := testkit.Run(t, p, "", "account:records")
	document := testkit.Run(t, p, "", "account:records", "-json")

	if text.Code != gonsole.ExitDone || text.Stdout != "" {
		t.Errorf("text: code %d, stdout %q, want 0 and nothing", text.Code, text.Stdout)
	}
	if document.Code != gonsole.ExitDone || document.Stdout != "{\n  \"records\": []\n}\n" {
		t.Errorf("json: code %d, stdout %q, want 0 and an empty list", document.Code, document.Stdout)
	}
}

func TestRecordsKeepsToTheLimit(t *testing.T) {
	t.Parallel()

	address := recorded(t)
	p := reading(address, limited(2), nil)
	apply(t, p, []string{"report:purge"}, []string{"report:export"}, []string{"report:purge"})
	capped := reading(address, limited(2), map[string]string{"MYAPP_COMMAND_RECORDS_LIMIT": "1"})

	fallback := testkit.Run(t, p, "", "account:records")
	setting := testkit.Run(t, capped, "", "account:records")
	flagged := testkit.Run(t, capped, "", "account:records", "-limit", "3")

	for _, got := range []testkit.Result{fallback, setting, flagged} {
		if got.Code != gonsole.ExitDone {
			t.Errorf("code %d, stderr %q, want 0", got.Code, got.Stderr)
		}
	}
	if count := len(lines(fallback.Stdout)); count != 2 {
		t.Errorf("with the fallback of 2: %d lines, want 2", count)
	}
	if count := len(lines(setting.Stdout)); count != 1 {
		t.Errorf("with the setting of 1: %d lines, want 1", count)
	}
	if count := len(lines(flagged.Stdout)); count != 3 {
		t.Errorf("with -limit 3: %d lines, want 3", count)
	}
}

func TestRecordsRefusesALimitItCannotKeepTo(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		cfg      auth.Config
		settings map[string]string
		args     []string
		code     int
		want     string
	}{
		{"a flag not above zero", limited(50), nil, []string{"-limit", "0"}, gonsole.ExitMisused,
			"myapp: account:records wants -limit above zero, got 0\n"},
		{"a flag below zero", limited(50), nil, []string{"-limit", "-1"}, gonsole.ExitMisused,
			"myapp: account:records wants -limit above zero, got -1\n"},
		{"a flag that is no number", limited(50), nil, []string{"-limit", "all"}, gonsole.ExitMisused,
			`myapp: account:records: invalid value "all" for flag -limit: parse error` + "\n"},
		{"a malformed setting", limited(50), map[string]string{"MYAPP_COMMAND_RECORDS_LIMIT": "all"}, nil,
			gonsole.ExitFailed, `myapp: MYAPP_COMMAND_RECORDS_LIMIT: must be a whole number, got "all"` + "\n"},
		{"a fallback not above zero", limited(0), nil, nil, gonsole.ExitFailed,
			"myapp: gonsole/auth: the records limit must stand above zero, got 0\n"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			p := reading(recorded(t), tt.cfg, tt.settings)

			got := testkit.Run(t, p, "", append([]string{"account:records"}, tt.args...)...)

			if got.Code != tt.code || !strings.HasPrefix(got.Stderr, tt.want) {
				t.Errorf("code %d, stderr %q, want %d and %q", got.Code, got.Stderr, tt.code, tt.want)
			}
		})
	}
}

func TestRecordsNamesTheMigrateCommandWhenTheRecordsAreMissing(t *testing.T) {
	t.Parallel()

	p := reading(seeded(t), limited(50), nil)

	got := testkit.Run(t, p, "", "account:records")

	want := "myapp: the command records are missing, run migrate first\n"
	if got.Code != gonsole.ExitFailed || got.Stderr != want {
		t.Errorf("code %d, stderr %q, want 1 and %q", got.Code, got.Stderr, want)
	}
}

func TestRecordsPassesAFailedReadThrough(t *testing.T) {
	t.Parallel()

	address := recorded(t)
	run(t, address, "ALTER TABLE gonsole.records DROP COLUMN flags")
	p := reading(address, limited(50), nil)

	got := testkit.Run(t, p, "", "account:records")

	if got.Code != gonsole.ExitFailed || got.Stdout != "" || !strings.Contains(got.Stderr, "flags") {
		t.Errorf("code %d, stdout %q, stderr %q, want 1 naming the missing column", got.Code, got.Stdout, got.Stderr)
	}
}

func TestRecordsNamesADatabaseItCannotReach(t *testing.T) {
	t.Parallel()

	p := reading("postgres://postgres@127.0.0.1:1/none?connect_timeout=1", limited(50), nil)

	got := testkit.Run(t, p, "", "account:records")

	if got.Code != gonsole.ExitFailed || got.Stdout != "" ||
		!strings.HasPrefix(got.Stderr, "myapp: failed to connect to ") {
		t.Errorf("code %d, stdout %q, stderr %q, want 1 and nothing listed", got.Code, got.Stdout, got.Stderr)
	}
}

func TestRecordsListsTheFlagsByName(t *testing.T) {
	t.Parallel()

	address := recorded(t)
	run(t, address, `INSERT INTO gonsole.records (id, actor, command, args, flags) VALUES (gen_random_uuid(), `+
		`'admin@example.com', 'report:purge', '[]', '{"zone": "d", "after": "a", "limit": "c", "kind": "b"}')`)
	p := reading(address, limited(50), nil)

	got := testkit.Run(t, p, "", "account:records")

	if !strings.HasSuffix(got.Stdout, "  admin@example.com  report:purge  -after a -kind b -limit c -zone d\n") {
		t.Errorf("stdout %q, want the flags sorted by name", got.Stdout)
	}
}

func TestRecordsQuotesAValueThatWouldReadWrongly(t *testing.T) {
	t.Parallel()

	address := recorded(t)
	cfg := limited(50)
	p := authorizing(address, cfg, nil, auth.Records(cfg), titled("report:create"))
	titles := []string{"Q3\nforged", "a\tb", `"a\nb"`, ""}
	for _, title := range titles {
		apply(t, p, []string{"report:create", title})
	}

	got := testkit.Run(t, p, "", "account:records")

	listing := lines(got.Stdout)
	if got.Code != gonsole.ExitDone || len(listing) != len(titles) {
		t.Fatalf("code %d, stdout %q, want %d lines", got.Code, got.Stdout, len(titles))
	}
	for i, title := range titles {
		if want := "  report:create  " + strconv.Quote(title); !strings.HasSuffix(listing[len(titles)-1-i], want) {
			t.Errorf("line %q, want it to end in %s", listing[len(titles)-1-i], want)
		}
	}
}

func TestRecordsListsTheNewerIDFirstWithinOneMoment(t *testing.T) {
	t.Parallel()

	address := recorded(t)
	run(t, address,
		`INSERT INTO gonsole.records (id, applied_at, actor, command, args, flags) VALUES `+
			`('01920000-0000-7000-8000-000000000001', '2026-09-30 10:00:00+00', 'admin@example.com', `+
			`'report:purge', '[]', '{}'), `+
			`('01920000-0000-7000-8000-000000000002', '2026-09-30 10:00:00+00', 'admin@example.com', `+
			`'report:export', '[]', '{}')`)
	p := reading(address, limited(50), nil)

	got := testkit.Run(t, p, "", "account:records")

	listing := lines(got.Stdout)
	if len(listing) != 2 || !strings.HasSuffix(listing[0], "report:export") {
		t.Errorf("stdout %q, want the export, with the newer id, first", got.Stdout)
	}
}
