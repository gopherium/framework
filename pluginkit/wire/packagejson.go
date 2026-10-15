// SPDX-License-Identifier: Apache-2.0

package wire

import (
	"bytes"
	"encoding/json"
	"encoding/json/jsontext"
	"errors"
	"fmt"
	"io"
	"os"
	"slices"
	"strings"
)

// The names and the dependency value that syncPackageJSON writes.
const (
	dependenciesKey     = "dependencies"
	recordKey           = "pluginwire"
	workspaceDependency = `"workspace:*"`
)

// errRecord reports a pluginwire record that is not an array of strings.
var errRecord = errors.New("pluginwire.dependencies is not an array of strings")

// member is one name and raw value of a JSON object.
type member struct {
	key   jsontext.Token
	value jsontext.Value
}

// name returns the unquoted name of m.
func (m member) name() string {
	return m.key.String()
}

// frontendPackages returns the sorted unique frontend package names of manifests.
func frontendPackages(manifests []manifest) []string {
	names := make([]string, 0, len(manifests))
	for _, m := range withFrontend(manifests) {
		names = append(names, m.Frontend)
	}
	slices.Sort(names)
	return slices.Compact(names)
}

// syncedPackageJSON reads the package.json at path and syncs it to the frontends of manifests.
func syncedPackageJSON(path string, manifests []manifest) ([]byte, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("pluginwire: %s: %w", path, err)
	}
	synced, err := syncPackageJSON(data, frontendPackages(manifests))
	if err != nil {
		return nil, fmt.Errorf("pluginwire: %s: %w", path, err)
	}
	return synced, nil
}

// syncPackageJSON returns data with every name in want as a workspace dependency recorded under pluginwire.
func syncPackageJSON(data []byte, want []string) ([]byte, error) {
	members, err := readObject(data, "the top level")
	if err != nil {
		return nil, err
	}
	deps, err := readDependencies(members)
	if err != nil {
		return nil, err
	}
	recorded, err := readRecord(members)
	if err != nil {
		return nil, err
	}
	if _, found := find(members, recordKey); len(want) == 0 && !found {
		return data, nil
	}
	return render(layout(members, want), dependencies(deps, recorded, want), want)
}

// openObject reads the opening brace from dec, naming the value what when it is empty or not an object.
func openObject(dec *jsontext.Decoder, what string) error {
	open, err := dec.ReadToken()
	if errors.Is(err, io.EOF) {
		return fmt.Errorf("%s is empty", what)
	}
	if err != nil {
		return err
	}
	if open.Kind() != '{' {
		return fmt.Errorf("%s is not an object", what)
	}
	return nil
}

// readObject returns the members of the one JSON object in data, naming it what when it is empty or not an object.
func readObject(data []byte, what string) ([]member, error) {
	dec := jsontext.NewDecoder(bytes.NewReader(data))
	if err := openObject(dec, what); err != nil {
		return nil, err
	}
	var members []member
	for dec.PeekKind() == '"' {
		key, err := dec.ReadToken()
		if err != nil {
			return nil, err
		}
		key = key.Clone()
		value, err := dec.ReadValue()
		if err != nil {
			return nil, err
		}
		members = append(members, member{key: key, value: value.Clone()})
	}
	if _, err := dec.ReadToken(); err != nil {
		return nil, err
	}
	if _, err := dec.ReadToken(); !errors.Is(err, io.EOF) {
		return nil, errors.New("data after the closing brace")
	}
	return members, nil
}

// find returns the member of members called name.
func find(members []member, name string) (member, bool) {
	i := slices.IndexFunc(members, func(m member) bool { return m.name() == name })
	if i < 0 {
		return member{}, false
	}
	return members[i], true
}

// readDependencies returns the members of the dependencies object in members, each a string.
func readDependencies(members []member) ([]member, error) {
	m, found := find(members, dependenciesKey)
	if !found {
		return nil, nil
	}
	deps, err := readObject(m.value, dependenciesKey)
	if err != nil {
		return nil, err
	}
	for _, dep := range deps {
		if dep.value.Kind() != '"' {
			return nil, fmt.Errorf("dependency %q is not a string", dep.name())
		}
	}
	return deps, nil
}

// readRecord returns the names in the pluginwire record of members.
func readRecord(members []member) ([]string, error) {
	m, found := find(members, recordKey)
	if !found {
		return nil, nil
	}
	record, err := readObject(m.value, recordKey)
	if err != nil {
		return nil, err
	}
	names, found := find(record, dependenciesKey)
	if !found {
		return nil, errRecord
	}
	return readStrings(names.value)
}

// readStrings returns the strings of the JSON array in value.
func readStrings(value jsontext.Value) ([]string, error) {
	var items []*string
	if err := json.Unmarshal(value, &items); err != nil || items == nil {
		return nil, errRecord
	}
	names := make([]string, 0, len(items))
	for _, item := range items {
		if item == nil {
			return nil, errRecord
		}
		names = append(names, *item)
	}
	return names, nil
}

// layout returns the top-level members to write, without the record when want is empty and with missing keys last.
func layout(members []member, want []string) []member {
	if len(want) == 0 {
		return slices.DeleteFunc(slices.Clone(members), func(m member) bool { return m.name() == recordKey })
	}
	out := slices.Clone(members)
	for _, name := range []string{dependenciesKey, recordKey} {
		if _, found := find(out, name); !found {
			out = append(out, member{key: jsontext.String(name)})
		}
	}
	return out
}

// dependencies returns deps without the recorded names and with every name in want as a workspace dependency, by name.
func dependencies(deps []member, recorded, want []string) []member {
	out := make([]member, 0, len(deps)+len(want))
	for _, dep := range deps {
		if !slices.Contains(recorded, dep.name()) && !slices.Contains(want, dep.name()) {
			out = append(out, dep)
		}
	}
	for _, name := range want {
		out = append(out, member{key: jsontext.String(name), value: jsontext.Value(workspaceDependency)})
	}
	slices.SortFunc(out, func(a, b member) int { return strings.Compare(a.name(), b.name()) })
	return out
}

// render returns members as tab-indented JSON text with deps as the dependencies and want as the record.
func render(members, deps []member, want []string) ([]byte, error) {
	var buf bytes.Buffer
	w := &writer{enc: jsontext.NewEncoder(&buf,
		jsontext.Multiline(true), jsontext.WithIndent("\t"), jsontext.PreserveRawStrings(true))}
	w.token(jsontext.BeginObject)
	for _, m := range members {
		w.token(m.key)
		switch m.name() {
		case dependenciesKey:
			w.object(deps)
		case recordKey:
			w.record(want)
		default:
			w.value(m.value)
		}
	}
	w.token(jsontext.EndObject)
	if w.err != nil {
		return nil, w.err
	}
	return buf.Bytes(), nil
}

// writer writes JSON tokens and values to an encoder and keeps the first error.
type writer struct {
	enc *jsontext.Encoder
	err error
}

// token writes t unless an earlier write failed.
func (w *writer) token(t jsontext.Token) {
	if w.err == nil {
		w.err = w.enc.WriteToken(t)
	}
}

// value writes v unless an earlier write failed.
func (w *writer) value(v jsontext.Value) {
	if w.err == nil {
		w.err = w.enc.WriteValue(v)
	}
}

// object writes members as one JSON object.
func (w *writer) object(members []member) {
	w.token(jsontext.BeginObject)
	for _, m := range members {
		w.token(m.key)
		w.value(m.value)
	}
	w.token(jsontext.EndObject)
}

// record writes names as the pluginwire record.
func (w *writer) record(names []string) {
	w.token(jsontext.BeginObject)
	w.token(jsontext.String(dependenciesKey))
	w.token(jsontext.BeginArray)
	for _, name := range names {
		w.token(jsontext.String(name))
	}
	w.token(jsontext.EndArray)
	w.token(jsontext.EndObject)
}
