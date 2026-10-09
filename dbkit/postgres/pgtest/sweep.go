// SPDX-License-Identifier: Apache-2.0

package pgtest

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/gopherium/framework/dbkit/postgres"
)

const (
	// sweepConns is the connection cap of the pool a sweep opens, the smallest that Open accepts.
	sweepConns = 2
	// objectInUse is the PostgreSQL error code of a drop that finds a session on the database.
	objectInUse = "55006"
	// noDatabase is the PostgreSQL error code of a drop that finds the database gone.
	noDatabase = "3D000"
	// insufficientPrivilege is the PostgreSQL error code of a query the role has no right to run.
	insufficientPrivilege = "42501"
)

// errSweepRights is the error of a sweep whose role may not read when each test database was made.
var errSweepRights = errors.New(
	"dbkit: Sweep needs a role that may run pg_stat_file and drop the test databases, such as a superuser")

// instancePattern matches the name pgtestdb gives each database it cuts from a template.
const instancePattern = `^testdb_tpl_[0-9a-f]{32}_inst_[0-9a-f]{8}$`

// instances lists the databases that are no template, match $1, hold $2 and keep a PG_VERSION file older than $3.
const instances = `SELECT datname FROM pg_database
WHERE NOT datistemplate AND datname ~ $1 AND strpos(datname, $2) > 0
AND (pg_stat_file('base/' || oid || '/PG_VERSION', true)).modification < now() - $3::interval
ORDER BY datname`

// Sweep drops each pgtestdb instance older than olderThan with no session on it, and returns the names it dropped.
func Sweep(ctx context.Context, address string, olderThan time.Duration) ([]string, error) {
	return sweep(ctx, address, olderThan, "")
}

// sweep is Sweep limited to the databases whose name holds scope.
func sweep(ctx context.Context, address string, olderThan time.Duration, scope string) (_ []string, err error) {
	if olderThan <= 0 {
		return nil, fmt.Errorf("dbkit: Sweep needs olderThan above zero, got %v", olderThan)
	}
	h, err := postgres.Open(address, postgres.Options{MaxConns: sweepConns})
	if err != nil {
		return nil, err
	}
	defer func() { err = errors.Join(err, h.Close()) }()
	names, err := list(ctx, h.Pool, olderThan, scope)
	if err != nil {
		return nil, err
	}
	var dropped []string
	for _, name := range names {
		gone, err := drop(ctx, h.Pool, name)
		if err != nil {
			return dropped, fmt.Errorf("dbkit: drop the test database %s: %w", name, err)
		}
		if gone {
			dropped = append(dropped, name)
		}
	}
	return dropped, nil
}

// list returns through pool the names of the instances older than olderThan whose name holds scope.
func list(ctx context.Context, pool *pgxpool.Pool, olderThan time.Duration, scope string) ([]string, error) {
	rows, _ := pool.Query(ctx, instances, instancePattern, scope, olderThan)
	names, err := pgx.CollectRows(rows, pgx.RowTo[string])
	var serverErr *pgconn.PgError
	switch {
	case errors.As(err, &serverErr) && serverErr.Code == insufficientPrivilege:
		return nil, fmt.Errorf("%w: %w", errSweepRights, err)
	case err != nil:
		return nil, fmt.Errorf("dbkit: list the test databases: %w", err)
	}
	return names, nil
}

// drop drops the database name through pool unless a session is on it or it is gone, and reports whether it went.
func drop(ctx context.Context, pool *pgxpool.Pool, name string) (bool, error) {
	_, err := pool.Exec(ctx, "DROP DATABASE "+pgx.Identifier{name}.Sanitize())
	var serverErr *pgconn.PgError
	if errors.As(err, &serverErr) && (serverErr.Code == objectInUse || serverErr.Code == noDatabase) {
		return false, nil
	}
	return err == nil, err
}
