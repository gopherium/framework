// SPDX-License-Identifier: Apache-2.0

package gonsole_test

import (
	"bufio"
	"context"
	"errors"
	"io"
	"log"
	"log/slog"
	"net"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gopherium/framework/gonsole"
)

// defaults are the timeouts a program falls back to when its settings are empty.
var defaults = gonsole.Timeouts{
	ReadHeader: 10 * time.Second, Read: 30 * time.Second, Idle: 120 * time.Second, Grace: 15 * time.Second,
	StopGrace: 10 * time.Second,
}

func TestEnvReadsTheTimeouts(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name   string
		values map[string]string
		want   gonsole.Timeouts
		err    string
	}{
		{"no settings", nil, defaults, ""},
		{"every setting", map[string]string{
			"MYAPP_HTTP_READ_HEADER_TIMEOUT": "2s", "MYAPP_HTTP_READ_TIMEOUT": "5s",
			"MYAPP_HTTP_IDLE_TIMEOUT": "1m", "MYAPP_SHUTDOWN_GRACE": "3s", "MYAPP_SHUTDOWN_STOP_GRACE": "6s",
		}, gonsole.Timeouts{ReadHeader: 2 * time.Second, Read: 5 * time.Second, Idle: time.Minute,
			Grace: 3 * time.Second, StopGrace: 6 * time.Second}, ""},
		{"settings that fail", map[string]string{
			"MYAPP_HTTP_READ_HEADER_TIMEOUT": "soon", "MYAPP_HTTP_READ_TIMEOUT": "0s",
			"MYAPP_HTTP_IDLE_TIMEOUT": "-1s", "MYAPP_SHUTDOWN_GRACE": "later", "MYAPP_SHUTDOWN_STOP_GRACE": "never",
		}, gonsole.Timeouts{}, `MYAPP_HTTP_READ_HEADER_TIMEOUT: must be a duration like 30s, got "soon"
MYAPP_HTTP_READ_TIMEOUT: must stand above zero, got "0s"
MYAPP_HTTP_IDLE_TIMEOUT: must stand above zero, got "-1s"
MYAPP_SHUTDOWN_GRACE: must be a duration like 30s, got "later"
MYAPP_SHUTDOWN_STOP_GRACE: must be a duration like 30s, got "never"`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			got, err := settings(tc.values).Timeouts(defaults)

			if got != tc.want || errorText(err) != tc.err {
				t.Errorf("Timeouts() = %+v, %q, want %+v, %q", got, errorText(err), tc.want, tc.err)
			}
		})
	}
}

func TestNewServerCarriesTheTimeouts(t *testing.T) {
	t.Parallel()

	handler := http.NotFoundHandler()

	srv := gonsole.NewServer("127.0.0.1:8080", handler, defaults)

	if srv.Addr != "127.0.0.1:8080" {
		t.Errorf("Addr = %q, want 127.0.0.1:8080", srv.Addr)
	}
	if srv.ReadHeaderTimeout != defaults.ReadHeader || srv.ReadTimeout != defaults.Read ||
		srv.IdleTimeout != defaults.Idle {
		t.Errorf("timeouts = %v, %v, %v, want %+v", srv.ReadHeaderTimeout, srv.ReadTimeout, srv.IdleTimeout, defaults)
	}
	if srv.WriteTimeout != 0 {
		t.Errorf("WriteTimeout = %v, want none for open streams", srv.WriteTimeout)
	}
	if srv.Handler == nil {
		t.Errorf("Handler = nil, want the handler")
	}
}

// journal is a logger target that keeps every line and hands out the address the server listens on.
type journal struct {
	mu        sync.Mutex
	lines     []string
	listening chan string
}

// newJournal returns an empty journal.
func newJournal() *journal {
	return &journal{listening: make(chan string, 1)}
}

// Write keeps one log line and hands out its address when it says the server listens.
func (j *journal) Write(line []byte) (int, error) {
	j.mu.Lock()
	defer j.mu.Unlock()
	text := strings.TrimSpace(string(line))
	j.lines = append(j.lines, text)
	if strings.Contains(text, "msg=listening") {
		_, address, _ := strings.Cut(text, "addr=")
		j.listening <- address
	}
	return len(line), nil
}

// said reports whether any kept line holds text.
func (j *journal) said(text string) bool {
	j.mu.Lock()
	defer j.mu.Unlock()
	for _, line := range j.lines {
		if strings.Contains(line, text) {
			return true
		}
	}
	return false
}

// logger returns a text logger writing to the journal.
func (j *journal) logger() *slog.Logger {
	return slog.New(slog.NewTextHandler(j, nil))
}

// stopper records every call to a stop function and the context it received.
type stopper struct {
	mu       sync.Mutex
	calls    int
	live     bool
	deadline time.Duration
	fails    error
}

// stop records one call and answers the stopper's error.
func (s *stopper) stop(ctx context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.calls++
	s.live = ctx.Err() == nil
	if deadline, bounded := ctx.Deadline(); bounded {
		s.deadline = time.Until(deadline)
	}
	return s.fails
}

// ranOnceWithin reports whether stop ran once, under a live context whose deadline stood about grace away.
func (s *stopper) ranOnceWithin(grace time.Duration) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.calls == 1 && s.live && s.deadline > grace-time.Second && s.deadline <= grace
}

// reportNames answers every request with the report names.
func reportNames() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, "quarterly\nyearly\n")
	})
}

// get fetches the root of the server at address and returns its body.
func get(t *testing.T, address string) string {
	t.Helper()
	request, err := http.NewRequestWithContext(t.Context(), http.MethodGet, "http://"+address+"/", nil)
	if err != nil {
		t.Fatalf("building the request: %v", err)
	}
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatalf("GET %s: %v", address, err)
	}
	defer func() { _ = response.Body.Close() }()
	body, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatalf("reading the answer: %v", err)
	}
	return string(body)
}

// serving starts Serve over handler on a free loopback port and returns the address and the end of the run.
func serving(
	t *testing.T, handler http.Handler, grace time.Duration, stop func(context.Context) error, j *journal,
) (string, func() error) {
	t.Helper()
	ctx, cancel := context.WithCancel(t.Context())
	timeouts := defaults
	timeouts.Grace = grace
	srv := gonsole.NewServer("127.0.0.1:0", handler, timeouts)
	done := make(chan error, 1)
	go func() { done <- gonsole.Serve(ctx, srv, timeouts, stop, j.logger()) }()
	select {
	case address := <-j.listening:
		return address, func() error {
			cancel()
			return <-done
		}
	case err := <-done:
		cancel()
		t.Fatalf("Serve() returned %v before listening", err)
	}
	return "", nil
}

func TestServeAnswersUntilTheRunEnds(t *testing.T) {
	t.Parallel()

	var s stopper
	j := newJournal()
	address, end := serving(t, reportNames(), time.Minute, s.stop, j)

	body := get(t, address)
	err := end()

	if body != "quarterly\nyearly\n" {
		t.Errorf("body = %q, want the report names", body)
	}
	if err != nil {
		t.Errorf("Serve() = %v, want nil", err)
	}
	if !j.said("msg=\"shutting down\"") {
		t.Errorf("log = %q, want a shutting down line", j.lines)
	}
	if !s.ranOnceWithin(defaults.StopGrace) {
		t.Errorf("stop calls = %d, live %t, deadline in %v, want one live call within the stop grace",
			s.calls, s.live, s.deadline)
	}
}

func TestServeGivesStopItsOwnGrace(t *testing.T) {
	t.Parallel()

	var s stopper
	_, end := serving(t, reportNames(), time.Hour, s.stop, newJournal())

	err := end()

	if err != nil {
		t.Errorf("Serve() = %v, want nil", err)
	}
	if !s.ranOnceWithin(defaults.StopGrace) {
		t.Errorf("stop calls = %d, live %t, deadline in %v, want one live call within the stop grace, not the grace",
			s.calls, s.live, s.deadline)
	}
}

func TestServeGivesUpOnARequestThatOutlastsTheGrace(t *testing.T) {
	t.Parallel()

	release := make(chan struct{})
	defer close(release)
	arrived := make(chan struct{})
	slow := http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		close(arrived)
		<-release
	})
	var s stopper
	address, end := serving(t, slow, 50*time.Millisecond, s.stop, newJournal())
	go func() { _, _ = http.Get("http://" + address + "/") }()
	<-arrived

	err := end()

	if !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("Serve() = %v, want the grace to run out", err)
	}
	if !s.ranOnceWithin(defaults.StopGrace) {
		t.Errorf("stop calls = %d, live %t, deadline in %v, want one live call within the stop grace",
			s.calls, s.live, s.deadline)
	}
}

func TestServeStopsOnlyAfterTheServerHasDrained(t *testing.T) {
	t.Parallel()

	stopped := make(chan struct{})
	release := make(chan struct{})
	arrived := make(chan struct{})
	var sawStop atomic.Bool
	pending := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		close(arrived)
		<-release
		select {
		case <-stopped:
			sawStop.Store(true)
		default:
		}
		_, _ = io.WriteString(w, "quarterly\n")
	})
	stop := func(context.Context) error {
		close(stopped)
		return nil
	}
	address, end := serving(t, pending, time.Minute, stop, newJournal())
	go func() { _, _ = http.Get("http://" + address + "/") }()
	<-arrived

	ended := make(chan error, 1)
	go func() { ended <- end() }()
	select {
	case <-stopped:
	case <-time.After(100 * time.Millisecond):
	}
	close(release)

	if err := <-ended; err != nil {
		t.Errorf("Serve() = %v, want nil", err)
	}
	if sawStop.Load() {
		t.Errorf("stop ran while a request was still being served")
	}
}

func TestServeTakesThePortOfHTTPForAnEmptyAddress(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	j := newJournal()

	err := gonsole.Serve(ctx, gonsole.NewServer("", reportNames(), defaults), defaults, nil, j.logger())

	address := ""
	var refused *net.OpError
	if errors.As(err, &refused) && refused.Addr != nil {
		address = refused.Addr.String()
	}
	select {
	case listened := <-j.listening:
		address = listened
	default:
	}
	if _, port, _ := net.SplitHostPort(address); port != "80" {
		t.Errorf("Serve() = %v, address %q, want port 80", err, address)
	}
}

func TestServeRunsWithoutALogger(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	err := gonsole.Serve(ctx, gonsole.NewServer("127.0.0.1:0", reportNames(), defaults), defaults, nil, nil)

	if err != nil {
		t.Errorf("Serve() = %v, want nil", err)
	}
}

func TestServeGivesThePortBackBeforeItReturns(t *testing.T) {
	for range 20 {
		ctx, cancel := context.WithCancel(t.Context())
		cancel()
		j := newJournal()

		err := gonsole.Serve(ctx, gonsole.NewServer("127.0.0.1:0", reportNames(), defaults), defaults, nil, j.logger())

		address := <-j.listening
		again, listenErr := net.Listen("tcp", address)
		if err != nil || listenErr != nil {
			t.Fatalf("Serve() = %v, listening again on %s = %v, want the port free", err, address, listenErr)
		}
		_ = again.Close()
	}
}

func TestServeStopsWithinTheStopGraceWhenTheRunEndedBeforeTheListenFailed(t *testing.T) {
	t.Parallel()

	taken, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("taking a port: %v", err)
	}
	defer func() { _ = taken.Close() }()
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	var s stopper

	_ = gonsole.Serve(ctx, gonsole.NewServer(taken.Addr().String(), reportNames(), defaults), defaults, s.stop, nil)

	if !s.ranOnceWithin(defaults.StopGrace) {
		t.Errorf("stop calls = %d, live %t, deadline in %v, want one live call within the stop grace",
			s.calls, s.live, s.deadline)
	}
}

func TestServeJoinsTheStopError(t *testing.T) {
	t.Parallel()

	s := stopper{fails: errors.New("the reports plugin did not stop")}
	_, end := serving(t, reportNames(), time.Minute, s.stop, newJournal())

	err := end()

	if errorText(err) != "the reports plugin did not stop" {
		t.Errorf("Serve() = %v, want the stop error", err)
	}
}

func TestServeReportsAnAddressItCannotTake(t *testing.T) {
	t.Parallel()

	taken, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("taking a port: %v", err)
	}
	defer func() { _ = taken.Close() }()
	s := stopper{fails: errors.New("the reports plugin did not stop")}
	srv := gonsole.NewServer(taken.Addr().String(), reportNames(), defaults)

	err = gonsole.Serve(t.Context(), srv, defaults, s.stop, nil)

	if !strings.HasPrefix(errorText(err), "http server: listen tcp "+taken.Addr().String()) {
		t.Errorf("Serve() = %v, want the listen failure", err)
	}
	if !strings.HasSuffix(errorText(err), "\nthe reports plugin did not stop") {
		t.Errorf("Serve() = %v, want the stop error joined", err)
	}
	if !s.ranOnceWithin(defaults.StopGrace) {
		t.Errorf("stop calls = %d, live %t, deadline in %v, want one live call within the stop grace",
			s.calls, s.live, s.deadline)
	}
}

func TestServeRunsWithoutAStop(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(t.Context())
	j := newJournal()
	srv := gonsole.NewServer("127.0.0.1:0", reportNames(), defaults)
	done := make(chan error, 1)
	go func() { done <- gonsole.Serve(ctx, srv, defaults, nil, j.logger()) }()
	<-j.listening

	cancel()

	if err := <-done; err != nil {
		t.Errorf("Serve() = %v, want nil", err)
	}
}

func TestServeLogsTheServerErrorsThroughTheLogger(t *testing.T) {
	t.Parallel()

	crashing := http.HandlerFunc(func(http.ResponseWriter, *http.Request) { panic("the report store vanished") })
	var s stopper
	j := newJournal()
	address, end := serving(t, crashing, time.Minute, s.stop, j)

	connection, err := net.Dial("tcp", address)
	if err != nil {
		t.Fatalf("dialling %s: %v", address, err)
	}
	_, _ = io.WriteString(connection, "GET / HTTP/1.1\r\nHost: reports\r\n\r\n")
	_, _ = bufio.NewReader(connection).ReadString('\n')
	_ = connection.Close()
	_ = end()

	if !j.said(`level=ERROR msg="http: panic serving`) || !j.said("the report store vanished") {
		t.Errorf("log = %q, want the server's own panic line at error level", j.lines)
	}
}

func TestServeKeepsTheErrorLogTheServerAlreadyHas(t *testing.T) {
	t.Parallel()

	var own strings.Builder
	var mu sync.Mutex
	crashing := http.HandlerFunc(func(http.ResponseWriter, *http.Request) { panic("the report store vanished") })
	ctx, cancel := context.WithCancel(t.Context())
	j := newJournal()
	srv := gonsole.NewServer("127.0.0.1:0", crashing, defaults)
	srv.ErrorLog = log.New(writerFunc(func(line []byte) (int, error) {
		mu.Lock()
		defer mu.Unlock()
		return own.Write(line)
	}), "", 0)
	done := make(chan error, 1)
	go func() { done <- gonsole.Serve(ctx, srv, defaults, nil, j.logger()) }()
	address := <-j.listening

	connection, err := net.Dial("tcp", address)
	if err != nil {
		t.Fatalf("dialling %s: %v", address, err)
	}
	_, _ = io.WriteString(connection, "GET / HTTP/1.1\r\nHost: reports\r\n\r\n")
	_, _ = bufio.NewReader(connection).ReadString('\n')
	_ = connection.Close()
	cancel()
	<-done

	mu.Lock()
	defer mu.Unlock()
	if !strings.Contains(own.String(), "panic serving") || j.said("panic serving") {
		t.Errorf("own log = %q, journal %q, want the panic line in the server's own log only", own.String(), j.lines)
	}
}

// writerFunc is a function that writes.
type writerFunc func([]byte) (int, error)

// Write calls the function.
func (f writerFunc) Write(p []byte) (int, error) {
	return f(p)
}
