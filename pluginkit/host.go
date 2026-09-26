// SPDX-License-Identifier: Apache-2.0

package pluginkit

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"time"
)

// Host starts and stops a fixed set of plugins.
type Host struct {
	plugins []Plugin
}

// NewHost returns a [Host] managing plugins. It panics when two
// plugins share an ID.
func NewHost(plugins ...Plugin) *Host {
	seen := make(map[string]struct{}, len(plugins))
	for _, p := range plugins {
		if _, ok := seen[p.ID()]; ok {
			panic(fmt.Sprintf("pluginkit: duplicate id %q", p.ID()))
		}
		seen[p.ID()] = struct{}{}
	}
	return &Host{plugins: plugins}
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

// Migrate applies the schema of every [Migrator] plugin in registration order, stopping at the first failure.
func (h *Host) Migrate(ctx context.Context) error {
	for _, p := range h.plugins {
		migrator, ok := p.(Migrator)
		if !ok {
			continue
		}
		if err := safeCall(ctx, p.ID(), "migrate", migrator.Migrate); err != nil {
			return err
		}
	}
	return nil
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
func safeCall(ctx context.Context, id, operation string, fn func(context.Context) error) (err error) {
	defer func() {
		if recovered := recover(); recovered != nil {
			err = fmt.Errorf("pluginkit: %s %s panicked: %v", id, operation, recovered)
		}
	}()
	if err := fn(ctx); err != nil {
		return fmt.Errorf("pluginkit: %s %s: %w", id, operation, err)
	}
	return nil
}
