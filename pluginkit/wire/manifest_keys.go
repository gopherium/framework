// SPDX-License-Identifier: Apache-2.0

package wire

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strings"
)

// keyShape is the json names of one struct a manifest is read into, and the shape of each entry of a list it holds.
type keyShape struct {
	names   []string
	entries map[string]keyShape
}

// manifestShape is how a manifest is read, from its top level down to each entry of its lists.
var manifestShape = keyShape{names: typedKeys, entries: map[string]keyShape{
	"defines":  {names: []string{"hook", "kind", "fires"}},
	"jobs":     {names: []string{"name", "every"}},
	"settings": {names: []string{"key", "type", "default", "label", "help", "min", "max", "options", "of"}},
	"menu":     {names: []string{"label", "screen", "parent", "position", "capability", "icon"}},
}}

// keyPath is the steps from the top of a manifest down to one value, each a key or the index of a list entry.
type keyPath []any

// plainKey matches a key a place writes after a dot, any other key being quoted in brackets.
var plainKey = regexp.MustCompile(`^[\pL\pN_-]+$`)

// stepName returns how a place writes step, as [0] for an index, .key for a plain key and ["key"] for any other.
func stepName(step any) string {
	key, isKey := step.(string)
	switch {
	case !isKey:
		return fmt.Sprintf("[%d]", step)
	case plainKey.MatchString(key):
		return "." + key
	}
	return fmt.Sprintf("[%q]", key)
}

// reject returns problem as an error, after the place p names when it sits below the top level.
func (p keyPath) reject(problem string) error {
	if len(p) == 0 {
		return errors.New(problem)
	}
	var place strings.Builder
	for _, step := range p {
		place.WriteString(stepName(step))
	}
	return fmt.Errorf("%s: %s", strings.TrimPrefix(place.String(), "."), problem)
}

// check rejects key when the object at p holds it already, or when it is one of the names of s in another case.
func (s keyShape) check(p keyPath, key string, seen map[string]bool) error {
	if seen[key] {
		return p.reject(fmt.Sprintf("the key %q appears twice", key))
	}
	i := slices.IndexFunc(s.names, func(name string) bool { return strings.EqualFold(key, name) })
	if i >= 0 && s.names[i] != key {
		return p.reject(fmt.Sprintf("the key %q must be spelled %q", key, s.names[i]))
	}
	return nil
}

// keyWalk reads the tokens of a manifest json.Valid accepted, so no read of a token fails.
type keyWalk struct {
	dec *json.Decoder
}

// checkStrict rejects what encoding/json v2 rejects and v1 reads: bad text, a repeated name or a key in another case.
func checkStrict(data []byte) error {
	if !json.Valid(data) {
		return nil
	}
	if err := checkText(data); err != nil {
		return err
	}
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.UseNumber()
	return keyWalk{dec: dec}.value(nil, manifestShape, keyShape{})
}

// next returns the next token of the manifest.
func (w keyWalk) next() json.Token {
	token, _ := w.dec.Token()
	return token
}

// value checks the next value at p, an object in it read as asObject and each entry of a list in it as asEntry.
func (w keyWalk) value(p keyPath, asObject, asEntry keyShape) error {
	switch w.next() {
	case json.Delim('{'):
		return w.object(p, asObject)
	case json.Delim('['):
		return w.list(p, asEntry)
	}
	return nil
}

// object checks each key of an object at p read as s and the value under it, up to its closing brace.
func (w keyWalk) object(p keyPath, s keyShape) error {
	seen := map[string]bool{}
	for w.dec.More() {
		key, _ := w.next().(string)
		if err := s.check(p, key, seen); err != nil {
			return err
		}
		seen[key] = true
		if err := w.value(append(p, key), keyShape{}, s.entries[key]); err != nil {
			return err
		}
	}
	w.next()
	return nil
}

// list checks each entry of a list at p, an object entry read as s, up to its closing bracket.
func (w keyWalk) list(p keyPath, s keyShape) error {
	for i := 0; w.dec.More(); i++ {
		if err := w.value(append(p, i), s, keyShape{}); err != nil {
			return err
		}
	}
	w.next()
	return nil
}
