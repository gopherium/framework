// SPDX-License-Identifier: Apache-2.0

package pluginkit

import (
	"cmp"
	"slices"
	"strings"

	"github.com/gopherium/framework/pluginkit/wire"
)

// InstalledPlugin is one compiled plugin a Plugins screen lists, with its state and why it paused.
type InstalledPlugin struct {
	ID           string      `json:"id"`
	Name         string      `json:"name"`
	Description  string      `json:"description"`
	Version      string      `json:"version"`
	State        PluginState `json:"status"`
	PausedStep   Step        `json:"paused_step"`
	PausedReason string      `json:"paused_reason"`
}

// Installed lists each manifest the records hold as installed, by name then id, one with no record as active.
func Installed(manifests []wire.Manifest, records map[string]PluginRecord) []InstalledPlugin {
	listed := make([]InstalledPlugin, 0, len(manifests))
	for _, manifest := range manifests {
		record, known := records[manifest.ID]
		if !known {
			record = PluginRecord{ID: manifest.ID, State: PluginActive}
		}
		if record.State != PluginNotInstalled {
			listed = append(listed, installedFrom(manifest, record))
		}
	}
	slices.SortFunc(listed, byName)
	return listed
}

// byName orders two rows by name without regard to case, then by id.
func byName(a, b InstalledPlugin) int {
	return cmp.Or(cmp.Compare(strings.ToLower(a.Name), strings.ToLower(b.Name)), cmp.Compare(a.ID, b.ID))
}

// installedFrom returns the row of manifest in the state record holds, with a step and a reason only when paused.
func installedFrom(manifest wire.Manifest, record PluginRecord) InstalledPlugin {
	plugin := InstalledPlugin{
		ID: manifest.ID, Name: manifest.Name, Description: manifest.Description, Version: manifest.Version,
		State: record.State,
	}
	if record.State == PluginPaused {
		plugin.PausedStep, plugin.PausedReason = record.Step, record.LastError
	}
	return plugin
}
