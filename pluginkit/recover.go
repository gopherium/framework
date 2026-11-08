// SPDX-License-Identifier: Apache-2.0

package pluginkit

import (
	"bufio"
	"maps"
	"net"
	"net/http"
	"runtime/debug"
	"sync/atomic"
)

// RouteFailure is one panic of one plugin's handler while it served one request.
type RouteFailure struct {
	ID     string
	Method string
	// Path is the request path the handler saw.
	Path string
	// Answered reports whether RouteSettings.Answer wrote the answer, false when the plugin's own had begun.
	Answered bool
	// Err names the plugin and holds the text of the panic.
	Err error
	// Stack is the stack of the panic.
	Stack string
}

// recovered serves one plugin's handler, reporting each panic of it and answering it while nothing went out.
type recovered struct {
	id       string
	handler  http.Handler
	settings RouteSettings
}

// middlewareWriter is the context key of the writer Protect hands the application's middleware.
type middlewareWriter struct{}

// ServeHTTP serves the plugin's handler through a writer that notes whether the plugin's answer began.
func (h *recovered) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	writer := &routeWriter{ResponseWriter: w, http1: r.ProtoMajor == 1}
	defer h.finish(writer, w.Header().Clone(), r)
	h.handler.ServeHTTP(writer, r)
}

// begunBefore reports whether the writer Protect handed the middleware serving r saw the answer begin.
func begunBefore(r *http.Request) bool {
	under, ok := r.Context().Value(middlewareWriter{}).(*routeWriter)
	return ok && under.begun.Load()
}

// finish recovers a panic of the plugin's handler, reports it, then answers it or cuts the answer the plugin began.
func (h *recovered) finish(writer *routeWriter, header http.Header, r *http.Request) {
	caught := recover()
	if caught == nil {
		return
	}
	if writer.hijacked != nil {
		_ = writer.hijacked.Close()
	}
	if caught == http.ErrAbortHandler {
		panic(caught)
	}
	begun := writer.begun.Load() || begunBefore(r)
	h.settings.Report(RouteFailure{
		ID: h.id, Method: r.Method, Path: r.URL.Path, Answered: !begun,
		Err: panicError("pluginkit: " + h.id + " route panicked: " + describe(caught)), Stack: string(debug.Stack()),
	})
	if begun {
		panic(http.ErrAbortHandler)
	}
	clear(writer.Header())
	maps.Copy(writer.Header(), header)
	h.settings.Answer.ServeHTTP(writer.ResponseWriter, r)
}

// routeWriter is the writer a plugin's handler, or the middleware before it, answers through, noting when it began.
type routeWriter struct {
	http.ResponseWriter
	// http1 makes a 101 status begin the answer, as only HTTP/1 switches protocols with it.
	http1    bool
	begun    atomic.Bool
	hijacked net.Conn
}

// WriteHeader writes the status, noting that the answer began unless the status is informational.
func (w *routeWriter) WriteHeader(status int) {
	w.ResponseWriter.WriteHeader(status)
	if w.beginsAnswer(status) {
		w.begun.Store(true)
	}
}

// beginsAnswer reports whether status begins the answer: a final status, or a 101 over HTTP/1.
func (w *routeWriter) beginsAnswer(status int) bool {
	if status >= http.StatusOK {
		return true
	}
	return status == http.StatusSwitchingProtocols && w.http1
}

// Write writes part of the body, noting that the answer began.
func (w *routeWriter) Write(body []byte) (int, error) {
	w.begun.Store(true)
	return w.ResponseWriter.Write(body)
}

// Flush sends what the handler wrote so far to the client.
func (w *routeWriter) Flush() {
	_ = w.FlushError()
}

// FlushError sends what the handler wrote so far, writing the status 200 first when nothing began the answer.
func (w *routeWriter) FlushError() error {
	if !w.begun.Load() {
		w.WriteHeader(http.StatusOK)
	}
	return http.NewResponseController(w.ResponseWriter).Flush()
}

// Hijack takes over the client's connection, noting that the answer began.
func (w *routeWriter) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	conn, buffered, err := http.NewResponseController(w.ResponseWriter).Hijack()
	if err == nil {
		w.begun.Store(true)
		w.hijacked = conn
	}
	return conn, buffered, err
}

// Unwrap returns the server's writer, so a response controller reaches its deadlines and full duplex.
func (w *routeWriter) Unwrap() http.ResponseWriter {
	return w.ResponseWriter
}
