// SPDX-License-Identifier: Apache-2.0

package pluginkit

import (
	"context"
	"fmt"
)

// Boot is one start of the application's plugins, from their records to their start.
type Boot struct {
	store   PluginStore
	records map[string]PluginRecord
}

// NewBoot reads every record of store once.
func NewBoot(ctx context.Context, store PluginStore) (*Boot, error) {
	var records map[string]PluginRecord
	err := guarded(ctx, "read the plugin records", func(ctx context.Context) error {
		var readErr error
		records, readErr = store.Records(ctx)
		return readErr
	})
	if err != nil {
		return nil, err
	}
	return &Boot{store: store, records: records}, nil
}

// Register records each new entry active, registers the active and paused ones, and pauses each that fails.
func Register[D, P any](ctx context.Context, boot *Boot, entries []Registration[D, P],
	depsFor func(id string) D) (RegisterOutcome[P], error) {
	wanted, err := admitted(ctx, boot, entries)
	if err != nil {
		return RegisterOutcome[P]{}, err
	}
	outcome := RegisterEach(wanted, depsFor)
	return outcome, boot.pauseEach(ctx, outcome.Failed)
}

// admitted returns the entries boot registers, in order, recording each new one active.
func admitted[D, P any](ctx context.Context, boot *Boot, entries []Registration[D, P]) ([]Registration[D, P], error) {
	var wanted []Registration[D, P]
	for _, entry := range entries {
		registers, err := boot.admit(ctx, entry.ID)
		if err != nil {
			return nil, err
		}
		if registers {
			wanted = append(wanted, entry)
		}
	}
	return wanted, nil
}

// pauseEach pauses each failed plugin but one whose migration record failed, stopping at a write that fails.
func (b *Boot) pauseEach(ctx context.Context, failures []Failure) error {
	for _, failure := range failures {
		if failure.Step == StepRecord {
			continue
		}
		if err := b.Pause(ctx, failure.ID, failure.Err); err != nil {
			return err
		}
	}
	return nil
}

// admit records the plugin id active when boot has never seen it, and reports whether a boot registers it.
func (b *Boot) admit(ctx context.Context, id string) (bool, error) {
	if record, known := b.records[id]; known {
		return record.State == PluginActive || record.State == PluginPaused, nil
	}
	return true, b.activate(ctx, id)
}

// Pause pauses the plugin id for failure, a step that failed or a reason of the application's own.
func (b *Boot) Pause(ctx context.Context, id string, failure error) error {
	return guarded(ctx, "pause "+id, func(ctx context.Context) error {
		return b.store.Pause(ctx, id, failure.Error())
	})
}

// activate marks the plugin id active and clears its error.
func (b *Boot) activate(ctx context.Context, id string) error {
	return guarded(ctx, "activate "+id, func(ctx context.Context) error { return b.store.Activate(ctx, id) })
}

// Started pauses each plugin that failed to migrate or start, and activates each paused plugin that started.
func (b *Boot) Started(ctx context.Context, outcome StartOutcome) error {
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("pluginkit: the boot's context ended: %w", err)
	}
	if err := b.pauseEach(ctx, outcome.Failed); err != nil {
		return err
	}
	for _, started := range outcome.Started {
		if b.records[started.ID].State != PluginPaused {
			continue
		}
		if err := b.activate(ctx, started.ID); err != nil {
			return err
		}
	}
	return nil
}
