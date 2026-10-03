// SPDX-License-Identifier: Apache-2.0

package auth_test

import (
	"context"
	"fmt"
	"io"
	"slices"
	"strings"
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

// standingOf returns the role and the standing of the account at email in the database at address.
func standingOf(t *testing.T, address, email string) string {
	t.Helper()
	held := account(t, storeAt(t, address), email)
	return fmt.Sprintf("role %q, disabled %t", held.Role, held.Disabled)
}

// refuses runs line on p and fails the test unless the run exits 1 with refusal alone on stderr.
func refuses(t *testing.T, p gonsole.Program, refusal string, line ...string) {
	t.Helper()
	got := testkit.Run(t, p, "", line...)
	if want := (testkit.Result{Code: gonsole.ExitFailed, Stderr: refusal}); got != want {
		t.Errorf("%q: Run() = %+v, want %+v", line, got, want)
	}
}

// reachingLines are one dry run of each account command that checks the reach of the acting account.
var reachingLines = [][]string{
	{"account:role", "editor@example.com", "author"},
	{"account:grant-role", "-role", "author"},
	{"account:disable", "editor@example.com"},
	{"account:enable", "editor@example.com"},
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

func TestAccountCommandsRefuseAChangeBeyondTheReachOfTheActingAccount(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		setup    []string
		args     []string
		target   string
		migrated string
		want     string
	}{
		{"a role carrying more", nil, []string{"account:role", "author@example.com", "editor"}, "author@example.com",
			"", "the role editor carries change_others_work, which the account manager@example.com lacks"},
		{"a role carrying more for the acting account itself", nil,
			[]string{"account:role", "manager@example.com", "admin"}, "manager@example.com", "",
			"the role admin carries change_others_work, which the account manager@example.com lacks"},
		{"an account under a role carrying more", nil, []string{"account:role", "editor@example.com", "author"},
			"editor@example.com", "",
			"the role editor of editor@example.com carries change_others_work, which the account manager@example.com lacks"},
		{"a role and an account both carrying more", nil, []string{"account:role", "editor@example.com", "admin"},
			"editor@example.com", "", "the role admin carries change_others_work, which the account manager@example.com lacks"},
		{"a disable of an account under a role carrying more", nil, []string{"account:disable", "admin@example.com"},
			"admin@example.com", "",
			"the role admin of admin@example.com carries change_others_work, which the account manager@example.com lacks"},
		{"an enable of an account under a role carrying more", []string{disabling("editor@example.com")},
			[]string{"account:enable", "editor@example.com"}, "editor@example.com", "",
			"the role editor of editor@example.com carries change_others_work, which the account manager@example.com lacks"},
		{"a grant of a role carrying more", []string{holding("author@example.com", "")},
			[]string{"account:grant-role", "-role", "editor"}, "author@example.com", migratedLine,
			"the role editor carries change_others_work, which the account manager@example.com lacks"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			address := managed(t)
			run(t, address, tt.setup...)
			before := standingOf(t, address, tt.target)
			line := slices.Concat(tt.args, []string{"-as", "manager@example.com"})

			refuses(t, overseen(address), "myapp: "+tt.want+"\n", line...)
			refuses(t, overseen(address), tt.migrated+"myapp: "+tt.want+"\n", append(line, "-yes")...)

			if held := records(t, address); len(held) != 0 {
				t.Errorf("records = %v, want none", held)
			}
			if after := standingOf(t, address, tt.target); after != before {
				t.Errorf("%s holds %s, want %s kept", tt.target, after, before)
			}
		})
	}
}

func TestSetRoleAnswersARoleBeyondReachForAnAddressNoAccountHolds(t *testing.T) {
	t.Parallel()

	want := "myapp: the role admin carries change_others_work, which the account manager@example.com lacks\n"
	refuses(t, overseen(managed(t)), want, "account:role", "nobody@example.com", "admin", "-as", "manager@example.com")
}

func TestAccountCommandsRefuseARoleNoActingRoleCovers(t *testing.T) {
	t.Parallel()

	cfg := guarded()
	cfg.Roles = func(context.Context, gonsole.Call) (auth.Roles, error) {
		return auth.Roles{
			Known:        vocabulary.Known,
			Privileged:   vocabulary.Privileged,
			Capabilities: map[string][]string{"admin": {"manage_users"}, "editor": {"change_others_work"}},
		}, nil
	}
	address := recorded(t)
	p := authorizing(address, cfg, nil, auth.Commands(cfg)...)

	refuses(t, p, "myapp: the role editor carries change_others_work, which the account admin@example.com lacks\n",
		"account:grant-role", "-role", "editor", "-as", "admin@example.com")
	refuses(t, p, "myapp: the role editor of editor@example.com carries change_others_work, "+
		"which the account admin@example.com lacks\n", "account:disable", "editor@example.com", "-as", "admin@example.com")
}

func TestAccountCommandsNameAnActingAddressNoAccountHolds(t *testing.T) {
	t.Parallel()

	for _, line := range reachingLines {
		t.Run(line[0], func(t *testing.T) {
			t.Parallel()

			refuses(t, trusting(recorded(t)), "myapp: no account answers to nobody@example.com\n",
				slices.Concat(line, []string{"-as", "nobody@example.com"})...)
		})
	}
}

func TestAccountCommandsTreatABlankActingAddressAsAMisuse(t *testing.T) {
	t.Parallel()

	p := trusting("postgres://postgres@127.0.0.1:1/none?connect_timeout=1")
	for _, line := range reachingLines {
		t.Run(line[0], func(t *testing.T) {
			t.Parallel()

			got := testkit.Run(t, p, "", slices.Concat(line, []string{"-as", "   "})...)

			if want := "myapp: -as wants the address of an account\n"; got.Code != gonsole.ExitMisused ||
				!strings.HasPrefix(got.Stderr, want) {
				t.Errorf("Run() = %+v, want exit 2 opening with %q", got, want)
			}
		})
	}
}

func TestAccountCommandsRefuseAnActingAccountWithoutARoleAnythingThatCarriesACapability(t *testing.T) {
	t.Parallel()

	address := managed(t)
	run(t, address, holding("manager@example.com", ""))
	p := trusting(address)

	refuses(t, p, "myapp: the role editor carries change_others_work, which the account manager@example.com lacks\n",
		"account:grant-role", "-role", "editor", "-as", "manager@example.com")
	refuses(t, p, "myapp: the role admin of admin@example.com carries manage_users, "+
		"which the account manager@example.com lacks\n", "account:disable", "admin@example.com", "-as", "manager@example.com")
}

func TestDisableRefusesTheActingAccountDisablingItself(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		setup []string
		args  []string
	}{
		{"a dry run beside a second admin", []string{holding("editor@example.com", "admin")},
			[]string{"account:disable", "admin@example.com"}},
		{"an applied run beside a second admin", []string{holding("editor@example.com", "admin")},
			[]string{"account:disable", "admin@example.com", "-yes"}},
		{"an address typed in capitals between spaces", []string{holding("editor@example.com", "admin")},
			[]string{"account:disable", " Admin@Example.COM ", "-yes"}},
		{"a dry run of the single admin", nil, []string{"account:disable", "admin@example.com"}},
		{"an applied run of the single admin", nil, []string{"account:disable", "admin@example.com", "-yes"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			address := recorded(t)
			run(t, address, tt.setup...)

			refuses(t, overseen(address), "myapp: the account admin@example.com cannot disable itself\n",
				slices.Concat(tt.args, []string{"-as", "admin@example.com"})...)

			if held := records(t, address); len(held) != 0 {
				t.Errorf("records = %v, want none", held)
			}
			if after := standingOf(t, address, "admin@example.com"); after != `role "admin", disabled false` {
				t.Errorf("admin@example.com holds %s, want it kept enabled", after)
			}
		})
	}
}
