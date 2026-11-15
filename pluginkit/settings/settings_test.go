// SPDX-License-Identifier: Apache-2.0

package settings

import (
	"bytes"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"testing"
)

// knownSecret is the text each test hides and then looks for in every print.
const knownSecret = "key-for-example"

// holder keeps a secret in an unexported field, where fmt cannot call its methods.
type holder struct {
	secret Hidden
}

// verbs are the fmt verbs a print of a secret may use.
var verbs = []string{"%v", "%+v", "%#v", "%s", "%q", "%x", "%X", "%d", "%T", "%p"}

// prints returns value through every verb in verbs.
func prints(value any) []string {
	out := make([]string, 0, len(verbs))
	for _, verb := range verbs {
		out = append(out, fmt.Sprintf(verb, value))
	}
	return out
}

// logged returns the lines slog's text and JSON handlers write for value under the key secret.
func logged(value any) []string {
	var text, structured bytes.Buffer
	slog.New(slog.NewTextHandler(&text, nil)).Info("read", "secret", value)
	slog.New(slog.NewJSONHandler(&structured, nil)).Info("read", "secret", value)
	return []string{text.String(), structured.String()}
}

// encoded returns value encoded by encoding/json, the error text when it fails.
func encoded(value any) string {
	data, err := json.Marshal(value)
	if err != nil {
		return err.Error()
	}
	return string(data)
}

// renderings returns every print, log line and encoding of value.
func renderings(value any) []string {
	return append(append(prints(value), logged(value)...), encoded(value))
}

func TestHiddenRevealsItsText(t *testing.T) {
	if got := hide(knownSecret).Reveal(); got != knownSecret {
		t.Errorf("Reveal() = %q, want %q", got, knownSecret)
	}
}

func TestHiddenTakesTheTextAsWritten(t *testing.T) {
	if got := hide("  spaced key ").Reveal(); got != "  spaced key " {
		t.Errorf("Reveal() = %q, want the text untrimmed", got)
	}
}

func TestHiddenWithNoValueRevealsNothingAndReadsNotSet(t *testing.T) {
	for _, h := range []Hidden{{}, hide("")} {
		if got := h.Reveal(); got != "" {
			t.Errorf("Reveal() = %q, want empty", got)
		}
		if got := h.String(); got != "not set" {
			t.Errorf("String() = %q, want not set", got)
		}
	}
}

func TestHiddenPrintsAsSetThroughEachPrinter(t *testing.T) {
	h := hide(knownSecret)
	cases := []struct {
		name, got, want string
	}{
		{"GoString", h.GoString(), "set"},
		{"LogValue", h.LogValue().String(), "set"},
		{"json", encoded(h), `"set"`},
		{"map", fmt.Sprintf(verbs[0], map[string]Hidden{"api_key": h}), "map[api_key:set]"},
		{"pointer", fmt.Sprintf(verbs[0], &h), "set"},
		{"slice", fmt.Sprintf(verbs[1], []Hidden{h}), "[set]"},
	}
	for _, c := range cases {
		if c.got != c.want {
			t.Errorf("%s = %q, want %q", c.name, c.got, c.want)
		}
	}
}

func TestHiddenPrintsAsSetThroughEachStringVerb(t *testing.T) {
	want := map[string]string{"%v": "set", "%+v": "set", "%#v": "set", "%s": "set", "%q": `"set"`, "%x": "736574"}
	for verb, text := range want {
		if got := fmt.Sprintf(verb, hide(knownSecret)); got != text {
			t.Errorf("%s = %q, want %q", verb, got, text)
		}
	}
}

func TestHiddenWithNoValuePrintsAsNotSet(t *testing.T) {
	h := Hidden{}
	for _, verb := range []string{"%v", "%#v", "%s"} {
		if got := fmt.Sprintf(verb, h); got != "not set" {
			t.Errorf("%s = %q, want not set", verb, got)
		}
	}
	if got := h.LogValue().String(); got != "not set" {
		t.Errorf("LogValue() = %q, want not set", got)
	}
	if got := encoded(h); got != `"not set"` {
		t.Errorf("json = %s, want \"not set\"", got)
	}
}

func TestHiddenLogsAsSetThroughBothHandlers(t *testing.T) {
	lines := logged(hide(knownSecret))
	if !strings.Contains(lines[0], "secret=set") {
		t.Errorf("text line %q does not hold secret=set", lines[0])
	}
	if !strings.Contains(lines[1], `"secret":"set"`) {
		t.Errorf("JSON line %q does not hold \"secret\":\"set\"", lines[1])
	}
}

func TestHiddenNeverShowsItsTextWhereverItSits(t *testing.T) {
	h := hide(knownSecret)
	places := map[string]any{
		"alone":              h,
		"pointer":            &h,
		"map":                map[string]Hidden{"api_key": h},
		"slice":              []Hidden{h},
		"unexported field":   holder{secret: h},
		"unexported pointer": &holder{secret: h},
		"value":              Value{Type: TypeSecret, Secret: h},
		"values":             Values{"api_key": {Type: TypeSecret, Secret: h}},
		"any map":            map[string]any{"api_key": h},
	}
	for place, value := range places {
		for _, out := range renderings(value) {
			if strings.Contains(out, knownSecret) {
				t.Errorf("%s shows the secret in %q", place, out)
			}
		}
	}
}
