// SPDX-License-Identifier: Apache-2.0

package pluginkit

import "net/http"

// Protect wraps a plugin handler in the caller-supplied middleware and serves the
// plugin's declared public paths untouched (exact match, per [PublicPathProvider]).
func Protect(handler http.Handler, publicPaths []string, wrap func(http.Handler) http.Handler) http.Handler {
	public := make(map[string]struct{}, len(publicPaths))
	for _, path := range publicPaths {
		public[path] = struct{}{}
	}
	protected := wrap(handler)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if _, ok := public[r.URL.Path]; ok {
			handler.ServeHTTP(w, r)
			return
		}
		protected.ServeHTTP(w, r)
	})
}
