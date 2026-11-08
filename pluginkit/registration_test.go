// SPDX-License-Identifier: Apache-2.0

package pluginkit_test

import (
	"errors"
	"testing"

	"github.com/gopherium/framework/pluginkit"
)

// listedDeps is what a test lends a plugin when it registers.
type listedDeps struct{ id string }

// listedPlugin is a plugin a test registered, holding the deps it received.
type listedPlugin struct {
	id   string
	deps listedDeps
}

// ID returns the id the plugin calls itself.
func (p *listedPlugin) ID() string {
	return p.id
}

// entry is one registration a test lists, its Register noting its call in calls and answering through answer.
func entry(id string, calls *[]string, answer func() error) pluginkit.Registration[listedDeps, *listedPlugin] {
	register := func(deps listedDeps) (*listedPlugin, error) {
		*calls = append(*calls, "register "+id)
		if err := answer(); err != nil {
			return nil, err
		}
		return &listedPlugin{id: id, deps: deps}, nil
	}
	return pluginkit.Registration[listedDeps, *listedPlugin]{ID: id, Register: register}
}

// succeeds answers no error.
func succeeds() error {
	return nil
}

// answers returns an answer that is err.
func answers(err error) func() error {
	return func() error { return err }
}

// panicsWith returns an answer that panics with value.
func panicsWith(value any) func() error {
	return func() error { panic(value) }
}

// depsOf returns the deps of each id, noting each call in calls.
func depsOf(calls *[]string) func(id string) listedDeps {
	return func(id string) listedDeps {
		*calls = append(*calls, "deps "+id)
		return listedDeps{id: id}
	}
}

// registeredIDs returns the ids of the plugins in outcome that registered, in order.
func registeredIDs(outcome pluginkit.RegisterOutcome[*listedPlugin]) []string {
	ids := []string{}
	for _, plugin := range outcome.Registered {
		ids = append(ids, plugin.id)
	}
	return ids
}

// failures returns the failures of outcome with their errors as text.
func failures(outcome pluginkit.RegisterOutcome[*listedPlugin]) []failureWant {
	got := []failureWant{}
	for _, failure := range outcome.Failed {
		got = append(got, failureWant{failure.ID, failure.Step, failure.Panicked, failure.Err.Error()})
	}
	return got
}

func TestRegisterEachRegistersEveryEntryInOrderAfterBuildingItsDeps(t *testing.T) {
	t.Parallel()

	var calls []string
	entries := []pluginkit.Registration[listedDeps, *listedPlugin]{
		entry("events", &calls, succeeds), entry("feed", &calls, succeeds), entry("gallery", &calls, succeeds),
	}

	outcome := pluginkit.RegisterEach(entries, depsOf(&calls))

	wantEqual(t, "registered", registeredIDs(outcome), []string{"events", "feed", "gallery"})
	wantEqual(t, "failed", failures(outcome), []failureWant{})
	wantEqual(t, "calls", calls, []string{
		"deps events", "register events", "deps feed", "register feed", "deps gallery", "register gallery",
	})
}

func TestRegisterEachHandsEachEntryTheDepsOfItsOwnID(t *testing.T) {
	t.Parallel()

	var calls []string
	entries := []pluginkit.Registration[listedDeps, *listedPlugin]{
		entry("events", &calls, succeeds), entry("feed", &calls, succeeds),
	}

	outcome := pluginkit.RegisterEach(entries, depsOf(&calls))

	for _, plugin := range outcome.Registered {
		wantEqual(t, plugin.id+"'s deps", plugin.deps, listedDeps{id: plugin.id})
	}
}

func TestRegisterEachGoesOnPastAFailingRegisterAndNamesIt(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		answer   func() error
		panicked bool
		text     string
	}{
		"an error": {answers(errors.New("the title is too long")), false, "register: the title is too long"},
		"a panic":  {panicsWith("boom"), true, "register panicked: boom"},
		"an error that holds the text of a panic": {
			answers(errors.New("pluginkit: feed register panicked: boom")), false,
			"register: pluginkit: feed register panicked: boom",
		},
		"a panic whose text panics too": {
			panicsWith(selfPanicking{}), true, "register panicked: pluginkit_test.selfPanicking whose text panicked",
		},
	}
	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			var calls []string
			entries := []pluginkit.Registration[listedDeps, *listedPlugin]{
				entry("events", &calls, succeeds), entry("feed", &calls, tc.answer), entry("gallery", &calls, succeeds),
			}

			outcome := pluginkit.RegisterEach(entries, depsOf(&calls))

			wantEqual(t, "registered", registeredIDs(outcome), []string{"events", "gallery"})
			wantEqual(t, "failed", failures(outcome), []failureWant{
				{"feed", pluginkit.StepRegister, tc.panicked, "pluginkit: feed " + tc.text},
			})
		})
	}
}

func TestRegisterEachFailsAPluginWhoseIDIsNotItsEntrys(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		plugin   *listedPlugin
		panicked bool
		text     string
	}{
		"another id": {&listedPlugin{id: "news"}, false, `pluginkit: feed register: the plugin calls itself "news"`},
		"no id":      {&listedPlugin{}, false, `pluginkit: feed register: the plugin calls itself ""`},
		"no plugin": {
			nil, true, "pluginkit: feed register panicked: runtime error: invalid memory address or nil pointer dereference",
		},
	}
	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			var calls []string
			entries := []pluginkit.Registration[listedDeps, *listedPlugin]{
				entry("events", &calls, succeeds),
				{ID: "feed", Register: func(listedDeps) (*listedPlugin, error) { return tc.plugin, nil }},
			}

			outcome := pluginkit.RegisterEach(entries, depsOf(&calls))

			wantEqual(t, "registered", registeredIDs(outcome), []string{"events"})
			wantEqual(t, "failed", failures(outcome), []failureWant{
				{"feed", pluginkit.StepRegister, tc.panicked, tc.text},
			})
		})
	}
}

func TestRegisterEachWrapsTheErrorARegisterAnswered(t *testing.T) {
	t.Parallel()

	var calls []string
	tooLong := errors.New("the title is too long")

	outcome := pluginkit.RegisterEach([]pluginkit.Registration[listedDeps, *listedPlugin]{
		entry("feed", &calls, answers(tooLong)),
	}, depsOf(&calls))

	if !errors.Is(outcome.Failed[0].Err, tooLong) {
		t.Errorf("feed's error = %v, want it to wrap the error its Register answered", outcome.Failed[0].Err)
	}
}

func TestRegisterEachAnswersAnEmptyOutcomeForNoEntries(t *testing.T) {
	t.Parallel()

	var calls []string

	outcome := pluginkit.RegisterEach([]pluginkit.Registration[listedDeps, *listedPlugin]{}, depsOf(&calls))

	wantEqual(t, "outcome", outcome, pluginkit.RegisterOutcome[*listedPlugin]{})
	wantEqual(t, "calls", calls, []string(nil))
}

func TestRegisterEachLetsAPanicWhileBuildingDepsReachTheCaller(t *testing.T) {
	t.Parallel()

	var calls []string
	entries := []pluginkit.Registration[listedDeps, *listedPlugin]{
		entry("events", &calls, succeeds), entry("feed", &calls, succeeds),
	}
	depsFor := func(id string) listedDeps {
		calls = append(calls, "deps "+id)
		if id == "feed" {
			panic("no settings for feed")
		}
		return listedDeps{id: id}
	}

	defer func() {
		wantEqual(t, "the panic", recover(), any("no settings for feed"))
		wantEqual(t, "calls", calls, []string{"deps events", "register events", "deps feed"})
	}()
	pluginkit.RegisterEach(entries, depsFor)
	t.Error("RegisterEach returned, want the panic of depsFor to reach the caller")
}
