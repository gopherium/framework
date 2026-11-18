// SPDX-License-Identifier: Apache-2.0

package wire

import (
	"encoding/json"
	"testing"
)

// wrongCaseKeys lists keys a struct of the manifest reads, spelled in another case, and the text rejecting each.
func wrongCaseKeys() map[string]wrongShape {
	return map[string]wrongShape{
		"a top level key in capitals": {
			`"DESCRIPTION": "Sells seats"`, `the key "DESCRIPTION" must be spelled "description"`,
		},
		"a top level key in mixed case":           {`"Version": "2.0.0"`, `the key "Version" must be spelled "version"`},
		"a top level key beside its own spelling": {`"Name": "Other"`, `the key "Name" must be spelled "name"`},
		"a top level key with an underscore": {
			`"Screen_Capabilities": {"seats": "a"}`, `the key "Screen_Capabilities" must be spelled "screen_capabilities"`,
		},
		"a top level key that folds by Unicode": {`"job` + "ſ" + `": []`, `the key "jobſ" must be spelled "jobs"`},
		"a key in another case before a wrong type": {
			`"Version": 5`, `the key "Version" must be spelled "version"`,
		},
		"a setting key": {
			`"settings": [{"key": "a", "type": "b", "Label": "c"}]`, `settings[0]: the key "Label" must be spelled "label"`,
		},
		"a later setting key": {
			`"settings": [{"key": "a", "type": "b"}, {"KEY": "c", "type": "d"}]`,
			`settings[1]: the key "KEY" must be spelled "key"`,
		},
		"a menu entry key": {
			`"menu": [{"label": "a", "screen": "b", "Capability": "c"}]`,
			`menu[0]: the key "Capability" must be spelled "capability"`,
		},
		"a job key": {`"jobs": [{"name": "a", "EVERY": "1h"}]`, `jobs[0]: the key "EVERY" must be spelled "every"`},
		"a define object key": {
			`"defines": ["a", {"Hook": "b"}]`, `defines[1]: the key "Hook" must be spelled "hook"`,
		},
	}
}

// repeatedKeys lists objects that hold one name twice, and the text rejecting each.
func repeatedKeys() map[string]wrongShape {
	return map[string]wrongShape{
		"a top level key twice":            {`"name": "Other"`, `the key "name" appears twice`},
		"an extra key twice":               {`"note": 1, "note": 2`, `the key "note" appears twice`},
		"a key twice once written escaped": {`"pages/all": 1, "pages\/all": 2`, `the key "pages/all" appears twice`},
		"a key twice before a wrong type":  {`"version": "1", "version": 5`, `the key "version" appears twice`},
		"a list key twice": {
			`"defines": [{"hook": "a", "kind": "filter", "fires": "job"}], "defines": ["b"]`,
			`the key "defines" appears twice`,
		},
		"a list key twice with no hook": {
			`"defines": [{"hook": "a"}], "defines": [{"kind": "filter"}]`, `the key "defines" appears twice`,
		},
		"a list key twice with a null": {`"defines": [{"hook": "a"}], "defines": [null]`, `the key "defines" appears twice`},
		"a setting key twice": {
			`"settings": [{"key": "a", "type": "b", "key": "c"}]`, `settings[0]: the key "key" appears twice`,
		},
		"a define object key twice": {
			`"defines": [{"hook": "a", "hook": "b"}]`, `defines[0]: the key "hook" appears twice`,
		},
		"a requires key twice": {`"requires": {"app": "1", "app": "2"}`, `requires: the key "app" appears twice`},
		"a key twice inside an extra value": {
			`"pages": [{"word": "forums"}, {"word": "forums", "word": "topics"}]`, `pages[1]: the key "word" appears twice`,
		},
		"a key twice deep inside an extra value": {
			`"assets": {"style": {"a": 1, "a": 2}}`, `assets.style: the key "a" appears twice`,
		},
		"a key twice after a number past float64": {
			`"pages": [1e400, {"a": 1, "a": 2}]`, `pages[1]: the key "a" appears twice`,
		},
		"a key twice under an empty key": {`"": {"a": 1, "a": 2}`, `[""]: the key "a" appears twice`},
		"a key twice under a key holding a dot": {
			`"a.b": {"c": 1, "c": 2}`, `["a.b"]: the key "c" appears twice`,
		},
		"a key twice under a later key holding a bracket": {
			`"assets": {"x[0]": [{"c": 1, "c": 2}]}`, `assets["x[0]"][0]: the key "c" appears twice`,
		},
	}
}

func TestLoadReadsAKeyOfDataAsWritten(t *testing.T) {
	t.Parallel()

	got := feedWith(t, `"GraphQL": true, "note": 1, "Note": 2, "ver-sion": "a", "ver_sion": "b", `+
		`"pages": [{"Word": "forums", "word": "topics"}], "requires": {"App": ">=1", "app": ">=2"}, `+
		`"screen_capabilities": {"Seats": "events.manage"}, "settings": [{"key": "a", "type": "b", "Note": 1}]`)

	assertSame(t, got.Requires, map[string]string{"App": ">=1", "app": ">=2"})
	assertSame(t, got.ScreenCapabilities, map[string]string{"Seats": "events.manage"})
	assertSame(t, got.Settings, []Setting{{Key: "a", Type: "b"}})
	assertSame(t, got.Extra, map[string]json.RawMessage{
		"GraphQL":  json.RawMessage(`true`),
		"note":     json.RawMessage(`1`),
		"Note":     json.RawMessage(`2`),
		"ver-sion": json.RawMessage(`"a"`),
		"ver_sion": json.RawMessage(`"b"`),
		"pages":    json.RawMessage(`[{"Word": "forums", "word": "topics"}]`),
	})
}

func TestManifestRejectsAWrongKeyWhenReadBack(t *testing.T) {
	t.Parallel()

	tests := map[string]struct{ index, want string }{
		"a key in another case": {`[{"id": "feed", "Name": "Feed"}]`, `the key "Name" must be spelled "name"`},
		"a key twice":           {`[{"id": "feed", "id": "other"}]`, `the key "id" appears twice`},
	}
	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			var got []Manifest
			err := json.Unmarshal([]byte(tc.index), &got)

			assertErrorText(t, err, tc.want)
		})
	}
}
