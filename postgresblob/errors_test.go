// SPDX-License-Identifier: Apache-2.0

package postgresblob_test

import (
	"errors"
	"fmt"
	"testing"

	"github.com/jackc/pgx/v5/pgconn"
	"gocloud.dev/gcerrors"

	"github.com/gopherium/framework/postgresblob"
)

// serverError returns a server error with the SQLSTATE code.
func serverError(code string) error {
	return &pgconn.PgError{Code: code, Message: "a server error"}
}

func TestErrorCodeNamesTheCodeOfEachServerError(t *testing.T) {
	t.Parallel()
	drv, err := postgresblob.OpenDriver(t.Context(), freshDB(t).pool, &postgresblob.Options{Schema: testSchema})
	if err != nil {
		t.Fatalf("OpenDriver() = %v, want a driver", err)
	}
	tests := map[string]struct {
		err  error
		want gcerrors.ErrorCode
	}{
		"an undefined table":            {serverError("42P01"), gcerrors.NotFound},
		"a unique violation":            {serverError("23505"), gcerrors.FailedPrecondition},
		"an insufficient privilege":     {serverError("42501"), gcerrors.PermissionDenied},
		"a character not in repertoire": {serverError("22021"), gcerrors.InvalidArgument},
		"a full disk":                   {serverError("53100"), gcerrors.ResourceExhausted},
		"an out of memory error":        {serverError("53200"), gcerrors.ResourceExhausted},
		"too many connections":          {serverError("53300"), gcerrors.ResourceExhausted},
		"an exceeded program limit":     {serverError("54000"), gcerrors.ResourceExhausted},
		"a canceled query":              {serverError("57014"), gcerrors.Canceled},
		"a syntax error":                {serverError("42601"), gcerrors.Unknown},
		"a wrapped server error":        {fmt.Errorf("a step: %w", serverError("42501")), gcerrors.PermissionDenied},
		"an error of another kind":      {errors.New("an error of another kind"), gcerrors.Unknown},
	}

	for name, tt := range tests {
		if got := drv.ErrorCode(tt.err); got != tt.want {
			t.Errorf("ErrorCode() of %s = %v, want %v", name, got, tt.want)
		}
	}
}

func TestErrorAsExposesTheServerError(t *testing.T) {
	t.Parallel()
	b := freshDB(t).open(postgresblob.Options{Schema: unmigratedSchema})

	_, err := b.Attributes(t.Context(), "notes/report.txt")

	var serverErr *pgconn.PgError
	if !b.ErrorAs(err, &serverErr) || serverErr.Code != "42P01" {
		t.Errorf("ErrorAs() of the error %v of a schema with no tables found %+v, want the server error 42P01",
			err, serverErr)
	}
	if code := gcerrors.Code(err); code != gcerrors.NotFound {
		t.Errorf("Attributes() on a schema with no tables failed with the code %v, want NotFound", code)
	}
}
