// SPDX-License-Identifier: Apache-2.0

package postgresblob

import (
	"context"

	"github.com/jackc/pgx/v5/pgxpool"
	"gocloud.dev/blob/driver"
)

// OpenDriver returns the driver OpenBucket wraps.
func OpenDriver(ctx context.Context, pool *pgxpool.Pool, opts *Options) (driver.Bucket, error) {
	b, err := openBucket(ctx, pool, opts)
	if err != nil {
		return nil, err
	}
	return b, nil
}

// MigrationStatements is migrationStatements under an exported name.
var MigrationStatements = migrationStatements

// LockOf is lockOf under an exported name.
var LockOf = lockOf
