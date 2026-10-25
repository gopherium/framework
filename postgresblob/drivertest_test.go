// SPDX-License-Identifier: Apache-2.0

package postgresblob_test

import (
	"context"
	"net/http"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
	"gocloud.dev/blob/driver"
	"gocloud.dev/blob/drivertest"

	"github.com/gopherium/framework/postgresblob"
)

// harness is the drivertest harness of one fresh test database.
type harness struct {
	// pool reaches the fresh test database.
	pool *pgxpool.Pool
	// opts are the options of every driver the harness makes.
	opts postgresblob.Options
	// prefix starts every key of every driver the harness makes, and an empty prefix adds nothing.
	prefix string
}

// harnessMaker returns a maker of harnesses whose drivers take opts in testSchema and prefix every key with prefix.
func harnessMaker(opts postgresblob.Options, prefix string) drivertest.HarnessMaker {
	opts.Schema = testSchema
	return func(_ context.Context, t *testing.T) (drivertest.Harness, error) {
		return &harness{pool: freshDB(t).pool, opts: opts, prefix: prefix}, nil
	}
}

// MakeDriver returns a driver on the tables of the fresh test database.
func (h *harness) MakeDriver(ctx context.Context) (driver.Bucket, error) {
	opts := h.opts
	drv, err := postgresblob.OpenDriver(ctx, h.pool, &opts)
	if err != nil || h.prefix == "" {
		return drv, err
	}
	return driver.NewPrefixedBucket(drv, h.prefix), nil
}

// MakeDriverForNonexistentBucket returns a driver on a schema the fresh test database lacks.
func (h *harness) MakeDriverForNonexistentBucket(ctx context.Context) (driver.Bucket, error) {
	opts := h.opts
	opts.Schema = unmigratedSchema
	return postgresblob.OpenDriver(ctx, h.pool, &opts)
}

// HTTPClient returns nil.
func (*harness) HTTPClient() *http.Client { return nil }

// SupportsCopy reports that the driver copies.
func (*harness) SupportsCopy() bool { return true }

// SupportsNonSlashDelimiters reports that the driver lists with any delimiter.
func (*harness) SupportsNonSlashDelimiters() bool { return true }

// Close closes the pool of the harness.
func (h *harness) Close() { h.pool.Close() }

func TestConformance(t *testing.T) {
	t.Parallel()

	drivertest.RunConformanceTests(t, harnessMaker(postgresblob.Options{}, ""), nil)
}

func TestConformanceThroughAPrefixedBucket(t *testing.T) {
	t.Parallel()

	drivertest.RunConformanceTests(t, harnessMaker(postgresblob.Options{}, "tenant/"), nil)
}

func TestConformanceWithSevenByteChunks(t *testing.T) {
	t.Parallel()

	drivertest.RunConformanceTests(t, harnessMaker(postgresblob.Options{ChunkSize: 7}, ""), nil)
}
