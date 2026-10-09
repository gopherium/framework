// SPDX-License-Identifier: Apache-2.0

package auth_test

import (
	"testing"

	"github.com/gopherium/gouncer/authkit/postgres"

	"github.com/gopherium/framework/gonsole/auth"
)

func TestThePostgresUserStoreIsAnAccountStore(t *testing.T) {
	t.Parallel()

	var store any = (*postgres.UserStore)(nil)

	if _, ok := store.(auth.Accounts); !ok {
		t.Error("*postgres.UserStore is not an auth.Accounts, want every account command able to run on it")
	}
}
