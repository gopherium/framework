// SPDX-License-Identifier: Apache-2.0

package auth

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/gopherium/framework/gonsole"
)

// Record returns the hook that stores one row naming the acting account and the command it applied.
func Record(cfg Config) func(ctx context.Context, call gonsole.Call, command string) error {
	return func(ctx context.Context, call gonsole.Call, command string) error {
		timeout, err := cfg.recordTimeout(call.Env)
		if err != nil {
			return err
		}
		insert, err := cfg.inserter(call)
		if err != nil {
			return err
		}
		bounded, cancel := context.WithTimeout(ctx, timeout)
		defer cancel()
		if err := insert(bounded, entryOf(call, command)); err != nil {
			return fmt.Errorf("record %s: %w", command, err)
		}
		return nil
	}
}

// inserter returns what stores the call's entry, the built stores or one connection to the database setting.
func (c Config) inserter(call gonsole.Call) (func(ctx context.Context, entry Entry) error, error) {
	if c.Stores != nil {
		return func(ctx context.Context, entry Entry) error {
			return c.withStores(ctx, call, func(stores Stores) error {
				return stores.Records.Insert(ctx, entry)
			})
		}, nil
	}
	databaseURL, err := call.DatabaseURL()
	if err != nil {
		return nil, err
	}
	return func(ctx context.Context, entry Entry) error {
		return store(ctx, databaseURL, entry)
	}, nil
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

// store inserts entry into the database at databaseURL over one connection.
func store(ctx context.Context, databaseURL string, entry Entry) error {
	conn, err := pgx.Connect(ctx, databaseURL)
	if err != nil {
		return err
	}
	defer func() { _ = conn.Close(context.WithoutCancel(ctx)) }()
	return postgresRecords{db: conn}.Insert(ctx, entry)
}

// entryOf returns the entry recording command as call ran it, under a new UUIDv7.
func entryOf(call gonsole.Call, command string) Entry {
	return Entry{
		ID: uuid.Must(uuid.NewV7()).String(), Actor: address(call.Actor), Command: command,
		Args: call.Args, Flags: call.Flags,
	}
}
