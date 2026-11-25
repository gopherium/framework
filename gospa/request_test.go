// SPDX-License-Identifier: Apache-2.0

package gospa_test

import (
	"errors"
	"io"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"sync/atomic"
	"testing/fstest"
	"time"
)

// strongTag matches the strong entity tag GoSPA gives, 32 hex digits in quotes.
var strongTag = regexp.MustCompile(`^"[0-9a-f]{32}"$`)

// built is the date the dated test files were last changed.
var built = time.Date(2026, 11, 1, 10, 0, 0, 0, time.UTC)

// ask sends one request for target to handler with the given header pairs and returns the answer.
func ask(handler http.Handler, method, target string, header ...string) *httptest.ResponseRecorder {
	request := httptest.NewRequest(method, target, nil)
	for i := 0; i+1 < len(header); i += 2 {
		request.Header.Set(header[i], header[i+1])
	}
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	return recorder
}

// dated returns a folder holding each name with its own name as content, every file changed at built.
func dated(names ...string) fstest.MapFS {
	folder := fstest.MapFS{}
	for _, name := range names {
		folder[name] = &fstest.MapFile{Data: []byte("content of " + name), ModTime: built}
	}
	return folder
}

// undated returns a folder holding each name with its own name as content, as embedded files carry no date.
func undated(names ...string) fstest.MapFS {
	folder := fstest.MapFS{}
	for _, name := range names {
		folder[name] = &fstest.MapFile{Data: []byte("content of " + name)}
	}
	return folder
}

// datedAndUndated returns the folder of names both with dates and without, keyed by which.
func datedAndUndated(names ...string) map[string]fstest.MapFS {
	return map[string]fstest.MapFS{"dated": dated(names...), "undated": undated(names...)}
}

// callersNotFound is a caller's own answer for a path that names no file.
var callersNotFound = http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
	w.WriteHeader(http.StatusTeapot)
	_, _ = w.Write([]byte("the caller's own answer"))
})

// errUnreadable is the error every read of a brokenFS file fails with.
var errUnreadable = errors.New("unreadable")

// absent reports whether header carries no value at all under key.
func absent(header http.Header, key string) bool {
	return len(header.Values(key)) == 0
}

// brokenFS wraps a folder whose files cannot seek, and whose reads or details fail when it says so.
type brokenFS struct {
	fstest.MapFS
	failRead, failStat bool
	read               *atomic.Int64
}

// Open opens name in the wrapped folder as a file that cannot seek.
func (b brokenFS) Open(name string) (fs.File, error) {
	file, err := b.MapFS.Open(name)
	if err != nil {
		return nil, err
	}
	return brokenFile{file: file, failRead: b.failRead, failStat: b.failStat, read: b.read}, nil
}

// brokenFile is a file that cannot seek, and whose reads or details fail when it says so.
type brokenFile struct {
	file               fs.File
	failRead, failStat bool
	read               *atomic.Int64
}

// Stat returns the details of the file, or errUnreadable when its details fail.
func (b brokenFile) Stat() (fs.FileInfo, error) {
	if b.failStat {
		return nil, errUnreadable
	}
	return b.file.Stat()
}

// Read reads the file and counts the bytes read, or fails with errUnreadable when its reads fail.
func (b brokenFile) Read(p []byte) (int, error) {
	if b.failRead {
		return 0, errUnreadable
	}
	n, err := b.file.Read(p)
	if b.read != nil {
		b.read.Add(int64(n))
	}
	return n, err
}

// Close closes the file.
func (b brokenFile) Close() error {
	return b.file.Close()
}

// unreadableFS wraps a folder whose files can seek but fail every read.
type unreadableFS struct {
	fstest.MapFS
}

// Open opens name in the wrapped folder as a file that can seek but fails every read.
func (u unreadableFS) Open(name string) (fs.File, error) {
	file, err := u.MapFS.Open(name)
	if err != nil {
		return nil, err
	}
	return unreadableFile{File: file}, nil
}

// unreadableFile is a file that can seek but fails every read with errUnreadable.
type unreadableFile struct {
	fs.File
}

// Read fails with errUnreadable.
func (unreadableFile) Read([]byte) (int, error) {
	return 0, errUnreadable
}

// Seek seeks the wrapped file.
func (u unreadableFile) Seek(offset int64, whence int) (int64, error) {
	return u.File.(io.Seeker).Seek(offset, whence)
}

// openOnlyFS wraps a folder that opens names and tells no file's details by name, as embed.FS does.
type openOnlyFS struct {
	folder fs.FS
}

// Open opens name in the wrapped folder.
func (o openOnlyFS) Open(name string) (fs.File, error) {
	return o.folder.Open(name)
}

// lenientFS wraps a folder that opens a name with a trailing slash as the name without it.
type lenientFS struct {
	fstest.MapFS
}

// Open opens name without its trailing slash.
func (l lenientFS) Open(name string) (fs.File, error) {
	return l.MapFS.Open(strings.TrimSuffix(name, "/"))
}
