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

// rolePage is the help page of account:role in a program whose account writes name no capability.
const rolePage = `set one account's role

Usage:
  myapp account:role [flags] <email> <role>

Flags:
  -yes
    	apply the change, a dry run without it
`

func TestSetRoleSetsTheRoleOnlyWithYes(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name   string
		args   []string
		stdout string
		stderr string
		role   string
	}{
		{"a dry run", []string{"account:role", "editor@example.com", "author"},
			"would set editor@example.com to author\n", dryRunNotice, "editor"},
		{"an applied change", []string{"account:role", "editor@example.com", "author", "-yes"},
			"set editor@example.com to author\n", "", "author"},
		{"an address typed in capitals between spaces", []string{"account:role", " Editor@Example.com ", "author", "-yes"},
			"set editor@example.com to author\n", "", "author"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			address := seeded(t)
			var reads rolesRead

			got := testkit.Run(t, program(address, auth.SetRole(config(&reads, ""))), "", tc.args...)

			if want := (testkit.Result{Code: gonsole.ExitDone, Stdout: tc.stdout, Stderr: tc.stderr}); got != want {
				t.Fatalf("Run() = %+v, want %+v", got, want)
			}
			if held := account(t, storeAt(t, address), "editor@example.com"); held.Role != tc.role {
				t.Errorf("role = %q, want %q", held.Role, tc.role)
			}
		})
	}
}

func TestSetRoleRefusesALineItCannotRun(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name   string
		args   []string
		code   int
		stdout string
		stderr string
		reads  int
	}{
		{"an unknown role", []string{"account:role", "editor@example.com", "owner"}, gonsole.ExitMisused, "",
			"myapp: unknown role \"owner\", want admin, editor or author\n\n" + rolePage, 1},
		{"a role in capitals", []string{"account:role", "editor@example.com", "Author"}, gonsole.ExitMisused, "",
			"myapp: unknown role \"Author\", want admin, editor or author\n\n" + rolePage, 1},
		{"no role", []string{"account:role", "editor@example.com"}, gonsole.ExitMisused, "",
			"myapp: account:role wants <role>\n\n" + rolePage, 0},
		{"an unknown address", []string{"account:role", "nobody@example.com", "author", "-yes"}, gonsole.ExitFailed,
			"", "myapp: gouncer: user not found\n", 1},
		{"an unknown address in a dry run", []string{"account:role", "nobody@example.com", "author"},
			gonsole.ExitFailed, "", "myapp: gouncer: user not found\n", 1},
		{"the last privileged account", []string{"account:role", "admin@example.com", "editor", "-yes"},
			gonsole.ExitFailed, "", "myapp: admin@example.com is the last enabled privileged account\n", 1},
		{"a request for help", []string{"account:role", "-h"}, gonsole.ExitDone, rolePage, "", 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			address := seeded(t)
			var reads rolesRead

			got := testkit.Run(t, program(address, auth.SetRole(config(&reads, ""))), "", tc.args...)

			if want := (testkit.Result{Code: tc.code, Stdout: tc.stdout, Stderr: tc.stderr}); got != want {
				t.Errorf("Run() = %+v, want %+v", got, want)
			}
			if reads.count != tc.reads {
				t.Errorf("roles read %d times, want %d", reads.count, tc.reads)
			}
			if held := account(t, storeAt(t, address), "admin@example.com"); held.Role != "admin" {
				t.Errorf("admin role = %q, want admin kept", held.Role)
			}
		})
	}
}

func TestSetRoleDemotesAPrivilegedAccountThatIsNotTheLast(t *testing.T) {
	t.Parallel()

	address := seeded(t)
	var reads rolesRead
	p := program(address, auth.SetRole(config(&reads, "")))
	promoted := testkit.Run(t, p, "", "account:role", "editor@example.com", "admin", "-yes")

	got := testkit.Run(t, p, "", "account:role", "admin@example.com", "author", "-yes")

	if promoted.Code != gonsole.ExitDone || got.Code != gonsole.ExitDone {
		t.Fatalf("Run() = %+v, then %+v, want both applied", promoted, got)
	}
	if held := account(t, storeAt(t, address), "admin@example.com"); held.Role != "author" {
		t.Errorf("role = %q, want author", held.Role)
	}
}

func TestSetRoleKeepsTheLastAccountUnderAPrivilegedRoleAPluginDeclares(t *testing.T) {
	t.Parallel()

	address := seeded(t)
	store := storeAt(t, address)
	if err := store.SetUserRole(t.Context(), account(t, store, "author@example.com").ID, "superadmin",
		nil); err != nil {
		t.Fatalf("SetUserRole() = %v", err)
	}
	cfg := auth.Config{Roles: func(context.Context, gonsole.Call) (auth.Roles, error) {
		return auth.Roles{
			Known:      []string{"admin", "editor", "author", "superadmin"},
			Privileged: gouncer.Roles{"admin", "superadmin"},
		}, nil
	}}

	got := testkit.Run(t, program(address, auth.SetRole(cfg)), "", "account:role", "admin@example.com", "author",
		"-yes")
	last := testkit.Run(t, program(address, auth.SetRole(cfg)), "", "account:role", "author@example.com", "editor",
		"-yes")

	if got.Code != gonsole.ExitDone {
		t.Errorf("demoting admin beside a superadmin = %+v, want it applied", got)
	}
	if want := "myapp: author@example.com is the last enabled privileged account\n"; last.Code != gonsole.ExitFailed ||
		last.Stderr != want {
		t.Errorf("demoting the last superadmin = %+v, want exit 1 and %q", last, want)
	}
}

func TestSetRoleFailsWhenTheRolesCannotBeRead(t *testing.T) {
	t.Parallel()

	cfg := auth.Config{Roles: func(context.Context, gonsole.Call) (auth.Roles, error) {
		return auth.Roles{}, errors.New("the plugin table is locked")
	}}

	got := testkit.Run(t, program(seeded(t), auth.SetRole(cfg)), "", "account:role", "editor@example.com", "author")

	if want := (testkit.Result{Code: gonsole.ExitFailed, Stderr: "myapp: the plugin table is locked\n"}); got != want {
		t.Errorf("Run() = %+v, want %+v", got, want)
	}
}

func TestSetRoleAsksForTheActingAccountTheProgramNames(t *testing.T) {
	t.Parallel()

	var reads rolesRead

	got := testkit.Run(t, program(seeded(t), auth.SetRole(config(&reads, "manage_accounts"))), "", "account:role",
		"editor@example.com", "author")

	if got.Code != gonsole.ExitMisused || !strings.HasPrefix(got.Stderr, "myapp: account:role wants -as <email>\n") {
		t.Errorf("Run() = %+v, want exit 2 asking for -as", got)
	}
}
