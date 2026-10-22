// SPDX-License-Identifier: Apache-2.0

package sqlite

import (
	"context"
	"database/sql/driver"
	"errors"
	"math"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	modernc "modernc.org/sqlite"

	"github.com/gopherium/framework/dbkit"
)

const (
	// internalLower is the statement that lowers the busy timeout before the optimize.
	internalLower = "PRAGMA busy_timeout=0"
	// internalOptimize is the optimize statement every new connection runs.
	internalOptimize = "PRAGMA optimize=0x10002"
	// internalRestore is the statement that sets internalOptions' busy timeout back.
	internalRestore = "PRAGMA busy_timeout=1500"
	// internalBusyPoll is the busy poll of internalOptions, the smallest Open accepts.
	internalBusyPoll = time.Millisecond
	// internalLongBusyTimeout is a busy timeout no internal test waits out.
	internalLongBusyTimeout = 20017 * time.Millisecond
	// internalShortBusyTimeout is a busy timeout a test waits out on purpose.
	internalShortBusyTimeout = 50 * time.Millisecond
	// internalShortBusyPoll is the busy poll of a test that waits out internalShortBusyTimeout.
	internalShortBusyPoll = 5 * time.Millisecond
	// internalUnreachedWait is a busy timeout and busy poll no test reaches.
	internalUnreachedWait = time.Hour
	// internalAtOnce is how long a test waits for a call that must return at once.
	internalAtOnce = 10 * time.Second
	// internalBusyOpens is how many first opens answer busy before an open answers a connection.
	internalBusyOpens = 2
	// internalSlowTries is how many first opens a test runs whose every try lasts the whole busy poll.
	internalSlowTries = 20
	// internalCallers is how many goroutines call Connect on one connector at once.
	internalCallers = 8
	// internalCallsEach is how many times each of internalCallers calls Connect.
	internalCallsEach = 50
	// internalOpenEnded starts the error of an open whose context ended before any try answered busy.
	internalOpenEnded = "dbkit: open the database: "
	// internalFreeTakes is how many times a test takes the free opening slot under an ended context.
	internalFreeTakes = 64
)

var (
	// errStepFailed is the error a chosen statement of the fake connection answers with.
	errStepFailed = errors.New("connector test: the statement failed on purpose")
	// errCloseFailed is the error the fake connection's Close answers with.
	errCloseFailed = errors.New("connector test: the close failed on purpose")
	// errRestoreFailed is the error the fake connection's restore answers with.
	errRestoreFailed = errors.New("connector test: the restore failed on purpose")
	// errEndCause is the cause a test ends a context with.
	errEndCause = errors.New("connector test: the context ended with this cause on purpose")
)

// internalOptions returns options with every required value set and Create on.
func internalOptions() Options {
	return Options{BusyTimeout: 1500 * time.Millisecond, BusyPoll: internalBusyPoll, CacheSize: 1, MaxConns: 2,
		Synchronous: SynchronousNormal, Create: true}
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

// internalPlainConn opens a driver connection with no rule to the file at path and closes it when the test ends.
func internalPlainConn(t *testing.T, path string) driver.ExecerContext {
	t.Helper()
	conn, err := (&modernc.Driver{}).Open(path)
	if err != nil {
		t.Fatalf("Open(%s) error = %v, want nil", path, err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	return conn.(driver.ExecerContext)
}

// internalBusyError returns the error SQLite answers a second writer while a first one holds the write lock.
func internalBusyError(t *testing.T) error {
	t.Helper()
	if err := ofdOnce(); err != nil {
		t.Fatalf("ofdOnce() error = %v, want nil", err)
	}
	path := filepath.Join(t.TempDir(), "site.db")
	if _, err := internalPlainConn(t, path).ExecContext(t.Context(), "BEGIN IMMEDIATE", nil); err != nil {
		t.Fatalf("BEGIN IMMEDIATE error = %v, want nil", err)
	}
	_, err := internalPlainConn(t, path).ExecContext(t.Context(), "BEGIN IMMEDIATE", nil)
	if !errors.Is(Classify(err), dbkit.ErrBusy) {
		t.Fatalf("a second BEGIN IMMEDIATE error = %v, want busy", err)
	}
	return err
}

// internalWaiting returns a connector with the busy timeout and busy poll given that opens through open.
func internalWaiting(timeout, poll time.Duration, open func(name string) (driver.Conn, error)) *connector {
	opts := internalOptions()
	opts.BusyTimeout, opts.BusyPoll = timeout, poll
	c := newConnector(&modernc.Driver{}, "/srv/site/site.db", opts)
	c.open = open
	return c
}

// internalBusyFirst returns an open that answers busy to its first n calls and conn after, and its count of calls.
func internalBusyFirst(busy error, n int32, conn driver.Conn) (func(string) (driver.Conn, error), *atomic.Int32) {
	opened := &atomic.Int32{}
	return func(string) (driver.Conn, error) {
		if opened.Add(1) <= n {
			return nil, busy
		}
		return conn, nil
	}, opened
}

// internalCall is a call of Connect running in a goroutine.
type internalCall struct {
	// cancel cancels the context of the call.
	cancel context.CancelFunc
	// ended is closed once the call returned.
	ended chan struct{}
	// conn is the connection of the call, read once ended is closed.
	conn driver.Conn
	// err is the error of the call, read once ended is closed.
	err error
}

// internalStart runs Connect on c in a goroutine, which the end of the test cancels and waits for.
func internalStart(t *testing.T, c *connector) *internalCall {
	t.Helper()
	return internalStartUnder(t, c, context.WithCancel)
}

// internalShortDeadline returns ctx with a deadline after internalShortBusyTimeout.
func internalShortDeadline(ctx context.Context) (context.Context, context.CancelFunc) {
	return context.WithTimeout(ctx, internalShortBusyTimeout)
}

// internalCauseOf returns ctx with a cancel that ends it with errEndCause as its cause.
func internalCauseOf(ctx context.Context) (context.Context, context.CancelFunc) {
	ctx, cancel := context.WithCancelCause(ctx)
	return ctx, func() { cancel(errEndCause) }
}

// internalStartUnder runs Connect on c in a goroutine under the context end makes, which the end of the test cancels.
func internalStartUnder(t *testing.T, c *connector,
	end func(context.Context) (context.Context, context.CancelFunc)) *internalCall {
	t.Helper()
	ctx, cancel := end(t.Context())
	call := &internalCall{cancel: cancel, ended: make(chan struct{})}
	go func() {
		defer close(call.ended)
		call.conn, call.err = c.Connect(ctx)
	}()
	t.Cleanup(func() {
		cancel()
		select {
		case <-call.ended:
		case <-time.After(internalAtOnce):
			t.Errorf("Connect() still runs %v after its context ended, want it to end with its context", internalAtOnce)
		}
	})
	return call
}

// within returns what the call answered, and fails the test when the call still runs after internalAtOnce.
func (c *internalCall) within(t *testing.T) (driver.Conn, error) {
	t.Helper()
	select {
	case <-c.ended:
		return c.conn, c.err
	case <-time.After(internalAtOnce):
		t.Fatalf("Connect() still runs after %v, want it to return at once", internalAtOnce)
		return nil, nil
	}
}

// mustWait fails the test when the call has already returned.
func (c *internalCall) mustWait(t *testing.T) {
	t.Helper()
	select {
	case <-c.ended:
		t.Errorf("Connect() returned %v, %v, want it still waiting", c.conn, c.err)
	default:
	}
}

// internalMustReceive fails the test when ch receives nothing within internalAtOnce, naming the signal it wants.
func internalMustReceive(t *testing.T, ch <-chan struct{}, want string) {
	t.Helper()
	select {
	case <-ch:
	case <-time.After(internalAtOnce):
		t.Fatalf("no signal after %v, want %s", internalAtOnce, want)
	}
}

// internalClosing is a connection that reports its Close on a channel.
type internalClosing struct {
	*internalConn
	// closed is closed once Close ran.
	closed chan struct{}
}

// Close closes the channel closed.
func (c *internalClosing) Close() error {
	close(c.closed)
	return nil
}

// internalHeld is an open whose every call waits until the test releases it.
type internalHeld struct {
	// conn is what the first call answers once released.
	conn driver.Conn
	// err is the error the first call answers once released.
	err error
	// started receives once the first call began.
	started chan struct{}
	// released is closed once the test lets every call answer.
	released chan struct{}
	// release closes released once.
	release func()
	// calls counts the calls that began.
	calls atomic.Int32
}

// internalHold returns a held open whose first call answers conn and err, which the end of the test releases.
func internalHold(t *testing.T, conn driver.Conn, err error) *internalHeld {
	t.Helper()
	released := make(chan struct{})
	h := &internalHeld{conn: conn, err: err, started: make(chan struct{}, 1), released: released,
		release: sync.OnceFunc(func() { close(released) })}
	t.Cleanup(h.release)
	return h
}

// open counts its call, signals the first one and answers once released, a fresh connection after the first call.
func (h *internalHeld) open(string) (driver.Conn, error) {
	if h.calls.Add(1) != 1 {
		<-h.released
		return &internalConn{}, nil
	}
	h.started <- struct{}{}
	<-h.released
	return h.conn, h.err
}

// internalGiveUp starts Connect on c, cancels it once the first try of held began and returns its error.
func internalGiveUp(t *testing.T, c *connector, held *internalHeld) error {
	t.Helper()
	call := internalStart(t, c)
	internalMustReceive(t, held.started, "the first try to begin")
	call.cancel()
	conn, err := call.within(t)
	if conn != nil || !errors.Is(err, context.Canceled) {
		t.Errorf("Connect() cancelled during its try = %v, %v, want nil and context.Canceled at once", conn, err)
	}
	return err
}

// internalMustOpenAgain fails the test unless a new Connect on c opens through the second call of held.
func internalMustOpenAgain(t *testing.T, c *connector, held *internalHeld) {
	t.Helper()
	conn, err := internalStart(t, c).within(t)
	if err != nil || conn == nil {
		t.Errorf("Connect() after the given-up try = %v, %v, want a connection", conn, err)
	}
	if got := held.calls.Load(); got != 2 {
		t.Errorf("opens = %d after the given-up try, want the second open to answer the new Connect", got)
	}
}

func TestEndedMarksAnErrorOnlyOnceItsContextEnded(t *testing.T) {
	t.Parallel()

	cancelled, cancel := context.WithCancel(t.Context())
	cancel()

	if got := ended(t.Context(), errStepFailed); got != errStepFailed {
		t.Errorf("ended() under a live context = %v, want the error as it is", got)
	}
	got := ended(cancelled, errStepFailed)
	if !errors.Is(got, errStepFailed) || !errors.Is(got, context.Canceled) {
		t.Errorf("ended() under a cancelled context = %v, want an error matching both the error and context.Canceled",
			got)
	}
	if want := errStepFailed.Error() + ": " + context.Canceled.Error(); got == nil || got.Error() != want {
		t.Errorf("ended() under a cancelled context = %v, want %q", got, want)
	}
}

func TestConnectRefusesAnEndedContext(t *testing.T) {
	t.Parallel()

	c, opened := internalConnector(&internalConn{}, nil)
	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	conn, err := c.Connect(ctx)

	if err != context.Canceled || conn != nil {
		t.Errorf("Connect() = %v, %v, want nil and context.Canceled itself", conn, err)
	}
	c.opening <- struct{}{}
	if *opened != 0 {
		t.Errorf("opens = %d once the opening slot is free, want no open", *opened)
	}
}

func TestConnectAnswersTheOpenError(t *testing.T) {
	t.Parallel()

	c, opened := internalConnector(nil, errStepFailed)

	conn, err := c.Connect(t.Context())

	if !errors.Is(err, errStepFailed) || conn != nil {
		t.Errorf("Connect() = %v, %v, want nil and the open error", conn, err)
	}
	if *opened != 1 {
		t.Errorf("opens = %d, want one open for an error that is not busy", *opened)
	}
}

func TestConnectTriesABusyOpenAgain(t *testing.T) {
	t.Parallel()

	fake := &internalConn{}
	open, opened := internalBusyFirst(internalBusyError(t), internalBusyOpens, fake)
	c := internalWaiting(internalLongBusyTimeout, internalBusyPoll, open)

	conn, err := c.Connect(t.Context())

	if err != nil || conn != fake {
		t.Errorf("Connect() = %v, %v, want the connection of the open after the busy ones", conn, err)
	}
	if got := opened.Load(); got != internalBusyOpens+1 {
		t.Errorf("opens = %d, want %d", got, internalBusyOpens+1)
	}
}

func TestABusyOpenStopsAtTheBusyTimeout(t *testing.T) {
	t.Parallel()

	busy := internalBusyError(t)
	open, _ := internalBusyFirst(busy, math.MaxInt32, nil)
	c := internalWaiting(internalShortBusyTimeout, internalShortBusyPoll, open)
	began := time.Now()

	conn, err := c.Connect(t.Context())

	waited := time.Since(began)
	if !errors.Is(Classify(err), dbkit.ErrBusy) || conn != nil {
		t.Errorf("Connect() = %v, %v, want nil and the busy error", conn, err)
	}
	if !errors.Is(err, busy) {
		t.Errorf("Connect() error = %v, want the last busy error %v", err, busy)
	}
	if waited < internalShortBusyTimeout {
		t.Errorf("Connect() answered after %v, want at least the busy timeout %v", waited, internalShortBusyTimeout)
	}
}

func TestABusyOpenEndsWithItsContext(t *testing.T) {
	t.Parallel()

	busy := internalBusyError(t)
	ctx, cancel := context.WithCancel(t.Context())
	var opened atomic.Int32
	c := internalWaiting(internalLongBusyTimeout, internalBusyPoll, func(string) (driver.Conn, error) {
		if opened.Add(1) == internalBusyOpens {
			cancel()
		}
		return nil, busy
	})
	began := time.Now()

	conn, err := c.Connect(ctx)

	waited := time.Since(began)
	if !errors.Is(err, context.Canceled) || conn != nil {
		t.Errorf("Connect() = %v, %v, want nil and an error matching context.Canceled", conn, err)
	}
	if !errors.Is(Classify(err), dbkit.ErrBusy) {
		t.Errorf("Classify(%v) is not busy, want the busy error kept", err)
	}
	if waited > internalLongBusyTimeout/10 {
		t.Errorf("Connect() answered after %v, want within a tenth of the busy timeout %v", waited,
			internalLongBusyTimeout)
	}
}

func TestAWaitingOpenLeavesTheSlotFree(t *testing.T) {
	t.Parallel()

	busy, fake := internalBusyError(t), &internalConn{}
	waiting := make(chan struct{}, 1)
	var opened atomic.Int32
	c := internalWaiting(internalUnreachedWait, internalUnreachedWait, func(string) (driver.Conn, error) {
		if opened.Add(1) == 1 {
			waiting <- struct{}{}
			return nil, busy
		}
		return fake, nil
	})
	first := internalStart(t, c)
	internalMustReceive(t, waiting, "the open to signal its call")

	conn, err := internalStart(t, c).within(t)

	if err != nil || conn != fake {
		t.Errorf("the second Connect() = %v, %v, want the open connection", conn, err)
	}
	first.mustWait(t)
	first.cancel()
	if _, err := first.within(t); !errors.Is(err, context.Canceled) {
		t.Errorf("the first Connect() error = %v, want context.Canceled once cancelled", err)
	}
}

func TestEachOpenTryWaitsOneBusyPoll(t *testing.T) {
	t.Parallel()

	var name string
	c := internalWaiting(internalLongBusyTimeout, internalShortBusyPoll, func(n string) (driver.Conn, error) {
		name = n
		return &internalConn{}, nil
	})

	if _, err := c.Connect(t.Context()); err != nil {
		t.Fatalf("Connect() error = %v, want nil", err)
	}

	if want := "&_busy_timeout=5&"; !strings.Contains(name, want) {
		t.Errorf("the open's name = %q, want it to hold %q, the busy poll", name, want)
	}
}

func TestTheOptimizeStepRunsInOrder(t *testing.T) {
	t.Parallel()

	fake := &internalConn{}
	c, _ := internalConnector(fake, nil)

	conn, err := c.Connect(t.Context())

	if err != nil || conn != fake {
		t.Errorf("Connect() = %v, %v, want the open connection", conn, err)
	}
	if fake.closed {
		t.Errorf("closed = %v, want the open connection left open", fake.closed)
	}
	internalMustRun(t, fake, internalLower, internalOptimize, internalRestore)
}

func TestAFailedStepClosesTheConnection(t *testing.T) {
	t.Parallel()

	cases := []struct {
		// name names the step that fails.
		name string
		// failed is the statement that fails.
		failed string
		// ran lists the statements the connection runs.
		ran []string
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

			if !errors.Is(err, errStepFailed) || conn != nil {
				t.Errorf("Connect() = %v, %v, want nil and the step's error", conn, err)
			}
			if !fake.closed {
				t.Errorf("closed = %v, want the connection closed", fake.closed)
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

func TestAFailedRestoreKeepsTheStepError(t *testing.T) {
	t.Parallel()

	fake := &internalConn{fail: map[string]error{internalOptimize: errStepFailed, internalRestore: errRestoreFailed}}
	c, _ := internalConnector(fake, nil)

	_, err := c.Connect(t.Context())

	if !errors.Is(err, errStepFailed) || !errors.Is(err, errRestoreFailed) {
		t.Errorf("Connect() error = %v, want both the step's error and the restore error", err)
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

func TestConnectAnswersAnErrorThatOnlyReadsBusy(t *testing.T) {
	t.Parallel()

	text := errors.New(internalBusyError(t).Error())
	c, opened := internalConnector(nil, text)

	conn, err := c.Connect(t.Context())

	if !errors.Is(err, text) || conn != nil {
		t.Errorf("Connect() = %v, %v, want nil and the open error", conn, err)
	}
	if *opened != 1 {
		t.Errorf("opens = %d, want one open for an error whose text alone reads busy", *opened)
	}
}

func TestABusyOpenTriesNoLongerThanTheBusyTimeout(t *testing.T) {
	t.Parallel()

	busy := internalBusyError(t)
	open, opened := internalBusyFirst(busy, math.MaxInt32, nil)
	c := internalWaiting(internalShortBusyTimeout, internalShortBusyPoll, open)

	_, err := c.Connect(t.Context())

	if most := int32(internalShortBusyTimeout/internalShortBusyPoll) + 2; opened.Load() > most || err != busy {
		t.Errorf("Connect() = %v after %d opens, want the last busy error itself after at most %d", err,
			opened.Load(), most)
	}
}

func TestASlowBusyOpenStopsAfterTheTryAtTheBusyTimeout(t *testing.T) {
	t.Parallel()

	busy := internalBusyError(t)
	for range internalSlowTries {
		var opened atomic.Int32
		c := internalWaiting(internalShortBusyTimeout, internalShortBusyTimeout, func(string) (driver.Conn, error) {
			opened.Add(1)
			time.Sleep(internalShortBusyTimeout)
			return nil, busy
		})

		if _, err := c.Connect(t.Context()); err != busy || opened.Load() != 1 {
			t.Fatalf("Connect() = %v after %d opens, want the busy error after the one open the busy timeout ended",
				err, opened.Load())
		}
	}
}

func TestASlowBusyOpenStopsDuringTheTryItsContextEnded(t *testing.T) {
	t.Parallel()

	busy := internalBusyError(t)
	for range internalSlowTries {
		ctx, cancel := context.WithCancel(t.Context())
		var opened atomic.Int32
		c := internalWaiting(internalUnreachedWait, internalShortBusyPoll, func(string) (driver.Conn, error) {
			opened.Add(1)
			cancel()
			time.Sleep(internalShortBusyPoll)
			return nil, busy
		})

		if _, err := c.Connect(ctx); !errors.Is(err, context.Canceled) || opened.Load() != 1 {
			t.Fatalf("Connect() = %v after %d opens, want context.Canceled after the one open its context ended",
				err, opened.Load())
		}
	}
}

func TestAHandleRunsOneOpenAtATime(t *testing.T) {
	t.Parallel()

	var running atomic.Int32
	var overlapped atomic.Bool
	c := internalWaiting(internalLongBusyTimeout, internalBusyPoll, func(string) (driver.Conn, error) {
		if running.Add(1) > 1 {
			overlapped.Store(true)
		}
		runtime.Gosched()
		running.Add(-1)
		return &internalConn{}, nil
	})
	var wg sync.WaitGroup
	for range internalCallers {
		wg.Go(func() {
			for range internalCallsEach {
				_, _ = c.Connect(t.Context())
			}
		})
	}
	wg.Wait()

	if overlapped.Load() {
		t.Error("two opens of one handle ran at once, want one at a time")
	}
}

func TestAnOpenEndsWithItsContextWhileItsTryRuns(t *testing.T) {
	t.Parallel()

	fake := &internalClosing{internalConn: &internalConn{}, closed: make(chan struct{})}
	held := internalHold(t, fake, nil)
	c := internalWaiting(internalUnreachedWait, internalUnreachedWait, held.open)

	err := internalGiveUp(t, c, held)

	if want := internalOpenEnded + context.Canceled.Error(); err == nil || err.Error() != want {
		t.Errorf("Connect() error = %v, want %q", err, want)
	}
	held.release()
	internalMustReceive(t, fake.closed, "the given-up try's connection closed")
	internalMustOpenAgain(t, c, held)
}

func TestAnOpenWaitingForTheSlotEndsWithItsOwnContext(t *testing.T) {
	t.Parallel()

	held := internalHold(t, &internalConn{}, nil)
	c := internalWaiting(internalUnreachedWait, internalUnreachedWait, held.open)
	_ = internalGiveUp(t, c, held)

	conn, err := internalStartUnder(t, c, internalShortDeadline).within(t)

	if conn != nil || !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("the second Connect() = %v, %v, want nil and context.DeadlineExceeded", conn, err)
	}
	if want := internalOpenEnded + context.DeadlineExceeded.Error(); err == nil || err.Error() != want {
		t.Errorf("the second Connect() error = %v, want %q", err, want)
	}
	if got := held.calls.Load(); got != 1 {
		t.Errorf("opens = %d, want the given-up try alone while it runs", got)
	}
}

func TestOpensEndingDuringOneTryLeaveOnlyThatTryRunning(t *testing.T) {
	t.Parallel()

	held := internalHold(t, &internalConn{}, nil)
	c := internalWaiting(internalUnreachedWait, internalUnreachedWait, held.open)
	_ = internalGiveUp(t, c, held)
	calls := make([]*internalCall, internalCallers)
	for i := range calls {
		calls[i] = internalStartUnder(t, c, internalShortDeadline)
	}

	for i, call := range calls {
		if _, err := call.within(t); !errors.Is(err, context.DeadlineExceeded) {
			t.Errorf("Connect() %d error = %v, want context.DeadlineExceeded", i, err)
		}
	}

	if got := held.calls.Load(); got != 1 {
		t.Errorf("opens = %d after %d Connects ended, want the given-up try alone", got, internalCallers)
	}
	held.release()
	internalMustOpenAgain(t, c, held)
}

func TestATryThatAnswersBusyAfterItsContextEndedLeavesNothingOpen(t *testing.T) {
	t.Parallel()

	held := internalHold(t, nil, internalBusyError(t))
	c := internalWaiting(internalUnreachedWait, internalUnreachedWait, held.open)

	err := internalGiveUp(t, c, held)

	if errors.Is(Classify(err), dbkit.ErrBusy) {
		t.Errorf("Connect() error = %v, want the context's alone, since no try answered busy before it ended", err)
	}
	held.release()
	internalMustOpenAgain(t, c, held)
}

func TestAnOpenEndingDuringItsFirstTryMatchesTheContextCause(t *testing.T) {
	t.Parallel()

	held := internalHold(t, &internalConn{}, nil)
	c := internalWaiting(internalUnreachedWait, internalUnreachedWait, held.open)
	call := internalStartUnder(t, c, internalCauseOf)
	internalMustReceive(t, held.started, "the first try to begin")

	call.cancel()

	conn, err := call.within(t)
	if conn != nil || !errors.Is(err, errEndCause) {
		t.Errorf("Connect() = %v, %v, want nil and an error matching the context's cause %q", conn, err, errEndCause)
	}
	if !errors.Is(err, context.Canceled) {
		t.Errorf("Connect() error = %v, want it to match context.Canceled as the busy shape does", err)
	}
}

func TestAnOpenEndingDuringALaterTryMarksTheLastBusyError(t *testing.T) {
	t.Parallel()

	busy := internalBusyError(t)
	held := internalHold(t, &internalConn{}, nil)
	var opened atomic.Int32
	c := internalWaiting(internalUnreachedWait, internalBusyPoll, func(name string) (driver.Conn, error) {
		if opened.Add(1) == 1 {
			return nil, busy
		}
		return held.open(name)
	})

	err := internalGiveUp(t, c, held)

	if want := busy.Error() + ": " + context.Canceled.Error(); !errors.Is(err, busy) || err.Error() != want {
		t.Errorf("Connect() error = %v, want %q, the last busy error marked with the context's", err, want)
	}
}

func TestAFreeSlotIsTakenEvenAfterTheContextEnded(t *testing.T) {
	t.Parallel()

	c, _ := internalConnector(&internalConn{}, nil)
	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	for take := range internalFreeTakes {
		if !c.takeSlot(ctx) {
			t.Fatalf("takeSlot() %d under an ended context = false, want the free slot taken at once", take)
		}
		<-c.opening
	}
}

// internalSlowClose is a connection whose Close waits until the test lets it finish.
type internalSlowClose struct {
	// internalConn gives the connection the methods of driver.Conn and ExecContext.
	*internalConn
	// closing receives once Close began.
	closing chan struct{}
	// finished is closed once the test lets Close return.
	finished chan struct{}
}

// Close signals closing and returns once finished is closed.
func (c *internalSlowClose) Close() error {
	c.closing <- struct{}{}
	<-c.finished
	return nil
}

func TestAGivenUpTryFreesTheSlotOnlyOnceItsConnectionClosed(t *testing.T) {
	t.Parallel()

	finished := make(chan struct{})
	finish := sync.OnceFunc(func() { close(finished) })
	t.Cleanup(finish)
	fake := &internalSlowClose{internalConn: &internalConn{}, closing: make(chan struct{}, 1), finished: finished}
	held := internalHold(t, fake, nil)
	c := internalWaiting(internalUnreachedWait, internalUnreachedWait, held.open)
	_ = internalGiveUp(t, c, held)
	held.release()
	internalMustReceive(t, fake.closing, "the given-up try's connection to start closing")

	conn, err := internalStartUnder(t, c, internalShortDeadline).within(t)

	if conn != nil || !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("Connect() while the given-up connection closes = %v, %v, want nil and context.DeadlineExceeded",
			conn, err)
	}
	finish()
	internalMustOpenAgain(t, c, held)
}
