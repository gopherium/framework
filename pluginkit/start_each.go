// SPDX-License-Identifier: Apache-2.0

package pluginkit

import (
	"context"
	"time"
)

// Step names the part of a plugin's boot a [Failure] happened in.
type Step string

const (
	// StepMigrate is the migration pass, and a plugin that fails it does not start.
	StepMigrate Step = "migrate"
	// StepRecord is the note of a migration that ran, and a plugin that fails it still starts.
	StepRecord Step = "record"
	// StepStart is a plugin's Start.
	StepStart Step = "start"
	// StepRegister is a plugin's Register, called by [RegisterEach].
	StepRegister Step = "register"
)

// Failure is one step one plugin failed, or was still waiting for when the context ended.
type Failure struct {
	ID   string
	Step Step
	// Panicked is true only for a panic this step recovered, never for an error that merely holds one.
	Panicked bool
	Err      error
}

// StartedPlugin is one plugin whose Start returned nil, and how long that Start took.
type StartedPlugin struct {
	ID   string
	Took time.Duration
}

// StartOutcome is what [Host.StartEach] answers, both lists in registration order, a failed record beside its start.
type StartOutcome struct {
	Started []StartedPlugin
	Failed  []Failure
}

// StartEach migrates then starts each plugin on its own past any failure, stopping none, and errs only on a store read.
func (h *Host) StartEach(ctx context.Context) (StartOutcome, error) {
	versions, err := h.readVersions(ctx)
	if err != nil {
		return StartOutcome{}, err
	}
	migrations := make([]Failure, len(h.plugins))
	for i, p := range h.plugins {
		migrations[i] = h.migrateUnlessEnded(ctx, p, versions)
	}
	var outcome StartOutcome
	for i, p := range h.plugins {
		outcome.startAfter(ctx, p, migrations[i])
	}
	return outcome, nil
}

// migrateUnlessEnded migrates p while ctx lasts, answering a [Failure] whose Err is nil when nothing failed.
func (h *Host) migrateUnlessEnded(ctx context.Context, p Plugin, versions map[string]string) Failure {
	if err := safeCall(ctx, p.ID(), string(StepMigrate), context.Context.Err); err != nil {
		return failureOf(p.ID(), StepMigrate, err)
	}
	step, err := h.migrateUnlessCurrent(ctx, p, versions)
	return failureOf(p.ID(), step, err)
}

// startAfter lists what p's migration did when it failed, then starts p unless that failure rules it out.
func (o *StartOutcome) startAfter(ctx context.Context, p Plugin, migration Failure) {
	if migration.Err != nil {
		o.Failed = append(o.Failed, migration)
	}
	if migration.Err != nil && migration.Step != StepRecord {
		return
	}
	began := time.Now()
	err := safeCall(ctx, p.ID(), string(StepStart), context.Context.Err)
	if err == nil {
		err = safeCall(ctx, p.ID(), string(StepStart), p.Start)
	}
	if err != nil {
		o.Failed = append(o.Failed, failureOf(p.ID(), StepStart, err))
		return
	}
	o.Started = append(o.Started, StartedPlugin{ID: p.ID(), Took: time.Since(began)})
}

// failureOf returns the failure of the plugin id at step, marked as panicked only when err is that step's own panic.
func failureOf(id string, step Step, err error) Failure {
	_, panicked := err.(panicError)
	return Failure{ID: id, Step: step, Panicked: panicked, Err: err}
}
