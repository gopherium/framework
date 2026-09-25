// SPDX-License-Identifier: Apache-2.0

package auth

import (
	"cmp"
	"context"
	"fmt"
	"io"
	"text/tabwriter"

	"github.com/gopherium/gouncer"
	"github.com/gopherium/gouncer/authkit/postgres"

	"github.com/gopherium/framework/gonsole"
)

// List returns account:list, which lists every account with its role and standing.
func List(_ Config) gonsole.Command {
	return gonsole.Command{
		Name:    "account:list",
		Summary: "list every account with its role",
		JSON:    true,
		Run: func(ctx context.Context, call gonsole.Call) error {
			return withStore(ctx, call, func(store *postgres.UserStore) error {
				users, err := store.ListUsers(ctx)
				if err != nil {
					return err
				}
				if call.JSON {
					return call.Encode(document(users))
				}
				return table(call.Stdout, users)
			})
		},
	}
}

// listing is the document account:list answers.
type listing struct {
	Accounts []listed `json:"accounts"`
}

// listed is one account in the document account:list answers.
type listed struct {
	ID       string `json:"id"`
	Email    string `json:"email"`
	Name     string `json:"name"`
	Role     string `json:"role"`
	Disabled bool   `json:"disabled"`
}

// document returns the document that lists users.
func document(users []gouncer.User) listing {
	answer := listing{Accounts: make([]listed, 0, len(users))}
	for _, user := range users {
		answer.Accounts = append(answer.Accounts, listed{
			ID: user.ID.String(), Email: user.Email, Name: user.Name, Role: user.Role, Disabled: user.Disabled,
		})
	}
	return answer
}

// table writes one aligned line per user to w: the address, the role or a dash, and the standing.
func table(w io.Writer, users []gouncer.User) error {
	aligned := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	for _, user := range users {
		standing := "enabled"
		if user.Disabled {
			standing = "disabled"
		}
		_, _ = fmt.Fprintf(aligned, "%s\t%s\t%s\n", user.Email, cmp.Or(user.Role, "-"), standing)
	}
	return aligned.Flush()
}
