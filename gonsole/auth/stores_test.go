// SPDX-License-Identifier: Apache-2.0

package auth_test

import (
	"encoding/json"
	"maps"
	"slices"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/gopherium/gouncer/authkit/postgres"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/gopherium/framework/gonsole/auth"
)

// recordsAt returns the PostgreSQL records store of the database at address, its pool closed when the test ends.
func recordsAt(t *testing.T, address string) auth.RecordStore {
	t.Helper()
	pool, err := pgxpool.New(t.Context(), address)
	if err != nil {
		t.Fatalf("opening %s: %v", address, err)
	}
	t.Cleanup(pool.Close)
	return auth.PostgresRecords(pool)
}

func TestPostgresRecordsHoldsTheTableOnlyAfterItsMigration(t *testing.T) {
	t.Parallel()

	address := migrated(t)
	records := recordsAt(t, address)
	before, beforeErr := records.Held(t.Context())
	if err := auth.RecordMigration().Run(t.Context(), address); err != nil {
		t.Fatalf("RecordMigration() = %v", err)
	}

	after, afterErr := records.Held(t.Context())

	if before || beforeErr != nil || !after || afterErr != nil {
		t.Errorf("Held() = %t %v before and %t %v after the migration, want false then true", before, beforeErr,
			after, afterErr)
	}
}

func TestPostgresRecordsStoresAnEntryAndListsIt(t *testing.T) {
	t.Parallel()

	address := seeded(t)
	if err := auth.RecordMigration().Run(t.Context(), address); err != nil {
		t.Fatalf("RecordMigration() = %v", err)
	}
	records := recordsAt(t, address)
	entry := auth.Entry{
		ID: uuid.Must(uuid.NewV7()).String(), Actor: "admin@example.com", Command: "account:role",
		Args: []string{"author@example.com", "editor"}, Flags: map[string]string{"yes": "true"},
	}

	if err := records.Insert(t.Context(), entry); err != nil {
		t.Fatalf("Insert() = %v", err)
	}
	listed, err := records.Latest(t.Context(), 10)

	if err != nil || len(listed) != 1 {
		t.Fatalf("Latest() = %v, %v, want the one entry", listed, err)
	}
	got, admin := listed[0], account(t, storeAt(t, address), "admin@example.com").ID.String()
	if got.Actor != entry.Actor || got.Command != entry.Command || got.AccountID == nil || *got.AccountID != admin {
		t.Errorf("Latest() = %+v, want the actor, the command and the account %s", got, admin)
	}
	if !slices.Equal(got.Args, entry.Args) || !maps.Equal(got.Flags, entry.Flags) || got.AppliedAt.IsZero() {
		t.Errorf("Latest() args %q, flags %v, applied at %v, want %q, %v and a time", got.Args, got.Flags,
			got.AppliedAt, entry.Args, entry.Flags)
	}
}

func TestAnEntryKeepsTheDocumentAccountRecordsAnswers(t *testing.T) {
	t.Parallel()

	accountID := "0193f1a2-7b3c-7d4e-8f5a-6b7c8d9e0f1a"
	entry := auth.Entry{
		ID: "0193f1a2-7b3c-7d4e-8f5a-000000000001", AppliedAt: time.Date(2026, 10, 9, 8, 30, 0, 0, time.UTC),
		Actor: "admin@example.com", AccountID: &accountID, Command: "account:disable",
		Args: []string{"author@example.com"}, Flags: map[string]string{},
	}

	document, err := json.Marshal(entry)

	want := `{"applied_at":"2026-10-09T08:30:00Z","actor":"admin@example.com",` +
		`"account_id":"0193f1a2-7b3c-7d4e-8f5a-6b7c8d9e0f1a","command":"account:disable",` +
		`"args":["author@example.com"],"flags":{}}`
	if err != nil || string(document) != want {
		t.Errorf("json.Marshal() = %s, %v, want %s", document, err, want)
	}
}

func TestThePostgresUserStoreIsAnAccountStore(t *testing.T) {
	t.Parallel()

	var store any = (*postgres.UserStore)(nil)

	if _, ok := store.(auth.Accounts); !ok {
		t.Error("*postgres.UserStore is not an auth.Accounts, want every account command able to run on it")
	}
}
