// SPDX-License-Identifier: Apache-2.0

package auth

import (
	"context"
	"fmt"
	"io"

	"github.com/gopherium/gouncer"
	"github.com/gopherium/gouncer/authkit"
)

// Account is one demo account a seed step ensures.
type Account struct {
	// Email is the account's address.
	Email string
	// Name is the account's display name.
	Name string
	// Password is the account's demo password.
	Password string
	// Role is the role the account stands under.
	Role string
}

// EnsureAccounts creates each account unless its address is taken and writes one line per account.
func EnsureAccounts(ctx context.Context, store gouncer.Store, accounts []Account, w io.Writer) error {
	for _, account := range accounts {
		created, err := authkit.EnsureAdmin(ctx, store, account.Email, account.Name, account.Password, account.Role)
		if err != nil {
			return fmt.Errorf("account %s: %w", account.Email, err)
		}
		verb := "kept"
		if created {
			verb = "created"
		}
		if _, err := fmt.Fprintf(w, "%s %s\n", verb, account.Email); err != nil {
			return err
		}
	}
	return nil
}
