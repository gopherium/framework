// SPDX-License-Identifier: Apache-2.0

package wire

import (
	"slices"
	"strings"
	"testing"
)

// assertSynced checks that syncPackageJSON turns input into want for frontends.
func assertSynced(t *testing.T, input string, frontends []string, want string) {
	t.Helper()
	got, err := syncPackageJSON([]byte(input), frontends)
	if err != nil {
		t.Fatalf("syncPackageJSON() error = %v, want nil", err)
	}
	if string(got) != want {
		t.Errorf("syncPackageJSON() = %q, want %q", got, want)
	}
}

func TestSyncPackageJSONAddsAndRecordsANewPlugin(t *testing.T) {
	t.Parallel()

	input := `{
	"name": "@myapp/frontend",
	"private": true,
	"dependencies": {
		"@myapp/frontend-sdk": "workspace:*",
		"react": "^19.0.0"
	}
}
`
	want := `{
	"name": "@myapp/frontend",
	"private": true,
	"dependencies": {
		"@myapp/frontend-sdk": "workspace:*",
		"@myapp/plugin-feed": "workspace:*",
		"react": "^19.0.0"
	},
	"pluginwire": {
		"dependencies": [
			"@myapp/plugin-feed"
		]
	}
}
`
	assertSynced(t, input, []string{"@myapp/plugin-feed"}, want)
}

func TestSyncPackageJSONKeepsEveryUnrecordedDependency(t *testing.T) {
	t.Parallel()

	input := `{
	"name": "@myapp/frontend",
	"dependencies": {
		"@myapp/frontend-sdk": "workspace:*",
		"@myapp/plugin-feed": "workspace:*",
		"@myapp/ui-primitives": "workspace:*",
		"react": "^19.0.0"
	},
	"pluginwire": {
		"dependencies": [
			"@myapp/plugin-feed"
		]
	},
	"devDependencies": {
		"@myapp/test-kit": "workspace:*",
		"vitest": "^5.0.0"
	}
}
`
	want := `{
	"name": "@myapp/frontend",
	"dependencies": {
		"@myapp/frontend-sdk": "workspace:*",
		"@myapp/plugin-feed": "workspace:*",
		"@myapp/plugin-gallery": "workspace:*",
		"@myapp/ui-primitives": "workspace:*",
		"react": "^19.0.0"
	},
	"pluginwire": {
		"dependencies": [
			"@myapp/plugin-feed",
			"@myapp/plugin-gallery"
		]
	},
	"devDependencies": {
		"@myapp/test-kit": "workspace:*",
		"vitest": "^5.0.0"
	}
}
`
	assertSynced(t, input, []string{"@myapp/plugin-feed", "@myapp/plugin-gallery"}, want)
}

func TestSyncPackageJSONDropsARecordedPluginNoManifestNames(t *testing.T) {
	t.Parallel()

	input := `{
	"dependencies": {
		"@myapp/plugin-feed": "workspace:*",
		"@myapp/plugin-gallery": "workspace:*",
		"@myapp/plugin-legacy": "workspace:*"
	},
	"pluginwire": {
		"dependencies": [
			"@myapp/plugin-feed",
			"@myapp/plugin-gallery"
		]
	}
}
`
	want := `{
	"dependencies": {
		"@myapp/plugin-feed": "workspace:*",
		"@myapp/plugin-legacy": "workspace:*"
	},
	"pluginwire": {
		"dependencies": [
			"@myapp/plugin-feed"
		]
	}
}
`
	assertSynced(t, input, []string{"@myapp/plugin-feed"}, want)
}

func TestSyncPackageJSONTakesOverAHandWrittenEntry(t *testing.T) {
	t.Parallel()

	input := `{
	"dependencies": {
		"@myapp/plugin-feed": "^1.2.0",
		"react": "^19.0.0"
	}
}
`
	want := `{
	"dependencies": {
		"@myapp/plugin-feed": "workspace:*",
		"react": "^19.0.0"
	},
	"pluginwire": {
		"dependencies": [
			"@myapp/plugin-feed"
		]
	}
}
`
	assertSynced(t, input, []string{"@myapp/plugin-feed"}, want)
}

func TestSyncPackageJSONRefusesMalformedInput(t *testing.T) {
	t.Parallel()

	const notStrings = "pluginwire.dependencies is not an array of strings"
	tests := map[string]struct {
		input string
		want  string
	}{
		"empty file":                 {input: ``, want: "the top level is empty"},
		"only whitespace":            {input: " \r\n\t", want: "the top level is empty"},
		"bad syntax":                 {input: `{"name": }`, want: "invalid character '}'"},
		"unclosed object":            {input: `{"name": "a"`, want: "unexpected EOF"},
		"trailing comma":             {input: `{"name": "a",}`, want: "invalid character ','"},
		"name not a string":          {input: `{1: "a"}`, want: "object member name must be a string"},
		"invalid UTF-8":              {input: "{\"name\": \"\xff\"}", want: "invalid UTF-8"},
		"array at the top level":     {input: `[]`, want: "the top level is not an object"},
		"a second value at the end":  {input: `{} {}`, want: "data after the closing brace"},
		"text at the end":            {input: `{} x`, want: "data after the closing brace"},
		"duplicate name":             {input: `{"name": "a", "name": "b"}`, want: `duplicate object member name "name"`},
		"dependencies not an object": {input: `{"dependencies": []}`, want: "dependencies is not an object"},
		"duplicate dependency": {
			input: `{"dependencies": {"react": "^19.0.0", "react": "^18.0.0"}}`,
			want:  `duplicate object member name "react"`,
		},
		"null dependency":          {input: `{"dependencies": {"react": null}}`, want: `dependency "react" is not a string`},
		"number dependency":        {input: `{"dependencies": {"react": 19}}`, want: `dependency "react" is not a string`},
		"pluginwire not an object": {input: `{"pluginwire": []}`, want: "pluginwire is not an object"},
		"record missing":           {input: `{"pluginwire": {}}`, want: notStrings},
		"record an object":         {input: `{"pluginwire": {"dependencies": {}}}`, want: notStrings},
		"record null":              {input: `{"pluginwire": {"dependencies": null}}`, want: notStrings},
		"record holding a number":  {input: `{"pluginwire": {"dependencies": [1]}}`, want: notStrings},
		"record holding null":      {input: `{"pluginwire": {"dependencies": [null]}}`, want: notStrings},
	}

	for testName, tc := range tests {
		t.Run(testName, func(t *testing.T) {
			t.Parallel()

			for _, frontends := range [][]string{nil, {"@myapp/plugin-feed"}} {
				got, err := syncPackageJSON([]byte(tc.input), frontends)
				if err == nil || !strings.Contains(err.Error(), tc.want) {
					t.Errorf("syncPackageJSON(%d frontends) = %q, %v, want an error containing %q",
						len(frontends), got, err, tc.want)
				}
			}
		})
	}
}

func TestSyncPackageJSONRefusesAFrontendNameThatIsNotUTF8(t *testing.T) {
	t.Parallel()

	got, err := syncPackageJSON([]byte(`{}`), []string{"@myapp/plugin-\xff"})

	if err == nil || !strings.Contains(err.Error(), "invalid UTF-8") {
		t.Errorf("syncPackageJSON() = %q, %v, want an invalid UTF-8 error", got, err)
	}
}

func TestSyncPackageJSONKeepsACanonicalFileOnTheFirstRun(t *testing.T) {
	t.Parallel()

	canonical := `{
	"name": "@myapp/frontend",
	"private": true,
	"version": "0.0.0",
	"type": "module",
	"scripts": {
		"build": "tsc -b && vite build",
		"lint:css": "stylelint \"src/**/*.css\""
	},
	"dependencies": {
		"@myapp/frontend-sdk": "workspace:*",
		"@myapp/plugin-feed": "workspace:*",
		"react": "^19.0.0"
	},
	"devDependencies": {
		"typescript": "~6.0.2",
		"vitest": "^5.0.0"
	},
	"pluginwire": {
		"dependencies": [
			"@myapp/plugin-feed"
		]
	}
}
`
	assertSynced(t, canonical, []string{"@myapp/plugin-feed"}, canonical)
}

func TestSyncPackageJSONKeepsEscapedAndLiteralText(t *testing.T) {
	t.Parallel()

	canonical := "{\n" +
		"\t\"description\": \"Caf\\u00e9 menu\",\n" +
		"\t\"title\": \"Café menu\",\n" +
		"\t\"scripts\": {\n" +
		"\t\t\"greet\": \"echo Caf\\u00e9\"\n" +
		"\t},\n" +
		"\t\"dependencies\": {\n" +
		"\t\t\"@myapp/plugin-feed\": \"workspace:*\",\n" +
		"\t\t\"caf\\u00e9-icons\": \"npm:caf\\u00e9-icons@^1.0.0\"\n" +
		"\t},\n" +
		"\t\"pluginwire\": {\n" +
		"\t\t\"dependencies\": [\n" +
		"\t\t\t\"@myapp/plugin-feed\"\n" +
		"\t\t]\n" +
		"\t}\n" +
		"}\n"

	assertSynced(t, canonical, []string{"@myapp/plugin-feed"}, canonical)
}

func TestSyncPackageJSONIsStableOnItsOwnOutput(t *testing.T) {
	t.Parallel()

	inputs := map[string]string{
		"unsorted dependencies": `{"dependencies": {"react": "^19.0.0", "@myapp/frontend-sdk": "workspace:*"}}`,
		"no dependencies key":   `{"name": "@myapp/frontend"}`,
		"a stale record": `{"dependencies": {"@myapp/plugin-gone": "workspace:*"}, ` +
			`"pluginwire": {"dependencies": ["@myapp/plugin-gone"]}}`,
	}

	for testName, input := range inputs {
		t.Run(testName, func(t *testing.T) {
			t.Parallel()

			first, err := syncPackageJSON([]byte(input), []string{"@myapp/plugin-feed"})
			if err != nil {
				t.Fatalf("first syncPackageJSON() error = %v, want nil", err)
			}
			second, err := syncPackageJSON(first, []string{"@myapp/plugin-feed"})
			if err != nil {
				t.Fatalf("second syncPackageJSON() error = %v, want nil", err)
			}
			if string(second) != string(first) {
				t.Errorf("second run = %q, want the first run %q", second, first)
			}
		})
	}
}

func TestSyncPackageJSONRewritesSpacesAndCRLFToTabsOnce(t *testing.T) {
	t.Parallel()

	spaced := `{
  "name": "@myapp/frontend",
  "dependencies": {
    "react": "^19.0.0"
  }
}
`
	want := `{
	"name": "@myapp/frontend",
	"dependencies": {
		"@myapp/plugin-feed": "workspace:*",
		"react": "^19.0.0"
	},
	"pluginwire": {
		"dependencies": [
			"@myapp/plugin-feed"
		]
	}
}
`
	inputs := map[string]string{"spaces": spaced, "spaces and CRLF": strings.ReplaceAll(spaced, "\n", "\r\n")}

	for testName, input := range inputs {
		t.Run(testName, func(t *testing.T) {
			t.Parallel()

			assertSynced(t, input, []string{"@myapp/plugin-feed"}, want)
			assertSynced(t, want, []string{"@myapp/plugin-feed"}, want)
		})
	}
}

func TestSyncPackageJSONLeavesAFileWithoutFrontendsOrRecordUntouched(t *testing.T) {
	t.Parallel()

	inputs := map[string]string{
		"unsorted and space indented": `{
  "dependencies": {
    "react": "^19.0.0",
    "@myapp/frontend-sdk": "workspace:*"
  }
}`,
		"no dependencies key": `{"name": "@myapp/frontend"}`,
	}

	for testName, input := range inputs {
		t.Run(testName, func(t *testing.T) {
			t.Parallel()

			assertSynced(t, input, nil, input)
		})
	}
}

func TestSyncPackageJSONDropsTheRecordWhenNoFrontendRemains(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		input string
		want  string
	}{
		"other dependencies remain": {
			input: `{
	"dependencies": {
		"@myapp/plugin-feed": "workspace:*",
		"react": "^19.0.0"
	},
	"pluginwire": {
		"dependencies": [
			"@myapp/plugin-feed"
		]
	},
	"devDependencies": {
		"vitest": "^5.0.0"
	}
}
`,
			want: `{
	"dependencies": {
		"react": "^19.0.0"
	},
	"devDependencies": {
		"vitest": "^5.0.0"
	}
}
`,
		},
		"no dependency remains": {
			input: `{"dependencies": {"@myapp/plugin-feed": "workspace:*"}, ` +
				`"pluginwire": {"dependencies": ["@myapp/plugin-feed"]}}`,
			want: `{
	"dependencies": {}
}
`,
		},
		"no dependencies key": {
			input: `{"name": "@myapp/frontend", "pluginwire": {"dependencies": []}}`,
			want: `{
	"name": "@myapp/frontend"
}
`,
		},
	}

	for testName, tc := range tests {
		t.Run(testName, func(t *testing.T) {
			t.Parallel()

			assertSynced(t, tc.input, nil, tc.want)
		})
	}
}

func TestSyncPackageJSONAddsDependenciesLastWhenMissing(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		input string
		want  string
	}{
		"no record either": {
			input: `{
	"name": "@myapp/frontend",
	"devDependencies": {
		"vitest": "^5.0.0"
	}
}
`,
			want: `{
	"name": "@myapp/frontend",
	"devDependencies": {
		"vitest": "^5.0.0"
	},
	"dependencies": {
		"@myapp/plugin-feed": "workspace:*"
	},
	"pluginwire": {
		"dependencies": [
			"@myapp/plugin-feed"
		]
	}
}
`,
		},
		"a record already in place": {
			input: `{"pluginwire": {"dependencies": []}, "name": "@myapp/frontend"}`,
			want: `{
	"pluginwire": {
		"dependencies": [
			"@myapp/plugin-feed"
		]
	},
	"name": "@myapp/frontend",
	"dependencies": {
		"@myapp/plugin-feed": "workspace:*"
	}
}
`,
		},
	}

	for testName, tc := range tests {
		t.Run(testName, func(t *testing.T) {
			t.Parallel()

			assertSynced(t, tc.input, []string{"@myapp/plugin-feed"}, tc.want)
		})
	}
}

func TestFrontendPackagesSortsEveryNameOnce(t *testing.T) {
	t.Parallel()

	got := frontendPackages([]manifest{
		{ID: "gallery", Name: "Gallery", Frontend: "@myapp/plugin-gallery"},
		{ID: "headless", Name: "Headless", Backend: "example.com/headless"},
		{ID: "audit", Name: "Audit", Frontend: "@myapp/plugin-audit"},
		{ID: "audit-copy", Name: "Audit copy", Frontend: "@myapp/plugin-audit"},
	})

	want := []string{"@myapp/plugin-audit", "@myapp/plugin-gallery"}
	if !slices.Equal(got, want) {
		t.Errorf("frontendPackages() = %q, want %q", got, want)
	}
}
