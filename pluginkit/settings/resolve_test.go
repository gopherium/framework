// SPDX-License-Identifier: Apache-2.0

package settings

import (
	"maps"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/gopherium/framework/pluginkit/wire"
)

// notesSchema returns the checked schema of a plugin notes that declares one setting of each type.
func notesSchema(t *testing.T) Schema {
	t.Helper()
	declared := everyType()
	declared[2].Default = "false"
	declared[5].Default = "news"
	schema, err := Check("notes", declared, appRules)
	if err != nil {
		t.Fatalf("Check = %v", err)
	}
	return schema
}

// resolved works out the notes settings from found and fails the test on an error.
func resolved(t *testing.T, found map[string]string) Values {
	t.Helper()
	values, err := notesSchema(t).Resolve(found)
	if err != nil {
		t.Fatalf("Resolve = %v", err)
	}
	return values
}

func TestResolveTakesEachDefaultWhenTheEnvironmentHoldsNone(t *testing.T) {
	for _, found := range []map[string]string{nil, {}, {"APP_PLUGIN_OTHER__TITLE": "Other"}} {
		values := resolved(t, found)
		want := Values{
			"title":    {Type: TypeText, Text: "Notes"},
			"per_page": {Type: TypeCount, Count: 10},
			"open":     {Type: TypeFlag},
			"keep_for": {Type: TypeDuration, Duration: 720 * time.Hour},
			"order":    {Type: TypeChoice, Text: "newest"},
			"tags":     {Type: TypeLines, Lines: []string{"news"}},
			"api_key":  {Type: TypeSecret},
		}
		sameValues(t, values, want)
	}
}

func TestResolveLetsTheEnvironmentWinOverTheDefault(t *testing.T) {
	values := resolved(t, map[string]string{
		"APP_PLUGIN_NOTES__TITLE":    " Field Notes ",
		"APP_PLUGIN_NOTES__PER_PAGE": "25",
		"APP_PLUGIN_NOTES__OPEN":     "true",
		"APP_PLUGIN_NOTES__KEEP_FOR": "48h",
		"APP_PLUGIN_NOTES__ORDER":    "oldest",
		"APP_PLUGIN_NOTES__TAGS":     "news,, events",
		"APP_PLUGIN_NOTES__API_KEY":  knownSecret,
	})
	want := Values{
		"title":    {Type: TypeText, Text: "Field Notes"},
		"per_page": {Type: TypeCount, Count: 25},
		"open":     {Type: TypeFlag, Flag: true},
		"keep_for": {Type: TypeDuration, Duration: 48 * time.Hour},
		"order":    {Type: TypeChoice, Text: "oldest"},
		"tags":     {Type: TypeLines, Lines: []string{"news", "events"}},
		"api_key":  {Type: TypeSecret, Secret: hide(knownSecret)},
	}
	sameValues(t, values, want)
}

func TestResolveCountsAnEmptyOrBlankValueAsUnset(t *testing.T) {
	for _, empty := range []string{"", "   ", "\t"} {
		values := resolved(t, map[string]string{"APP_PLUGIN_NOTES__PER_PAGE": empty, "APP_PLUGIN_NOTES__TITLE": empty})
		if got := values["per_page"].Count; got != 10 {
			t.Errorf("per_page with %q = %d, want the default 10", empty, got)
		}
		if got := values["title"].Text; got != "Notes" {
			t.Errorf("title with %q = %q, want the default Notes", empty, got)
		}
	}
}

func TestResolveLetsAListOfOnlyCommasClearTheDefaultList(t *testing.T) {
	values := resolved(t, map[string]string{"APP_PLUGIN_NOTES__TAGS": " , ,"})
	if got := values["tags"].Lines; got != nil {
		t.Errorf("tags with only commas = %q, want no items in place of the default news", got)
	}
}

func TestResolveTakesASecretOnlyFromTheEnvironmentAndAsWritten(t *testing.T) {
	cases := []struct {
		value, want string
	}{
		{"", ""},
		{"   ", "   "},
		{" key-for-example ", " key-for-example "},
	}
	for _, c := range cases {
		values := resolved(t, map[string]string{"APP_PLUGIN_NOTES__API_KEY": c.value})
		if got := values["api_key"].Secret.Reveal(); got != c.want {
			t.Errorf("api_key with %q reveals %q, want %q", c.value, got, c.want)
		}
	}
}

func TestResolveNeverTakesTheDefaultOfASecret(t *testing.T) {
	schema := Schema{ID: "notes", Fields: []Field{
		{Key: "api_key", Type: TypeSecret, Env: "APP_PLUGIN_NOTES__API_KEY", Default: knownSecret},
	}}
	values, err := schema.Resolve(nil)
	if err != nil {
		t.Fatalf("Resolve = %v", err)
	}
	if got := values["api_key"].Secret.String(); got != "not set" {
		t.Errorf("a secret with a default and no variable reads %q, want not set", got)
	}
}

func TestResolveFillsOnlyTheSecretOfASecret(t *testing.T) {
	got := resolved(t, map[string]string{"APP_PLUGIN_NOTES__API_KEY": knownSecret})["api_key"]
	if got.Text != "" || got.Lines != nil {
		t.Errorf("a secret filled %+v beside its Secret", got)
	}
}

func TestResolveGivesTheZeroValueWhenNeitherSourceHoldsOne(t *testing.T) {
	schema, err := Check("notes", []wire.Setting{
		{Key: "title", Type: TypeText}, {Key: "open", Type: TypeFlag}, {Key: "tags", Type: TypeLines},
	}, appRules)
	if err != nil {
		t.Fatalf("Check = %v", err)
	}
	values, err := schema.Resolve(map[string]string{"APP_PLUGIN_NOTES__OPEN": " "})
	if err != nil {
		t.Fatalf("Resolve = %v", err)
	}
	sameValues(t, values, Values{"title": {Type: TypeText}, "open": {Type: TypeFlag}, "tags": {Type: TypeLines}})
}

func TestResolveNamesTheVariableOfAValueItsTypeRejects(t *testing.T) {
	cases := []struct {
		variable, value, want string
	}{
		{"APP_PLUGIN_NOTES__PER_PAGE", "banana", `APP_PLUGIN_NOTES__PER_PAGE: must be a whole number, got "banana"`},
		{"APP_PLUGIN_NOTES__PER_PAGE", "2.5", `APP_PLUGIN_NOTES__PER_PAGE: must be a whole number, got "2.5"`},
		{"APP_PLUGIN_NOTES__PER_PAGE", "60", `APP_PLUGIN_NOTES__PER_PAGE: must stand at or below 50, got "60"`},
		{"APP_PLUGIN_NOTES__PER_PAGE", " 0 ", `APP_PLUGIN_NOTES__PER_PAGE: must stand at or above 1, got "0"`},
		{"APP_PLUGIN_NOTES__KEEP_FOR", "soon", `APP_PLUGIN_NOTES__KEEP_FOR: must be a duration like 30s, got "soon"`},
		{"APP_PLUGIN_NOTES__KEEP_FOR", "-1h", `APP_PLUGIN_NOTES__KEEP_FOR: must stand above zero, got "-1h"`},
		{"APP_PLUGIN_NOTES__OPEN", "maybe", `APP_PLUGIN_NOTES__OPEN: must be true or false, got "maybe"`},
		{"APP_PLUGIN_NOTES__ORDER", "random", `APP_PLUGIN_NOTES__ORDER: must be one of newest, oldest, got "random"`},
	}
	for _, c := range cases {
		values, err := notesSchema(t).Resolve(map[string]string{c.variable: c.value})
		if err == nil || err.Error() != c.want {
			t.Errorf("%s=%q: Resolve = %v, want %q", c.variable, c.value, err, c.want)
		}
		if values != nil {
			t.Errorf("%s=%q: Resolve answered %v beside its error", c.variable, c.value, values)
		}
	}
}

func TestResolveAnswersTheFirstRejectedValueInTheManifestsOrder(t *testing.T) {
	_, err := notesSchema(t).Resolve(map[string]string{
		"APP_PLUGIN_NOTES__ORDER":    "random",
		"APP_PLUGIN_NOTES__PER_PAGE": "banana",
		"APP_PLUGIN_NOTES__OPEN":     "maybe",
	})
	if want := `APP_PLUGIN_NOTES__PER_PAGE: must be a whole number, got "banana"`; err == nil || err.Error() != want {
		t.Errorf("Resolve = %v, want %q", err, want)
	}
}

func TestResolveNeverQuotesASecretBesideARejectedValue(t *testing.T) {
	for _, neighbour := range []string{"APP_PLUGIN_NOTES__PER_PAGE", "APP_PLUGIN_NOTES__OPEN"} {
		_, err := notesSchema(t).Resolve(map[string]string{"APP_PLUGIN_NOTES__API_KEY": knownSecret, neighbour: "banana"})
		if err == nil || strings.Contains(err.Error(), knownSecret) {
			t.Errorf("Resolve = %v, want an error that does not hold the secret", err)
		}
	}
}

func TestResolveRejectsAFieldOfAnUnknownTypeByItsVariable(t *testing.T) {
	schema := Schema{ID: "notes", Fields: []Field{{Key: "mood", Type: "colour", Env: "APP_PLUGIN_NOTES__MOOD"}}}
	_, err := schema.Resolve(map[string]string{"APP_PLUGIN_NOTES__MOOD": "blue"})
	want := `APP_PLUGIN_NOTES__MOOD: type "colour" is not one of text, count, flag, duration, choice, secret, lines`
	if err == nil || err.Error() != want {
		t.Errorf("Resolve = %v, want %q", err, want)
	}
}

func TestResolveOfASchemaWithNoFieldsAnswersNoValues(t *testing.T) {
	values, err := Schema{ID: "notes"}.Resolve(map[string]string{"APP_PLUGIN_NOTES__TITLE": "Notes"})
	if err != nil {
		t.Fatalf("Resolve = %v", err)
	}
	if values == nil || len(values) != 0 {
		t.Errorf("Resolve = %#v, want an empty map", values)
	}
}

func TestResolvedValuesNeverShowAKnownSecret(t *testing.T) {
	values := resolved(t, map[string]string{"APP_PLUGIN_NOTES__API_KEY": knownSecret})
	for _, value := range []any{values, &values, values["api_key"], map[string]Values{"notes": values}} {
		for _, out := range renderings(value) {
			if strings.Contains(out, knownSecret) {
				t.Errorf("a rendering of the values shows the secret: %q", out)
			}
		}
	}
	if !strings.Contains(encoded(values), `"Secret":"set"`) {
		t.Errorf("the JSON of the values %s does not mark the secret as set", encoded(values))
	}
}

// sameValues fails the test when got and want hold different readings or different keys.
func sameValues(t *testing.T, got, want Values) {
	t.Helper()
	keys, wanted := slices.Sorted(maps.Keys(got)), slices.Sorted(maps.Keys(want))
	if !slices.Equal(keys, wanted) {
		t.Errorf("keys %v, want %v", keys, wanted)
	}
	for key, value := range want {
		if !sameValue(got[key], value) {
			t.Errorf("%s = %+v, want %+v", key, got[key], value)
		}
	}
}
