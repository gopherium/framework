// SPDX-License-Identifier: Apache-2.0

package postgresblob

import (
	"errors"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5/pgconn"
	"gocloud.dev/gcerrors"
)

// MaxKeyBytes is the most bytes a key may hold.
const MaxKeyBytes = 1024

// insufficientResources is the SQLSTATE class of a server short of disk, memory or connections.
const insufficientResources = "53"

var (
	// errNotFound answers a key that holds no live object.
	errNotFound = errors.New("postgresblob: no object has the key")
	// errExists answers a write under IfNotExist to a key that holds a live object.
	errExists = errors.New("postgresblob: an object already has the key")
	// errUnimplemented answers SignedURL.
	errUnimplemented = errors.New("postgresblob: signed URLs are not supported")
	// errEmptyKey refuses an empty key.
	errEmptyKey = errors.New("postgresblob: the key is empty")
	// errLongKey refuses a key longer than MaxKeyBytes.
	errLongKey = fmt.Errorf("postgresblob: the key is longer than %d bytes", MaxKeyBytes)
	// errTooLarge refuses a write or a copy that would take an object past Options.MaxSize.
	errTooLarge = errors.New("postgresblob: the object would be larger than the size cap")
	// errReplaced ends a read whose object was replaced or deleted since the reader opened.
	errReplaced = errors.New("postgresblob: the object was replaced or deleted during the read")
	// errSwept ends an upload whose pending row Sweep deleted.
	errSwept = errors.New("postgresblob: Sweep deleted the upload before it finished")
)

// sentinelCodes pairs each error of this package with its code.
var sentinelCodes = []struct {
	// err is the error of this package.
	err error
	// code is its code.
	code gcerrors.ErrorCode
}{
	{err: errNotFound, code: gcerrors.NotFound},
	{err: errExists, code: gcerrors.FailedPrecondition},
	{err: errUnimplemented, code: gcerrors.Unimplemented},
	{err: errEmptyKey, code: gcerrors.InvalidArgument},
	{err: errLongKey, code: gcerrors.InvalidArgument},
	{err: errTooLarge, code: gcerrors.ResourceExhausted},
	{err: errReplaced, code: gcerrors.FailedPrecondition},
	{err: errSwept, code: gcerrors.FailedPrecondition},
}

// serverCodes are the codes of the SQLSTATE values the driver names one by one.
var serverCodes = map[string]gcerrors.ErrorCode{
	"42P01": gcerrors.NotFound,
	"23505": gcerrors.FailedPrecondition,
	"42501": gcerrors.PermissionDenied,
	"22021": gcerrors.InvalidArgument,
	"54000": gcerrors.ResourceExhausted,
	"57014": gcerrors.Canceled,
}

// errorCode returns the code of err, Unknown for an error the driver does not name.
func errorCode(err error) gcerrors.ErrorCode {
	for _, sentinel := range sentinelCodes {
		if errors.Is(err, sentinel.err) {
			return sentinel.code
		}
	}
	var serverErr *pgconn.PgError
	if !errors.As(err, &serverErr) {
		return gcerrors.Unknown
	}
	if code, named := serverCodes[serverErr.Code]; named {
		return code
	}
	if strings.HasPrefix(serverErr.Code, insufficientResources) {
		return gcerrors.ResourceExhausted
	}
	return gcerrors.Unknown
}

// checkKey returns the error for a key the driver refuses.
func checkKey(key string) error {
	switch {
	case key == "":
		return errEmptyKey
	case len(key) > MaxKeyBytes:
		return errLongKey
	}
	return nil
}
