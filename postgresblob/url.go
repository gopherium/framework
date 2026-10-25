// SPDX-License-Identifier: Apache-2.0

package postgresblob

import (
	"context"
	"errors"
	"net/url"

	"github.com/jackc/pgx/v5/pgxpool"
	"gocloud.dev/blob"
)

const (
	// Scheme is the URL scheme postgresblob registers its URLOpener under on blob.DefaultURLMux.
	Scheme = "postgres"
	// schemaParameter is the query parameter of a URL that names the schema.
	schemaParameter = "schema"
)

// errUnparsable refuses a URL pgx cannot parse, and holds none of pgx's text.
var errUnparsable = errors.New("postgresblob: pgx cannot parse the URL or one of its settings")

// init registers a URLOpener with no options for Scheme on blob.DefaultURLMux.
func init() {
	blob.DefaultURLMux().RegisterBucket(Scheme, &URLOpener{})
}

// URLOpener opens buckets from postgres URLs, each on a pool of its own that the bucket closes.
type URLOpener struct {
	// Options are the options of every bucket the opener opens.
	Options Options
}

// OpenBucketURL opens a bucket on a new pool for u, in the schema its query parameter schema names.
func (o *URLOpener) OpenBucketURL(ctx context.Context, u *url.URL) (*blob.Bucket, error) {
	opts := o.Options
	address := *u
	query := address.Query()
	if query.Has(schemaParameter) {
		opts.Schema = query.Get(schemaParameter)
		query.Del(schemaParameter)
	}
	address.RawQuery = query.Encode()
	config, err := pgxpool.ParseConfig(address.String())
	if err != nil {
		return nil, errUnparsable
	}
	pool := must(pgxpool.NewWithConfig(ctx, config))
	b, err := openBucket(ctx, pool, &opts)
	if err != nil {
		pool.Close()
		return nil, err
	}
	b.ownsPool = true
	return blob.NewBucket(b), nil
}

// must returns value, and panics with err when err is set.
func must[T any](value T, err error) T {
	if err != nil {
		panic(err)
	}
	return value
}
