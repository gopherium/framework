// SPDX-License-Identifier: Apache-2.0

package sqlite

import (
	"context"
	"database/sql/driver"
	"errors"
	"slices"
	"testing"
	"time"

	modernc "modernc.org/sqlite"
)

const (
	// internalLower is the statement that lowers the busy timeout before the optimize.
	internalLower = "PRAGMA busy_timeout=0"
	// internalOptimize is the optimize statement every new connection runs.
	internalOptimize = "PRAGMA optimize=0x10002"
	// internalRestore is the statement that sets internalOptions' busy timeout back.
	internalRestore = "PRAGMA busy_timeout=1500"
)

var (
	// errStepFailed is the error a chosen statement of the fake connection answers with.
	errStepFailed = errors.New("connector test: the statement failed on purpose")
	// errCloseFailed is the error the fake connection's Close answers with.
	errCloseFailed = errors.New("connector test: the close failed on purpose")
)

// internalOptions returns options with every required value set and Create on.
func internalOptions() Options {
	return Options{BusyTimeout: 1500 * time.Millisecond, CacheSize: 1, MaxConns: 2, Synchronous: SynchronousNormal,
		Create: true}
}

// internalConn is a connection that records its statements and fails the chosen ones.
type internalConn struct {
	// fail maps a statement to the error it answers with.
	fail map[string]error
	// closeErr is the error Close answers with.
	closeErr error
	// after runs after each statement with its text.
	after func(query string)
	// ran lists every statement in the order it ran.
	ran []string
	// ended lists every statement that ran on an ended context.
	ended []string
	// closed reports that Close ran.
	closed bool
}

// Prepare refuses every statement.
func (c *internalConn) Prepare(string) (driver.Stmt, error) {
	return nil, errors.New("connector test: no statement is prepared on an internalConn")
}

// Close records the close and answers closeErr.
func (c *internalConn) Close() error {
	c.closed = true
	return c.closeErr
}

// Begin refuses every transaction.
func (c *internalConn) Begin() (driver.Tx, error) {
	return nil, errors.New("connector test: no transaction runs on an internalConn")
}

// ExecContext records query and answers the error chosen for it.
func (c *internalConn) ExecContext(ctx context.Context, query string, _ []driver.NamedValue) (driver.Result, error) {
	c.ran = append(c.ran, query)
	if ctx.Err() != nil {
		c.ended = append(c.ended, query)
	}
	if c.after != nil {
		c.after(query)
	}
	if err := c.fail[query]; err != nil {
		return nil, err
	}
	return driver.RowsAffected(0), nil
}

// internalConnector returns a connector that opens conn, or fails with openErr, and counts its opens.
func internalConnector(conn driver.Conn, openErr error) (*connector, *int) {
	c := newConnector(&modernc.Driver{}, "/srv/site/site.db", internalOptions())
	opened := 0
	c.open = func(string) (driver.Conn, error) {
		opened++
		return conn, openErr
	}
	return c, &opened
}

// internalMustRun fails the test unless conn ran exactly want.
func internalMustRun(t *testing.T, conn *internalConn, want ...string) {
	t.Helper()
	if !slices.Equal(conn.ran, want) {
		t.Errorf("statements = %q, want %q", conn.ran, want)
	}
}

func TestConnectRefusesAnEndedContext(t *testing.T) {
	t.Parallel()

	c, opened := internalConnector(&internalConn{}, nil)
	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	conn, err := c.Connect(ctx)

	if !errors.Is(err, context.Canceled) || conn != nil || *opened != 0 {
		t.Errorf("Connect() = %v, %v after %d opens, want nil, context.Canceled and no open", conn, err, *opened)
	}
}

func TestConnectAnswersTheOpenError(t *testing.T) {
	t.Parallel()

	c, _ := internalConnector(nil, errStepFailed)

	conn, err := c.Connect(t.Context())

	if !errors.Is(err, errStepFailed) || conn != nil {
		t.Errorf("Connect() = %v, %v, want nil and the open error", conn, err)
	}
}

func TestTheOptimizeStepRunsInOrder(t *testing.T) {
	t.Parallel()

	fake := &internalConn{}
	c, _ := internalConnector(fake, nil)

	conn, err := c.Connect(t.Context())

	if err != nil || conn != fake || fake.closed {
		t.Errorf("Connect() = %v, %v, closed %v, want the open connection", conn, err, fake.closed)
	}
	internalMustRun(t, fake, internalLower, internalOptimize, internalRestore)
}

func TestAFailedStepClosesTheConnection(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name   string
		failed string
		ran    []string
	}{
		{"the lowering", internalLower, []string{internalLower, internalRestore}},
		{"the optimize", internalOptimize, []string{internalLower, internalOptimize, internalRestore}},
		{"the restore", internalRestore, []string{internalLower, internalOptimize, internalRestore}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			fake := &internalConn{fail: map[string]error{c.failed: errStepFailed}}
			connector, _ := internalConnector(fake, nil)

			conn, err := connector.Connect(t.Context())

			if !errors.Is(err, errStepFailed) || conn != nil || !fake.closed {
				t.Errorf("Connect() = %v, %v, closed %v, want nil, the step's error and a closed connection",
					conn, err, fake.closed)
			}
			internalMustRun(t, fake, c.ran...)
		})
	}
}

func TestAFailedCloseJoinsTheStepError(t *testing.T) {
	t.Parallel()

	fake := &internalConn{fail: map[string]error{internalOptimize: errStepFailed}, closeErr: errCloseFailed}
	c, _ := internalConnector(fake, nil)

	_, err := c.Connect(t.Context())

	if !errors.Is(err, errStepFailed) || !errors.Is(err, errCloseFailed) {
		t.Errorf("Connect() error = %v, want both the step's error and the close error", err)
	}
}

func TestTheRestoreOutlivesAnEndedContext(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(t.Context())
	fake := &internalConn{after: func(query string) {
		if query == internalOptimize {
			cancel()
		}
	}}
	c, _ := internalConnector(fake, nil)

	if _, err := c.Connect(ctx); err != nil {
		t.Fatalf("Connect() error = %v, want nil", err)
	}

	internalMustRun(t, fake, internalLower, internalOptimize, internalRestore)
	if len(fake.ended) != 0 {
		t.Errorf("statements on an ended context = %q, want the restore run on a live one", fake.ended)
	}
}
