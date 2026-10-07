// SPDX-License-Identifier: Apache-2.0

package sqlite

import (
	"errors"
	"path/filepath"
	"testing"

	modernc "modernc.org/sqlite"
)

func TestOFDAnswersThatKeepOpenSafe(t *testing.T) {
	t.Parallel()

	for _, answer := range []error{nil, modernc.ErrOFDLockingUnavailable} {
		if err := ofdError(answer); err != nil {
			t.Errorf("ofdError(%v) = %v, want nil", answer, err)
		}
	}
}

func TestOpenFailsWhenOFDCameTooLate(t *testing.T) {
	path := filepath.Join(t.TempDir(), "site.db")
	tooLate := ofdError(modernc.ErrOFDLockingTooLate)
	switched := ofdOnce
	ofdOnce = func() error { return tooLate }
	t.Cleanup(func() { ofdOnce = switched })

	db, err := Open("sqlite:"+path, internalOptions())

	if !errors.Is(err, modernc.ErrOFDLockingTooLate) || db != nil {
		t.Errorf("Open() = %v, %v, want nil and the too late error", db, err)
	}
}

func TestOFDTooLateFailsTheOpen(t *testing.T) {
	t.Parallel()

	err := ofdError(modernc.ErrOFDLockingTooLate)

	want := "dbkit: SQLite locked a database file before Open could switch on OFD locks: " +
		modernc.ErrOFDLockingTooLate.Error()
	if !errors.Is(err, modernc.ErrOFDLockingTooLate) || err.Error() != want {
		t.Errorf("ofdError() = %v, want %q", err, want)
	}
}
