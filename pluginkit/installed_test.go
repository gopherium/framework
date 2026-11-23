// SPDX-License-Identifier: Apache-2.0

package pluginkit_test

import (
	"encoding/json"
	"testing"

	"github.com/gopherium/framework/pluginkit"
	"github.com/gopherium/framework/pluginkit/wire"
)

// feedDescription is the description the feed's manifest gives.
const feedDescription = "Serves the published posts as an RSS feed."

// manifestOf returns the manifest of the plugin id named name, with no description or version.
func manifestOf(id, name string) wire.Manifest {
	return wire.Manifest{ID: id, Name: name}
}

// feedManifest returns the manifest of the feed, with its description and its version.
func feedManifest() wire.Manifest {
	return wire.Manifest{ID: "feed", Name: "Feed", Description: feedDescription, Version: "0.0.1"}
}

// feedRow returns the row of the feed in state, with no step and no reason.
func feedRow(state pluginkit.PluginState) pluginkit.InstalledPlugin {
	return pluginkit.InstalledPlugin{
		ID: "feed", Name: "Feed", Description: feedDescription, Version: "0.0.1", State: state,
	}
}

// idsOf returns the id of each listed plugin, in its order.
func idsOf(listed []pluginkit.InstalledPlugin) []string {
	ids := make([]string, 0, len(listed))
	for _, plugin := range listed {
		ids = append(ids, plugin.ID)
	}
	return ids
}

// recordsOf returns records keyed by plugin id.
func recordsOf(records ...pluginkit.PluginRecord) map[string]pluginkit.PluginRecord {
	byID := make(map[string]pluginkit.PluginRecord, len(records))
	for _, record := range records {
		byID[record.ID] = record
	}
	return byID
}

func TestInstalledListsAPluginWithTheNameDescriptionAndVersionOfItsManifest(t *testing.T) {
	t.Parallel()

	for _, state := range []pluginkit.PluginState{pluginkit.PluginActive, pluginkit.PluginInactive} {
		t.Run(string(state), func(t *testing.T) {
			t.Parallel()

			got := pluginkit.Installed([]wire.Manifest{feedManifest()}, recordsOf(record("feed", state)))

			wantEqual(t, "listed", got, []pluginkit.InstalledPlugin{feedRow(state)})
		})
	}
}

func TestInstalledGivesAPausedPluginItsStepAndItsOwnError(t *testing.T) {
	t.Parallel()

	records := recordsOf(pluginkit.PluginRecord{
		ID: "feed", State: pluginkit.PluginPaused, Step: pluginkit.StepRegister, LastError: "the title is too long",
	})

	got := pluginkit.Installed([]wire.Manifest{feedManifest()}, records)

	want := feedRow(pluginkit.PluginPaused)
	want.PausedStep, want.PausedReason = pluginkit.StepRegister, "the title is too long"
	wantEqual(t, "listed", got, []pluginkit.InstalledPlugin{want})
}

func TestInstalledGivesAStepAndAReasonOnlyToAPausedPlugin(t *testing.T) {
	t.Parallel()

	for _, state := range []pluginkit.PluginState{pluginkit.PluginActive, pluginkit.PluginInactive} {
		t.Run(string(state), func(t *testing.T) {
			t.Parallel()

			records := recordsOf(pluginkit.PluginRecord{
				ID: "feed", State: state, Step: pluginkit.StepStart, LastError: "no route table",
			})

			got := pluginkit.Installed([]wire.Manifest{feedManifest()}, records)

			wantEqual(t, "listed", got, []pluginkit.InstalledPlugin{feedRow(state)})
		})
	}
}

func TestInstalledLeavesOutAPluginTheRecordsHoldAsNotInstalled(t *testing.T) {
	t.Parallel()

	manifests := []wire.Manifest{manifestOf("events", "Events"), feedManifest()}
	records := recordsOf(record("events", pluginkit.PluginActive), record("feed", pluginkit.PluginNotInstalled))

	got := pluginkit.Installed(manifests, records)

	wantEqual(t, "listed", got, []pluginkit.InstalledPlugin{
		{ID: "events", Name: "Events", State: pluginkit.PluginActive},
	})
}

func TestInstalledLeavesOutARecordWithNoManifest(t *testing.T) {
	t.Parallel()

	records := recordsOf(record("feed", pluginkit.PluginActive), record("gallery", pluginkit.PluginPaused))

	got := pluginkit.Installed([]wire.Manifest{feedManifest()}, records)

	wantEqual(t, "listed", got, []pluginkit.InstalledPlugin{feedRow(pluginkit.PluginActive)})
}

func TestInstalledListsAManifestWithNoRecordAsActive(t *testing.T) {
	t.Parallel()

	for name, records := range map[string]map[string]pluginkit.PluginRecord{
		"no records at all":      nil,
		"records of others only": recordsOf(record("events", pluginkit.PluginInactive)),
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			got := pluginkit.Installed([]wire.Manifest{feedManifest()}, records)

			wantEqual(t, "listed", got, []pluginkit.InstalledPlugin{feedRow(pluginkit.PluginActive)})
		})
	}
}

func TestInstalledListsByNameThenByID(t *testing.T) {
	t.Parallel()

	manifests := []wire.Manifest{
		manifestOf("events", "Calendar"), manifestOf("gallery", "Calendar"), manifestOf("feed", "Atom"),
		manifestOf("archive", "Calendar"),
	}

	got := pluginkit.Installed(manifests, nil)

	wantEqual(t, "ids", idsOf(got), []string{"feed", "archive", "events", "gallery"})
}

func TestInstalledComparesNamesWithoutRegardToCase(t *testing.T) {
	t.Parallel()

	manifests := []wire.Manifest{
		manifestOf("zines", "Zines"), manifestOf("upper", "Atom"), manifestOf("lower", "atom"),
		manifestOf("bold", "BOLD"),
	}

	got := pluginkit.Installed(manifests, nil)

	wantEqual(t, "ids", idsOf(got), []string{"lower", "upper", "bold", "zines"})
}

func TestInstalledSaysWhetherAPluginDeclaresSettings(t *testing.T) {
	t.Parallel()

	feed := feedManifest()
	feed.Settings = []wire.Setting{{Key: "title", Type: "text"}}
	manifests := []wire.Manifest{manifestOf("events", "Events"), feed}

	got := pluginkit.Installed(manifests, nil)

	withSettings := feedRow(pluginkit.PluginActive)
	withSettings.HasSettings = true
	wantEqual(t, "listed", got, []pluginkit.InstalledPlugin{
		{ID: "events", Name: "Events", State: pluginkit.PluginActive}, withSettings,
	})
}

func TestInstalledAnswersAnEmptyListThatEncodesAsOne(t *testing.T) {
	t.Parallel()

	got := pluginkit.Installed(nil, recordsOf(record("feed", pluginkit.PluginActive)))

	data, err := json.Marshal(got)

	assertErrorFree(t, err)
	wantEqual(t, "JSON", string(data), "[]")
}

func TestInstalledPluginEncodesUnderTheNamesAScreenReads(t *testing.T) {
	t.Parallel()

	row := feedRow(pluginkit.PluginPaused)
	row.PausedStep, row.PausedReason, row.HasSettings = pluginkit.StepStart, "no route table", true

	data, err := json.Marshal(row)

	assertErrorFree(t, err)
	wantEqual(t, "JSON", string(data), `{"id":"feed","name":"Feed","description":"`+feedDescription+
		`","version":"0.0.1","status":"paused","paused_step":"start","paused_reason":"no route table",`+
		`"has_settings":true}`)
}
