// SPDX-License-Identifier: Apache-2.0

package postgresblob

import (
	"context"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"gocloud.dev/blob/driver"
)

// DefaultPageSize is the page size of a listing that sets none.
const DefaultPageSize = 1000

// lastByte is the highest byte value, which no UTF-8 key holds.
const lastByte = 0xFF

// listing is one ListPaged call in progress.
type listing struct {
	// b is the bucket being listed.
	b *bucket
	// prefix starts every key the listing answers.
	prefix []byte
	// upper is the bound every key under prefix stays below.
	upper []byte
	// delimiter ends a directory, and an empty delimiter lists every key flat.
	delimiter string
	// pageSize is the most objects the page holds.
	pageSize int
	// from is the bound the next key starts at or after.
	from []byte
	// page is the page so far.
	page driver.ListPage
}

// ListPaged returns one page of the live objects under the prefix of opts, in byte order, after BeforeList.
func (b *bucket) ListPaged(ctx context.Context, opts *driver.ListOptions) (*driver.ListPage, error) {
	if err := runHook(opts.BeforeList); err != nil {
		return nil, err
	}
	l := &listing{
		b:         b,
		prefix:    append([]byte{}, opts.Prefix...),
		upper:     append([]byte(opts.Prefix), lastByte),
		delimiter: opts.Delimiter,
		pageSize:  opts.PageSize,
		from:      opts.PageToken,
	}
	if l.pageSize == 0 {
		l.pageSize = DefaultPageSize
	}
	return l.run(ctx)
}

// run queries until the page is full or the prefix holds no more keys, and returns the page.
func (l *listing) run(ctx context.Context) (*driver.ListPage, error) {
	again := true
	for again {
		var err error
		if again, err = l.query(ctx); err != nil {
			return nil, err
		}
	}
	return &l.page, nil
}

// listRow is one row a listing reads.
type listRow struct {
	// Key is the key of the object.
	Key []byte
	// Size is the size of the object in bytes.
	Size int64
	// MD5 is the MD5 hash of the object.
	MD5 []byte
	// ModTime is the time the object was last written.
	ModTime time.Time
}

// query reads the keys from l.from, and reports whether it stopped at a directory.
func (l *listing) query(ctx context.Context) (bool, error) {
	rows, _ := l.b.pool.Query(ctx, l.b.sql.listLive, l.prefix, l.from, l.upper, l.pageSize-len(l.page.Objects)+1)
	found, err := pgx.CollectRows(rows, pgx.RowToStructByPos[listRow])
	if err != nil {
		return false, err
	}
	for _, row := range found {
		if len(l.page.Objects) == l.pageSize {
			l.page.NextPageToken = l.from
			return false, nil
		}
		obj := &driver.ListObject{Key: string(row.Key), ModTime: row.ModTime, Size: row.Size, MD5: row.MD5}
		next := append(row.Key, 0)
		if dir, isDir := l.dirOf(obj.Key); isDir {
			obj = &driver.ListObject{Key: dir, IsDir: true}
			next = append([]byte(dir), lastByte)
		}
		l.page.Objects = append(l.page.Objects, obj)
		l.from = next
		if obj.IsDir {
			return true, nil
		}
	}
	return false, nil
}

// dirOf returns the directory key holds under the prefix, and reports whether it is in one.
func (l *listing) dirOf(key string) (string, bool) {
	if l.delimiter == "" {
		return "", false
	}
	i := strings.Index(key[len(l.prefix):], l.delimiter)
	if i < 0 {
		return "", false
	}
	return key[:len(l.prefix)+i+len(l.delimiter)], true
}
