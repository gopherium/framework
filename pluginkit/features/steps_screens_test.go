// SPDX-License-Identifier: Apache-2.0

package features_test

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/cucumber/godog"

	"github.com/gopherium/framework/pluginkit/wire"
)

// earlierWiring is what the wiring files hold before a scenario wires its plugins.
const earlierWiring = "earlier wiring\n"

// screensConfig is the wiring a scenario starts from, before it asks for more.
var screensConfig = wire.Config{
	SDKImport:    "example.com/app/sdk",
	FrontendSDK:  "@example/frontend-sdk",
	GoWiringPath: "plugins_gen.go",
	TSWiringPath: "plugins.ts",
	License:      "Apache-2.0",
}

// screenImport finds one plugin id and its screens module in the lazy screens file.
var screenImport = regexp.MustCompile(`(?m)^\t("(?:[^"\\]|\\.)*"): \(\) => import\(("(?:[^"\\]|\\.)*")\),$`)

// screensWorld is what one scenario of the screens feature builds and finds.
type screensWorld struct {
	t         *testing.T
	root      string
	manifests map[string]map[string]any
	cfg       wire.Config
	read      []wire.Manifest
	err       error
}

// plugin returns the manifest of id, starting it with its id and name.
func (w *screensWorld) plugin(id string) map[string]any {
	m, ok := w.manifests[id]
	if !ok {
		m = map[string]any{"id": id, "name": id}
		w.manifests[id] = m
	}
	return m
}

// declare gives the plugin id a backend when asked, and the frontend and screens module it names.
func (w *screensWorld) declare(id string, backend bool, frontend, screens string) {
	m := w.plugin(id)
	if backend {
		m["backend"] = "example.com/plugins/" + id
	}
	if frontend != "" {
		m["frontend"] = frontend
	}
	if screens != "" {
		m["screens"] = screens
	}
}

// hasBackend gives the plugin id a backend.
func (w *screensWorld) hasBackend(id string) {
	w.declare(id, true, "", "")
}

// hasBackendAndScreens gives the plugin id a backend and a screens module.
func (w *screensWorld) hasBackendAndScreens(id, screens string) {
	w.declare(id, true, "", screens)
}

// hasFrontend gives the plugin id a frontend.
func (w *screensWorld) hasFrontend(id, frontend string) {
	w.declare(id, false, frontend, "")
}

// hasFrontendAndScreens gives the plugin id a frontend and a screens module.
func (w *screensWorld) hasFrontendAndScreens(id, frontend, screens string) {
	w.declare(id, false, frontend, screens)
}

// hasOnlyScreens gives the plugin id a screens module and nothing else.
func (w *screensWorld) hasOnlyScreens(id, screens string) {
	w.declare(id, false, "", screens)
}

// hasMenuEntry adds the menu entry written as JSON to the plugin id.
func (w *screensWorld) hasMenuEntry(id, entry string) {
	m := w.plugin(id)
	menu, _ := m["menu"].([]json.RawMessage)
	m["menu"] = append(menu, json.RawMessage(entry))
}

// hasScreenCapabilities gives the plugin id the screen capabilities written as JSON.
func (w *screensWorld) hasScreenCapabilities(id, capabilities string) {
	w.plugin(id)["screen_capabilities"] = json.RawMessage(capabilities)
}

// namesCapability names the capability a screen of the plugin id needs.
func (w *screensWorld) namesCapability(id, capability, screen string) {
	m := w.plugin(id)
	capabilities, ok := m["screen_capabilities"].(map[string]string)
	if !ok {
		capabilities = map[string]string{}
		m["screen_capabilities"] = capabilities
	}
	capabilities[screen] = capability
}

// asksForScreens switches the lazy screens file on.
func (w *screensWorld) asksForScreens() {
	w.cfg.TSScreensPath = "screens.ts"
}

// asksForIndex switches the manifest index on.
func (w *screensWorld) asksForIndex() {
	w.cfg.ManifestIndexPath = "index.json"
}

// keepsPackageJSON switches the package.json on, starting from one that names no plugin.
func (w *screensWorld) keepsPackageJSON() {
	w.cfg.PackageJSONPath = "package.json"
}

// writeFile writes data to the slash path under the scenario's root.
func (w *screensWorld) writeFile(path string, data []byte) error {
	full := filepath.Join(w.root, filepath.FromSlash(path))
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		return err
	}
	return os.WriteFile(full, data, 0o644)
}

// readFile returns the slash path under the scenario's root.
func (w *screensWorld) readFile(path string) (string, error) {
	data, err := os.ReadFile(filepath.Join(w.root, filepath.FromSlash(path)))
	return string(data), err
}

// writeTree writes every manifest, the earlier wiring and any package.json under a new root.
func (w *screensWorld) writeTree() error {
	w.root = w.t.TempDir()
	for id, m := range w.manifests {
		data, err := json.Marshal(m)
		if err != nil {
			return err
		}
		if err := w.writeFile("plugins/"+id+"/plugin.json", data); err != nil {
			return err
		}
	}
	for _, path := range []string{w.cfg.GoWiringPath, w.cfg.TSWiringPath} {
		if err := w.writeFile(path, []byte(earlierWiring)); err != nil {
			return err
		}
	}
	if w.cfg.PackageJSONPath == "" {
		return nil
	}
	return w.writeFile(w.cfg.PackageJSONPath, []byte(`{"name": "@example/admin"}`))
}

// readManifests reads every manifest the scenario wrote.
func (w *screensWorld) readManifests() error {
	if err := w.writeTree(); err != nil {
		return err
	}
	w.read, w.err = wire.Load(w.root, []string{"plugins"}, nil)
	return nil
}

// wirePlugins runs the generator over the manifests the scenario wrote.
func (w *screensWorld) wirePlugins() error {
	if err := w.writeTree(); err != nil {
		return err
	}
	w.err = wire.Run(w.root, w.cfg)
	return nil
}

// wired returns the error that stopped the run, if one did.
func (w *screensWorld) wired() error {
	if w.err != nil {
		return fmt.Errorf("the run failed: %w", w.err)
	}
	return nil
}

// manifestOf returns the manifest of id as the scenario read it.
func (w *screensWorld) manifestOf(id string) (wire.Manifest, error) {
	if err := w.wired(); err != nil {
		return wire.Manifest{}, err
	}
	i := slices.IndexFunc(w.read, func(m wire.Manifest) bool { return m.ID == id })
	if i < 0 {
		return wire.Manifest{}, fmt.Errorf("no manifest of %s was read", id)
	}
	return w.read[i], nil
}

// readMenuEntry checks that the manifest of id holds the menu entry written as JSON.
func (w *screensWorld) readMenuEntry(id, entry string) error {
	m, err := w.manifestOf(id)
	if err != nil {
		return err
	}
	var want wire.MenuEntry
	if err := json.Unmarshal([]byte(entry), &want); err != nil {
		return err
	}
	if !slices.ContainsFunc(m.Menu, func(got wire.MenuEntry) bool { return reflect.DeepEqual(got, want) }) {
		return fmt.Errorf("the menu of %s is %+v, want it to hold %+v", id, m.Menu, want)
	}
	return nil
}

// readScreens checks that the manifest of id names the screens module.
func (w *screensWorld) readScreens(id, screens string) error {
	m, err := w.manifestOf(id)
	if err != nil {
		return err
	}
	if m.Screens != screens {
		return fmt.Errorf("the screens module of %s is %q, want %q", id, m.Screens, screens)
	}
	return nil
}

// readCapability checks that the manifest of id names the capability of the screen.
func (w *screensWorld) readCapability(id, capability, screen string) error {
	m, err := w.manifestOf(id)
	if err != nil {
		return err
	}
	if got := m.ScreenCapabilities[screen]; got != capability {
		return fmt.Errorf("the screen %s of %s needs %q, want %q", screen, id, got, capability)
	}
	return nil
}

// readNoExtra checks that the manifest of id keeps every key in a typed field.
func (w *screensWorld) readNoExtra(id string) error {
	m, err := w.manifestOf(id)
	if err != nil {
		return err
	}
	if len(m.Extra) > 0 {
		return fmt.Errorf("the manifest of %s keeps %d extra keys, want none", id, len(m.Extra))
	}
	return nil
}

// screenImports returns every plugin id of the lazy screens file with its screens module, in order.
func (w *screensWorld) screenImports() ([][2]string, error) {
	if err := w.wired(); err != nil {
		return nil, err
	}
	text, err := w.readFile(w.cfg.TSScreensPath)
	if err != nil {
		return nil, err
	}
	var imports [][2]string
	for _, match := range screenImport.FindAllStringSubmatch(text, -1) {
		var entry [2]string
		err := errors.Join(json.Unmarshal([]byte(match[1]), &entry[0]), json.Unmarshal([]byte(match[2]), &entry[1]))
		if err != nil {
			return nil, err
		}
		imports = append(imports, entry)
	}
	return imports, nil
}

// mapsInOrder checks that the lazy screens file imports the module of each plugin of the table, in its order.
func (w *screensWorld) mapsInOrder(table *godog.Table) error {
	got, err := w.screenImports()
	if err != nil {
		return err
	}
	var want [][2]string
	for _, row := range table.Rows[1:] {
		want = append(want, [2]string{row.Cells[0].Value, row.Cells[1].Value})
	}
	if !slices.Equal(got, want) {
		return fmt.Errorf("the lazy screens file maps %q, want %q", got, want)
	}
	return nil
}

// mapsNoPlugin checks that the lazy screens file is an empty map.
func (w *screensWorld) mapsNoPlugin() error {
	got, err := w.screenImports()
	if err != nil {
		return err
	}
	text, err := w.readFile(w.cfg.TSScreensPath)
	if err != nil {
		return err
	}
	if len(got) > 0 || !strings.HasSuffix(text, "= {}\n") {
		return fmt.Errorf("the lazy screens file is %q, want an empty map", text)
	}
	return nil
}

// importsFrontends checks that the TypeScript wiring imports both frontends.
func (w *screensWorld) importsFrontends(first, second string) error {
	if err := w.wired(); err != nil {
		return err
	}
	text, err := w.readFile(w.cfg.TSWiringPath)
	if err != nil {
		return err
	}
	for _, frontend := range []string{first, second} {
		if !strings.Contains(text, "from '"+frontend+"'\n") {
			return fmt.Errorf("the TypeScript wiring is %q, want it to import %s", text, frontend)
		}
	}
	return nil
}

// writesOnlyWiring checks that the run wrote the Go and TypeScript wiring and no other file.
func (w *screensWorld) writesOnlyWiring() error {
	if err := w.wired(); err != nil {
		return err
	}
	entries, err := os.ReadDir(w.root)
	if err != nil {
		return err
	}
	var files []string
	for _, entry := range entries {
		files = append(files, entry.Name())
	}
	if want := []string{"plugins", "plugins.ts", "plugins_gen.go"}; !slices.Equal(files, want) {
		return fmt.Errorf("the root holds %q, want only %q", files, want)
	}
	if kept, err := w.readFile(w.cfg.TSWiringPath); err != nil || kept == earlierWiring {
		return fmt.Errorf("the TypeScript wiring was not written: %v", err)
	}
	return nil
}

// refusedWith checks that the run failed with the text, the scenario's root left out of its paths.
func (w *screensWorld) refusedWith(text string) error {
	if w.err == nil {
		return errors.New("the run succeeded, want it refused")
	}
	if got := strings.ReplaceAll(w.err.Error(), w.root+string(filepath.Separator), ""); got != text {
		return fmt.Errorf("the run failed with %q, want %q", got, text)
	}
	return nil
}

// earlierWiringKept checks that both wiring files still hold the earlier wiring.
func (w *screensWorld) earlierWiringKept() error {
	for _, path := range []string{w.cfg.GoWiringPath, w.cfg.TSWiringPath} {
		kept, err := w.readFile(path)
		if err != nil || kept != earlierWiring {
			return fmt.Errorf("%s holds %q, want the earlier wiring: %v", path, kept, err)
		}
	}
	return nil
}

// dependsOnlyOn checks that the package.json depends on the one package as a workspace package.
func (w *screensWorld) dependsOnlyOn(name string) error {
	if err := w.wired(); err != nil {
		return err
	}
	text, err := w.readFile(w.cfg.PackageJSONPath)
	if err != nil {
		return err
	}
	var packageJSON struct{ Dependencies map[string]string }
	if err := json.Unmarshal([]byte(text), &packageJSON); err != nil {
		return err
	}
	if want := map[string]string{name: "workspace:*"}; !reflect.DeepEqual(packageJSON.Dependencies, want) {
		return fmt.Errorf("the package.json depends on %v, want %v", packageJSON.Dependencies, want)
	}
	return nil
}

// indexHolds checks that the manifest index holds the entry of id written as JSON.
func (w *screensWorld) indexHolds(id string, entry *godog.DocString) error {
	if err := w.wired(); err != nil {
		return err
	}
	text, err := w.readFile(w.cfg.ManifestIndexPath)
	if err != nil {
		return err
	}
	var index []map[string]any
	var want map[string]any
	if err := errors.Join(json.Unmarshal([]byte(text), &index), json.Unmarshal([]byte(entry.Content), &want)); err != nil {
		return err
	}
	i := slices.IndexFunc(index, func(m map[string]any) bool { return m["id"] == id })
	if i < 0 || !reflect.DeepEqual(index[i], want) {
		return fmt.Errorf("the manifest index is %s, want it to hold %s", text, entry.Content)
	}
	return nil
}

// givenScreens registers the steps that write manifests and ask for outputs.
func givenScreens(sc *godog.ScenarioContext, w *screensWorld) {
	sc.Step(`^the plugin "([^"]*)" has a backend$`, w.hasBackend)
	sc.Step(`^the plugin "([^"]*)" has a backend and the screens module "([^"]*)"$`, w.hasBackendAndScreens)
	sc.Step(`^the plugin "([^"]*)" has the frontend "([^"]*)"$`, w.hasFrontend)
	sc.Step(`^the plugin "([^"]*)" has the frontend "([^"]*)" and the screens module "([^"]*)"$`,
		w.hasFrontendAndScreens)
	sc.Step(`^the plugin "([^"]*)" has only the screens module "([^"]*)"$`, w.hasOnlyScreens)
	sc.Step(`^"([^"]*)" has the menu entry (\{.*\})$`, w.hasMenuEntry)
	sc.Step(`^"([^"]*)" has the screen capabilities (\{.*\})$`, w.hasScreenCapabilities)
	sc.Step(`^"([^"]*)" names "([^"]*)" for the screen "([^"]*)"$`, w.namesCapability)
	sc.Step(`^the application asks for a lazy screens file$`, w.asksForScreens)
	sc.Step(`^the application asks for a manifest index$`, w.asksForIndex)
	sc.Step(`^the application keeps its plugin packages in a package\.json$`, w.keepsPackageJSON)
}

// whenScreens registers the steps that read the manifests or wire the plugins.
func whenScreens(sc *godog.ScenarioContext, w *screensWorld) {
	sc.Step(`^the manifests are read$`, w.readManifests)
	sc.Step(`^the plugins are wired$`, w.wirePlugins)
}

// thenScreens registers the steps that check what was read or written.
func thenScreens(sc *godog.ScenarioContext, w *screensWorld) {
	sc.Step(`^the manifest of "([^"]*)" has the menu entry (\{.*\})$`, w.readMenuEntry)
	sc.Step(`^the manifest of "([^"]*)" names the screens module "([^"]*)"$`, w.readScreens)
	sc.Step(`^the manifest of "([^"]*)" names "([^"]*)" for the screen "([^"]*)"$`, w.readCapability)
	sc.Step(`^the manifest of "([^"]*)" keeps no extra key$`, w.readNoExtra)
	sc.Step(`^the lazy screens file maps these plugins, in order:$`, w.mapsInOrder)
	sc.Step(`^the lazy screens file maps no plugin$`, w.mapsNoPlugin)
	sc.Step(`^the frontend wiring imports "([^"]*)" and "([^"]*)"$`, w.importsFrontends)
	sc.Step(`^the generator writes only the Go and the TypeScript wiring$`, w.writesOnlyWiring)
	sc.Step(`^the wiring is refused with: (.+)$`, w.refusedWith)
	sc.Step(`^the files of the earlier wiring are left as they were$`, w.earlierWiringKept)
	sc.Step(`^the package\.json depends only on "([^"]*)" as a workspace package$`, w.dependsOnlyOn)
	sc.Step(`^the manifest index holds this entry for "([^"]*)":$`, w.indexHolds)
}

// initializeScreens gives each scenario of the screens feature a world of its own and its steps.
func initializeScreens(t *testing.T) func(*godog.ScenarioContext) {
	return func(sc *godog.ScenarioContext) {
		w := &screensWorld{t: t, manifests: map[string]map[string]any{}, cfg: screensConfig}
		givenScreens(sc, w)
		whenScreens(sc, w)
		thenScreens(sc, w)
	}
}
