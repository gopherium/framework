// SPDX-License-Identifier: Apache-2.0

package auth_test

import (
	"testing"

	"github.com/gopherium/framework/gonsole/auth"
)

func TestMigrationAppliesTheAccountSchemaAsOftenAsItRuns(t *testing.T) {
	t.Parallel()

	address := empty(t)
	step := auth.Migration()

	for run := range 2 {
		if err := step.Run(t.Context(), address); err != nil {
			t.Fatalf("run %d: Run() = %v, want nil", run+1, err)
		}
	}

	if step.Name != "accounts" {
		t.Errorf("Name = %q, want accounts", step.Name)
	}
	if _, err := storeAt(t, address).ListUsers(t.Context()); err != nil {
		t.Errorf("ListUsers() after the migration = %v, want the account schema in place", err)
	}
}
