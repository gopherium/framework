// SPDX-License-Identifier: Apache-2.0

package features_test

import (
	"context"
	"fmt"
	"slices"
	"testing"
	"time"

	"github.com/cucumber/godog"

	"github.com/gopherium/framework/pluginkit"
)

// screensHook is the name of the hook a scenario asks the plugins through.
const screensHook = "admin.screens"

// gated is the interface an application asks its plugins through.
type gated interface {
	ID() string
	pluginkit.ScreenGate
}

var _ pluginkit.ScreenGate = (*gatePlugin)(nil)

// gatePlugin is a plugin of a scenario that answers whether its screens show.
type gatePlugin struct {
	*scenePlugin
	hides bool
}

// ShowsScreens handles the call, then answers that the screens show unless the scenario hides them.
func (g *gatePlugin) ShowsScreens(ctx context.Context) (bool, error) {
	if err := g.Handle(ctx); err != nil {
		return false, err
	}
	return !g.hides, nil
}

// gateWorld is the part of a scenario's state only the screen gate steps use.
type gateWorld struct {
	gates  map[string]*gatePlugin
	hidden []string
}

// gateWorldKey carries a scenario's gate state in its context.
type gateWorldKey struct{}

// gatesOf returns the gate state of the scenario ctx belongs to.
func gatesOf(ctx context.Context) *gateWorld {
	return ctx.Value(gateWorldKey{}).(*gateWorld)
}

// registerGates registers the plugins of list, in order after any registered before, as answering for their screens.
func registerGates(ctx context.Context, list string) {
	w, gates := worldOf(ctx), gatesOf(ctx)
	w.name = screensHook
	for _, id := range names(list) {
		p := &scenePlugin{id: id, world: w}
		g := &gatePlugin{scenePlugin: p}
		w.plugins = append(w.plugins, g)
		w.byID[id] = p
		gates.gates[id] = g
	}
}

// hideScreens makes the plugin id answer that its screens hide.
func hideScreens(ctx context.Context, id string) error {
	g, ok := gatesOf(ctx).gates[id]
	if !ok {
		return fmt.Errorf("no plugin %q answers for its screens in this scenario", id)
	}
	g.hides = true
	return nil
}

// hiddenOf lists p after ids when its screens hide, turning an answer after ctx ended into the context's error.
func hiddenOf(ctx context.Context, p gated, ids []string) ([]string, error) {
	shows, err := p.ShowsScreens(ctx)
	if err == nil {
		err = ctx.Err()
	}
	if err != nil || shows {
		return ids, err
	}
	return append(ids, p.ID()), nil
}

// askHidden asks every plugin of the scenario whether its screens show, through one filter hook.
func askHidden(ctx context.Context) error {
	w := worldOf(ctx)
	hook, err := pluginkit.NewHook[gated](w.name, w.plugins, w.settings())
	if err != nil {
		return err
	}
	w.onFakeClock(func() {
		gatesOf(ctx).hidden = pluginkit.Filter(context.Background(), hook, []string(nil), hiddenOf)
	})
	return nil
}

// hiddenAre checks that the plugins of list, and no others, were listed as hiding their screens, in that order.
func hiddenAre(ctx context.Context, list string) error {
	if hidden, want := gatesOf(ctx).hidden, names(list); !slices.Equal(hidden, want) {
		return fmt.Errorf("listed %q as hiding their screens, want %q", hidden, want)
	}
	return nil
}

// noneHidden checks that no plugin was listed as hiding its screens.
func noneHidden(ctx context.Context) error {
	if hidden := gatesOf(ctx).hidden; len(hidden) != 0 {
		return fmt.Errorf("listed %q as hiding their screens, want none", hidden)
	}
	return nil
}

// initializeScreenGate returns the steps of the screen gate feature, each scenario with its own world on a fake clock.
func initializeScreenGate(t *testing.T) func(*godog.ScenarioContext) {
	return func(sc *godog.ScenarioContext) {
		sc.Before(func(ctx context.Context, _ *godog.Scenario) (context.Context, error) {
			w := &hookWorld{
				t: t, budget: time.Hour, timedFirings: 20, byID: map[string]*scenePlugin{}, late: map[string]bool{},
			}
			ctx = context.WithValue(ctx, hookWorldKey{}, w)
			return context.WithValue(ctx, gateWorldKey{}, &gateWorld{gates: map[string]*gatePlugin{}}), nil
		})
		sc.After(noPanicLeft)
		sc.Step(`^the plugins? (.+) answers? whether (?:its|their) screens show$`, registerGates)
		sc.Step(`^the plugin "([^"]*)" is registered without answering it$`, registerBystander)
		sc.Step(`^"([^"]*)" hides its screens$`, hideScreens)
		sc.Step(`^the hook has a budget of (\S+) ms$`, setBudget)
		sc.Step(`^"([^"]*)" takes (\S+) ms$`, setTakes)
		sc.Step(`^"([^"]*)" fails with "([^"]*)"$`, failsWith)
		sc.Step(`^"([^"]*)" panics with "([^"]*)"$`, panicsWith)
		sc.Step(`^the application asks which plugins hide their screens$`, askHidden)
		sc.Step(`^the plugins that hide their screens are (.+)$`, hiddenAre)
		sc.Step(`^no plugin hides its screens$`, noneHidden)
		sc.Step(`^the report holds "([^"]*)"( with the stack of the panic)?$`, reportHolds)
		sc.Step(`^the report marks the failure of "([^"]*)" as caused by the ended context$`, reportMarksContextEnded)
	}
}
