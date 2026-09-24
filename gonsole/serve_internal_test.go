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
	"sync/atomic"
	"testing"
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
	timeouts := Timeouts{ReadHeader: time.Second, Read: time.Second, Idle: time.Minute, Grace: 5 * time.Second}
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

	if !strings.HasPrefix(errorLine(served), "http server: the listener broke") {
		t.Errorf("serveOn() = %v, want the serving failure", served)
	}
	if second == nil || late.Load() != 0 {
		t.Errorf("a request after the failure = %v, handled after stop %d times, want none handled", second, late.Load())
	}
	if strings.Contains(logged.String(), "shutting down") {
		t.Errorf("log = %q, want no shutting down line after a failure", logged.String())
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
