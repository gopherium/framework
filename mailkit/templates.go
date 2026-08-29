// SPDX-License-Identifier: Apache-2.0

package mailkit

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"text/template"
)

// Templates renders mail from template files, preferring an override directory.
type Templates struct {
	defaults    fs.FS
	overrideDir string
}

// NewTemplates returns Templates over the caller's default files and an optional override directory.
func NewTemplates(defaults fs.FS, overrideDir string) (*Templates, error) {
	if defaults == nil {
		return nil, errors.New("mailkit: nil defaults filesystem")
	}
	if overrideDir != "" {
		info, err := os.Stat(overrideDir)
		if err != nil {
			return nil, fmt.Errorf("mailkit: override directory: %w", err)
		}
		if !info.IsDir() {
			return nil, fmt.Errorf("mailkit: override directory %s is not a directory", overrideDir)
		}
	}
	return &Templates{defaults: defaults, overrideDir: overrideDir}, nil
}

// Render executes the named template with data, answering a Message whose To the caller fills.
func (t *Templates) Render(name string, data any) (Message, error) {
	raw, err := t.read(name)
	if err != nil {
		return Message{}, err
	}
	parsed, err := template.New(name).Parse(string(raw))
	if err != nil {
		return Message{}, fmt.Errorf("mailkit: parse template %s: %w", name, err)
	}
	var out strings.Builder
	if err := parsed.Execute(&out, data); err != nil {
		return Message{}, fmt.Errorf("mailkit: render template %s: %w", name, err)
	}
	return split(name, out.String())
}

// read answers the named file from the override directory, else the defaults.
func (t *Templates) read(name string) ([]byte, error) {
	if !fs.ValidPath(name) || name == "." || strings.ContainsAny(name, `/\`) {
		return nil, fmt.Errorf("mailkit: template name %q: %w", name, fs.ErrInvalid)
	}
	if t.overrideDir != "" {
		raw, err := os.ReadFile(filepath.Join(t.overrideDir, name))
		if err == nil {
			return raw, nil
		}
		if !errors.Is(err, fs.ErrNotExist) {
			return nil, fmt.Errorf("mailkit: read override %s: %w", name, err)
		}
	}
	raw, err := fs.ReadFile(t.defaults, name)
	if err != nil {
		return nil, fmt.Errorf("mailkit: template %s: %w", name, err)
	}
	return raw, nil
}

// split cuts rendered output into the subject line and the body.
func split(name, rendered string) (Message, error) {
	rendered = strings.TrimPrefix(rendered, "\ufeff")
	rendered = strings.TrimLeft(rendered, " \t\r\n")
	if rendered == "" {
		return Message{}, fmt.Errorf("mailkit: render template %s: %w", name, ErrNoSubject)
	}
	subject, body, _ := strings.Cut(rendered, "\n")
	return Message{Subject: strings.TrimRight(subject, "\r"), Body: body}, nil
}
