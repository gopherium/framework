// SPDX-License-Identifier: Apache-2.0

package postgresblob

import (
	"context"
	"crypto/md5"
	"hash"
	"maps"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"gocloud.dev/blob/driver"
)

// execer runs one statement, on the pool or in a transaction.
type execer interface {
	// Exec runs sql with args and returns its command tag.
	Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
}

// writer streams one object into chunk rows and makes it live on Close.
type writer struct {
	// ctx is the context of the upload.
	ctx context.Context
	// b is the bucket the object goes to.
	b *bucket
	// key is the key of the object.
	key string
	// id is the id of the row of the object.
	id uuid.UUID
	// contentType is the MIME type of the object.
	contentType string
	// opts are the write options of the object.
	opts *driver.WriterOptions
	// metadata is a copy of the metadata of the object.
	metadata map[string]string
	// buf holds the bytes of the chunk not yet written.
	buf []byte
	// seq is the number of the next chunk.
	seq int32
	// size counts the bytes written so far.
	size int64
	// md5 hashes the bytes written so far.
	md5 hash.Hash
	// pending reports whether the pending row of the object was written.
	pending bool
	// err is the first error of a write, which ends the upload.
	err error
}

// NewTypedWriter returns a writer of the object at key, after BeforeWrite.
func (b *bucket) NewTypedWriter(ctx context.Context, key, contentType string,
	opts *driver.WriterOptions) (driver.Writer, error) {
	if err := checkKey(key); err != nil {
		return nil, err
	}
	if err := runHook(opts.BeforeWrite); err != nil {
		return nil, err
	}
	metadata := make(map[string]string, len(opts.Metadata))
	maps.Copy(metadata, opts.Metadata)
	return &writer{
		ctx:         ctx,
		b:           b,
		key:         key,
		id:          uuid.Must(uuid.NewV7()),
		contentType: contentType,
		opts:        opts,
		metadata:    metadata,
		buf:         []byte{},
		md5:         md5.New(),
	}, nil
}

// Write buffers p, writing each full chunk as one row once more bytes follow it, and refuses p past the size cap.
func (w *writer) Write(p []byte) (int, error) {
	if w.err != nil {
		return 0, w.err
	}
	if w.err = w.b.checkSize(w.size + int64(len(p))); w.err != nil {
		return 0, w.err
	}
	written := 0
	for len(p) > 0 {
		if len(w.buf) == w.b.opts.ChunkSize {
			if w.err = w.flush(w.b.pool); w.err != nil {
				return written, w.err
			}
		}
		n := min(len(p), w.b.opts.ChunkSize-len(w.buf))
		w.buf = append(w.buf, p[:n]...)
		_, _ = w.md5.Write(p[:n])
		w.size += int64(n)
		written += n
		p = p[n:]
	}
	return written, nil
}

// ensurePending writes the pending row of the object through db unless it was written.
func (w *writer) ensurePending(db execer) error {
	if w.pending {
		return nil
	}
	_, err := db.Exec(w.ctx, w.b.sql.insertPending, w.id, []byte(w.key), int32(w.b.opts.ChunkSize), w.contentType,
		w.opts.CacheControl, w.opts.ContentDisposition, w.opts.ContentEncoding, w.opts.ContentLanguage, w.metadata)
	w.pending = err == nil
	return err
}

// flush writes the buffered chunk through db, after the pending row of the object.
func (w *writer) flush(db execer) error {
	if err := w.ensurePending(db); err != nil {
		return err
	}
	tag, err := db.Exec(w.ctx, w.b.sql.insertChunk, w.id, w.seq, w.buf)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return errSwept
	}
	w.seq++
	w.buf = w.buf[:0]
	return nil
}

// Close makes the object live, or deletes its pending row when the context ended or a write failed.
func (w *writer) Close() error {
	err := w.ctx.Err()
	if err == nil {
		err = w.err
	}
	if err == nil {
		err = pgx.BeginTxFunc(w.ctx, w.b.pool, pgx.TxOptions{}, w.commitIn)
	}
	if err != nil && w.pending {
		w.dropPending()
	}
	return err
}

// dropPending deletes the pending row of the object within the cleanup timeout, even once its context ended.
func (w *writer) dropPending() {
	ctx, cancel := context.WithTimeout(context.WithoutCancel(w.ctx), w.b.opts.CleanupTimeout)
	defer cancel()
	_, _ = w.b.pool.Exec(ctx, w.b.sql.deletePending, w.id)
}

// commitIn writes the last chunk in tx under the lock of the key, and makes the object live in place of any other.
func (w *writer) commitIn(tx pgx.Tx) error {
	if err := w.b.lockIn(w.ctx, tx, w.key); err != nil {
		return err
	}
	if w.opts.IfNotExist {
		var exists bool
		if err := tx.QueryRow(w.ctx, w.b.sql.liveExists, []byte(w.key)).Scan(&exists); err != nil {
			return err
		}
		if exists {
			return errExists
		}
	}
	if err := w.flush(tx); err != nil {
		return err
	}
	return w.b.replaceIn(w.ctx, tx, finished{key: w.key, id: w.id, size: w.size, md5: w.md5.Sum(nil)})
}
