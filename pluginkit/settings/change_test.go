// SPDX-License-Identifier: Apache-2.0

package settings

import (
	"errors"
	"fmt"
	"math"
	"reflect"
	"strconv"
	"strings"
	"testing"

	"github.com/gopherium/framework/pluginkit/wire"
)

// anotherSecret is a second secret text a change may hold beside the one the environment holds.
const anotherSecret = "another-key-for-example"

// changeSchema returns the checked schema of a plugin notes with a setting of each type and a count per set of bounds.
func changeSchema(t *testing.T) Schema {
	t.Helper()
	schema, err := Check("notes", []wire.Setting{
		{Key: "title", Type: TypeText, Default: "Notes"},
		{Key: "per_page", Type: TypeCount, Default: "10", Min: number(1), Max: number(50)},
		{Key: "total", Type: TypeCount, Default: "0"},
		{Key: "seats", Type: TypeCount, Default: "1", Min: number(1)},
		{Key: "slots", Type: TypeCount, Default: "1", Max: number(50)},
		{Key: "open", Type: TypeFlag, Default: "false"},
		{Key: "keep_for", Type: TypeDuration, Default: "720h"},
		{Key: "order", Type: TypeChoice, Default: "newest", Options: []string{"newest", "oldest"}},
		{Key: "tags", Type: TypeLines, Default: "news"},
		{Key: "api_key", Type: TypeSecret},
	}, appRules)
	if err != nil {
		t.Fatalf("Check = %v", err)
	}
	return schema
}

// changeCase is one change, the environment beside it and the problem CheckChange answers, nil for none.
type changeCase struct {
	change map[string]string
	env    map[string]string
	want   *ChangeError
}

// runChanges checks the change of each case against the change schema and compares the problem it answers.
func runChanges(t *testing.T, cases []changeCase) {
	t.Helper()
	schema := changeSchema(t)
	for _, c := range cases {
		checkChangeCase(t, schema, c)
	}
}

// checkChangeCase checks one case's change against schema and reports any difference from the case.
func checkChangeCase(t *testing.T, schema Schema, c changeCase) {
	t.Helper()
	err := schema.CheckChange(c.change, c.env)
	if c.want == nil {
		if err != nil {
			t.Errorf("CheckChange(%v, %v) = %v, want nil", c.change, c.env, err)
		}
		return
	}
	var got *ChangeError
	if !errors.As(err, &got) {
		t.Errorf("CheckChange(%v, %v) = %v, want the change error %s", c.change, c.env, err, described(c.want))
		return
	}
	if !reflect.DeepEqual(got, c.want) {
		t.Errorf("CheckChange(%v, %v) = %s, want %s", c.change, c.env, described(got), described(c.want))
	}
}

// described returns every field of a change error, the bounds by the numbers they point to.
func described(e *ChangeError) string {
	return fmt.Sprintf("{Kind:%q Key:%q Env:%q Reason:%q Min:%s Max:%s Options:%#v}",
		e.Kind, e.Key, e.Env, e.Reason, pointed(e.Min), pointed(e.Max), e.Options)
}

// pointed returns the number n points to as text, nil when n is nil.
func pointed(n *int) string {
	if n == nil {
		return "nil"
	}
	return strconv.Itoa(*n)
}

// invalidAs returns the change error of a value the field key of the change schema rejects for reason.
func invalidAs(key, reason string) *ChangeError {
	return &ChangeError{Kind: KindInvalid, Key: key, Env: Name(appRules.Prefix, "notes", key), Reason: reason}
}

// lockedBy returns the change error of the field key of the change schema, which its variable sets.
func lockedBy(key string) *ChangeError {
	return &ChangeError{Kind: KindLocked, Key: key, Env: Name(appRules.Prefix, "notes", key)}
}

// belowMin returns the change error of a value below floor, the bound the check of the field key used.
func belowMin(key string, floor int) *ChangeError {
	problem := invalidAs(key, ReasonBelowMin)
	problem.Min = &floor
	return problem
}

// aboveMax returns the change error of a value above ceiling, the bound the check of the field key used.
func aboveMax(key string, ceiling int) *ChangeError {
	problem := invalidAs(key, ReasonAboveMax)
	problem.Max = &ceiling
	return problem
}

// notOption returns the change error of a value that is none of the options of order.
func notOption() *ChangeError {
	problem := invalidAs("order", ReasonNotOption)
	problem.Options = []string{"newest", "oldest"}
	return problem
}

func TestKindsReadAsTheNamesAnApplicationSends(t *testing.T) {
	cases := []struct {
		got, want string
	}{
		{KindUnknown, "unknown"},
		{KindLocked, "locked"},
		{KindInvalid, "invalid"},
	}
	for _, c := range cases {
		if c.got != c.want {
			t.Errorf("kind %q, want %q", c.got, c.want)
		}
	}
}

func TestCheckChangeAcceptsAChangeToDeclaredSettings(t *testing.T) {
	top, bottom := strconv.Itoa(math.MaxInt), strconv.Itoa(math.MinInt)
	runChanges(t, []changeCase{
		{change: nil},
		{change: map[string]string{}},
		{change: map[string]string{
			"title": "Field Notes", "per_page": "25", "open": "true", "keep_for": "48h",
			"order": "oldest", "tags": "news, events",
		}},
		{change: map[string]string{"per_page": " 25 ", "open": " 1 ", "keep_for": " 2h ", "order": " newest "}},
		{change: map[string]string{"per_page": "1", "seats": "1", "slots": "50"}},
		{change: map[string]string{"per_page": "50", "total": top, "seats": top, "slots": bottom}},
		{change: map[string]string{"total": bottom, "open": "F", "keep_for": "1ns"}},
		{change: map[string]string{"title": "Field Notes"}, env: map[string]string{"APP_PLUGIN_OTHER__TITLE": "Other"}},
		{change: map[string]string{"order": "oldest"}, env: map[string]string{"APP_PLUGIN_NOTES__TITLE": "Garden Notes"}},
		{change: map[string]string{"title": "Field Notes"},
			env: map[string]string{"APP_PLUGIN_NOTES__API_KEY": knownSecret}},
	})
}

func TestCheckChangeAcceptsAnEmptyTextOrLines(t *testing.T) {
	runChanges(t, []changeCase{
		{change: map[string]string{"title": ""}},
		{change: map[string]string{"tags": ""}},
		{change: map[string]string{"title": "   ", "tags": " , ,"}},
	})
}

func TestCheckChangeRejectsAnEmptyValueOfATypeThatHasNone(t *testing.T) {
	runChanges(t, []changeCase{
		{change: map[string]string{"per_page": ""}, want: invalidAs("per_page", ReasonNotWhole)},
		{change: map[string]string{"total": "  "}, want: invalidAs("total", ReasonNotWhole)},
		{change: map[string]string{"open": ""}, want: invalidAs("open", ReasonNotFlag)},
		{change: map[string]string{"keep_for": ""}, want: invalidAs("keep_for", ReasonNotDuration)},
		{change: map[string]string{"order": ""}, want: notOption()},
	})
}

func TestCheckChangeRejectsAValueItsTypeRejectsWithItsReason(t *testing.T) {
	runChanges(t, []changeCase{
		{change: map[string]string{"per_page": "banana"}, want: invalidAs("per_page", ReasonNotWhole)},
		{change: map[string]string{"per_page": "2.5"}, want: invalidAs("per_page", ReasonNotWhole)},
		{change: map[string]string{"total": "1e3"}, want: invalidAs("total", ReasonNotWhole)},
		{change: map[string]string{"per_page": "0"}, want: belowMin("per_page", 1)},
		{change: map[string]string{"per_page": "60"}, want: aboveMax("per_page", 50)},
		{change: map[string]string{"open": "maybe"}, want: invalidAs("open", ReasonNotFlag)},
		{change: map[string]string{"keep_for": "soon"}, want: invalidAs("keep_for", ReasonNotDuration)},
		{change: map[string]string{"keep_for": "-1h"}, want: invalidAs("keep_for", ReasonNotPositive)},
		{change: map[string]string{"keep_for": "0s"}, want: invalidAs("keep_for", ReasonNotPositive)},
		{change: map[string]string{"order": "random"}, want: notOption()},
		{change: map[string]string{"order": "Newest"}, want: notOption()},
	})
}

func TestCheckChangeGivesOnlyTheBoundItsCheckUsed(t *testing.T) {
	above := strconv.FormatUint(uint64(math.MaxInt)+1, 10)
	below := "-" + strconv.FormatUint(uint64(math.MaxInt)+2, 10)
	runChanges(t, []changeCase{
		{change: map[string]string{"per_page": "0"}, want: belowMin("per_page", 1)},
		{change: map[string]string{"per_page": "-4"}, want: belowMin("per_page", 1)},
		{change: map[string]string{"per_page": "51"}, want: aboveMax("per_page", 50)},
		{change: map[string]string{"per_page": beyondEveryInt}, want: aboveMax("per_page", 50)},
		{change: map[string]string{"total": above}, want: aboveMax("total", math.MaxInt)},
		{change: map[string]string{"total": below}, want: belowMin("total", math.MinInt)},
		{change: map[string]string{"seats": "0"}, want: belowMin("seats", 1)},
		{change: map[string]string{"seats": beyondEveryInt}, want: aboveMax("seats", math.MaxInt)},
		{change: map[string]string{"slots": "51"}, want: aboveMax("slots", 50)},
		{change: map[string]string{"slots": "-" + beyondEveryInt}, want: belowMin("slots", math.MinInt)},
	})
}

func TestCheckChangeGivesTheOptionsOfAChoiceApartFromTheSchema(t *testing.T) {
	schema := changeSchema(t)
	var problem *ChangeError
	if err := schema.CheckChange(map[string]string{"order": "random"}, nil); !errors.As(err, &problem) {
		t.Fatalf("CheckChange = %v, want a change error", err)
	}
	problem.Options[0] = "changed"
	if got := schema.Fields[7].Options[0]; got != "newest" {
		t.Errorf("the first option of order became %q after the rejection changed", got)
	}
}

func TestCheckChangeRejectsAKeyTheSchemaDoesNotDeclare(t *testing.T) {
	runChanges(t, []changeCase{
		{change: map[string]string{"colour": "green"}, want: &ChangeError{Kind: KindUnknown, Key: "colour"}},
		{change: map[string]string{"Title": "Field Notes"}, want: &ChangeError{Kind: KindUnknown, Key: "Title"}},
		{change: map[string]string{"": "Field Notes"}, want: &ChangeError{Kind: KindUnknown, Key: ""}},
		{change: map[string]string{"title": "Field Notes", "colour": "green"},
			want: &ChangeError{Kind: KindUnknown, Key: "colour"}},
	})
}

func TestCheckChangeAnswersTheUndeclaredKeysFirstSortedByKey(t *testing.T) {
	change := map[string]string{
		"title": "Field Notes", "zebra": "stripes", "colour": "green", "per_page": "banana", "mood": "calm",
		"api_key": knownSecret, "apple": "red", "order": "random",
	}
	env := map[string]string{"APP_PLUGIN_NOTES__TITLE": "Garden Notes"}
	for range 32 {
		runChanges(t, []changeCase{{change: change, env: env, want: &ChangeError{Kind: KindUnknown, Key: "apple"}}})
	}
}

func TestCheckChangeAnswersTheDeclaredFieldsInTheManifestsOrder(t *testing.T) {
	runChanges(t, []changeCase{
		{change: map[string]string{"order": "random", "per_page": "banana"}, want: invalidAs("per_page", ReasonNotWhole)},
		{change: map[string]string{"tags": "", "keep_for": "soon", "open": "maybe"}, want: invalidAs("open", ReasonNotFlag)},
		{change: map[string]string{"api_key": knownSecret, "order": "random"}, want: notOption()},
		{change: map[string]string{"title": "Field Notes", "per_page": "banana"},
			env: map[string]string{"APP_PLUGIN_NOTES__TITLE": "Garden Notes"}, want: lockedBy("title")},
		{change: map[string]string{"per_page": "banana", "order": "oldest"},
			env: map[string]string{"APP_PLUGIN_NOTES__ORDER": "newest"}, want: invalidAs("per_page", ReasonNotWhole)},
		{change: map[string]string{"title": "Field Notes", "order": "random"}, want: notOption()},
	})
}

func TestCheckChangeRejectsAFieldTheEnvironmentSetsAsLocked(t *testing.T) {
	runChanges(t, []changeCase{
		{change: map[string]string{"title": "Field Notes"},
			env: map[string]string{"APP_PLUGIN_NOTES__TITLE": "Garden Notes"}, want: lockedBy("title")},
		{change: map[string]string{"title": "Garden Notes"},
			env: map[string]string{"APP_PLUGIN_NOTES__TITLE": " Garden Notes "}, want: lockedBy("title")},
		{change: map[string]string{"per_page": "25"},
			env: map[string]string{"APP_PLUGIN_NOTES__PER_PAGE": "25"}, want: lockedBy("per_page")},
		{change: map[string]string{"per_page": "banana"},
			env: map[string]string{"APP_PLUGIN_NOTES__PER_PAGE": "25"}, want: lockedBy("per_page")},
		{change: map[string]string{"tags": ""},
			env: map[string]string{"APP_PLUGIN_NOTES__TAGS": ","}, want: lockedBy("tags")},
	})
}

func TestCheckChangeLetsABlankVariableLockNothing(t *testing.T) {
	cases := make([]changeCase, 0, 3)
	for _, blank := range []string{"", "  ", "\t\n"} {
		cases = append(cases, changeCase{
			change: map[string]string{"title": "Field Notes"},
			env:    map[string]string{"APP_PLUGIN_NOTES__TITLE": blank},
		})
	}
	runChanges(t, cases)
}

func TestCheckChangeRejectsASecretBecauseItComesFromTheEnvironmentOnly(t *testing.T) {
	runChanges(t, []changeCase{
		{change: map[string]string{"api_key": knownSecret}, want: invalidAs("api_key", ReasonEnvironmentOnly)},
		{change: map[string]string{"api_key": ""}, want: invalidAs("api_key", ReasonEnvironmentOnly)},
		{change: map[string]string{"api_key": knownSecret},
			env: map[string]string{"APP_PLUGIN_NOTES__API_KEY": "  "}, want: invalidAs("api_key", ReasonEnvironmentOnly)},
	})
}

func TestCheckChangeRejectsASecretTheEnvironmentSetsAsLocked(t *testing.T) {
	runChanges(t, []changeCase{
		{change: map[string]string{"api_key": anotherSecret},
			env: map[string]string{"APP_PLUGIN_NOTES__API_KEY": knownSecret}, want: lockedBy("api_key")},
	})
}

func TestChangeErrorSaysTheKeyAndItsProblem(t *testing.T) {
	cases := []struct {
		problem *ChangeError
		want    string
	}{
		{&ChangeError{Kind: KindUnknown, Key: "colour"}, "colour: no setting has this key"},
		{lockedBy("title"), "title: locked by APP_PLUGIN_NOTES__TITLE"},
		{invalidAs("per_page", ReasonNotWhole), "per_page: value rejected as not_whole"},
		{belowMin("per_page", 1), "per_page: value rejected as below_min"},
		{invalidAs("api_key", ReasonEnvironmentOnly), "api_key: value rejected as environment_only"},
	}
	for _, c := range cases {
		if got := c.problem.Error(); got != c.want {
			t.Errorf("Error() = %q, want %q", got, c.want)
		}
	}
}

func TestCheckChangeNeverShowsTheValueOfASecret(t *testing.T) {
	schema := changeSchema(t)
	cases := []struct {
		change, env map[string]string
	}{
		{change: map[string]string{"api_key": knownSecret}},
		{change: map[string]string{"api_key": anotherSecret},
			env: map[string]string{"APP_PLUGIN_NOTES__API_KEY": knownSecret}},
		{change: map[string]string{"api_key": knownSecret, "per_page": "banana"}},
		{change: map[string]string{"api_key": knownSecret, "colour": "green"}},
	}
	for _, c := range cases {
		err := schema.CheckChange(c.change, c.env)
		if err == nil {
			t.Fatalf("CheckChange(%v) = nil, want a rejection", c.change)
		}
		for _, out := range renderings(err) {
			if strings.Contains(out, knownSecret) || strings.Contains(out, anotherSecret) {
				t.Errorf("a rendering of the rejection of %v shows a secret: %q", c.change, out)
			}
		}
	}
}

func TestCheckChangeRejectsAFieldOfAnUnknownTypeByItsKey(t *testing.T) {
	schema := Schema{ID: "notes", Fields: []Field{{Key: "mood", Type: "colour", Env: "APP_PLUGIN_NOTES__MOOD"}}}
	err := schema.CheckChange(map[string]string{"mood": "blue"}, nil)
	want := `mood: type "colour" is not one of text, count, flag, duration, choice, secret, lines`
	if err == nil || err.Error() != want {
		t.Fatalf("CheckChange = %v, want %q", err, want)
	}
	var problem *ChangeError
	if errors.As(err, &problem) {
		t.Errorf("CheckChange answered the change error %s for a schema it cannot read", described(problem))
	}
}

func TestCheckChangeOfASchemaWithNoFieldsRejectsEveryKey(t *testing.T) {
	schema := Schema{ID: "notes"}
	if err := schema.CheckChange(nil, nil); err != nil {
		t.Errorf("CheckChange(nil) = %v, want nil", err)
	}
	var problem *ChangeError
	err := schema.CheckChange(map[string]string{"title": "Field Notes"}, nil)
	if !errors.As(err, &problem) || !reflect.DeepEqual(problem, &ChangeError{Kind: KindUnknown, Key: "title"}) {
		t.Errorf("CheckChange = %v, want the key title rejected as unknown", err)
	}
}
