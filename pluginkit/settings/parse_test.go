// SPDX-License-Identifier: Apache-2.0

package settings

import (
	"math"
	"reflect"
	"strconv"
	"testing"
	"time"
)

// beyondEveryInt is a whole number past the int of every platform.
const beyondEveryInt = "99999999999999999999"

// bound returns n as a bound of a count.
func bound(n int) *int {
	return &n
}

// parseCase is one value a parser reads and the reading or the error it answers.
type parseCase struct {
	field Field
	value string
	want  Value
	err   string
}

// runParse reads each case's value with the parser of its field's type and compares what it answers.
func runParse(t *testing.T, cases []parseCase) {
	t.Helper()
	for _, c := range cases {
		parse, err := parserOf(c.field.Type)
		if err != nil {
			t.Fatalf("parserOf(%q) = %v", c.field.Type, err)
		}
		got, err := parse(c.field, c.value)
		if c.err != "" {
			if err == nil || err.Error() != c.err {
				t.Errorf("%s %q: error %v, want %q", c.field.Type, c.value, err, c.err)
			}
			continue
		}
		if err != nil {
			t.Errorf("%s %q: error %v, want none", c.field.Type, c.value, err)
			continue
		}
		if !sameValue(got, c.want) {
			t.Errorf("%s %q = %+v, want %+v", c.field.Type, c.value, got, c.want)
		}
	}
}

// sameValue reports whether two values hold the same reading, a secret compared by its text.
func sameValue(a, b Value) bool {
	if a.Secret.Reveal() != b.Secret.Reveal() {
		return false
	}
	a.Secret, b.Secret = Hidden{}, Hidden{}
	return reflect.DeepEqual(a, b)
}

func TestParseTextTrimsSurroundingSpaces(t *testing.T) {
	text := Field{Type: TypeText}
	runParse(t, []parseCase{
		{field: text, value: "Field Notes", want: Value{Type: TypeText, Text: "Field Notes"}},
		{field: text, value: "  Field Notes \t", want: Value{Type: TypeText, Text: "Field Notes"}},
		{field: text, value: "", want: Value{Type: TypeText}},
	})
}

func TestParseCountReadsAWholeNumber(t *testing.T) {
	open := Field{Type: TypeCount}
	runParse(t, []parseCase{
		{field: open, value: "25", want: Value{Type: TypeCount, Count: 25}},
		{field: open, value: " 25 ", want: Value{Type: TypeCount, Count: 25}},
		{field: open, value: "+7", want: Value{Type: TypeCount, Count: 7}},
		{field: open, value: "0", want: Value{Type: TypeCount}},
		{field: open, value: "-3", want: Value{Type: TypeCount, Count: -3}},
		{field: open, value: strconv.Itoa(math.MaxInt), want: Value{Type: TypeCount, Count: math.MaxInt}},
		{field: open, value: strconv.Itoa(math.MinInt), want: Value{Type: TypeCount, Count: math.MinInt}},
	})
}

func TestParseCountRejectsAnythingButAWholeNumber(t *testing.T) {
	open := Field{Type: TypeCount}
	runParse(t, []parseCase{
		{field: open, value: "banana", err: `must be a whole number, got "banana"`},
		{field: open, value: "2.5", err: `must be a whole number, got "2.5"`},
		{field: open, value: "1e3", err: `must be a whole number, got "1e3"`},
		{field: open, value: "", err: `must be a whole number, got ""`},
		{field: open, value: " 2 5 ", err: `must be a whole number, got "2 5"`},
	})
}

func TestParseCountKeepsWithinItsBounds(t *testing.T) {
	seats := Field{Type: TypeCount, Min: bound(1), Max: bound(50)}
	runParse(t, []parseCase{
		{field: seats, value: "1", want: Value{Type: TypeCount, Count: 1}},
		{field: seats, value: "50", want: Value{Type: TypeCount, Count: 50}},
		{field: seats, value: "60", err: `must stand at or below 50, got "60"`},
		{field: seats, value: "0", err: `must stand at or above 1, got "0"`},
		{field: seats, value: "-4", err: `must stand at or above 1, got "-4"`},
	})
}

func TestParseCountRejectsANumberPastTheInt(t *testing.T) {
	top, bottom := strconv.Itoa(math.MaxInt), strconv.Itoa(math.MinInt)
	runParse(t, []parseCase{
		{field: Field{Type: TypeCount}, value: beyondEveryInt,
			err: "must stand at or below " + top + `, got "` + beyondEveryInt + `"`},
		{field: Field{Type: TypeCount}, value: "-" + beyondEveryInt,
			err: "must stand at or above " + bottom + `, got "-` + beyondEveryInt + `"`},
		{field: Field{Type: TypeCount, Max: bound(50)}, value: beyondEveryInt,
			err: `must stand at or below 50, got "` + beyondEveryInt + `"`},
		{field: Field{Type: TypeCount, Min: bound(1)}, value: "-" + beyondEveryInt,
			err: `must stand at or above 1, got "-` + beyondEveryInt + `"`},
	})
}

func TestParseFlagReadsEveryWordParseBoolTakes(t *testing.T) {
	flag := Field{Type: TypeFlag}
	cases := make([]parseCase, 0, 12)
	for _, word := range []string{"true", "TRUE", "True", "t", "T", "1"} {
		cases = append(cases, parseCase{field: flag, value: word, want: Value{Type: TypeFlag, Flag: true}})
	}
	for _, word := range []string{"false", "FALSE", "False", "f", "F", " 0 "} {
		cases = append(cases, parseCase{field: flag, value: word, want: Value{Type: TypeFlag}})
	}
	runParse(t, cases)
}

func TestParseFlagRejectsAnotherWord(t *testing.T) {
	flag := Field{Type: TypeFlag}
	runParse(t, []parseCase{
		{field: flag, value: "maybe", err: `must be true or false, got "maybe"`},
		{field: flag, value: "yes", err: `must be true or false, got "yes"`},
	})
}

func TestParseDurationReadsADurationAboveZero(t *testing.T) {
	keep := Field{Type: TypeDuration}
	runParse(t, []parseCase{
		{field: keep, value: "30s", want: Value{Type: TypeDuration, Duration: 30 * time.Second}},
		{field: keep, value: " 720h ", want: Value{Type: TypeDuration, Duration: 720 * time.Hour}},
		{field: keep, value: "1h30m", want: Value{Type: TypeDuration, Duration: 90 * time.Minute}},
		{field: keep, value: "soon", err: `must be a duration like 30s, got "soon"`},
		{field: keep, value: "30", err: `must be a duration like 30s, got "30"`},
		{field: keep, value: "-1h", err: `must stand above zero, got "-1h"`},
		{field: keep, value: "0s", err: `must stand above zero, got "0s"`},
		{field: keep, value: "9999999999h", err: `must be a duration like 30s, got "9999999999h"`},
	})
}

func TestParseChoiceReadsOneOfItsOptions(t *testing.T) {
	order := Field{Type: TypeChoice, Options: []string{"newest", "oldest"}}
	runParse(t, []parseCase{
		{field: order, value: "oldest", want: Value{Type: TypeChoice, Text: "oldest"}},
		{field: order, value: " newest ", want: Value{Type: TypeChoice, Text: "newest"}},
		{field: order, value: "random", err: `must be one of newest, oldest, got "random"`},
		{field: order, value: "Newest", err: `must be one of newest, oldest, got "Newest"`},
	})
}

func TestParseLinesTrimsEachItemAndDropsTheEmptyOnes(t *testing.T) {
	tags := Field{Type: TypeLines}
	runParse(t, []parseCase{
		{field: tags, value: "news", want: Value{Type: TypeLines, Lines: []string{"news"}}},
		{field: tags, value: "news,, events", want: Value{Type: TypeLines, Lines: []string{"news", "events"}}},
		{field: tags, value: " a , b ,c,", want: Value{Type: TypeLines, Lines: []string{"a", "b", "c"}}},
		{field: tags, value: " , ,", want: Value{Type: TypeLines}},
		{field: tags, value: "", want: Value{Type: TypeLines}},
	})
}

func TestParseSecretTakesTheTextAsWritten(t *testing.T) {
	key := Field{Type: TypeSecret}
	runParse(t, []parseCase{
		{field: key, value: "key-for-example", want: Value{Type: TypeSecret, Secret: hide("key-for-example")}},
		{field: key, value: " key-for-example ", want: Value{Type: TypeSecret, Secret: hide(" key-for-example ")}},
		{field: key, value: "a,b", want: Value{Type: TypeSecret, Secret: hide("a,b")}},
		{field: key, value: "", want: Value{Type: TypeSecret}},
	})
}

func TestParseSecretFillsOnlyTheSecret(t *testing.T) {
	parse, _ := parserOf(TypeSecret)
	got, _ := parse(Field{Type: TypeSecret}, knownSecret)
	if got.Text != "" || got.Lines != nil {
		t.Errorf("a secret filled %+v beside its Secret", got)
	}
}

func TestParserOfRejectsAnUnknownType(t *testing.T) {
	for _, kind := range []string{"colour", "picker", "content", "", "Text"} {
		_, err := parserOf(kind)
		want := `type "` + kind + `" is not one of text, count, flag, duration, choice, secret, lines`
		if err == nil || err.Error() != want {
			t.Errorf("parserOf(%q) = %v, want %q", kind, err, want)
		}
	}
}

func TestParserOfKnowsTheSevenTypes(t *testing.T) {
	for _, kind := range []string{TypeText, TypeCount, TypeFlag, TypeDuration, TypeChoice, TypeSecret, TypeLines} {
		if _, err := parserOf(kind); err != nil {
			t.Errorf("parserOf(%q) = %v, want a parser", kind, err)
		}
	}
}
