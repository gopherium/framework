// SPDX-License-Identifier: Apache-2.0

package settings

import (
	"math"
	"reflect"
	"strings"
	"testing"

	"github.com/gopherium/framework/pluginkit/wire"
)

// appRules are the rules of an application that names its variables under APP_PLUGIN_ and reserves job_ and db_.
var appRules = Rules{Prefix: "APP_PLUGIN_", Reserved: []string{"job_", "db_"}}

// number returns n as a bound of a declared setting.
func number(n float64) *float64 {
	return &n
}

// everyType declares one setting of each of the seven types.
func everyType() []wire.Setting {
	return []wire.Setting{
		{Key: "title", Type: TypeText, Default: "Notes", Label: "Title", Help: "The title readers see"},
		{Key: "per_page", Type: TypeCount, Default: "10", Min: number(1), Max: number(50)},
		{Key: "open", Type: TypeFlag},
		{Key: "keep_for", Type: TypeDuration, Default: "720h"},
		{Key: "order", Type: TypeChoice, Default: "newest", Options: []string{"newest", "oldest"}},
		{Key: "tags", Type: TypeLines},
		{Key: "api_key", Type: TypeSecret},
	}
}

// rejection is one declaration Check rejects and the reason it answers.
type rejection struct {
	name     string
	declared []wire.Setting
	want     string
}

// rejections lists every rule a declaration can break, as the rows of the design name them.
var rejections = []rejection{
	{"unknown type", []wire.Setting{{Key: "mood", Type: "colour"}},
		`settings[0] mood: type "colour" is not one of text, count, flag, duration, choice, secret, lines`},
	{"key shape", []wire.Setting{{Key: "Max__Seats", Type: TypeText}},
		"settings[0] Max__Seats: a key is lowercase words joined by one underscore"},
	{"key starts with an underscore", []wire.Setting{{Key: "_title", Type: TypeText}},
		"settings[0] _title: a key is lowercase words joined by one underscore"},
	{"reserved job_", []wire.Setting{{Key: "title", Type: TypeText}, {Key: "job_every", Type: TypeText}},
		"settings[1] job_every: a key must not start with job_ or db_"},
	{"reserved db_", []wire.Setting{{Key: "db_conns", Type: TypeText}},
		"settings[0] db_conns: a key must not start with job_ or db_"},
	{"repeated key", []wire.Setting{{Key: "per_page", Type: TypeCount, Default: "1"}, {Key: "title", Type: TypeText},
		{Key: "title", Type: TypeText}}, "settings[2] title: the key is declared twice"},
	{"count with no default", []wire.Setting{{Key: "max_seats", Type: TypeCount}},
		"settings[0] max_seats: a count needs a default"},
	{"count with a blank default", []wire.Setting{{Key: "max_seats", Type: TypeCount, Default: "  "}},
		"settings[0] max_seats: a count needs a default"},
	{"bad count default", []wire.Setting{{Key: "max_seats", Type: TypeCount, Default: "lots"}},
		`settings[0] max_seats: the default must be a whole number, got "lots"`},
	{"count default above max", []wire.Setting{{Key: "max_seats", Type: TypeCount, Default: "60", Min: number(1),
		Max: number(50)}}, `settings[0] max_seats: the default must stand at or below 50, got "60"`},
	{"count default below min", []wire.Setting{{Key: "max_seats", Type: TypeCount, Default: "0", Min: number(1)}},
		`settings[0] max_seats: the default must stand at or above 1, got "0"`},
	{"bounds upside down", []wire.Setting{{Key: "max_seats", Type: TypeCount, Default: "7", Min: number(10),
		Max: number(5)}}, "settings[0] max_seats: min 10 stands above max 5"},
	{"min not whole", []wire.Setting{{Key: "max_seats", Type: TypeCount, Default: "4", Min: number(1.5)}},
		"settings[0] max_seats: min 1.5 is not a whole number"},
	{"max not whole", []wire.Setting{{Key: "max_seats", Type: TypeCount, Default: "4", Max: number(9.25)}},
		"settings[0] max_seats: max 9.25 is not a whole number"},
	{"min past every int", []wire.Setting{{Key: "max_seats", Type: TypeCount, Default: "4", Min: number(1e20)}},
		"settings[0] max_seats: min 100000000000000000000 does not fit in a count"},
	{"max below every int", []wire.Setting{{Key: "max_seats", Type: TypeCount, Default: "4", Max: number(-1e20)}},
		"settings[0] max_seats: max -100000000000000000000 does not fit in a count"},
	{"max at two to the 63", []wire.Setting{{Key: "max_seats", Type: TypeCount, Default: "4",
		Max: number(math.Exp2(63))}}, "settings[0] max_seats: max 9223372036854776000 does not fit in a count"},
	{"max on a text", []wire.Setting{{Key: "title", Type: TypeText, Max: number(80)}},
		"settings[0] title: min and max apply only to a count"},
	{"min on a lines", []wire.Setting{{Key: "tags", Type: TypeLines, Min: number(1)}},
		"settings[0] tags: min and max apply only to a count"},
	{"max on a secret", []wire.Setting{{Key: "api_key", Type: TypeSecret, Max: number(8)}},
		"settings[0] api_key: min and max apply only to a count"},
	{"options on a text", []wire.Setting{{Key: "title", Type: TypeText, Options: []string{"a", "b"}}},
		"settings[0] title: options apply only to a choice"},
	{"choice with no options", []wire.Setting{{Key: "order", Type: TypeChoice}},
		"settings[0] order: a choice needs options"},
	{"choice option with a space at its end", []wire.Setting{{Key: "order", Type: TypeChoice, Default: "oldest",
		Options: []string{"newest ", "oldest"}}},
		`settings[0] order: an option must not start or end with a space, got "newest "`},
	{"choice option with a space at its start", []wire.Setting{{Key: "order", Type: TypeChoice, Default: "newest",
		Options: []string{"newest", " oldest"}}},
		`settings[0] order: an option must not start or end with a space, got " oldest"`},
	{"choice with no default", []wire.Setting{{Key: "order", Type: TypeChoice, Options: []string{"newest", "oldest"}}},
		"settings[0] order: a choice needs a default"},
	{"choice default not an option", []wire.Setting{{Key: "order", Type: TypeChoice, Default: "random",
		Options: []string{"newest", "oldest"}}},
		`settings[0] order: the default must be one of newest, oldest, got "random"`},
	{"duration with no default", []wire.Setting{{Key: "keep_for", Type: TypeDuration}},
		"settings[0] keep_for: a duration needs a default"},
	{"bad duration default", []wire.Setting{{Key: "keep_for", Type: TypeDuration, Default: "soon"}},
		`settings[0] keep_for: the default must be a duration like 30s, got "soon"`},
	{"duration default not above zero", []wire.Setting{{Key: "keep_for", Type: TypeDuration, Default: "0s"}},
		`settings[0] keep_for: the default must stand above zero, got "0s"`},
	{"bad flag default", []wire.Setting{{Key: "open", Type: TypeFlag, Default: "maybe"}},
		`settings[0] open: the default must be true or false, got "maybe"`},
	{"default on a secret", []wire.Setting{{Key: "api_key", Type: TypeSecret, Default: "key-for-example"}},
		"settings[0] api_key: a secret takes no default, because plugin.json is public"},
	{"blank default on a secret", []wire.Setting{{Key: "api_key", Type: TypeSecret, Default: "   "}},
		"settings[0] api_key: a secret takes no default, because plugin.json is public"},
	{"of on a text", []wire.Setting{{Key: "title", Type: TypeText, Of: "post"}},
		"settings[0] title: a text setting takes no of field"},
	{"of on a count", []wire.Setting{{Key: "per_page", Type: TypeCount, Default: "1", Of: "post"}},
		"settings[0] per_page: a count setting takes no of field"},
	{"of on a lines", []wire.Setting{{Key: "tags", Type: TypeLines, Of: "post"}},
		"settings[0] tags: a lines setting takes no of field"},
}

func TestCheckRejectsEachDeclarationOutsideTheRules(t *testing.T) {
	for _, c := range rejections {
		schema, err := Check("notes", c.declared, appRules)
		if err == nil || err.Error() != c.want {
			t.Errorf("%s: Check = %v, want %q", c.name, err, c.want)
		}
		if !reflect.DeepEqual(schema, Schema{}) {
			t.Errorf("%s: Check answered %+v beside its error", c.name, schema)
		}
	}
}

func TestCheckNeverQuotesTheDefaultOfASecret(t *testing.T) {
	for _, secret := range []string{"key-for-example", " spaced ", "sk_live_example"} {
		_, err := Check("notes", []wire.Setting{{Key: "api_key", Type: TypeSecret, Default: secret}}, appRules)
		if err == nil || strings.Contains(err.Error(), secret) {
			t.Errorf("Check of a secret default %q = %v, want an error that does not quote it", secret, err)
		}
	}
}

func TestCheckAnswersEachSettingReadyToResolve(t *testing.T) {
	schema, err := Check("notes", everyType(), appRules)
	if err != nil {
		t.Fatalf("Check = %v, want nil", err)
	}
	one, fifty := 1, 50
	want := Schema{ID: "notes", Fields: []Field{
		{Key: "title", Type: TypeText, Label: "Title", Help: "The title readers see", Env: "APP_PLUGIN_NOTES__TITLE",
			Default: "Notes"},
		{Key: "per_page", Type: TypeCount, Env: "APP_PLUGIN_NOTES__PER_PAGE", Default: "10", Min: &one, Max: &fifty},
		{Key: "open", Type: TypeFlag, Env: "APP_PLUGIN_NOTES__OPEN"},
		{Key: "keep_for", Type: TypeDuration, Env: "APP_PLUGIN_NOTES__KEEP_FOR", Default: "720h"},
		{Key: "order", Type: TypeChoice, Env: "APP_PLUGIN_NOTES__ORDER", Default: "newest",
			Options: []string{"newest", "oldest"}},
		{Key: "tags", Type: TypeLines, Env: "APP_PLUGIN_NOTES__TAGS"},
		{Key: "api_key", Type: TypeSecret, Env: "APP_PLUGIN_NOTES__API_KEY"},
	}}
	if !reflect.DeepEqual(schema, want) {
		t.Errorf("Check = %+v, want %+v", schema, want)
	}
}

func TestCheckKeepsTheLabelAndHelpOfEachSetting(t *testing.T) {
	declared := everyType()
	for i := range declared {
		declared[i].Label, declared[i].Help = "Label of "+declared[i].Key, "Help for "+declared[i].Key
	}
	schema, err := Check("notes", declared, appRules)
	if err != nil {
		t.Fatalf("Check = %v, want nil", err)
	}
	if len(schema.Fields) != len(declared) {
		t.Fatalf("Check answered %d fields, want %d", len(schema.Fields), len(declared))
	}
	for i, field := range schema.Fields {
		if field.Label != declared[i].Label {
			t.Errorf("%s: label %q, want %q", field.Key, field.Label, declared[i].Label)
		}
		if field.Help != declared[i].Help {
			t.Errorf("%s: help %q, want %q", field.Key, field.Help, declared[i].Help)
		}
	}
}

func TestCheckLeavesTheLabelAndHelpEmptyWhenTheManifestGivesNone(t *testing.T) {
	schema, err := Check("notes", []wire.Setting{{Key: "title", Type: TypeText}}, appRules)
	if err != nil {
		t.Fatalf("Check = %v, want nil", err)
	}
	if field := schema.Fields[0]; field.Label != "" || field.Help != "" {
		t.Errorf("a setting with no label or help answered %q and %q", field.Label, field.Help)
	}
}

func TestCheckTakesTheDefaultsEachTypeAccepts(t *testing.T) {
	declared := []wire.Setting{
		{Key: "open", Type: TypeFlag, Default: "true"},
		{Key: "shut", Type: TypeFlag, Default: " F "},
		{Key: "tags", Type: TypeLines, Default: "news, events"},
		{Key: "title", Type: TypeText, Default: "  Notes  "},
		{Key: "quiet", Type: TypeFlag, Default: "  "},
		{Key: "floor", Type: TypeCount, Default: "-5"},
		{Key: "lowest", Type: TypeCount, Default: "0", Min: number(float64(math.MinInt))},
		{Key: "ceiling", Type: TypeCount, Default: "5", Min: number(5), Max: number(5)},
		{Key: "keep_for", Type: TypeDuration, Default: " 1h "},
		{Key: "order", Type: TypeChoice, Default: " oldest ", Options: []string{"newest", "oldest"}},
	}
	if _, err := Check("notes", declared, appRules); err != nil {
		t.Errorf("Check = %v, want nil", err)
	}
}

func TestCheckAnswersTheFirstProblemInTheManifestsOrder(t *testing.T) {
	declared := []wire.Setting{
		{Key: "title", Type: TypeText},
		{Key: "max_seats", Type: TypeCount},
		{Key: "mood", Type: "colour"},
	}
	_, err := Check("notes", declared, appRules)
	if want := "settings[1] max_seats: a count needs a default"; err == nil || err.Error() != want {
		t.Errorf("Check = %v, want %q", err, want)
	}
}

func TestCheckRejectsTheIDOfAPluginWithSettingsBeforeItsSettings(t *testing.T) {
	for _, id := range []string{"notes--pro", "notes-"} {
		_, err := Check(id, []wire.Setting{{Key: "Bad", Type: "colour"}}, appRules)
		want := "the id " + id + " must not hold two hyphens in a row or end with one when the plugin has settings"
		if err == nil || err.Error() != want {
			t.Errorf("Check(%q) = %v, want %q", id, err, want)
		}
	}
}

func TestCheckKeepsAnyIDForAPluginWithNoSettings(t *testing.T) {
	for _, declared := range [][]wire.Setting{nil, {}} {
		schema, err := Check("notes--pro", declared, appRules)
		if err != nil {
			t.Errorf("Check = %v, want nil", err)
		}
		if !reflect.DeepEqual(schema, Schema{ID: "notes--pro"}) {
			t.Errorf("Check = %+v, want a schema with no fields", schema)
		}
	}
}

func TestCheckReservesNothingWhenTheRulesReserveNothing(t *testing.T) {
	declared := []wire.Setting{{Key: "job_every", Type: TypeText}, {Key: "db_conns", Type: TypeText}}
	if _, err := Check("notes", declared, Rules{Prefix: "APP_PLUGIN_"}); err != nil {
		t.Errorf("Check = %v, want nil", err)
	}
}

func TestCheckKeepsTheOptionsApartFromTheDeclaration(t *testing.T) {
	declared := []wire.Setting{{Key: "order", Type: TypeChoice, Default: "newest", Options: []string{"newest", "oldest"}}}
	schema, err := Check("notes", declared, appRules)
	if err != nil {
		t.Fatalf("Check = %v, want nil", err)
	}
	declared[0].Options[0] = "changed"
	if got := schema.Fields[0].Options[0]; got != "newest" {
		t.Errorf("the field's first option became %q after the declaration changed", got)
	}
}

func TestCheckRejectsABoundThatIsNotANumber(t *testing.T) {
	declared := []wire.Setting{{Key: "max_seats", Type: TypeCount, Default: "4", Min: number(math.NaN())}}
	_, err := Check("notes", declared, appRules)
	if want := "settings[0] max_seats: min NaN is not a whole number"; err == nil || err.Error() != want {
		t.Errorf("Check = %v, want %q", err, want)
	}
	declared[0].Min = number(math.Inf(1))
	_, err = Check("notes", declared, appRules)
	if want := "settings[0] max_seats: min +Inf does not fit in a count"; err == nil || err.Error() != want {
		t.Errorf("Check = %v, want %q", err, want)
	}
}
