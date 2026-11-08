// SPDX-License-Identifier: Apache-2.0

package pluginkit_test

import (
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"slices"
	"sync"
	"testing"

	"github.com/gopherium/framework/pluginkit"
)

// routeReport keeps every failure a test's Report receives.
type routeReport struct {
	mu       sync.Mutex
	failures []pluginkit.RouteFailure
}

// report keeps the failure.
func (r *routeReport) report(failure pluginkit.RouteFailure) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.failures = append(r.failures, failure)
}

// held returns every failure kept so far.
func (r *routeReport) held() []pluginkit.RouteFailure {
	r.mu.Lock()
	defer r.mu.Unlock()
	return slices.Clone(r.failures)
}

// answerPanic is the tests' answer to a request whose plugin handler panicked.
func answerPanic(w http.ResponseWriter, _ *http.Request) {
	w.WriteHeader(http.StatusInternalServerError)
	_, _ = io.WriteString(w, "internal error")
}

// markWrapped is the tests' middleware, which sets X-Wrapped before it calls next.
func markWrapped(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Wrapped", "yes")
		next.ServeHTTP(w, r)
	})
}

// routeSettings returns the tests' route settings, reporting into report.
func routeSettings(report *routeReport) pluginkit.RouteSettings {
	return pluginkit.RouteSettings{Wrap: markWrapped, Answer: http.HandlerFunc(answerPanic), Report: report.report}
}

func protectFixture() http.Handler {
	plugin := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	return pluginkit.Protect("feed", plugin, []string{"/webhook", "/rss.xml"}, routeSettings(&routeReport{}))
}

func TestProtectLetsPublicPathsThroughUntouched(t *testing.T) {
	t.Parallel()

	for _, method := range []string{http.MethodGet, http.MethodPost} {
		recorder := httptest.NewRecorder()

		protectFixture().ServeHTTP(recorder, httptest.NewRequest(method, "/webhook", nil))

		if recorder.Code != http.StatusOK {
			t.Errorf("%s /webhook status = %d, want %d", method, recorder.Code, http.StatusOK)
		}
		if recorder.Header().Get("X-Wrapped") != "" {
			t.Errorf("%s /webhook was wrapped, want the public passthrough", method)
		}
	}
}

func TestProtectWrapsEverythingElse(t *testing.T) {
	t.Parallel()

	tests := map[string]string{
		"plain path":               "/messages",
		"trailing slash variant":   "/webhook/",
		"prefix of a public path":  "/rss",
		"public path as a prefix":  "/rss.xml/extra",
		"case-sensitive mismatch":  "/Webhook",
		"query does not affect it": "/other?path=/webhook",
	}

	for testName, path := range tests {
		t.Run(testName, func(t *testing.T) {
			t.Parallel()

			recorder := httptest.NewRecorder()

			protectFixture().ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, path, nil))

			if recorder.Header().Get("X-Wrapped") != "yes" {
				t.Errorf("GET %s was not wrapped, want the protecting middleware applied", path)
			}
		})
	}
}

func TestProtectRecoversAPanicOnAPublicPathWithoutTheMiddleware(t *testing.T) {
	t.Parallel()

	report := &routeReport{}
	plugin := http.HandlerFunc(func(http.ResponseWriter, *http.Request) { panicWith("boom") })
	recorder := httptest.NewRecorder()

	pluginkit.Protect("feed", plugin, []string{"/rss.xml"}, routeSettings(report)).
		ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/rss.xml", nil))

	if recorder.Code != http.StatusInternalServerError || recorder.Header().Get("X-Wrapped") != "" {
		t.Errorf("answered %d with X-Wrapped %q, want 500 unwrapped", recorder.Code, recorder.Header().Get("X-Wrapped"))
	}
	if failures := report.held(); len(failures) != 1 || failures[0].Path != "/rss.xml" {
		t.Errorf("reported %+v, want one failure for /rss.xml", failures)
	}
}

func TestProtectBuildsTheMiddlewareOnce(t *testing.T) {
	t.Parallel()

	built := 0
	settings := routeSettings(&routeReport{})
	settings.Wrap = func(next http.Handler) http.Handler {
		built++
		return next
	}
	protected := pluginkit.Protect("feed", http.NotFoundHandler(), nil, settings)

	for range 3 {
		protected.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/items", nil))
	}

	if built != 1 {
		t.Errorf("the middleware was built %d times, want once", built)
	}
}

func TestProtectLetsAPanicOfTheMiddlewareReachTheServer(t *testing.T) {
	t.Parallel()

	report := &routeReport{}
	settings := routeSettings(report)
	settings.Wrap = func(http.Handler) http.Handler {
		return http.HandlerFunc(func(http.ResponseWriter, *http.Request) { panicWith("session store down") })
	}
	protected := pluginkit.Protect("feed", http.NotFoundHandler(), nil, settings)

	escaped := serveCatching(protected, httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/items", nil))

	if escaped != "session store down" {
		t.Errorf("the server got the panic %v, want %q", escaped, "session store down")
	}
	if failures := report.held(); len(failures) != 0 {
		t.Errorf("reported %+v, want nothing for a panic of the middleware", failures)
	}
}

func TestProtectRefusesRouteSettingsItCannotServeWith(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		id     string
		change func(*pluginkit.RouteSettings)
		want   string
	}{
		"empty id": {
			change: func(*pluginkit.RouteSettings) {},
			want:   "pluginkit: Protect: the plugin id is empty",
		},
		"nil Wrap": {
			id: "feed", change: func(s *pluginkit.RouteSettings) { s.Wrap = nil },
			want: "pluginkit: Protect: RouteSettings.Wrap is nil",
		},
		"nil Answer": {
			id: "feed", change: func(s *pluginkit.RouteSettings) { s.Answer = nil },
			want: "pluginkit: Protect: RouteSettings.Answer is nil",
		},
		"nil HandlerFunc as Answer": {
			id: "feed", change: func(s *pluginkit.RouteSettings) { s.Answer = http.HandlerFunc(nil) },
			want: "pluginkit: Protect: RouteSettings.Answer is nil",
		},
		"nil Report": {
			id: "feed", change: func(s *pluginkit.RouteSettings) { s.Report = nil },
			want: "pluginkit: Protect: RouteSettings.Report is nil",
		},
	}

	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			settings := routeSettings(&routeReport{})
			test.change(&settings)

			got := protectPanic(test.id, settings)

			if fmt.Sprint(got) != test.want {
				t.Errorf("Protect panicked with %v, want %q", got, test.want)
			}
		})
	}
}

// protectPanic returns what Protect panicked with for the id and settings, nil when it did not.
func protectPanic(id string, settings pluginkit.RouteSettings) (escaped any) {
	defer func() { escaped = recover() }()
	pluginkit.Protect(id, http.NotFoundHandler(), nil, settings)
	return nil
}
