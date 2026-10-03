// SPDX-License-Identifier: Apache-2.0

package auth_test

import (
	"context"
	"fmt"
	"io"
	"slices"
	"testing"

	"github.com/gopherium/framework/gonsole"
	"github.com/gopherium/framework/gonsole/auth"
	"github.com/gopherium/framework/gonsole/testkit"
)

// tiered is capable plus a manager carrying only manage_users and an author carrying an empty list.
var tiered = auth.Roles{
	Known:      []string{"admin", "editor", "author", "manager"},
	Privileged: vocabulary.Privileged,
	Capabilities: map[string][]string{
		"admin":   {"manage_users", "change_others_work"},
		"editor":  {"change_others_work"},
		"manager": {"manage_users"},
		"author":  {},
	},
}

// reaching is a config over tiered naming manage_users, recording within a second.
func reaching() auth.Config {
	cfg := guarded()
	cfg.Roles = func(context.Context, gonsole.Call) (auth.Roles, error) { return tiered, nil }
	return cfg
}

// managed returns the address of a recorded database that also holds manager@example.com under manager.
func managed(t *testing.T) string {
	t.Helper()
	address := recorded(t)
	manager := auth.Account{Email: "manager@example.com", Name: "Maria Perez", Password: demoPassword, Role: "manager"}
	if err := auth.EnsureAccounts(t.Context(), storeAt(t, address), []auth.Account{manager}, io.Discard); err != nil {
		t.Fatalf("adding the manager: %v", err)
	}
	return address
}

// overseen returns a program over the database at address whose account commands auth.Authorize checks.
func overseen(address string) gonsole.Program {
	return authorizing(address, reaching(), nil, auth.Commands(reaching())...)
}

// trusting returns a program over the database at address whose own check lets every acting account through.
func trusting(address string) gonsole.Program {
	return recording(address, reaching(), nil, auth.Commands(reaching())...)
}

// holding returns the statement that puts the account at email under role.
func holding(email, role string) string {
	return fmt.Sprintf("UPDATE auth.users SET role = '%s' WHERE email = '%s'", role, email)
}

// disabling returns the statement that disables the account at email.
func disabling(email string) string {
	return fmt.Sprintf("UPDATE auth.users SET disabled = true WHERE email = '%s'", email)
}

func TestAccountCommandsApplyAChangeWithinTheReachOfTheActingAccount(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		setup  []string
		over   func(address string) gonsole.Program
		actor  string
		args   []string
		stdout string
	}{
		{"the manager sets the author to manager", nil, overseen, "manager@example.com",
			[]string{"account:role", "author@example.com", "manager"}, "set author@example.com to manager\n"},
		{"the manager disables the author", nil, overseen, "manager@example.com",
			[]string{"account:disable", "author@example.com"}, "disabled author@example.com\n"},
		{"the manager enables a disabled author", []string{disabling("author@example.com")}, overseen,
			"manager@example.com", []string{"account:enable", "author@example.com"}, "enabled author@example.com\n"},
		{"the manager grants author", []string{holding("editor@example.com", "")}, overseen, "manager@example.com",
			[]string{"account:grant-role", "-role", "author"}, "granted author to 1 account\n"},
		{"the manager changes an account with no role", []string{holding("editor@example.com", "")}, overseen,
			"manager@example.com", []string{"account:role", "editor@example.com", "author"},
			"set editor@example.com to author\n"},
		{"the manager changes an account whose stored role the roles leave out",
			[]string{holding("editor@example.com", "guest")}, overseen, "manager@example.com",
			[]string{"account:disable", "editor@example.com"}, "disabled editor@example.com\n"},
		{"the manager disables a second manager", []string{holding("author@example.com", "manager")}, overseen,
			"manager@example.com", []string{"account:disable", "author@example.com"}, "disabled author@example.com\n"},
		{"the admin sets the editor to admin", nil, overseen, "admin@example.com",
			[]string{"account:role", "editor@example.com", "admin"}, "set editor@example.com to admin\n"},
		{"the admin demotes a second admin", []string{holding("editor@example.com", "admin")}, overseen,
			"admin@example.com", []string{"account:role", "editor@example.com", "author"},
			"set editor@example.com to author\n"},
		{"an actor with no role gives itself author under the program's own check",
			[]string{holding("manager@example.com", "")}, trusting, "manager@example.com",
			[]string{"account:grant-role", "-role", "author"}, "granted author to 1 account\n"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			address := managed(t)
			run(t, address, tt.setup...)

			got := testkit.Run(t, tt.over(address), "", slices.Concat(tt.args, []string{"-yes", "-as", tt.actor})...)

			if got.Code != gonsole.ExitDone || got.Stdout != tt.stdout {
				t.Errorf("code %d, stdout %q, stderr %q, want 0 and %q", got.Code, got.Stdout, got.Stderr, tt.stdout)
			}
			if held := records(t, address); len(held) != 1 || held[0].command != tt.args[0] {
				t.Errorf("records = %v, want one record of %s", held, tt.args[0])
			}
		})
	}
}

func TestEnableLetsTheActingAccountNameItself(t *testing.T) {
	t.Parallel()

	got := testkit.Run(t, overseen(managed(t)), "", "account:enable", "manager@example.com", "-yes", "-as",
		"manager@example.com")

	if got.Code != gonsole.ExitDone || got.Stdout != "enabled manager@example.com\n" {
		t.Errorf("code %d, stdout %q, stderr %q, want 0 and the account enabled", got.Code, got.Stdout, got.Stderr)
	}
}
