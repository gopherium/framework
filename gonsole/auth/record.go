// SPDX-License-Identifier: Apache-2.0

package auth

import (
	"context"
	"encoding/json"
	"fmt"
	"maps"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/gopherium/framework/gonsole"
)

// storeRecord inserts one record, finding the account the acting address names.
const storeRecord = `INSERT INTO gonsole.records (id, actor, account_id, command, args, flags)
VALUES ($1::uuid, $2, (SELECT id FROM auth.users WHERE email = $2), $3, $4::jsonb, $5::jsonb)`

// Record returns the hook that stores one row naming the acting account and the command it applied.
func Record(cfg Config) func(ctx context.Context, call gonsole.Call, command string) error {
	return func(ctx context.Context, call gonsole.Call, command string) error {
		timeout, err := cfg.recordTimeout(call.Env)
		if err != nil {
			return err
		}
		databaseURL, err := call.DatabaseURL()
		if err != nil {
			return err
		}
		bounded, cancel := context.WithTimeout(ctx, timeout)
		defer cancel()
		if err := store(bounded, databaseURL, call, command); err != nil {
			return fmt.Errorf("record %s: %w", command, err)
		}
		return nil
	}
}

// recordTimeout returns how long storing one record may take, as the setting or the fallback names it.
func (c Config) recordTimeout(env gonsole.Env) (time.Duration, error) {
	timeout, err := env.Duration("COMMAND_RECORD_TIMEOUT", c.RecordTimeout)
	if err != nil {
		return 0, err
	}
	if timeout <= 0 {
		return 0, fmt.Errorf("gonsole/auth: the record timeout must stand above zero, got %v", timeout)
	}
	return timeout, nil
}

// store inserts the record of command as call ran it into the database at databaseURL.
func store(ctx context.Context, databaseURL string, call gonsole.Call, command string) error {
	conn, err := pgx.Connect(ctx, databaseURL)
	if err != nil {
		return err
	}
	defer func() { _ = conn.Close(context.WithoutCancel(ctx)) }()
	flags := map[string]string{}
	maps.Copy(flags, call.Flags)
	id := uuid.Must(uuid.NewV7()).String()
	args := string(must(json.Marshal(append([]string{}, call.Args...))))
	_, err = conn.Exec(ctx, storeRecord, id, address(call.Actor), command, args, string(must(json.Marshal(flags))))
	return err
}
