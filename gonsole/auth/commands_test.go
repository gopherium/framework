// SPDX-License-Identifier: Apache-2.0

package auth_test

import (
	"slices"
	"testing"

	"github.com/gopherium/framework/gonsole"
	"github.com/gopherium/framework/gonsole/auth"
	"github.com/gopherium/framework/gonsole/testkit"
)

// intro is the paragraph every listing prints before its commands.
const intro = "Every command answers -h. A command that offers -json answers one JSON document. " +
	"A command that offers -yes is a dry run until -yes.\n"

// accountListing is the listing of a program whose only commands are the account commands.
const accountListing = `myapp

Usage:
  myapp <command> [flags] [arguments]

` + intro + `
Available commands:
  check                 check every setting, every plugin and every command name
  help                  print the help of one command
  list                  list every command
  migrate               apply every schema step
  version               print the version
 account
  account:create-admin  create an account under a role
  account:disable       disable one account
  account:enable        enable one disabled account
  account:grant-role    give a role to every account holding none
  account:list          list every account with its role
  account:role          set one account's role
`

func TestCommandsOffersEveryAccountCommandInOrder(t *testing.T) {
	t.Parallel()

	var reads rolesRead

	commands := auth.Commands(config(&reads, "manage_accounts"))

	var got []string
	for _, cmd := range commands {
		got = append(got, cmd.Name+" "+cmd.Capability)
	}
	want := []string{
		"account:create-admin ", "account:grant-role manage_accounts", "account:list ",
		"account:role manage_accounts", "account:disable manage_accounts", "account:enable manage_accounts",
	}
	if !slices.Equal(got, want) {
		t.Errorf("commands = %q, want %q", got, want)
	}
	if err := program(migrated(t), commands...).Check(gonsole.Loaded{}); err != nil {
		t.Errorf("Check() = %v, want nil", err)
	}
}

func TestCommandsListUnderTheAccountNamespace(t *testing.T) {
	t.Parallel()

	var reads rolesRead

	got := testkit.Run(t, program(migrated(t), auth.Commands(config(&reads, ""))...), "", "list")

	if got != (testkit.Result{Code: gonsole.ExitDone, Stdout: accountListing}) {
		t.Errorf("Run() = %+v, want %q", got, accountListing)
	}
}
