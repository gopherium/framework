// SPDX-License-Identifier: Apache-2.0

package gospa_test

import (
	"io/fs"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"github.com/gopherium/framework/gospa"
)

// page is the page of the test apps.
const page = `<!doctype html><html><head><title>App</title></head><body><div id="root"></div></body></html>`

// builtApp returns a dated build holding the page and each name with its own name as content.
func builtApp(names ...string) fstest.MapFS {
	build := dated(names...)
	build["index.html"] = &fstest.MapFile{Data: []byte(page), ModTime: built}
	return build
}

// caselessFS wraps a folder that opens a name in any case, as on macOS and Windows.
type caselessFS struct {
	fstest.MapFS
}

// Open opens name in lower case.
func (c caselessFS) Open(name string) (fs.File, error) {
	return c.MapFS.Open(strings.ToLower(name))
}

// newApp returns the handler App gives for build and opts, failing the test when App fails.
func newApp(t *testing.T, build fstest.MapFS, opts gospa.AppOptions) http.Handler {
	t.Helper()
	handler, err := gospa.App(build, opts)
	if err != nil {
		t.Fatalf("App() = %v", err)
	}
	return handler
}

// isPage checks that answer sends want as the page, checked on every load and dated by nothing.
func isPage(t *testing.T, target string, answer *httptest.ResponseRecorder, want string) {
	t.Helper()
	header := answer.Header()
	if answer.Code != http.StatusOK || answer.Body.String() != want {
		t.Errorf("GET %s = %d %q, want 200 with the page %q", target, answer.Code, answer.Body, want)
	}
	for key, value := range map[string]string{
		"Content-Type":           "text/html; charset=utf-8",
		"Cache-Control":          "no-cache",
		"X-Content-Type-Options": "nosniff",
	} {
		if got := header.Get(key); got != value {
			t.Errorf("GET %s %s = %q, want %q", target, key, got, value)
		}
	}
	if !absent(header, "Last-Modified") {
		t.Errorf("GET %s Last-Modified = %q, want none", target, header.Values("Last-Modified"))
	}
	if tag := header.Get("Etag"); !strongTag.MatchString(tag) {
		t.Errorf("GET %s Etag = %q, want a tag of 32 hex digits in quotes", target, tag)
	}
}

func TestAppSendsThePageForEveryPathThatNamesNoFile(t *testing.T) {
	t.Parallel()

	handler := newApp(t, builtApp("favicon.svg", "images/logo.png"), gospa.AppOptions{})
	for _, target := range []string{
		"/", "/posts/42", "/settings/", "/images", "/images/", "/images/missing.png", "/favicon.svg/",
		"/index.html", "/.well-known/missing", "/assets-old/app.js", "/assetsfoo", "/docs/assets/missing.js",
	} {
		answer := ask(handler, http.MethodGet, target)
		isPage(t, target, answer, page)
	}
}

func TestAppSendsThePageWithItsValuesForIndexHTMLInAnyCase(t *testing.T) {
	t.Parallel()

	build := caselessFS{MapFS: builtApp()}
	handler, err := gospa.App(build, gospa.AppOptions{Meta: []gospa.Meta{{Name: "api-root", Content: "/api/"}}})
	if err != nil {
		t.Fatalf("App() = %v", err)
	}
	want := strings.Replace(page, "<head>", `<head><meta name="api-root" content="/api/">`, 1)
	for _, target := range []string{"/index.html", "/INDEX.HTML", "/Index.html"} {
		isPage(t, target, ask(handler, http.MethodGet, target), want)
	}
}

func TestAppSendsThePageForAnEmptyPath(t *testing.T) {
	t.Parallel()

	handler := newApp(t, builtApp(), gospa.AppOptions{})
	answer := ask(http.StripPrefix("/admin", handler), http.MethodGet, "/admin")
	isPage(t, "/admin", answer, page)
}

func TestAppSendsAFileTheBuildWrote(t *testing.T) {
	t.Parallel()

	handler := newApp(t, builtApp("images/logo.png", "docs/index.html", ".well-known/security.txt"), gospa.AppOptions{})
	for target, kind := range map[string]string{
		"/images/logo.png":          "image/png",
		"/docs/index.html":          "text/html; charset=utf-8",
		"/.well-known/security.txt": "text/plain; charset=utf-8",
	} {
		answer := ask(handler, http.MethodGet, target)
		if answer.Code != http.StatusOK || answer.Body.String() != "content of "+target[1:] {
			t.Errorf("GET %s = %d %q, want 200 with the file", target, answer.Code, answer.Body)
		}
		if got := answer.Header().Get("Content-Type"); got != kind {
			t.Errorf("GET %s Content-Type = %q, want %q", target, got, kind)
		}
		if got := answer.Header().Get("Last-Modified"); got != "Sun, 01 Nov 2026 10:00:00 GMT" {
			t.Errorf("GET %s Last-Modified = %q, want the date of the file", target, got)
		}
	}
}

func TestAppAnswersNotFoundUnderAssetsForAPathThatNamesNoFile(t *testing.T) {
	t.Parallel()

	handler := newApp(t, builtApp("assets/index-DWLUpakq.js", "assets/fonts/inter-Bx3kP9qa.woff2"), gospa.AppOptions{})
	for _, target := range []string{
		"/assets/index-0ldHa5h1.js", "/assets/", "/assets", "/assets/fonts", "/assets/fonts/", "/assets/index-DWLUpakq.js/",
	} {
		answer := ask(handler, http.MethodGet, target)
		if answer.Code != http.StatusNotFound || strings.Contains(answer.Body.String(), "<html>") {
			t.Errorf("GET %s = %d %q, want 404", target, answer.Code, answer.Body)
		}
	}
}

func TestAppLetsABrowserKeepAHashedFileForTheWindow(t *testing.T) {
	t.Parallel()

	build := builtApp(
		"assets/index-DWLUpakq.js", "assets/vendor-a_B-9xYz.js", "assets/chunks/posts-0ldHa5h1.js",
		"assets/index-DWLUpakq.js.map", "assets/index-DWLUpak.js", "assets/logo.svg", "assets/index-DWLUpakq",
		"assets/index-DWLUpakqX.js", "docs/assets/index-DWLUpakq.js", "favicon-DWLUpakq.svg", "favicon.svg",
	)
	cases := map[time.Duration]string{
		0:                            "public, max-age=31536000, immutable",
		time.Hour:                    "public, max-age=3600, immutable",
		90*time.Minute + time.Second: "public, max-age=5401, immutable",
		1500 * time.Millisecond:      "public, max-age=1, immutable",
	}
	for window, hashed := range cases {
		handler := newApp(t, build, gospa.AppOptions{HashedMaxAge: window})
		for target, want := range map[string]string{
			"/assets/index-DWLUpakq.js":        hashed,
			"/assets/vendor-a_B-9xYz.js":       hashed,
			"/assets/chunks/posts-0ldHa5h1.js": hashed,
			"/assets/index-DWLUpakq.js.map":    hashed,
			"/assets/index-DWLUpak.js":         "no-cache",
			"/assets/logo.svg":                 "no-cache",
			"/assets/index-DWLUpakq":           "no-cache",
			"/assets/index-DWLUpakqX.js":       "no-cache",
			"/docs/assets/index-DWLUpakq.js":   "no-cache",
			"/favicon-DWLUpakq.svg":            "no-cache",
			"/favicon.svg":                     "no-cache",
			"/":                                "no-cache",
			"/posts/42":                        "no-cache",
		} {
			if got := ask(handler, http.MethodGet, target).Header().Get("Cache-Control"); got != want {
				t.Errorf("GET %s with the window %v Cache-Control = %q, want %q", target, window, got, want)
			}
		}
	}
	if gospa.DefaultHashedMaxAge != 365*24*time.Hour {
		t.Errorf("DefaultHashedMaxAge = %v, want one year", gospa.DefaultHashedMaxAge)
	}
}

func TestAppReadsThePageAgainEachTimeItIsSent(t *testing.T) {
	t.Parallel()

	build := builtApp()
	handler := newApp(t, build, gospa.AppOptions{})
	first := ask(handler, http.MethodGet, "/posts/42")
	build["index.html"] = &fstest.MapFile{Data: []byte(`<!doctype html><title>Rebuilt</title>`), ModTime: built}
	again := ask(handler, http.MethodGet, "/posts/42", "If-None-Match", first.Header().Get("Etag"))
	isPage(t, "/posts/42", again, `<!doctype html><title>Rebuilt</title>`)
}

func TestAppAnswersAServerErrorWhenThePageIsGone(t *testing.T) {
	t.Parallel()

	build := builtApp()
	handler := newApp(t, build, gospa.AppOptions{})
	delete(build, "index.html")
	answer := ask(handler, http.MethodGet, "/posts/42")
	if answer.Code != http.StatusInternalServerError || strings.Contains(answer.Body.String(), "index.html") {
		t.Errorf("GET /posts/42 with no page = %d %q, want 500 naming no file", answer.Code, answer.Body)
	}
}

func TestAppChecksThePageByATagOfTheBytesSentAndNeverByItsDate(t *testing.T) {
	t.Parallel()

	handler := newApp(t, builtApp(), gospa.AppOptions{})
	first := ask(handler, http.MethodGet, "/posts/42")
	tag := first.Header().Get("Etag")
	checked := ask(handler, http.MethodGet, "/posts/42", "If-None-Match", tag)
	if checked.Code != http.StatusNotModified || checked.Body.Len() != 0 {
		t.Errorf("GET /posts/42 with its tag = %d %q, want 304 with no body", checked.Code, checked.Body)
	}
	later := built.Add(time.Hour).Format(http.TimeFormat)
	dated := ask(handler, http.MethodGet, "/posts/42", "If-Modified-Since", later)
	isPage(t, "/posts/42", dated, page)
}

func TestAppWritesItsValuesIntoThePage(t *testing.T) {
	t.Parallel()

	values := []gospa.Meta{{Name: "api-root", Content: "/api/"}, {Name: "write-timeout", Content: "30"}}
	handler := newApp(t, builtApp("help.html"), gospa.AppOptions{Meta: values})
	want := `<!doctype html><html><head><meta name="api-root" content="/api/"><meta name="write-timeout" content="30">` +
		`<title>App</title></head><body><div id="root"></div></body></html>`
	for _, target := range []string{"/", "/posts/42", "/index.html"} {
		answer := ask(handler, http.MethodGet, target)
		isPage(t, target, answer, want)
	}
	if got := ask(handler, http.MethodGet, "/help.html").Body.String(); got != "content of help.html" {
		t.Errorf("GET /help.html = %q, want the file as built", got)
	}
}

func TestAppTagsThePageAfreshWhenItsValuesChange(t *testing.T) {
	t.Parallel()

	build := builtApp()
	before := newApp(t, build, gospa.AppOptions{Meta: []gospa.Meta{{Name: "write-timeout", Content: "30"}}})
	tag := ask(before, http.MethodGet, "/").Header().Get("Etag")
	after := newApp(t, build, gospa.AppOptions{Meta: []gospa.Meta{{Name: "write-timeout", Content: "60"}}})
	answer := ask(after, http.MethodGet, "/", "If-None-Match", tag)
	want := strings.Replace(page, "<head>", `<head><meta name="write-timeout" content="60">`, 1)
	isPage(t, "/", answer, want)
}

func TestAppAnswersHeadWithTheHeadersOfThePage(t *testing.T) {
	t.Parallel()

	handler := newApp(t, builtApp(), gospa.AppOptions{})
	answer := ask(handler, http.MethodHead, "/posts/42")
	isPage(t, "HEAD /posts/42", answer, "")
	if got := answer.Header().Get("Content-Length"); got != strconv.Itoa(len(page)) {
		t.Errorf("HEAD /posts/42 Content-Length = %q, want %d", got, len(page))
	}
}

func TestAppAnswersOnlyGetAndHead(t *testing.T) {
	t.Parallel()

	handler := newApp(t, builtApp("favicon.svg"), gospa.AppOptions{NotFound: callersNotFound})
	for _, method := range []string{http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete} {
		for _, target := range []string{"/", "/posts/42", "/favicon.svg", "/assets/index-0ldHa5h1.js", "/.env"} {
			answer := ask(handler, method, target)
			if answer.Code != http.StatusMethodNotAllowed || answer.Header().Get("Allow") != "GET, HEAD" {
				t.Errorf("%s %s = %d allowing %q, want 405 allowing GET, HEAD",
					method, target, answer.Code, answer.Header().Get("Allow"))
			}
		}
	}
}

func TestAppNeverSendsAHiddenFileOrAPathWithAnEmptySegment(t *testing.T) {
	t.Parallel()

	build := builtApp(".env", ".git/config", "assets/index-DWLUpakq.js", "GIT~1/config", "index.html.", "INDEX~1.HTM")
	handler := newApp(t, build, gospa.AppOptions{})
	for _, target := range []string{
		"/.env", "/.git/config", "/.git/", "/../go.mod", "/%2e%2e/go.mod", "/posts/../.env",
		"//assets/index-DWLUpakq.js", "/posts//42", "//",
		"/GIT~1/config", "/index.html.", "/index.html%20", "/INDEX~1.HTM", "/posts/~42",
	} {
		answer := ask(handler, http.MethodGet, target)
		if answer.Code != http.StatusNotFound || answer.Body.String() == page {
			t.Errorf("GET %s = %d %q, want 404", target, answer.Code, answer.Body)
		}
	}
}

func TestAppSendsTheCallersNotFound(t *testing.T) {
	t.Parallel()

	handler := newApp(t, builtApp(".env"), gospa.AppOptions{NotFound: callersNotFound})
	for _, target := range []string{"/assets/index-0ldHa5h1.js", "/assets/", "/.env", "/posts//42"} {
		answer := ask(handler, http.MethodGet, target)
		if answer.Code != http.StatusTeapot || answer.Body.String() != "the caller's own answer" {
			t.Errorf("GET %s = %d %q, want the caller's own answer", target, answer.Code, answer.Body)
		}
	}
}

func TestAppIsNotSetUpWithoutWhatItNeeds(t *testing.T) {
	t.Parallel()

	folder := builtApp()
	folder["index.html"] = &fstest.MapFile{Mode: fs.ModeDir | 0o755}
	cases := []struct {
		name  string
		build fstest.MapFS
		opts  gospa.AppOptions
		want  string
	}{
		{name: "a build with no page", build: dated("favicon.svg"), want: "index.html"},
		{name: "a page that is a folder", build: folder, want: "index.html"},
		{
			name: "a value with no name",
			opts: gospa.AppOptions{Meta: []gospa.Meta{{Content: "30"}}},
			want: "gospa: page value 0 has no name",
		},
		{
			name: "a name given twice",
			opts: gospa.AppOptions{Meta: []gospa.Meta{{Name: "a", Content: "1"}, {Name: "a", Content: "2"}}},
			want: `gospa: page value "a" is given twice`,
		},
		{
			name: "a window below one second",
			opts: gospa.AppOptions{HashedMaxAge: 500 * time.Millisecond},
			want: "gospa: HashedMaxAge 500ms is below one second",
		},
		{
			name: "a negative window",
			opts: gospa.AppOptions{HashedMaxAge: -time.Hour},
			want: "gospa: HashedMaxAge -1h0m0s is below one second",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			build := tc.build
			if build == nil {
				build = builtApp()
			}
			handler, err := gospa.App(build, tc.opts)
			if err == nil || handler != nil {
				t.Fatalf("App() = %v, %v, want an error holding %q", handler, err, tc.want)
			}
			if !strings.Contains(err.Error(), tc.want) || !strings.HasPrefix(err.Error(), "gospa: ") {
				t.Errorf("App() = %q, want a gospa error holding %q", err, tc.want)
			}
		})
	}
}
