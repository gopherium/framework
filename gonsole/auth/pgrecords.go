// SPDX-License-Identifier: Apache-2.0

package auth

import (
	"context"
	"encoding/json"
	"maps"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

// storeRecord inserts one record, finding the account the acting address names.
const storeRecord = `INSERT INTO gonsole.records (id, actor, account_id, command, args, flags)
VALUES ($1::uuid, $2, (SELECT id FROM auth.users WHERE email = $2), $3, $4::jsonb, $5::jsonb)`

// latestRecords reads the newest records first, as many as the limit allows.
const latestRecords = `SELECT applied_at, actor, account_id::text, command, args, flags
FROM gonsole.records ORDER BY applied_at DESC, id DESC LIMIT $1`

// querier runs the statements of the PostgreSQL record store, on a pool or on one connection.
type querier interface {
	// Exec runs a statement that answers no rows.
	Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
	// Query runs a statement that answers rows.
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
	// QueryRow runs a statement that answers one row.
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

// postgresRecords is the record store of one PostgreSQL database.
type postgresRecords struct {
	// db runs the statements.
	db querier
}

// PostgresRecords returns the record store of the PostgreSQL database behind pool.
func PostgresRecords(pool *pgxpool.Pool) RecordStore {
	return postgresRecords{db: pool}
}

// Held reports whether the database holds the gonsole.records table.
func (r postgresRecords) Held(ctx context.Context) (bool, error) {
	var held bool
	err := r.db.QueryRow(ctx, "SELECT to_regclass('gonsole.records') IS NOT NULL").Scan(&held)
	return held, err
}

// Insert stores entry, finding the account its actor names in auth.users.
func (r postgresRecords) Insert(ctx context.Context, entry Entry) error {
	args, flags := encoded(entry)
	_, err := r.db.Exec(ctx, storeRecord, entry.ID, entry.Actor, entry.Command, args, flags)
	return err
}

// Latest returns the newest entries first, at most limit of them.
func (r postgresRecords) Latest(ctx context.Context, limit int) ([]Entry, error) {
	rows, err := r.db.Query(ctx, latestRecords, limit)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(row pgx.CollectableRow) (Entry, error) {
		var held Entry
		err := row.Scan(&held.AppliedAt, &held.Actor, &held.AccountID, &held.Command, &held.Args, &held.Flags)
		return held, err
	})
}

// encoded returns the JSON text of the entry's arguments and flags, an empty list and object for none.
func encoded(entry Entry) (string, string) {
	flags := map[string]string{}
	maps.Copy(flags, entry.Flags)
	return string(must(json.Marshal(append([]string{}, entry.Args...)))), string(must(json.Marshal(flags)))
}
