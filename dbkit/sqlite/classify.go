// SPDX-License-Identifier: Apache-2.0

package sqlite

import (
	"errors"
	"fmt"
	"strings"

	modernc "modernc.org/sqlite"
	sqlite3 "modernc.org/sqlite/lib"

	"github.com/gopherium/framework/dbkit"
)

// LibcVersion is the modernc.org/libc version the driver is pinned with.
const LibcVersion = "v1.77.1"

// restrictMessage is the text SQLite gives a foreign key a RESTRICT action stops.
const restrictMessage = "FOREIGN KEY constraint failed"

// constraintClasses maps each extended constraint result code that has a class to that class.
var constraintClasses = map[int]error{
	sqlite3.SQLITE_CONSTRAINT_UNIQUE:     dbkit.ErrUnique,
	sqlite3.SQLITE_CONSTRAINT_PRIMARYKEY: dbkit.ErrUnique,
	sqlite3.SQLITE_CONSTRAINT_ROWID:      dbkit.ErrUnique,
	sqlite3.SQLITE_CONSTRAINT_FOREIGNKEY: dbkit.ErrForeignKey,
	sqlite3.SQLITE_CONSTRAINT_NOTNULL:    dbkit.ErrNotNull,
	sqlite3.SQLITE_CONSTRAINT_CHECK:      dbkit.ErrCheck,
}

// Classify returns err wrapped in its dbkit error class, or err unchanged when it has none or already carries it.
func Classify(err error) error {
	var driverErr *modernc.Error
	if !errors.As(err, &driverErr) {
		return err
	}
	class := classOf(driverErr)
	if class == nil || errors.Is(err, class) {
		return err
	}
	return fmt.Errorf("%w: %w", class, err)
}

// classOf returns the class of a SQLite error, or nil when it has none.
func classOf(driverErr *modernc.Error) error {
	code := driverErr.Code()
	if primary := code & 0xff; primary == sqlite3.SQLITE_BUSY || primary == sqlite3.SQLITE_LOCKED {
		return dbkit.ErrBusy
	}
	if code == sqlite3.SQLITE_CONSTRAINT_TRIGGER && strings.Contains(driverErr.Error(), restrictMessage) {
		return dbkit.ErrForeignKey
	}
	return constraintClasses[code]
}
