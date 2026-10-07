// SPDX-License-Identifier: Apache-2.0

package dbkit_test

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/gopherium/framework/dbkit"
)

// basicClasses lists every error class the root module defines.
var basicClasses = []error{dbkit.ErrBusy, dbkit.ErrUnique, dbkit.ErrForeignKey, dbkit.ErrNotNull, dbkit.ErrCheck}

func TestErrorClassesAreDistinct(t *testing.T) {
	t.Parallel()

	messages := make(map[string]bool, len(basicClasses))
	for i, class := range basicClasses {
		if class == nil {
			t.Fatalf("class %d is nil", i)
		}
		if !strings.HasPrefix(class.Error(), "dbkit: ") {
			t.Errorf("class %d reads %q, want it to start with dbkit: ", i, class)
		}
		if messages[class.Error()] {
			t.Errorf("class %d repeats the message %q", i, class)
		}
		messages[class.Error()] = true
		for j, other := range basicClasses {
			if i != j && errors.Is(class, other) {
				t.Errorf("class %d matches class %d, want no match", i, j)
			}
		}
	}
}

func TestAClassWrapsTheOriginal(t *testing.T) {
	t.Parallel()

	original := errors.New("engine error 2067")
	for i, class := range basicClasses {
		wrapped := fmt.Errorf("%w: %w", class, original)
		if !errors.Is(wrapped, class) {
			t.Errorf("class %d: %v does not match its class", i, wrapped)
		}
		if !errors.Is(wrapped, original) {
			t.Errorf("class %d: %v does not match the original", i, wrapped)
		}
		for j, other := range basicClasses {
			if i != j && errors.Is(wrapped, other) {
				t.Errorf("class %d: %v matches class %d, want no match", i, wrapped, j)
			}
		}
	}
}
