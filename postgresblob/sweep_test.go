// SPDX-License-Identifier: Apache-2.0

package postgresblob_test

import (
	"testing"
	"time"

	"gocloud.dev/blob"
	"gocloud.dev/gcerrors"

	"github.com/gopherium/framework/postgresblob"
)

// staleAge is an age every pending row of a finished statement is older than.
const staleAge = time.Microsecond

// uploadText is the text every pending upload writes.
const uploadText = "an upload longer than one chunk"

// pendingUpload starts an upload of key on b that leaves its pending row in the database, closed when the test ends.
func pendingUpload(t *testing.T, b *blob.Bucket, key string) *blob.Writer {
	t.Helper()
	w, err := b.NewWriter(t.Context(), key, textOptions)
	if err != nil {
		t.Fatalf("NewWriter() = %v, want a writer", err)
	}
	t.Cleanup(func() { _ = w.Close() })
	if _, err := w.Write([]byte(uploadText)); err != nil {
		t.Fatalf("Write() = %v, want nil", err)
	}
	return w
}

// sweepOf returns the options of a sweep of testSchema.
func sweepOf(olderThan time.Duration, limit int) postgresblob.SweepOptions {
	return postgresblob.SweepOptions{Schema: testSchema, OlderThan: olderThan, Limit: limit}
}

// pendingAge sets how long ago the pending row of the key its first argument names was last touched.
const pendingAge = "UPDATE files.objects SET modified_at = now() - $2::interval WHERE key = $1 AND state = 'pending'"

// ageUpload makes the pending row of key on db look last touched age ago.
func (db testDB) ageUpload(key string, age time.Duration) {
	db.t.Helper()
	tag, err := db.pool.Exec(db.t.Context(), pendingAge, []byte(key), age)
	if err != nil || tag.RowsAffected() != 1 {
		db.t.Fatalf("age the upload of %s: %v, %v", key, tag, err)
	}
}

func TestSweepDeletesTheOldestUploadsFirst(t *testing.T) {
	t.Parallel()
	db := freshDB(t)
	b := db.open(postgresblob.Options{ChunkSize: 4})
	const youngest = "uploads/one-hour.txt"
	ages := map[string]time.Duration{
		"uploads/four-hours.txt":  4 * time.Hour,
		"uploads/three-hours.txt": 3 * time.Hour,
		youngest:                  time.Hour,
	}
	for key, age := range ages {
		pendingUpload(t, b, key)
		db.ageUpload(key, age)
	}

	n, err := postgresblob.Sweep(t.Context(), db.pool, sweepOf(30*time.Minute, 2))

	if n != 2 || err != nil {
		t.Errorf("Sweep() of three stale uploads with the limit 2 = %d, %v, want 2", n, err)
	}
	if left := db.count(keyRows, []byte(youngest)); left != 1 {
		t.Errorf("the youngest stale upload holds %d rows after the sweep, want its pending row", left)
	}
}

func TestSweepCountsTheAgeFromTheLastChunk(t *testing.T) {
	t.Parallel()
	db := freshDB(t)
	b := db.open(postgresblob.Options{ChunkSize: 4})
	const key = "uploads/slow.txt"
	w := pendingUpload(t, b, key)
	db.ageUpload(key, time.Hour)
	if _, err := w.Write([]byte("one more chunk of text")); err != nil {
		t.Fatalf("Write() of one more chunk = %v, want nil", err)
	}

	n, err := postgresblob.Sweep(t.Context(), db.pool, sweepOf(30*time.Minute, 10))

	if n != 0 || err != nil {
		t.Errorf("Sweep() of an upload that wrote a chunk just now = %d, %v, want 0", n, err)
	}
}

func TestSweepDeletesStaleUploadsLimitAtATime(t *testing.T) {
	t.Parallel()
	db := freshDB(t)
	b := db.open(postgresblob.Options{ChunkSize: 4})
	for _, key := range []string{"uploads/a.txt", "uploads/b.txt", "uploads/c.txt"} {
		pendingUpload(t, b, key)
	}

	n, err := postgresblob.Sweep(t.Context(), db.pool, sweepOf(staleAge, 2))

	if n != 2 || err != nil {
		t.Errorf("Sweep() of three stale uploads with the limit 2 = %d, %v, want 2", n, err)
	}
	if left := db.count(pendingRows); left != 1 {
		t.Errorf("%d pending rows stay after the sweep, want 1", left)
	}
}

func TestSweepSparesFreshUploads(t *testing.T) {
	t.Parallel()
	db := freshDB(t)
	b := db.open(postgresblob.Options{ChunkSize: 4})
	const key = "uploads/fresh.txt"
	w := pendingUpload(t, b, key)

	n, err := postgresblob.Sweep(t.Context(), db.pool, sweepOf(time.Hour, 10))

	if n != 0 || err != nil {
		t.Errorf("Sweep() of a fresh upload = %d, %v, want 0", n, err)
	}
	if err := w.Close(); err != nil {
		t.Errorf("Close() of the spared upload = %v, want nil", err)
	}
	expectContent(t, b, key, []byte(uploadText))
}

func TestTheWriterOfASweptUploadFailsAndLeavesNoObject(t *testing.T) {
	t.Parallel()
	db := freshDB(t)
	b := db.open(postgresblob.Options{ChunkSize: 4})
	const key = "uploads/stale.txt"
	w := pendingUpload(t, b, key)
	if n, err := postgresblob.Sweep(t.Context(), db.pool, sweepOf(staleAge, 10)); n != 1 || err != nil {
		t.Fatalf("Sweep() of a stale upload = %d, %v, want 1", n, err)
	}

	err := w.Close()

	expectCode(t, "Close() of a swept upload", err, gcerrors.FailedPrecondition)
	if n := db.count(keyRows, []byte(key)); n != 0 {
		t.Errorf("the key holds %d rows after the swept upload closed, want none", n)
	}
}

func TestSweepRefusesAnAgeAtOrBelowZeroAndALimitBelowOne(t *testing.T) {
	t.Parallel()
	db := freshDB(t)
	tests := map[string]struct {
		opts postgresblob.SweepOptions
		want string
	}{
		"a zero age":     {opts: sweepOf(0, 1), want: "OlderThan"},
		"a negative age": {opts: sweepOf(-time.Second, 1), want: "OlderThan"},
		"a zero limit":   {opts: sweepOf(time.Hour, 0), want: "Limit"},
	}

	for name, tt := range tests {
		n, err := postgresblob.Sweep(t.Context(), db.pool, tt.opts)
		if n != 0 || !mentions(err, tt.want) {
			t.Errorf("Sweep() with %s = %d, %v, want an error that names %s", name, n, err, tt.want)
		}
	}
}
