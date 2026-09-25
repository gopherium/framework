// SPDX-License-Identifier: Apache-2.0

package auth_test

import (
	"context"
	"errors"
	"slices"
	"testing"

	"github.com/gopherium/framework/gonsole"
	"github.com/gopherium/framework/gonsole/auth"
	"github.com/gopherium/framework/gonsole/testkit"
)

// createPage is the help page of account:create-admin.
const createPage = `create an account under a role

Usage:
  myapp account:create-admin [flags]

Flags:
  -email address
    	address of the new account
  -name name
    	display name of the new account
  -role role
    	role the new account starts under
`

// migratedLine is the line the account schema step writes before a command that asks for it.
const migratedLine = "migrated accounts\n"

// creating returns the arguments that create maria.perez@example.com under role.
func creating(role string) []string {
	return []string{"account:create-admin", "-email", "maria.perez@example.com", "-name", "Maria Perez", "-role", role}
}

func TestCreateAdminCreatesAnAccountAfterTheSchema(t *testing.T) {
	t.Parallel()

	address := empty(t)
	var reads rolesRead

	got := testkit.Run(t, program(address, auth.CreateAdmin(config(&reads, ""))), demoPassword+"\n", creating("admin")...)

	want := testkit.Result{Code: gonsole.ExitDone, Stdout: "Password: created user maria.perez@example.com\n",
		Stderr: migratedLine}
	if got != want {
		t.Fatalf("Run() = %+v, want %+v", got, want)
	}
	if held := account(t, storeAt(t, address), "maria.perez@example.com"); held.Role != "admin" ||
		held.Name != "Maria Perez" {
		t.Errorf("account = %+v, want Maria Perez under admin", held)
	}
}

func TestCreateAdminRefusesALineItCannotRun(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name   string
		args   []string
		stdin  string
		code   int
		stdout string
		stderr string
		reads  int
	}{
		{"no address", []string{"account:create-admin", "-name", "Maria Perez", "-role", "admin"}, "",
			gonsole.ExitMisused, "", migratedLine + "myapp: account:create-admin wants -email <address>\n\n" + createPage, 0},
		{"no name", []string{"account:create-admin", "-email", "maria.perez@example.com", "-role", "admin"}, "",
			gonsole.ExitMisused, "", migratedLine + "myapp: account:create-admin wants -name <name>\n\n" + createPage, 0},
		{"a blank name", append(creating("admin")[:3], "-name", "  ", "-role", "admin"), "", gonsole.ExitMisused, "",
			migratedLine + "myapp: account:create-admin wants -name <name>\n\n" + createPage, 0},
		{"no role", creating("admin")[:5], "", gonsole.ExitMisused, "",
			migratedLine + "myapp: account:create-admin wants -role <role>\n\n" + createPage, 0},
		{"an unknown role", creating("owner"), "", gonsole.ExitMisused, "",
			migratedLine + "myapp: unknown role \"owner\", want admin, editor or author\n\n" + createPage, 1},
		{"a padded role", creating(" admin"), "", gonsole.ExitMisused, "",
			migratedLine + "myapp: unknown role \" admin\", want admin, editor or author\n\n" + createPage, 1},
		{"a taken address", []string{"account:create-admin", "-email", "admin@example.com", "-name", "Maria Perez",
			"-role", "admin"}, demoPassword + "\n", gonsole.ExitFailed, "Password: ",
			migratedLine + "myapp: gouncer: email already taken\n", 1},
		{"a weak password", creating("admin"), "short\n", gonsole.ExitFailed, "Password: ",
			migratedLine + "myapp: gouncer: password shorter than 12 characters\n", 1},
		{"a request for help", []string{"account:create-admin", "-h"}, "", gonsole.ExitDone, createPage, "", 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			var reads rolesRead

			got := testkit.Run(t, program(seeded(t), auth.CreateAdmin(config(&reads, ""))), tc.stdin, tc.args...)

			want := testkit.Result{Code: tc.code, Stdout: tc.stdout, Stderr: tc.stderr}
			if got != want {
				t.Errorf("Run() = %+v, want %+v", got, want)
			}
			if reads.count != tc.reads {
				t.Errorf("roles read %d times, want %d", reads.count, tc.reads)
			}
		})
	}
}

func TestCreateAdminTakesARoleThePluginsDeclare(t *testing.T) {
	t.Parallel()

	address := migrated(t)
	cfg := auth.Config{Roles: func(context.Context, gonsole.Call) (auth.Roles, error) {
		return auth.Roles{Known: append(slices.Clone(vocabulary.Known), "superadmin"), Privileged: vocabulary.Privileged}, nil
	}}

	got := testkit.Run(t, program(address, auth.CreateAdmin(cfg)), demoPassword+"\n", creating("superadmin")...)

	if got.Code != gonsole.ExitDone {
		t.Fatalf("Run() = %+v, want a created account", got)
	}
	if held := account(t, storeAt(t, address), "maria.perez@example.com"); held.Role != "superadmin" {
		t.Errorf("role = %q, want superadmin", held.Role)
	}
}

func TestCreateAdminFailsWhenTheRolesCannotBeRead(t *testing.T) {
	t.Parallel()

	cfg := auth.Config{Roles: func(context.Context, gonsole.Call) (auth.Roles, error) {
		return auth.Roles{}, errors.New("the plugin table is locked")
	}}

	got := testkit.Run(t, program(migrated(t), auth.CreateAdmin(cfg)), demoPassword+"\n", creating("admin")...)

	want := testkit.Result{Code: gonsole.ExitFailed, Stderr: migratedLine + "myapp: the plugin table is locked\n"}
	if got != want {
		t.Errorf("Run() = %+v, want %+v", got, want)
	}
}

func TestCreateAdminNamesNoRoleWhenTheProgramDeclaresNone(t *testing.T) {
	t.Parallel()

	cfg := auth.Config{Roles: func(context.Context, gonsole.Call) (auth.Roles, error) { return auth.Roles{}, nil }}

	got := testkit.Run(t, program(migrated(t), auth.CreateAdmin(cfg)), "", creating("admin")...)

	want := migratedLine + "myapp: unknown role \"admin\", want a role the program declares\n\n" + createPage
	if got.Code != gonsole.ExitMisused || got.Stderr != want {
		t.Errorf("Run() = %+v, want exit 2 and %q", got, want)
	}
}

func TestCreateAdminNamesTheOneRoleTheProgramDeclares(t *testing.T) {
	t.Parallel()

	cfg := auth.Config{Roles: func(context.Context, gonsole.Call) (auth.Roles, error) {
		return auth.Roles{Known: []string{"admin"}}, nil
	}}

	got := testkit.Run(t, program(migrated(t), auth.CreateAdmin(cfg)), "", creating("owner")...)

	want := migratedLine + "myapp: unknown role \"owner\", want admin\n\n" + createPage
	if got.Code != gonsole.ExitMisused || got.Stderr != want {
		t.Errorf("Run() = %+v, want exit 2 and %q", got, want)
	}
}
