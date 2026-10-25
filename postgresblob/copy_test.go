// SPDX-License-Identifier: Apache-2.0

package postgresblob_test

import (
	"testing"

	"gocloud.dev/blob"
	"gocloud.dev/gcerrors"

	"github.com/gopherium/framework/postgresblob"
)

func TestCopyOntoItselfKeepsTheObject(t *testing.T) {
	t.Parallel()
	db := freshDB(t)
	b := db.open(postgresblob.Options{ChunkSize: 4})
	const key = "notes/report.txt"
	content := []byte("a report longer than one chunk")
	opts := &blob.WriterOptions{ContentType: "text/plain", Metadata: map[string]string{"author": "Ada Lovelace"}}
	if err := b.WriteAll(t.Context(), key, content, opts); err != nil {
		t.Fatalf("WriteAll() = %v, want nil", err)
	}

	if err := b.Copy(t.Context(), key, key, nil); err != nil {
		t.Fatalf("Copy() of a key onto itself = %v, want nil", err)
	}

	expectContent(t, b, key, content)
	attrs, err := b.Attributes(t.Context(), key)
	if err != nil {
		t.Fatalf("Attributes() after the copy = %v, want the attributes", err)
	}
	if attrs.Metadata["author"] != "Ada Lovelace" {
		t.Errorf("the metadata after the copy = %v, want the author kept", attrs.Metadata)
	}
	if attrs.ContentType != "text/plain" {
		t.Errorf("the content type after the copy = %q, want text/plain", attrs.ContentType)
	}
	if n := db.count(liveRows, []byte(key)); n != 1 {
		t.Errorf("the key holds %d live rows after the copy, want 1", n)
	}
}

func TestACopyTheRoleMayNotWriteFailsWithPermissionDeniedAndKeepsTheDestination(t *testing.T) {
	t.Parallel()
	db := freshDB(t)
	b := db.open(postgresblob.Options{})
	const source, destination = "notes/source.txt", "notes/destination.txt"
	for key, text := range map[string]string{source: "the source", destination: "the destination"} {
		if err := b.WriteAll(t.Context(), key, []byte(text), textOptions); err != nil {
			t.Fatalf("WriteAll(%q) = %v, want nil", key, err)
		}
	}
	db.exec("REVOKE INSERT ON files.chunks FROM CURRENT_USER")

	err := b.Copy(t.Context(), destination, source, nil)

	expectCode(t, "Copy() by a role that may not insert chunks", err, gcerrors.PermissionDenied)
	expectContent(t, b, destination, []byte("the destination"))
}

func TestACopyPastTheCapFailsAndKeepsTheDestination(t *testing.T) {
	t.Parallel()
	db := freshDB(t)
	const large, small, destination = "notes/large.txt", "notes/small.txt", "notes/destination.txt"
	writeTexts(t, db.open(postgresblob.Options{}),
		map[string]string{large: "a text past the cap", small: "four", destination: "old"})
	capped := db.open(postgresblob.Options{MaxSize: 4})

	pastErr := capped.Copy(t.Context(), destination, large, nil)

	expectCode(t, "Copy() of 19 bytes through a cap of 4", pastErr, gcerrors.ResourceExhausted)
	expectContent(t, capped, destination, []byte("old"))
	if n := db.count(pendingRows); n != 0 {
		t.Errorf("%d pending rows stay after the copy past the cap, want none", n)
	}
	if err := capped.Copy(t.Context(), destination, small, nil); err != nil {
		t.Errorf("Copy() of exactly the cap = %v, want nil", err)
	}
}
