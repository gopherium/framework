// SPDX-License-Identifier: Apache-2.0

package gospa

import (
	"io"
	"net/http"
	"strings"
)

// forward reads a file that cannot seek as an io.ReadSeeker that seeks only before its first read.
type forward struct {
	io.Reader
	size int64
}

// Seek tells the size for a seek to the end, and otherwise skips to offset.
func (f *forward) Seek(offset int64, whence int) (int64, error) {
	if whence == io.SeekEnd {
		return f.size, nil
	}
	return io.CopyN(io.Discard, f.Reader, offset)
}

// ranged returns r, or a copy without its Range header when content only moves ahead and r asks for several ranges.
func ranged(r *http.Request, content io.ReadSeeker) *http.Request {
	if _, ahead := content.(*forward); !ahead || !strings.Contains(r.Header.Get("Range"), ",") {
		return r
	}
	whole := r.Clone(r.Context())
	whole.Header.Del("Range")
	return whole
}
