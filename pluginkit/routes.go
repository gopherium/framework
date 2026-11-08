// SPDX-License-Identifier: Apache-2.0

package pluginkit

import (
	"context"
	"errors"
	"net/http"
)

// errNoHandler is the failure of a Routes that answered no handler.
var errNoHandler = errors.New("Routes answered no handler")

// RoutesOutcome is what [RoutesEach] answers, its maps keyed by plugin id and its failures in the order of the plugins.
type RoutesOutcome struct {
	Routes      map[string]http.Handler
	PublicPaths map[string][]string
	Failed      []Failure
}

// RoutesEach reads each plugin's routes and public paths inside a recover, leaving out every plugin that fails.
func RoutesEach[P interface{ ID() string }](plugins []P) RoutesOutcome {
	outcome := RoutesOutcome{Routes: map[string]http.Handler{}, PublicPaths: map[string][]string{}}
	for _, plugin := range plugins {
		id := plugin.ID()
		err := guarded(context.Background(), id+" "+string(StepRoutes), func(context.Context) error {
			return outcome.read(id, plugin)
		})
		if err != nil {
			delete(outcome.Routes, id)
			delete(outcome.PublicPaths, id)
			outcome.Failed = append(outcome.Failed, failureOf(id, StepRoutes, err))
		}
	}
	return outcome
}

// read adds the routes and public paths the plugin id offers, failing a Routes that answered no handler.
func (o *RoutesOutcome) read(id string, plugin any) error {
	if provider, ok := plugin.(RouteProvider); ok {
		handler := provider.Routes()
		if isNil(handler) {
			return errNoHandler
		}
		o.Routes[id] = handler
	}
	if provider, ok := plugin.(PublicPathProvider); ok {
		o.PublicPaths[id] = provider.PublicPaths()
	}
	return nil
}
