// SPDX-License-Identifier: Apache-2.0

package sqlitetest_test

import (
	"testing"

	"github.com/gopherium/framework/dbkit/sqlite/sqlitetest"
)

func TestLibcMatchesThePin(t *testing.T) {
	t.Parallel()

	sqlitetest.CheckLibc(t)
}
