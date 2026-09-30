// SPDX-License-Identifier: Apache-2.0

package auth

import (
	"errors"
	"testing"
)

func TestMustPanicsWithTheErrorOfAValueItCannotBuild(t *testing.T) {
	t.Parallel()

	failed := errors.New("built from a broken input")
	defer func() {
		if recovered := recover(); recovered != failed {
			t.Errorf("recovered %v, want the error", recovered)
		}
	}()

	must(0, failed)
}
