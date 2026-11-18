// SPDX-License-Identifier: Apache-2.0

package features_test

import (
	"fmt"
	"slices"
	"testing"

	"github.com/cucumber/godog"

	"github.com/gopherium/framework/pluginkit"
	"github.com/gopherium/framework/pluginkit/wire"
)

// installedWorld is what one scenario of the installed plugins builds and finds.
type installedWorld struct {
	boot      *bootWorld
	named     map[string]string
	described map[string]string
	versions  map[string]string
	listed    []pluginkit.InstalledPlugin
}

// list joins the manifest of each compiled plugin with the store's records, as an application lists its plugins.
func (w *installedWorld) list() {
	manifests := make([]wire.Manifest, 0, len(w.boot.compiled))
	for _, id := range w.boot.compiled {
		manifests = append(manifests, wire.Manifest{
			ID: id, Name: w.named[id], Description: w.described[id], Version: w.versions[id],
		})
	}
	w.listed = pluginkit.Installed(manifests, w.boot.seeds)
}

// row returns the row the plugin id should have in state, with the name, description and version its manifest gives.
func (w *installedWorld) row(id string, state pluginkit.PluginState) pluginkit.InstalledPlugin {
	return pluginkit.InstalledPlugin{
		ID: id, Name: w.named[id], Description: w.described[id], Version: w.versions[id], State: state,
	}
}

// holds checks that the list holds exactly want as the row of its plugin.
func (w *installedWorld) holds(want pluginkit.InstalledPlugin) error {
	at := slices.IndexFunc(w.listed, func(plugin pluginkit.InstalledPlugin) bool { return plugin.ID == want.ID })
	if at < 0 {
		return fmt.Errorf("the list holds %+v, want a row of %s", w.listed, want.ID)
	}
	if got := w.listed[at]; got != want {
		return fmt.Errorf("the list holds %+v, want %+v", got, want)
	}
	return nil
}

// holdsAs checks the row of the plugin id in state, with its manifest's words and no step or reason.
func (w *installedWorld) holdsAs(id, state string) error {
	return w.holds(w.row(id, pluginkit.PluginState(state)))
}

// holdsBare checks the row of the plugin id in state, with its manifest's name and no description or version.
func (w *installedWorld) holdsBare(id, state string) error {
	return w.holds(pluginkit.InstalledPlugin{ID: id, Name: w.named[id], State: pluginkit.PluginState(state)})
}

// holdsPaused checks the row of the plugin id as paused at step, with its own error reading reason.
func (w *installedWorld) holdsPaused(id, step, reason string) error {
	want := w.row(id, pluginkit.PluginPaused)
	want.PausedStep, want.PausedReason = pluginkit.Step(step), reason
	return w.holds(want)
}

// namesInOrder checks that the list holds exactly the plugins of list, in order.
func (w *installedWorld) namesInOrder(list string) error {
	ids := make([]string, 0, len(w.listed))
	for _, plugin := range w.listed {
		ids = append(ids, plugin.ID)
	}
	if want := names(list); !slices.Equal(ids, want) {
		return fmt.Errorf("the list names %q, want %q", ids, want)
	}
	return nil
}

// leavesOut checks that the list holds no row of the plugin id.
func (w *installedWorld) leavesOut(id string) error {
	if slices.ContainsFunc(w.listed, func(plugin pluginkit.InstalledPlugin) bool { return plugin.ID == id }) {
		return fmt.Errorf("the list holds %+v, want no row of %s", w.listed, id)
	}
	return nil
}

// initializeInstalled gives each installed plugins scenario a world of its own, the boot's Given steps and its own.
func initializeInstalled(t *testing.T) func(*godog.ScenarioContext) {
	return func(sc *godog.ScenarioContext) {
		w := &installedWorld{
			boot: newBootWorld(t), named: map[string]string{}, described: map[string]string{},
			versions: map[string]string{},
		}
		givenBoot(sc, w.boot)
		sc.Step(`^the manifest of "([^"]*)" names it "([^"]*)"$`, func(id, name string) { set(w.named, id, name) })
		sc.Step(`^the manifest of "([^"]*)" describes it as "([^"]*)"$`,
			func(id, text string) { set(w.described, id, text) })
		sc.Step(`^the manifest of "([^"]*)" gives the version "([^"]*)"$`,
			func(id, version string) { set(w.versions, id, version) })
		sc.Step(`^the application lists the installed plugins$`, w.list)
		sc.Step(`^the list holds "([^"]*)" as "(active|inactive)"$`, w.holdsAs)
		sc.Step(`^the list holds "([^"]*)" as "(active|inactive)" with no description and no version$`, w.holdsBare)
		sc.Step(`^the list holds "([^"]*)" as paused at "([^"]*)", its reason reading "(.*)"$`, w.holdsPaused)
		sc.Step(`^the list names ((?:"[^"]*"(?:, | and )?)+), in that order$`, w.namesInOrder)
		sc.Step(`^the list leaves out "([^"]*)"$`, w.leavesOut)
	}
}
