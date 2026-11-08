// SPDX-License-Identifier: Apache-2.0

package pluginkit

import (
	"bufio"
	"maps"
	"net"
	"net/http"
	"runtime/debug"
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

// ServeHTTP serves the plugin's handler through a writer that notes whether the answer began, before or in it.
func (h *recovered) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	writer := &routeWriter{ResponseWriter: w, http1: r.ProtoMajor == 1, begun: begunUnder(w)}
	defer h.finish(writer, w.Header().Clone(), r)
	h.handler.ServeHTTP(writer, r)
}

// begunUnder reports whether the answer under w already began, as seen by the route writer w unwraps to.
func begunUnder(w http.ResponseWriter) bool {
	for {
		switch under := w.(type) {
		case *routeWriter:
			return under.begun
		case interface{ Unwrap() http.ResponseWriter }:
			w = under.Unwrap()
		default:
			return false
		}
	}
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
	h.settings.Report(RouteFailure{
		ID: h.id, Method: r.Method, Path: r.URL.Path, Answered: !writer.begun,
		Err: panicError("pluginkit: " + h.id + " route panicked: " + describe(caught)), Stack: string(debug.Stack()),
	})
	if writer.begun {
		panic(http.ErrAbortHandler)
	}
	clear(writer.Header())
	maps.Copy(writer.Header(), header)
	h.settings.Answer.ServeHTTP(writer.ResponseWriter, r)
}

// routeWriter is the writer a plugin's handler answers through, noting whether its answer began.
type routeWriter struct {
	http.ResponseWriter
	// http1 makes a 101 status begin the answer, as only HTTP/1 switches protocols with it.
	http1    bool
	begun    bool
	hijacked net.Conn
}

// WriteHeader writes the status, noting that the answer began unless the status is informational.
func (w *routeWriter) WriteHeader(status int) {
	w.ResponseWriter.WriteHeader(status)
	if w.beginsAnswer(status) {
		w.begun = true
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
	w.begun = true
	return w.ResponseWriter.Write(body)
}

// Flush sends what the handler wrote so far to the client.
func (w *routeWriter) Flush() {
	_ = w.FlushError()
}

// FlushError sends what the handler wrote so far, writing the status 200 first when nothing began the answer.
func (w *routeWriter) FlushError() error {
	if !w.begun {
		w.WriteHeader(http.StatusOK)
	}
	return http.NewResponseController(w.ResponseWriter).Flush()
}

// Hijack takes over the client's connection, noting that the answer began.
func (w *routeWriter) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	conn, buffered, err := http.NewResponseController(w.ResponseWriter).Hijack()
	if err == nil {
		w.begun, w.hijacked = true, conn
	}
	return conn, buffered, err
}

// Unwrap returns the server's writer, so a response controller reaches its deadlines and full duplex.
func (w *routeWriter) Unwrap() http.ResponseWriter {
	return w.ResponseWriter
}
