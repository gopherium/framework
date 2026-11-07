// SPDX-License-Identifier: Apache-2.0

package pluginkit

import "context"

// PluginState is where one compiled plugin stands for the application.
type PluginState string

const (
	// PluginNotInstalled is a compiled plugin the application has not installed, which a boot never registers.
	PluginNotInstalled PluginState = "not installed"
	// PluginInactive is a plugin turned off on purpose, which a boot never registers.
	PluginInactive PluginState = "inactive"
	// PluginActive is a plugin a boot registers and starts.
	PluginActive PluginState = "active"
	// PluginPaused is a plugin a boot stopped after it failed, which every boot tries again.
	PluginPaused PluginState = "paused"
)

// PluginRecord is what an application keeps about one plugin between boots.
type PluginRecord struct {
	ID    string
	State PluginState
	// LastError is the text of the failure that paused the plugin, and empty otherwise.
	LastError string
}

// PluginStore is an application's record of each plugin's state.
type PluginStore interface {
	// Records returns every record, keyed by plugin id, in one read.
	Records(ctx context.Context) (map[string]PluginRecord, error)
	// Activate marks the plugin id active and clears its error, adding its record when it has none.
	Activate(ctx context.Context, id string) error
	// Pause marks the plugin id paused with the text of its failure, adding its record when it has none.
	Pause(ctx context.Context, id, failure string) error
}
