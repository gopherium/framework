// SPDX-License-Identifier: Apache-2.0

package auth_test

import (
	"strings"
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

func TestRecordMigrationCreatesTheRecordsAsOftenAsItRuns(t *testing.T) {
	t.Parallel()

	address := empty(t)
	step := auth.RecordMigration()

	for run := range 2 {
		if err := step.Run(t.Context(), address); err != nil {
			t.Fatalf("run %d: Run() = %v, want nil", run+1, err)
		}
	}

	if step.Name != "records" {
		t.Errorf("Name = %q, want records", step.Name)
	}
	if !exists(t, address, "gonsole.records") || !exists(t, address, "gonsole.goose_db_version") {
		t.Errorf("gonsole.records and its version table are missing after the migration")
	}
}

func TestRecordMigrationNamesAnAddressItCannotRead(t *testing.T) {
	t.Parallel()

	err := auth.RecordMigration().Run(t.Context(), "postgres://%zz")

	if err == nil || !strings.HasPrefix(err.Error(), "open the database: ") {
		t.Errorf("Run() = %v, want the address named unreadable", err)
	}
}

func TestRecordMigrationNamesADatabaseItCannotReach(t *testing.T) {
	t.Parallel()

	err := auth.RecordMigration().Run(t.Context(), "postgres://postgres@127.0.0.1:1/none?connect_timeout=1")

	if err == nil || !strings.HasPrefix(err.Error(), "create the gonsole schema: ") {
		t.Errorf("Run() = %v, want the schema named", err)
	}
}

func TestRecordMigrationNamesAStepItCannotApply(t *testing.T) {
	t.Parallel()

	address := empty(t)
	run(t, address, "CREATE SCHEMA gonsole", "CREATE TABLE gonsole.records (id int)")

	err := auth.RecordMigration().Run(t.Context(), address)

	if err == nil || !strings.HasPrefix(err.Error(), "apply the records schema: ") {
		t.Errorf("Run() = %v, want the records schema named", err)
	}
}
