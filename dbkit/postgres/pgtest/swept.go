// SPDX-License-Identifier: Apache-2.0

package pgtest

import (
	"sync"
	"testing"
	"time"

	"github.com/peterldowns/pgtestdb"
)

// sweepKey names the server and the scope one sweep ran for.
type sweepKey struct {
	// address is the server address the caller passed.
	address string
	// scope is the text the swept database names hold, empty for every instance.
	scope string
}

// sweeps holds the sweep of each address and scope that this test process ran.
var sweeps sync.Map

// NewSwept sweeps the stale test databases of the server at address once per test process, then answers New.
func NewSwept(t testing.TB, address string, olderThan time.Duration, migrator pgtestdb.Migrator) string {
	t.Helper()
	return newSwept(t, address, olderThan, migrator, "")
}

// newSwept is NewSwept limited to the databases whose name holds scope.
func newSwept(t testing.TB, address string, olderThan time.Duration, migrator pgtestdb.Migrator, scope string) string {
	t.Helper()
	if olderThan <= 0 {
		t.Fatalf("dbkit: NewSwept needs olderThan above zero, got %v", olderThan)
	}
	once, _ := sweeps.LoadOrStore(sweepKey{address: address, scope: scope}, new(sync.Once))
	once.(*sync.Once).Do(func() {
		if _, err := sweep(t.Context(), address, olderThan, scope); err != nil {
			t.Logf("dbkit: the sweep of stale test databases failed, the tests go on: %v", err)
		}
	})
	return New(t, address, migrator)
}
