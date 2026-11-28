// SPDX-License-Identifier: Apache-2.0

package gospa

import (
	"path"
	"strings"
)

// types maps a lower case file extension to the Content-Type sent for it.
var types = map[string]string{
	".html":        "text/html; charset=utf-8",
	".htm":         "text/html; charset=utf-8",
	".css":         "text/css; charset=utf-8",
	".js":          "text/javascript; charset=utf-8",
	".mjs":         "text/javascript; charset=utf-8",
	".json":        "application/json",
	".map":         "application/json",
	".webmanifest": "application/manifest+json",
	".txt":         "text/plain; charset=utf-8",
	".xml":         "text/xml; charset=utf-8",
	".svg":         "image/svg+xml",
	".png":         "image/png",
	".jpg":         "image/jpeg",
	".jpeg":        "image/jpeg",
	".gif":         "image/gif",
	".webp":        "image/webp",
	".avif":        "image/avif",
	".ico":         "image/vnd.microsoft.icon",
	".woff":        "font/woff",
	".woff2":       "font/woff2",
	".ttf":         "font/ttf",
	".otf":         "font/otf",
	".wasm":        "application/wasm",
	".pdf":         "application/pdf",
	".zip":         "application/zip",
	".mp3":         "audio/mpeg",
	".m4a":         "audio/mp4",
	".wav":         "audio/wav",
	".ogg":         "audio/ogg",
	".flac":        "audio/flac",
	".mp4":         "video/mp4",
	".webm":        "video/webm",
}

// typeOf returns the Content-Type of name by its extension, application/octet-stream when types lacks it.
func typeOf(name string) string {
	if kind, ok := types[strings.ToLower(path.Ext(name))]; ok {
		return kind
	}
	return "application/octet-stream"
}
