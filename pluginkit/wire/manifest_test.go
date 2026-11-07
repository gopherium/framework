// SPDX-License-Identifier: Apache-2.0

package wire

import (
	"encoding/json"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
	"testing/fstest"
)

// fullManifest names every key a manifest can hold, its last one only as a raw extra.
const fullManifest = `{
	"id": "events",
	"name": "Events",
	"backend": "example.com/myapp/plugins/events",
	"frontend": "@myapp/plugin-events",
	"version": "2.0.0",
	"license": "Apache-2.0",
	"did": "did:plc:example",
	"requires": {"app": ">=0.31.0", "shop": "^2.1"},
	"tested": "0.32",
	"engines": ["postgres", "sqlite"],
	"uninstall": true,
	"hooks": ["content.changed", "page.head"],
	"defines": ["shop.order-placed", {"hook": "forum.post-check", "kind": "filter", "fires": "job"}],
	"jobs": [{"name": "forget-old", "every": "24h"}],
	"settings": [
		{"key": "max_seats", "type": "count", "default": "4", "label": "Seats", "help": "Per booking", "min": 1, "max": 50},
		{"key": "tone", "type": "choice", "options": ["calm", "loud"]},
		{"key": "venue", "type": "picker", "of": "venue"}
	],
	"menu": [
		{
			"label": "Reservations", "screen": "reservations", "parent": "content", "position": 30,
			"capability": "events.manage", "icon": "calendar"
		},
		{"label": "Check-in", "screen": "check-in", "capability": "events.checkin"}
	],
	"screens": "@myapp/plugin-events/screens",
	"screen_capabilities": {"seats": "events.manage"},
	"graphql": true
}`

// number returns a pointer to value, as a setting's limits hold it.
func number(value float64) *float64 {
	return &value
}

// position returns a pointer to value, as a menu entry's position holds it.
func position(value int) *int {
	return &value
}

// fullManifestRead is what fullManifest reads as.
func fullManifestRead() Manifest {
	return Manifest{
		ID:        "events",
		Name:      "Events",
		Backend:   "example.com/myapp/plugins/events",
		Frontend:  "@myapp/plugin-events",
		Version:   "2.0.0",
		License:   "Apache-2.0",
		DID:       "did:plc:example",
		Requires:  map[string]string{"app": ">=0.31.0", "shop": "^2.1"},
		Tested:    "0.32",
		Engines:   []string{"postgres", "sqlite"},
		Uninstall: true,
		Hooks:     []string{"content.changed", "page.head"},
		Defines: []Define{
			{Hook: "shop.order-placed"},
			{Hook: "forum.post-check", Kind: "filter", Fires: "job"},
		},
		Jobs: []Job{{Name: "forget-old", Every: "24h"}},
		Settings: []Setting{
			{
				Key: "max_seats", Type: "count", Default: "4", Label: "Seats", Help: "Per booking",
				Min: number(1), Max: number(50),
			},
			{Key: "tone", Type: "choice", Options: []string{"calm", "loud"}},
			{Key: "venue", Type: "picker", Of: "venue"},
		},
		Menu: []MenuEntry{
			{
				Label: "Reservations", Screen: "reservations", Parent: "content", Position: position(30),
				Capability: "events.manage", Icon: "calendar",
			},
			{Label: "Check-in", Screen: "check-in", Capability: "events.checkin"},
		},
		Screens:            "@myapp/plugin-events/screens",
		ScreenCapabilities: map[string]string{"seats": "events.manage"},
		Extra:              map[string]json.RawMessage{"graphql": json.RawMessage(`true`)},
	}
}

// loadOne reads manifestJSON as the only plugin of a new root, in a folder named dir.
func loadOne(t *testing.T, dir, manifestJSON string) (Manifest, error) {
	t.Helper()
	root := t.TempDir()
	writePlugin(t, root, dir, manifestJSON)
	manifests, err := Load(root, []string{"plugins"}, nil)
	if err != nil {
		return Manifest{}, err
	}
	if len(manifests) != 1 {
		t.Fatalf("Load() = %d manifests, want 1", len(manifests))
	}
	return manifests[0], nil
}

// feedWith reads the feed manifest with keys added, stopping the test when it does not load.
func feedWith(t *testing.T, keys string) Manifest {
	t.Helper()
	got, err := loadOne(t, "feed", withKeys(feedManifest, keys))
	if err != nil {
		t.Fatalf("Load() error = %v, want nil", err)
	}
	return got
}

// readOne reads manifestJSON through a plugin's own file system.
func readOne(manifestJSON string) (Manifest, error) {
	return Read(fstest.MapFS{"plugin.json": {Data: []byte(manifestJSON)}})
}

// assertSame checks that got deeply equals want.
func assertSame(t *testing.T, got, want any) {
	t.Helper()
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %+v, want %+v", got, want)
	}
}

// assertErrorText checks that err has exactly the text want.
func assertErrorText(t *testing.T, err error, want string) {
	t.Helper()
	if err == nil || err.Error() != want {
		t.Errorf("error = %v, want %q", err, want)
	}
}

// manifestPath returns the plugin.json path of a plugin folder under the plugins root of root.
func manifestPath(root, dir string) string {
	return filepath.Join(root, "plugins", dir, "plugin.json")
}

// assertIDs checks that manifests hold exactly the ids want, in order.
func assertIDs(t *testing.T, manifests []Manifest, want ...string) {
	t.Helper()
	got := make([]string, len(manifests))
	for i, m := range manifests {
		got[i] = m.ID
	}
	if !slices.Equal(got, want) {
		t.Errorf("ids = %q, want %q", got, want)
	}
}

// assertErrorFree stops the test when err is set.
func assertErrorFree(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatalf("error = %v, want nil", err)
	}
}

func TestLoadReadsEveryNewField(t *testing.T) {
	t.Parallel()

	got, err := loadOne(t, "events", fullManifest)

	assertErrorFree(t, err)
	assertSame(t, got, fullManifestRead())
}

func TestLoadLeavesAnAbsentOptionalKeyEmpty(t *testing.T) {
	t.Parallel()

	got, err := loadOne(t, "feed", feedManifest)

	assertErrorFree(t, err)
	assertSame(t, got, Manifest{
		ID: "feed", Name: "Feed", Backend: "example.com/myapp/plugins/feed", Frontend: "@myapp/plugin-feed",
	})
}

func TestLoadReadsAStringDefineAsAnEntryWithOnlyAHook(t *testing.T) {
	t.Parallel()

	got := feedWith(t, `"defines": ["shop.order-placed"]`)

	assertSame(t, got.Defines, []Define{{Hook: "shop.order-placed"}})
}

func TestDefineReplacesTheEntryItIsReadInto(t *testing.T) {
	t.Parallel()

	for name, data := range map[string]string{"a plain string": `"new"`, "an object": `{"hook": "new"}`} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			got := Define{Hook: "old", Kind: "filter", Fires: "job"}

			err := json.Unmarshal([]byte(data), &got)

			assertErrorFree(t, err)
			assertSame(t, got, Define{Hook: "new"})
		})
	}
}

func TestDefineKeepsTheEntryItFailsToRead(t *testing.T) {
	t.Parallel()

	got := Define{Hook: "old", Kind: "filter", Fires: "job"}

	err := json.Unmarshal([]byte(`{"hook": "new", "kind": 5}`), &got)

	assertErrorText(t, err, "json: cannot unmarshal number into Go struct field define.kind of type string")
	assertSame(t, got, Define{Hook: "old", Kind: "filter", Fires: "job"})
}

func TestLoadReadsARepeatedDefinesAsTheLastOne(t *testing.T) {
	t.Parallel()

	for name, last := range map[string]string{"a plain string": `"b"`, "an object": `{"hook": "b"}`} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			got := feedWith(t, `"defines": [{"hook": "a", "kind": "filter", "fires": "job"}], "defines": [`+last+`]`)

			assertSame(t, got.Defines, []Define{{Hook: "b"}})
		})
	}
}

func TestLoadLeavesAnAbsentFiresEmpty(t *testing.T) {
	t.Parallel()

	got := feedWith(t, `"defines": [{"hook": "forum.post-check", "kind": "filter"}]`)

	assertSame(t, got.Defines, []Define{{Hook: "forum.post-check", Kind: "filter"}})
}

func TestLoadKeepsAZeroLimitApartFromAnAbsentOne(t *testing.T) {
	t.Parallel()

	got := feedWith(t, `"settings": [{"key": "seats", "type": "count", "min": 0}]`)

	assertSame(t, got.Settings, []Setting{{Key: "seats", Type: "count", Min: number(0)}})
}

func TestLoadKeepsTwoMenuEntriesThatAskOneCapabilityForOneScreen(t *testing.T) {
	t.Parallel()

	got := feedWith(t, `"menu": [{"label": "a", "screen": "list", "capability": "b"}, `+
		`{"label": "c", "screen": "list", "parent": "content", "capability": "b"}]`)

	assertSame(t, got.Menu, []MenuEntry{
		{Label: "a", Screen: "list", Capability: "b"},
		{Label: "c", Screen: "list", Parent: "content", Capability: "b"},
	})
}

func TestLoadReadsANullInsideAMenuEntryAsAnAbsentKey(t *testing.T) {
	t.Parallel()

	got := feedWith(t, `"menu": [{"label": "a", "screen": "b", "capability": "c", "parent": null, "position": null, `+
		`"icon": null}]`)

	assertSame(t, got.Menu, []MenuEntry{{Label: "a", Screen: "b", Capability: "c"}})
}

func TestLoadKeepsAZeroPositionApartFromAnAbsentOne(t *testing.T) {
	t.Parallel()

	got := feedWith(t, `"menu": [{"label": "a", "screen": "b", "capability": "c", "position": 0}, `+
		`{"label": "d", "screen": "e", "capability": "f"}]`)

	assertSame(t, got.Menu, []MenuEntry{
		{Label: "a", Screen: "b", Capability: "c", Position: position(0)},
		{Label: "d", Screen: "e", Capability: "f"},
	})
}

func TestLoadKeepsAnUnknownKeyInExtra(t *testing.T) {
	t.Parallel()

	got := feedWith(t, `"graphql": true, "pages": [{"word": "forums"}], "note": null`)

	assertSame(t, got.Extra, map[string]json.RawMessage{
		"graphql": json.RawMessage(`true`),
		"pages":   json.RawMessage(`[{"word": "forums"}]`),
		"note":    json.RawMessage(`null`),
	})
}

func TestLoadReadsANullAsAnAbsentKey(t *testing.T) {
	t.Parallel()

	got := feedWith(t, `"version": null, "requires": null, "engines": null, "uninstall": null, "hooks": null, `+
		`"defines": null, "jobs": null, "settings": null, "menu": null, "screens": null, "screen_capabilities": null`)

	want, err := loadOne(t, "feed", feedManifest)
	assertErrorFree(t, err)
	assertSame(t, got, want)
}

func TestLoadReadsANullInsideRequiresAsAnEmptyRange(t *testing.T) {
	t.Parallel()

	got := feedWith(t, `"requires": {"app": null}`)

	assertSame(t, got.Requires, map[string]string{"app": ""})
}

func TestLoadDropsAKeyAnEntryHasNoFieldFor(t *testing.T) {
	t.Parallel()

	got := feedWith(t, `"defines": [{"hook": "a", "note": 1}], "jobs": [{"name": "b", "every": "1h", "note": 1}], `+
		`"settings": [{"key": "c", "type": "d", "note": 1}], "menu": [{"label": "e", "screen": "f", "capability": "g", `+
		`"note": 1}]`)

	assertSame(t, got.Defines, []Define{{Hook: "a"}})
	assertSame(t, got.Jobs, []Job{{Name: "b", Every: "1h"}})
	assertSame(t, got.Settings, []Setting{{Key: "c", Type: "d"}})
	assertSame(t, got.Menu, []MenuEntry{{Label: "e", Screen: "f", Capability: "g"}})
	assertSame(t, got.Extra, map[string]json.RawMessage(nil))
}

func TestLoadAndReadLeaveTheMeaningOfAValueAlone(t *testing.T) {
	t.Parallel()

	keys := `"version": "not a version", "license": "not a license", "requires": {"app": "whenever"}, ` +
		`"engines": ["no such engine"], "jobs": [{"name": "sweep", "every": "whenever"}], ` +
		`"settings": [{"key": "a", "type": "no such type", "min": 9, "max": 1}]`

	got := feedWith(t, keys)

	assertSame(t, got.Version, "not a version")
	assertSame(t, got.License, "not a license")
	assertSame(t, got.Requires, map[string]string{"app": "whenever"})
	assertSame(t, got.Engines, []string{"no such engine"})
	assertSame(t, got.Jobs, []Job{{Name: "sweep", Every: "whenever"}})
	assertSame(t, got.Settings, []Setting{{Key: "a", Type: "no such type", Min: number(9), Max: number(1)}})
	read, err := readOne(withKeys(feedManifest, keys))
	assertErrorFree(t, err)
	assertSame(t, read, got)
}

func TestReadLeavesTheIDsTheGeneratedWiringOwnsToLoad(t *testing.T) {
	t.Parallel()

	tests := map[string]string{
		"an id the Go wiring owns":         `{"id": "fmt", "name": "Fmt", "backend": "example.com/fmt"}`,
		"an id the TypeScript wiring owns": `{"id": "class", "name": "Class", "frontend": "@myapp/plugin-class"}`,
	}
	for name, manifestJSON := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			_, err := readOne(manifestJSON)

			assertErrorFree(t, err)
		})
	}
}

func TestLoadReadsANewKeyInAnyCaseAsEncodingJSONDoes(t *testing.T) {
	t.Parallel()

	got := feedWith(t, `"Version": "2.0.0", "ENGINES": ["sqlite"], "job`+"ſ"+`": [{"NAME": "sweep", "Every": "1h"}], `+
		`"defines": [{"HOOK": "a", "Kind": "b", "FIRES": "c"}], "ver-sion": "a", "ver_sion": "b"`)

	assertSame(t, got.Version, "2.0.0")
	assertSame(t, got.Engines, []string{"sqlite"})
	assertSame(t, got.Jobs, []Job{{Name: "sweep", Every: "1h"}})
	assertSame(t, got.Defines, []Define{{Hook: "a", Kind: "b", Fires: "c"}})
	assertSame(t, got.Extra, map[string]json.RawMessage{
		"ver-sion": json.RawMessage(`"a"`),
		"ver_sion": json.RawMessage(`"b"`),
	})
}

func TestTypedKeysNameEveryManifestField(t *testing.T) {
	t.Parallel()

	var fromTags []string
	fields := reflect.TypeFor[Manifest]()
	for i := range fields.NumField() {
		name, _, _ := strings.Cut(fields.Field(i).Tag.Get("json"), ",")
		if name != "-" {
			fromTags = append(fromTags, name)
		}
	}

	assertSame(t, typedKeys, fromTags)
}

// wrongShape is one set of keys a manifest may not hold, and the text refusing them.
type wrongShape struct{ keys, want string }

// wrongTypes lists keys of the wrong JSON type, and what the decoder says of each.
func wrongTypes() map[string]wrongShape {
	return map[string]wrongShape{
		"a number for version": {
			`"version": 5`, "json: cannot unmarshal number into Go struct field manifest.version of type string",
		},
		"a number for license": {
			`"license": 5`, "json: cannot unmarshal number into Go struct field manifest.license of type string",
		},
		"a number for did": {
			`"did": 5`, "json: cannot unmarshal number into Go struct field manifest.did of type string",
		},
		"a list for requires": {
			`"requires": []`,
			"json: cannot unmarshal array into Go struct field manifest.requires of type map[string]string",
		},
		"a number inside requires": {
			`"requires": {"app": 5}`,
			"json: cannot unmarshal number into Go struct field manifest.requires.app of type string",
		},
		"a number for tested": {
			`"tested": 5`, "json: cannot unmarshal number into Go struct field manifest.tested of type string",
		},
		"a string for engines": {
			`"engines": "postgres"`,
			"json: cannot unmarshal string into Go struct field manifest.engines of type []string",
		},
		"a number in engines": {
			`"engines": [5]`, "json: cannot unmarshal number into manifest.engines.0 of type string",
		},
		"a string for uninstall": {
			`"uninstall": "yes"`, "json: cannot unmarshal string into Go struct field manifest.uninstall of type bool",
		},
		"a string for hooks": {
			`"hooks": "page.head"`, "json: cannot unmarshal string into Go struct field manifest.hooks of type []string",
		},
	}
}

// wrongEntryTypes lists keys of the wrong JSON type inside jobs and settings, and what the decoder says of each.
func wrongEntryTypes() map[string]wrongShape {
	return map[string]wrongShape{
		"a number for a job name": {
			`"jobs": [{"name": 5, "every": "1h"}]`,
			"json: cannot unmarshal number into Go struct field manifest.jobs.0.name of type string",
		},
		"a number for a job interval": {
			`"jobs": [{"name": "a", "every": 5}]`,
			"json: cannot unmarshal number into Go struct field manifest.jobs.0.every of type string",
		},
		"a number for a setting key": {
			`"settings": [{"key": 5, "type": "b"}]`,
			"json: cannot unmarshal number into Go struct field manifest.settings.0.key of type string",
		},
		"a number for a setting type": {
			`"settings": [{"key": "a", "type": 5}]`,
			"json: cannot unmarshal number into Go struct field manifest.settings.0.type of type string",
		},
		"a number for a setting default": {
			`"settings": [{"key": "a", "type": "b", "default": 4}]`,
			"json: cannot unmarshal number into Go struct field manifest.settings.0.default of type string",
		},
		"a number for a setting label": {
			`"settings": [{"key": "a", "type": "b", "label": 5}]`,
			"json: cannot unmarshal number into Go struct field manifest.settings.0.label of type string",
		},
		"a number for a setting help": {
			`"settings": [{"key": "a", "type": "b", "help": 5}]`,
			"json: cannot unmarshal number into Go struct field manifest.settings.0.help of type string",
		},
		"a number for a setting of": {
			`"settings": [{"key": "a", "type": "b", "of": 5}]`,
			"json: cannot unmarshal number into Go struct field manifest.settings.0.of of type string",
		},
		"a string for min": {
			`"settings": [{"key": "a", "type": "b", "min": "1"}]`,
			"json: cannot unmarshal string into Go struct field manifest.settings.0.min of type float64",
		},
		"a string for max": {
			`"settings": [{"key": "a", "type": "b", "max": "1"}]`,
			"json: cannot unmarshal string into Go struct field manifest.settings.0.max of type float64",
		},
	}
}

// wrongDefines lists defines of the wrong JSON type, and the text naming the entry of each.
func wrongDefines() map[string]wrongShape {
	return map[string]wrongShape{
		"a string for defines": {
			`"defines": "a"`,
			"json: cannot unmarshal string into Go struct field manifest.defines of type []wire.Define",
		},
		"a number in defines": {
			`"defines": ["a", 5]`, "defines[1]: json: cannot unmarshal number into Go value of type wire.define",
		},
		"a list in defines": {
			`"defines": [["a"]]`, "defines[0]: json: cannot unmarshal array into Go value of type wire.define",
		},
		"true in defines": {
			`"defines": [true]`, "defines[0]: json: cannot unmarshal bool into Go value of type wire.define",
		},
		"a number for a defined hook": {
			`"defines": [{"hook": "a"}, {"hook": 5}]`,
			"defines[1]: json: cannot unmarshal number into Go struct field define.hook of type string",
		},
		"a number for a defined kind": {
			`"defines": [{"hook": "a", "kind": 5}]`,
			"defines[0]: json: cannot unmarshal number into Go struct field define.kind of type string",
		},
		"a number for a defined fires": {
			`"defines": [{"hook": "a", "fires": 5}]`,
			"defines[0]: json: cannot unmarshal number into Go struct field define.fires of type string",
		},
		"a wrong version before a wrong define": {
			`"version": 5, "defines": [5]`,
			"json: cannot unmarshal number into Go struct field manifest.version of type string",
		},
	}
}

// emptyValues lists keys that hold nothing where a value is required, and the text wire gives for each.
func emptyValues() map[string]wrongShape {
	return map[string]wrongShape{
		"an empty string in engines":   {`"engines": ["postgres", ""]`, "engines[1]: must not be empty"},
		"an empty string in hooks":     {`"hooks": [""]`, "hooks[0]: must not be empty"},
		"a null in hooks":              {`"hooks": ["page.head", null]`, "hooks[1]: must not be empty"},
		"an empty string in defines":   {`"defines": [""]`, "defines[0]: hook is required"},
		"a defines entry with no hook": {`"defines": ["a", {"kind": "filter"}]`, "defines[1]: hook is required"},
		"a null in defines":            {`"defines": [null]`, "defines[0]: hook is required"},
		"a repeated defines with no hook": {
			`"defines": [{"hook": "a"}], "defines": [{"kind": "filter"}]`, "defines[0]: hook is required",
		},
		"a repeated defines with a null": {`"defines": [{"hook": "a"}], "defines": [null]`, "defines[0]: hook is required"},
		"a job with no name":             {`"jobs": [{"every": "24h"}]`, "jobs[0]: name is required"},
		"a job with no interval": {
			`"jobs": [{"name": "a", "every": "1h"}, {"name": "b"}]`, "jobs[1]: every is required",
		},
		"a setting with no key": {`"settings": [{"type": "count"}]`, "settings[0]: key is required"},
		"a setting with no type": {
			`"settings": [{"key": "a", "type": "b"}, {"key": "c"}]`, "settings[1]: type is required",
		},
		"an empty engine before a hook": {`"engines": [""], "hooks": [""]`, "engines[0]: must not be empty"},
		"an empty hook before a define": {`"hooks": [""], "defines": [""]`, "hooks[0]: must not be empty"},
		"an empty define before a job":  {`"defines": [""], "jobs": [{}]`, "defines[0]: hook is required"},
		"an empty job before a setting": {`"jobs": [{}], "settings": [{}]`, "jobs[0]: name is required"},
		"an empty string in options": {
			`"settings": [{"key": "a", "type": "b", "options": ["c", ""]}]`,
			"settings[0].options[1]: must not be empty",
		},
		"an empty option in a later setting": {
			`"settings": [{"key": "a", "type": "b"}, {"key": "c", "type": "d", "options": [""]}]`,
			"settings[1].options[0]: must not be empty",
		},
	}
}

// wrongScreenTypes lists menu and screen keys of the wrong JSON type, and what the decoder says of each.
func wrongScreenTypes() map[string]wrongShape {
	return map[string]wrongShape{
		"an object for menu": {
			`"menu": {"label": "a"}`,
			"json: cannot unmarshal object into Go struct field manifest.menu of type []wire.MenuEntry",
		},
		"a number for a menu label": {
			`"menu": [{"label": 5, "screen": "a", "capability": "b"}]`,
			"json: cannot unmarshal number into Go struct field manifest.menu.0.label of type string",
		},
		"a number for a menu parent": {
			`"menu": [{"label": "a", "screen": "b", "capability": "c", "parent": 5}]`,
			"json: cannot unmarshal number into Go struct field manifest.menu.0.parent of type string",
		},
		"a string for a menu position": {
			`"menu": [{"label": "a", "screen": "b", "capability": "c", "position": "first"}]`,
			"json: cannot unmarshal string into Go struct field manifest.menu.0.position of type int",
		},
		"a fraction for a menu position": {
			`"menu": [{"label": "a", "screen": "b", "capability": "c", "position": 1.5}]`,
			"json: cannot unmarshal number 1.5 into Go struct field manifest.menu.0.position of type int",
		},
		"a number for a menu icon": {
			`"menu": [{"label": "a", "screen": "b", "capability": "c", "icon": 5}]`,
			"json: cannot unmarshal number into Go struct field manifest.menu.0.icon of type string",
		},
		"a list for screens": {
			`"screens": ["a"]`, "json: cannot unmarshal array into Go struct field manifest.screens of type string",
		},
		"a list for screen capabilities": {
			`"screen_capabilities": ["seats"]`,
			"json: cannot unmarshal array into Go struct field manifest.screen_capabilities of type map[string]string",
		},
		"a number for a screen capability": {
			`"screen_capabilities": {"seats": 3}`,
			"json: cannot unmarshal number into Go struct field manifest.screen_capabilities.seats of type string",
		},
	}
}

// emptyScreenValues lists menu entries and screen capabilities wire refuses, and the text it gives for each.
func emptyScreenValues() map[string]wrongShape {
	return map[string]wrongShape{
		"a menu entry with no label":      {`"menu": [{"screen": "a", "capability": "b"}]`, "menu[0]: label is required"},
		"a menu entry with no screen":     {`"menu": [{"label": "a", "capability": "b"}]`, "menu[0]: screen is required"},
		"a null in menu":                  {`"menu": [null]`, "menu[0]: label is required"},
		"an empty menu before its screen": {`"menu": [{"label": "", "screen": ""}]`, "menu[0]: label is required"},
		"a menu entry with no capability": {
			`"menu": [{"label": "a", "screen": "b", "capability": "c"}, {"label": "d", "screen": "e"}]`,
			"menu[1]: capability is required",
		},
		"a menu screen with a slash": {
			`"menu": [{"label": "a", "screen": "list/all", "capability": "b"}]`,
			`menu[0]: the screen "list/all" must be one path segment`,
		},
		"a menu screen that is only a slash": {
			`"menu": [{"label": "a", "screen": "/", "capability": "b"}]`, `menu[0]: the screen "/" must be one path segment`,
		},
		"a menu screen with a slash and no capability": {
			`"menu": [{"label": "a", "screen": "list/all"}]`, "menu[0]: capability is required",
		},
		"a null label in a menu entry": {
			`"menu": [{"label": null, "screen": "a", "capability": "b"}]`, "menu[0]: label is required",
		},
		"two capabilities for one menu screen": {
			`"menu": [{"label": "a", "screen": "list", "capability": "b"}, {"label": "c", "screen": "list", ` +
				`"capability": "d"}]`,
			`menu[1]: the screen "list" asks for "d" but menu[0] asks for "b"`,
		},
		"a later capability for one menu screen": {
			`"menu": [{"label": "a", "screen": "list", "capability": "b"}, {"label": "c", "screen": "list", ` +
				`"capability": "b"}, {"label": "e", "screen": "list", "capability": "f"}]`,
			`menu[2]: the screen "list" asks for "f" but menu[0] asks for "b"`,
		},
		"an incomplete entry for a menu screen asked before": {
			`"menu": [{"label": "a", "screen": "list", "capability": "b"}, {"label": "c", "screen": "list"}]`,
			"menu[1]: capability is required",
		},
		"a screen capability that is empty": {
			`"screen_capabilities": {"seats": ""}`, `screen_capabilities: the screen "seats" needs a capability`,
		},
		"a screen capability that is null": {
			`"screen_capabilities": {"seats": null}`, `screen_capabilities: the screen "seats" needs a capability`,
		},
		"an empty screen name": {
			`"screen_capabilities": {"": "a"}`, `screen_capabilities: the screen "" must be one path segment`,
		},
		"a screen name with a slash": {
			`"screen_capabilities": {"seats/all": "a"}`, `screen_capabilities: the screen "seats/all" must be one path segment`,
		},
		"a screen name with a slash and no capability": {
			`"screen_capabilities": {"seats/all": ""}`,
			`screen_capabilities: the screen "seats/all" must be one path segment`,
		},
		"the first wrong screen in name order": {
			`"screen_capabilities": {"b/c": "d", "a": ""}`, `screen_capabilities: the screen "a" needs a capability`,
		},
		"a screen capability for a screen of the menu": {
			`"menu": [{"label": "a", "screen": "list", "capability": "b"}, {"label": "c", "screen": "seats", ` +
				`"capability": "d"}], "screen_capabilities": {"seats": "e"}`,
			`screen_capabilities: the screen "seats" takes the capability of menu[1]`,
		},
		"a screen capability for the first screen of the menu": {
			`"menu": [{"label": "a", "screen": "list", "capability": "b"}], "screen_capabilities": {"list": "c"}`,
			`screen_capabilities: the screen "list" takes the capability of menu[0]`,
		},
		"an empty setting before a menu entry": {`"settings": [{}], "menu": [{}]`, "settings[0]: key is required"},
		"an empty menu entry before a screen capability": {
			`"menu": [{}], "screen_capabilities": {"": "a"}`, "menu[0]: label is required",
		},
	}
}

// eachWrongShape runs check in a parallel subtest for every wrong shape.
func eachWrongShape(t *testing.T, check func(t *testing.T, tc wrongShape)) {
	t.Helper()
	for _, shapes := range []map[string]wrongShape{
		wrongTypes(), wrongEntryTypes(), wrongDefines(), emptyValues(), wrongScreenTypes(), emptyScreenValues(),
	} {
		for name, tc := range shapes {
			t.Run(name, func(t *testing.T) {
				t.Parallel()

				check(t, tc)
			})
		}
	}
}

func TestLoadRefusesAWrongShape(t *testing.T) {
	t.Parallel()

	eachWrongShape(t, func(t *testing.T, tc wrongShape) {
		root := t.TempDir()
		writePlugin(t, root, "feed", withKeys(feedManifest, tc.keys))

		_, err := Load(root, []string{"plugins"}, nil)

		assertErrorText(t, err, "pluginwire: "+manifestPath(root, "feed")+": "+tc.want)
	})
}

func TestReadRefusesAWrongShape(t *testing.T) {
	t.Parallel()

	eachWrongShape(t, func(t *testing.T, tc wrongShape) {
		_, err := readOne(withKeys(feedManifest, tc.keys))

		assertErrorText(t, err, tc.want)
	})
}

func TestRunRefusesAWrongShapeAndKeepsTheEarlierWiring(t *testing.T) {
	t.Parallel()

	eachWrongShape(t, func(t *testing.T, tc wrongShape) {
		root, err := runBeside(t, testConfig, "beta", withKeys(
			`{"id": "beta", "name": "Beta", "backend": "example.com/myapp/plugins/beta"}`, tc.keys))

		assertErrorText(t, err, "pluginwire: "+manifestPath(root, "beta")+": "+tc.want)
		assertWiringKept(t, testConfig, root)
	})
}

func TestLoadRefusesEmptyRoots(t *testing.T) {
	t.Parallel()

	for name, roots := range map[string][]string{"nil": nil, "empty": {}} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			manifests, err := Load(t.TempDir(), roots, nil)

			assertErrorText(t, err, "wire: Load needs at least one root")
			assertSame(t, manifests, []Manifest(nil))
		})
	}
}

func TestLoadKeepsTodaysErrorTexts(t *testing.T) {
	t.Parallel()

	for name, tc := range todaysErrors() {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			root := t.TempDir()
			writePlugin(t, root, tc.dir, tc.manifest)

			manifests, err := Load(root, []string{"plugins"}, nil)

			assertErrorText(t, err, "pluginwire: "+manifestPath(root, tc.dir)+": "+tc.want)
			assertSame(t, manifests, []Manifest(nil))
		})
	}
}

func TestLoadKeepsTodaysErrorTextOfAMissingRoot(t *testing.T) {
	t.Parallel()

	root := t.TempDir()

	_, err := Load(root, []string{"plugins"}, nil)

	assertErrorText(t, err,
		"pluginwire: reading plugins directory: open "+filepath.Join(root, "plugins")+": no such file or directory")
}

func TestLoadKeepsTodaysErrorTextOfAMissingManifest(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	writePlugin(t, root, "ghost", "")

	_, err := Load(root, []string{"plugins"}, nil)

	path := manifestPath(root, "ghost")
	assertErrorText(t, err, "pluginwire: "+path+": open "+path+": no such file or directory")
}

func TestLoadRefusesTheIDsRunRefuses(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		id, manifest, want string
		reserved           []string
	}{
		"an id the application reserves": {
			id: "token", manifest: `{"id": "token", "name": "Token", "frontend": "@myapp/plugin-token"}`,
			reserved: []string{"serve", "token"}, want: `id "token" is reserved`,
		},
		"an id the Go wiring owns": {
			id: "fmt", manifest: `{"id": "fmt", "name": "Fmt", "backend": "example.com/fmt"}`,
			want: `id "fmt" collides with the generated Go wiring`,
		},
		"an id the TypeScript wiring owns": {
			id: "class", manifest: `{"id": "class", "name": "Class", "frontend": "@myapp/plugin-class"}`,
			want: `id "class" collides with the generated TypeScript wiring`,
		},
	}
	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			root := t.TempDir()
			writePlugin(t, root, tc.id, tc.manifest)

			_, err := Load(root, []string{"plugins"}, tc.reserved)

			assertErrorText(t, err, "pluginwire: "+manifestPath(root, tc.id)+": "+tc.want)
		})
	}
}

func TestLoadRefusesAnIDFoundInTwoRoots(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	writePlugin(t, root, "feed", feedManifest)
	writePluginIn(t, root, "extra", "feed", feedManifest)

	_, err := Load(root, []string{"plugins", "extra"}, nil)

	assertErrorText(t, err, "pluginwire: plugin feed appears under plugins and extra")
}

func TestLoadReadsEveryRootInOrder(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	writePlugin(t, root, "zeta", `{"id": "zeta", "name": "Zeta", "backend": "example.com/zeta"}`)
	writePluginIn(t, root, "extra", "alpha", `{"id": "alpha", "name": "Alpha", "backend": "example.com/alpha"}`)

	manifests, err := Load(root, []string{"plugins", "extra"}, nil)

	assertErrorFree(t, err)
	assertIDs(t, manifests, "zeta", "alpha")
}

func TestLoadAnswersAnEmptyListWhenARootHoldsNoPlugin(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	makeDirs(t, root, "plugins")

	manifests, err := Load(root, []string{"plugins"}, nil)

	assertErrorFree(t, err)
	assertSame(t, manifests, []Manifest{})
}

func TestLoadStillRefusesAManifestWithOnlyScreens(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	writePlugin(t, root, "feed", `{"id": "feed", "name": "Feed", "screens": "@myapp/plugin-feed/screens"}`)

	_, err := Load(root, []string{"plugins"}, nil)

	assertErrorText(t, err,
		"pluginwire: "+manifestPath(root, "feed")+": at least one of backend or frontend is required")
}

func TestReadTakesAPluginFileSystem(t *testing.T) {
	t.Parallel()

	got, err := readOne(fullManifest)

	assertErrorFree(t, err)
	assertSame(t, got, fullManifestRead())
}

func TestReadRefusesAManifestThatFailsOnItsOwn(t *testing.T) {
	t.Parallel()

	tests := map[string]struct{ manifest, want string }{
		"an id that breaks the pattern": {
			`{"id": "Loud", "name": "Loud", "backend": "example.com/loud"}`, `id "Loud" must match ^[a-z][a-z0-9-]*$`,
		},
		"a missing name": {`{"id": "feed", "backend": "example.com/feed"}`, "name is required"},
		"neither backend nor frontend": {
			`{"id": "feed", "name": "Feed"}`, "at least one of backend or frontend is required",
		},
		"malformed json": {`{`, "unexpected end of JSON input"},
	}
	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			got, err := readOne(tc.manifest)

			assertErrorText(t, err, tc.want)
			assertSame(t, got, Manifest{})
		})
	}
}

func TestReadReportsAMissingManifestWithNoPathOfItsOwn(t *testing.T) {
	t.Parallel()

	got, err := Read(fstest.MapFS{})

	assertErrorText(t, err, "open plugin.json: file does not exist")
	assertSame(t, got, Manifest{})
}
