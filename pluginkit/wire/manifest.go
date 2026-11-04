// SPDX-License-Identifier: Apache-2.0

package wire

import (
	"bytes"
	"cmp"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"maps"
	"slices"
	"strings"
)

// Manifest is one plugin.json read as data, every key after Frontend optional.
type Manifest struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	Backend  string `json:"backend,omitempty"`
	Frontend string `json:"frontend,omitempty"`

	Version   string            `json:"version,omitempty"`
	License   string            `json:"license,omitempty"`
	DID       string            `json:"did,omitempty"`
	Requires  map[string]string `json:"requires,omitempty"`
	Tested    string            `json:"tested,omitempty"`
	Engines   []string          `json:"engines,omitempty"`
	Uninstall bool              `json:"uninstall,omitempty"`
	Hooks     []string          `json:"hooks,omitempty"`
	Defines   []Define          `json:"defines,omitempty"`
	Jobs      []Job             `json:"jobs,omitempty"`
	Settings  []Setting         `json:"settings,omitempty"`

	// Extra holds every other key of the manifest as raw JSON, for the application to check and read.
	Extra map[string]json.RawMessage `json:"-"`
}

// MarshalJSON writes m under the key names of plugin.json, its Extra keys after the typed ones.
func (m Manifest) MarshalJSON() ([]byte, error) {
	typed, typedErr := encodeJSON(manifest(m), "")
	extra, extraErr := encodeJSON(m.Extra, "")
	if err := errors.Join(typedErr, extraErr); err != nil {
		return nil, err
	}
	if len(m.Extra) == 0 {
		return typed, nil
	}
	return slices.Concat(typed[:len(typed)-len("}\n")], []byte(","), extra[1:]), nil
}

// UnmarshalJSON reads m from a plugin.json or an entry of the manifest index, checking only the JSON types.
func (m *Manifest) UnmarshalJSON(data []byte) error {
	parsed, err := parseManifest(data)
	*m = Manifest(parsed)
	return err
}

// Define is one hook a plugin offers to other plugins.
type Define struct {
	Hook  string `json:"hook"`
	Kind  string `json:"kind,omitempty"`
	Fires string `json:"fires,omitempty"`
}

// UnmarshalJSON replaces d with a plain string read as the name of the hook, or an object read by its keys.
func (d *Define) UnmarshalJSON(data []byte) error {
	var hook string
	if json.Unmarshal(data, &hook) == nil {
		*d = Define{Hook: hook}
		return nil
	}
	type define Define
	var entry define
	if err := json.Unmarshal(data, &entry); err != nil {
		return err
	}
	*d = Define(entry)
	return nil
}

// Job is one scheduled job of a plugin and its default interval.
type Job struct {
	Name  string `json:"name"`
	Every string `json:"every"`
}

// Setting is one setting of a plugin, the schema an application draws a form from.
type Setting struct {
	Key     string   `json:"key"`
	Type    string   `json:"type"`
	Default string   `json:"default,omitempty"`
	Label   string   `json:"label,omitempty"`
	Help    string   `json:"help,omitempty"`
	Min     *float64 `json:"min,omitempty"`
	Max     *float64 `json:"max,omitempty"`
	Options []string `json:"options,omitempty"`
	Of      string   `json:"of,omitempty"`
}

// manifest is a Manifest with no JSON methods, so decoding it stays plain and its errors name manifest.
type manifest Manifest

// typedKeys are the manifest keys Manifest has a field for.
var typedKeys = []string{
	"id", "name", "backend", "frontend", "version", "license", "did", "requires", "tested", "engines", "uninstall",
	"hooks", "defines", "jobs", "settings",
}

// isTypedKey reports whether encoding/json reads key into a field of Manifest.
func isTypedKey(key string, _ json.RawMessage) bool {
	return slices.ContainsFunc(typedKeys, func(typed string) bool { return strings.EqualFold(key, typed) })
}

// parseManifest decodes one plugin.json, keeping the keys Manifest has no field for in Extra.
func parseManifest(data []byte) (manifest, error) {
	var m manifest
	var raw map[string]json.RawMessage
	err := json.Unmarshal(data, &m)
	if err == nil {
		err = json.Unmarshal(data, &raw)
	}
	if err != nil {
		return manifest{}, nameDefine(data, err)
	}
	maps.DeleteFunc(raw, isTypedKey)
	if len(raw) > 0 {
		m.Extra = raw
	}
	return m, nil
}

// nameDefine puts the entry of defines that gave err in front of it, as the decoder does for jobs and settings.
func nameDefine(data []byte, err error) error {
	var entries struct{ Defines []json.RawMessage }
	if json.Unmarshal(data, &entries) != nil {
		return err
	}
	for i, entry := range entries.Defines {
		if entryErr := new(Define).UnmarshalJSON(entry); entryErr != nil && entryErr.Error() == err.Error() {
			return fmt.Errorf("defines[%d]: %w", i, err)
		}
	}
	return err
}

// checkList refuses an empty string in the list called name.
func checkList(name string, values []string) error {
	if i := slices.Index(values, ""); i >= 0 {
		return fmt.Errorf("%s[%d]: must not be empty", name, i)
	}
	return nil
}

// checkDefines refuses a defined hook with no name.
func checkDefines(defines []Define) error {
	for i, define := range defines {
		if define.Hook == "" {
			return fmt.Errorf("defines[%d]: hook is required", i)
		}
	}
	return nil
}

// checkJobs refuses a job with no name or no interval.
func checkJobs(jobs []Job) error {
	for i, job := range jobs {
		if job.Name == "" {
			return fmt.Errorf("jobs[%d]: name is required", i)
		}
		if job.Every == "" {
			return fmt.Errorf("jobs[%d]: every is required", i)
		}
	}
	return nil
}

// checkSettings refuses a setting with no key, no type or an empty option.
func checkSettings(settings []Setting) error {
	for i, setting := range settings {
		if setting.Key == "" {
			return fmt.Errorf("settings[%d]: key is required", i)
		}
		if setting.Type == "" {
			return fmt.Errorf("settings[%d]: type is required", i)
		}
		if err := checkList(fmt.Sprintf("settings[%d].options", i), setting.Options); err != nil {
			return err
		}
	}
	return nil
}

// checkShape refuses an empty string where the shape of a manifest asks for one, naming the first such entry.
func checkShape(m manifest) error {
	return cmp.Or(
		checkList("engines", m.Engines),
		checkList("hooks", m.Hooks),
		checkDefines(m.Defines),
		checkJobs(m.Jobs),
		checkSettings(m.Settings),
	)
}

// Read reads one plugin's manifest from the plugin's own file system, with the checks that need only the manifest.
func Read(plugin fs.FS) (Manifest, error) {
	data, err := fs.ReadFile(plugin, "plugin.json")
	if err != nil {
		return Manifest{}, err
	}
	m, err := parseManifest(data)
	if err != nil {
		return Manifest{}, err
	}
	if err := validate(m, m.ID); err != nil {
		return Manifest{}, err
	}
	return Manifest(m), nil
}

// Load reads every manifest under the roots of dir in order, with the checks Run applies.
func Load(dir string, roots, reserved []string) ([]Manifest, error) {
	if len(roots) == 0 {
		return nil, errors.New("wire: Load needs at least one root")
	}
	manifests, err := loadRoots(dir, roots, reserved)
	if err != nil {
		return nil, err
	}
	return exported(manifests), nil
}

// exported returns manifests as the type callers read.
func exported(manifests []manifest) []Manifest {
	converted := make([]Manifest, len(manifests))
	for i, m := range manifests {
		converted[i] = Manifest(m)
	}
	return converted
}

// generateIndex renders every manifest as a JSON list in wiring order.
func generateIndex(manifests []manifest) ([]byte, error) {
	return encodeJSON(exported(manifests), "\t")
}

// encodeJSON returns value as JSON ending in a newline, indented by indent, with its HTML characters left readable.
func encodeJSON(value any, indent string) ([]byte, error) {
	var b bytes.Buffer
	encoder := json.NewEncoder(&b)
	encoder.SetEscapeHTML(false)
	encoder.SetIndent("", indent)
	err := encoder.Encode(value)
	return b.Bytes(), err
}
