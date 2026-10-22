// SPDX-License-Identifier: Apache-2.0

package sqlite_test

import (
	"cmp"
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"log/slog"
	"testing"
	"time"

	"github.com/gopherium/framework/dbkit"
	"github.com/gopherium/framework/dbkit/sqlite"
	"github.com/gopherium/framework/dbkit/sqlite/internal/seam"
)

const (
	// connectorSkipped is the message of the note a skipped optimize leaves.
	connectorSkipped = "dbkit: PRAGMA optimize skipped on a new connection while the database is busy"
	// connectorNewFiles is how many new files the test opens a full pool on at once.
	connectorNewFiles = 100
	// connectorHold is how long a writer holds a new file while a first open must wait for it.
	connectorHold = 100 * time.Millisecond
	// connectorDeadline is the deadline of a first open behind a writer that never lets go.
	connectorDeadline = 500 * time.Millisecond
	// connectorWaitDeadline is the deadline of a new connection's wait behind a held writer.
	connectorWaitDeadline = 100 * time.Millisecond
	// connectorShortBusyTimeout is a busy timeout a first open waits out on purpose.
	connectorShortBusyTimeout = 300 * time.Millisecond
	// connectorShortBusyPoll is how often a first open that waits out connectorShortBusyTimeout tries again.
	connectorShortBusyPoll = 5 * time.Millisecond
)

// connectorRecords is a log handler that sends every record it handles to a channel.
type connectorRecords struct {
	// records receives a copy of every record.
	records chan slog.Record
}

// Enabled reports every level as enabled.
func (h connectorRecords) Enabled(context.Context, slog.Level) bool {
	return true
}

// Handle sends a copy of r to the channel.
func (h connectorRecords) Handle(_ context.Context, r slog.Record) error {
	h.records <- r.Clone()
	return nil
}

// WithAttrs returns the handler unchanged.
func (h connectorRecords) WithAttrs([]slog.Attr) slog.Handler {
	return h
}

// WithGroup returns the handler unchanged.
func (h connectorRecords) WithGroup(string) slog.Handler {
	return h
}

// connectorAttrs returns the attributes of r by key.
func connectorAttrs(r slog.Record) map[string]slog.Value {
	attrs := map[string]slog.Value{}
	r.Attrs(func(a slog.Attr) bool {
		attrs[a.Key] = a.Value
		return true
	})
	return attrs
}

// connectorMustSkip fails the test unless records holds exactly one skipped optimize note for path.
func connectorMustSkip(t *testing.T, records <-chan slog.Record, path string) {
	t.Helper()
	select {
	case r := <-records:
		attrs := connectorAttrs(r)
		if r.Message != connectorSkipped || attrs["path"].String() != path {
			t.Errorf("note = %q %v, want %q with the path", r.Message, attrs, connectorSkipped)
		}
		if cause, _ := attrs["error"].Any().(error); !errors.Is(sqlite.Classify(cause), dbkit.ErrBusy) {
			t.Errorf("note error = %v, want the busy error", cause)
		}
	default:
		t.Fatal("no note arrived, want the skipped optimize noted")
	}
	select {
	case r := <-records:
		t.Errorf("a second note %q arrived, want one", r.Message)
	default:
	}
}

func TestANewConnectionOpensWhileAWriterHoldsTheLock(t *testing.T) {
	t.Parallel()

	records := make(chan slog.Record, 8)
	address, path := testAddress(t)
	opts := testOptions()
	opts.Logger = slog.New(connectorRecords{records: records})
	db := mustOpen(t, address, opts)
	rulesExec(t, db, "CREATE TABLE optimize_rows (a INTEGER PRIMARY KEY, b TEXT NOT NULL)")
	rulesExec(t, db, "CREATE INDEX optimize_rows_b ON optimize_rows (b)")
	rulesExec(t, db, "INSERT INTO optimize_rows (b) VALUES ('a'), ('b'), ('c'), ('d'), ('e'), ('f'), ('g'), ('h')")
	rulesHoldWriter(t, db, "INSERT INTO optimize_rows (b) VALUES ('held')")
	began := time.Now()

	conn, err := db.Conn(t.Context())

	took := time.Since(began)
	if err != nil {
		t.Fatalf("Conn() error = %v, want a new connection while the writer holds the lock", err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	if took > optionsBusyTimeout/2 {
		t.Errorf("Conn() took %v, want well inside the busy timeout %v", took, optionsBusyTimeout)
	}
	connectorMustWaitWithItsContext(t, conn)
	connectorMustSkip(t, records, path)
}

// connectorMustWaitWithItsContext fails the test unless conn reads BusyTimeout in ms and its wait ends at its deadline.
func connectorMustWaitWithItsContext(t *testing.T, conn *sql.Conn) {
	t.Helper()
	var busy int64
	err := conn.QueryRowContext(t.Context(), "PRAGMA busy_timeout").Scan(&busy)
	if want := optionsBusyTimeout.Milliseconds(); err != nil || busy != want {
		t.Errorf("PRAGMA busy_timeout = %d, %v, want %d, the busy timeout in ms", busy, err, want)
	}
	ctx, cancel := context.WithTimeout(t.Context(), connectorWaitDeadline)
	defer cancel()

	waited, err := rulesBegin(ctx, conn)

	if !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("BeginTx() on the new connection error = %v, want one matching context.DeadlineExceeded", err)
	}
	if waited > optionsBusyTimeout/10 {
		t.Errorf("BeginTx() on the new connection answered after %v, want within a tenth of the busy timeout %v",
			waited, optionsBusyTimeout)
	}
}

func TestAnOptimizeWithNoWriterLeavesNoNote(t *testing.T) {
	t.Parallel()

	records := make(chan slog.Record, 8)
	opts := testOptions()
	opts.Logger = slog.New(connectorRecords{records: records})
	db := rulesOpen(t, opts)
	rulesExec(t, db, "CREATE TABLE optimize_rows (a INTEGER PRIMARY KEY, b TEXT NOT NULL)")
	rulesExec(t, db, "CREATE INDEX optimize_rows_b ON optimize_rows (b)")

	rulesConns(t, db)

	select {
	case r := <-records:
		t.Errorf("note %q arrived, want none from an optimize with no writer", r.Message)
	default:
	}
}

// connectorTaken is one connection a goroutine took from the pool, or the error it got.
type connectorTaken struct {
	// conn is the connection taken, nil after an error.
	conn *sql.Conn
	// err is the error of the take.
	err error
}

// connectorOpenAtOnce takes every connection of a full pool on db at once, closes them and returns every error.
func connectorOpenAtOnce(t *testing.T, db *sql.DB) error {
	t.Helper()
	start := make(chan struct{})
	taken := make(chan connectorTaken, optionsMaxConns)
	for range optionsMaxConns {
		go func() {
			<-start
			conn, err := db.Conn(t.Context())
			taken <- connectorTaken{conn: conn, err: err}
		}()
	}
	close(start)
	var errs []error
	var conns []*sql.Conn
	for range optionsMaxConns {
		got := <-taken
		errs = append(errs, got.err)
		if got.conn != nil {
			conns = append(conns, got.conn)
		}
	}
	for _, conn := range conns {
		errs = append(errs, conn.Close())
	}
	return errors.Join(errs...)
}

func TestTheFirstConnectionsOfANewFileOpenTogether(t *testing.T) {
	t.Parallel()

	for file := range connectorNewFiles {
		db := rulesOpen(t, testOptions())

		err := connectorOpenAtOnce(t, db)

		if err != nil {
			t.Fatalf("a full pool opened at once on new file %d error = %v, want every connection open", file, err)
		}
		if err := db.Close(); err != nil {
			t.Fatalf("Close() error = %v, want nil", err)
		}
	}
}

// connectorTwoPoolsAtOnce takes a full pool on each of two handles of one new file at once and returns every error.
func connectorTwoPoolsAtOnce(t *testing.T) error {
	t.Helper()
	address, _ := testAddress(t)
	handles := []*sql.DB{mustOpen(t, address, testOptions()), mustOpen(t, address, testOptions())}
	taken := make(chan error, len(handles))
	for _, db := range handles {
		go func() { taken <- connectorOpenAtOnce(t, db) }()
	}
	errs := []error{<-taken, <-taken}
	for _, db := range handles {
		errs = append(errs, db.Close())
	}
	return errors.Join(errs...)
}

func TestTwoHandlesOpenANewFileTogether(t *testing.T) {
	t.Parallel()

	failed := 0
	var first error
	for range connectorNewFiles {
		if err := connectorTwoPoolsAtOnce(t); err != nil {
			failed++
			first = cmp.Or(first, err)
		}
	}

	if failed != 0 {
		t.Errorf("%d of %d new files failed two full pools opened at once, the first with %v, want none",
			failed, connectorNewFiles, first)
	}
}

// connectorHoldNewFile returns a handle on a new file and a plain driver connection that ran begin on that file.
func connectorHoldNewFile(t *testing.T, opts sqlite.Options, begin string) (*sql.DB, *sql.Conn) {
	t.Helper()
	address, path := testAddress(t)
	db := mustOpen(t, address, opts)
	plain, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatalf("sql.Open() error = %v, want nil", err)
	}
	t.Cleanup(func() { _ = plain.Close() })
	holder, err := plain.Conn(t.Context())
	if err != nil {
		t.Fatalf("Conn() error = %v, want nil", err)
	}
	t.Cleanup(func() { _ = holder.Close() })
	if _, err := holder.ExecContext(t.Context(), begin); err != nil {
		t.Fatalf("ExecContext(%q) error = %v, want nil", begin, err)
	}
	return db, holder
}

// connectorPing pings db in a goroutine and returns the channel that receives its error.
func connectorPing(t *testing.T, db *sql.DB) <-chan error {
	t.Helper()
	pinged := make(chan error, 1)
	go func() { pinged <- db.PingContext(t.Context()) }()
	return pinged
}

func TestTheFirstOpenOfANewFileWaitsForTheWriter(t *testing.T) {
	t.Parallel()

	db, holder := connectorHoldNewFile(t, testOptions(), "BEGIN IMMEDIATE")
	began := time.Now()
	pinged := connectorPing(t, db)

	select {
	case err := <-pinged:
		t.Fatalf("PingContext() = %v after %v while the writer holds the file, want it to wait", err,
			time.Since(began))
	case <-time.After(connectorHold):
	}
	if _, err := holder.ExecContext(t.Context(), "ROLLBACK"); err != nil {
		t.Fatalf("ROLLBACK error = %v, want nil", err)
	}

	if err := <-pinged; err != nil {
		t.Fatalf("PingContext() error = %v, want nil once the writer let go", err)
	}
	var mode string
	if err := db.QueryRowContext(t.Context(), "PRAGMA journal_mode").Scan(&mode); err != nil || mode != "wal" {
		t.Errorf("PRAGMA journal_mode = %q, %v, want wal", mode, err)
	}
}

func TestTheFirstOpenOfANewFileStopsAtTheBusyTimeout(t *testing.T) {
	t.Parallel()

	opts := testOptions()
	opts.BusyTimeout, opts.BusyPoll = connectorShortBusyTimeout, connectorShortBusyPoll
	db, _ := connectorHoldNewFile(t, opts, "BEGIN IMMEDIATE")
	began := time.Now()

	err := db.PingContext(t.Context())

	waited := time.Since(began)
	if !errors.Is(sqlite.Classify(err), dbkit.ErrBusy) {
		t.Errorf("PingContext() error = %v, want busy while the writer holds the file", err)
	}
	if waited < connectorShortBusyTimeout {
		t.Errorf("PingContext() answered after %v, want at least the busy timeout %v", waited, connectorShortBusyTimeout)
	}
}

// connectorMustEndAtItsDeadline fails unless a ping ends at its deadline, busy if wantBusy, then opens after holder.
func connectorMustEndAtItsDeadline(t *testing.T, db *sql.DB, holder *sql.Conn, wantBusy bool) {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), connectorDeadline)
	defer cancel()
	began := time.Now()

	err := db.PingContext(ctx)

	waited := time.Since(began)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("PingContext() error = %v, want one matching context.DeadlineExceeded", err)
	}
	if busy := errors.Is(sqlite.Classify(err), dbkit.ErrBusy); busy != wantBusy {
		t.Errorf("PingContext() error = %v, busy %v, want busy %v", err, busy, wantBusy)
	}
	if waited > optionsBusyTimeout/10 {
		t.Errorf("PingContext() answered after %v, want within a tenth of the busy timeout %v", waited,
			optionsBusyTimeout)
	}
	if _, err := holder.ExecContext(t.Context(), "ROLLBACK"); err != nil {
		t.Fatalf("ROLLBACK error = %v, want nil", err)
	}
	if err := db.PingContext(t.Context()); err != nil {
		t.Errorf("PingContext() error = %v once the holder let go, want nil", err)
	}
}

func TestTheFirstOpenEndsWithItsContextBehindAnExclusiveLock(t *testing.T) {
	t.Parallel()

	db, holder := connectorHoldNewFile(t, testOptions(), "BEGIN EXCLUSIVE")

	connectorMustEndAtItsDeadline(t, db, holder, true)
}

func TestTheFirstOpenEndsWithItsContextAtTheLongestBusyPoll(t *testing.T) {
	t.Parallel()

	opts := testOptions()
	opts.BusyPoll = opts.BusyTimeout
	db, holder := connectorHoldNewFile(t, opts, "BEGIN EXCLUSIVE")

	connectorMustEndAtItsDeadline(t, db, holder, false)
}

// connectorSerializer is the Serialize method of the driver's own connection.
type connectorSerializer interface {
	// Serialize returns the main database as bytes.
	Serialize() ([]byte, error)
}

func TestRawHandsOutTheDriversConnection(t *testing.T) {
	t.Parallel()

	conn := busyPin(t, rulesOpen(t, testOptions()))
	var got any

	err := conn.Raw(func(raw any) error {
		got = raw
		return nil
	})

	if err != nil {
		t.Fatalf("Raw() error = %v, want nil", err)
	}
	if _, ok := got.(connectorSerializer); !ok {
		t.Errorf("Raw() hands out %T, want the driver's own connection with Serialize() ([]byte, error)", got)
	}
}

// connectorWrapped is a connection the test's wrapper returned.
type connectorWrapped struct {
	driver.Conn
}

func TestTheSeamWrapsEveryNewConnection(t *testing.T) {
	t.Parallel()

	address, path := testAddress(t)
	seam.Set(path, func(c driver.Conn) driver.Conn { return &connectorWrapped{Conn: c} })
	t.Cleanup(func() { seam.Clear(path) })
	db := mustOpen(t, address, testOptions())

	for i, conn := range rulesConns(t, db) {
		err := conn.Raw(func(raw any) error {
			if _, ok := raw.(*connectorWrapped); !ok {
				return errors.New("connector test: the connection is not wrapped")
			}
			return nil
		})
		if err != nil {
			t.Errorf("connection %d: %v, want the seam's wrapper", i, err)
		}
	}
}
