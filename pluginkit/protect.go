// SPDX-License-Identifier: Apache-2.0

package pluginkit

import "net/http"

// RouteSettings are what every plugin's routes are protected under, one value an application reuses for each plugin.
type RouteSettings struct {
	// Wrap is the application's middleware, which every path but a plugin's public ones goes through.
	Wrap func(http.Handler) http.Handler
	// Answer answers a request whose plugin handler panicked before anything went out, and must not panic.
	Answer http.Handler
	// Report receives every panic of a plugin's handler, from many requests at once, and must not panic.
	Report func(RouteFailure)
}

// Protect serves the handler of plugin id behind settings.Wrap and its public paths without it, recovering each panic.
func Protect(id string, handler http.Handler, publicPaths []string, settings RouteSettings) http.Handler {
	checkRouteSettings(id, settings)
	public := make(map[string]struct{}, len(publicPaths))
	for _, path := range publicPaths {
		public[path] = struct{}{}
	}
	plugin := &recovered{id: id, handler: handler, settings: settings}
	protected := settings.Wrap(plugin)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if _, ok := public[r.URL.Path]; ok {
			plugin.ServeHTTP(w, r)
			return
		}
		protected.ServeHTTP(w, r)
	})
}

// checkRouteSettings panics naming the first value [Protect] cannot serve a plugin's routes without.
func checkRouteSettings(id string, settings RouteSettings) {
	switch {
	case id == "":
		panic("pluginkit: Protect: the plugin id is empty")
	case settings.Wrap == nil:
		panic("pluginkit: Protect: RouteSettings.Wrap is nil")
	case isNil(settings.Answer):
		panic("pluginkit: Protect: RouteSettings.Answer is nil")
	case settings.Report == nil:
		panic("pluginkit: Protect: RouteSettings.Report is nil")
	}
}
