// SPDX-License-Identifier: Apache-2.0

package postgresblob_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"gocloud.dev/blob"
	"gocloud.dev/gcerrors"

	"github.com/gopherium/framework/postgresblob"
)

// textOptions are the options of a plain text write.
var textOptions = &blob.WriterOptions{ContentType: "text/plain"}

func TestACanceledWriteLeavesNoPendingRow(t *testing.T) {
	t.Parallel()
	db := freshDB(t)
	b := db.open(postgresblob.Options{ChunkSize: 4})
	const key = "notes/draft.txt"
	ctx, cancel := context.WithCancel(t.Context())
	w, err := b.NewWriter(ctx, key, textOptions)
	if err != nil {
		t.Fatalf("NewWriter() = %v, want a writer", err)
	}
	if _, err := w.Write([]byte("a draft longer than one chunk")); err != nil {
		t.Fatalf("Write() = %v, want nil", err)
	}
	if n := db.count(keyRows, []byte(key)); n != 1 {
		t.Fatalf("the key holds %d rows during the upload, want its pending row", n)
	}

	cancel()
	err = w.Close()

	if !errors.Is(err, context.Canceled) {
		t.Errorf("Close() after the cancel = %v, want context.Canceled", err)
	}
	if n := db.count(keyRows, []byte(key)); n != 0 {
		t.Errorf("the key holds %d rows after the canceled upload, want none", n)
	}
}

func TestAKeyOverTheByteLimitIsRefused(t *testing.T) {
	t.Parallel()
	b := freshDB(t).open(postgresblob.Options{})
	longest := strings.Repeat("k", postgresblob.MaxKeyBytes)
	if err := b.WriteAll(t.Context(), longest, []byte("text"), textOptions); err != nil {
		t.Fatalf("WriteAll() with a key of %d bytes = %v, want nil", len(longest), err)
	}

	writeErr := b.WriteAll(t.Context(), longest+"k", []byte("text"), textOptions)
	copyErr := b.Copy(t.Context(), longest+"k", longest, nil)

	expectCode(t, "WriteAll() with a key one byte past the limit", writeErr, gcerrors.InvalidArgument)
	expectCode(t, "Copy() to a key one byte past the limit", copyErr, gcerrors.InvalidArgument)
}

func TestAWritePastTheCapFailsAndKeepsTheOldObject(t *testing.T) {
	t.Parallel()
	db := freshDB(t)
	b := db.open(postgresblob.Options{ChunkSize: 4, MaxSize: 8})
	const key = "notes/short.txt"
	old := []byte("old text")
	if err := b.WriteAll(t.Context(), key, old, textOptions); err != nil {
		t.Fatalf("WriteAll() of exactly the cap = %v, want nil", err)
	}
	w, err := b.NewWriter(t.Context(), key, textOptions)
	if err != nil {
		t.Fatalf("NewWriter() = %v, want a writer", err)
	}

	_, writeErr := w.Write([]byte("new te"))
	if writeErr == nil {
		_, writeErr = w.Write([]byte("xt past"))
	}
	_, againErr := w.Write([]byte("x"))
	closeErr := w.Close()

	expectCode(t, "Write() past the cap", writeErr, gcerrors.ResourceExhausted)
	expectCode(t, "Write() after the refused write", againErr, gcerrors.ResourceExhausted)
	expectCode(t, "Close() after a write past the cap", closeErr, gcerrors.ResourceExhausted)
	expectContent(t, b, key, old)
	if n := db.count(pendingRows); n != 0 {
		t.Errorf("%d pending rows stay after the refused write, want none", n)
	}
}

// earlierCreateTime moves the create time of the live object of the key its one argument names back one day.
const earlierCreateTime = "UPDATE files.objects SET created_at = created_at - interval '24 hours' " +
	"WHERE key = $1 AND state = 'live'"

func TestAnOverwriteKeepsTheCreateTimeOfTheFirstWrite(t *testing.T) {
	t.Parallel()
	db := freshDB(t)
	b := db.open(postgresblob.Options{})
	const key = "notes/report.txt"
	if err := b.WriteAll(t.Context(), key, []byte("old text"), textOptions); err != nil {
		t.Fatalf("WriteAll() = %v, want nil", err)
	}
	if _, err := db.pool.Exec(t.Context(), earlierCreateTime, []byte(key)); err != nil {
		t.Fatalf("%s: %v", earlierCreateTime, err)
	}
	before, err := b.Attributes(t.Context(), key)
	if err != nil {
		t.Fatalf("Attributes() before the overwrite = %v, want the attributes", err)
	}

	if err := b.WriteAll(t.Context(), key, []byte("new text"), textOptions); err != nil {
		t.Fatalf("WriteAll() of the overwrite = %v, want nil", err)
	}

	after, err := b.Attributes(t.Context(), key)
	if err != nil {
		t.Fatalf("Attributes() after the overwrite = %v, want the attributes", err)
	}
	if !after.CreateTime.Equal(before.CreateTime) {
		t.Errorf("CreateTime after the overwrite = %v, want the first write's %v", after.CreateTime, before.CreateTime)
	}
}
