// SPDX-License-Identifier: Apache-2.0

package gonsole

import (
	"context"
	"errors"
	"fmt"
	"sync"
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
	// Failed joins the errors of plugins that failed to register or to describe their commands.
	Failed error
	// Release stops every registered plugin and closes what registering opened.
	Release func(ctx context.Context) error
}

// memo is the one registration of the plugins a run makes.
type memo struct {
	register   func(ctx context.Context, call Call) (Loaded, error)
	call       Call
	audit      *audit
	mu         sync.Mutex
	done       bool
	loaded     Loaded
	err        error
	commands   map[string]Command
	namespaces map[string][]string
}

// answer registers the plugins once, in describe mode when describe is set, and returns that registration's answer.
func (m *memo) answer(ctx context.Context, describe bool) (Loaded, error) {
	if m.register == nil {
		return Loaded{}, nil
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if !m.done {
		m.done = true
		m.call.Describe = describe
		m.loaded, m.err = m.registered(ctx)
		if m.err == nil {
			m.admit()
		}
	}
	return m.loaded, m.err
}

// missing returns the lines of what failed to load: the registration error, or the plugin failures and the offences.
func (m *memo) missing() []string {
	if m.err != nil {
		return lines(m.err)
	}
	failed := lines(m.loaded.Failed)
	for _, offence := range m.audit.offences {
		failed = append(failed, lines(offence)...)
	}
	return failed
}

// admit indexes the commands of the registered groups that break no rule.
func (m *memo) admit() {
	var kept []Command
	for _, group := range m.loaded.Groups {
		kept = append(kept, m.audit.admit(group)...)
	}
	m.commands, m.namespaces = index(kept)
}

// registered returns what the program's Plugins answers, a panic inside it as the error naming the plugins.
func (m *memo) registered(ctx context.Context) (loaded Loaded, err error) {
	defer recoverRun("plugins", &err)
	return m.register(ctx, m.call)
}

// release stops the registered plugins under a context the end of the run cannot cancel, nothing when none registered.
func (m *memo) release(ctx context.Context) (err error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	defer recoverRun("plugins", &err)
	if err := stopWithin(context.WithoutCancel(ctx), m.loaded.Release); err != nil {
		return fmt.Errorf("release the plugins: %w", err)
	}
	return nil
}
