// SPDX-License-Identifier: Apache-2.0

package settings

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/gopherium/framework/pluginkit/wire"
)

// formSchema returns the checked schema of a plugin notes that declares a setting of each type with its label.
func formSchema(t *testing.T) Schema {
	t.Helper()
	schema, err := Check("notes", []wire.Setting{
		{Key: "title", Type: TypeText, Default: "Notes", Label: "Title", Help: "Shown above the notes."},
		{Key: "per_page", Type: TypeCount, Default: "10", Min: number(1), Max: number(50), Label: "Per page"},
		{Key: "open", Type: TypeFlag, Default: "false", Label: "Open"},
		{Key: "keep_for", Type: TypeDuration, Default: "720h", Label: "Keep for"},
		{Key: "order", Type: TypeChoice, Default: "newest", Options: []string{"newest", "oldest"}, Label: "Order"},
		{Key: "tags", Type: TypeLines, Default: "news", Label: "Tags"},
		{Key: "api_key", Type: TypeSecret, Label: "API key"},
	}, appRules)
	if err != nil {
		t.Fatalf("Check = %v", err)
	}
	return schema
}

// formValues returns values of the form schema as an application finds them, title saved and per_page and api_key set.
func formValues() Values {
	return Values{
		"title":    {Type: TypeText, Source: SourceSaved, Text: "Field Notes"},
		"per_page": {Type: TypeCount, Source: SourceEnvironment, Count: 25},
		"open":     {Type: TypeFlag, Source: SourceDefault},
		"keep_for": {Type: TypeDuration, Source: SourceDefault, Text: "720h", Duration: 720 * time.Hour},
		"order":    {Type: TypeChoice, Source: SourceDefault, Text: "newest"},
		"tags":     {Type: TypeLines, Source: SourceDefault, Lines: []string{"news"}},
		"api_key":  {Type: TypeSecret, Source: SourceEnvironment, Secret: hide(knownSecret)},
	}
}

// oneField describes field for a form with value, as the only field of a plugin notes.
func oneField(field Field, value Value) FormField {
	return Schema{ID: "notes", Fields: []Field{field}}.FormFields(Values{field.Key: value})[0]
}

// sameJSON fails the test when value does not encode as want, read with its spaces and line breaks dropped.
func sameJSON(t *testing.T, value any, want string) {
	t.Helper()
	var compact bytes.Buffer
	if err := json.Compact(&compact, []byte(want)); err != nil {
		t.Fatalf("the wanted JSON does not read: %v", err)
	}
	if got := encoded(value); got != compact.String() {
		t.Errorf("JSON = %s\nwant %s", got, compact.String())
	}
}

func TestFormFieldsEncodeEachTypeInTheManifestsOrder(t *testing.T) {
	sameJSON(t, formSchema(t).FormFields(formValues()), `[
		{"key": "title", "type": "text", "label": "Title", "help": "Shown above the notes.", "default": "Notes",
			"value": "Field Notes", "source": "saved", "locked": false, "env": "APP_PLUGIN_NOTES__TITLE"},
		{"key": "per_page", "type": "count", "label": "Per page", "min": 1, "max": 50, "default": "10",
			"value": "25", "source": "environment", "locked": true, "env": "APP_PLUGIN_NOTES__PER_PAGE"},
		{"key": "open", "type": "flag", "label": "Open", "default": "false",
			"value": "false", "source": "default", "locked": false, "env": "APP_PLUGIN_NOTES__OPEN"},
		{"key": "keep_for", "type": "duration", "label": "Keep for", "default": "720h",
			"value": "720h", "source": "default", "locked": false, "env": "APP_PLUGIN_NOTES__KEEP_FOR"},
		{"key": "order", "type": "choice", "label": "Order", "options": ["newest", "oldest"], "default": "newest",
			"value": "newest", "source": "default", "locked": false, "env": "APP_PLUGIN_NOTES__ORDER"},
		{"key": "tags", "type": "lines", "label": "Tags", "default": "news",
			"value": "news", "source": "default", "locked": false, "env": "APP_PLUGIN_NOTES__TAGS"},
		{"key": "api_key", "type": "secret", "label": "API key",
			"set": true, "source": "environment", "locked": true, "env": "APP_PLUGIN_NOTES__API_KEY"}
	]`)
}

func TestFormFieldsShowEachValueInItsPlainForm(t *testing.T) {
	cases := []struct {
		kind  string
		value Value
		want  string
	}{
		{TypeCount, Value{Type: TypeCount, Count: 20}, "20"},
		{TypeCount, Value{Type: TypeCount, Count: -3}, "-3"},
		{TypeCount, Value{Type: TypeCount}, "0"},
		{TypeFlag, Value{Type: TypeFlag, Flag: true}, "true"},
		{TypeFlag, Value{Type: TypeFlag}, "false"},
		{TypeDuration, Value{Type: TypeDuration, Text: "2h", Duration: 2 * time.Hour}, "2h"},
		{TypeDuration, Value{Type: TypeDuration, Text: "1h30m", Duration: 90 * time.Minute}, "1h30m"},
		{TypeText, Value{Type: TypeText, Text: "Field Notes"}, "Field Notes"},
		{TypeText, Value{Type: TypeText}, ""},
		{TypeChoice, Value{Type: TypeChoice, Text: "oldest"}, "oldest"},
		{TypeLines, Value{Type: TypeLines, Lines: []string{"news", "events"}}, "news, events"},
		{TypeLines, Value{Type: TypeLines, Lines: []string{"news"}}, "news"},
		{TypeLines, Value{Type: TypeLines}, ""},
	}
	for _, c := range cases {
		got := oneField(Field{Key: "shown", Type: c.kind, Env: "APP_PLUGIN_NOTES__SHOWN"}, c.value).Value
		if got == nil {
			t.Errorf("%s %+v has no value, want %q", c.kind, c.value, c.want)
			continue
		}
		if *got != c.want {
			t.Errorf("%s %+v shows %q, want %q", c.kind, c.value, *got, c.want)
		}
	}
}

func TestFormFieldsKeepASavedEmptyValue(t *testing.T) {
	title := Field{Key: "title", Type: TypeText, Env: "APP_PLUGIN_NOTES__TITLE", Default: "Notes"}
	sameJSON(t, oneField(title, Value{Type: TypeText, Source: SourceSaved}), `{"key": "title", "type": "text",
		"default": "Notes", "value": "", "source": "saved", "locked": false, "env": "APP_PLUGIN_NOTES__TITLE"}`)
	tags := Field{Key: "tags", Type: TypeLines, Env: "APP_PLUGIN_NOTES__TAGS", Default: "news"}
	sameJSON(t, oneField(tags, Value{Type: TypeLines, Source: SourceSaved}), `{"key": "tags", "type": "lines",
		"default": "news", "value": "", "source": "saved", "locked": false, "env": "APP_PLUGIN_NOTES__TAGS"}`)
}

func TestFormFieldsGiveASecretOnlyWhetherItIsSet(t *testing.T) {
	key := Field{Key: "api_key", Type: TypeSecret, Label: "API key", Env: "APP_PLUGIN_NOTES__API_KEY"}
	sameJSON(t, oneField(key, Value{Type: TypeSecret, Source: SourceEnvironment, Secret: hide(knownSecret)}),
		`{"key": "api_key", "type": "secret", "label": "API key",
			"set": true, "source": "environment", "locked": true, "env": "APP_PLUGIN_NOTES__API_KEY"}`)
	sameJSON(t, oneField(key, Value{Type: TypeSecret, Source: SourceDefault}),
		`{"key": "api_key", "type": "secret", "label": "API key",
			"set": false, "source": "default", "locked": false, "env": "APP_PLUGIN_NOTES__API_KEY"}`)
}

func TestFormFieldsNeverGiveTheDefaultOfASecret(t *testing.T) {
	key := Field{Key: "api_key", Type: TypeSecret, Env: "APP_PLUGIN_NOTES__API_KEY", Default: knownSecret}
	got := oneField(key, Value{Type: TypeSecret, Source: SourceDefault})
	if got.Default != "" {
		t.Errorf("a secret with a default describes the default %q, want none", got.Default)
	}
	if got.Value != nil {
		t.Errorf("a secret describes the value %q, want none", *got.Value)
	}
}

func TestFormFieldsLockOnlyAValueFromTheEnvironment(t *testing.T) {
	cases := []struct {
		source string
		locked bool
	}{
		{SourceEnvironment, true},
		{SourceSaved, false},
		{SourceDefault, false},
		{"", false},
	}
	title := Field{Key: "title", Type: TypeText, Env: "APP_PLUGIN_NOTES__TITLE"}
	for _, c := range cases {
		got := oneField(title, Value{Type: TypeText, Source: c.source, Text: "Notes"})
		if got.Locked != c.locked {
			t.Errorf("a value from %q is locked %t, want %t", c.source, got.Locked, c.locked)
		}
		if got.Source != c.source {
			t.Errorf("a value from %q describes the source %q", c.source, got.Source)
		}
		if got.Env != "APP_PLUGIN_NOTES__TITLE" {
			t.Errorf("a value from %q names the variable %q, want APP_PLUGIN_NOTES__TITLE", c.source, got.Env)
		}
	}
}

func TestFormFieldsGiveTheBoundsOfACountOnlyWhenGiven(t *testing.T) {
	cases := []struct {
		min, max *int
		bounds   string
	}{
		{bound(1), bound(50), `"min": 1, "max": 50,`},
		{bound(0), nil, `"min": 0,`},
		{nil, bound(50), `"max": 50,`},
		{nil, nil, ``},
	}
	for _, c := range cases {
		seats := Field{Key: "seats", Type: TypeCount, Env: "APP_PLUGIN_NOTES__SEATS", Default: "1", Min: c.min, Max: c.max}
		sameJSON(t, oneField(seats, Value{Type: TypeCount, Source: SourceDefault, Count: 1}),
			`{"key": "seats", "type": "count", `+c.bounds+` "default": "1",
				"value": "1", "source": "default", "locked": false, "env": "APP_PLUGIN_NOTES__SEATS"}`)
	}
}

func TestFormFieldsGiveTheDefaultTrimmed(t *testing.T) {
	cases := []struct {
		field Field
		want  string
	}{
		{Field{Key: "title", Type: TypeText, Default: "  Notes \t"}, "Notes"},
		{Field{Key: "per_page", Type: TypeCount, Default: " 10 "}, "10"},
		{Field{Key: "tags", Type: TypeLines, Default: " news, events "}, "news, events"},
		{Field{Key: "title", Type: TypeText}, ""},
	}
	for _, c := range cases {
		if got := oneField(c.field, Value{Type: c.field.Type}).Default; got != c.want {
			t.Errorf("the default %q of %s is described as %q, want %q", c.field.Default, c.field.Key, got, c.want)
		}
	}
}

func TestFormFieldsOfASchemaWithNoFieldsEncodeAsAnEmptyList(t *testing.T) {
	sameJSON(t, Schema{ID: "notes"}.FormFields(Values{"title": {Type: TypeText, Text: "Notes"}}), `[]`)
}

func TestFormFieldsLeaveTheSchemaAlone(t *testing.T) {
	schema := formSchema(t)
	before := encoded(schema)
	fields := schema.FormFields(formValues())
	*fields[1].Min, *fields[1].Max, fields[4].Options[0] = 7, 8, "changed"
	if after := encoded(schema); after != before {
		t.Errorf("a change to the form fields changed the schema to %s, want %s", after, before)
	}
}

func TestFormFieldsNeverShowTheValueOfASecret(t *testing.T) {
	fields := formSchema(t).FormFields(formValues())
	for _, value := range []any{fields, &fields, fields[6], &fields[6]} {
		for _, out := range renderings(value) {
			if strings.Contains(out, knownSecret) {
				t.Errorf("a rendering of the form fields shows the secret: %q", out)
			}
		}
	}
}
