// SPDX-License-Identifier: Apache-2.0

package gospa_test

import (
	"crypto/sha256"
	"encoding/hex"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"testing/fstest"
	"time"

	"github.com/gopherium/framework/gospa"
)

func TestFilesSendsAFileWithItsTypeCheckedOnEveryLoad(t *testing.T) {
	t.Parallel()

	handler := gospa.Files(dated("images/logo.png"), gospa.FilesOptions{})
	answer := ask(handler, http.MethodGet, "/images/logo.png")

	want := map[string]string{
		"Content-Type":           "image/png",
		"Cache-Control":          "no-cache",
		"X-Content-Type-Options": "nosniff",
		"Last-Modified":          "Sun, 01 Nov 2026 10:00:00 GMT",
	}
	if answer.Code != http.StatusOK || answer.Body.String() != "content of images/logo.png" {
		t.Errorf("GET /images/logo.png = %d %q, want 200 with the file", answer.Code, answer.Body)
	}
	for key, value := range want {
		if got := answer.Header().Get(key); got != value {
			t.Errorf("GET /images/logo.png %s = %q, want %q", key, got, value)
		}
	}
	if !absent(answer.Header(), "Etag") {
		t.Errorf("GET /images/logo.png Etag = %q, want none", answer.Header().Values("Etag"))
	}
}

func TestFilesSendsOnlyRegularFiles(t *testing.T) {
	t.Parallel()

	folder := fstest.MapFS{
		"link.png": {Data: []byte("logo.png"), Mode: fs.ModeSymlink, ModTime: built},
		"device":   {Mode: fs.ModeDevice, ModTime: built},
		"pipe":     {Mode: fs.ModeNamedPipe, ModTime: built},
		"socket":   {Mode: fs.ModeSocket, ModTime: built},
	}
	handler := gospa.Files(folder, gospa.FilesOptions{})
	for _, target := range []string{"/link.png", "/device", "/pipe", "/socket"} {
		if answer := ask(handler, http.MethodGet, target); answer.Code != http.StatusNotFound {
			t.Errorf("GET %s = %d %q, want 404", target, answer.Code, answer.Body)
		}
	}
}

func TestFilesSendsOnlyRegularFilesOfAFolderThatOnlyOpensNames(t *testing.T) {
	t.Parallel()

	handler := gospa.Files(openOnlyFS{folder: dated("logo.png", "docs/guide.pdf")}, gospa.FilesOptions{})
	if answer := ask(handler, http.MethodGet, "/logo.png"); answer.Code != http.StatusOK {
		t.Errorf("GET /logo.png = %d %q, want 200 with the file", answer.Code, answer.Body)
	}
	for _, target := range []string{"/docs", "/missing.png"} {
		if answer := ask(handler, http.MethodGet, target); answer.Code != http.StatusNotFound {
			t.Errorf("GET %s = %d %q, want 404", target, answer.Code, answer.Body)
		}
	}
}

func TestFilesAnswersNotFoundForANameAFolderCouldNotHold(t *testing.T) {
	t.Parallel()

	handler := gospa.Files(lenientFS{MapFS: dated("logo.png")}, gospa.FilesOptions{})
	if answer := ask(handler, http.MethodGet, "/logo.png/"); answer.Code != http.StatusNotFound {
		t.Errorf("GET /logo.png/ from a lenient folder = %d %q, want 404", answer.Code, answer.Body)
	}
}

func TestFilesAnswersNotFoundForAPathThatNamesNoFile(t *testing.T) {
	t.Parallel()

	handler := gospa.Files(dated("logo.png", "docs/index.html"), gospa.FilesOptions{})
	for _, target := range []string{"/missing.png", "/", "/docs", "/docs/", "/logo.png/", "/index.html"} {
		answer := ask(handler, http.MethodGet, target)
		if answer.Code != http.StatusNotFound {
			t.Errorf("GET %s = %d %q, want 404", target, answer.Code, answer.Body)
		}
		if got := answer.Header().Get("X-Content-Type-Options"); got != "nosniff" {
			t.Errorf("GET %s X-Content-Type-Options = %q, want nosniff", target, got)
		}
	}
}

func TestFilesAnswersNotFoundForAnEmptyPath(t *testing.T) {
	t.Parallel()

	handler := gospa.Files(dated("logo.png"), gospa.FilesOptions{})
	request := ask(http.StripPrefix("/media", handler), http.MethodGet, "/media")
	if request.Code != http.StatusNotFound {
		t.Errorf("GET /media under /media = %d, want 404", request.Code)
	}
}

func TestFilesNeverSendsAHiddenFile(t *testing.T) {
	t.Parallel()

	folder := dated(
		".env", ".git/config", "docs/.draft.md", ".well-known/.key", "..well-known/x", "docs/guide.pdf",
		".well-known.bak/key", ".well-known-old/key",
	)
	handler := gospa.Files(folder, gospa.FilesOptions{})
	for _, target := range []string{
		"/.env", "/.git/config", "/.git/", "/docs/.draft.md", "/docs/../.env", "/../go.mod",
		"/%2e%2e/go.mod", "/docs/%2e%2e/.env", "/.", "/./docs/guide.pdf", "/..", "/.well-known/.key", "/..well-known/x",
		"/.well-known.bak/key", "/.well-known-old/key",
	} {
		answer := ask(handler, http.MethodGet, target)
		if answer.Code != http.StatusNotFound || strings.Contains(answer.Body.String(), "content of") {
			t.Errorf("GET %s = %d %q, want 404", target, answer.Code, answer.Body)
		}
	}
}

func TestFilesAnswersNotFoundForANameWindowsReadsAsAnother(t *testing.T) {
	t.Parallel()

	folder := dated("GIT~1/config", "ENV~1", "env~1", "docs~/guide.pdf", "docs/guide.pdf.", "docs/guide.pdf ", "docs. /x")
	handler := gospa.Files(folder, gospa.FilesOptions{})
	for _, target := range []string{
		"/GIT~1/config", "/ENV~1", "/env~1", "/docs~/guide.pdf", "/docs/guide.pdf.", "/docs/guide.pdf%20", "/docs.%20/x",
	} {
		answer := ask(handler, http.MethodGet, target)
		if answer.Code != http.StatusNotFound || strings.Contains(answer.Body.String(), "content of") {
			t.Errorf("GET %s = %d %q, want 404", target, answer.Code, answer.Body)
		}
	}
}

func TestFilesSendsAFileUnderWellKnown(t *testing.T) {
	t.Parallel()

	handler := gospa.Files(dated(".well-known/security.txt", "site/.well-known/assetlinks.json"), gospa.FilesOptions{})
	for target, kind := range map[string]string{
		"/.well-known/security.txt":         "text/plain; charset=utf-8",
		"/site/.well-known/assetlinks.json": "application/json",
	} {
		answer := ask(handler, http.MethodGet, target)
		if answer.Code != http.StatusOK || answer.Header().Get("Content-Type") != kind {
			t.Errorf("GET %s = %d %q, want 200 as %s", target, answer.Code, answer.Header().Get("Content-Type"), kind)
		}
	}
}

func TestFilesAnswersNotFoundForAnEmptySegment(t *testing.T) {
	t.Parallel()

	handler := gospa.Files(dated("logo.png", "docs/guide.pdf"), gospa.FilesOptions{})
	for _, target := range []string{"//logo.png", "/docs//guide.pdf", "//"} {
		if answer := ask(handler, http.MethodGet, target); answer.Code != http.StatusNotFound {
			t.Errorf("GET %s = %d %q, want 404", target, answer.Code, answer.Body)
		}
	}
}

func TestFilesAnswersOnlyGetAndHead(t *testing.T) {
	t.Parallel()

	handler := gospa.Files(dated("logo.png"), gospa.FilesOptions{NotFound: callersNotFound})
	for _, method := range []string{
		http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete, http.MethodOptions, http.MethodTrace,
	} {
		for _, target := range []string{"/logo.png", "/missing.png", "/.env"} {
			answer := ask(handler, method, target)
			if answer.Code != http.StatusMethodNotAllowed || answer.Header().Get("Allow") != "GET, HEAD" {
				t.Errorf("%s %s = %d allowing %q, want 405 allowing GET, HEAD",
					method, target, answer.Code, answer.Header().Get("Allow"))
			}
		}
	}
}

func TestFilesAnswersHeadWithTheHeadersAlone(t *testing.T) {
	t.Parallel()

	handler := gospa.Files(dated("logo.png"), gospa.FilesOptions{})
	answer := ask(handler, http.MethodHead, "/logo.png")
	if answer.Code != http.StatusOK || answer.Body.Len() != 0 {
		t.Errorf("HEAD /logo.png = %d with %d bytes, want 200 with none", answer.Code, answer.Body.Len())
	}
	if got, want := answer.Header().Get("Content-Length"), strconv.Itoa(len("content of logo.png")); got != want {
		t.Errorf("HEAD /logo.png Content-Length = %q, want %s", got, want)
	}
}

func TestFilesSendsTheTypeOfEachNameFromItsOwnTable(t *testing.T) {
	t.Parallel()

	types := map[string]string{
		"page.html":        "text/html; charset=utf-8",
		"page.htm":         "text/html; charset=utf-8",
		"style.css":        "text/css; charset=utf-8",
		"app.js":           "text/javascript; charset=utf-8",
		"app.mjs":          "text/javascript; charset=utf-8",
		"data.json":        "application/json",
		"app.js.map":       "application/json",
		"site.webmanifest": "application/manifest+json",
		"robots.txt":       "text/plain; charset=utf-8",
		"feed.xml":         "text/xml; charset=utf-8",
		"logo.svg":         "image/svg+xml",
		"logo.png":         "image/png",
		"photo.jpg":        "image/jpeg",
		"photo.jpeg":       "image/jpeg",
		"anim.gif":         "image/gif",
		"photo.webp":       "image/webp",
		"photo.avif":       "image/avif",
		"favicon.ico":      "image/vnd.microsoft.icon",
		"font.woff":        "font/woff",
		"font.woff2":       "font/woff2",
		"font.ttf":         "font/ttf",
		"font.otf":         "font/otf",
		"module.wasm":      "application/wasm",
		"guide.pdf":        "application/pdf",
		"bundle.zip":       "application/zip",
		"song.mp3":         "audio/mpeg",
		"song.m4a":         "audio/mp4",
		"song.wav":         "audio/wav",
		"song.ogg":         "audio/ogg",
		"song.flac":        "audio/flac",
		"clip.mp4":         "video/mp4",
		"clip.webm":        "video/webm",
		"LOGO.PNG":         "image/png",
		"notes":            "application/octet-stream",
		"report.xyz":       "application/octet-stream",
		"archive.tar.gz":   "application/octet-stream",
	}
	folder := fstest.MapFS{}
	for name := range types {
		folder[name] = &fstest.MapFile{Data: []byte("<!doctype html><script>window.loaded = true</script>"), ModTime: built}
	}
	handler := gospa.Files(folder, gospa.FilesOptions{})
	for name, kind := range types {
		if got := ask(handler, http.MethodGet, "/"+name).Header().Get("Content-Type"); got != kind {
			t.Errorf("GET /%s Content-Type = %q, want %q", name, got, kind)
		}
	}
}

func TestFilesChecksADatedFileByItsDate(t *testing.T) {
	t.Parallel()

	handler := gospa.Files(dated("logo.png"), gospa.FilesOptions{})
	answer := ask(handler, http.MethodGet, "/logo.png", "If-Modified-Since", "Sun, 01 Nov 2026 10:00:00 GMT")
	if answer.Code != http.StatusNotModified || answer.Body.Len() != 0 {
		t.Errorf("GET /logo.png since its date = %d %q, want 304 with no body", answer.Code, answer.Body)
	}
}

func TestFilesChecksAFileWithNoDateByATagOfItsContent(t *testing.T) {
	t.Parallel()

	handler := gospa.Files(undated("logo.png"), gospa.FilesOptions{})
	tag := contentTag(t, ask(handler, http.MethodGet, "/logo.png"))
	checked := ask(handler, http.MethodGet, "/logo.png", "If-None-Match", tag)
	if checked.Code != http.StatusNotModified || checked.Body.Len() != 0 {
		t.Errorf("GET /logo.png with its tag = %d %q, want 304 with no body", checked.Code, checked.Body)
	}
}

func TestFilesTagsAFileWithNoDateTheSameEachTimeAndApartFromOtherContent(t *testing.T) {
	t.Parallel()

	handler := gospa.Files(undated("logo.png", "icon.png"), gospa.FilesOptions{})
	tag := contentTag(t, ask(handler, http.MethodGet, "/logo.png"))
	if again := contentTag(t, ask(handler, http.MethodGet, "/logo.png")); again != tag {
		t.Errorf("GET /logo.png again tagged %q, want %q", again, tag)
	}
	if other := contentTag(t, ask(handler, http.MethodGet, "/icon.png")); other == tag {
		t.Errorf("GET /icon.png tagged %q, the same as another content", other)
	}
}

// contentTag returns the tag of an answer that sent a file with no date, failing the test when it has another shape.
func contentTag(t *testing.T, answer *httptest.ResponseRecorder) string {
	t.Helper()
	if answer.Code != http.StatusOK {
		t.Fatalf("the answer = %d %q, want 200", answer.Code, answer.Body)
	}
	tag := answer.Header().Get("Etag")
	if !strongTag.MatchString(tag) {
		t.Fatalf("the answer is tagged %q, want 32 hex digits in quotes", tag)
	}
	if !absent(answer.Header(), "Last-Modified") {
		t.Errorf("the answer Last-Modified = %q, want none", answer.Header().Values("Last-Modified"))
	}
	return tag
}

func TestFilesChecksAFileDatedAtTheEpochByATagOfItsContent(t *testing.T) {
	t.Parallel()

	folder := fstest.MapFS{"robots.txt": {Data: []byte("User-agent: *"), ModTime: time.Unix(0, 0)}}
	handler := gospa.Files(folder, gospa.FilesOptions{})
	tag := contentTag(t, ask(handler, http.MethodGet, "/robots.txt"))
	checked := ask(handler, http.MethodGet, "/robots.txt", "If-None-Match", tag)
	if checked.Code != http.StatusNotModified {
		t.Errorf("GET /robots.txt dated at the epoch with its tag = %d %q, want 304", checked.Code, checked.Body)
	}
}

func TestFilesSendsAPartOfAFile(t *testing.T) {
	t.Parallel()

	for name, folder := range datedAndUndated("docs/guide.pdf") {
		handler := gospa.Files(folder, gospa.FilesOptions{})
		answer := ask(handler, http.MethodGet, "/docs/guide.pdf", "Range", "bytes=0-6")
		if answer.Code != http.StatusPartialContent || answer.Body.String() != "content" {
			t.Errorf("GET the first 7 bytes of a %s /docs/guide.pdf = %d %q, want 206 content", name, answer.Code, answer.Body)
		}
	}
}

func TestFilesSendsAFileThatCannotSeek(t *testing.T) {
	t.Parallel()

	for name, folder := range datedAndUndated("logo.png") {
		handler := gospa.Files(brokenFS{MapFS: folder}, gospa.FilesOptions{})
		for range 2 {
			answer := ask(handler, http.MethodGet, "/logo.png", "Range", "bytes=11-")
			if answer.Code != http.StatusPartialContent || answer.Body.String() != "logo.png" {
				t.Errorf("GET the end of a %s /logo.png that cannot seek = %d %q, want 206 logo.png",
					name, answer.Code, answer.Body)
			}
		}
	}
}

func TestFilesReadsAFileThatCannotSeekOnlyAsFarAsItSends(t *testing.T) {
	t.Parallel()

	size := 1 << 20
	folder := fstest.MapFS{"clip.mp4": {Data: make([]byte, size), ModTime: built}}
	cases := []struct {
		method, span string
		most         int64
	}{
		{method: http.MethodHead, most: 0},
		{method: http.MethodGet, span: "bytes=0-0", most: 1},
		{method: http.MethodGet, span: "bytes=0-99", most: 100},
	}
	for _, tc := range cases {
		read := &atomic.Int64{}
		handler := gospa.Files(brokenFS{MapFS: folder, read: read}, gospa.FilesOptions{})
		answer := ask(handler, tc.method, "/clip.mp4", "Range", tc.span)
		if got := read.Load(); got > tc.most {
			t.Errorf("%s /clip.mp4 %s read %d bytes, want at most %d", tc.method, tc.span, got, tc.most)
		}
		if got := answer.Header().Get("Content-Length"); tc.span == "" && got != strconv.Itoa(size) {
			t.Errorf("%s /clip.mp4 Content-Length = %q, want %d", tc.method, got, size)
		}
	}
}

func TestFilesSendsTheWholeFileThatCannotSeekForSeveralRanges(t *testing.T) {
	t.Parallel()

	handler := gospa.Files(brokenFS{MapFS: dated("logo.png")}, gospa.FilesOptions{})
	for _, spans := range []string{"bytes=11-12,0-3", "bytes=0-5,3-8", "bytes=0-1,5-6"} {
		request := httptest.NewRequest(http.MethodGet, "/logo.png", nil)
		request.Header.Set("Range", spans)
		answer := httptest.NewRecorder()
		handler.ServeHTTP(answer, request)
		if answer.Code != http.StatusOK || answer.Body.String() != "content of logo.png" {
			t.Errorf("GET %s of /logo.png that cannot seek = %d %q, want 200 with the whole file",
				spans, answer.Code, answer.Body)
		}
		if got := request.Header.Get("Range"); got != spans {
			t.Errorf("GET %s of /logo.png left the request Range %q, want it as sent", spans, got)
		}
	}
}

func TestFilesSendsSeveralRangesOfAFileThatCanSeek(t *testing.T) {
	t.Parallel()

	handler := gospa.Files(dated("logo.png"), gospa.FilesOptions{})
	answer := ask(handler, http.MethodGet, "/logo.png", "Range", "bytes=11-12,0-3")
	if answer.Code != http.StatusPartialContent {
		t.Fatalf("GET two ranges of /logo.png = %d %q, want 206", answer.Code, answer.Body)
	}
	if got := answer.Header().Get("Content-Type"); !strings.HasPrefix(got, "multipart/byteranges") {
		t.Errorf("GET two ranges of /logo.png Content-Type = %q, want multipart/byteranges", got)
	}
	body := answer.Body.String()
	if !strings.Contains(body, "\r\n\r\nlo\r\n") || !strings.Contains(body, "\r\n\r\ncont\r\n") {
		t.Errorf("GET two ranges of /logo.png = %q, want the parts lo and cont", body)
	}
}

func TestFilesSendsAChangedFileWithNoDateToABrowserHoldingItsOldTag(t *testing.T) {
	t.Parallel()

	folder := undated("logo.png")
	handler := gospa.Files(folder, gospa.FilesOptions{})
	old := contentTag(t, ask(handler, http.MethodGet, "/logo.png"))
	folder["logo.png"].Data = []byte("a new logo")
	answer := ask(handler, http.MethodGet, "/logo.png", "If-None-Match", old)
	if answer.Code != http.StatusOK || answer.Body.String() != "a new logo" {
		t.Fatalf("GET the changed /logo.png with its old tag = %d %q, want 200 a new logo", answer.Code, answer.Body)
	}
	if tag := contentTag(t, answer); tag == old {
		t.Errorf("GET the changed /logo.png tagged %q, the tag of its old content", tag)
	}
}

func TestFilesTagsAFileWithNoDateByTheBytesItSends(t *testing.T) {
	t.Parallel()

	handler := gospa.Files(undated("docs/guide.pdf"), gospa.FilesOptions{})
	answer := ask(handler, http.MethodGet, "/docs/guide.pdf")
	sum := sha256.Sum256(answer.Body.Bytes())
	if got, want := contentTag(t, answer), `"`+hex.EncodeToString(sum[:16])+`"`; got != want {
		t.Errorf("GET /docs/guide.pdf %q tagged %s, want %s", answer.Body, got, want)
	}
}

func TestFilesSendsAFileWithNoDateThatCannotSeekWithNothingToCheck(t *testing.T) {
	t.Parallel()

	handler := gospa.Files(brokenFS{MapFS: undated("logo.png")}, gospa.FilesOptions{})
	answer := ask(handler, http.MethodGet, "/logo.png")
	if answer.Code != http.StatusOK || answer.Body.String() != "content of logo.png" {
		t.Fatalf("GET an undated /logo.png that cannot seek = %d %q, want 200 with the file", answer.Code, answer.Body)
	}
	for _, key := range []string{"ETag", "Last-Modified"} {
		if !absent(answer.Header(), key) {
			t.Errorf("GET an undated /logo.png that cannot seek %s = %q, want none", key, answer.Header().Values(key))
		}
	}
}

func TestFilesAnswersAServerErrorForAFileWithNoDateItCannotRead(t *testing.T) {
	t.Parallel()

	handler := gospa.Files(unreadableFS{MapFS: undated("logo.png")}, gospa.FilesOptions{})
	answer := ask(handler, http.MethodGet, "/logo.png")
	if answer.Code != http.StatusInternalServerError || strings.Contains(answer.Body.String(), "unreadable") {
		t.Errorf("GET an undated /logo.png that cannot be read = %d %q, want 500 naming no cause", answer.Code, answer.Body)
	}
}

func TestFilesCutsShortTheAnswerForADatedFileItCannotRead(t *testing.T) {
	t.Parallel()

	handler := gospa.Files(brokenFS{MapFS: dated("logo.png"), failRead: true}, gospa.FilesOptions{})
	answer := ask(handler, http.MethodGet, "/logo.png")
	if answer.Code != http.StatusOK || answer.Body.Len() != 0 {
		t.Errorf("GET a dated /logo.png that cannot be read = %d %q, want 200 with no byte", answer.Code, answer.Body)
	}
	if got, want := answer.Header().Get("Content-Length"), strconv.Itoa(len("content of logo.png")); got != want {
		t.Errorf("GET a dated /logo.png that cannot be read Content-Length = %q, want %s", got, want)
	}
}

func TestFilesAnswersNotFoundForAFileWithoutDetails(t *testing.T) {
	t.Parallel()

	handler := gospa.Files(brokenFS{MapFS: dated("logo.png"), failStat: true}, gospa.FilesOptions{})
	if answer := ask(handler, http.MethodGet, "/logo.png"); answer.Code != http.StatusNotFound {
		t.Errorf("GET /logo.png without details = %d %q, want 404", answer.Code, answer.Body)
	}
}

func TestFilesSendsTheCallersNotFound(t *testing.T) {
	t.Parallel()

	handler := gospa.Files(dated("logo.png", "docs/guide.pdf"), gospa.FilesOptions{NotFound: callersNotFound})
	for _, target := range []string{"/missing.png", "/docs/", "/.env", "//logo.png"} {
		answer := ask(handler, http.MethodGet, target)
		if answer.Code != http.StatusTeapot || answer.Body.String() != "the caller's own answer" {
			t.Errorf("GET %s = %d %q, want the caller's own answer", target, answer.Code, answer.Body)
		}
	}
}
