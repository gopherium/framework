// SPDX-License-Identifier: Apache-2.0

package gonsole

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"
)

// brittle is a listener whose accepts after the first fail for good once broken is closed.
type brittle struct {
	net.Listener
	accepted int
	broken   chan struct{}
}

// Accept returns the first connection and a lasting failure after it.
func (b *brittle) Accept() (net.Conn, error) {
	b.accepted++
	if b.accepted > 1 {
		<-b.broken
		return nil, errors.New("the listener broke")
	}
	return b.Listener.Accept()
}

func TestServeShutsTheServerDownBeforeItStopsWhenServingFails(t *testing.T) {
	t.Parallel()

	inner, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listening: %v", err)
	}
	var stopped atomic.Bool
	var late atomic.Int32
	handler := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if stopped.Load() {
			late.Add(1)
		}
		_, _ = io.WriteString(w, "quarterly\n")
	})
	timeouts := Timeouts{ReadHeader: time.Second, Read: time.Second, Idle: time.Minute, Grace: 5 * time.Second,
		CancelGrace: 5 * time.Second, StopGrace: 5 * time.Second}
	srv := NewServer(inner.Addr().String(), handler, timeouts)
	stop := func(ctx context.Context) error {
		stopped.Store(true)
		return ctx.Err()
	}
	client := &http.Client{Transport: &http.Transport{}}
	address := "http://" + inner.Addr().String() + "/"
	listener := &brittle{Listener: inner, broken: make(chan struct{})}
	var logged strings.Builder
	done := make(chan error, 1)
	go func() {
		done <- serveOn(t.Context(), srv, listener, timeouts, stop, slog.New(slog.NewTextHandler(&logged, nil)))
	}()

	fetch(t, client, address)
	close(listener.broken)
	served := <-done
	_, second := client.Get(address)

	if errorLine(served) != "http server: the listener broke" {
		t.Errorf("serveOn() = %v, want only the serving failure", served)
	}
	if second == nil || late.Load() != 0 {
		t.Errorf("a request after the failure = %v, handled after stop %d times, want none handled", second, late.Load())
	}
	if strings.Contains(logged.String(), "shutting down") {
		t.Errorf("log = %q, want no shutting down line after a failure", logged.String())
	}
}

func TestServeCancelsBeforeItStopsWhenServingFails(t *testing.T) {
	t.Parallel()

	inner, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listening: %v", err)
	}
	arrived := make(chan struct{})
	causes := make(chan error, 1)
	waiting := http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		close(arrived)
		<-r.Context().Done()
		causes <- context.Cause(r.Context())
	})
	timeouts := Timeouts{ReadHeader: time.Second, Read: time.Second, Idle: time.Minute, Grace: 50 * time.Millisecond,
		CancelGrace: 5 * time.Second, StopGrace: 5 * time.Second}
	srv := NewServer(inner.Addr().String(), waiting, timeouts)
	endedFirst := false
	stop := func(ctx context.Context) error {
		endedFirst = len(causes) == 1
		return ctx.Err()
	}
	listener := &brittle{Listener: inner, broken: make(chan struct{})}
	done := make(chan error, 1)
	go func() { done <- serveOn(t.Context(), srv, listener, timeouts, stop, nil) }()
	client := &http.Client{Transport: &http.Transport{}}
	go func() { _, _ = client.Get("http://" + inner.Addr().String() + "/") }()
	<-arrived

	close(listener.broken)
	served := <-done

	if errorLine(served) != "http server: the listener broke" || !endedFirst {
		t.Errorf("serveOn() = %v, request ended before stop %t, want only the serving failure after it ended",
			served, endedFirst)
	}
	select {
	case cause := <-causes:
		if !errors.Is(cause, ErrGraceRanOut) {
			t.Errorf("cause = %v, want the grace to have run out", cause)
		}
	default:
		t.Errorf("the request was still running when serveOn returned")
	}
}

func TestServeRunsTheShutdownHooksOnce(t *testing.T) {
	t.Parallel()

	synctest.Test(t, func(t *testing.T) {
		var calls atomic.Int32
		timeouts := Timeouts{ReadHeader: time.Second, Read: time.Second, Idle: time.Minute, Grace: time.Second,
			CancelGrace: time.Second, StopGrace: time.Second}
		srv := NewServer("127.0.0.1:0", http.NotFoundHandler(), timeouts)
		srv.RegisterOnShutdown(func() { calls.Add(1) })
		ctx, cancel := context.WithCancel(t.Context())
		cancel()

		err := Serve(ctx, srv, timeouts, nil, nil)
		synctest.Wait()

		if err != nil || calls.Load() != 1 {
			t.Errorf("Serve() = %v, shutdown hooks ran %d times, want nil and once", err, calls.Load())
		}
	})
}

// slowWrites is a listener whose connections wait before each write, as over a slow network.
type slowWrites struct {
	net.Listener
	delay time.Duration
}

// Accept returns the next connection with its writes slowed.
func (s slowWrites) Accept() (net.Conn, error) {
	conn, err := s.Listener.Accept()
	if err != nil {
		return nil, err
	}
	return slowConn{Conn: conn, delay: s.delay}, nil
}

// slowConn is a connection whose writes wait before they start.
type slowConn struct {
	net.Conn
	delay time.Duration
}

// Write waits the delay, then writes p.
func (c slowConn) Write(p []byte) (int, error) {
	time.Sleep(c.delay)
	return c.Conn.Write(p)
}

func TestServeDeliversACancelledResponseOverASlowConnection(t *testing.T) {
	t.Parallel()

	inner, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listening: %v", err)
	}
	arrived := make(chan struct{})
	unavailable := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		close(arrived)
		<-r.Context().Done()
		w.WriteHeader(http.StatusServiceUnavailable)
	})
	timeouts := Timeouts{ReadHeader: time.Second, Read: time.Second, Idle: time.Minute,
		Grace: 10 * time.Millisecond, CancelGrace: 5 * time.Second, StopGrace: 5 * time.Second}
	srv := NewServer(inner.Addr().String(), unavailable, timeouts)
	ctx, cancel := context.WithCancel(t.Context())
	listener := slowWrites{Listener: inner, delay: 50 * time.Millisecond}
	done := make(chan error, 1)
	go func() { done <- serveOn(ctx, srv, listener, timeouts, nil, nil) }()
	client := &http.Client{Transport: &http.Transport{}, Timeout: 10 * time.Second}
	answered := make(chan int, 1)
	go func() {
		response, err := client.Get("http://" + inner.Addr().String() + "/")
		if err != nil {
			answered <- 0
			return
		}
		_ = response.Body.Close()
		answered <- response.StatusCode
	}()
	<-arrived

	cancel()
	served := <-done

	if code := <-answered; served != nil || code != http.StatusServiceUnavailable {
		t.Errorf("serveOn() = %v, answer %d, want nil and the cancelled request's own 503", served, code)
	}
}

// watched is a context that says when its Done channel is first asked for.
type watched struct {
	context.Context
	once  sync.Once
	asked chan struct{}
}

// newWatched returns ctx, watched for the first ask of its Done channel.
func newWatched(ctx context.Context) *watched {
	return &watched{Context: ctx, asked: make(chan struct{})}
}

// Done closes asked on its first call and returns the context's Done channel.
func (w *watched) Done() <-chan struct{} {
	w.once.Do(func() { close(w.asked) })
	return w.Context.Done()
}

func TestSettleReturnsAtOnceWhenNoRequestRuns(t *testing.T) {
	t.Parallel()

	var requests inflight
	requests.enter()
	requests.leave()
	requests.enter()
	requests.leave()
	ctx := newWatched(t.Context())

	running := requests.settle(ctx)

	select {
	case <-ctx.asked:
		t.Errorf("settle waited on its context with no request running")
	default:
	}
	if running != 0 {
		t.Errorf("settle() = %d, want 0", running)
	}
}

func TestSettleReturnsOnceTheRunningRequestsEnd(t *testing.T) {
	t.Parallel()

	var requests inflight
	requests.enter()
	ctx := newWatched(t.Context())
	settled := make(chan int, 1)
	go func() { settled <- requests.settle(ctx) }()
	<-ctx.asked

	requests.leave()

	if running := <-settled; running != 0 {
		t.Errorf("settle() = %d, want 0 once the request ended", running)
	}
}

func TestSettleCountsTheRequestsStillRunningWhenItsContextEnds(t *testing.T) {
	t.Parallel()

	var requests inflight
	requests.enter()
	requests.enter()
	ctx, cancel := context.WithCancel(t.Context())
	watching := newWatched(ctx)
	settled := make(chan int, 1)
	go func() { settled <- requests.settle(watching) }()
	<-watching.asked
	requests.leave()

	cancel()

	if running := <-settled; running != 1 {
		t.Errorf("settle() = %d, want the one request still running", running)
	}
}

// fetch fetches address with client and reads the whole answer.
func fetch(t *testing.T, client *http.Client, address string) {
	t.Helper()
	response, err := client.Get(address)
	if err != nil {
		t.Fatalf("GET %s: %v", address, err)
	}
	_, _ = io.Copy(io.Discard, response.Body)
	_ = response.Body.Close()
}

// errorLine returns the message of err, empty when it is nil.
func errorLine(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}
