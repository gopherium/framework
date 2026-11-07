// SPDX-License-Identifier: Apache-2.0

package pluginstoretest

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/gopherium/framework/pluginkit"
)

// recorder is a [testing.TB] that keeps each failure it is told about and goes on.
type recorder struct {
	testing.TB
	failures []string
}

// Helper marks nothing.
func (r *recorder) Helper() {}

// Errorf keeps the failure.
func (r *recorder) Errorf(format string, args ...any) {
	r.failures = append(r.failures, fmt.Sprintf(format, args...))
}

// Fatalf keeps the failure and returns, where a real test would stop.
func (r *recorder) Fatalf(format string, args ...any) {
	r.failures = append(r.failures, fmt.Sprintf(format, args...))
}

// unreadable is a store that cannot read its records.
type unreadable struct {
	pluginkit.PluginStore
}

// Records answers the store's failure.
func (unreadable) Records(context.Context) (map[string]pluginkit.PluginRecord, error) {
	return nil, errors.New("connection dropped")
}

func TestActReportsAWriteTheStoreRefused(t *testing.T) {
	r := &recorder{}

	act(r, errors.New("connection dropped"))

	if len(r.failures) != 1 || r.failures[0] != "the store refused a write: connection dropped" {
		t.Errorf("failures = %q, want the refused write", r.failures)
	}
}

func TestWantReportsAStoreThatCannotRead(t *testing.T) {
	r := &recorder{}

	want(r, unreadable{}, nil)

	if len(r.failures) != 1 || r.failures[0] != "the store could not read its records: connection dropped" {
		t.Errorf("failures = %q, want the failed read", r.failures)
	}
}

func TestWantReportsRecordsThatDiffer(t *testing.T) {
	r := &recorder{}
	store := NewMemory(pluginkit.PluginRecord{ID: "events", State: pluginkit.PluginActive})

	want(r, store, map[string]pluginkit.PluginRecord{"events": {ID: "events", State: pluginkit.PluginPaused}})

	if len(r.failures) != 1 {
		t.Errorf("failures = %q, want one naming the records that differ", r.failures)
	}
}
