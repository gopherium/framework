// SPDX-License-Identifier: Apache-2.0

package auth_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/gopherium/gouncer"

	"github.com/gopherium/framework/gonsole/auth"
)

// demoPassword is the password every demo account in these tests holds.
const demoPassword = "password1234"

// demoAccounts returns two demo accounts, one per role.
func demoAccounts() []auth.Account {
	return []auth.Account{
		{Email: "admin@example.com", Name: "Maria Perez", Password: demoPassword, Role: "admin"},
		{Email: "editor@example.com", Name: "Maria Perez", Password: demoPassword, Role: "editor"},
	}
}

// failingWriter is a writer whose every write fails.
type failingWriter struct{}

// Write fails.
func (failingWriter) Write([]byte) (int, error) {
	return 0, errors.New("the output is closed")
}

func TestEnsureAccountsCreatesEachAccountOnce(t *testing.T) {
	t.Parallel()

	store := storeAt(t, migrated(t))
	var first, second strings.Builder

	errFirst := auth.EnsureAccounts(t.Context(), store, demoAccounts(), &first)
	errSecond := auth.EnsureAccounts(t.Context(), store, demoAccounts(), &second)

	if errFirst != nil || errSecond != nil {
		t.Fatalf("EnsureAccounts() = %v, then %v, want nil twice", errFirst, errSecond)
	}
	if want := "created admin@example.com\ncreated editor@example.com\n"; first.String() != want {
		t.Errorf("first run wrote %q, want %q", first.String(), want)
	}
	if want := "kept admin@example.com\nkept editor@example.com\n"; second.String() != want {
		t.Errorf("second run wrote %q, want %q", second.String(), want)
	}
	editor, err := store.UserByEmail(t.Context(), "editor@example.com")
	if err != nil || editor.Role != "editor" {
		t.Errorf("editor = %+v, %v, want the editor role", editor, err)
	}
}

func TestEnsureAccountsGivesTheRoleToATakenAccountHoldingNone(t *testing.T) {
	t.Parallel()

	store := storeAt(t, migrated(t))
	roleless, err := gouncer.NewUser("admin@example.com", "Maria Perez", demoPassword)
	if err != nil {
		t.Fatalf("NewUser() = %v", err)
	}
	if err := store.CreateUser(t.Context(), roleless); err != nil {
		t.Fatalf("CreateUser() = %v", err)
	}
	var out strings.Builder

	err = auth.EnsureAccounts(t.Context(), store, demoAccounts()[:1], &out)

	if want := "kept admin@example.com\n"; err != nil || out.String() != want {
		t.Errorf("EnsureAccounts() = %v, wrote %q, want nil and %q", err, out.String(), want)
	}
	held, err := store.UserByEmail(t.Context(), "admin@example.com")
	if err != nil || held.Role != "admin" {
		t.Errorf("taken account = %+v, %v, want it to hold admin", held, err)
	}
}

func TestEnsureAccountsStopsAtAnAccountItCannotCreate(t *testing.T) {
	t.Parallel()

	store := storeAt(t, migrated(t))
	accounts := demoAccounts()
	accounts = []auth.Account{
		accounts[0], {Email: "weak@example.com", Name: "Maria Perez", Password: "short", Role: "editor"}, accounts[1],
	}
	var out strings.Builder

	err := auth.EnsureAccounts(t.Context(), store, accounts, &out)

	want := "account weak@example.com: gouncer: password shorter than 12 characters"
	if !errors.Is(err, gouncer.ErrWeakPassword) || errorText(err) != want {
		t.Errorf("EnsureAccounts() = %v, want the weak password refusal %q", err, want)
	}
	if want := "created admin@example.com\n"; out.String() != want {
		t.Errorf("wrote %q, want %q", out.String(), want)
	}
	if _, err := store.UserByEmail(t.Context(), "editor@example.com"); !errors.Is(err, gouncer.ErrUserNotFound) {
		t.Errorf("the account after the refusal = %v, want it never created", err)
	}
}

func TestEnsureAccountsFailsWhenItsLineCannotBeWritten(t *testing.T) {
	t.Parallel()

	store := storeAt(t, migrated(t))

	err := auth.EnsureAccounts(t.Context(), store, demoAccounts(), failingWriter{})

	if errorText(err) != "the output is closed" {
		t.Errorf("EnsureAccounts() = %v, want the write failure", err)
	}
	if _, err := store.UserByEmail(t.Context(), "editor@example.com"); !errors.Is(err, gouncer.ErrUserNotFound) {
		t.Errorf("the second account = %v, want it never created after the failed line", err)
	}
}

// errorText returns the message of err, empty when it is nil.
func errorText(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}
