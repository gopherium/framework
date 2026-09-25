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

// disablePage is the help page of account:disable in a program whose account writes name no capability.
const disablePage = `disable one account

Usage:
  myapp account:disable [flags] <email>

Flags:
  -yes
    	apply the change, a dry run without it
`

// enablePage is the help page of account:enable in a program whose account writes name no capability.
const enablePage = `enable one disabled account

Usage:
  myapp account:enable [flags] <email>

Flags:
  -yes
    	apply the change, a dry run without it
`

// withDisabledEditor returns the address of a seeded database whose editor is disabled.
func withDisabledEditor(t *testing.T) string {
	t.Helper()
	address := seeded(t)
	store := storeAt(t, address)
	if err := store.SetUserDisabled(t.Context(), account(t, store, "editor@example.com").ID, true); err != nil {
		t.Fatalf("SetUserDisabled() = %v", err)
	}
	return address
}

// standingCommands returns account:disable and account:enable over cfg.
func standingCommands(cfg auth.Config) []gonsole.Command {
	return []gonsole.Command{auth.Disable(cfg), auth.Enable(cfg)}
}

func TestDisableAndEnableChangeTheStandingOnlyWithYes(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name     string
		disabled bool
		args     []string
		stdout   string
		stderr   string
		after    bool
	}{
		{"a dry run of a disable", false, []string{"account:disable", "editor@example.com"},
			"would disable editor@example.com\n", dryRunNotice, false},
		{"an applied disable", false, []string{"account:disable", "editor@example.com", "-yes"},
			"disabled editor@example.com\n", "", true},
		{"a disable of an address typed in capitals", false, []string{"account:disable", " Editor@Example.com ", "-yes"},
			"disabled editor@example.com\n", "", true},
		{"a dry run of an enable", true, []string{"account:enable", "editor@example.com"},
			"would enable editor@example.com\n", dryRunNotice, true},
		{"an applied enable", true, []string{"account:enable", "editor@example.com", "-yes"},
			"enabled editor@example.com\n", "", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			address := seeded(t)
			if tc.disabled {
				address = withDisabledEditor(t)
			}
			var reads rolesRead

			got := testkit.Run(t, program(address, standingCommands(config(&reads, ""))...), "", tc.args...)

			if want := (testkit.Result{Code: gonsole.ExitDone, Stdout: tc.stdout, Stderr: tc.stderr}); got != want {
				t.Fatalf("Run() = %+v, want %+v", got, want)
			}
			if held := account(t, storeAt(t, address), "editor@example.com"); held.Disabled != tc.after {
				t.Errorf("disabled = %t, want %t", held.Disabled, tc.after)
			}
		})
	}
}

func TestDisableAndEnableRefuseALineTheyCannotRun(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name   string
		args   []string
		code   int
		stdout string
		stderr string
	}{
		{"the last privileged account", []string{"account:disable", "admin@example.com", "-yes"}, gonsole.ExitFailed,
			"", "myapp: admin@example.com is the last enabled privileged account\n"},
		{"an unknown address to disable", []string{"account:disable", "nobody@example.com"}, gonsole.ExitFailed, "",
			"myapp: gouncer: user not found\n"},
		{"an unknown address to enable", []string{"account:enable", "nobody@example.com", "-yes"}, gonsole.ExitFailed,
			"", "myapp: gouncer: user not found\n"},
		{"no address", []string{"account:disable"}, gonsole.ExitMisused, "",
			"myapp: account:disable wants <email>\n\n" + disablePage},
		{"a request for help to disable", []string{"account:disable", "-h"}, gonsole.ExitDone, disablePage, ""},
		{"a request for help to enable", []string{"account:enable", "-h"}, gonsole.ExitDone, enablePage, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			address := seeded(t)
			var reads rolesRead

			got := testkit.Run(t, program(address, standingCommands(config(&reads, ""))...), "", tc.args...)

			if want := (testkit.Result{Code: tc.code, Stdout: tc.stdout, Stderr: tc.stderr}); got != want {
				t.Errorf("Run() = %+v, want %+v", got, want)
			}
			if held := account(t, storeAt(t, address), "admin@example.com"); held.Disabled {
				t.Error("the admin account is disabled, want it kept enabled")
			}
		})
	}
}

func TestDisableFailsWhenTheRolesCannotBeRead(t *testing.T) {
	t.Parallel()

	cfg := auth.Config{Roles: func(context.Context, gonsole.Call) (auth.Roles, error) {
		return auth.Roles{}, errors.New("the plugin table is locked")
	}}

	got := testkit.Run(t, program(seeded(t), standingCommands(cfg)...), "", "account:disable", "editor@example.com")

	if want := (testkit.Result{Code: gonsole.ExitFailed, Stderr: "myapp: the plugin table is locked\n"}); got != want {
		t.Errorf("Run() = %+v, want %+v", got, want)
	}
}

func TestDisableAndEnableAskForTheActingAccountTheProgramNames(t *testing.T) {
	t.Parallel()

	for _, command := range []string{"account:disable", "account:enable"} {
		t.Run(command, func(t *testing.T) {
			t.Parallel()

			var reads rolesRead

			got := testkit.Run(t, program(seeded(t), standingCommands(config(&reads, "manage_accounts"))...), "",
				command, "editor@example.com")

			if want := "myapp: " + command + " wants -as <email>\n"; got.Code != gonsole.ExitMisused ||
				!strings.HasPrefix(got.Stderr, want) {
				t.Errorf("Run() = %+v, want exit 2 opening with %q", got, want)
			}
		})
	}
}
