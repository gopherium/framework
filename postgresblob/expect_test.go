// SPDX-License-Identifier: Apache-2.0

package postgresblob_test

import (
	"bytes"
	"context"
	"slices"
	"strings"
	"testing"

	"gocloud.dev/blob"
	"gocloud.dev/gcerrors"
)

// mentions reports whether err is set and its message holds every one of words.
func mentions(err error, words ...string) bool {
	return err != nil && !slices.ContainsFunc(words, lacks(err))
}

// mentionsAny reports whether err is set and its message holds one of words at least.
func mentionsAny(err error, words ...string) bool {
	return err != nil && slices.ContainsFunc(words, func(word string) bool { return !lacks(err)(word) })
}

// lacks returns a test of whether the message of err lacks a word.
func lacks(err error) func(word string) bool {
	return func(word string) bool { return !strings.Contains(err.Error(), word) }
}

// expectCode fails the test unless err, returned by call, carries the code want.
func expectCode(t *testing.T, call string, err error, want gcerrors.ErrorCode) {
	t.Helper()
	if code := gcerrors.Code(err); code != want {
		t.Errorf("%s = %v, the code %v, want %v", call, err, code, want)
	}
}

// expectContent fails the test unless the live object of key on b holds want.
func expectContent(t *testing.T, b *blob.Bucket, key string, want []byte) {
	t.Helper()
	got, err := b.ReadAll(context.Background(), key)
	if err != nil || !bytes.Equal(got, want) {
		t.Errorf("ReadAll(%q) = %q, %v, want %q", key, got, err, want)
	}
}
