// SPDX-License-Identifier: Apache-2.0

package pluginkit_test

import (
	"bufio"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/gopherium/framework/pluginkit"
)

// panicWith panics with value, so the stack of a test's panic names it.
func panicWith(value any) {
	panic(value)
}

// serveCatching serves r through handler and returns what the handler panicked with, nil when it did not.
func serveCatching(handler http.Handler, w http.ResponseWriter, r *http.Request) (escaped any) {
	defer func() { escaped = recover() }()
	handler.ServeHTTP(w, r)
	return nil
}

// protectedFeed returns handler as the plugin "feed" serves it under the tests' settings, reporting into report.
func protectedFeed(handler http.Handler, report *routeReport) http.Handler {
	return pluginkit.Protect("feed", handler, nil, routeSettings(report))
}

// getItems returns a GET request for /items over HTTP/1.1.
func getItems() *http.Request {
	return httptest.NewRequest(http.MethodGet, "/items", nil)
}

// informedWriter is a recorder that sends a status below 200 aside, as a server sends an informational answer.
type informedWriter struct {
	*httptest.ResponseRecorder
	informed []int
}

// WriteHeader keeps a status below 200 aside and records any other.
func (w *informedWriter) WriteHeader(status int) {
	if status < http.StatusOK {
		w.informed = append(w.informed, status)
		return
	}
	w.ResponseRecorder.WriteHeader(status)
}

// hijackWriter is a recorder whose connection a handler can take over.
type hijackWriter struct {
	*httptest.ResponseRecorder
	conn net.Conn
	err  error
}

// Hijack hands over the writer's connection, or its error.
func (w *hijackWriter) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	if w.err != nil {
		return nil, nil, w.err
	}
	return w.conn, nil, nil
}

// deadlineWriter is a recorder that keeps the read deadline a handler sets.
type deadlineWriter struct {
	*httptest.ResponseRecorder
	deadline time.Time
}

// SetReadDeadline keeps the deadline.
func (w *deadlineWriter) SetReadDeadline(deadline time.Time) error {
	w.deadline = deadline
	return nil
}

// bareWriter is a writer that can do nothing but answer.
type bareWriter struct {
	header http.Header
	status int
}

// Header returns the writer's header.
func (w *bareWriter) Header() http.Header {
	return w.header
}

// Write takes the body and drops it.
func (w *bareWriter) Write(body []byte) (int, error) {
	return len(body), nil
}

// WriteHeader keeps the status.
func (w *bareWriter) WriteHeader(status int) {
	w.status = status
}

func TestRecoverAnswersAPanicBeforeTheAnswerBegan(t *testing.T) {
	t.Parallel()

	report := &routeReport{}
	plugin := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("X-Feed", "plugin")
		w.Header().Add("X-Wrapped", "plugin")
		http.SetCookie(w, &http.Cookie{Name: "feed", Value: "plugin"})
		panicWith("boom")
	})
	recorder := httptest.NewRecorder()

	escaped := serveCatching(protectedFeed(plugin, report), recorder, httptest.NewRequest(http.MethodPost, "/items", nil))

	answer := recorder.Result()
	if escaped != nil || answer.StatusCode != http.StatusInternalServerError ||
		recorder.Body.String() != "internal error" {
		t.Fatalf("answered %d %q with the panic %v, want the application's answer", answer.StatusCode,
			recorder.Body.String(), escaped)
	}
	if answer.Header.Get("X-Feed") != "" || len(answer.Cookies()) != 0 ||
		!slices.Equal(answer.Header.Values("X-Wrapped"), []string{"yes"}) {
		t.Errorf("the answer holds %v, want only the middleware's header", answer.Header)
	}
	failures := report.held()
	if len(failures) != 1 {
		t.Fatalf("reported %+v, want one failure", failures)
	}
	got := failures[0]
	if got.ID != "feed" || got.Method != http.MethodPost || got.Path != "/items" || !got.Answered ||
		got.Err == nil || got.Err.Error() != "pluginkit: feed route panicked: boom" {
		t.Errorf("reported %+v, want the answered panic of feed for POST /items", got)
	}
	if !strings.Contains(got.Stack, "pluginkit_test.panicWith") {
		t.Errorf("the stack does not hold the panic:\n%s", got.Stack)
	}
}

func TestRecoverCutsAnAnswerThatBegan(t *testing.T) {
	t.Parallel()

	tests := map[string]func(http.ResponseWriter){
		"a status 200":       func(w http.ResponseWriter) { w.WriteHeader(http.StatusOK) },
		"a body":             func(w http.ResponseWriter) { _, _ = io.WriteString(w, "partial") },
		"an empty write":     func(w http.ResponseWriter) { _, _ = w.Write(nil) },
		"a flush":            func(w http.ResponseWriter) { w.(http.Flusher).Flush() },
		"a controller flush": func(w http.ResponseWriter) { _ = http.NewResponseController(w).Flush() },
		"a 101 over HTTP/1":  func(w http.ResponseWriter) { w.WriteHeader(http.StatusSwitchingProtocols) },
	}

	for name, begin := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			report := &routeReport{}
			plugin := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				begin(w)
				panicWith("boom")
			})
			writer := &informedWriter{ResponseRecorder: httptest.NewRecorder()}

			escaped := serveCatching(protectedFeed(plugin, report), writer, getItems())

			if escaped != http.ErrAbortHandler {
				t.Errorf("the server got the panic %v, want http.ErrAbortHandler", escaped)
			}
			if failures := report.held(); len(failures) != 1 || failures[0].Answered {
				t.Errorf("reported %+v, want one failure not answered", failures)
			}
		})
	}
}

func TestRecoverAnswersAfterAnInformationalStatus(t *testing.T) {
	t.Parallel()

	tests := map[string]struct{ status, proto int }{
		"a 103 over HTTP/1": {http.StatusEarlyHints, 1},
		"a 103 over HTTP/2": {http.StatusEarlyHints, 2},
		"a 101 over HTTP/2": {http.StatusSwitchingProtocols, 2},
	}

	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			report := &routeReport{}
			plugin := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(test.status)
				panicWith("boom")
			})
			writer := &informedWriter{ResponseRecorder: httptest.NewRecorder()}
			request := getItems()
			request.ProtoMajor = test.proto

			escaped := serveCatching(protectedFeed(plugin, report), writer, request)

			if escaped != nil || writer.Code != http.StatusInternalServerError {
				t.Errorf("answered %d with the panic %v, want the application's answer", writer.Code, escaped)
			}
			if !slices.Equal(writer.informed, []int{test.status}) {
				t.Errorf("informed %v, want %d", writer.informed, test.status)
			}
			if failures := report.held(); len(failures) != 1 || !failures[0].Answered {
				t.Errorf("reported %+v, want one answered failure", failures)
			}
		})
	}
}

func TestRecoverAnswersAStatusTheServerRefused(t *testing.T) {
	t.Parallel()

	report := &routeReport{}
	plugin := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(42) })
	recorder := httptest.NewRecorder()

	escaped := serveCatching(protectedFeed(plugin, report), recorder, getItems())

	if escaped != nil || recorder.Code != http.StatusInternalServerError {
		t.Errorf("answered %d with the panic %v, want the application's answer", recorder.Code, escaped)
	}
	want := "pluginkit: feed route panicked: invalid WriteHeader code 42"
	if failures := report.held(); len(failures) != 1 || failures[0].Err.Error() != want {
		t.Errorf("reported %+v, want %q", failures, want)
	}
}

func TestRecoverClosesAConnectionTheHandlerHijacked(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		value    any
		reported int
	}{
		"before a panic":  {value: "boom", reported: 1},
		"before an abort": {value: http.ErrAbortHandler, reported: 0},
	}

	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			server, client := net.Pipe()
			t.Cleanup(func() { _ = client.Close() })
			report := &routeReport{}
			plugin := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				if _, _, err := http.NewResponseController(w).Hijack(); err != nil {
					t.Errorf("Hijack failed: %v", err)
				}
				panicWith(test.value)
			})
			writer := &hijackWriter{ResponseRecorder: httptest.NewRecorder(), conn: server}

			escaped := serveCatching(protectedFeed(plugin, report), writer, getItems())

			if escaped != http.ErrAbortHandler {
				t.Errorf("the server got the panic %v, want http.ErrAbortHandler", escaped)
			}
			_ = client.SetReadDeadline(time.Now().Add(time.Second))
			if _, err := client.Read(make([]byte, 1)); !errors.Is(err, io.EOF) {
				t.Errorf("reading the hijacked connection answered %v, want io.EOF", err)
			}
			if failures := report.held(); len(failures) != test.reported || slices.ContainsFunc(failures,
				func(failure pluginkit.RouteFailure) bool { return failure.Answered }) {
				t.Errorf("reported %+v, want %d failures not answered", failures, test.reported)
			}
		})
	}
}

func TestRecoverAnswersAPanicAfterAHijackThatFailed(t *testing.T) {
	t.Parallel()

	report := &routeReport{}
	plugin := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if _, _, err := http.NewResponseController(w).Hijack(); err == nil {
			t.Error("Hijack worked, want the writer's error")
		}
		panicWith("boom")
	})
	writer := &hijackWriter{ResponseRecorder: httptest.NewRecorder(), err: errors.New("the connection is gone")}

	escaped := serveCatching(protectedFeed(plugin, report), writer, getItems())

	if escaped != nil || writer.Code != http.StatusInternalServerError {
		t.Errorf("answered %d with the panic %v, want the application's answer", writer.Code, escaped)
	}
	if failures := report.held(); len(failures) != 1 || !failures[0].Answered {
		t.Errorf("reported %+v, want one answered failure", failures)
	}
}

func TestRecoverLeavesAnAbortToTheServer(t *testing.T) {
	t.Parallel()

	report := &routeReport{}
	plugin := http.HandlerFunc(func(http.ResponseWriter, *http.Request) { panicWith(http.ErrAbortHandler) })
	recorder := httptest.NewRecorder()

	escaped := serveCatching(protectedFeed(plugin, report), recorder, getItems())

	if escaped != http.ErrAbortHandler {
		t.Errorf("the server got the panic %v, want http.ErrAbortHandler", escaped)
	}
	if recorder.Body.Len() != 0 || recorder.Result().StatusCode != http.StatusOK {
		t.Errorf("answered %d %q, want nothing written", recorder.Code, recorder.Body.String())
	}
	if failures := report.held(); len(failures) != 0 {
		t.Errorf("reported %+v, want nothing for an abort", failures)
	}
}

func TestRecoverAnswersAPluginWithNoHandler(t *testing.T) {
	t.Parallel()

	report := &routeReport{}
	recorder := httptest.NewRecorder()

	escaped := serveCatching(protectedFeed(nil, report), recorder, getItems())

	if escaped != nil || recorder.Code != http.StatusInternalServerError {
		t.Errorf("answered %d with the panic %v, want the application's answer", recorder.Code, escaped)
	}
	want := "pluginkit: feed route panicked: runtime error: invalid memory address or nil pointer dereference"
	if failures := report.held(); len(failures) != 1 || failures[0].Err.Error() != want {
		t.Errorf("reported %+v, want %q", failures, want)
	}
}

func TestRecoverLeavesAnAnswerThatDidNotPanicAlone(t *testing.T) {
	t.Parallel()

	report := &routeReport{}
	plugin := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("X-Feed", "plugin")
		w.WriteHeader(http.StatusCreated)
		_, _ = io.WriteString(w, "created")
	})
	recorder := httptest.NewRecorder()

	escaped := serveCatching(protectedFeed(plugin, report), recorder, getItems())

	if escaped != nil || recorder.Code != http.StatusCreated || recorder.Body.String() != "created" ||
		recorder.Header().Get("X-Feed") != "plugin" {
		t.Errorf("answered %d %q %v with the panic %v, want the plugin's answer", recorder.Code,
			recorder.Body.String(), recorder.Header(), escaped)
	}
	if failures := report.held(); len(failures) != 0 {
		t.Errorf("reported %+v, want nothing", failures)
	}
}

func TestRecoverSetsAReadDeadlineOnTheServersWriter(t *testing.T) {
	t.Parallel()

	at := time.Date(2026, 11, 8, 12, 0, 0, 0, time.UTC)
	plugin := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if err := http.NewResponseController(w).SetReadDeadline(at); err != nil {
			t.Errorf("SetReadDeadline answered %v, want nil", err)
		}
	})
	writer := &deadlineWriter{ResponseRecorder: httptest.NewRecorder()}

	protectedFeed(plugin, &routeReport{}).ServeHTTP(writer, getItems())

	if !writer.deadline.Equal(at) {
		t.Errorf("the server's writer holds the deadline %v, want %v", writer.deadline, at)
	}
}

func TestRecoverFlushesThroughTheServersWriter(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		status int
		want   int
	}{
		"before any status": {want: http.StatusOK},
		"after a status":    {status: http.StatusAccepted, want: http.StatusAccepted},
	}

	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			plugin := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				if test.status != 0 {
					w.WriteHeader(test.status)
				}
				w.(http.Flusher).Flush()
			})
			recorder := httptest.NewRecorder()

			protectedFeed(plugin, &routeReport{}).ServeHTTP(recorder, getItems())

			if !recorder.Flushed || recorder.Code != test.want {
				t.Errorf("flushed %t with %d, want flushed with %d", recorder.Flushed, recorder.Code, test.want)
			}
		})
	}
}

func TestRecoverAnswersTheErrorOfAWriterThatCannotFlush(t *testing.T) {
	t.Parallel()

	plugin := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if err := http.NewResponseController(w).Flush(); !errors.Is(err, http.ErrNotSupported) {
			t.Errorf("Flush answered %v, want http.ErrNotSupported", err)
		}
	})
	writer := &bareWriter{header: http.Header{}}

	protectedFeed(plugin, &routeReport{}).ServeHTTP(writer, getItems())

	if writer.status != http.StatusOK {
		t.Errorf("the server's writer holds %d, want the 200 the flush began", writer.status)
	}
}

func BenchmarkProtectServesARoute(b *testing.B) {
	plugin := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) })
	protected := protectedFeed(plugin, &routeReport{})
	writer := &bareWriter{header: http.Header{}}
	request := getItems()

	b.ReportAllocs()
	for b.Loop() {
		protected.ServeHTTP(writer, request)
	}
}
