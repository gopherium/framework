// SPDX-License-Identifier: Apache-2.0

package dbkit

import "errors"

var (
	// ErrBusy is the class of an error from a lock that was not free within the engine's wait.
	ErrBusy = errors.New("dbkit: the database is busy")
	// ErrUnique is the class of an error from a unique or primary key constraint.
	ErrUnique = errors.New("dbkit: a unique constraint failed")
	// ErrForeignKey is the class of an error from a foreign key constraint.
	ErrForeignKey = errors.New("dbkit: a foreign key constraint failed")
	// ErrNotNull is the class of an error from a not null constraint.
	ErrNotNull = errors.New("dbkit: a not null constraint failed")
	// ErrCheck is the class of an error from a check constraint.
	ErrCheck = errors.New("dbkit: a check constraint failed")
)
