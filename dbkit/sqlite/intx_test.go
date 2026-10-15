// SPDX-License-Identifier: Apache-2.0

package sqlite_test

import (
	"database/sql"
	"errors"
	"testing"

	"github.com/gopherium/framework/dbkit"
	"github.com/gopherium/framework/dbkit/sqlite/sqlitetest"
)

func TestInTxJoinsBodyAndRollbackErrors(t *testing.T) {
	t.Parallel()

	errBody := errors.New("sqlite test: the transaction body failed on purpose")
	errRollback := errors.New("sqlite test: the rollback failed on purpose")
	faults := &sqlitetest.Faults{}
	db := sqlitetest.OpenWithFaults(t, testOptions(), faults)
	faults.FailNextRollback(errRollback)

	err := dbkit.InTx(t.Context(), db, func(*sql.Tx) error { return errBody })

	if !errors.Is(err, errBody) {
		t.Errorf("InTx() error = %v, want it to match the body error", err)
	}
	if !errors.Is(err, errRollback) {
		t.Errorf("InTx() error = %v, want it to match the rollback error", err)
	}
}
