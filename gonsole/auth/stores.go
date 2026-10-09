// SPDX-License-Identifier: Apache-2.0

package auth

import (
	"context"

	"github.com/gopherium/gouncer/authkit"
)

// Accounts is the account store the account commands and Authorize run on.
type Accounts interface {
	authkit.AdminStore

	// GrantRoleToRoleless gives role to every account holding none and returns how many it changed.
	GrantRoleToRoleless(ctx context.Context, role string) (int64, error)
}
