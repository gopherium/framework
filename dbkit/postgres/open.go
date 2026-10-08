// SPDX-License-Identifier: Apache-2.0

package postgres

import (
	"context"
	"database/sql"
	"fmt"
	"math"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/jackc/pgx/v5/stdlib"

	"github.com/gopherium/framework/dbkit"
)

var (
	// errUnparsable refuses an address pgx cannot parse, and holds none of pgx's text.
	errUnparsable = fmt.Errorf("%w: pgx cannot parse the address or one of its settings", dbkit.ErrAddress)
	// errHealthCheck refuses an address that sets pool_health_check_period to zero or less.
	errHealthCheck = fmt.Errorf("%w: the address sets pool_health_check_period to zero or less, "+
		"give it a positive duration", dbkit.ErrAddress)
	// errLifetime refuses an address that sets pool_max_conn_lifetime to zero or less.
	errLifetime = fmt.Errorf("%w: the address sets pool_max_conn_lifetime to zero or less, "+
		"give it a positive duration", dbkit.ErrAddress)
	// errJitter refuses an address that sets pool_max_conn_lifetime_jitter below zero.
	errJitter = fmt.Errorf("%w: the address sets pool_max_conn_lifetime_jitter below zero, "+
		"give it zero or a positive duration", dbkit.ErrAddress)
	// errPingTimeout refuses an address that sets pool_ping_timeout.
	errPingTimeout = fmt.Errorf("%w: the address sets pool_ping_timeout, which only pgx 5.11 and later read, drop it",
		dbkit.ErrAddress)
)

// poolKeys are the pool settings Open refuses in an address.
var poolKeys = []string{"pool_max_conns", "pool_min_conns", "pool_min_idle_conns"}

// pingTimeoutKey is the pool setting pgx 5.10 sends to the server as a runtime parameter.
const pingTimeoutKey = "pool_ping_timeout"

// errPoolKey returns the error for an address that sets the pool setting key.
func errPoolKey(key string) error {
	return fmt.Errorf("%w: the address sets %s, Options.MaxConns sets the cap and the pool opens no idle connection",
		dbkit.ErrAddress, key)
}

// Options configures one PostgreSQL pool. MaxConns is required.
type Options struct {
	// MaxConns caps the connections the pool and its database/sql view hold together, from 2 to 2147483647.
	MaxConns int
}

// check returns the error for the first option Open refuses.
func (o Options) check() error {
	switch {
	case o.MaxConns < 2:
		return fmt.Errorf("dbkit: the option MaxConns must be 2 or more, got %d", o.MaxConns)
	case o.MaxConns > math.MaxInt32:
		return fmt.Errorf("dbkit: the option MaxConns must be %d or less, got %d", math.MaxInt32, o.MaxConns)
	}
	return nil
}

// Handle is the one PostgreSQL pool of a site and a database/sql view that draws from it.
type Handle struct {
	// Pool owns every connection of the handle.
	Pool *pgxpool.Pool
	// DB is the database/sql view of Pool, with no idle connection of its own.
	DB *sql.DB
}

// Open returns a handle on the PostgreSQL database of address, capped at opts.MaxConns, and connects to nothing.
func Open(address string, opts Options) (*Handle, error) {
	if err := opts.check(); err != nil {
		return nil, err
	}
	if err := CheckAddress(address); err != nil {
		return nil, err
	}
	config, err := poolConfig(address)
	if err != nil {
		return nil, err
	}
	config.MaxConns = int32(opts.MaxConns)
	config.MinConns = 0
	config.MinIdleConns = 0
	pool := must(pgxpool.NewWithConfig(context.Background(), config))
	return &Handle{Pool: pool, DB: stdlib.OpenDBFromPool(pool)}, nil
}

// poolConfig returns pgx's pool configuration of address, or the error for a pool setting Open refuses.
func poolConfig(address string) (*pgxpool.Config, error) {
	connConfig, err := pgx.ParseConfig(address)
	if err != nil {
		return nil, errUnparsable
	}
	if err := checkPoolKeys(connConfig.RuntimeParams); err != nil {
		return nil, err
	}
	config, err := pgxpool.ParseConfig(address)
	if err != nil {
		return nil, errUnparsable
	}
	if err := checkDurations(config); err != nil {
		return nil, err
	}
	return config, nil
}

// checkPoolKeys returns the error for the first refused pool setting among the runtime parameters params.
func checkPoolKeys(params map[string]string) error {
	for _, key := range poolKeys {
		if _, set := params[key]; set {
			return errPoolKey(key)
		}
	}
	if _, set := params[pingTimeoutKey]; set {
		return errPingTimeout
	}
	return nil
}

// checkDurations returns the error for the first pool duration of config that stops the pool.
func checkDurations(config *pgxpool.Config) error {
	switch {
	case config.HealthCheckPeriod <= 0:
		return errHealthCheck
	case config.MaxConnLifetime <= 0:
		return errLifetime
	case config.MaxConnLifetimeJitter < 0:
		return errJitter
	}
	return nil
}

// Close closes the view, then the pool.
func (h *Handle) Close() error {
	err := h.DB.Close()
	h.Pool.Close()
	return err
}

// must returns value, and panics with err when err is set.
func must[T any](value T, err error) T {
	if err != nil {
		panic(err)
	}
	return value
}
