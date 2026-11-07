// SPDX-License-Identifier: Apache-2.0

// Package pluginstoretest checks that a [pluginkit.PluginStore] keeps the contract a boot relies on.
package pluginstoretest

import (
	"context"
	"maps"
	"sync"
	"testing"

	"github.com/gopherium/framework/pluginkit"
)

// Run runs every case of the contract, each on a new empty store from newStore.
func Run(t *testing.T, newStore func(t *testing.T) pluginkit.PluginStore) {
	t.Helper()
	for name, check := range cases() {
		t.Run(name, func(t *testing.T) {
			check(t, newStore(t))
		})
	}
}

// cases are the checks of the contract, by name.
func cases() map[string]func(t *testing.T, store pluginkit.PluginStore) {
	return map[string]func(t *testing.T, store pluginkit.PluginStore){
		"an empty store holds no record": func(t *testing.T, store pluginkit.PluginStore) {
			want(t, store, nil)
		},
		"activating a plugin it never saw records it active": func(t *testing.T, store pluginkit.PluginStore) {
			act(t, store.Activate(t.Context(), "events"))
			want(t, store, map[string]pluginkit.PluginRecord{"events": active("events")})
		},
		"pausing a plugin it never saw records it paused": func(t *testing.T, store pluginkit.PluginStore) {
			act(t, store.Pause(t.Context(), "events", "a setting is missing"))
			want(t, store, map[string]pluginkit.PluginRecord{"events": paused("events", "a setting is missing")})
		},
		"pausing an active plugin keeps its failure": func(t *testing.T, store pluginkit.PluginStore) {
			act(t, store.Activate(t.Context(), "events"))
			act(t, store.Pause(t.Context(), "events", "a setting is missing"))
			want(t, store, map[string]pluginkit.PluginRecord{"events": paused("events", "a setting is missing")})
		},
		"pausing a paused plugin keeps the newer failure": func(t *testing.T, store pluginkit.PluginStore) {
			act(t, store.Pause(t.Context(), "events", "a setting is missing"))
			act(t, store.Pause(t.Context(), "events", "no route table"))
			want(t, store, map[string]pluginkit.PluginRecord{"events": paused("events", "no route table")})
		},
		"activating a paused plugin clears its failure": func(t *testing.T, store pluginkit.PluginStore) {
			act(t, store.Pause(t.Context(), "events", "a setting is missing"))
			act(t, store.Activate(t.Context(), "events"))
			want(t, store, map[string]pluginkit.PluginRecord{"events": active("events")})
		},
		"activating an active plugin keeps it active": func(t *testing.T, store pluginkit.PluginStore) {
			act(t, store.Activate(t.Context(), "events"))
			act(t, store.Activate(t.Context(), "events"))
			want(t, store, map[string]pluginkit.PluginRecord{"events": active("events")})
		},
		"activating a plugin keeps every other record": func(t *testing.T, store pluginkit.PluginStore) {
			act(t, store.Pause(t.Context(), "feed", "no route table"))
			act(t, store.Activate(t.Context(), "events"))
			want(t, store, map[string]pluginkit.PluginRecord{
				"events": active("events"), "feed": paused("feed", "no route table"),
			})
		},
		"one read answers every plugin by its id": func(t *testing.T, store pluginkit.PluginStore) {
			act(t, store.Activate(t.Context(), "events"))
			act(t, store.Pause(t.Context(), "feed", "no route table"))
			want(t, store, map[string]pluginkit.PluginRecord{
				"events": active("events"), "feed": paused("feed", "no route table"),
			})
		},
	}
}

// active returns the record of an active plugin id.
func active(id string) pluginkit.PluginRecord {
	return pluginkit.PluginRecord{ID: id, State: pluginkit.PluginActive}
}

// paused returns the record of the plugin id paused for failure.
func paused(id, failure string) pluginkit.PluginRecord {
	return pluginkit.PluginRecord{ID: id, State: pluginkit.PluginPaused, LastError: failure}
}

// act stops t when the store refused a write.
func act(t testing.TB, err error) {
	t.Helper()
	if err != nil {
		t.Fatalf("the store refused a write: %v", err)
	}
}

// want checks that store holds exactly records.
func want(t testing.TB, store pluginkit.PluginStore, records map[string]pluginkit.PluginRecord) {
	t.Helper()
	got, err := store.Records(context.Background())
	if err != nil {
		t.Fatalf("the store could not read its records: %v", err)
		return
	}
	if !maps.Equal(got, records) {
		t.Errorf("records = %+v, want %+v", got, records)
	}
}

// Memory is a [pluginkit.PluginStore] kept in memory, safe for concurrent use.
type Memory struct {
	mu      sync.Mutex
	records map[string]pluginkit.PluginRecord
}

// NewMemory returns a store in memory holding records.
func NewMemory(records ...pluginkit.PluginRecord) *Memory {
	held := make(map[string]pluginkit.PluginRecord, len(records))
	for _, record := range records {
		held[record.ID] = record
	}
	return &Memory{records: held}
}

// Records returns a copy of every record held.
func (m *Memory) Records(context.Context) (map[string]pluginkit.PluginRecord, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return maps.Clone(m.records), nil
}

// Activate marks the plugin id active and clears its error.
func (m *Memory) Activate(_ context.Context, id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.records[id] = active(id)
	return nil
}

// Pause marks the plugin id paused with the text of its failure.
func (m *Memory) Pause(_ context.Context, id, failure string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.records[id] = paused(id, failure)
	return nil
}
