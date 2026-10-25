// SPDX-License-Identifier: Apache-2.0

package postgresblob_test

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"gocloud.dev/blob"
	"gocloud.dev/gcerrors"

	"github.com/gopherium/framework/postgresblob"
)

const (
	// concurrentWriters is how many writers race for one key.
	concurrentWriters = 8
	// lockWaitBound is how long a test waits for sessions to queue on a lock.
	lockWaitBound = 10 * time.Second
	// lockWaits counts the sessions of the current database that wait on a lock.
	lockWaits = "SELECT count(*) FROM pg_stat_activity WHERE datname = current_database() AND wait_event_type = 'Lock'"
	// liveRowLock locks for update the live rows of the keys its one argument lists.
	liveRowLock = "SELECT FROM files.objects WHERE key = ANY($1) AND state = 'live' FOR UPDATE"
	// firstVersion is the text a key holds before a replace.
	firstVersion = "the first version"
	// secondVersion is the text a replace writes.
	secondVersion = "the second version"
)

// awaitLockWaits fails the test unless at least n sessions of db wait on a lock within lockWaitBound.
func (db testDB) awaitLockWaits(n int) {
	db.t.Helper()
	deadline := time.Now().Add(lockWaitBound)
	for db.count(lockWaits) < n {
		if time.Now().After(deadline) {
			db.t.Fatalf("fewer than %d sessions wait on a lock after %v", n, lockWaitBound)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// holdRows begins a transaction on a new connection that locks the live rows of keys for update.
func (db testDB) holdRows(keys ...string) pgx.Tx {
	db.t.Helper()
	tx, err := db.holder().Begin(db.t.Context())
	if err != nil {
		db.t.Fatalf("begin the holding transaction: %v", err)
	}
	raw := make([][]byte, len(keys))
	for i, key := range keys {
		raw[i] = []byte(key)
	}
	tag, err := tx.Exec(db.t.Context(), liveRowLock, raw)
	if err != nil || tag.RowsAffected() != int64(len(keys)) {
		db.t.Fatalf("lock the live rows of %q: %v, %v", keys, tag, err)
	}
	return tx
}

// release rolls back the holding transaction tx, failing the test when it cannot.
func (db testDB) release(tx pgx.Tx) {
	db.t.Helper()
	if err := tx.Rollback(db.t.Context()); err != nil {
		db.t.Fatalf("roll back the holding transaction: %v", err)
	}
}

// writeTexts writes each text of texts under its key on b, failing the test at the first write that fails.
func writeTexts(t *testing.T, b *blob.Bucket, texts map[string]string) {
	t.Helper()
	for key, text := range texts {
		if err := b.WriteAll(t.Context(), key, []byte(text), textOptions); err != nil {
			t.Fatalf("WriteAll(%q) = %v, want nil", key, err)
		}
	}
}

// duringAReplace returns the error of call, run while a write of secondVersion waits to replace firstVersion at key.
func (db testDB) duringAReplace(b *blob.Bucket, key string, call func() error) error {
	db.t.Helper()
	writeTexts(db.t, b, map[string]string{key: firstVersion})
	holder := db.holdRows(key)
	writeErr := make(chan error, 1)
	go func() { writeErr <- b.WriteAll(db.t.Context(), key, []byte(secondVersion), textOptions) }()
	db.awaitLockWaits(1)
	callErr := make(chan error, 1)
	go func() { callErr <- call() }()
	db.awaitLockWaits(2)
	db.release(holder)
	if err := <-writeErr; err != nil {
		db.t.Fatalf("WriteAll() of the second version = %v, want nil", err)
	}
	return <-callErr
}

// raceWrites runs concurrentWriters writes of key on b at once with opts, and returns their errors.
func raceWrites(b *blob.Bucket, key string, opts *blob.WriterOptions) []error {
	errs := make([]error, concurrentWriters)
	var wg sync.WaitGroup
	for i := range concurrentWriters {
		wg.Go(func() {
			errs[i] = b.WriteAll(context.Background(), key, fmt.Appendf(nil, "version %d", i), opts)
		})
	}
	wg.Wait()
	return errs
}

func TestConcurrentWritesToOneKeyAllSucceedAndLeaveOneObject(t *testing.T) {
	t.Parallel()
	db := freshDB(t)
	b := db.open(postgresblob.Options{})
	const key = "notes/shared.txt"

	errs := raceWrites(b, key, textOptions)

	for i, err := range errs {
		if err != nil {
			t.Errorf("concurrent WriteAll() %d = %v, want nil", i, err)
		}
	}
	if n := db.count(liveRows, []byte(key)); n != 1 {
		t.Errorf("the key holds %d live rows after the race, want 1", n)
	}
}

func TestConcurrentWritesUnderIfNotExistLetExactlyOneWin(t *testing.T) {
	t.Parallel()
	b := freshDB(t).open(postgresblob.Options{})
	opts := &blob.WriterOptions{ContentType: "text/plain", IfNotExist: true}

	errs := raceWrites(b, "notes/claimed.txt", opts)

	wins := 0
	for i, err := range errs {
		switch code := gcerrors.Code(err); code {
		case gcerrors.OK:
			wins++
		case gcerrors.FailedPrecondition:
		default:
			t.Errorf("concurrent WriteAll() %d under IfNotExist = %v, the code %v, want nil or FailedPrecondition",
				i, err, code)
		}
	}
	if wins != 1 {
		t.Errorf("%d concurrent writes under IfNotExist succeeded, want exactly 1", wins)
	}
}

func TestCopiesThatSwapTwoKeysBothSucceed(t *testing.T) {
	t.Parallel()
	db := freshDB(t)
	b := db.open(postgresblob.Options{})
	const first, second = "swap/first.txt", "swap/second.txt"
	writeTexts(t, b, map[string]string{first: "the first text", second: "the second text"})
	holder := db.holdRows(first, second)
	errs := make(chan error, 2)
	go func() { errs <- b.Copy(t.Context(), second, first, nil) }()
	go func() { errs <- b.Copy(t.Context(), first, second, nil) }()
	db.awaitLockWaits(2)

	db.release(holder)

	for range 2 {
		if err := <-errs; err != nil {
			t.Errorf("Copy() in a swap of two keys = %v, the code %v, want nil", err, gcerrors.Code(err))
		}
	}
	firstText, err := b.ReadAll(t.Context(), first)
	if err != nil {
		t.Fatalf("ReadAll(%q) after the swap = %v, want the text", first, err)
	}
	expectContent(t, b, second, firstText)
}

func TestACopyDuringAReplaceOfItsSourceCopiesTheNewObject(t *testing.T) {
	t.Parallel()
	db := freshDB(t)
	b := db.open(postgresblob.Options{})
	const source, destination = "race/source.txt", "race/destination.txt"

	err := db.duringAReplace(b, source, func() error { return b.Copy(t.Context(), destination, source, nil) })

	if err != nil {
		t.Errorf("Copy() of a source that held an object throughout = %v, the code %v, want nil", err, gcerrors.Code(err))
	}
	expectContent(t, b, destination, []byte(secondVersion))
}

func TestADeleteDuringAReplaceDeletesTheNewObject(t *testing.T) {
	t.Parallel()
	db := freshDB(t)
	b := db.open(postgresblob.Options{})
	const key = "race/deleted.txt"

	err := db.duringAReplace(b, key, func() error { return b.Delete(t.Context(), key) })

	if err != nil {
		t.Errorf("Delete() of a key that held an object throughout = %v, the code %v, want nil", err, gcerrors.Code(err))
	}
	if n := db.count(keyRows, []byte(key)); n != 0 {
		t.Errorf("the key holds %d rows after the delete, want none", n)
	}
}
