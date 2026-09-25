// SPDX-License-Identifier: Apache-2.0

package auth_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/gopherium/gouncer"

	"github.com/gopherium/framework/gonsole"
	"github.com/gopherium/framework/gonsole/auth"
	"github.com/gopherium/framework/gonsole/testkit"
)

// grantPage is the help page of account:grant-role in a program whose account writes name no capability.
const grantPage = `give a role to every account holding none

Usage:
  myapp account:grant-role [flags]

Flags:
  -role role
    	role to give every account holding none
  -yes
    	apply the change, a dry run without it
`

// dryRunNotice is the line a dry run of myapp ends with on stderr.
const dryRunNotice = "myapp: dry run, nothing changed, pass -yes to apply\n"

// withRoleless returns the address of a seeded database that also holds count accounts under no role.
func withRoleless(t *testing.T, count int) string {
	t.Helper()
	address := seeded(t)
	store := storeAt(t, address)
	for n := range count {
		roleless, err := gouncer.NewUser(string(rune('a'+n))+"-roleless@example.com", "Maria Perez", demoPassword)
		if err != nil {
			t.Fatalf("NewUser() = %v", err)
		}
		if err := store.CreateUser(t.Context(), roleless); err != nil {
			t.Fatalf("CreateUser() = %v", err)
		}
	}
	return address
}

func TestGrantRoleGivesTheRoleOnlyWithYes(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name     string
		roleless int
		args     []string
		stdout   string
		stderr   string
		role     string
	}{
		{"a dry run", 2, []string{"account:grant-role", "-role", "editor"}, "would grant editor to 2 accounts\n",
			dryRunNotice, ""},
		{"an applied grant", 2, []string{"account:grant-role", "-role", "editor", "-yes"},
			"granted editor to 2 accounts\n", migratedLine, "editor"},
		{"a dry run over one account", 1, []string{"account:grant-role", "-role", "author"},
			"would grant author to 1 account\n", dryRunNotice, ""},
		{"an applied grant over one account", 1, []string{"account:grant-role", "-role", "author", "-yes"},
			"granted author to 1 account\n", migratedLine, "author"},
		{"a grant with no account to take it", 0, []string{"account:grant-role", "-role", "editor", "-yes"},
			"granted editor to 0 accounts\n", migratedLine, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			address := withRoleless(t, tc.roleless)
			var reads rolesRead

			got := testkit.Run(t, program(address, auth.GrantRole(config(&reads, ""))), "", tc.args...)

			want := testkit.Result{Code: gonsole.ExitDone, Stdout: tc.stdout, Stderr: tc.stderr}
			if got != want {
				t.Fatalf("Run() = %+v, want %+v", got, want)
			}
			if tc.roleless > 0 {
				if held := account(t, storeAt(t, address), "a-roleless@example.com"); held.Role != tc.role {
					t.Errorf("role = %q, want %q", held.Role, tc.role)
				}
			}
			if held := account(t, storeAt(t, address), "admin@example.com"); held.Role != "admin" {
				t.Errorf("an account holding a role now holds %q, want admin kept", held.Role)
			}
		})
	}
}

func TestGrantRoleRefusesALineItCannotRun(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name   string
		args   []string
		stderr string
		reads  int
	}{
		{"no role", []string{"account:grant-role"}, "myapp: account:grant-role wants -role <role>\n\n" + grantPage, 0},
		{"an unknown role", []string{"account:grant-role", "-role", "owner"},
			"myapp: unknown role \"owner\", want admin, editor or author\n\n" + grantPage, 1},
		{"an unknown role with yes", []string{"account:grant-role", "-role", "owner", "-yes"},
			migratedLine + "myapp: unknown role \"owner\", want admin, editor or author\n\n" + grantPage, 1},
		{"a padded role", []string{"account:grant-role", "-role", " editor"},
			"myapp: unknown role \" editor\", want admin, editor or author\n\n" + grantPage, 1},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			address := withRoleless(t, 1)
			var reads rolesRead

			got := testkit.Run(t, program(address, auth.GrantRole(config(&reads, ""))), "", tc.args...)

			if want := (testkit.Result{Code: gonsole.ExitMisused, Stderr: tc.stderr}); got != want {
				t.Errorf("Run() = %+v, want %+v", got, want)
			}
			if reads.count != tc.reads {
				t.Errorf("roles read %d times, want %d", reads.count, tc.reads)
			}
			if held := account(t, storeAt(t, address), "a-roleless@example.com"); held.Role != "" {
				t.Errorf("role = %q, want none granted", held.Role)
			}
		})
	}
}

func TestGrantRoleDryRunFailsOverADatabaseNeverMigrated(t *testing.T) {
	t.Parallel()

	var reads rolesRead

	got := testkit.Run(t, program(empty(t), auth.GrantRole(config(&reads, ""))), "", "account:grant-role", "-role",
		"editor")

	if got.Code != gonsole.ExitFailed || !strings.HasPrefix(got.Stderr, "myapp: postgres: list users: ") {
		t.Errorf("Run() = %+v, want exit 1 and the failed listing", got)
	}
}

func TestGrantRoleCountsADisabledAccountHoldingNoRole(t *testing.T) {
	t.Parallel()

	address := withRoleless(t, 1)
	store := storeAt(t, address)
	if err := store.SetUserDisabled(t.Context(), account(t, store, "a-roleless@example.com").ID, true); err != nil {
		t.Fatalf("SetUserDisabled() = %v", err)
	}
	var reads rolesRead
	p := program(address, auth.GrantRole(config(&reads, "")))

	dryRun := testkit.Run(t, p, "", "account:grant-role", "-role", "editor")
	applied := testkit.Run(t, p, "", "account:grant-role", "-role", "editor", "-yes")

	if dryRun.Stdout != "would grant editor to 1 account\n" || applied.Stdout != "granted editor to 1 account\n" {
		t.Errorf("dry run %q then %q, want both to count the disabled account", dryRun.Stdout, applied.Stdout)
	}
}

func TestGrantRoleFailsWhenTheRolesCannotBeRead(t *testing.T) {
	t.Parallel()

	cfg := auth.Config{Roles: func(context.Context, gonsole.Call) (auth.Roles, error) {
		return auth.Roles{}, errors.New("the plugin table is locked")
	}}

	got := testkit.Run(t, program(withRoleless(t, 1), auth.GrantRole(cfg)), "", "account:grant-role", "-role",
		"editor")

	if want := (testkit.Result{Code: gonsole.ExitFailed, Stderr: "myapp: the plugin table is locked\n"}); got != want {
		t.Errorf("Run() = %+v, want %+v", got, want)
	}
}

func TestGrantRoleReportsAWriteTheStoreRefuses(t *testing.T) {
	t.Parallel()

	address := withRoleless(t, 1)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	cfg := auth.Config{Roles: func(context.Context, gonsole.Call) (auth.Roles, error) {
		cancel()
		return vocabulary, nil
	}}
	var stdout, stderr strings.Builder

	code := program(address, auth.GrantRole(cfg)).Run(ctx, []string{"account:grant-role", "-role", "editor", "-yes"},
		strings.NewReader(""), &stdout, &stderr)

	if code != gonsole.ExitFailed || stdout.String() != "" ||
		!strings.Contains(stderr.String(), "myapp: postgres: grant role: ") {
		t.Errorf("Run() = %d, %q, %q, want exit 1, nothing granted and the refused write", code, stdout.String(),
			stderr.String())
	}
	if held := account(t, storeAt(t, address), "a-roleless@example.com"); held.Role != "" {
		t.Errorf("role = %q, want none granted", held.Role)
	}
}

func TestGrantRoleAsksTheProgramForItsCapability(t *testing.T) {
	t.Parallel()

	var reads rolesRead
	var asked []string
	p := program(withRoleless(t, 1), auth.GrantRole(config(&reads, "manage_accounts")))
	p.Authorize = func(_ context.Context, call gonsole.Call, capability string) error {
		asked = append(asked, call.Actor+" "+capability)
		return nil
	}

	missingActor := testkit.Run(t, p, "", "account:grant-role", "-role", "editor")
	got := testkit.Run(t, p, "", "account:grant-role", "-role", "editor", "-as", "maria.perez@example.com")

	if missingActor.Code != gonsole.ExitMisused {
		t.Errorf("Run() without -as = %+v, want exit 2", missingActor)
	}
	if got.Code != gonsole.ExitDone || len(asked) != 1 || asked[0] != "maria.perez@example.com manage_accounts" {
		t.Errorf("Run() = %+v, Authorize asked %q, want exit 0 and one manage_accounts check", got, asked)
	}
}
