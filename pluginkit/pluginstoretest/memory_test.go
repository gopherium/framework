// SPDX-License-Identifier: Apache-2.0

package pluginstoretest_test

import (
	"maps"
	"testing"

	"github.com/gopherium/framework/pluginkit"
	"github.com/gopherium/framework/pluginkit/pluginstoretest"
)

func TestMemoryKeepsThePluginStoreContract(t *testing.T) {
	pluginstoretest.Run(t, func(*testing.T) pluginkit.PluginStore { return pluginstoretest.NewMemory() })
}

func TestNewMemoryHoldsTheRecordsItIsGiven(t *testing.T) {
	seeded := []pluginkit.PluginRecord{
		{ID: "events", State: pluginkit.PluginInactive},
		{ID: "feed", State: pluginkit.PluginPaused, LastError: "a setting is missing"},
	}

	got, err := pluginstoretest.NewMemory(seeded...).Records(t.Context())

	want := map[string]pluginkit.PluginRecord{"events": seeded[0], "feed": seeded[1]}
	if err != nil || !maps.Equal(got, want) {
		t.Errorf("Records() = %+v, %v, want %+v", got, err, want)
	}
}

func TestMemoryRecordsAreACopyOfWhatItHolds(t *testing.T) {
	store := pluginstoretest.NewMemory(pluginkit.PluginRecord{ID: "events", State: pluginkit.PluginActive})
	first, _ := store.Records(t.Context())

	delete(first, "events")
	again, _ := store.Records(t.Context())

	if len(again) != 1 {
		t.Errorf("Records() after a change to the last answer = %+v, want events still held", again)
	}
}
