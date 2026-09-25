// SPDX-License-Identifier: Apache-2.0

package gonsole

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"sync"
	"time"
)

// ErrGraceRanOut is the cause context.Cause reports for a request Serve cancels after the shutdown grace.
var ErrGraceRanOut = errors.New("gonsole: the shutdown grace ran out")

// ErrStillServing reports requests still running when the cancel grace ended.
var ErrStillServing = errors.New("gonsole: requests still running after the cancel grace")

// Timeouts are the HTTP timeouts and the shutdown graces one server runs under.
type Timeouts struct {
	// ReadHeader bounds reading one request's headers, the HTTP_READ_HEADER_TIMEOUT setting.
	ReadHeader time.Duration
	// Read bounds reading one whole request, the HTTP_READ_TIMEOUT setting.
	Read time.Duration
	// Idle bounds how long a kept alive connection waits for its next request, the HTTP_IDLE_TIMEOUT setting.
	Idle time.Duration
	// Grace bounds how long open requests get to finish once the run ends, the SHUTDOWN_GRACE setting.
	Grace time.Duration
	// CancelGrace bounds how long the requests cancelled after the grace get to end, the SHUTDOWN_CANCEL_GRACE setting.
	CancelGrace time.Duration
	// StopGrace bounds the stop of what the server serves, the SHUTDOWN_STOP_GRACE setting.
	StopGrace time.Duration
}

// Timeouts returns the HTTP timeouts and the shutdown graces, each falling back to fallback.
func (e Env) Timeouts(fallback Timeouts) (Timeouts, error) {
	var read Timeouts
	var failed [6]error
	read.ReadHeader, failed[0] = e.Duration("HTTP_READ_HEADER_TIMEOUT", fallback.ReadHeader)
	read.Read, failed[1] = e.Duration("HTTP_READ_TIMEOUT", fallback.Read)
	read.Idle, failed[2] = e.Duration("HTTP_IDLE_TIMEOUT", fallback.Idle)
	read.Grace, failed[3] = e.Duration("SHUTDOWN_GRACE", fallback.Grace)
	read.CancelGrace, failed[4] = e.Duration("SHUTDOWN_CANCEL_GRACE", fallback.CancelGrace)
	read.StopGrace, failed[5] = e.Duration("SHUTDOWN_STOP_GRACE", fallback.StopGrace)
	if err := errors.Join(failed[:]...); err != nil {
		return Timeouts{}, err
	}
	return read, nil
}

// NewServer returns an HTTP server for handler at addr under the timeouts.
func NewServer(addr string, handler http.Handler, t Timeouts) *http.Server {
	return &http.Server{
		Addr: addr, Handler: handler, ReadHeaderTimeout: t.ReadHeader, ReadTimeout: t.Read, IdleTimeout: t.Idle,
	}
}

// Serve serves srv until ctx ends or serving fails, then drains it, cancelling what outlasts the grace, and calls stop.
func Serve(
	ctx context.Context, srv *http.Server, t Timeouts, stop func(context.Context) error, logger *slog.Logger,
) error {
	if err := refuseGraces(t); err != nil {
		return err
	}
	listener, err := net.Listen("tcp", cmp.Or(srv.Addr, ":http"))
	if err != nil {
		return errors.Join(fmt.Errorf("http server: %w", err), stopWithin(ctx, t, stop))
	}
	return serveOn(ctx, srv, listener, t, stop, logger)
}

// refuseGraces returns an error naming each grace of t that does not stand above zero.
func refuseGraces(t Timeouts) error {
	graces := []struct {
		name  string
		grace time.Duration
	}{{"Grace", t.Grace}, {"CancelGrace", t.CancelGrace}, {"StopGrace", t.StopGrace}}
	var refused []error
	for _, g := range graces {
		if g.grace <= 0 {
			refused = append(refused, fmt.Errorf("gonsole: Timeouts.%s must stand above zero, got %v", g.name, g.grace))
		}
	}
	return errors.Join(refused...)
}

// serveOn serves srv on listener until ctx ends or serving fails, then drains it and calls stop within the stop grace.
func serveOn(
	ctx context.Context, srv *http.Server, listener net.Listener, t Timeouts, stop func(context.Context) error,
	logger *slog.Logger,
) error {
	logger = cmp.Or(logger, slog.New(slog.DiscardHandler))
	if srv.ErrorLog == nil {
		srv.ErrorLog = slog.NewLogLogger(logger.Handler(), slog.LevelError)
	}
	requests := track(ctx, srv)
	served := make(chan error, 1)
	go func() { served <- srv.Serve(listener) }()
	logger.Info("listening", "addr", listener.Addr().String())
	var failed error
	select {
	case err := <-served:
		failed = fmt.Errorf("http server: %w", err)
	case <-ctx.Done():
		logger.Info("shutting down")
	}
	still := drain(ctx, srv, requests, t, logger)
	if failed == nil {
		<-served
	}
	return errors.Join(failed, still, stopWithin(ctx, t, stop))
}

// track points the requests of srv at a base context the end of ctx cannot cancel and counts them while they run.
func track(ctx context.Context, srv *http.Server) *inflight {
	base, cancel := context.WithCancelCause(context.WithoutCancel(ctx))
	requests := &inflight{cancel: cancel}
	srv.BaseContext = func(net.Listener) context.Context { return base }
	srv.Handler = requests.wrap(cmp.Or[http.Handler](srv.Handler, http.DefaultServeMux))
	return requests
}

// drain shuts srv down within the grace, cancels what still runs, and closes what outlives the cancel grace.
func drain(ctx context.Context, srv *http.Server, requests *inflight, t Timeouts, logger *slog.Logger) error {
	shutting, stopShutting := context.WithCancel(context.WithoutCancel(ctx))
	shut := make(chan struct{})
	go func() {
		_ = srv.Shutdown(shutting)
		close(shut)
	}()
	grace, endGrace := context.WithTimeout(context.WithoutCancel(ctx), t.Grace)
	defer endGrace()
	if running := requests.settle(grace); running > 0 {
		logger.Warn("cancelling the requests still running after the shutdown grace", "count", running)
	}
	requests.cancel(ErrGraceRanOut)
	cancelGrace, endCancelGrace := context.WithTimeout(context.WithoutCancel(ctx), t.CancelGrace)
	defer endCancelGrace()
	await(cancelGrace, shut)
	running := requests.settle(cancelGrace)
	stopShutting()
	<-shut
	_ = srv.Close()
	if running > 0 {
		return ErrStillServing
	}
	return nil
}

// await waits until shut closes or ctx ends.
func await(ctx context.Context, shut <-chan struct{}) {
	select {
	case <-shut:
	case <-ctx.Done():
	}
}

// inflight counts the requests one server is handling and cancels them together.
type inflight struct {
	mu      sync.Mutex
	running int
	idle    chan struct{}
	cancel  context.CancelCauseFunc
}

// wrap returns next, counting each request while it runs.
func (in *inflight) wrap(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		in.enter()
		defer in.leave()
		next.ServeHTTP(w, r)
	})
}

// enter counts one more running request.
func (in *inflight) enter() {
	in.mu.Lock()
	defer in.mu.Unlock()
	if in.running == 0 {
		in.idle = make(chan struct{})
	}
	in.running++
}

// leave counts one running request fewer.
func (in *inflight) leave() {
	in.mu.Lock()
	defer in.mu.Unlock()
	in.running--
	if in.running == 0 {
		close(in.idle)
	}
}

// state returns how many requests run and a channel closed once none does.
func (in *inflight) state() (int, <-chan struct{}) {
	in.mu.Lock()
	defer in.mu.Unlock()
	return in.running, in.idle
}

// settle waits until the requests running now end or ctx ends, then returns how many requests run.
func (in *inflight) settle(ctx context.Context) int {
	running, idle := in.state()
	if running == 0 {
		return 0
	}
	select {
	case <-idle:
	case <-ctx.Done():
	}
	running, _ = in.state()
	return running
}

// stopWithin calls stop, when set, under a context the stop grace bounds and the end of ctx cannot cancel.
func stopWithin(ctx context.Context, t Timeouts, stop func(context.Context) error) error {
	bounded, cancel := context.WithTimeout(context.WithoutCancel(ctx), t.StopGrace)
	defer cancel()
	return optional(bounded, stop)
}

// optional calls fn under ctx, nothing when fn is nil.
func optional(ctx context.Context, fn func(context.Context) error) error {
	if fn == nil {
		return nil
	}
	return fn(ctx)
}
