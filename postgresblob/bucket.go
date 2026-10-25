// SPDX-License-Identifier: Apache-2.0

package postgresblob

import (
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"hash/fnv"
	"slices"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"gocloud.dev/blob"
	"gocloud.dev/blob/driver"
	"gocloud.dev/gcerrors"
)

const (
	// DefaultSchema is the schema of a bucket whose options name none.
	DefaultSchema = "blob"
	// DefaultChunkSize is the chunk size in bytes of a bucket whose options set none.
	DefaultChunkSize = 1 << 20
	// MaxChunkSize is the largest chunk size in bytes that Options.ChunkSize may set.
	MaxChunkSize = 256 << 20
	// DefaultCleanupTimeout is the cleanup timeout of a bucket whose options set none.
	DefaultCleanupTimeout = 10 * time.Second
	// utf8Encoding is the server encoding the driver needs.
	utf8Encoding = "UTF8"
)

// Options configures one bucket, each zero field taking its default.
type Options struct {
	// Schema names the schema that holds the tables, DefaultSchema when empty.
	Schema string
	// ChunkSize is the most bytes one chunk row holds, DefaultChunkSize when zero.
	ChunkSize int
	// MaxSize is the most bytes one object may hold, with no cap when zero.
	MaxSize int64
	// CleanupTimeout bounds the removal of an upload whose context ended, DefaultCleanupTimeout when zero.
	CleanupTimeout time.Duration
}

// errNoPool refuses a nil pool.
var errNoPool = errors.New("postgresblob: the driver needs a pool, got nil")

// resolved returns o with each zero field set to its default, or the error for the first field out of range.
func (o Options) resolved() (Options, error) {
	switch {
	case o.ChunkSize < 0 || o.ChunkSize > MaxChunkSize:
		return o, fmt.Errorf("postgresblob: the option ChunkSize must be from 0 to %d, got %d", MaxChunkSize, o.ChunkSize)
	case o.MaxSize < 0:
		return o, fmt.Errorf("postgresblob: the option MaxSize must be 0 or more, got %d", o.MaxSize)
	case o.CleanupTimeout < 0:
		return o, fmt.Errorf("postgresblob: the option CleanupTimeout must be 0 or more, got %v", o.CleanupTimeout)
	}
	o.Schema = schemaOr(o.Schema)
	if o.ChunkSize == 0 {
		o.ChunkSize = DefaultChunkSize
	}
	if o.CleanupTimeout == 0 {
		o.CleanupTimeout = DefaultCleanupTimeout
	}
	return o, nil
}

// bucket is the driver of the tables of one schema.
type bucket struct {
	// pool reaches the database.
	pool *pgxpool.Pool
	// opts are the options with their defaults.
	opts Options
	// sql are the statements of the tables of the schema.
	sql statements
	// ownsPool reports whether the bucket opened the pool and closes it.
	ownsPool bool
}

// OpenBucket returns a bucket on the tables of opts.Schema reached through pool, which it never closes.
func OpenBucket(ctx context.Context, pool *pgxpool.Pool, opts *Options) (*blob.Bucket, error) {
	b, err := openBucket(ctx, pool, opts)
	if err != nil {
		return nil, err
	}
	return blob.NewBucket(b), nil
}

// openBucket returns the driver behind OpenBucket, once the server encoding is UTF8.
func openBucket(ctx context.Context, pool *pgxpool.Pool, opts *Options) (*bucket, error) {
	if pool == nil {
		return nil, errNoPool
	}
	var o Options
	if opts != nil {
		o = *opts
	}
	o, err := o.resolved()
	if err != nil {
		return nil, err
	}
	var encoding string
	if err := pool.QueryRow(ctx, serverEncoding).Scan(&encoding); err != nil {
		return nil, fmt.Errorf("postgresblob: read the server encoding: %w", err)
	}
	if encoding != utf8Encoding {
		return nil, fmt.Errorf("postgresblob: the database encoding is %s, the driver needs %s", encoding, utf8Encoding)
	}
	return &bucket{pool: pool, opts: o, sql: statementsOf(o.Schema)}, nil
}

// noAs is the As function of every hook, which exposes no type.
func noAs(any) bool { return false }

// runHook calls hook with noAs when hook is set, and returns its error.
func runHook(hook func(asFunc func(any) bool) error) error {
	if hook == nil {
		return nil
	}
	return hook(noAs)
}

// finished is a pending row ready to become the live object of its key.
type finished struct {
	// key is the key the row becomes the live object of.
	key string
	// id is the id of the row.
	id uuid.UUID
	// size is the size of the object in bytes.
	size int64
	// md5 is the MD5 hash of the object.
	md5 []byte
}

// lockOf returns the advisory lock key of key in schema.
func lockOf(schema, key string) int64 {
	h := fnv.New64a()
	_, _ = h.Write([]byte(schema))
	_, _ = h.Write([]byte{0})
	_, _ = h.Write([]byte(key))
	return int64(h.Sum64())
}

// lockIn takes the transaction lock of each of keys in tx, in ascending order of lock key.
func (b *bucket) lockIn(ctx context.Context, tx pgx.Tx, keys ...string) error {
	locks := make([]int64, 0, len(keys))
	for _, key := range keys {
		locks = append(locks, lockOf(b.opts.Schema, key))
	}
	slices.Sort(locks)
	for _, lock := range slices.Compact(locks) {
		if _, err := tx.Exec(ctx, lockKey, lock); err != nil {
			return err
		}
	}
	return nil
}

// checkSize returns the error for an object of size bytes past Options.MaxSize, or nil.
func (b *bucket) checkSize(size int64) error {
	if limit := b.opts.MaxSize; limit > 0 && size > limit {
		return fmt.Errorf("%w of %d bytes", errTooLarge, limit)
	}
	return nil
}

// replaceIn makes the pending row of row the live object of its key in tx, in place of any live one.
func (b *bucket) replaceIn(ctx context.Context, tx pgx.Tx, row finished) error {
	var created *time.Time
	err := tx.QueryRow(ctx, b.sql.deleteLive, []byte(row.key)).Scan(&created)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return err
	}
	_, err = tx.Exec(ctx, b.sql.markLive, row.id, row.size, row.md5, created)
	return err
}

// etagOf returns the ETag of the object row id.
func etagOf(id uuid.UUID) string {
	return `"` + hex.EncodeToString(id[:]) + `"`
}

// ErrorCode returns the code of err.
func (*bucket) ErrorCode(err error) gcerrors.ErrorCode { return errorCode(err) }

// As answers false for every type.
func (*bucket) As(any) bool { return false }

// ErrorAs sets target, a **pgconn.PgError, to the server error err holds, and reports whether it held one.
func (*bucket) ErrorAs(err error, target any) bool {
	serverErr, ok := target.(**pgconn.PgError)
	return ok && errors.As(err, serverErr)
}

// Attributes returns the attributes of the live object of key.
func (b *bucket) Attributes(ctx context.Context, key string) (*driver.Attributes, error) {
	var id uuid.UUID
	attrs := &driver.Attributes{}
	err := b.pool.QueryRow(ctx, b.sql.readAttributes, []byte(key)).Scan(&id, &attrs.Size, &attrs.ContentType,
		&attrs.CacheControl, &attrs.ContentDisposition, &attrs.ContentEncoding, &attrs.ContentLanguage,
		&attrs.Metadata, &attrs.MD5, &attrs.CreateTime, &attrs.ModTime)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, errNotFound
	}
	if err != nil {
		return nil, err
	}
	attrs.ETag = etagOf(id)
	return attrs, nil
}

// Copy copies the live object of srcKey to dstKey in one transaction, in place of any live object of dstKey.
func (b *bucket) Copy(ctx context.Context, dstKey, srcKey string, opts *driver.CopyOptions) error {
	if err := checkKey(dstKey); err != nil {
		return err
	}
	if err := runHook(opts.BeforeCopy); err != nil {
		return err
	}
	return pgx.BeginTxFunc(ctx, b.pool, pgx.TxOptions{}, func(tx pgx.Tx) error {
		return b.copyIn(ctx, tx, dstKey, srcKey)
	})
}

// copyIn copies the live object of srcKey to dstKey in tx, under the locks of both keys and within Options.MaxSize.
func (b *bucket) copyIn(ctx context.Context, tx pgx.Tx, dstKey, srcKey string) error {
	if err := b.lockIn(ctx, tx, dstKey, srcKey); err != nil {
		return err
	}
	var srcID uuid.UUID
	var size int64
	var md5 []byte
	err := tx.QueryRow(ctx, b.sql.copySource, []byte(srcKey)).Scan(&srcID, &size, &md5)
	if errors.Is(err, pgx.ErrNoRows) {
		return errNotFound
	}
	if err != nil {
		return err
	}
	if err := b.checkSize(size); err != nil {
		return err
	}
	row := finished{key: dstKey, id: uuid.Must(uuid.NewV7()), size: size, md5: md5}
	if _, err := tx.Exec(ctx, b.sql.copyObject, row.id, []byte(dstKey), srcID); err != nil {
		return err
	}
	return b.replaceIn(ctx, tx, row)
}

// Delete deletes the live object of key in one transaction.
func (b *bucket) Delete(ctx context.Context, key string, opts *driver.DeleteOptions) error {
	if err := runHook(opts.BeforeDelete); err != nil {
		return err
	}
	return pgx.BeginTxFunc(ctx, b.pool, pgx.TxOptions{}, func(tx pgx.Tx) error {
		return b.deleteIn(ctx, tx, key)
	})
}

// deleteIn deletes the live object of key in tx, under the lock of key.
func (b *bucket) deleteIn(ctx context.Context, tx pgx.Tx, key string) error {
	if err := b.lockIn(ctx, tx, key); err != nil {
		return err
	}
	tag, err := tx.Exec(ctx, b.sql.deleteLive, []byte(key))
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return errNotFound
	}
	return nil
}

// SignedURL answers Unimplemented.
func (*bucket) SignedURL(context.Context, string, *driver.SignedURLOptions) (string, error) {
	return "", errUnimplemented
}

// Close closes the pool when the bucket opened it, and leaves a pool it was given open.
func (b *bucket) Close() error {
	if b.ownsPool {
		b.pool.Close()
	}
	return nil
}
