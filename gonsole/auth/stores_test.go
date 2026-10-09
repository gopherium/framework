// SPDX-License-Identifier: Apache-2.0

package auth_test

import (
	"context"
	"encoding/json"
	"io"
	"maps"
	"testing"
	"time"

	"github.com/gopherium/gouncer/authkit/postgres"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/gopherium/framework/gonsole/auth"
	"github.com/gopherium/framework/gonsole/auth/recordstest"
)

// plantRecord inserts one record as applied at a given time, finding the account the acting address names.
const plantRecord = `INSERT INTO gonsole.records (id, applied_at, actor, account_id, command, args, flags)
VALUES ($1::uuid, $2, $3, (SELECT id FROM auth.users WHERE email = $3), $4, $5::jsonb, $6::jsonb)`

// postgresFixture returns the PostgreSQL record store of a fresh database and the hooks recordstest drives it with.
func postgresFixture(t *testing.T) recordstest.Fixture {
	t.Helper()
	address := migrated(t)
	pool, err := pgxpool.New(t.Context(), address)
	if err != nil {
		t.Fatalf("opening %s: %v", address, err)
	}
	t.Cleanup(pool.Close)
	accounts := postgres.NewUserStore(pool)
	return recordstest.Fixture{
		Records: auth.PostgresRecords(pool),
		Migrate: func(ctx context.Context) error { return auth.RecordMigration().Run(ctx, address) },
		Account: func(ctx context.Context, email string) (string, error) {
			held := auth.Account{Email: email, Name: "Maria Perez", Password: demoPassword, Role: "admin"}
			if err := auth.EnsureAccounts(ctx, accounts, []auth.Account{held}, io.Discard); err != nil {
				return "", err
			}
			user, err := accounts.UserByEmail(ctx, email)
			return user.ID.String(), err
		},
		Plant: func(ctx context.Context, entry auth.Entry, appliedAt time.Time) error {
			flags := map[string]string{}
			maps.Copy(flags, entry.Flags)
			_, err := pool.Exec(ctx, plantRecord, entry.ID, appliedAt, entry.Actor, entry.Command,
				append([]string{}, entry.Args...), flags)
			return err
		},
	}
}

func TestPostgresRecordsKeepTheContract(t *testing.T) {
	t.Parallel()

	recordstest.Run(t, postgresFixture)
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
