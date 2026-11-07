// SPDX-License-Identifier: Apache-2.0

package pluginkit

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"time"
)

var (
	// errNoMigrationStore refuses a host built to skip migrations with no store to read them from.
	errNoMigrationStore = errors.New("pluginkit: the migration store is nil")
	// errEmptyMigrationVersion refuses a migration version that holds nothing to compare.
	errEmptyMigrationVersion = errors.New("the version is empty")
	// errNilMigrator refuses a lookup that claims a migrator and gives none.
	errNilMigrator = errors.New("the lookup gave a nil migrator")
)

// Host starts and stops a fixed set of plugins.
type Host struct {
	plugins    []Plugin
	store      MigrationStore
	migratorOf MigratorOf
}

// NewHost returns a [Host] managing plugins. It panics when two
// plugins share an ID.
func NewHost(plugins ...Plugin) *Host {
	if err := duplicateID(plugins); err != nil {
		panic(err.Error())
	}
	return &Host{plugins: plugins}
}

// NewHostWithMigrationStore returns a [Host] whose migrations skip every plugin store records as current.
func NewHostWithMigrationStore(store MigrationStore, migratorOf MigratorOf, plugins ...Plugin) (*Host, error) {
	if store == nil {
		return nil, errNoMigrationStore
	}
	if err := duplicateID(plugins); err != nil {
		return nil, err
	}
	return &Host{plugins: plugins, store: store, migratorOf: migratorOf}, nil
}

// duplicateID returns an error naming the first id two plugins share.
func duplicateID(plugins []Plugin) error {
	seen := make(map[string]struct{}, len(plugins))
	for _, p := range plugins {
		if _, ok := seen[p.ID()]; ok {
			return fmt.Errorf("pluginkit: duplicate id %q", p.ID())
		}
		seen[p.ID()] = struct{}{}
	}
	return nil
}

// Start migrates and starts every plugin in order, stopping the started ones within stopGrace when one fails.
func (h *Host) Start(ctx context.Context, stopGrace time.Duration) error {
	if stopGrace <= 0 {
		return fmt.Errorf("pluginkit: the stop grace must stand above zero, got %v", stopGrace)
	}
	if err := h.Migrate(ctx); err != nil {
		return err
	}
	for i, p := range h.plugins {
		if err := safeCall(ctx, p.ID(), "start", p.Start); err != nil {
			return errors.Join(err, h.rollBack(ctx, stopGrace, i-1))
		}
	}
	return nil
}

// rollBack stops the plugins from index down under a context stopGrace bounds and the end of ctx cannot cancel.
func (h *Host) rollBack(ctx context.Context, stopGrace time.Duration, index int) error {
	stopping, cancel := context.WithTimeout(context.WithoutCancel(ctx), stopGrace)
	defer cancel()
	return h.stopDownFrom(stopping, index)
}

// Migrate applies every [Migrator] plugin's schema in order, past those its store records as current, until one fails.
func (h *Host) Migrate(ctx context.Context) error {
	versions, err := h.readVersions(ctx)
	if err != nil {
		return err
	}
	for _, p := range h.plugins {
		if _, err := h.migrateUnlessCurrent(ctx, p, versions); err != nil {
			return err
		}
	}
	return nil
}

// readVersions returns every migration version the store records, in one read, and none on a host with no store.
func (h *Host) readVersions(ctx context.Context) (map[string]string, error) {
	var versions map[string]string
	if h.store == nil {
		return versions, nil
	}
	err := guarded(ctx, "read the migration versions", func(ctx context.Context) error {
		var err error
		versions, err = h.store.Versions(ctx)
		return err
	})
	return versions, err
}

// migrateUnlessCurrent applies and records p's schema unless versions holds it, answering the step an error came from.
func (h *Host) migrateUnlessCurrent(ctx context.Context, p Plugin, versions map[string]string) (Step, error) {
	migrator, ok, err := h.migratorFor(ctx, p)
	if err != nil || !ok {
		return StepMigrate, err
	}
	versioner, ok := migrator.(MigrationVersioner)
	if !ok || h.store == nil {
		return StepMigrate, safeCall(ctx, p.ID(), "migrate", migrator.Migrate)
	}
	version, err := migrationVersion(ctx, p.ID(), versioner)
	if err != nil || version == versions[p.ID()] {
		return StepMigrate, err
	}
	if err := safeCall(ctx, p.ID(), "migrate", migrator.Migrate); err != nil {
		return StepMigrate, err
	}
	return StepRecord, safeCall(ctx, p.ID(), "record", func(ctx context.Context) error {
		return h.store.Record(ctx, p.ID(), version)
	})
}

// migratorFor returns the [Migrator] the lookup gives for p, or p itself when the lookup gives none.
func (h *Host) migratorFor(ctx context.Context, p Plugin) (migrator Migrator, ok bool, err error) {
	if h.migratorOf != nil {
		err = safeCall(ctx, p.ID(), "migrator lookup", func(context.Context) error {
			migrator, ok = h.migratorOf(p)
			if ok && migrator == nil {
				return errNilMigrator
			}
			return nil
		})
		if err != nil || ok {
			return migrator, ok, err
		}
	}
	migrator, ok = p.(Migrator)
	return migrator, ok, nil
}

// migrationVersion returns the migration version versioner reports for the plugin id, refusing an empty one.
func migrationVersion(ctx context.Context, id string, versioner MigrationVersioner) (string, error) {
	var version string
	err := safeCall(ctx, id, "migration version", func(context.Context) error {
		var err error
		if version, err = versioner.MigrationVersion(); err == nil && version == "" {
			err = errEmptyMigrationVersion
		}
		return err
	})
	return version, err
}

// Seed asks every [Seeder] plugin to fill its schema in registration order,
// stopping at the first failure.
func (h *Host) Seed(ctx context.Context) error {
	for _, p := range h.plugins {
		seeder, ok := p.(Seeder)
		if !ok {
			continue
		}
		if err := safeCall(ctx, p.ID(), "seed", seeder.Seed); err != nil {
			return err
		}
	}
	return nil
}

// Routes returns the HTTP handler of every [RouteProvider] plugin,
// keyed by plugin ID.
func (h *Host) Routes() map[string]http.Handler {
	routes := make(map[string]http.Handler)
	for _, p := range h.plugins {
		if provider, ok := p.(RouteProvider); ok {
			routes[p.ID()] = provider.Routes()
		}
	}
	return routes
}

// PublicPaths returns the session-exempt paths of every
// [PublicPathProvider] plugin, keyed by plugin ID.
func (h *Host) PublicPaths() map[string][]string {
	paths := make(map[string][]string)
	for _, p := range h.plugins {
		if provider, ok := p.(PublicPathProvider); ok {
			paths[p.ID()] = provider.PublicPaths()
		}
	}
	return paths
}

// Stop stops every plugin in reverse registration order, continuing
// past failures and returning them joined.
func (h *Host) Stop(ctx context.Context) error {
	return h.stopDownFrom(ctx, len(h.plugins)-1)
}

// stopDownFrom stops plugins from index down to zero in reverse order, collecting and joining any errors.
func (h *Host) stopDownFrom(ctx context.Context, index int) error {
	var errs []error
	for i := index; i >= 0; i-- {
		if err := safeCall(ctx, h.plugins[i].ID(), "stop", h.plugins[i].Stop); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

// safeCall runs fn, wrapping any returned error and converting any panic into an error tagged with the
// plugin id and operation.
func safeCall(ctx context.Context, id, operation string, fn func(context.Context) error) error {
	return guarded(ctx, id+" "+operation, fn)
}

// guarded runs fn, wrapping any returned error and converting any panic into an error tagged with name.
func guarded(ctx context.Context, name string, fn func(context.Context) error) (err error) {
	defer func() {
		if recovered := recover(); recovered != nil {
			err = panicError("pluginkit: " + name + " panicked: " + describe(recovered))
		}
	}()
	if err := fn(ctx); err != nil {
		return fmt.Errorf("pluginkit: %s: %w", name, err)
	}
	return nil
}

// panicError is a panic [guarded] recovered, holding the text that names where it came from.
type panicError string

// Error returns the text of the panic.
func (e panicError) Error() string {
	return string(e)
}
