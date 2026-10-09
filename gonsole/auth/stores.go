// SPDX-License-Identifier: Apache-2.0

package auth

import (
	"context"
	"time"

	"github.com/gopherium/gouncer/authkit"
)

// Stores is the account store and the record store one call runs on.
type Stores struct {
	// Accounts holds the accounts.
	Accounts Accounts
	// Records holds the command records.
	Records RecordStore
}

// Accounts is the account store the account commands and Authorize run on.
type Accounts interface {
	authkit.AdminStore

	// GrantRoleToRoleless gives role to every account holding none and returns how many it changed.
	GrantRoleToRoleless(ctx context.Context, role string) (int64, error)
}

// RecordStore is the store of the command records Record writes and account:records lists.
type RecordStore interface {
	// Held reports whether the database holds the table the records go to.
	Held(ctx context.Context) (bool, error)
	// Insert stores entry, finding the account its actor names.
	Insert(ctx context.Context, entry Entry) error
	// Latest returns the newest entries first, at most limit of them.
	Latest(ctx context.Context, limit int) ([]Entry, error)
}

// Entry is one applied guarded command as account:records answers it.
type Entry struct {
	// ID is the entry's UUIDv7, which the document of account:records leaves out.
	ID string `json:"-"`
	// AppliedAt is when the command applied.
	AppliedAt time.Time `json:"applied_at"`
	// Actor is the address of the acting account.
	Actor string `json:"actor"`
	// AccountID is the id of the account at the actor's address, nil when none held it.
	AccountID *string `json:"account_id"`
	// Command is the full name of the command.
	Command string `json:"command"`
	// Args are the command's positional arguments.
	Args []string `json:"args"`
	// Flags maps each of the command's own flags the line set to its value.
	Flags map[string]string `json:"flags"`
}
