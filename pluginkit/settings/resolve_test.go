// SPDX-License-Identifier: Apache-2.0

package settings

import (
	"errors"
	"maps"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/gopherium/framework/pluginkit/wire"
)

// savedSecretText is the text that reports a saved secret, which never quotes it.
const savedSecretText = "a secret comes from the environment only"

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

// inEnv returns the sources of an application that found env in the environment and nothing saved.
func inEnv(env map[string]string) Sources {
	return Sources{Env: env}
}

// savedAs returns the sources of an application that found nothing in the environment and saved holds.
func savedAs(saved map[string]string) Sources {
	return Sources{Saved: saved}
}

// resolvedWithReports works out the notes settings from found and fails the test on an error.
func resolvedWithReports(t *testing.T, found Sources) (Values, []RejectedSavedValue) {
	t.Helper()
	values, rejected, err := notesSchema(t).Resolve(found)
	if err != nil {
		t.Fatalf("Resolve = %v", err)
	}
	return values, rejected
}

// resolved works out the notes settings from found and fails the test on an error or a report.
func resolved(t *testing.T, found Sources) Values {
	t.Helper()
	values, rejected := resolvedWithReports(t, found)
	if rejected != nil {
		t.Errorf("Resolve reported %+v, want nil", rejected)
	}
	return values
}

// defaults returns the notes values when each one comes from its default.
func defaults() Values {
	return Values{
		"title":    {Type: TypeText, Source: SourceDefault, Text: "Notes"},
		"per_page": {Type: TypeCount, Source: SourceDefault, Count: 10},
		"open":     {Type: TypeFlag, Source: SourceDefault},
		"keep_for": {Type: TypeDuration, Source: SourceDefault, Text: "720h", Duration: 720 * time.Hour},
		"order":    {Type: TypeChoice, Source: SourceDefault, Text: "newest"},
		"tags":     {Type: TypeLines, Source: SourceDefault, Lines: []string{"news"}},
		"api_key":  {Type: TypeSecret, Source: SourceDefault},
	}
}

// everyVariable returns an environment that sets each notes setting.
func everyVariable() map[string]string {
	return map[string]string{
		"APP_PLUGIN_NOTES__TITLE":    " Field Notes ",
		"APP_PLUGIN_NOTES__PER_PAGE": "25",
		"APP_PLUGIN_NOTES__OPEN":     "true",
		"APP_PLUGIN_NOTES__KEEP_FOR": " 48h ",
		"APP_PLUGIN_NOTES__ORDER":    "oldest",
		"APP_PLUGIN_NOTES__TAGS":     "news,, events",
		"APP_PLUGIN_NOTES__API_KEY":  knownSecret,
	}
}

// fromEnvironment returns the notes values everyVariable works out to.
func fromEnvironment() Values {
	return Values{
		"title":    {Type: TypeText, Source: SourceEnvironment, Text: "Field Notes"},
		"per_page": {Type: TypeCount, Source: SourceEnvironment, Count: 25},
		"open":     {Type: TypeFlag, Source: SourceEnvironment, Flag: true},
		"keep_for": {Type: TypeDuration, Source: SourceEnvironment, Text: "48h", Duration: 48 * time.Hour},
		"order":    {Type: TypeChoice, Source: SourceEnvironment, Text: "oldest"},
		"tags":     {Type: TypeLines, Source: SourceEnvironment, Lines: []string{"news", "events"}},
		"api_key":  {Type: TypeSecret, Source: SourceEnvironment, Secret: hide(knownSecret)},
	}
}

// wantReport is the report a test wants: the key and the reason and text of its value error.
type wantReport struct {
	key, reason, text string
}

// onlyReport fails the test unless rejected holds one report, the one want describes.
func onlyReport(t *testing.T, rejected []RejectedSavedValue, want wantReport) {
	t.Helper()
	if len(rejected) != 1 {
		t.Errorf("Resolve reported %+v, want one report of %s", rejected, want.key)
		return
	}
	sameReport(t, rejected[0], want)
}

// sameReport fails the test unless report is the one want describes.
func sameReport(t *testing.T, report RejectedSavedValue, want wantReport) {
	t.Helper()
	var valueErr *ValueError
	if report.Key != want.key || !errors.As(report.Err, &valueErr) {
		t.Errorf("report %+v, want a value error of %s", report, want.key)
		return
	}
	if valueErr.Reason != want.reason || valueErr.Error() != want.text {
		t.Errorf("%s reported as %q reading %q, want %q reading %q",
			want.key, valueErr.Reason, valueErr, want.reason, want.text)
	}
}

// keysOf returns the key of each report, in its order.
func keysOf(rejected []RejectedSavedValue) []string {
	keys := make([]string, 0, len(rejected))
	for _, report := range rejected {
		keys = append(keys, report.Key)
	}
	return keys
}

func TestResolveTakesEachDefaultWhenTheEnvironmentHoldsNone(t *testing.T) {
	for _, found := range []Sources{
		{},
		{Env: map[string]string{}, Saved: map[string]string{}},
		inEnv(map[string]string{"APP_PLUGIN_OTHER__TITLE": "Other"}),
	} {
		sameValues(t, resolved(t, found), defaults())
	}
}

func TestResolveLetsTheEnvironmentWinOverTheDefault(t *testing.T) {
	sameValues(t, resolved(t, inEnv(everyVariable())), fromEnvironment())
}

func TestResolveLetsASavedValueWinOverTheDefault(t *testing.T) {
	values := resolved(t, savedAs(map[string]string{
		"title": " Field Notes ", "per_page": "25", "open": "true", "keep_for": " 48h ", "order": "oldest",
		"tags": "news,, events",
	}))
	want := defaults()
	want["title"] = Value{Type: TypeText, Source: SourceSaved, Text: "Field Notes"}
	want["per_page"] = Value{Type: TypeCount, Source: SourceSaved, Count: 25}
	want["open"] = Value{Type: TypeFlag, Source: SourceSaved, Flag: true}
	want["keep_for"] = Value{Type: TypeDuration, Source: SourceSaved, Text: "48h", Duration: 48 * time.Hour}
	want["order"] = Value{Type: TypeChoice, Source: SourceSaved, Text: "oldest"}
	want["tags"] = Value{Type: TypeLines, Source: SourceSaved, Lines: []string{"news", "events"}}
	sameValues(t, values, want)
}

func TestResolveLetsTheEnvironmentWinOverASavedValue(t *testing.T) {
	values := resolved(t, Sources{Env: everyVariable(), Saved: map[string]string{
		"title": "Saved Notes", "per_page": "30", "open": "false", "keep_for": "2h", "order": "newest",
		"tags": "saved", "api_key": "saved-key",
	}})
	sameValues(t, values, fromEnvironment())
}

func TestResolveLetsASavedValueApplyUnderABlankVariable(t *testing.T) {
	for _, blank := range []string{"", "   ", "\t"} {
		values := resolved(t, Sources{
			Env:   map[string]string{"APP_PLUGIN_NOTES__TITLE": blank, "APP_PLUGIN_NOTES__PER_PAGE": blank},
			Saved: map[string]string{"title": "Field Notes", "per_page": "25"},
		})
		want := defaults()
		want["title"] = Value{Type: TypeText, Source: SourceSaved, Text: "Field Notes"}
		want["per_page"] = Value{Type: TypeCount, Source: SourceSaved, Count: 25}
		sameValues(t, values, want)
	}
}

func TestResolveKeepsASavedEmptyTextOrEmptyLinesOverTheDefault(t *testing.T) {
	for _, empty := range []string{"", "  ", " , ,"} {
		values := resolved(t, savedAs(map[string]string{"title": empty, "tags": empty}))
		want := defaults()
		want["title"] = Value{Type: TypeText, Source: SourceSaved, Text: strings.TrimSpace(empty)}
		want["tags"] = Value{Type: TypeLines, Source: SourceSaved}
		sameValues(t, values, want)
	}
}

func TestResolveFallsBackToTheDefaultForASavedValueItsTypeRejects(t *testing.T) {
	cases := []struct {
		key, saved, reason, text string
	}{
		{"per_page", "banana", ReasonNotWhole, `must be a whole number, got "banana"`},
		{"per_page", "", ReasonNotWhole, `must be a whole number, got ""`},
		{"per_page", " 0 ", ReasonBelowMin, `must stand at or above 1, got "0"`},
		{"per_page", "60", ReasonAboveMax, `must stand at or below 50, got "60"`},
		{"open", "maybe", ReasonNotFlag, `must be true or false, got "maybe"`},
		{"open", "", ReasonNotFlag, `must be true or false, got ""`},
		{"keep_for", "soon", ReasonNotDuration, `must be a duration like 30s, got "soon"`},
		{"keep_for", "", ReasonNotDuration, `must be a duration like 30s, got ""`},
		{"keep_for", "-1h", ReasonNotPositive, `must stand above zero, got "-1h"`},
		{"order", "random", ReasonNotOption, `must be one of newest, oldest, got "random"`},
		{"order", "", ReasonNotOption, `must be one of newest, oldest, got ""`},
	}
	for _, c := range cases {
		values, rejected := resolvedWithReports(t, savedAs(map[string]string{c.key: c.saved}))
		sameValues(t, values, defaults())
		onlyReport(t, rejected, wantReport{c.key, c.reason, c.text})
	}
}

func TestResolveNeverTakesASavedSecretAndReportsItWithoutItsValue(t *testing.T) {
	for _, found := range []Sources{
		savedAs(map[string]string{"api_key": knownSecret}),
		savedAs(map[string]string{"api_key": ""}),
		{Env: map[string]string{"APP_PLUGIN_NOTES__API_KEY": "  "}, Saved: map[string]string{"api_key": knownSecret}},
	} {
		values, rejected := resolvedWithReports(t, found)
		sameValues(t, values, defaults())
		onlyReport(t, rejected, wantReport{"api_key", ReasonEnvironmentOnly, savedSecretText})
		for _, out := range renderings(rejected) {
			if strings.Contains(out, knownSecret) {
				t.Errorf("a rendering of the reports shows the secret: %q", out)
			}
		}
	}
}

func TestResolveNeitherReadsNorReportsASavedValueUnderAVariableThatHoldsOne(t *testing.T) {
	values := resolved(t, Sources{
		Env:   map[string]string{"APP_PLUGIN_NOTES__PER_PAGE": "25", "APP_PLUGIN_NOTES__API_KEY": knownSecret},
		Saved: map[string]string{"per_page": "banana", "api_key": "saved-key"},
	})
	want := defaults()
	want["per_page"] = Value{Type: TypeCount, Source: SourceEnvironment, Count: 25}
	want["api_key"] = Value{Type: TypeSecret, Source: SourceEnvironment, Secret: hide(knownSecret)}
	sameValues(t, values, want)
}

func TestResolveIgnoresSavedKeysTheSchemaDoesNotDeclare(t *testing.T) {
	values := resolved(t, Sources{
		Env:   map[string]string{"title": "Keyed Notes"},
		Saved: map[string]string{"colour": "green", "APP_PLUGIN_NOTES__TITLE": "Named Notes"},
	})
	sameValues(t, values, defaults())
}

func TestResolveReportsEachRejectedSavedValueInTheManifestsOrder(t *testing.T) {
	values, rejected := resolvedWithReports(t, savedAs(map[string]string{
		"order": "random", "api_key": knownSecret, "per_page": "banana", "title": "Field Notes", "open": "maybe",
	}))
	if got, want := keysOf(rejected), []string{"per_page", "open", "order", "api_key"}; !slices.Equal(got, want) {
		t.Fatalf("Resolve reported %q, want %q", got, want)
	}
	sameReport(t, rejected[0], wantReport{"per_page", ReasonNotWhole, `must be a whole number, got "banana"`})
	sameReport(t, rejected[1], wantReport{"open", ReasonNotFlag, `must be true or false, got "maybe"`})
	sameReport(t, rejected[2], wantReport{"order", ReasonNotOption, `must be one of newest, oldest, got "random"`})
	sameReport(t, rejected[3], wantReport{"api_key", ReasonEnvironmentOnly, savedSecretText})
	want := defaults()
	want["title"] = Value{Type: TypeText, Source: SourceSaved, Text: "Field Notes"}
	sameValues(t, values, want)
}

func TestResolveCountsAnEmptyOrBlankValueAsUnset(t *testing.T) {
	for _, empty := range []string{"", "   ", "\t"} {
		values := resolved(t, inEnv(map[string]string{
			"APP_PLUGIN_NOTES__PER_PAGE": empty, "APP_PLUGIN_NOTES__TITLE": empty,
		}))
		sameValues(t, values, defaults())
	}
}

func TestResolveLetsAListOfOnlyCommasClearTheDefaultList(t *testing.T) {
	values := resolved(t, inEnv(map[string]string{"APP_PLUGIN_NOTES__TAGS": " , ,"}))
	if got := values["tags"]; !sameValue(got, Value{Type: TypeLines, Source: SourceEnvironment}) {
		t.Errorf("tags with only commas = %+v, want no items in place of the default news", got)
	}
}

func TestResolveTakesASecretOnlyFromTheEnvironmentAndAsWritten(t *testing.T) {
	cases := []struct {
		value string
		want  Value
	}{
		{"", Value{Type: TypeSecret, Source: SourceDefault}},
		{"   ", Value{Type: TypeSecret, Source: SourceDefault}},
		{" key-for-example ", Value{Type: TypeSecret, Source: SourceEnvironment, Secret: hide(" key-for-example ")}},
	}
	for _, c := range cases {
		got := resolved(t, inEnv(map[string]string{"APP_PLUGIN_NOTES__API_KEY": c.value}))["api_key"]
		if !sameValue(got, c.want) {
			t.Errorf("api_key with %q = %+v revealing %q, want %+v", c.value, got, got.Secret.Reveal(), c.want)
		}
	}
}

func TestResolveNeverTakesTheDefaultOfASecret(t *testing.T) {
	schema := Schema{ID: "notes", Fields: []Field{
		{Key: "api_key", Type: TypeSecret, Env: "APP_PLUGIN_NOTES__API_KEY", Default: knownSecret},
	}}
	values, rejected, err := schema.Resolve(Sources{})
	if err != nil {
		t.Fatalf("Resolve = %v", err)
	}
	sameValues(t, values, Values{"api_key": {Type: TypeSecret, Source: SourceDefault}})
	if rejected != nil {
		t.Errorf("Resolve reported %+v, want nil", rejected)
	}
}

func TestResolveFillsOnlyTheSecretOfASecret(t *testing.T) {
	got := resolved(t, inEnv(map[string]string{"APP_PLUGIN_NOTES__API_KEY": knownSecret}))["api_key"]
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
	values, rejected, err := schema.Resolve(inEnv(map[string]string{"APP_PLUGIN_NOTES__OPEN": " "}))
	if err != nil {
		t.Fatalf("Resolve = %v", err)
	}
	sameValues(t, values, Values{
		"title": {Type: TypeText, Source: SourceDefault},
		"open":  {Type: TypeFlag, Source: SourceDefault},
		"tags":  {Type: TypeLines, Source: SourceDefault},
	})
	if rejected != nil {
		t.Errorf("Resolve reported %+v, want nil", rejected)
	}
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
		values, rejected, err := notesSchema(t).Resolve(inEnv(map[string]string{c.variable: c.value}))
		if err == nil || err.Error() != c.want {
			t.Errorf("%s=%q: Resolve = %v, want %q", c.variable, c.value, err, c.want)
		}
		if values != nil || rejected != nil {
			t.Errorf("%s=%q: Resolve answered %v and %v beside its error", c.variable, c.value, values, rejected)
		}
	}
}

func TestResolveRejectsAValueInTheEnvironmentWhateverIsSaved(t *testing.T) {
	_, _, err := notesSchema(t).Resolve(Sources{
		Env:   map[string]string{"APP_PLUGIN_NOTES__PER_PAGE": "banana"},
		Saved: map[string]string{"per_page": "25", "order": "random"},
	})
	if want := `APP_PLUGIN_NOTES__PER_PAGE: must be a whole number, got "banana"`; err == nil || err.Error() != want {
		t.Errorf("Resolve = %v, want %q", err, want)
	}
}

func TestResolveNamesTheVariableOfADefaultItsTypeRejects(t *testing.T) {
	schema := Schema{ID: "notes", Fields: []Field{
		{Key: "per_page", Type: TypeCount, Env: "APP_PLUGIN_NOTES__PER_PAGE", Default: "lots"},
	}}
	for _, found := range []Sources{{}, savedAs(map[string]string{"per_page": "banana"})} {
		values, rejected, err := schema.Resolve(found)
		if want := `APP_PLUGIN_NOTES__PER_PAGE: must be a whole number, got "lots"`; err == nil || err.Error() != want {
			t.Errorf("Resolve = %v, want %q", err, want)
		}
		if values != nil || rejected != nil {
			t.Errorf("Resolve answered %v and %v beside its error", values, rejected)
		}
	}
}

func TestResolveAnswersTheFirstRejectedValueInTheManifestsOrder(t *testing.T) {
	_, _, err := notesSchema(t).Resolve(inEnv(map[string]string{
		"APP_PLUGIN_NOTES__ORDER":    "random",
		"APP_PLUGIN_NOTES__PER_PAGE": "banana",
		"APP_PLUGIN_NOTES__OPEN":     "maybe",
	}))
	if want := `APP_PLUGIN_NOTES__PER_PAGE: must be a whole number, got "banana"`; err == nil || err.Error() != want {
		t.Errorf("Resolve = %v, want %q", err, want)
	}
}

func TestResolveNeverQuotesASecretBesideARejectedValue(t *testing.T) {
	for _, neighbour := range []string{"APP_PLUGIN_NOTES__PER_PAGE", "APP_PLUGIN_NOTES__OPEN"} {
		_, _, err := notesSchema(t).Resolve(Sources{
			Env:   map[string]string{"APP_PLUGIN_NOTES__API_KEY": knownSecret, neighbour: "banana"},
			Saved: map[string]string{"api_key": knownSecret},
		})
		if err == nil || strings.Contains(err.Error(), knownSecret) {
			t.Errorf("Resolve = %v, want an error that does not hold the secret", err)
		}
	}
}

func TestResolveRejectsAFieldOfAnUnknownTypeByItsVariable(t *testing.T) {
	schema := Schema{ID: "notes", Fields: []Field{{Key: "mood", Type: "colour", Env: "APP_PLUGIN_NOTES__MOOD"}}}
	want := `APP_PLUGIN_NOTES__MOOD: type "colour" is not one of text, count, flag, duration, choice, secret, lines`
	for _, found := range []Sources{
		{}, inEnv(map[string]string{"APP_PLUGIN_NOTES__MOOD": "blue"}), savedAs(map[string]string{"mood": "blue"}),
	} {
		_, _, err := schema.Resolve(found)
		if err == nil || err.Error() != want {
			t.Errorf("Resolve = %v, want %q", err, want)
		}
	}
}

func TestResolveOfASchemaWithNoFieldsAnswersNoValues(t *testing.T) {
	values, rejected, err := Schema{ID: "notes"}.Resolve(Sources{
		Env:   map[string]string{"APP_PLUGIN_NOTES__TITLE": "Notes"},
		Saved: map[string]string{"title": "Notes"},
	})
	if err != nil {
		t.Fatalf("Resolve = %v", err)
	}
	if values == nil || len(values) != 0 {
		t.Errorf("Resolve = %#v, want an empty map", values)
	}
	if rejected != nil {
		t.Errorf("Resolve reported %+v, want nil", rejected)
	}
}

func TestResolvedValuesNeverShowAKnownSecret(t *testing.T) {
	values := resolved(t, inEnv(map[string]string{"APP_PLUGIN_NOTES__API_KEY": knownSecret}))
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
