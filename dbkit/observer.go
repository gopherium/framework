// SPDX-License-Identifier: Apache-2.0

package dbkit

import (
	"context"
	"fmt"
	"strconv"
)

// Kind names the call that sent a statement.
type Kind int

const (
	// KindExec is a call to Exec.
	KindExec Kind = iota + 1
	// KindQuery is a call to Query.
	KindQuery
	// KindQueryRow is a call to QueryRow.
	KindQueryRow
	// KindBegin is a call to Begin.
	KindBegin
)

// String returns the name of the call.
func (k Kind) String() string {
	switch k {
	case KindExec:
		return "Exec"
	case KindQuery:
		return "Query"
	case KindQueryRow:
		return "QueryRow"
	case KindBegin:
		return "Begin"
	default:
		return "Kind(" + strconv.Itoa(int(k)) + ")"
	}
}

// Statement is what the observer sees before a statement or a transaction start reaches the driver.
type Statement struct {
	// ID is the ID of the share.
	ID string
	// Kind is the call that sent the statement.
	Kind Kind
	// Query is the text as the caller wrote it, before any rewrite, and empty for Begin.
	Query string
	// Writes reports whether the statement may write, always true for Begin.
	Writes bool
	// InTransaction reports whether the statement runs inside a transaction of the share.
	InTransaction bool
	// Count is the number of statements in the request so far, this one included and Begin never counted, 0 with none.
	Count int
	// PastBudget reports whether Count is above the budget of the request.
	PastBudget bool
}

// Observer sees every statement and transaction start of a share before the driver, and refuses one by an error.
type Observer func(ctx context.Context, s Statement) error

// observe shows st to the observer of the share and marks the error of a refused statement with ErrRefused.
func (s *Share) observe(ctx context.Context, st Statement) error {
	if s.observer == nil {
		return nil
	}
	if err := s.observer(ctx, st); err != nil {
		return fmt.Errorf("%w: %w", ErrRefused, err)
	}
	return nil
}
