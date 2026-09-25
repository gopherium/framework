// SPDX-License-Identifier: Apache-2.0

package auth

import (
	"errors"
	"fmt"
	"testing"

	"github.com/gopherium/gouncer"
)

func TestUncoveredWordsOnlyTheLastPrivilegedRefusal(t *testing.T) {
	t.Parallel()

	failed := errors.New("postgres: set user role: connection reset")

	last := uncovered("admin@example.com", fmt.Errorf("postgres: %w", gouncer.ErrLastPrivileged))
	other := uncovered("admin@example.com", failed)

	if want := "admin@example.com is the last enabled privileged account"; last == nil || last.Error() != want {
		t.Errorf("uncovered() = %v, want %q", last, want)
	}
	if !errors.Is(other, failed) {
		t.Errorf("uncovered() = %v, want the other failure unchanged", other)
	}
}
