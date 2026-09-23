// SPDX-License-Identifier: Apache-2.0

package gonsole

// Group is the commands one plugin offers under the namespace equal to its id.
type Group struct {
	// Namespace is the plugin id every command name in the group starts with.
	Namespace string
	// Commands are the plugin's commands.
	Commands []Command
}

// Loaded is what registering the plugins answers.
type Loaded struct {
	// Groups are the command groups, one per plugin that offers commands.
	Groups []Group
}
