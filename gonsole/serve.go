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
	"time"
)

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
	// StopGrace bounds the stop of what the server serves, the SHUTDOWN_STOP_GRACE setting.
	StopGrace time.Duration
}

// Timeouts returns the HTTP timeouts and the shutdown graces, each falling back to fallback.
func (e Env) Timeouts(fallback Timeouts) (Timeouts, error) {
	var read Timeouts
	var failed [5]error
	read.ReadHeader, failed[0] = e.Duration("HTTP_READ_HEADER_TIMEOUT", fallback.ReadHeader)
	read.Read, failed[1] = e.Duration("HTTP_READ_TIMEOUT", fallback.Read)
	read.Idle, failed[2] = e.Duration("HTTP_IDLE_TIMEOUT", fallback.Idle)
	read.Grace, failed[3] = e.Duration("SHUTDOWN_GRACE", fallback.Grace)
	read.StopGrace, failed[4] = e.Duration("SHUTDOWN_STOP_GRACE", fallback.StopGrace)
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

// Serve serves srv until ctx ends or serving fails, then shuts it down and calls stop, each within its own grace.
func Serve(
	ctx context.Context, srv *http.Server, t Timeouts, stop func(context.Context) error, logger *slog.Logger,
) error {
	listener, err := net.Listen("tcp", cmp.Or(srv.Addr, ":http"))
	if err != nil {
		return errors.Join(fmt.Errorf("http server: %w", err), stopWithin(ctx, t, stop))
	}
	return serveOn(ctx, srv, listener, t, stop, logger)
}

// serveOn serves srv on listener until ctx ends or serving fails, then shuts it down and calls stop, each in its grace.
func serveOn(
	ctx context.Context, srv *http.Server, listener net.Listener, t Timeouts, stop func(context.Context) error,
	logger *slog.Logger,
) error {
	logger = cmp.Or(logger, slog.New(slog.DiscardHandler))
	if srv.ErrorLog == nil {
		srv.ErrorLog = slog.NewLogLogger(logger.Handler(), slog.LevelError)
	}
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
	grace, cancel := context.WithTimeout(context.WithoutCancel(ctx), t.Grace)
	defer cancel()
	shut := srv.Shutdown(grace)
	if failed == nil {
		<-served
	}
	return errors.Join(failed, shut, stopWithin(ctx, t, stop))
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
