// SPDX-License-Identifier: Apache-2.0

package pluginkit

import "context"

// Registration is one compiled plugin's manifest id and the function that registers it from its dependencies.
type Registration[D, P any] struct {
	ID       string
	Register func(D) (P, error)
}

// RegisterOutcome is what [RegisterEach] answers, both lists in the order of the entries.
type RegisterOutcome[P any] struct {
	Registered []P
	Failed     []Failure
}

// RegisterEach calls each entry's Register with depsFor of its id, each inside its own recover, past any failure.
func RegisterEach[D, P any](entries []Registration[D, P], depsFor func(id string) D) RegisterOutcome[P] {
	var outcome RegisterOutcome[P]
	for _, entry := range entries {
		plugin, err := register(entry, depsFor(entry.ID))
		if err != nil {
			outcome.Failed = append(outcome.Failed, failureOf(entry.ID, StepRegister, err))
			continue
		}
		outcome.Registered = append(outcome.Registered, plugin)
	}
	return outcome
}

// register calls entry's Register with deps inside a recover, naming the plugin in any failure.
func register[D, P any](entry Registration[D, P], deps D) (plugin P, err error) {
	err = guarded(context.Background(), entry.ID+" "+string(StepRegister), func(context.Context) error {
		var registerErr error
		plugin, registerErr = entry.Register(deps)
		return registerErr
	})
	return plugin, err
}
