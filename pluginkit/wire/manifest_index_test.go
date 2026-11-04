// SPDX-License-Identifier: Apache-2.0

package wire

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// indexPath is where these tests ask for the manifest index.
const indexPath = "internal/plugins/index.json"

// withIndex returns cfg with the manifest index switched on.
func withIndex(cfg Config) Config {
	cfg.ManifestIndexPath = indexPath
	return cfg
}

// runForIndex runs the generator over manifests with the index on and returns what it wrote there.
func runForIndex(t *testing.T, manifests map[string]string) string {
	t.Helper()
	root := wiringTree(t, withIndex(testConfig), manifests)
	makeDirs(t, root, "internal/plugins")
	if err := Run(root, withIndex(testConfig)); err != nil {
		t.Fatalf("Run() error = %v, want nil", err)
	}
	return readFile(t, root, indexPath)
}

// fullIndex is the index of the plugins fullManifest and feedManifest.
const fullIndex = `[
	{
		"id": "events",
		"name": "Events",
		"backend": "example.com/myapp/plugins/events",
		"frontend": "@myapp/plugin-events",
		"version": "2.0.0",
		"license": "Apache-2.0",
		"did": "did:plc:example",
		"requires": {
			"app": ">=0.31.0",
			"shop": "^2.1"
		},
		"tested": "0.32",
		"engines": [
			"postgres",
			"sqlite"
		],
		"uninstall": true,
		"hooks": [
			"content.changed",
			"page.head"
		],
		"defines": [
			{
				"hook": "shop.order-placed"
			},
			{
				"hook": "forum.post-check",
				"kind": "filter",
				"fires": "job"
			}
		],
		"jobs": [
			{
				"name": "forget-old",
				"every": "24h"
			}
		],
		"settings": [
			{
				"key": "max_seats",
				"type": "count",
				"default": "4",
				"label": "Seats",
				"help": "Per booking",
				"min": 1,
				"max": 50
			},
			{
				"key": "tone",
				"type": "choice",
				"options": [
					"calm",
					"loud"
				]
			},
			{
				"key": "venue",
				"type": "picker",
				"of": "venue"
			}
		],
		"graphql": true,
		"menu": [
			{
				"label": "Reservations"
			}
		],
		"screens": "@myapp/plugin-events/screens"
	},
	{
		"id": "feed",
		"name": "Feed",
		"backend": "example.com/myapp/plugins/feed",
		"frontend": "@myapp/plugin-feed"
	}
]
`

func TestRunWritesTheManifestIndexWhenTheSwitchIsOn(t *testing.T) {
	t.Parallel()

	got := runForIndex(t, map[string]string{"events": fullManifest, "feed": feedManifest})

	if got != fullIndex {
		t.Errorf("index = %s\nwant %s", got, fullIndex)
	}
}

func TestRunListsTheIndexInWiringOrderAcrossRoots(t *testing.T) {
	t.Parallel()

	cfg := withIndex(testConfig)
	cfg.Roots = []string{"plugins", "extra"}
	root := wiringTree(t, cfg, map[string]string{
		"mid":  `{"id": "mid", "name": "Mid", "frontend": "@myapp/plugin-mid"}`,
		"zeta": `{"id": "zeta", "name": "Zeta", "backend": "example.com/zeta"}`,
	})
	writePluginIn(t, root, "extra", "alpha", `{"id": "alpha", "name": "Alpha", "backend": "example.com/alpha"}`)
	makeDirs(t, root, "internal/plugins")

	if err := Run(root, cfg); err != nil {
		t.Fatalf("Run() error = %v, want nil", err)
	}

	assertIDs(t, readIndex(t, root), "mid", "zeta", "alpha")
}

// readIndex reads the manifest index under root back into manifests.
func readIndex(t *testing.T, root string) []Manifest {
	t.Helper()
	var got []Manifest
	if err := json.Unmarshal([]byte(readFile(t, root, indexPath)), &got); err != nil {
		t.Fatalf("reading the index back: %v", err)
	}
	return got
}

func TestManifestIndexLeavesOutAnAbsentOrEmptyOptionalKey(t *testing.T) {
	t.Parallel()

	got := runForIndex(t, map[string]string{
		"feed": withKeys(feedManifest,
			`"uninstall": false, "engines": [], "requires": {}, "settings": [{"key": "seats", "type": "count"}]`),
	})

	want := `[
	{
		"id": "feed",
		"name": "Feed",
		"backend": "example.com/myapp/plugins/feed",
		"frontend": "@myapp/plugin-feed",
		"settings": [
			{
				"key": "seats",
				"type": "count"
			}
		]
	}
]
`
	if got != want {
		t.Errorf("index = %s\nwant %s", got, want)
	}
}

func TestManifestIndexKeepsAZeroLimit(t *testing.T) {
	t.Parallel()

	got := runForIndex(t, map[string]string{
		"feed": withKeys(feedManifest, `"settings": [{"key": "seats", "type": "count", "min": 0, "max": 0}]`),
	})

	if !strings.Contains(got, `"min": 0,`) || !strings.Contains(got, `"max": 0`) {
		t.Errorf("index = %s, want both limits kept at zero", got)
	}
}

func TestManifestIndexKeepsMarkupReadable(t *testing.T) {
	t.Parallel()

	got := runForIndex(t, map[string]string{
		"feed": withKeys(feedManifest, `"requires": {"app": ">=0.31.0 <1.0.0"}, "note": "a & b <c>"`),
	})

	for _, want := range []string{`"app": ">=0.31.0 <1.0.0"`, `"note": "a & b <c>"`} {
		if !strings.Contains(got, want) {
			t.Errorf("index = %s, want it to hold %s", got, want)
		}
	}
}

func TestRunWritesAnEmptyIndexWhenNoPluginExists(t *testing.T) {
	t.Parallel()

	root := wiringTree(t, withIndex(testConfig), nil)
	makeDirs(t, root, "plugins", "internal/plugins")

	if err := Run(root, withIndex(testConfig)); err != nil {
		t.Fatalf("Run() error = %v, want nil", err)
	}

	if got := readFile(t, root, indexPath); got != "[]\n" {
		t.Errorf("index = %q, want an empty list", got)
	}
}

func TestRunWritesTheIndexBesideEveryOtherOutput(t *testing.T) {
	t.Parallel()

	for name, cfg := range everySwitchToday() {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			on := withIndex(cfg)
			root := wiringTree(t, on, map[string]string{"feed": feedManifest})
			makeDirs(t, root, "internal/plugins")

			if err := Run(root, on); err != nil {
				t.Fatalf("Run() error = %v, want nil", err)
			}

			files := generatedFiles(t, root)
			if files[indexPath] == "" {
				t.Errorf("generated files = %q, want the index among them", files)
			}
			delete(files, indexPath)
			assertSameFiles(t, files, generate(t, cfg, map[string]string{"feed": feedManifest}))
		})
	}
}

func TestRunWritesNoIndexWhenAPackageJSONStopsIt(t *testing.T) {
	t.Parallel()

	cfg := withIndex(testConfig)
	cfg.PackageJSONPath = "frontend/package.json"
	root := writeBeside(t, cfg, "beta", `{"id": "beta", "name": "Beta", "frontend": "@myapp/plugin-beta"}`)
	writeFile(t, root, cfg.PackageJSONPath, `{`)
	makeDirs(t, root, "internal/plugins")

	if err := Run(root, cfg); err == nil {
		t.Fatal("Run() error = nil, want the package.json error")
	}

	assertWiringKept(t, cfg, root)
	assertNoIndex(t, root)
}

// assertNoIndex checks that no manifest index exists under root.
func assertNoIndex(t *testing.T, root string) {
	t.Helper()
	if _, err := os.Stat(filepath.Join(root, filepath.FromSlash(indexPath))); !os.IsNotExist(err) {
		t.Errorf("the index exists after the failure, stat error = %v", err)
	}
}

// tooDeepForTheIndex returns feedManifest with a key as deep as the reader takes, the index list one level past it.
func tooDeepForTheIndex() string {
	return withKeys(feedManifest, `"note": `+strings.Repeat("[", 9999)+strings.Repeat("]", 9999))
}

func TestRunRefusesAManifestTooDeepForTheIndex(t *testing.T) {
	t.Parallel()

	cfg := withIndex(testConfig)
	root := writeBeside(t, cfg, "feed", tooDeepForTheIndex())
	makeDirs(t, root, "internal/plugins")

	err := Run(root, cfg)

	assertErrorText(t, err,
		"pluginwire: "+indexPath+": json: error calling MarshalJSON for type *wire.Manifest: exceeded max depth")
	assertWiringKept(t, cfg, root)
	assertNoIndex(t, root)
}

func TestRunTakesAManifestTooDeepForTheIndexWhenTheSwitchIsOff(t *testing.T) {
	t.Parallel()

	_, err := runBeside(t, testConfig, "feed", tooDeepForTheIndex())

	assertErrorFree(t, err)
}

func TestRunReportsAnIndexItCannotWrite(t *testing.T) {
	t.Parallel()

	root := wiringTree(t, withIndex(testConfig), map[string]string{"feed": feedManifest})

	err := Run(root, withIndex(testConfig))

	path := filepath.Join(root, filepath.FromSlash(indexPath))
	assertErrorText(t, err, "pluginwire: open "+path+": no such file or directory")
}

func TestManifestIndexReadsBackAsLoadReadsTheManifests(t *testing.T) {
	t.Parallel()

	root := wiringTree(t, withIndex(testConfig), map[string]string{
		"events": fullManifest,
		"feed":   withKeys(feedManifest, `"defines": ["shop.order-placed"]`),
	})
	makeDirs(t, root, "internal/plugins")
	if err := Run(root, withIndex(testConfig)); err != nil {
		t.Fatalf("Run() error = %v, want nil", err)
	}
	loaded, err := Load(root, []string{"plugins"}, nil)
	if err != nil {
		t.Fatalf("Load() error = %v, want nil", err)
	}

	got := readIndex(t, root)

	if len(got) != len(loaded) {
		t.Fatalf("index = %d manifests, want %d", len(got), len(loaded))
	}
	for i := range got {
		assertSameManifest(t, got[i], loaded[i])
	}
}

// assertSameManifest checks that got and want match, each raw extra compared by its JSON value.
func assertSameManifest(t *testing.T, got, want Manifest) {
	t.Helper()
	if len(got.Extra) != len(want.Extra) {
		t.Errorf("Extra = %q, want %q", got.Extra, want.Extra)
	}
	for key, raw := range want.Extra {
		var gotValue, wantValue any
		if err := json.Unmarshal(got.Extra[key], &gotValue); err != nil {
			t.Errorf("Extra[%s] = %q: %v", key, got.Extra[key], err)
		}
		if err := json.Unmarshal(raw, &wantValue); err != nil {
			t.Errorf("want Extra[%s] = %q: %v", key, raw, err)
		}
		if !reflect.DeepEqual(gotValue, wantValue) {
			t.Errorf("Extra[%s] = %v, want %v", key, gotValue, wantValue)
		}
	}
	got.Extra, want.Extra = nil, nil
	if !reflect.DeepEqual(got, want) {
		t.Errorf("manifest = %+v, want %+v", got, want)
	}
}

func TestManifestMarshalsUnderTheKeysOfItsFile(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		manifest Manifest
		want     string
	}{
		"no extra": {
			Manifest{ID: "feed", Name: "Feed"},
			`{"id":"feed","name":"Feed"}`,
		},
		"an empty extra": {
			Manifest{ID: "feed", Name: "Feed", Extra: map[string]json.RawMessage{}},
			`{"id":"feed","name":"Feed"}`,
		},
		"an extra after a typed object": {
			Manifest{
				ID: "feed", Name: "Feed", Requires: map[string]string{"app": "1"},
				Extra: map[string]json.RawMessage{"note": json.RawMessage(`1`)},
			},
			`{"id":"feed","name":"Feed","requires":{"app":"1"},"note":1}`,
		},
		"two extras in key order": {
			Manifest{
				ID: "feed", Name: "Feed",
				Extra: map[string]json.RawMessage{"zone": json.RawMessage(`2`), "area": json.RawMessage(`1`)},
			},
			`{"id":"feed","name":"Feed","area":1,"zone":2}`,
		},
	}
	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			got, err := json.Marshal(tc.manifest)

			if err != nil {
				t.Fatalf("json.Marshal() error = %v, want nil", err)
			}
			if string(got) != tc.want {
				t.Errorf("json.Marshal() = %s, want %s", got, tc.want)
			}
		})
	}
}

func TestManifestRefusesToMarshalAnExtraThatIsNotJSON(t *testing.T) {
	t.Parallel()

	_, err := json.Marshal(Manifest{
		ID: "feed", Name: "Feed", Extra: map[string]json.RawMessage{"note": json.RawMessage(`{`)},
	})

	if err == nil {
		t.Fatal("json.Marshal() error = nil, want an error")
	}
}

func TestManifestReportsAWrongShapeWhenReadBack(t *testing.T) {
	t.Parallel()

	var got Manifest
	err := json.Unmarshal([]byte(`{"id": "feed", "version": 5}`), &got)

	assertErrorText(t, err, "json: cannot unmarshal number into Go struct field manifest.version of type string")
}
