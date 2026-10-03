// SPDX-License-Identifier: Apache-2.0

package auth_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/gopherium/framework/gonsole"
	"github.com/gopherium/framework/gonsole/auth"
	"github.com/gopherium/framework/gonsole/testkit"
)

// capable is vocabulary with the capabilities each role carries.
var capable = auth.Roles{
	Known:        vocabulary.Known,
	Privileged:   vocabulary.Privileged,
	Capabilities: map[string][]string{"admin": {"manage_users", "change_others_work"}, "editor": {"change_others_work"}},
}

// checked is a config over capable naming manage_users, recording within a second.
func checked() auth.Config {
	cfg := guarded()
	cfg.Roles = func(context.Context, gonsole.Call) (auth.Roles, error) { return capable, nil }
	return cfg
}

// authorizing returns a program over the database at address that checks and records its guarded commands with cfg.
func authorizing(
	address string, cfg auth.Config, settings map[string]string, commands ...gonsole.Command,
) gonsole.Program {
	p := recording(address, cfg, settings, commands...)
	p.Authorize = auth.Authorize(cfg)
	return p
}

func TestAuthorizeLetsAnAccountWhoseRoleCarriesTheCapability(t *testing.T) {
	t.Parallel()

	address := recorded(t)
	p := authorizing(address, checked(), nil, command("report:purge"))

	got := testkit.Run(t, p, "", "report:purge", "-as", " Admin@Example.COM ")

	if got.Code != gonsole.ExitDone || got.Stdout != "done\n" || len(records(t, address)) != 1 {
		t.Errorf("code %d, stdout %q, stderr %q, want 0, done and one record", got.Code, got.Stdout, got.Stderr)
	}
}

func TestAuthorizeRefusesAnActorItCannotLetThrough(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		setup []string
		actor string
		want  string
	}{
		{"a role lacking the capability", nil, "editor@example.com",
			"myapp: the account editor@example.com holds the role editor, which lacks manage_users\n"},
		{"an address no account holds", nil, "nobody@example.com",
			"myapp: no account answers to nobody@example.com\n"},
		{"a disabled account", []string{"UPDATE auth.users SET disabled = true WHERE email = 'admin@example.com'"},
			"admin@example.com", "myapp: the account admin@example.com is disabled\n"},
		{"an account never activated", []string{"UPDATE auth.users SET confirmed = false WHERE email = 'admin@example.com'"},
			"admin@example.com", "myapp: the account admin@example.com was never activated\n"},
		{"an account holding no role", []string{"UPDATE auth.users SET role = '' WHERE email = 'author@example.com'"},
			"author@example.com", "myapp: the account author@example.com holds no role, so it lacks manage_users\n"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			address := recorded(t)
			run(t, address, tt.setup...)
			p := authorizing(address, checked(), nil, command("report:purge"))

			got := testkit.Run(t, p, "", "report:purge", "-as", tt.actor)

			if got.Code != gonsole.ExitFailed || got.Stdout != "" || got.Stderr != tt.want {
				t.Errorf("code %d, stdout %q, stderr %q, want 1, nothing run and %q", got.Code, got.Stdout, got.Stderr, tt.want)
			}
			if held := records(t, address); len(held) != 0 {
				t.Errorf("records = %v, want none", held)
			}
		})
	}
}

func TestAuthorizeChecksADryRunToo(t *testing.T) {
	t.Parallel()

	address := recorded(t)
	p := authorizing(address, checked(), nil, auth.Commands(checked())...)

	got := testkit.Run(t, p, "", "account:role", "author@example.com", "editor", "-as", "editor@example.com")

	if got.Code != gonsole.ExitFailed || !strings.Contains(got.Stderr, "which lacks manage_users") {
		t.Errorf("code %d, stderr %q, want 1 naming the missing capability", got.Code, got.Stderr)
	}
}

func TestAuthorizeTreatsABlankActorAsAMisuse(t *testing.T) {
	t.Parallel()

	address := recorded(t)
	p := authorizing(address, checked(), nil, command("report:purge"))

	got := testkit.Run(t, p, "", "report:purge", "-as", "   ")

	if got.Code != gonsole.ExitMisused || !strings.HasPrefix(got.Stderr, "myapp: -as wants the address of an account\n") {
		t.Errorf("code %d, stderr %q, want 2 and the misuse", got.Code, got.Stderr)
	}
}

func TestAuthorizeStopsAMalformedRecordTimeoutBeforeTheCommandRuns(t *testing.T) {
	t.Parallel()

	address := recorded(t)
	settings := map[string]string{"MYAPP_COMMAND_RECORD_TIMEOUT": "soon"}
	p := authorizing(address, checked(), settings, command("report:purge"))

	got := testkit.Run(t, p, "", "report:purge", "-as", "admin@example.com")

	want := `myapp: MYAPP_COMMAND_RECORD_TIMEOUT: must be a duration like 30s, got "soon"` + "\n"
	if got.Code != gonsole.ExitFailed || got.Stdout != "" || got.Stderr != want {
		t.Errorf("code %d, stdout %q, stderr %q, want 1, nothing run and %q", got.Code, got.Stdout, got.Stderr, want)
	}
}

func TestAuthorizeNamesTheMigrateCommandWhenTheRecordsAreMissing(t *testing.T) {
	t.Parallel()

	address := seeded(t)
	p := authorizing(address, checked(), nil, command("report:purge"))

	got := testkit.Run(t, p, "", "report:purge", "-as", "admin@example.com")

	want := "myapp: the command records are missing, run migrate first\n"
	if got.Code != gonsole.ExitFailed || got.Stdout != "" || got.Stderr != want {
		t.Errorf("code %d, stdout %q, stderr %q, want 1, nothing run and %q", got.Code, got.Stdout, got.Stderr, want)
	}
}

func TestAuthorizePassesARolesFailureThrough(t *testing.T) {
	t.Parallel()

	address := recorded(t)
	cfg := checked()
	cfg.Roles = func(context.Context, gonsole.Call) (auth.Roles, error) {
		return auth.Roles{}, errors.New("roles unread")
	}
	p := authorizing(address, cfg, nil, command("report:purge"))

	got := testkit.Run(t, p, "", "report:purge", "-as", "admin@example.com")

	if got.Code != gonsole.ExitFailed || got.Stderr != "myapp: roles unread\n" {
		t.Errorf("code %d, stderr %q, want 1 and the roles failure", got.Code, got.Stderr)
	}
}

func TestAuthorizePassesALookupFailureThrough(t *testing.T) {
	t.Parallel()

	address := empty(t)
	if err := auth.RecordMigration().Run(t.Context(), address); err != nil {
		t.Fatalf("applying the records schema: %v", err)
	}
	p := authorizing(address, checked(), nil, command("report:purge"))

	got := testkit.Run(t, p, "", "report:purge", "-as", "admin@example.com")

	if got.Code != gonsole.ExitFailed || got.Stdout != "" || !strings.Contains(got.Stderr, "auth.users") {
		t.Errorf("code %d, stdout %q, stderr %q, want 1, nothing run and the missing accounts",
			got.Code, got.Stdout, got.Stderr)
	}
}

func TestAuthorizeNamesADatabaseItCannotReach(t *testing.T) {
	t.Parallel()

	p := authorizing("postgres://postgres@127.0.0.1:1/none?connect_timeout=1", checked(), nil, command("report:purge"))

	got := testkit.Run(t, p, "", "report:purge", "-as", "admin@example.com")

	if got.Code != gonsole.ExitFailed || got.Stdout != "" ||
		!strings.HasPrefix(got.Stderr, "myapp: failed to connect to ") {
		t.Errorf("code %d, stdout %q, stderr %q, want 1 and nothing run", got.Code, got.Stdout, got.Stderr)
	}
}
