// SPDX-License-Identifier: Apache-2.0

package sqlite_test

import (
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
		cause, _ := attrs["error"].Any().(error)
		busy := errors.Is(sqlite.Classify(cause), dbkit.ErrBusy)
		if r.Message != connectorSkipped || attrs["path"].String() != path || !busy {
			t.Errorf("note = %q %v, want %q with the path and the busy error", r.Message, attrs, connectorSkipped)
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
	var busy int64
	if err := conn.QueryRowContext(t.Context(), "PRAGMA busy_timeout").Scan(&busy); err != nil {
		t.Fatalf("PRAGMA busy_timeout error = %v, want nil", err)
	}
	if busy != optionsBusyTimeout.Milliseconds() {
		t.Errorf("PRAGMA busy_timeout = %d, want %d set back after the optimize", busy, optionsBusyTimeout.Milliseconds())
	}
	connectorMustSkip(t, records, path)
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
