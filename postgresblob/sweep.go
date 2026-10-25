// SPDX-License-Identifier: Apache-2.0

package postgresblob

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// sweepPending deletes the oldest pending rows untouched for an interval that no transaction holds, up to a limit.
const sweepPending = `DELETE FROM {objects} WHERE id IN (
    SELECT id FROM {objects} WHERE state = 'pending' AND modified_at < now() - $1::interval
    ORDER BY modified_at LIMIT $2 FOR UPDATE SKIP LOCKED
)`

// SweepOptions name the unfinished uploads one Sweep call deletes.
type SweepOptions struct {
	// Schema names the schema that holds the tables, DefaultSchema when empty.
	Schema string
	// OlderThan is how long an upload must go without a new chunk before Sweep deletes it, above zero.
	OlderThan time.Duration
	// Limit is the most uploads one call deletes, 1 or more.
	Limit int
}

// Sweep deletes the oldest unfinished uploads opts names, at most opts.Limit, and returns how many it deleted.
func Sweep(ctx context.Context, pool *pgxpool.Pool, opts SweepOptions) (int, error) {
	switch {
	case pool == nil:
		return 0, errNoPool
	case opts.OlderThan <= 0:
		return 0, fmt.Errorf("postgresblob: the option OlderThan must be above zero, got %v", opts.OlderThan)
	case opts.Limit < 1:
		return 0, fmt.Errorf("postgresblob: the option Limit must be 1 or more, got %d", opts.Limit)
	}
	schema := schemaOr(opts.Schema)
	tag, err := pool.Exec(ctx, tablesOf(schema).Replace(sweepPending), opts.OlderThan, opts.Limit)
	if err != nil {
		return 0, fmt.Errorf("postgresblob: sweep the schema %s: %w", schema, err)
	}
	return int(tag.RowsAffected()), nil
}
