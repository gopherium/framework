// SPDX-License-Identifier: Apache-2.0

package postgresblob

import (
	"context"
	"testing"

	"gocloud.dev/blob/driver"
)

func TestASmallWriteHoldsNoMoreThanItsBytesNeed(t *testing.T) {
	t.Parallel()

	b := &bucket{opts: Options{ChunkSize: DefaultChunkSize}}
	w, err := b.NewTypedWriter(context.Background(), "notes/small.txt", "text/plain", &driver.WriterOptions{})
	if err != nil {
		t.Fatalf("NewTypedWriter() = %v, want a writer", err)
	}
	if _, err := w.Write([]byte("hello")); err != nil {
		t.Fatalf("Write() = %v, want nil", err)
	}

	if held := cap(w.(*writer).buf); held >= DefaultChunkSize {
		t.Errorf("a 5 byte write holds a buffer of %d bytes, want less than the chunk size %d", held, DefaultChunkSize)
	}
}
