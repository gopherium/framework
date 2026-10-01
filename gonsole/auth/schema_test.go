// SPDX-License-Identifier: Apache-2.0

package auth_test

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/pressly/goose/v3/lock"

	"github.com/gopherium/framework/gonsole"
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

func TestEveryStepWaitsForTheMigrationLock(t *testing.T) {
	t.Parallel()

	tests := []struct {
		step  gonsole.Step
		table string
	}{
		{auth.Migration(), "auth.users"},
		{auth.RecordMigration(), "gonsole.records"},
	}
	for _, tt := range tests {
		t.Run(tt.step.Name, func(t *testing.T) {
			t.Parallel()

			address := empty(t)
			holdMigrationLock(t, address)
			ctx, cancel := context.WithTimeout(t.Context(), 500*time.Millisecond)
			defer cancel()

			err := tt.step.Run(ctx, address)

			if !errors.Is(err, context.DeadlineExceeded) || exists(t, address, tt.table) {
				t.Errorf("Run() = %v, want the deadline and no %s table", err, tt.table)
			}
		})
	}
}

func TestEveryStepLetsRunsAtOnceAllSucceed(t *testing.T) {
	t.Parallel()

	for _, step := range []gonsole.Step{auth.Migration(), auth.RecordMigration()} {
		t.Run(step.Name, func(t *testing.T) {
			t.Parallel()

			address := empty(t)
			failures := make(chan error, 4)
			var runs sync.WaitGroup
			for range 4 {
				runs.Go(func() { failures <- step.Run(t.Context(), address) })
			}
			runs.Wait()
			close(failures)

			for err := range failures {
				if err != nil {
					t.Errorf("Run() = %v, want every run to succeed", err)
				}
			}
		})
	}
}

func TestEveryStepSucceedsWhenAnotherSessionCreatesItsSchemaFirst(t *testing.T) {
	t.Parallel()

	tests := []struct {
		step   gonsole.Step
		schema string
	}{
		{auth.Migration(), "auth"},
		{auth.RecordMigration(), "gonsole"},
	}
	for _, tt := range tests {
		t.Run(tt.step.Name, func(t *testing.T) {
			t.Parallel()

			address := empty(t)
			holder, err := pgx.Connect(t.Context(), address)
			if err != nil {
				t.Fatalf("connecting to %s: %v", address, err)
			}
			defer func() { _ = holder.Close(context.Background()) }()
			tx, err := holder.Begin(t.Context())
			if err != nil {
				t.Fatalf("beginning: %v", err)
			}
			if _, err := tx.Exec(t.Context(), "CREATE SCHEMA "+tt.schema); err != nil {
				t.Fatalf("creating the %s schema: %v", tt.schema, err)
			}
			ran := make(chan error, 1)
			go func() { ran <- tt.step.Run(t.Context(), address) }()
			awaitSession(t, address, holder, ran, "wait_event_type", "Lock")

			if err := tx.Commit(t.Context()); err != nil {
				t.Fatalf("committing the %s schema: %v", tt.schema, err)
			}

			if err := <-ran; err != nil {
				t.Errorf("Run() = %v, want it to succeed", err)
			}
		})
	}
}

func TestEveryStepLetsAConcurrentIndexBuildFinishWhileItWaits(t *testing.T) {
	t.Parallel()

	for _, step := range []gonsole.Step{auth.Migration(), auth.RecordMigration()} {
		t.Run(step.Name, func(t *testing.T) {
			t.Parallel()

			address := empty(t)
			run(t, address, "CREATE TABLE items (x int)")
			holder := holdMigrationLock(t, address)
			ran := make(chan error, 1)
			go func() { ran <- step.Run(t.Context(), address) }()
			awaitSession(t, address, holder, ran, "query", "%advisory%")

			_, built := holder.Exec(t.Context(), "CREATE INDEX CONCURRENTLY items_x ON items (x)")
			if _, err := holder.Exec(t.Context(), "SELECT pg_advisory_unlock($1)", lock.DefaultLockID); err != nil {
				t.Fatalf("releasing the migration lock: %v", err)
			}

			if err := <-ran; built != nil || err != nil {
				t.Errorf("index build = %v and Run() = %v, want both to finish", built, err)
			}
		})
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

	if err == nil || !strings.HasPrefix(err.Error(), "apply the records schema: ") {
		t.Errorf("Run() = %v, want the records schema named", err)
	}
}

func TestRecordMigrationNamesASchemaItCannotCreate(t *testing.T) {
	t.Parallel()

	address := empty(t)
	run(t, address, "DO $$ BEGIN EXECUTE format("+
		"'ALTER DATABASE %I SET default_transaction_read_only = on', current_database()); END $$")

	err := auth.RecordMigration().Run(t.Context(), address)

	if err == nil || !strings.Contains(err.Error(), "create the gonsole schema: ") {
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
