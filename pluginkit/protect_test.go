// SPDX-License-Identifier: Apache-2.0

package pluginkit_test

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gopherium/framework/pluginkit"
)

func protectFixture() http.Handler {
	plugin := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	wrap := func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("X-Wrapped", "yes")
			next.ServeHTTP(w, r)
		})
	}
	return pluginkit.Protect(plugin, []string{"/webhook", "/rss.xml"}, wrap)
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
