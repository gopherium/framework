// SPDX-License-Identifier: Apache-2.0

package pluginkit

import (
	"context"
	"errors"
	"fmt"
	"reflect"
)

// errNoPlugin is the failure of a Register that answered neither a plugin nor an error.
var errNoPlugin = errors.New("Register answered no plugin")

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

// RegisterEach calls each entry's Register with depsFor of its id inside a recover, past any failure or mismatched ID.
func RegisterEach[D any, P interface{ ID() string }](
	entries []Registration[D, P], depsFor func(id string) D,
) RegisterOutcome[P] {
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
func register[D any, P interface{ ID() string }](entry Registration[D, P], deps D) (plugin P, err error) {
	err = guarded(context.Background(), entry.ID+" "+string(StepRegister), func(context.Context) error {
		var registerErr error
		plugin, registerErr = entry.Register(deps)
		if registerErr != nil {
			return registerErr
		}
		if isNil(plugin) {
			return errNoPlugin
		}
		if id := plugin.ID(); id != entry.ID {
			return fmt.Errorf("the plugin calls itself %q", id)
		}
		return nil
	})
	return plugin, err
}

// isNil reports whether plugin holds nothing: a nil interface, pointer, map, slice, channel or function.
func isNil(plugin any) bool {
	value := reflect.ValueOf(plugin)
	switch value.Kind() {
	case reflect.Invalid:
		return true
	case reflect.Pointer, reflect.Map, reflect.Slice, reflect.Chan, reflect.Func:
		return value.IsNil()
	default:
		return false
	}
}
