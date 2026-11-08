// SPDX-License-Identifier: Apache-2.0

package features_test

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/cucumber/godog"

	"github.com/gopherium/framework/pluginkit"
	"github.com/gopherium/framework/pluginkit/wire"
)

// registrationConfig is the wiring a registration scenario starts from.
var registrationConfig = wire.Config{
	SDKImport:    "example.com/app/sdk",
	FrontendSDK:  "@example/frontend-sdk",
	GoWiringPath: "cmd/app/plugins_gen.go",
	TSWiringPath: "web/plugins.ts",
	License:      "Apache-2.0",
}

// listedID finds the id of each entry of a generated registration list.
var listedID = regexp.MustCompile(`\{ID: ("(?:[^"\\]|\\.)*"), Register:`)

// laneDeps is what a scenario lends a plugin when it registers.
type laneDeps struct{ id string }

// lanePlugin is a plugin a scenario registered.
type lanePlugin struct{ id string }

// ID returns the id the plugin calls itself.
func (p *lanePlugin) ID() string {
	return p.id
}

// registrationFailure is one failure of a registration outcome, its error as text.
type registrationFailure struct {
	ID       string
	Step     string
	Panicked bool
	Err      string
}

// registrationWorld is what one scenario of the registration feature builds and finds.
type registrationWorld struct {
	t          *testing.T
	root       string
	manifests  map[string]map[string]any
	cfg        wire.Config
	err        error
	compiled   []string
	leftOut    map[string]bool
	answers    map[string]func() error
	calledAs   map[string]string
	depsPanic  map[string]string
	built      []string
	received   map[string]laneDeps
	registered []string
	failed     []registrationFailure
	escaped    any
	ownPlugin  bool
	sources    map[string]string
}

// backend gives the plugin id a manifest with a backend.
func (w *registrationWorld) backend(id string) {
	w.manifests[id] = map[string]any{"id": id, "name": id, "backend": "example.com/app/plugins/" + id}
}

// onlyFrontend gives the plugin id a manifest with only a frontend.
func (w *registrationWorld) onlyFrontend(id, frontend string) {
	w.manifests[id] = map[string]any{"id": id, "name": id, "frontend": frontend}
}

// asksForList switches the registration list on.
func (w *registrationWorld) asksForList() {
	w.cfg.RegistrationList = true
}

// asksForListAndRegistry switches the registration list and the registry on.
func (w *registrationWorld) asksForListAndRegistry() {
	w.cfg.RegistrationList = true
	w.cfg.GoRegistryPath = "registry/registry_gen.go"
	w.cfg.GoRegistryPackage = "registry"
}

// writeFile writes text to the slash path under the scenario's root.
func (w *registrationWorld) writeFile(path, text string) error {
	full := filepath.Join(w.root, filepath.FromSlash(path))
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		return err
	}
	return os.WriteFile(full, []byte(text), 0o644)
}

// readFile returns the slash path under the scenario's root.
func (w *registrationWorld) readFile(path string) (string, error) {
	data, err := os.ReadFile(filepath.Join(w.root, filepath.FromSlash(path)))
	return string(data), err
}

// addManifests adds every manifest of the scenario to its source files.
func (w *registrationWorld) addManifests() error {
	for id, m := range w.manifests {
		data, err := json.Marshal(m)
		if err != nil {
			return err
		}
		w.sources["plugins/"+id+"/plugin.json"] = string(data)
	}
	return nil
}

// makeWiringFolders creates the folder of every file the wiring writes.
func (w *registrationWorld) makeWiringFolders() error {
	var err error
	for _, path := range []string{w.cfg.GoWiringPath, w.cfg.TSWiringPath, w.cfg.GoRegistryPath} {
		err = errors.Join(err, os.MkdirAll(filepath.Join(w.root, filepath.Dir(filepath.FromSlash(path))), 0o755))
	}
	return err
}

// writeTree writes every manifest and source file of the scenario under a new root, and the folders the wiring needs.
func (w *registrationWorld) writeTree() error {
	w.root = w.t.TempDir()
	if err := w.addManifests(); err != nil {
		return err
	}
	var err error
	for path, text := range w.sources {
		err = errors.Join(err, w.writeFile(path, text))
	}
	return errors.Join(err, w.makeWiringFolders())
}

// wirePlugins runs the generator over the manifests the scenario wrote.
func (w *registrationWorld) wirePlugins() error {
	if err := w.writeTree(); err != nil {
		return err
	}
	w.err = wire.Run(w.root, w.cfg)
	return nil
}

// wiredFile returns the generated file at path, failing when the run failed.
func (w *registrationWorld) wiredFile(path string) (string, error) {
	if w.err != nil {
		return "", fmt.Errorf("the run failed: %w", w.err)
	}
	return w.readFile(path)
}

// listedIDs returns the ids the registration list in the file at path names, in order.
func (w *registrationWorld) listedIDs(path string) ([]string, error) {
	text, err := w.wiredFile(path)
	if err != nil {
		return nil, err
	}
	ids := []string{}
	for _, match := range listedID.FindAllStringSubmatch(text, -1) {
		var id string
		if err := json.Unmarshal([]byte(match[1]), &id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, nil
}

// listNames checks that the main registration list names the plugins of list, in order.
func (w *registrationWorld) listNames(list string) error {
	ids, err := w.listedIDs(w.cfg.GoWiringPath)
	if err != nil {
		return err
	}
	if want := names(list); !slices.Equal(ids, want) {
		return fmt.Errorf("the registration list names %q, want %q", ids, want)
	}
	return nil
}

// registryNames checks that the registry's list names only the plugin id.
func (w *registrationWorld) registryNames(id string) error {
	ids, err := w.listedIDs(w.cfg.GoRegistryPath)
	if err != nil {
		return err
	}
	if !slices.Equal(ids, []string{id}) {
		return fmt.Errorf("the registry's list names %q, want only %q", ids, id)
	}
	return nil
}

// holds checks whether the generated file at path holds the text part, as wanted.
func (w *registrationWorld) holds(path, part string, want bool) error {
	text, err := w.wiredFile(path)
	if err != nil {
		return err
	}
	if strings.Contains(text, part) != want {
		return fmt.Errorf("%s holding %q is %t, want %t", path, part, !want, want)
	}
	return nil
}

// noRegisterPlugins checks that the Go wiring declares no registerPlugins function.
func (w *registrationWorld) noRegisterPlugins() error {
	return w.holds(w.cfg.GoWiringPath, "func registerPlugins(", false)
}

// keepsRegisterPlugins checks that the Go wiring declares the registerPlugins function.
func (w *registrationWorld) keepsRegisterPlugins() error {
	return w.holds(w.cfg.GoWiringPath, "func registerPlugins(", true)
}

// noList checks that the Go wiring holds no registration list.
func (w *registrationWorld) noList() error {
	return w.holds(w.cfg.GoWiringPath, "pluginRegistrations", false)
}

// noAll checks that the registry declares no All function.
func (w *registrationWorld) noAll() error {
	return w.holds(w.cfg.GoRegistryPath, "func All(", false)
}

// compile lists the plugins of list as compiled, each registering unless told otherwise.
func (w *registrationWorld) compile(list string) {
	w.compiled = names(list)
}

// noneCompiled lists no compiled plugin.
func (w *registrationWorld) noneCompiled() {
	w.compiled = []string{}
}

// leaveOut keeps the plugin id out of what the application registers.
func (w *registrationWorld) leaveOut(id string) {
	w.leftOut[id] = true
}

// registerPanics makes the Register of id panic with value.
func (w *registrationWorld) registerPanics(id, value string) {
	w.answers[id] = func() error { panic(value) }
}

// registerFails makes the Register of id answer an error with text.
func (w *registrationWorld) registerFails(id, text string) {
	w.answers[id] = func() error { return errors.New(text) }
}

// registerCallsItself makes the Register of id answer a plugin whose ID is name.
func (w *registrationWorld) registerCallsItself(id, name string) {
	w.calledAs[id] = name
}

// depsPanics makes building the dependencies of id panic with value.
func (w *registrationWorld) depsPanics(id, value string) {
	w.depsPanic[id] = value
}

// entries returns the registration of each compiled plugin the application keeps, in order.
func (w *registrationWorld) entries() []pluginkit.Registration[laneDeps, *lanePlugin] {
	var kept []pluginkit.Registration[laneDeps, *lanePlugin]
	for _, id := range w.compiled {
		if w.leftOut[id] {
			continue
		}
		kept = append(kept, pluginkit.Registration[laneDeps, *lanePlugin]{ID: id, Register: w.registerOf(id)})
	}
	return kept
}

// registerOf returns the Register of the plugin id, noting the deps it receives.
func (w *registrationWorld) registerOf(id string) func(laneDeps) (*lanePlugin, error) {
	return func(deps laneDeps) (*lanePlugin, error) {
		w.received[id] = deps
		if answer, ok := w.answers[id]; ok {
			if err := answer(); err != nil {
				return nil, err
			}
		}
		if name, ok := w.calledAs[id]; ok {
			return &lanePlugin{id: name}, nil
		}
		return &lanePlugin{id: id}, nil
	}
}

// depsFor builds the deps of id, noting the call, and panics when the scenario says so.
func (w *registrationWorld) depsFor(id string) laneDeps {
	w.built = append(w.built, id)
	if value, ok := w.depsPanic[id]; ok {
		panic(value)
	}
	return laneDeps{id: id}
}

// registerEach registers the plugins the application keeps, noting the outcome or the panic that escaped.
func (w *registrationWorld) registerEach() {
	defer func() { w.escaped = recover() }()
	outcome := pluginkit.RegisterEach(w.entries(), w.depsFor)
	for _, plugin := range outcome.Registered {
		w.registered = append(w.registered, plugin.id)
	}
	for _, f := range outcome.Failed {
		w.failed = append(w.failed, registrationFailure{f.ID, string(f.Step), f.Panicked, f.Err.Error()})
	}
}

// settled returns the panic that escaped the registering, if one did.
func (w *registrationWorld) settled() error {
	if w.escaped != nil {
		return fmt.Errorf("the registering panicked: %v", w.escaped)
	}
	return nil
}

// areRegistered checks that exactly the plugins of list registered, in order.
func (w *registrationWorld) areRegistered(list string) error {
	if err := w.settled(); err != nil {
		return err
	}
	if want := names(list); !slices.Equal(w.registered, want) {
		return fmt.Errorf("registered %q, want %q", w.registered, want)
	}
	return nil
}

// neverRan checks that the Register of id was not called.
func (w *registrationWorld) neverRan(id string) error {
	if _, ran := w.received[id]; ran {
		return fmt.Errorf("the Register of %s ran", id)
	}
	return nil
}

// receivedDeps checks that the Register of id received the deps built for owner.
func (w *registrationWorld) receivedDeps(id, owner string) error {
	if got := w.received[id]; got != (laneDeps{id: owner}) {
		return fmt.Errorf("the Register of %s received %+v, want the deps of %s", id, got, owner)
	}
	return nil
}

// builtOnce checks that deps were built for the plugins of list, in order, once each.
func (w *registrationWorld) builtOnce(list string) error {
	if want := names(list); !slices.Equal(w.built, want) {
		return fmt.Errorf("deps were built for %q, want %q", w.built, want)
	}
	return nil
}

// failure returns the failure of id.
func (w *registrationWorld) failure(id string) (registrationFailure, error) {
	if err := w.settled(); err != nil {
		return registrationFailure{}, err
	}
	i := slices.IndexFunc(w.failed, func(f registrationFailure) bool { return f.ID == id })
	if i < 0 {
		return registrationFailure{}, fmt.Errorf("no failure of %s among %+v", id, w.failed)
	}
	return w.failed[i], nil
}

// failedAt checks that id failed at step with the text.
func (w *registrationWorld) failedAt(id, step, text string) error {
	f, err := w.failure(id)
	if err != nil {
		return err
	}
	if f.Step != step || f.Err != text {
		return fmt.Errorf("%s failed at %q with %q, want %q with %q", id, f.Step, f.Err, step, text)
	}
	return nil
}

// markedPanic checks whether the failure of id is marked as a panic, as the step says.
func (w *registrationWorld) markedPanic(id, not string) error {
	f, err := w.failure(id)
	if err != nil {
		return err
	}
	if want := not == ""; f.Panicked != want {
		return fmt.Errorf("the failure of %s is marked as a panic %t, want %t", id, f.Panicked, want)
	}
	return nil
}

// noneAtAll checks that nothing registered and nothing failed.
func (w *registrationWorld) noneAtAll() error {
	if err := w.settled(); err != nil {
		return err
	}
	if len(w.registered)+len(w.failed) > 0 {
		return fmt.Errorf("registered %q and failed %+v, want neither", w.registered, w.failed)
	}
	return nil
}

// panicked checks that the registering panicked with value.
func (w *registrationWorld) panicked(value string) error {
	if !reflect.DeepEqual(w.escaped, value) {
		return fmt.Errorf("the registering panicked with %v, want %q", w.escaped, value)
	}
	return nil
}

// givenRegistration registers the steps that write manifests and plugins and ask for the list.
func givenRegistration(sc *godog.ScenarioContext, w *registrationWorld) {
	sc.Step(`^the plugin "([^"]*)" has a backend$`, w.backend)
	sc.Step(`^the plugin "([^"]*)" has only the frontend "([^"]*)"$`, w.onlyFrontend)
	sc.Step(`^the application asks for a registration list$`, w.asksForList)
	sc.Step(`^the application asks for a registration list and a registry$`, w.asksForListAndRegistry)
	sc.Step(`^the compiled plugins (.+)$`, w.compile)
	sc.Step(`^no compiled plugin$`, w.noneCompiled)
	sc.Step(`^the application leaves "([^"]*)" out$`, w.leaveOut)
	sc.Step(`^the Register of "([^"]*)" panics with "([^"]*)"$`, w.registerPanics)
	sc.Step(`^the Register of "([^"]*)" answers the error "([^"]*)"$`, w.registerFails)
	sc.Step(`^the Register of "([^"]*)" answers a plugin that calls itself "([^"]*)"$`, w.registerCallsItself)
	sc.Step(`^building the dependencies of "([^"]*)" panics with "([^"]*)"$`, w.depsPanics)
	givenBuild(sc, w)
}

// whenRegistration registers the steps that wire, register, or build and run the plugins.
func whenRegistration(sc *godog.ScenarioContext, w *registrationWorld) {
	sc.Step(`^the plugins are wired$`, w.wirePlugins)
	sc.Step(`^the application registers each plugin$`, w.registerEach)
	sc.Step(`^the plugins are wired, built and registered one by one$`, w.buildAndRun)
}

// thenRegistration registers the steps that check the generated files and the outcome.
func thenRegistration(sc *godog.ScenarioContext, w *registrationWorld) {
	sc.Step(`^the registration list names (.+), in that order$`, w.listNames)
	sc.Step(`^the Go wiring holds no registerPlugins function$`, w.noRegisterPlugins)
	sc.Step(`^the registry's Registrations names "([^"]*)"$`, w.registryNames)
	sc.Step(`^the registry holds no All function$`, w.noAll)
	sc.Step(`^the Go wiring holds the registerPlugins function$`, w.keepsRegisterPlugins)
	sc.Step(`^the Go wiring holds no registration list$`, w.noList)
	sc.Step(`^((?:"[^"]*"(?:, | and )?)+) (?:is|are) registered(?:, in that order)?$`, w.areRegistered)
	sc.Step(`^the Register of "([^"]*)" never ran$`, w.neverRan)
	sc.Step(`^the Register of "([^"]*)" received the dependencies built for "([^"]*)"$`, w.receivedDeps)
	sc.Step(`^dependencies were built for (.+), in that order, once each$`, w.builtOnce)
	sc.Step(`^"([^"]*)" failed at the step "([^"]*)" with: (.+)$`, w.failedAt)
	sc.Step(`^the failure of "([^"]*)" is (not )?marked as a panic$`, w.markedPanic)
	sc.Step(`^no plugin is registered and none failed$`, w.noneAtAll)
	sc.Step(`^the registering panics with "([^"]*)"$`, w.panicked)
}

// initializeRegistration gives each scenario of the registration feature a world of its own and its steps.
func initializeRegistration(t *testing.T) func(*godog.ScenarioContext) {
	return func(sc *godog.ScenarioContext) {
		w := &registrationWorld{
			t: t, manifests: map[string]map[string]any{}, cfg: registrationConfig, leftOut: map[string]bool{},
			answers: map[string]func() error{}, depsPanic: map[string]string{}, received: map[string]laneDeps{},
			sources: map[string]string{}, calledAs: map[string]string{},
		}
		givenRegistration(sc, w)
		whenRegistration(sc, w)
		thenRegistration(sc, w)
	}
}

// sdkWithAlias is an SDK whose Plugin is pluginkit's own.
const sdkWithAlias = `package sdk

import "github.com/gopherium/framework/pluginkit"

type Deps struct{ ID string }

type Plugin = pluginkit.Plugin
`

// sdkWithOwnPlugin is an SDK that declares its own Plugin.
const sdkWithOwnPlugin = `package sdk

import "context"

type Deps struct{ ID string }

type Plugin interface {
	ID() string
	Start(ctx context.Context) error
	Stop(ctx context.Context) error
}
`

// workingPlugin is a plugin package whose Register answers a plugin named by the deps it received.
const workingPlugin = `package %s

import (
	"context"

	"example.com/app/sdk"
)

type Plugin struct{ deps sdk.Deps }

func Register(deps sdk.Deps) (*Plugin, error) {
	return &Plugin{deps: deps}, nil
}

func (p *Plugin) ID() string { return p.deps.ID }

func (p *Plugin) Start(context.Context) error { return nil }

func (p *Plugin) Stop(context.Context) error { return nil }
`

// failingPlugin is a plugin package whose Register answers an error.
const failingPlugin = `package %s

import (
	"context"
	"errors"

	"example.com/app/sdk"
)

type Plugin struct{}

func Register(sdk.Deps) (*Plugin, error) {
	return nil, errors.New(%q)
}

func (p *Plugin) ID() string { return %q }

func (p *Plugin) Start(context.Context) error { return nil }

func (p *Plugin) Stop(context.Context) error { return nil }
`

// reportingMain registers each plugin of the generated list and prints the outcome as JSON.
const reportingMain = `package main

import (
	"encoding/json"
	"os"

	"github.com/gopherium/framework/pluginkit"

	"example.com/app/sdk"
)

type failure struct {
	ID       string
	Step     string
	Panicked bool
	Err      string
}

func main() {
	outcome := pluginkit.RegisterEach(pluginRegistrations, func(id string) sdk.Deps { return sdk.Deps{ID: id} })
	registered := []string{}
	for _, plugin := range outcome.Registered {
		registered = append(registered, plugin.ID())
	}
	failed := []failure{}
	for _, f := range outcome.Failed {
		failed = append(failed, failure{f.ID, string(f.Step), f.Panicked, f.Err.Error()})
	}
	_ = json.NewEncoder(os.Stdout).Encode(map[string]any{"registered": registered, "failed": failed})
}
`

// sdkPlugin chooses the SDK the build scenario compiles against.
func (w *registrationWorld) sdkPlugin(kind string) {
	w.ownPlugin = kind == "an interface of its own"
}

// buildWorking gives the plugin id a backend whose Register answers a plugin.
func (w *registrationWorld) buildWorking(id string) {
	w.backend(id)
	w.sources["plugins/"+id+"/"+id+".go"] = fmt.Sprintf(workingPlugin, id)
}

// buildFailing gives the plugin id a backend whose Register answers an error with text.
func (w *registrationWorld) buildFailing(id, text string) {
	w.backend(id)
	w.sources["plugins/"+id+"/"+id+".go"] = fmt.Sprintf(failingPlugin, id, text, id)
}

// givenBuild registers the steps that write an application module to build.
func givenBuild(sc *godog.ScenarioContext, w *registrationWorld) {
	sc.Step(`^an SDK whose Plugin is (an alias of the pluginkit Plugin|an interface of its own)$`, w.sdkPlugin)
	sc.Step(`^the plugin "([^"]*)" has a backend whose Register answers a plugin$`, w.buildWorking)
	sc.Step(`^the plugin "([^"]*)" has a backend whose Register answers the error "([^"]*)"$`, w.buildFailing)
}

// moduleFile returns the go.mod of the application module, its pluginkit replaced by this checkout's.
func moduleFile() (string, error) {
	pluginkitDir, err := filepath.Abs("..")
	if err != nil {
		return "", err
	}
	own, err := os.ReadFile(filepath.Join(pluginkitDir, "go.mod"))
	if err != nil {
		return "", err
	}
	version := regexp.MustCompile(`(?m)^go (\S+)$`).FindSubmatch(own)
	if version == nil {
		return "", errors.New("pluginkit's go.mod names no Go version")
	}
	return fmt.Sprintf("module example.com/app\n\ngo %s\n\nrequire github.com/gopherium/framework/pluginkit v0.0.0\n\n"+
		"replace github.com/gopherium/framework/pluginkit => %s\n", version[1], pluginkitDir), nil
}

// buildAndRun writes the application module, wires it with the list on, then builds and runs it.
func (w *registrationWorld) buildAndRun() error {
	mod, err := moduleFile()
	if err != nil {
		return err
	}
	w.sources["go.mod"] = mod
	w.sources["sdk/sdk.go"] = sdkWithAlias
	if w.ownPlugin {
		w.sources["sdk/sdk.go"] = sdkWithOwnPlugin
	}
	w.sources["cmd/app/main.go"] = reportingMain
	if err := w.wirePlugins(); err != nil || w.err != nil {
		return errors.Join(err, w.err)
	}
	return w.run()
}

// run builds and runs the application, reading the outcome it prints.
func (w *registrationWorld) run() error {
	cmd := exec.Command("go", "run", "./cmd/app")
	cmd.Dir = w.root
	cmd.Env = append(os.Environ(), "GOWORK=off", "GOFLAGS=-mod=mod", "GOPROXY=off", "GOTOOLCHAIN=local")
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		return fmt.Errorf("go run: %w: %s", err, stderr.String())
	}
	var outcome struct {
		Registered []string
		Failed     []registrationFailure
	}
	if err := json.Unmarshal(out, &outcome); err != nil {
		return err
	}
	w.registered, w.failed = outcome.Registered, outcome.Failed
	return nil
}
