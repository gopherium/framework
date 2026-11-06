// SPDX-License-Identifier: Apache-2.0

// Package pluginkit provides compile-time plugin infrastructure: a lifecycle
// contract, a plugin host, hooks that call every plugin implementing an
// interface, and route guarding for plugin namespaces.
package pluginkit

import (
	"context"
	"net/http"
)

// Plugin is an independently addable unit of functionality with a
// managed lifecycle.
type Plugin interface {
	ID() string
	// Start readies the plugin, returning by the time ctx ends, and may run again after Stop.
	Start(ctx context.Context) error
	// Stop releases what the plugin holds by the time ctx ends, whether or not Start ran, and may run more than once.
	Stop(ctx context.Context) error
}

// Migrator is implemented by plugins that own database schema, which a host migrates before starting any
// plugin, and which a host with a [MigrationStore] skips while its recorded version is current.
type Migrator interface {
	// Migrate applies the plugin's schema, returning by the time ctx ends.
	Migrate(ctx context.Context) error
}

// MigrationVersioner is implemented by a [Migrator] whose schema reports a version a host can compare.
type MigrationVersioner interface {
	// MigrationVersion returns the version of the schema, which a host compares as an opaque string.
	MigrationVersion() (string, error)
}

// MigrationStore is an application's record of the migration version each plugin last applied.
type MigrationStore interface {
	// Versions returns every recorded version, keyed by plugin id, in one read.
	Versions(ctx context.Context) (map[string]string, error)
	// Record stores version as the one plugin id has now applied.
	Record(ctx context.Context, id, version string) error
}

// MigratorOf returns the [Migrator] that applies a plugin's schema for it, and false when the plugin's own applies.
type MigratorOf func(Plugin) (Migrator, bool)

// Seeder is implemented by plugins that can fill their own schema with
// development data, which the host asks for outside the start path.
type Seeder interface {
	Seed(ctx context.Context) error
}

// RouteProvider is implemented by plugins that expose HTTP endpoints
// under their own namespace.
type RouteProvider interface {
	// Routes returns the plugin's handler, whether or not Start ran or worked.
	Routes() http.Handler
}

// PublicPathProvider is implemented by plugins declaring namespace-relative paths that must stay
// reachable without a session. Paths match exactly, for every HTTP method, and all else stays protected.
type PublicPathProvider interface {
	// PublicPaths returns those paths, whether or not Start ran or worked.
	PublicPaths() []string
}
