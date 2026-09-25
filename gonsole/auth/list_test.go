// SPDX-License-Identifier: Apache-2.0

package auth_test

import (
	"strings"
	"testing"

	"github.com/gopherium/gouncer"

	"github.com/gopherium/framework/gonsole"
	"github.com/gopherium/framework/gonsole/auth"
	"github.com/gopherium/framework/gonsole/testkit"
)

// listed returns the address of a seeded database whose editor is disabled and that holds one account under no role.
func listed(t *testing.T) string {
	t.Helper()
	address := seeded(t)
	store := storeAt(t, address)
	roleless, err := gouncer.NewUser("roleless@example.com", "account without a role", demoPassword)
	if err != nil {
		t.Fatalf("NewUser() = %v", err)
	}
	if err := store.CreateUser(t.Context(), roleless); err != nil {
		t.Fatalf("CreateUser() = %v", err)
	}
	if err := store.SetUserDisabled(t.Context(), account(t, store, "editor@example.com").ID, true); err != nil {
		t.Fatalf("SetUserDisabled() = %v", err)
	}
	return address
}

func TestListPrintsEveryAccountWithItsRoleAndStanding(t *testing.T) {
	t.Parallel()

	var reads rolesRead

	got := testkit.Run(t, program(listed(t), auth.List(config(&reads, "manage_accounts"))), "", "account:list")

	want := `roleless@example.com  -       enabled
admin@example.com     admin   enabled
author@example.com    author  enabled
editor@example.com    editor  disabled
`
	if got != (testkit.Result{Code: gonsole.ExitDone, Stdout: want}) {
		t.Errorf("Run() = %+v, want %q", got, want)
	}
	if reads.count != 0 {
		t.Errorf("roles read %d times, want none for a listing", reads.count)
	}
}

func TestListAnswersOneDocument(t *testing.T) {
	t.Parallel()

	address := listed(t)
	var reads rolesRead

	got := testkit.Run(t, program(address, auth.List(config(&reads, ""))), "", "account:list", "-json")

	document := got.Stdout
	store := storeAt(t, address)
	for _, email := range []string{"admin@example.com", "author@example.com", "editor@example.com",
		"roleless@example.com"} {
		document = strings.ReplaceAll(document, account(t, store, email).ID.String(), "id of "+email)
	}
	want := `{
  "accounts": [
    {
      "id": "id of roleless@example.com",
      "email": "roleless@example.com",
      "name": "account without a role",
      "role": "",
      "disabled": false
    },
    {
      "id": "id of admin@example.com",
      "email": "admin@example.com",
      "name": "admin account",
      "role": "admin",
      "disabled": false
    },
    {
      "id": "id of author@example.com",
      "email": "author@example.com",
      "name": "author account",
      "role": "author",
      "disabled": false
    },
    {
      "id": "id of editor@example.com",
      "email": "editor@example.com",
      "name": "editor account",
      "role": "editor",
      "disabled": true
    }
  ]
}
`
	if got.Code != gonsole.ExitDone || got.Stderr != "" || document != want {
		t.Errorf("Run() = %+v with the stored ids named, document %q, want %q", got, document, want)
	}
}

func TestListOfNoAccount(t *testing.T) {
	t.Parallel()

	var reads rolesRead
	p := program(migrated(t), auth.List(config(&reads, "")))

	text := testkit.Run(t, p, "", "account:list")
	document := testkit.Run(t, p, "", "account:list", "-json")

	if text != (testkit.Result{Code: gonsole.ExitDone}) {
		t.Errorf("Run() = %+v, want nothing printed", text)
	}
	if want := "{\n  \"accounts\": []\n}\n"; document != (testkit.Result{Code: gonsole.ExitDone, Stdout: want}) {
		t.Errorf("Run() with -json = %+v, want %q", document, want)
	}
}

func TestListFailsWithoutAUsableDatabase(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name   string
		env    map[string]string
		prefix string
	}{
		{"no database setting", map[string]string{}, "myapp: MYAPP_DATABASE_URL is required\n"},
		{"a malformed database address", map[string]string{
			"MYAPP_DATABASE_URL": "postgres://localhost:5434/postgres?pool_max_conns=0",
		}, "myapp: open the database: "},
		{"a database never migrated", nil, "myapp: postgres: list users: "},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			var reads rolesRead
			p := program(empty(t), auth.List(config(&reads, "")))
			if tc.env != nil {
				p.Env.Getenv = testkit.Getenv(tc.env)
			}

			got := testkit.Run(t, p, "", "account:list")

			if got.Code != gonsole.ExitFailed || got.Stdout != "" || !strings.HasPrefix(got.Stderr, tc.prefix) {
				t.Errorf("Run() = %+v, want exit 1 and stderr opening with %q", got, tc.prefix)
			}
		})
	}
}
