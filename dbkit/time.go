// SPDX-License-Identifier: Apache-2.0

package dbkit

import (
	"database/sql/driver"
	"errors"
	"fmt"
	"math"
	"time"
)

// errNullTime refuses a NULL scanned into a Time.
var errNullTime = errors.New("dbkit: a NULL never scans into a Time, scan a nullable column into a *dbkit.Time")

// firstTime is the earliest time a Time stores.
var firstTime = time.UnixMicro(math.MinInt64)

// pastLastTime is the first time after the latest a Time stores.
var pastLastTime = time.UnixMicro(math.MaxInt64).Add(time.Microsecond)

// Time is a time.Time stored as INTEGER UTC microseconds since the Unix epoch.
type Time struct {
	time.Time
}

// Value returns the time as an int64 of microseconds since the Unix epoch, or an error for a time outside that range.
func (t Time) Value() (driver.Value, error) {
	if t.Before(firstTime) || !t.Before(pastLastTime) {
		return nil, fmt.Errorf("dbkit: the time %v falls outside the int64 microsecond range", t.Time)
	}
	return t.UnixMicro(), nil
}

// Scan reads an int64 of microseconds since the Unix epoch into the time in UTC.
func (t *Time) Scan(src any) error {
	switch v := src.(type) {
	case int64:
		t.Time = time.UnixMicro(v).UTC()
		return nil
	case nil:
		return errNullTime
	default:
		return fmt.Errorf("dbkit: a Time scans an int64 of microseconds, got %T", src)
	}
}
