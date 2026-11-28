// SPDX-License-Identifier: Apache-2.0

package gospa

import (
	"bytes"
	"crypto/sha256"
	"fmt"
	"io/fs"
	"net/http"
	"regexp"
	"strings"
	"time"
)

// DefaultHashedMaxAge is how long a browser keeps a hashed file when AppOptions sets no window.
const DefaultHashedMaxAge = 365 * 24 * time.Hour

// pageName is the file that holds the page of a built app.
const pageName = "index.html"

// assetsDir is the folder a build writes its hashed files to.
const assetsDir = "assets"

// hashedName matches a name under assets ending in a dash, 8 letters, digits, dashes or underscores and an extension.
var hashedName = regexp.MustCompile(`^assets/(?:[^/]+/)*[^/]*-[A-Za-z0-9_-]{8}(?:\.[A-Za-z0-9]+)+$`)

// AppOptions says how long App lets a browser keep hashed files, what its page carries and how it answers not found.
type AppOptions struct {
	// HashedMaxAge is how long a browser keeps a hashed file, DefaultHashedMaxAge when zero, else one second or more.
	HashedMaxAge time.Duration
	// Meta holds the values the page carries in its head, in order.
	Meta []Meta
	// NotFound answers a path that names no file and may not get the page, http.NotFound when nil.
	NotFound http.Handler
}

// app is the handler App returns.
type app struct {
	folder   folder
	head     []byte
	hashed   string
	notFound http.Handler
}

// App returns a handler serving the built single-page app in fsys, its page for a path outside assets naming no file.
func App(fsys fs.FS, opts AppOptions) (http.Handler, error) {
	if _, err := fs.ReadFile(fsys, pageName); err != nil {
		return nil, fmt.Errorf("gospa: read the app page: %w", err)
	}
	head, err := metaElements(opts.Meta)
	if err != nil {
		return nil, err
	}
	hashed, err := hashedControl(opts.HashedMaxAge)
	if err != nil {
		return nil, err
	}
	return &app{folder: folder{fsys: fsys}, head: head, hashed: hashed, notFound: orNotFound(opts.NotFound)}, nil
}

// hashedControl returns the Cache-Control of a hashed file kept for maxAge, DefaultHashedMaxAge when zero.
func hashedControl(maxAge time.Duration) (string, error) {
	if maxAge == 0 {
		maxAge = DefaultHashedMaxAge
	}
	if maxAge < time.Second {
		return "", fmt.Errorf("gospa: HashedMaxAge %v is below one second", maxAge)
	}
	return fmt.Sprintf("public, max-age=%d, immutable", int64(maxAge/time.Second)), nil
}

// ServeHTTP sends the file r names, the page when it names no file outside assets, or the not found answer.
func (a *app) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if !readable(w, r) {
		return
	}
	name, ok := fileName(r.URL.Path)
	switch {
	case !ok:
		a.notFound.ServeHTTP(w, r)
	case strings.EqualFold(name, pageName):
		a.sendPage(w, r)
	default:
		a.sendFile(w, r, name)
	}
}

// sendFile sends the file at name, or else the not found answer under assets and the page elsewhere.
func (a *app) sendFile(w http.ResponseWriter, r *http.Request, name string) {
	open, ok := a.folder.open(name)
	switch {
	case ok:
		a.folder.send(w, r, open, a.controlOf(name))
	case name == assetsDir || strings.HasPrefix(name, assetsDir+"/"):
		a.notFound.ServeHTTP(w, r)
	default:
		a.sendPage(w, r)
	}
}

// controlOf returns the Cache-Control of the file at name.
func (a *app) controlOf(name string) string {
	if hashedName.MatchString(name) {
		return a.hashed
	}
	return noCache
}

// sendPage sends the page with its values, tagged by the bytes sent and dated by nothing.
func (a *app) sendPage(w http.ResponseWriter, r *http.Request) {
	page, err := fs.ReadFile(a.folder.fsys, pageName)
	if err != nil {
		http.Error(w, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
		return
	}
	if a.head != nil {
		page = withHead(page, a.head)
	}
	header := w.Header()
	header.Set("ETag", tagOf(page))
	describe(header, pageName, noCache)
	http.ServeContent(w, r, "", time.Time{}, bytes.NewReader(page))
}

// tagOf returns the strong entity tag of data.
func tagOf(data []byte) string {
	sum := sha256.Sum256(data)
	return tagFrom(sum[:])
}
