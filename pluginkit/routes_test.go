// SPDX-License-Identifier: Apache-2.0

package pluginkit_test

import (
	"maps"
	"net/http"
	"slices"
	"testing"

	"github.com/gopherium/framework/pluginkit"
)

// panickyRoutesPlugin is a plugin whose Routes panics.
type panickyRoutesPlugin struct {
	fakePlugin
}

// Routes panics.
func (p *panickyRoutesPlugin) Routes() http.Handler {
	panic("boom")
}

// servingPlugin is a plugin with routes and public paths.
type servingPlugin struct {
	routedPlugin
	paths func() []string
}

// PublicPaths returns what paths answers.
func (p *servingPlugin) PublicPaths() []string {
	return p.paths()
}

// routed returns a plugin id whose Routes answers handler.
func routed(id string, handler http.Handler) *routedPlugin {
	return &routedPlugin{fakePlugin: fakePlugin{id: id}, handler: handler}
}

func TestRoutesEachReadsEachPluginsRoutesAndPublicPaths(t *testing.T) {
	t.Parallel()

	events, feed := http.NewServeMux(), http.NewServeMux()
	plugins := []pluginkit.Plugin{
		&servingPlugin{routedPlugin: *routed("events", events), paths: func() []string { return []string{"/rss.xml"} }},
		&fakePlugin{id: "theme"},
		routed("feed", feed),
		&publicPathsPlugin{fakePlugin: fakePlugin{id: "hooks"}, paths: []string{"/hook"}},
	}

	outcome := pluginkit.RoutesEach(plugins)

	wantRoutes := map[string]http.Handler{"events": events, "feed": feed}
	if !maps.Equal(outcome.Routes, wantRoutes) {
		t.Errorf("Routes = %v, want %v", outcome.Routes, wantRoutes)
	}
	wantPaths := map[string][]string{"events": {"/rss.xml"}, "hooks": {"/hook"}}
	if !maps.EqualFunc(outcome.PublicPaths, wantPaths, slices.Equal) {
		t.Errorf("PublicPaths = %v, want %v", outcome.PublicPaths, wantPaths)
	}
	if len(outcome.Failed) != 0 {
		t.Errorf("Failed = %+v, want none", outcome.Failed)
	}
}

func TestRoutesEachFailsAPluginWhoseRoutesFailToLoad(t *testing.T) {
	t.Parallel()

	const panicked = "pluginkit: feed routes panicked: boom"
	const noHandler = "pluginkit: feed routes: Routes answered no handler"
	mux := http.NewServeMux()
	tests := map[string]struct {
		plugin   pluginkit.Plugin
		want     string
		panicked bool
	}{
		"Routes panics": {plugin: &panickyRoutesPlugin{fakePlugin{id: "feed"}}, want: panicked, panicked: true},
		"PublicPaths panics": {
			plugin:   &servingPlugin{routedPlugin: *routed("feed", mux), paths: func() []string { panic("boom") }},
			want:     panicked,
			panicked: true,
		},
		"Routes answers nil":           {plugin: routed("feed", nil), want: noHandler},
		"Routes answers a nil mux":     {plugin: routed("feed", (*http.ServeMux)(nil)), want: noHandler},
		"Routes answers a nil handler": {plugin: routed("feed", http.HandlerFunc(nil)), want: noHandler},
	}

	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			outcome := pluginkit.RoutesEach([]pluginkit.Plugin{routed("events", mux), test.plugin, routed("gallery", mux)})

			if got := slices.Sorted(maps.Keys(outcome.Routes)); !slices.Equal(got, []string{"events", "gallery"}) {
				t.Errorf("Routes hold %q, want events and gallery", got)
			}
			if len(outcome.PublicPaths) != 0 {
				t.Errorf("PublicPaths = %v, want none", outcome.PublicPaths)
			}
			if len(outcome.Failed) != 1 {
				t.Fatalf("Failed = %+v, want one failure", outcome.Failed)
			}
			got := outcome.Failed[0]
			if got.ID != "feed" || got.Step != pluginkit.StepRoutes || got.Panicked != test.panicked ||
				got.Err.Error() != test.want {
				t.Errorf("Failed = %+v, want feed at %s with %q panicked %t", got, pluginkit.StepRoutes, test.want,
					test.panicked)
			}
		})
	}
}

func TestRoutesEachListsTheFailuresInTheOrderOfThePlugins(t *testing.T) {
	t.Parallel()

	outcome := pluginkit.RoutesEach([]*routedPlugin{routed("gallery", nil), routed("events", nil)})

	var got []string
	for _, failure := range outcome.Failed {
		got = append(got, failure.ID)
	}
	if !slices.Equal(got, []string{"gallery", "events"}) {
		t.Errorf("Failed lists %q, want gallery then events", got)
	}
}

func TestRoutesEachAnswersEmptyMapsForNoPlugins(t *testing.T) {
	t.Parallel()

	outcome := pluginkit.RoutesEach[pluginkit.Plugin](nil)

	if outcome.Routes == nil || outcome.PublicPaths == nil || len(outcome.Routes)+len(outcome.PublicPaths) != 0 {
		t.Errorf("RoutesEach(nil) = %+v, want two empty maps", outcome)
	}
	if outcome.Failed != nil {
		t.Errorf("Failed = %+v, want none", outcome.Failed)
	}
}
