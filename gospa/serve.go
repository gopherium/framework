// SPDX-License-Identifier: Apache-2.0

package gospa

import (
	"crypto/sha256"
	"encoding/hex"
	"io"
	"io/fs"
	"net/http"
	"strings"
	"time"
)

// noCache lets a browser keep an answer but check it with the server on every load.
const noCache = "no-cache"

// folder sends the regular files of one fs.FS, tagging by content each file that carries no date and can seek.
type folder struct {
	fsys fs.FS
}

// openFile is a regular file of a folder, open to be sent.
type openFile struct {
	name string
	file fs.File
	info fs.FileInfo
}

// open returns the regular file at name, or false when name names none.
func (f *folder) open(name string) (openFile, bool) {
	if !fs.ValidPath(name) || !regular(f.fsys, name) {
		return openFile{}, false
	}
	file, err := f.fsys.Open(name)
	if err != nil {
		return openFile{}, false
	}
	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() {
		_ = file.Close()
		return openFile{}, false
	}
	return openFile{name: name, file: file, info: info}, true
}

// regular reports whether name is a regular file of fsys, or true when fsys cannot tell without opening it.
func regular(fsys fs.FS, name string) bool {
	stater, ok := fsys.(fs.StatFS)
	if !ok {
		return true
	}
	info, err := stater.Stat(name)
	return err == nil && info.Mode().IsRegular()
}

// send writes open with the type of its name and control as its Cache-Control, answering ranges and checks.
func (f *folder) send(w http.ResponseWriter, r *http.Request, open openFile, control string) {
	defer func() { _ = open.file.Close() }()
	content, tag, err := sendable(open)
	if err != nil {
		http.Error(w, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
		return
	}
	header := w.Header()
	if tag != "" {
		header.Set("ETag", tag)
	}
	describe(header, open.name, control)
	http.ServeContent(w, ranged(r, content), "", open.info.ModTime(), content)
}

// describe sets the type of name, control as the Cache-Control, and nosniff.
func describe(header http.Header, name, control string) {
	header.Set("Content-Type", typeOf(name))
	header.Set("Cache-Control", control)
	header.Set("X-Content-Type-Options", "nosniff")
}

// sendable returns a seekable reader over open and, when open carries no date and can seek, the tag of its bytes.
func sendable(open openFile) (io.ReadSeeker, string, error) {
	seeker, ok := open.file.(io.ReadSeeker)
	if !ok {
		return &forward{Reader: open.file, size: open.info.Size()}, "", nil
	}
	if dated(open.info.ModTime()) {
		return seeker, "", nil
	}
	sum := sha256.New()
	if _, err := io.Copy(sum, seeker); err != nil {
		return nil, "", err
	}
	return seeker, tagFrom(sum.Sum(nil)), nil
}

// dated reports whether changed is a date http.ServeContent sends, as neither the zero time nor the Unix epoch is.
func dated(changed time.Time) bool {
	return !changed.IsZero() && !changed.Equal(time.Unix(0, 0))
}

// tagFrom returns the strong entity tag of a sha256 sum.
func tagFrom(sum []byte) string {
	return `"` + hex.EncodeToString(sum[:16]) + `"`
}

// readable reports whether r asks to read, answering 405 when it does not.
func readable(w http.ResponseWriter, r *http.Request) bool {
	if r.Method == http.MethodGet || r.Method == http.MethodHead {
		return true
	}
	w.Header().Set("Allow", "GET, HEAD")
	http.Error(w, http.StatusText(http.StatusMethodNotAllowed), http.StatusMethodNotAllowed)
	return false
}

// fileName returns the file name a URL path asks for, or false when one of its segments may not stand in a path.
func fileName(urlPath string) (string, bool) {
	name := strings.TrimPrefix(urlPath, "/")
	segments := strings.Split(name, "/")
	for i, segment := range segments {
		if !allowed(segment, i == len(segments)-1) {
			return "", false
		}
	}
	return name, true
}

// allowed reports whether segment may stand in a path, last saying whether it ends the path.
func allowed(segment string, last bool) bool {
	switch {
	case segment == "":
		return last
	case strings.HasPrefix(segment, "."):
		return segment == ".well-known"
	}
	return !strings.ContainsRune(segment, '~') && strings.TrimRight(segment, ". ") == segment
}

// orNotFound returns handler, or http.NotFound when handler is nil.
func orNotFound(handler http.Handler) http.Handler {
	if handler == nil {
		return http.HandlerFunc(http.NotFound)
	}
	return handler
}
