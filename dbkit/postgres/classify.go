// SPDX-License-Identifier: Apache-2.0

package postgres

import (
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5/pgconn"

	"github.com/gopherium/framework/dbkit"
)

// codeClasses maps each PostgreSQL error code that has a class to that class.
var codeClasses = map[string]error{
	"23505": dbkit.ErrUnique,
	"23503": dbkit.ErrForeignKey,
	"23502": dbkit.ErrNotNull,
	"23514": dbkit.ErrCheck,
	"55P03": dbkit.ErrBusy,
	"40001": dbkit.ErrBusy,
	"40P01": dbkit.ErrBusy,
}

// Classify returns err wrapped in its dbkit error class, or err unchanged when it has none or already carries it.
func Classify(err error) error {
	var serverErr *pgconn.PgError
	if !errors.As(err, &serverErr) {
		return err
	}
	class := codeClasses[serverErr.Code]
	if class == nil || errors.Is(err, class) {
		return err
	}
	return fmt.Errorf("%w: %w", class, err)
}
