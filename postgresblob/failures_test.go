// SPDX-License-Identifier: Apache-2.0

package postgresblob_test

import (
	"context"
	"errors"
	"net/url"
	"slices"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"gocloud.dev/blob"
	"gocloud.dev/gcerrors"

	"github.com/gopherium/framework/postgresblob"
)

// lockNotAvailable is the SQLSTATE of a lock wait that lock_timeout ended.
const lockNotAvailable = "55P03"

// exec runs each statement on db, failing the test at the first that fails.
func (db testDB) exec(statements ...string) {
	db.t.Helper()
	for _, statement := range statements {
		if _, err := db.pool.Exec(context.Background(), statement); err != nil {
			db.t.Fatalf("%s: %v", statement, err)
		}
	}
}

// withLockTimeout returns db reached through a pool whose lock waits end after a tenth of a second.
func (db testDB) withLockTimeout() testDB {
	db.t.Helper()
	address := addressWith(db.t, db.address, url.Values{"lock_timeout": {"100ms"}})
	return testDB{t: db.t, address: address, pool: poolOn(db.t, address)}
}

// holder returns a connection to db, closed when the test ends.
func (db testDB) holder() *pgx.Conn {
	db.t.Helper()
	conn, err := pgx.Connect(context.Background(), db.address)
	if err != nil {
		db.t.Fatalf("connect to the test database: %v", err)
	}
	db.t.Cleanup(func() { _ = conn.Close(context.Background()) })
	return conn
}

// serverCode returns the SQLSTATE of the server error err holds, or nothing.
func serverCode(err error) string {
	var serverErr *pgconn.PgError
	if errors.As(err, &serverErr) {
		return serverErr.Code
	}
	return ""
}

func TestAFailingHookStopsItsCall(t *testing.T) {
	t.Parallel()
	b := freshDB(t).open(postgresblob.Options{})
	const key = "notes/report.txt"
	if err := b.WriteAll(t.Context(), key, []byte("text"), textOptions); err != nil {
		t.Fatalf("WriteAll() = %v, want nil", err)
	}
	hookErr := errors.New("the hook says no")
	hook := func(func(any) bool) error { return hookErr }
	ctx := t.Context()
	calls := map[string]func() error{
		"BeforeWrite": func() error {
			return b.WriteAll(ctx, "notes/other.txt", []byte("text"),
				&blob.WriterOptions{ContentType: "text/plain", BeforeWrite: hook})
		},
		"BeforeRead": func() error {
			_, err := b.NewReader(ctx, key, &blob.ReaderOptions{BeforeRead: hook})
			return err
		},
		"BeforeList": func() error {
			_, err := b.List(&blob.ListOptions{BeforeList: hook}).Next(ctx)
			return err
		},
		"BeforeCopy": func() error {
			return b.Copy(ctx, "notes/copy.txt", key, &blob.CopyOptions{BeforeCopy: hook})
		},
	}

	for name, call := range calls {
		if err := call(); !errors.Is(err, hookErr) {
			t.Errorf("the call with a failing %s = %v, want the error of the hook", name, err)
		}
	}
	if keys := keysOf(t, b, "notes/"); !slices.Equal(keys, []string{key}) {
		t.Errorf("the bucket holds %q after the stopped calls, want only %q", keys, key)
	}
}

func TestTheBucketExposesNoTypeThroughAs(t *testing.T) {
	t.Parallel()
	b := freshDB(t).open(postgresblob.Options{})

	var pool *pgxpool.Pool
	if b.As(&pool) {
		t.Error("As() of a pool answered true, want false")
	}
}

func TestEveryCallOnASchemaWithNoTablesFailsWithNotFound(t *testing.T) {
	t.Parallel()
	db := freshDB(t)
	b := db.open(postgresblob.Options{Schema: unmigratedSchema, ChunkSize: 4})
	ctx := t.Context()
	const key = "notes/report.txt"
	claim := &blob.WriterOptions{ContentType: "text/plain", IfNotExist: true}
	calls := map[string]func() error{
		"NewReader": func() error {
			_, err := b.NewReader(ctx, key, nil)
			return err
		},
		"Copy":                      func() error { return b.Copy(ctx, "notes/copy.txt", key, nil) },
		"Delete":                    func() error { return b.Delete(ctx, key) },
		"WriteAll of one chunk":     func() error { return b.WriteAll(ctx, key, []byte("one"), textOptions) },
		"WriteAll of many chunks":   func() error { return b.WriteAll(ctx, key, []byte("many chunks"), textOptions) },
		"WriteAll under IfNotExist": func() error { return b.WriteAll(ctx, key, []byte("one"), claim) },
	}

	for name, call := range calls {
		if code := gcerrors.Code(call()); code != gcerrors.NotFound {
			t.Errorf("%s on a schema with no tables failed with the code %v, want NotFound", name, code)
		}
	}
	_, err := postgresblob.Sweep(ctx, db.pool,
		postgresblob.SweepOptions{Schema: unmigratedSchema, OlderThan: time.Hour, Limit: 1})
	if serverCode(err) != "42P01" || !mentions(err, unmigratedSchema) {
		t.Errorf("Sweep() of a schema with no tables = %v, want the server error 42P01 and the schema named", err)
	}
}

func TestACanceledContextEndsARead(t *testing.T) {
	t.Parallel()
	b := freshDB(t).open(postgresblob.Options{})
	const key = "notes/report.txt"
	if err := b.WriteAll(t.Context(), key, []byte("text"), textOptions); err != nil {
		t.Fatalf("WriteAll() = %v, want nil", err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	r, err := b.NewReader(ctx, key, nil)
	if err != nil {
		t.Fatalf("NewReader() = %v, want a reader", err)
	}
	defer func() { _ = r.Close() }()

	cancel()
	_, err = r.Read(make([]byte, 4))

	if !errors.Is(err, context.Canceled) {
		t.Errorf("Read() after the cancel = %v, want context.Canceled", err)
	}
}

func TestACanceledContextEndsAnUploadBetweenChunks(t *testing.T) {
	t.Parallel()
	db := freshDB(t)
	b := db.open(postgresblob.Options{ChunkSize: 4})
	const key = "notes/draft.txt"
	ctx, cancel := context.WithCancel(t.Context())
	w, err := b.NewWriter(ctx, key, textOptions)
	if err != nil {
		t.Fatalf("NewWriter() = %v, want a writer", err)
	}
	if _, err := w.Write([]byte("first!")); err != nil {
		t.Fatalf("Write() of the first chunk = %v, want nil", err)
	}

	cancel()
	_, writeErr := w.Write([]byte("second"))
	closeErr := w.Close()

	if !errors.Is(writeErr, context.Canceled) || !errors.Is(closeErr, context.Canceled) {
		t.Errorf("Write() and Close() after the cancel = %v and %v, want context.Canceled", writeErr, closeErr)
	}
	if n := db.count(keyRows, []byte(key)); n != 0 {
		t.Errorf("the key holds %d rows after the canceled upload, want none", n)
	}
}

func TestOpenBucketWithACanceledContextFails(t *testing.T) {
	t.Parallel()
	pool := freshDB(t).pool
	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	b, err := postgresblob.OpenBucket(ctx, pool, nil)

	if !errors.Is(err, context.Canceled) || !mentions(err, "server encoding") {
		t.Errorf("OpenBucket() with a canceled context = %v, %v, want context.Canceled while reading the encoding", b, err)
	}
}

func TestMigrateFailsAndCreatesNothingWhenATableOfTheSchemaExists(t *testing.T) {
	t.Parallel()
	db := emptyDB(t)
	db.exec("CREATE SCHEMA archive", "CREATE TABLE archive.objects (note text)")

	err := postgresblob.Migrate(t.Context(), db.sqlDB(), "archive")

	if serverCode(err) != "42P07" || !mentions(err, "archive") {
		t.Errorf("Migrate() over an existing archive.objects = %v, want the server error 42P07 and the schema named", err)
	}
	if db.hasTable("archive.chunks") {
		t.Error("the database holds archive.chunks after the failed migration, want none")
	}
}

func TestAWriteCopyOrDeleteWaitsForTheLockOfItsKeyUntilLockTimeout(t *testing.T) {
	t.Parallel()
	db := freshDB(t)
	b := db.withLockTimeout().open(postgresblob.Options{})
	const key, source = "notes/locked.txt", "notes/source.txt"
	if err := b.WriteAll(t.Context(), source, []byte("text"), textOptions); err != nil {
		t.Fatalf("WriteAll() of the source = %v, want nil", err)
	}
	lock := postgresblob.LockOf(testSchema, key)
	if _, err := db.holder().Exec(t.Context(), "SELECT pg_advisory_lock($1)", lock); err != nil {
		t.Fatalf("hold the lock of the key: %v", err)
	}

	writeErr := b.WriteAll(t.Context(), key, []byte("text"), textOptions)
	copyErr := b.Copy(t.Context(), key, source, nil)
	deleteErr := b.Delete(t.Context(), key)

	for call, err := range map[string]error{"WriteAll()": writeErr, "Copy()": copyErr, "Delete()": deleteErr} {
		if serverCode(err) != lockNotAvailable {
			t.Errorf("%s of a locked key = %v, want the server error %s", call, err, lockNotAvailable)
		}
	}
	if n := db.count(keyRows, []byte(key)); n != 0 {
		t.Errorf("the locked key holds %d rows, want none", n)
	}
}

func TestAWriteThatCannotDeleteTheOldObjectKeepsIt(t *testing.T) {
	t.Parallel()
	db := freshDB(t)
	const key = "notes/report.txt"
	if err := db.open(postgresblob.Options{}).WriteAll(t.Context(), key, []byte("old text"), textOptions); err != nil {
		t.Fatalf("WriteAll() = %v, want nil", err)
	}
	holder, err := db.holder().Begin(t.Context())
	if err != nil {
		t.Fatalf("begin the holding transaction: %v", err)
	}
	if _, err := holder.Exec(t.Context(), "SELECT FROM files.objects WHERE key = $1 FOR UPDATE", []byte(key)); err != nil {
		t.Fatalf("lock the row of the old object: %v", err)
	}

	err = db.withLockTimeout().open(postgresblob.Options{}).WriteAll(t.Context(), key, []byte("new text"), textOptions)

	if serverCode(err) != lockNotAvailable {
		t.Errorf("WriteAll() over a row another transaction locks = %v, want the server error %s", err, lockNotAvailable)
	}
	if got, err := db.open(postgresblob.Options{}).ReadAll(t.Context(), key); err != nil || string(got) != "old text" {
		t.Errorf("ReadAll() after the failed write = %q, %v, want the old text", got, err)
	}
}
