// SPDX-License-Identifier: Apache-2.0

package postgresblob_test

import (
	"bytes"
	"strconv"
	"testing"

	"gocloud.dev/gcerrors"

	"github.com/gopherium/framework/postgresblob"
)

// digits returns size bytes of the ten digits in a loop.
func digits(size int) []byte {
	return bytes.Repeat([]byte("0123456789"), size/10+1)[:size]
}

func TestOneReadFetchesAtMostOneChunkOrTheDefaultChunkSize(t *testing.T) {
	t.Parallel()
	db := freshDB(t)
	tests := map[string]struct {
		chunkSize int
		want      int
	}{
		"chunks smaller than the default": {chunkSize: 4096, want: postgresblob.DefaultChunkSize},
		"chunks larger than the default": {
			chunkSize: 2 * postgresblob.DefaultChunkSize,
			want:      2 * postgresblob.DefaultChunkSize,
		},
	}

	for name, tt := range tests {
		b := db.open(postgresblob.Options{ChunkSize: tt.chunkSize})
		key := "large/" + strconv.Itoa(tt.chunkSize) + ".txt"
		content := digits(tt.want + 10)
		if err := b.WriteAll(t.Context(), key, content, textOptions); err != nil {
			t.Fatalf("WriteAll() of %d bytes = %v, want nil", len(content), err)
		}
		r, err := b.NewReader(t.Context(), key, nil)
		if err != nil {
			t.Fatalf("NewReader() = %v, want a reader", err)
		}
		buf := make([]byte, 2*len(content))

		n, err := r.Read(buf)

		_ = r.Close()
		if n != tt.want || err != nil {
			t.Errorf("Read() into %d bytes with %s = %d bytes, %v, want %d bytes", len(buf), name, n, err, tt.want)
		}
		if !bytes.Equal(buf[:n], content[:n]) {
			t.Errorf("Read() with %s filled %d bytes that differ from the start of the object", name, n)
		}
	}
}

func TestAReadOfAReplacedObjectFails(t *testing.T) {
	t.Parallel()
	b := freshDB(t).open(postgresblob.Options{ChunkSize: 4})
	const key = "notes/report.txt"
	if err := b.WriteAll(t.Context(), key, []byte("the first version"), textOptions); err != nil {
		t.Fatalf("WriteAll() = %v, want nil", err)
	}
	r, err := b.NewReader(t.Context(), key, nil)
	if err != nil {
		t.Fatalf("NewReader() = %v, want a reader", err)
	}
	defer func() { _ = r.Close() }()
	start := make([]byte, 4)
	if n, err := r.Read(start); n != 4 || err != nil {
		t.Fatalf("Read() of the first chunk = %d, %v, want 4 bytes", n, err)
	}
	if err := b.WriteAll(t.Context(), key, []byte("the second version"), textOptions); err != nil {
		t.Fatalf("WriteAll() of the replacement = %v, want nil", err)
	}

	n, err := r.Read(make([]byte, 64))

	if n != 0 || gcerrors.Code(err) != gcerrors.FailedPrecondition {
		t.Errorf("Read() after the replacement = %d, %v, want no bytes and FailedPrecondition", n, err)
	}
}
