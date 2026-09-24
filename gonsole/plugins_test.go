// SPDX-License-Identifier: Apache-2.0

package gonsole_test

import (
	"bytes"
	"context"
	"errors"
	"flag"
	"fmt"
	"reflect"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

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

// registry is what a program's Plugins answers and the log of every registration and release.
type registry struct {
	mu             sync.Mutex
	groups         []gonsole.Group
	failed         error
	fail           error
	lost           error
	refuse         error
	withoutRelease bool
	calls          []gonsole.Call
	log            []string
}

// note appends one entry to the log.
func (r *registry) note(format string, args ...any) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.log = append(r.log, fmt.Sprintf(format, args...))
}

// register answers the groups, the plugin failures and the failure r holds, with a release unless r goes without one.
func (r *registry) register(_ context.Context, call gonsole.Call) (gonsole.Loaded, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.calls = append(r.calls, call)
	r.log = append(r.log, fmt.Sprintf("register describe=%t", call.Describe))
	loaded := gonsole.Loaded{Groups: r.groups, Failed: r.failed}
	if !r.withoutRelease {
		loaded.Release = r.release
	}
	return loaded, r.fail
}

// release logs whether its context is live and answers the failure r holds.
func (r *registry) release(ctx context.Context) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.log = append(r.log, fmt.Sprintf("release live=%t", ctx.Err() == nil))
	return r.lost
}

// demoGroups returns one plugin group, demo, whose demo:list answers a document, demo:move acts and demo:sync writes.
func demoGroups() []gonsole.Group {
	list := gonsole.Command{Name: "demo:list", Summary: "list the demo", JSON: true,
		Run: func(_ context.Context, call gonsole.Call) error {
			if call.JSON {
				return call.Encode(map[string][]string{"demo": {"synced"}})
			}
			_, err := fmt.Fprintln(call.Stdout, "synced")
			return err
		}}
	move := gonsole.Command{Name: "demo:move", Summary: "move the demo", Capability: "manage_demo",
		Run: func(_ context.Context, call gonsole.Call) error {
			_, err := fmt.Fprintf(call.Stdout, "moved as %s\n", call.Actor)
			return err
		}}
	sync := gonsole.Command{Name: "demo:sync", Summary: "sync the demo", Writes: true,
		Run: func(_ context.Context, call gonsole.Call) error {
			_, err := fmt.Fprintf(call.Stdout, "sync apply=%t\n", call.Apply)
			return err
		}}
	return []gonsole.Group{{Namespace: "demo", Commands: []gonsole.Command{list, move, sync}}}
}

// plugged returns a program called myapp whose plugins r registers, with a status command and report:plugins.
func plugged(r *registry) gonsole.Program {
	namespaced := gonsole.Command{
		Name:    "report:plugins",
		Summary: "print the plugin namespaces",
		Run: func(ctx context.Context, call gonsole.Call) error {
			loaded, err := call.Plugins(ctx)
			if err != nil {
				return err
			}
			_, err = fmt.Fprintln(call.Stdout, namespaces(loaded.Groups))
			return err
		},
	}
	return gonsole.Program{
		Name:     "myapp",
		Env:      settings(map[string]string{"MYAPP_DATABASE_URL": databaseAddress}),
		Database: "DATABASE_URL",
		Commands: []gonsole.Command{echo("status"), namespaced},
		Plugins:  r.register,
		Authorize: func(_ context.Context, call gonsole.Call, capability string) error {
			r.note("authorize %s for %s", call.Actor, capability)
			return r.refuse
		},
		Record: func(_ context.Context, call gonsole.Call, command string) error {
			r.note("record %s ran %s", call.Actor, command)
			return nil
		},
	}
}

// pluginsPage is the help page of the report:plugins command plugged declares.
const pluginsPage = `print the plugin namespaces

Usage:
  myapp report:plugins
`

func TestPluginsNeedACallTheEngineBuilt(t *testing.T) {
	t.Parallel()

	_, err := gonsole.Call{}.Plugins(t.Context())

	if want := "gonsole: no plugins in this call"; errorText(err) != want {
		t.Errorf("Plugins() error = %q, want %q", errorText(err), want)
	}
}

func TestPluginsOfAProgramWithoutPluginsAreEmpty(t *testing.T) {
	t.Parallel()

	var loaded gonsole.Loaded
	var err error
	cmd := echo("status")
	cmd.Run = func(ctx context.Context, call gonsole.Call) error {
		loaded, err = call.Plugins(ctx)
		return nil
	}

	got := execute(t, single(cmd), "status")

	if got.code != gonsole.ExitDone || loaded.Groups != nil || loaded.Release != nil || err != nil {
		t.Errorf("code %d, Plugins() = %v with release %t, %v, want 0, no group, no release and nil",
			got.code, namespaces(loaded.Groups), loaded.Release != nil, err)
	}
}

func TestRunRegistersThePluginsOnceAndReleasesThemOnce(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		fail error
		want string
	}{
		{"a registration that succeeds", nil, "[demo] <nil>"},
		{"a registration that fails", errors.New("the plugin table is locked"), "[demo] the plugin table is locked"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			r := &registry{groups: demoGroups(), fail: tc.fail}
			var answers []string
			ask := func(ctx context.Context, call gonsole.Call) {
				for range 2 {
					loaded, err := call.Plugins(ctx)
					answers = append(answers, fmt.Sprintf("%v %v", namespaces(loaded.Groups), err))
				}
			}
			p := plugged(r)
			p.Commands = append(p.Commands, gonsole.Command{
				Name: "report:sync", Summary: "sync every report", Capability: "manage_reports",
				Run: func(ctx context.Context, call gonsole.Call) error {
					ask(ctx, call)
					return nil
				},
			})
			p.Authorize = func(ctx context.Context, call gonsole.Call, _ string) error {
				ask(ctx, call)
				return nil
			}
			p.Record = func(context.Context, gonsole.Call, string) error { return nil }

			got := execute(t, p, "report:sync", "-as", actingAccount)

			if got.code != gonsole.ExitDone {
				t.Errorf("code = %d, want %d, stderr %q", got.code, gonsole.ExitDone, got.stderr)
			}
			if want := slices.Repeat([]string{tc.want}, 4); !slices.Equal(answers, want) {
				t.Errorf("answers = %q, want %q", answers, want)
			}
			if want := []string{"register describe=false", "release live=true"}; !slices.Equal(r.log, want) {
				t.Errorf("calls = %q, want %q", r.log, want)
			}
		})
	}
}

func TestPluginsRegisterOnceForEveryGoroutineOfARun(t *testing.T) {
	t.Parallel()

	r := &registry{groups: demoGroups()}
	p := plugged(r)
	p.Commands[1].Run = func(ctx context.Context, call gonsole.Call) error {
		var wg sync.WaitGroup
		for range 8 {
			wg.Go(func() { _, _ = call.Plugins(ctx) })
		}
		wg.Wait()
		return nil
	}

	got := execute(t, p, "report:plugins")

	want := []string{"register describe=false", "release live=true"}
	if got.code != gonsole.ExitDone || !slices.Equal(r.log, want) {
		t.Errorf("code %d, calls = %q, want 0 and %q", got.code, r.log, want)
	}
}

func TestPluginsRegisterWithTheStreamsAndSettingsOfTheRun(t *testing.T) {
	t.Parallel()

	r := &registry{}
	stdin := strings.NewReader("")
	var stdout, stderr bytes.Buffer

	code := plugged(r).Run(t.Context(), []string{"report:plugins"}, stdin, &stdout, &stderr)

	if code != gonsole.ExitDone || len(r.calls) != 1 {
		t.Fatalf("code = %d after %d registrations, want 0 after 1, stderr %q", code, len(r.calls), stderr.String())
	}
	call := r.calls[0]
	if call.Stdin != stdin || call.Stdout != &stdout || call.Stderr != &stderr {
		t.Errorf("registration streams = %p %p %p, want the streams of the run", call.Stdin, call.Stdout, call.Stderr)
	}
	if address, err := call.DatabaseURL(); address != databaseAddress || err != nil {
		t.Errorf("DatabaseURL() = %q, %v, want %q", address, err, databaseAddress)
	}
	if call.Env.Prefix != "MYAPP_" || call.Args != nil || call.JSON || call.Apply || call.Actor != "" || call.Describe {
		t.Errorf("registration call = %+v, want the program settings and nothing of the command", call)
	}
}

func TestPluginsHandACoreCommandTheFailuresWithoutAWarning(t *testing.T) {
	t.Parallel()

	failed := errors.New("plugin billing: no signing key")
	var seen error
	p := plugged(&registry{groups: demoGroups(), failed: failed})
	p.Commands[1].Run = func(ctx context.Context, call gonsole.Call) error {
		loaded, err := call.Plugins(ctx)
		seen = loaded.Failed
		return err
	}

	got := execute(t, p, "report:plugins")

	if got.code != gonsole.ExitDone || got.stderr != "" || !errors.Is(seen, failed) {
		t.Errorf("run = %d, %q, Failed %v, want 0, no warning and %v", got.code, got.stderr, seen, failed)
	}
}

func TestRegistrationGetsACallWithoutPlugins(t *testing.T) {
	t.Parallel()

	var inner error
	p := plugged(&registry{})
	p.Plugins = func(ctx context.Context, call gonsole.Call) (gonsole.Loaded, error) {
		_, inner = call.Plugins(ctx)
		return gonsole.Loaded{}, nil
	}

	got := execute(t, p, "report:plugins")

	if want := "gonsole: no plugins in this call"; got.code != gonsole.ExitDone || errorText(inner) != want {
		t.Errorf("code %d, inner Plugins() error = %q, want 0 and %q", got.code, errorText(inner), want)
	}
}

func TestPluginsAnswerTheLoadedAsRegistered(t *testing.T) {
	t.Parallel()

	r := &registry{groups: []gonsole.Group{
		{Namespace: "demo", Commands: []gonsole.Command{echo("demo:Sync"), echo("demo:sync")}},
		{Namespace: "list", Commands: []gonsole.Command{echo("list:all")}},
	}}
	var loaded gonsole.Loaded
	p := plugged(r)
	p.Commands[1].Run = func(ctx context.Context, call gonsole.Call) error {
		loaded, _ = call.Plugins(ctx)
		return nil
	}

	execute(t, p, "report:plugins")

	if got := namespaces(loaded.Groups); !slices.Equal(got, []string{"demo", "list"}) {
		t.Errorf("namespaces = %v, want demo then list", got)
	}
	if want := map[string][]string{"demo": {"demo:Sync", "demo:sync"}, "list": {"list:all"}}; !reflect.DeepEqual(
		names(loaded.Groups), want) {
		t.Errorf("groups = %v, want %v", names(loaded.Groups), want)
	}
}

func TestRunRegistersNoPluginsForARunThatAsksForNone(t *testing.T) {
	t.Parallel()

	for _, args := range [][]string{
		{"status"}, {"version"}, {"help", "report:plugins"}, {"report:plugins", "-h"}, {"report:nope"},
	} {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			t.Parallel()

			r := &registry{groups: demoGroups()}

			execute(t, plugged(r), args...)

			if len(r.log) != 0 {
				t.Errorf("calls = %q, want none", r.log)
			}
		})
	}
}

func TestRunRefusesABrokenProgramBeforeItRegisters(t *testing.T) {
	t.Parallel()

	for _, args := range [][]string{{"report:plugins"}, {"list"}, {"-h"}, {"demo:sync"}} {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			t.Parallel()

			r := &registry{groups: demoGroups()}
			p := plugged(r)
			p.Commands = append(p.Commands, echo("list"))

			got := execute(t, p, args...)

			if want := "myapp: gonsole: command \"list\" is a base command\n"; got.code != gonsole.ExitFailed ||
				got.stderr != want {
				t.Errorf("code %d, stderr = %q, want %d and %q", got.code, got.stderr, gonsole.ExitFailed, want)
			}
			if len(r.log) != 0 {
				t.Errorf("calls = %q, want none", r.log)
			}
		})
	}
}

func TestRunReleasesThePluginsWithALiveContextAfterTheRunEnds(t *testing.T) {
	t.Parallel()

	type key struct{}
	var seen string
	ctx, cancel := context.WithCancel(context.WithValue(t.Context(), key{}, "run"))
	defer cancel()
	p := plugged(&registry{})
	p.Plugins = func(context.Context, gonsole.Call) (gonsole.Loaded, error) {
		return gonsole.Loaded{Release: func(ctx context.Context) error {
			seen = fmt.Sprintf("%v live=%t", ctx.Value(key{}), ctx.Err() == nil)
			return nil
		}}, nil
	}
	p.Commands[1].Run = func(ctx context.Context, call gonsole.Call) error {
		_, err := call.Plugins(ctx)
		cancel()
		return err
	}

	var stderr strings.Builder
	code := p.Run(ctx, []string{"report:plugins"}, strings.NewReader(""), &stderr, &stderr)

	if code != gonsole.ExitDone || seen != "run live=true" {
		t.Errorf("code %d, release saw %q, want 0 and %q, stderr %q", code, seen, "run live=true", stderr.String())
	}
}

func TestRunReleasesThePluginsAfterTheDryRunNoticeAndTheRecord(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name   string
		args   []string
		stderr string
	}{
		{"a dry run", []string{"report:sync", "-as", actingAccount},
			"ran\nmyapp: dry run, nothing changed, pass -yes to apply\nreleased\n"},
		{"an applied run", []string{"report:sync", "-yes", "-as", actingAccount}, "ran\nrecorded\nreleased\n"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			p := plugged(&registry{})
			p.Plugins = func(_ context.Context, call gonsole.Call) (gonsole.Loaded, error) {
				return gonsole.Loaded{Release: func(context.Context) error {
					_, err := fmt.Fprintln(call.Stderr, "released")
					return err
				}}, nil
			}
			p.Commands = append(p.Commands, gonsole.Command{
				Name: "report:sync", Summary: "sync every report", Writes: true, Capability: "manage_reports",
				Run: func(ctx context.Context, call gonsole.Call) error {
					if _, err := call.Plugins(ctx); err != nil {
						return err
					}
					_, err := fmt.Fprintln(call.Stderr, "ran")
					return err
				},
			})
			p.Authorize = func(context.Context, gonsole.Call, string) error { return nil }
			p.Record = func(_ context.Context, call gonsole.Call, _ string) error {
				_, err := fmt.Fprintln(call.Stderr, "recorded")
				return err
			}

			got := execute(t, p, tc.args...)

			if got.code != gonsole.ExitDone || got.stderr != tc.stderr {
				t.Errorf("code %d, stderr = %q, want 0 and %q", got.code, got.stderr, tc.stderr)
			}
		})
	}
}

func TestRunAddsAReleaseFailureToTheAnswer(t *testing.T) {
	t.Parallel()

	lost := errors.New("the pool would not close")
	const released = "myapp: release the plugins: the pool would not close\n"
	cases := []struct {
		name           string
		answer         error
		lost           error
		withoutRelease bool
		code           int
		stderr         string
	}{
		{"a command that succeeds", nil, lost, false, gonsole.ExitFailed, released},
		{"a command that fails", errors.New("the report store vanished"), lost, false, gonsole.ExitFailed,
			"myapp: the report store vanished\n" + released},
		{"a command misused", gonsole.Misuse(errors.New("report:plugins wants -since")), lost, false,
			gonsole.ExitMisused, "myapp: report:plugins wants -since\n" + released + "\n" + pluginsPage},
		{"a command that answers with help", fmt.Errorf("asked for the page: %w", flag.ErrHelp), lost, false,
			gonsole.ExitFailed, released},
		{"a command that fails before a release that succeeds", errors.New("the report store vanished"), nil,
			false, gonsole.ExitFailed, "myapp: the report store vanished\n"},
		{"a command that succeeds without a release", nil, nil, true, gonsole.ExitDone, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			p := plugged(&registry{lost: tc.lost, withoutRelease: tc.withoutRelease})
			p.Commands[1].Run = func(ctx context.Context, call gonsole.Call) error {
				_, _ = call.Plugins(ctx)
				return tc.answer
			}

			got := execute(t, p, "report:plugins")

			if got.code != tc.code || got.stderr != tc.stderr {
				t.Errorf("code %d, stderr = %q, want %d and %q", got.code, got.stderr, tc.code, tc.stderr)
			}
		})
	}
}

func TestRunTurnsAPanicOfThePluginsIntoAFailure(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name    string
		plugins func(context.Context, gonsole.Call) (gonsole.Loaded, error)
		wraps   string
		line    string
	}{
		{"a registration that panics", func(context.Context, gonsole.Call) (gonsole.Loaded, error) {
			panic("the plugin table vanished")
		}, "", "myapp: plugins: panic: the plugin table vanished\n"},
		{"a registration that panics under a command that wraps it", func(context.Context, gonsole.Call) (
			gonsole.Loaded, error) {
			panic("the plugin table vanished")
		}, "load the plugins", "myapp: load the plugins: plugins: panic: the plugin table vanished\n"},
		{"a release that panics", func(context.Context, gonsole.Call) (gonsole.Loaded, error) {
			return gonsole.Loaded{Release: func(context.Context) error { panic("the pool vanished") }}, nil
		}, "", "myapp: plugins: panic: the pool vanished\n"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			p := plugged(&registry{})
			p.Plugins = tc.plugins
			if tc.wraps != "" {
				p.Commands[1].Run = func(ctx context.Context, call gonsole.Call) error {
					_, err := call.Plugins(ctx)
					return fmt.Errorf("%s: %w", tc.wraps, err)
				}
			}

			got := execute(t, p, "report:plugins")

			if got.code != gonsole.ExitFailed {
				t.Errorf("code = %d, want %d", got.code, gonsole.ExitFailed)
			}
			stack, opened := strings.CutPrefix(got.stderr, tc.line)
			if !opened {
				t.Errorf("stderr opens with %q, want %q", firstLine(got.stderr), tc.line)
			}
			if !strings.HasPrefix(stack, "goroutine ") {
				t.Errorf("after the panic line = %q, want the raw stack", firstLine(stack))
			}
		})
	}
}

func TestRunPrintsTheStackOfEveryPanic(t *testing.T) {
	t.Parallel()

	p := plugged(&registry{})
	p.Plugins = func(context.Context, gonsole.Call) (gonsole.Loaded, error) {
		return gonsole.Loaded{Release: func(context.Context) error { panic("the pool vanished") }}, nil
	}
	p.Commands[1].Run = func(ctx context.Context, call gonsole.Call) error {
		_, _ = call.Plugins(ctx)
		panic("the report store vanished")
	}

	got := execute(t, p, "report:plugins")

	const lines = "myapp: report:plugins: panic: the report store vanished\nmyapp: plugins: panic: the pool vanished\n"
	stacks, opened := strings.CutPrefix(got.stderr, lines)
	if got.code != gonsole.ExitFailed || !opened {
		t.Fatalf("code %d, stderr opens with %q, want %d and %q", got.code, firstLine(got.stderr), gonsole.ExitFailed,
			lines)
	}
	run, release, split := strings.Cut(stacks, "\ngoroutine ")
	if !split || !strings.Contains(run, "(*runner).invoke") || !strings.Contains(release, "(*memo).release") {
		t.Errorf("stacks = %q, want the stack of the run and then the stack of the release", stacks)
	}
}

func TestRunWaitsForARegistrationStillRunningBeforeItReleases(t *testing.T) {
	t.Parallel()

	entered, proceed := make(chan struct{}), make(chan struct{})
	released := false
	p := plugged(&registry{})
	p.Plugins = func(context.Context, gonsole.Call) (gonsole.Loaded, error) {
		close(entered)
		<-proceed
		return gonsole.Loaded{Release: func(context.Context) error {
			released = true
			return nil
		}}, nil
	}
	p.Commands[1].Run = func(ctx context.Context, call gonsole.Call) error {
		go func() { _, _ = call.Plugins(ctx) }()
		<-entered
		time.AfterFunc(50*time.Millisecond, func() { close(proceed) })
		return nil
	}

	got := execute(t, p, "report:plugins")

	if got.code != gonsole.ExitDone || !released {
		t.Errorf("code %d, released %t, want 0 and the release the registration answered", got.code, released)
	}
}

func TestPluginsRegisterUnderTheContextOfTheFirstCaller(t *testing.T) {
	t.Parallel()

	type key struct{}
	var seen string
	p := plugged(&registry{})
	p.Plugins = func(ctx context.Context, _ gonsole.Call) (gonsole.Loaded, error) {
		seen = fmt.Sprintf("%v %v", ctx.Value(key{}), ctx.Err())
		return gonsole.Loaded{}, nil
	}
	p.Commands[1].Run = func(ctx context.Context, call gonsole.Call) error {
		caller, cancel := context.WithCancel(context.WithValue(ctx, key{}, "caller"))
		cancel()
		_, err := call.Plugins(caller)
		return err
	}

	got := execute(t, p, "report:plugins")

	if want := "caller context canceled"; got.code != gonsole.ExitDone || seen != want {
		t.Errorf("code %d, registration saw %q, want 0 and %q, stderr %q", got.code, seen, want, got.stderr)
	}
}

func TestRegistrationReadsTheSettingsOfAProgramWithoutAReader(t *testing.T) {
	t.Parallel()

	owner := "unread"
	p := plugged(&registry{})
	p.Env = gonsole.Env{Prefix: "MYAPP_"}
	p.Plugins = func(_ context.Context, call gonsole.Call) (gonsole.Loaded, error) {
		owner = call.Env.Getenv(call.Env.Key("OWNER"))
		return gonsole.Loaded{}, nil
	}

	got := execute(t, p, "report:plugins")

	if got.code != gonsole.ExitDone || owner != "" {
		t.Errorf("code %d, owner %q, want 0 and empty, stderr %q", got.code, owner, got.stderr)
	}
}
