// SPDX-License-Identifier: Apache-2.0

// Package pluginkit provides compile-time plugin infrastructure: a lifecycle
// contract, a plugin host, and route guarding for plugin namespaces.
package pluginkit

import (
	"context"
	"net/http"
)

// Plugin is an independently addable unit of functionality with a
// managed lifecycle.
type Plugin interface {
	ID() string
	Start(ctx context.Context) error
	Stop(ctx context.Context) error
}

// Migrator is implemented by plugins that own database schema, which
// the host migrates before starting any plugin.
type Migrator interface {
	Migrate(ctx context.Context) error
}

// Seeder is implemented by plugins that can fill their own schema with
// development data, which the host asks for outside the start path.
type Seeder interface {
	Seed(ctx context.Context) error
}

// RouteProvider is implemented by plugins that expose HTTP endpoints
// under their own namespace.
type RouteProvider interface {
	Routes() http.Handler
}

// PublicPathProvider is implemented by plugins declaring namespace-relative paths that must stay
// reachable without a session. Paths match exactly, for every HTTP method, and all else stays protected.
type PublicPathProvider interface {
	PublicPaths() []string
}
