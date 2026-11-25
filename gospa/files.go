// SPDX-License-Identifier: Apache-2.0

package gospa

import (
	"io/fs"
	"net/http"
)

// FilesOptions says how Files answers a path that names no file it may send.
type FilesOptions struct {
	// NotFound answers such a path, http.NotFound when nil.
	NotFound http.Handler
}

// files is the handler Files returns.
type files struct {
	folder   folder
	notFound http.Handler
}

// Files returns a handler that sends the regular files of fsys by their path and nothing else.
func Files(fsys fs.FS, opts FilesOptions) http.Handler {
	return &files{folder: folder{fsys: fsys}, notFound: orNotFound(opts.NotFound)}
}

// ServeHTTP sends the file r names, or the not found answer when it names none it may send.
func (h *files) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if !readable(w, r) {
		return
	}
	name, ok := fileName(r.URL.Path)
	if !ok {
		h.notFound.ServeHTTP(w, r)
		return
	}
	open, ok := h.folder.open(name)
	if !ok {
		h.notFound.ServeHTTP(w, r)
		return
	}
	h.folder.send(w, r, open, noCache)
}
