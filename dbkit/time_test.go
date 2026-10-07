// SPDX-License-Identifier: Apache-2.0

package dbkit_test

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"io"
	"math"
	"reflect"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/gopherium/framework/dbkit"
)

// basicSnapshotter is a dbkit.Snapshotter, checked at compile time.
var _ dbkit.Snapshotter = basicSnapshotter{}

// basicSnapshotter is a Snapshotter that reports a copy it never writes.
type basicSnapshotter struct{}

// Snapshot reports target as written with a busy checkpoint.
func (basicSnapshotter) Snapshot(_ context.Context, target string) (dbkit.Snapshot, error) {
	checkpoint := dbkit.Checkpoint{Busy: true, LogFrames: 3, CheckpointedFrames: 2, Took: time.Millisecond}
	return dbkit.Snapshot{Path: target, Checkpoint: checkpoint}, nil
}

// basicConnector is a fake driver whose queries return fixed rows and whose statements record their arguments.
type basicConnector struct {
	// rows holds the one column of every row each query returns.
	rows []driver.Value
	// mu guards bound.
	mu sync.Mutex
	// bound holds the arguments of every statement in the order they ran.
	bound [][]driver.Value
}

// Connect returns a connection to the fake driver.
func (c *basicConnector) Connect(context.Context) (driver.Conn, error) {
	return basicConn{connector: c}, nil
}

// Driver returns the fake driver.
func (c *basicConnector) Driver() driver.Driver {
	return c
}

// Open returns a connection to the fake driver.
func (c *basicConnector) Open(string) (driver.Conn, error) {
	return basicConn{connector: c}, nil
}

// record keeps a copy of one statement's arguments.
func (c *basicConnector) record(args []driver.Value) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.bound = append(c.bound, slices.Clone(args))
}

// boundArgs returns the arguments of every statement in the order they ran.
func (c *basicConnector) boundArgs() [][]driver.Value {
	c.mu.Lock()
	defer c.mu.Unlock()
	return slices.Clone(c.bound)
}

// basicConn is one connection to the fake driver.
type basicConn struct {
	// connector is the fake driver the connection belongs to.
	connector *basicConnector
}

// Prepare returns a statement on the connection.
func (c basicConn) Prepare(string) (driver.Stmt, error) {
	return basicStmt(c), nil
}

// Close closes nothing.
func (basicConn) Close() error {
	return nil
}

// Begin refuses every transaction.
func (basicConn) Begin() (driver.Tx, error) {
	return nil, errors.New("basic fake driver opens no transaction")
}

// basicStmt is one statement on the fake driver.
type basicStmt struct {
	// connector is the fake driver the statement records its arguments on.
	connector *basicConnector
}

// Close closes nothing.
func (basicStmt) Close() error {
	return nil
}

// NumInput reports an unknown count of placeholders.
func (basicStmt) NumInput() int {
	return -1
}

// Exec records args and reports one affected row.
func (s basicStmt) Exec(args []driver.Value) (driver.Result, error) {
	s.connector.record(args)
	return driver.RowsAffected(1), nil
}

// Query records args and returns the fake driver's fixed rows.
func (s basicStmt) Query(args []driver.Value) (driver.Rows, error) {
	s.connector.record(args)
	return &basicRows{values: s.connector.rows}, nil
}

// basicRows walks the fake driver's fixed rows.
type basicRows struct {
	// values holds the one column of every row not read yet.
	values []driver.Value
}

// Columns names the one column.
func (*basicRows) Columns() []string {
	return []string{"at"}
}

// Close closes nothing.
func (*basicRows) Close() error {
	return nil
}

// Next copies the next row into dest.
func (r *basicRows) Next(dest []driver.Value) error {
	if len(r.values) == 0 {
		return io.EOF
	}
	dest[0], r.values = r.values[0], r.values[1:]
	return nil
}

// basicOpen returns a handle over a fake driver whose queries return rows, and that driver.
func basicOpen(t *testing.T, rows ...driver.Value) (*sql.DB, *basicConnector) {
	t.Helper()
	connector := &basicConnector{rows: rows}
	db := sql.OpenDB(connector)
	t.Cleanup(func() {
		if err := db.Close(); err != nil {
			t.Errorf("closing the fake handle gave %v", err)
		}
	})
	return db, connector
}

// basicRoundTrip writes at through Value and reads it back through Scan.
func basicRoundTrip(t *testing.T, at time.Time) (driver.Value, dbkit.Time) {
	t.Helper()
	value, err := dbkit.Time{Time: at}.Value()
	if err != nil {
		t.Fatalf("Value() of %v gave %v, want no error", at, err)
	}
	var got dbkit.Time
	if err := got.Scan(value); err != nil {
		t.Fatalf("Scan(%v) gave %v, want no error", value, err)
	}
	return value, got
}

func TestTimeRoundTripsAtTheMicrosecond(t *testing.T) {
	t.Parallel()

	wholeMicroseconds := time.Date(2026, 10, 6, 12, 30, 45, 123456000, time.UTC)

	value, got := basicRoundTrip(t, wholeMicroseconds)

	if value != wholeMicroseconds.UnixMicro() {
		t.Errorf("Value() = %#v, want the int64 %d", value, wholeMicroseconds.UnixMicro())
	}
	if got.Time != wholeMicroseconds {
		t.Errorf("round trip gave %v, want %v", got.Time, wholeMicroseconds)
	}
}

func TestTimeTruncatesNanosecondsTowardThePast(t *testing.T) {
	t.Parallel()

	cases := []time.Time{
		time.Date(2026, 10, 6, 12, 30, 45, 123456789, time.UTC),
		time.Date(2026, 10, 6, 12, 30, 45, 999, time.UTC),
		time.Date(1969, 12, 31, 23, 59, 59, 999999999, time.UTC),
		time.Date(1969, 7, 20, 20, 17, 40, 500, time.UTC),
	}
	for _, extraNanoseconds := range cases {
		want := extraNanoseconds.Truncate(time.Microsecond)

		value, got := basicRoundTrip(t, extraNanoseconds)

		if value != want.UnixMicro() {
			t.Errorf("Value() of %v = %#v, want the int64 %d", extraNanoseconds, value, want.UnixMicro())
		}
		if got.Time != want || got.After(extraNanoseconds) {
			t.Errorf("round trip of %v gave %v, want %v", extraNanoseconds, got.Time, want)
		}
	}
}

func TestTimeBefore1970RoundTrips(t *testing.T) {
	t.Parallel()

	beforeEpoch := time.Date(1969, 12, 31, 23, 59, 59, 999999000, time.UTC)

	value, got := basicRoundTrip(t, beforeEpoch)

	if value != int64(-1) {
		t.Errorf("Value() of %v = %#v, want the int64 -1", beforeEpoch, value)
	}
	if got.Time != beforeEpoch {
		t.Errorf("round trip of %v gave %v", beforeEpoch, got.Time)
	}
}

func TestTimeValueKeepsTheEdgesOfTheMicrosecondRange(t *testing.T) {
	t.Parallel()

	firstMicrosecond := time.UnixMicro(math.MinInt64).UTC()
	lastMicrosecond := time.UnixMicro(math.MaxInt64).UTC()
	cases := []struct {
		name string
		at   time.Time
		want int64
	}{
		{"first microsecond", firstMicrosecond, math.MinInt64},
		{"last microsecond", lastMicrosecond, math.MaxInt64},
		{"last microsecond and 999 ns", lastMicrosecond.Add(999 * time.Nanosecond), math.MaxInt64},
		{"zero time", time.Time{}, -62135596800000000},
		{"last four digit year", time.Date(9999, 12, 31, 23, 59, 59, 999999000, time.UTC), 253402300799999999},
	}
	for _, c := range cases {
		value, got := basicRoundTrip(t, c.at)

		if value != c.want {
			t.Errorf("%s: Value() = %#v, want the int64 %d", c.name, value, c.want)
		}
		if want := c.at.Truncate(time.Microsecond); got.Time != want {
			t.Errorf("%s: round trip gave %v, want %v", c.name, got.Time, want)
		}
	}
}

func TestTimeValueRefusesATimeOutsideTheMicrosecondRange(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		at   time.Time
		want string
	}{
		{
			"last microsecond and one more",
			time.UnixMicro(math.MaxInt64).Add(time.Microsecond).UTC(),
			"dbkit: the time 294247-01-10 04:00:54.775808 +0000 UTC falls outside the int64 microsecond range",
		},
		{
			"first microsecond and one ns less",
			time.UnixMicro(math.MinInt64).Add(-time.Nanosecond).UTC(),
			"dbkit: the time -290308-12-21 19:59:05.224191999 +0000 UTC falls outside the int64 microsecond range",
		},
		{
			"max time sentinel",
			time.Unix(1<<63-62135596801, 999999999).UTC(),
			"dbkit: the time 292277024627-12-06 15:30:07.999999999 +0000 UTC falls outside the int64 microsecond range",
		},
		{
			"far future year",
			time.Date(300000, 1, 1, 0, 0, 0, 0, time.UTC),
			"dbkit: the time 300000-01-01 00:00:00 +0000 UTC falls outside the int64 microsecond range",
		},
	}
	for _, c := range cases {
		value, err := dbkit.Time{Time: c.at}.Value()

		if err == nil || err.Error() != c.want {
			t.Errorf("%s: Value() gave %v, want %q", c.name, err, c.want)
		}
		if value != nil {
			t.Errorf("%s: Value() = %#v beside its error, want nil", c.name, value)
		}
	}
}

func TestTimeScansIntoUTC(t *testing.T) {
	t.Parallel()

	aheadOfUTC := time.Date(2026, 10, 6, 15, 30, 45, 123456000, time.FixedZone("ahead", 3*60*60))

	_, got := basicRoundTrip(t, aheadOfUTC)

	if got.Location() != time.UTC {
		t.Errorf("Scan gave the location %v, want UTC", got.Location())
	}
	if !got.Equal(aheadOfUTC) {
		t.Errorf("round trip of %v gave %v, want the same instant", aheadOfUTC, got.Time)
	}
}

func TestTimeScanRefusesAnotherType(t *testing.T) {
	t.Parallel()

	cases := []struct {
		src  any
		want string
	}{
		{nil, "dbkit: a NULL never scans into a Time, scan a nullable column into a *dbkit.Time"},
		{"2026-10-06T12:30:45Z", "dbkit: a Time scans an int64 of microseconds, got string"},
		{[]byte("1791297045123456"), "dbkit: a Time scans an int64 of microseconds, got []uint8"},
		{float64(1.5), "dbkit: a Time scans an int64 of microseconds, got float64"},
		{time.Unix(0, 0), "dbkit: a Time scans an int64 of microseconds, got time.Time"},
	}
	kept := time.Date(2026, 10, 6, 12, 0, 0, 0, time.UTC)
	for _, c := range cases {
		got := dbkit.Time{Time: kept}
		if err := got.Scan(c.src); err == nil || err.Error() != c.want {
			t.Errorf("Scan(%#v) gave %v, want %q", c.src, err, c.want)
		}
		if got.Time != kept {
			t.Errorf("Scan(%#v) changed the time to %v, want it kept", c.src, got.Time)
		}
	}
}

func TestANullScansIntoANilTimePointer(t *testing.T) {
	t.Parallel()

	at := time.Date(2026, 10, 6, 12, 30, 45, 123456000, time.UTC)
	db, _ := basicOpen(t, nil, at.UnixMicro())

	rows, err := db.QueryContext(t.Context(), "SELECT at")
	if err != nil {
		t.Fatalf("query gave %v", err)
	}
	var got []*dbkit.Time
	for rows.Next() {
		var p *dbkit.Time
		if err := rows.Scan(&p); err != nil {
			t.Fatalf("Scan gave %v, want no error", err)
		}
		got = append(got, p)
	}
	if err := errors.Join(rows.Err(), rows.Close()); err != nil {
		t.Fatalf("reading the rows gave %v", err)
	}

	if len(got) != 2 {
		t.Fatalf("read %d rows, want 2", len(got))
	}
	if got[0] != nil {
		t.Errorf("NULL scanned as %v, want a nil pointer", got[0])
	}
	if got[1] == nil || got[1].Time != at {
		t.Errorf("value scanned as %v, want %v", got[1], at)
	}
}

func TestANilTimePointerBindsAsNull(t *testing.T) {
	t.Parallel()

	at := time.Date(2026, 10, 6, 12, 30, 45, 123456000, time.UTC)
	db, connector := basicOpen(t)
	var missing *dbkit.Time

	if _, err := db.ExecContext(t.Context(), "INSERT", missing, &dbkit.Time{Time: at}, dbkit.Time{Time: at}); err != nil {
		t.Fatalf("exec gave %v", err)
	}

	want := [][]driver.Value{{nil, at.UnixMicro(), at.UnixMicro()}}
	if got := connector.boundArgs(); !reflect.DeepEqual(got, want) {
		t.Errorf("bound %#v, want %#v", got, want)
	}
}
