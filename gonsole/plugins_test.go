// SPDX-License-Identifier: Apache-2.0

package gonsole_test

import (
	"errors"
	"reflect"
	"slices"
	"testing"

	"github.com/gopherium/framework/gonsole"
)

// compiled is a fake compiled plugin, known by its id.
type compiled interface {
	ID() string
}

// silent is a compiled plugin that offers no commands and counts each read of its id.
type silent struct {
	id    string
	reads *int
}

// ID returns the plugin's id.
func (s silent) ID() string {
	if s.reads != nil {
		*s.reads++
	}
	return s.id
}

// nameless is a compiled plugin that offers commands and panics when asked for its id.
type nameless struct {
	calls *int
}

// ID panics.
func (nameless) ID() string {
	panic("no id")
}

// Commands returns one command and counts the call.
func (n nameless) Commands() []gonsole.Command {
	*n.calls++
	return []gonsole.Command{echo("nameless:one")}
}

// provider is a compiled plugin that offers the commands it holds and counts each call of Commands.
type provider struct {
	id       string
	commands []gonsole.Command
	calls    *int
	panics   any
}

// ID returns the plugin's id.
func (p provider) ID() string {
	return p.id
}

// Commands returns the plugin's commands, or panics with the value it holds.
func (p provider) Commands() []gonsole.Command {
	if p.calls != nil {
		*p.calls++
	}
	if p.panics != nil {
		panic(p.panics)
	}
	return p.commands
}

var _ gonsole.Provider = provider{}

// namespaces returns the namespace of every group in order.
func namespaces(groups []gonsole.Group) []string {
	var held []string
	for _, group := range groups {
		held = append(held, group.Namespace)
	}
	return held
}

// names returns the full names of the commands in every group, one list per group.
func names(groups []gonsole.Group) map[string][]string {
	held := map[string][]string{}
	for _, group := range groups {
		for _, cmd := range group.Commands {
			held[group.Namespace] = append(held[group.Namespace], cmd.Name)
		}
	}
	return held
}

func TestWalkGathersTheCommandsOfEveryProviderInOrder(t *testing.T) {
	t.Parallel()

	plugins := []compiled{
		provider{id: "alpha", commands: []gonsole.Command{echo("alpha:one"), echo("alpha:two")}},
		provider{id: "beta", commands: []gonsole.Command{echo("beta:one")}},
	}

	groups, err := gonsole.Walk(plugins)

	if err != nil {
		t.Fatalf("Walk() error = %v, want nil", err)
	}
	if got := namespaces(groups); !slices.Equal(got, []string{"alpha", "beta"}) {
		t.Fatalf("namespaces = %v, want alpha then beta", got)
	}
	want := map[string][]string{"alpha": {"alpha:one", "alpha:two"}, "beta": {"beta:one"}}
	if got := names(groups); !reflect.DeepEqual(got, want) {
		t.Errorf("commands = %v, want %v", got, want)
	}
}

func TestWalkNamesEachGroupAfterItsPlugin(t *testing.T) {
	t.Parallel()

	groups, err := gonsole.Walk([]compiled{provider{id: "demo", commands: []gonsole.Command{echo("other:sync")}}})

	if err != nil || len(groups) != 1 || groups[0].Namespace != "demo" {
		t.Errorf("Walk() = %v, %v, want one group named demo", names(groups), err)
	}
}

func TestWalkAsksEachProviderOnce(t *testing.T) {
	t.Parallel()

	var alpha, beta int
	plugins := []compiled{
		provider{id: "alpha", commands: []gonsole.Command{echo("alpha:one")}, calls: &alpha},
		provider{id: "beta", commands: []gonsole.Command{echo("beta:one")}, calls: &beta},
	}

	_, _ = gonsole.Walk(plugins)

	if alpha != 1 || beta != 1 {
		t.Errorf("calls = %d, %d, want 1 each", alpha, beta)
	}
}

func TestWalkLeavesOutPluginsWithoutCommands(t *testing.T) {
	t.Parallel()

	plugins := []compiled{
		silent{id: "quiet"},
		provider{id: "none"},
		provider{id: "empty", commands: []gonsole.Command{}},
		provider{id: "alpha", commands: []gonsole.Command{echo("alpha:one")}},
	}

	groups, err := gonsole.Walk(plugins)

	if got := namespaces(groups); !slices.Equal(got, []string{"alpha"}) || err != nil {
		t.Errorf("Walk() = %v, %v, want only alpha and no error", got, err)
	}
}

func TestWalkReadsNoIDOfAPluginThatIsNoProvider(t *testing.T) {
	t.Parallel()

	var reads int

	_, _ = gonsole.Walk([]compiled{silent{id: "quiet", reads: &reads}})

	if reads != 0 {
		t.Errorf("ID() read %d times, want 0", reads)
	}
}

func TestWalkLetsAPanickingIDThrough(t *testing.T) {
	t.Parallel()

	var calls int
	defer func() {
		if value := recover(); value != "no id" || calls != 0 {
			t.Errorf("recovered %v after %d calls of Commands, want the panic of ID and no call", value, calls)
		}
	}()

	_, _ = gonsole.Walk([]compiled{nameless{calls: &calls}})
}

func TestWalkOfNoPlugins(t *testing.T) {
	t.Parallel()

	groups, err := gonsole.Walk[compiled](nil)

	if groups != nil || err != nil {
		t.Errorf("Walk() = %v, %v, want nil and nil", groups, err)
	}
}

func TestWalkKeepsTheOtherGroupsWhenAPluginPanics(t *testing.T) {
	t.Parallel()

	plugins := []compiled{
		provider{id: "alpha", commands: []gonsole.Command{echo("alpha:one")}},
		provider{id: "broken", panics: "boom"},
		provider{id: "beta", commands: []gonsole.Command{echo("beta:one")}},
	}

	groups, err := gonsole.Walk(plugins)

	if got := namespaces(groups); !slices.Equal(got, []string{"alpha", "beta"}) {
		t.Errorf("namespaces = %v, want alpha then beta", got)
	}
	if want := map[string][]string{"alpha": {"alpha:one"}, "beta": {"beta:one"}}; !reflect.DeepEqual(names(groups), want) {
		t.Errorf("groups = %v, want %v", names(groups), want)
	}
	if errorText(err) != "plugin broken: commands panicked: boom" {
		t.Errorf("Walk() error = %q, want the panic named", errorText(err))
	}
}

func TestWalkJoinsEveryPanic(t *testing.T) {
	t.Parallel()

	plugins := []compiled{provider{id: "a", panics: "x"}, provider{id: "b", panics: errors.New("y")}}

	groups, err := gonsole.Walk(plugins)

	if groups != nil {
		t.Errorf("groups = %v, want nil", namespaces(groups))
	}
	if errorText(err) != "plugin a: commands panicked: x\nplugin b: commands panicked: y" {
		t.Errorf("Walk() error = %q, want both panics", errorText(err))
	}
	var joined interface{ Unwrap() []error }
	if !errors.As(err, &joined) || len(joined.Unwrap()) != 2 {
		t.Errorf("Walk() error = %v, want two joined errors", err)
	}
}
