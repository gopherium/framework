// SPDX-License-Identifier: Apache-2.0

package postgresblob

import (
	"context"
	"errors"
	"io"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"gocloud.dev/blob/driver"
)

// reader reads a range of one object, one statement per Read.
type reader struct {
	// ctx is the context of every read.
	ctx context.Context
	// b is the bucket the object lives in.
	b *bucket
	// id is the id of the row of the object.
	id uuid.UUID
	// chunkSize is the chunk size the object was written with.
	chunkSize int64
	// perRead is the most bytes one Read fetches, the chunk size or DefaultChunkSize when that is larger.
	perRead int64
	// pos is the offset of the next byte to read.
	pos int64
	// end is the offset after the last byte to read.
	end int64
	// attrs are the attributes of the object.
	attrs driver.ReaderAttributes
}

// NewRangeReader returns a reader of at most length bytes of the live object of key from offset, after BeforeRead.
func (b *bucket) NewRangeReader(ctx context.Context, key string, offset, length int64,
	opts *driver.ReaderOptions) (driver.Reader, error) {
	r := &reader{ctx: ctx, b: b}
	err := b.pool.QueryRow(ctx, b.sql.readObject, []byte(key)).Scan(&r.id, &r.attrs.Size, &r.chunkSize,
		&r.attrs.ContentType, &r.attrs.ModTime)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, errNotFound
	}
	if err != nil {
		return nil, err
	}
	if err := runHook(opts.BeforeRead); err != nil {
		return nil, err
	}
	r.perRead = max(r.chunkSize, DefaultChunkSize)
	r.pos = min(offset, r.attrs.Size)
	r.end = r.attrs.Size
	if length >= 0 && length < r.end-r.pos {
		r.end = r.pos + length
	}
	return r, nil
}

// Read fills p with at most perRead of the next bytes of the range, from the chunks that hold them.
func (r *reader) Read(p []byte) (int, error) {
	if r.pos >= r.end {
		return 0, io.EOF
	}
	stop := r.pos + min(int64(len(p)), r.end-r.pos, r.perRead)
	var data []byte
	if err := r.b.pool.QueryRow(r.ctx, r.b.sql.readRange, r.id, r.pos, stop, r.chunkSize).Scan(&data); err != nil {
		return 0, err
	}
	if int64(len(data)) < stop-r.pos {
		return 0, errReplaced
	}
	copied := copy(p, data)
	r.pos += int64(copied)
	return copied, nil
}

// Close closes the reader, which holds no connection.
func (*reader) Close() error { return nil }

// Attributes returns the attributes of the object.
func (r *reader) Attributes() *driver.ReaderAttributes { return &r.attrs }

// As answers false for every type.
func (*reader) As(any) bool { return false }
