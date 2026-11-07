// SPDX-License-Identifier: Apache-2.0

package wire

import (
	"io/fs"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// everyNewKey is every key a manifest may name beside the ones wire has always read.
const everyNewKey = `"version": "2.0.0",
	"license": "Apache-2.0",
	"did": "did:plc:example",
	"requires": {"app": ">=0.31.0"},
	"tested": "0.32",
	"engines": ["postgres", "sqlite"],
	"uninstall": true,
	"hooks": ["content.changed"],
	"defines": ["shop.order-placed", {"hook": "forum.post-check", "kind": "filter", "fires": "job"}],
	"jobs": [{"name": "forget-old", "every": "24h"}],
	"settings": [{"key": "max_seats", "type": "count", "default": "4", "min": 1, "max": 50}],
	"menu": [{"label": "Reservations", "screen": "reservations", "capability": "events.manage"}],
	"screens": "@myapp/plugin-feed/screens",
	"screen_capabilities": {"seats": "events.manage"},
	"pages": [{"word": "forums"}],
	"islands": ["reply-form"],
	"blocks": ["blocks/latest-topics"],
	"assets": {"style": "public/forum.css"},
	"mail_addresses": true`

// appPackageJSON is the package.json of an application before the generator runs.
const appPackageJSON = `{
	"name": "@myapp/frontend",
	"dependencies": {
		"@myapp/frontend-sdk": "workspace:*"
	}
}
`

// withKeys returns manifestJSON with keys added before its closing brace.
func withKeys(manifestJSON, keys string) string {
	return strings.TrimSuffix(manifestJSON, "}") + ",\n\t" + keys + "\n}"
}

// wiringTree writes manifests by plugin folder, and every directory and package.json cfg needs, under a new root.
func wiringTree(t *testing.T, cfg Config, manifests map[string]string) string {
	t.Helper()
	root := t.TempDir()
	for dir, manifestJSON := range manifests {
		writePlugin(t, root, dir, manifestJSON)
	}
	makeDirs(t, root, "cmd/myapp", "frontend/src/plugins", "internal/graphroot")
	if cfg.PackageJSONPath != "" {
		writeFile(t, root, cfg.PackageJSONPath, appPackageJSON)
	}
	return root
}

// generatedFiles returns every file under root outside the plugin folders, by slash path.
func generatedFiles(t *testing.T, root string) map[string]string {
	t.Helper()
	files := map[string]string{}
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		relative, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		if entry.IsDir() {
			if relative == "plugins" {
				return fs.SkipDir
			}
			return nil
		}
		data, err := os.ReadFile(path)
		files[filepath.ToSlash(relative)] = string(data)
		return err
	})
	if err != nil {
		t.Fatalf("walking %s: %v", root, err)
	}
	return files
}

// generate runs the generator over manifests and returns every file it left outside the plugin folders.
func generate(t *testing.T, cfg Config, manifests map[string]string) map[string]string {
	t.Helper()
	root := wiringTree(t, cfg, manifests)
	if err := Run(root, cfg); err != nil {
		t.Fatalf("Run() error = %v, want nil", err)
	}
	return generatedFiles(t, root)
}

// assertSameFiles checks that two generated trees hold the same files with the same bytes.
func assertSameFiles(t *testing.T, got, want map[string]string) {
	t.Helper()
	if !maps.Equal(got, want) {
		t.Errorf("generated files = %q, want %q", got, want)
	}
}

// everySwitchToday lists the configs that switch on each output wire has always had.
func everySwitchToday() map[string]Config {
	withPackageJSON := testConfig
	withPackageJSON.PackageJSONPath = "frontend/package.json"
	withRegistry := testConfig
	withRegistry.GoRegistryPath = "internal/graphroot/registry_gen.go"
	withRegistry.GoRegistryPackage = "graphroot"
	withBoth := withRegistry
	withBoth.PackageJSONPath = withPackageJSON.PackageJSONPath
	return map[string]Config{
		"only the wiring": testConfig,
		"a package.json":  withPackageJSON,
		"a registry":      withRegistry,
		"both":            withBoth,
	}
}

func TestRunKeepsTheWiringBytesWhenManifestsGainNewFields(t *testing.T) {
	t.Parallel()

	alphaManifest := `{"id": "alpha", "name": "Alpha", "backend": "example.com/myapp/plugins/alpha"}`
	for name, cfg := range everySwitchToday() {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			plain := generate(t, cfg, map[string]string{"alpha": alphaManifest, "feed": feedManifest})
			gained := generate(t, cfg, map[string]string{
				"alpha": withKeys(alphaManifest, everyNewKey),
				"feed":  withKeys(feedManifest, everyNewKey),
			})

			assertSameFiles(t, gained, plain)
		})
	}
}

func TestRunKeepsTheWiringBytesOfAManifestWithAGraphqlKey(t *testing.T) {
	t.Parallel()

	plain := map[string]string{}
	withGraphql := map[string]string{}
	for _, id := range []string{"email", "fields", "importer", "whatsapp"} {
		manifestJSON := `{"id": "` + id + `", "name": "A plugin", "backend": "example.com/myapp/plugins/` + id +
			`", "frontend": "@myapp/plugin-` + id + `"}`
		plain[id] = manifestJSON
		withGraphql[id] = withKeys(manifestJSON, `"graphql": true`)
	}
	for name, cfg := range everySwitchToday() {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			assertSameFiles(t, generate(t, cfg, withGraphql), generate(t, cfg, plain))
		})
	}
}

func TestRunWritesNoNewFileWhenEverySwitchIsOff(t *testing.T) {
	t.Parallel()

	files := generate(t, testConfig, map[string]string{"feed": withKeys(feedManifest, everyNewKey)})

	want := []string{testConfig.GoWiringPath, testConfig.TSWiringPath}
	if got := slices.Sorted(maps.Keys(files)); !slices.Equal(got, want) {
		t.Errorf("generated files = %q, want only %q", got, want)
	}
}

// todaysErrorCase is one manifest wire refuses, with the text it has always given after the manifest's path.
type todaysErrorCase struct {
	dir      string
	manifest string
	want     string
}

// todaysErrors lists the manifests wire refuses and the text each has always given.
func todaysErrors() map[string]todaysErrorCase {
	return map[string]todaysErrorCase{
		"malformed json": {dir: "broken", manifest: `{`, want: "unexpected end of JSON input"},
		"a number for the id": {
			dir: "numbered", manifest: `{"id": 5}`,
			want: "json: cannot unmarshal number into Go struct field manifest.id of type string",
		},
		"a number for the backend": {
			dir: "numbered", manifest: `{"id": "numbered", "name": "Numbered", "backend": 5}`,
			want: "json: cannot unmarshal number into Go struct field manifest.backend of type string",
		},
		"a list at the top level": {
			dir: "listed", manifest: `[]`,
			want: "json: cannot unmarshal array into Go value of type wire.manifest",
		},
		"a missing id": {
			dir: "anon", manifest: `{"name": "Anon", "backend": "example.com/anon"}`,
			want: `id "" must match ^[a-z][a-z0-9-]*$`,
		},
		"an uppercase id with no name": {
			dir: "Loud", manifest: `{"id": "Loud"}`,
			want: `id "Loud" must match ^[a-z][a-z0-9-]*$`,
		},
		"an id that does not match its folder, with no name": {
			dir: "alias", manifest: `{"id": "other"}`,
			want: `id "other" does not match directory "alias"`,
		},
		"a missing name with no backend": {
			dir: "nameless", manifest: `{"id": "nameless"}`,
			want: "name is required",
		},
		"neither backend nor frontend": {
			dir: "hollow", manifest: `{"id": "hollow", "name": "Hollow"}`,
			want: "at least one of backend or frontend is required",
		},
		"a bad id beside an empty new key": {
			dir: "Loud", manifest: `{"id": "Loud", "name": "Loud", "backend": "example.com/loud", "engines": [""]}`,
			want: `id "Loud" must match ^[a-z][a-z0-9-]*$`,
		},
		"an id that does not match its folder beside an empty new key": {
			dir: "alias", manifest: `{"id": "other", "name": "Other", "backend": "example.com/other", "hooks": [""]}`,
			want: `id "other" does not match directory "alias"`,
		},
		"a missing name beside an empty new key": {
			dir: "nameless", manifest: `{"id": "nameless", "backend": "example.com/nameless", "jobs": [{}]}`,
			want: "name is required",
		},
		"neither backend nor frontend beside an empty new key": {
			dir: "hollow", manifest: `{"id": "hollow", "name": "Hollow", "settings": [{}]}`,
			want: "at least one of backend or frontend is required",
		},
	}
}

func TestRunKeepsTodaysErrorTexts(t *testing.T) {
	t.Parallel()

	for name, tc := range todaysErrors() {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			root := t.TempDir()
			writePlugin(t, root, tc.dir, tc.manifest)

			err := Run(root, testConfig)

			assertErrorText(t, err, "pluginwire: "+manifestPath(root, tc.dir)+": "+tc.want)
		})
	}
}

func TestRunKeepsTodaysErrorTextOfAMissingRoot(t *testing.T) {
	t.Parallel()

	root := t.TempDir()

	err := Run(root, testConfig)

	assertErrorText(t, err,
		"pluginwire: reading plugins directory: open "+filepath.Join(root, "plugins")+": no such file or directory")
}

func TestRunKeepsTodaysErrorTextOfAMissingManifest(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	writePlugin(t, root, "ghost", "")

	err := Run(root, testConfig)

	path := manifestPath(root, "ghost")
	assertErrorText(t, err, "pluginwire: "+path+": open "+path+": no such file or directory")
}
