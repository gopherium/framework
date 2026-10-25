// SPDX-License-Identifier: Apache-2.0

package postgresblob_test

import (
	"slices"
	"testing"

	"github.com/gopherium/framework/postgresblob"
)

func TestListFollowsByteOrderAcrossATwoByteRune(t *testing.T) {
	t.Parallel()
	b := freshDB(t).open(postgresblob.Options{})
	want := []string{"names/e", "names/z", "names/é", "names/\U0010FFFF"}

	for _, key := range []string{"names/é", "names/\U0010FFFF", "names/z", "names/e"} {
		if err := b.WriteAll(t.Context(), key, []byte("text"), nil); err != nil {
			t.Fatalf("WriteAll(%q) = %v, want nil", key, err)
		}
	}
	got := keysOf(t, b, "names/")

	if !slices.Equal(got, want) {
		t.Errorf("List() keys = %q, want the byte order %q", got, want)
	}
}
