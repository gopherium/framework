// SPDX-License-Identifier: Apache-2.0

package gonsole

import (
	"errors"
	"fmt"
)

// Group is the commands one plugin offers under the namespace equal to its id.
type Group struct {
	// Namespace is the plugin id every command name in the group starts with.
	Namespace string
	// Commands are the plugin's commands.
	Commands []Command
}

// Provider is implemented by plugins that offer commands under the namespace equal to their id.
type Provider interface {
	// Commands returns the plugin's commands.
	Commands() []Command
}

// Walk returns the command groups of the plugins that implement Provider and an error naming each one that panicked.
func Walk[P interface{ ID() string }](plugins []P) ([]Group, error) {
	var groups []Group
	var panics []error
	for _, plugin := range plugins {
		offering, offers := any(plugin).(Provider)
		if !offers {
			continue
		}
		id := plugin.ID()
		commands, err := provided(id, offering)
		if err != nil {
			panics = append(panics, err)
		}
		if len(commands) > 0 {
			groups = append(groups, Group{Namespace: id, Commands: commands})
		}
	}
	return groups, errors.Join(panics...)
}

// provided returns the commands p offers, a panic inside it as an error naming the plugin id.
func provided(id string, p Provider) (commands []Command, err error) {
	defer func() {
		if value := recover(); value != nil {
			err = fmt.Errorf("plugin %s: commands panicked: %v", id, value)
		}
	}()
	return p.Commands(), nil
}

// Loaded is what registering the plugins answers.
type Loaded struct {
	// Groups are the command groups, one per plugin that offers commands.
	Groups []Group
}
