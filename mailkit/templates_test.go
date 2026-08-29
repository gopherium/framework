// SPDX-License-Identifier: Apache-2.0

package mailkit_test

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/gopherium/framework/mailkit"
)

// defaultsWith returns an in memory defaults filesystem holding one invite template.
func defaultsWith(content string) fstest.MapFS {
	return fstest.MapFS{"invite.tmpl": &fstest.MapFile{Data: []byte(content)}}
}

// templates builds Templates over the given sources, failing the test on error.
func templates(t *testing.T, defaults fs.FS, overrideDir string) *mailkit.Templates {
	t.Helper()
	held, err := mailkit.NewTemplates(defaults, overrideDir)
	if err != nil {
		t.Fatalf("NewTemplates() error = %v, want nil", err)
	}
	return held
}

func TestRenderFillsSubjectAndBodyFromTheTemplate(t *testing.T) {
	t.Parallel()

	defaults := defaultsWith("You are invited, {{.Name}}\nHello {{.Name}}, set your password.")
	tpls := templates(t, defaults, "")

	m, err := tpls.Render("invite.tmpl", map[string]string{"Name": "Maria Perez"})

	if err != nil {
		t.Fatalf("Render() error = %v, want nil", err)
	}
	if m.Subject != "You are invited, Maria Perez" {
		t.Errorf("subject = %q, want the rendered first line", m.Subject)
	}
	if m.Body != "Hello Maria Perez, set your password." {
		t.Errorf("body = %q, want the rendered remainder", m.Body)
	}
	if m.To != "" {
		t.Errorf("to = %q, want empty for the caller to fill", m.To)
	}
}

func TestRenderSplitsAtTheFirstNewline(t *testing.T) {
	t.Parallel()

	tpls := templates(t, defaultsWith("Subject line\nFirst body line\nSecond body line\n"), "")

	m, err := tpls.Render("invite.tmpl", nil)

	if err != nil {
		t.Fatalf("Render() error = %v, want nil", err)
	}
	if m.Subject != "Subject line" {
		t.Errorf("subject = %q, want %q", m.Subject, "Subject line")
	}
	if m.Body != "First body line\nSecond body line\n" {
		t.Errorf("body = %q, want every later line kept", m.Body)
	}
}

func TestRenderPrefersTheOverrideDirectory(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	overridden := filepath.Join(dir, "invite.tmpl")
	if err := os.WriteFile(overridden, []byte("Operator subject\nOperator body"), 0o600); err != nil {
		t.Fatalf("writing the override: %v", err)
	}
	tpls := templates(t, defaultsWith("Default subject\nDefault body"), dir)

	m, err := tpls.Render("invite.tmpl", nil)

	if err != nil {
		t.Fatalf("Render() error = %v, want nil", err)
	}
	if m.Subject != "Operator subject" {
		t.Errorf("subject = %q, want the override to win", m.Subject)
	}
}

func TestRenderFallsBackToTheDefaults(t *testing.T) {
	t.Parallel()

	tpls := templates(t, defaultsWith("Default subject\nDefault body"), t.TempDir())

	m, err := tpls.Render("invite.tmpl", nil)

	if err != nil {
		t.Fatalf("Render() error = %v, want the default used", err)
	}
	if m.Subject != "Default subject" {
		t.Errorf("subject = %q, want the default", m.Subject)
	}
}

func TestRenderReadsTheOverrideAtRenderTime(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	overridden := filepath.Join(dir, "invite.tmpl")
	if err := os.WriteFile(overridden, []byte("First wording\nBody"), 0o600); err != nil {
		t.Fatalf("writing the override: %v", err)
	}
	tpls := templates(t, defaultsWith("Default subject\nDefault body"), dir)
	if _, err := tpls.Render("invite.tmpl", nil); err != nil {
		t.Fatalf("first Render() error = %v, want nil", err)
	}

	if err := os.WriteFile(overridden, []byte("Second wording\nBody"), 0o600); err != nil {
		t.Fatalf("rewriting the override: %v", err)
	}
	m, err := tpls.Render("invite.tmpl", nil)

	if err != nil {
		t.Fatalf("second Render() error = %v, want nil", err)
	}
	if m.Subject != "Second wording" {
		t.Errorf("subject = %q, want the edit applied with no restart", m.Subject)
	}
}

func TestRenderSurfacesAnUnreadableOverride(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	if err := os.Mkdir(filepath.Join(dir, "invite.tmpl"), 0o700); err != nil {
		t.Fatalf("planting the unreadable override: %v", err)
	}
	tpls := templates(t, defaultsWith("Default subject\nDefault body"), dir)

	_, err := tpls.Render("invite.tmpl", nil)

	if err == nil {
		t.Fatal("Render() error = nil, want the unreadable override surfaced, not the default applied")
	}
	if errors.Is(err, fs.ErrNotExist) {
		t.Errorf("Render() error = %v, want a read failure, not a missing template", err)
	}
}

func TestRenderTrimsACarriageReturnFromTheSubject(t *testing.T) {
	t.Parallel()

	tpls := templates(t, defaultsWith("Subject line\r\nBody line\r\n"), "")

	m, err := tpls.Render("invite.tmpl", nil)

	if err != nil {
		t.Fatalf("Render() error = %v, want nil", err)
	}
	if m.Subject != "Subject line" {
		t.Errorf("subject = %q, want the carriage return trimmed", m.Subject)
	}
}

func TestRenderStripsALeadingByteOrderMark(t *testing.T) {
	t.Parallel()

	tpls := templates(t, defaultsWith("\ufeffSubject line\nBody"), "")

	m, err := tpls.Render("invite.tmpl", nil)

	if err != nil {
		t.Fatalf("Render() error = %v, want nil", err)
	}
	if m.Subject != "Subject line" {
		t.Errorf("subject = %q, want the byte order mark stripped", m.Subject)
	}
}

func TestRenderTrimsLeadingBlankOutput(t *testing.T) {
	t.Parallel()

	tpls := templates(t, defaultsWith("{{/* the invite mail */}}\nSubject line\nBody"), "")

	m, err := tpls.Render("invite.tmpl", nil)

	if err != nil {
		t.Fatalf("Render() error = %v, want nil", err)
	}
	if m.Subject != "Subject line" {
		t.Errorf("subject = %q, want the blank first line skipped", m.Subject)
	}
	if m.Body != "Body" {
		t.Errorf("body = %q, want %q", m.Body, "Body")
	}
}

func TestRenderRefusesAnEmptyRender(t *testing.T) {
	t.Parallel()

	tpls := templates(t, defaultsWith("{{if .Confirmed}}Subject\nBody{{end}}"), "")

	_, err := tpls.Render("invite.tmpl", map[string]bool{"Confirmed": false})

	if !errors.Is(err, mailkit.ErrNoSubject) {
		t.Errorf("Render() error = %v, want mailkit.ErrNoSubject", err)
	}
	if err != nil && !strings.Contains(err.Error(), "invite.tmpl") {
		t.Errorf("Render() error = %v, want the template named", err)
	}
}

func TestRenderAnswersAnEmptyBodyForASubjectOnlyFile(t *testing.T) {
	t.Parallel()

	tpls := templates(t, defaultsWith("Only a subject"), "")

	m, err := tpls.Render("invite.tmpl", nil)

	if err != nil {
		t.Fatalf("Render() error = %v, want nil", err)
	}
	if m.Subject != "Only a subject" {
		t.Errorf("subject = %q, want the whole file", m.Subject)
	}
	if m.Body != "" {
		t.Errorf("body = %q, want empty", m.Body)
	}
}

func TestRenderTruncatesTheSubjectAtRenderedNewlines(t *testing.T) {
	t.Parallel()

	tpls := templates(t, defaultsWith("Hi {{.Name}}\nBody"), "")

	m, err := tpls.Render("invite.tmpl", map[string]string{"Name": "Maria\nPerez"})

	if err != nil {
		t.Fatalf("Render() error = %v, want nil", err)
	}
	if m.Subject != "Hi Maria" {
		t.Errorf("subject = %q, want the first rendered line alone", m.Subject)
	}
	if m.Body != "Perez\nBody" {
		t.Errorf("body = %q, want the rendered remainder", m.Body)
	}
}

func TestRenderRefusesAnUnknownTemplate(t *testing.T) {
	t.Parallel()

	tpls := templates(t, defaultsWith("Subject\nBody"), "")

	_, err := tpls.Render("goodbye.tmpl", nil)

	if !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("Render() error = %v, want fs.ErrNotExist wrapped", err)
	}
	if err != nil && !strings.Contains(err.Error(), "goodbye.tmpl") {
		t.Errorf("Render() error = %v, want the template named", err)
	}
}

func TestRenderRefusesAPathTraversingName(t *testing.T) {
	t.Parallel()

	tpls := templates(t, defaultsWith("Subject\nBody"), t.TempDir())

	for _, name := range []string{"../invite.tmpl", "nested/invite.tmpl", ""} {
		if _, err := tpls.Render(name, nil); err == nil {
			t.Errorf("Render(%q) error = nil, want the name refused", name)
		}
	}
}

func TestRenderSurfacesAParseError(t *testing.T) {
	t.Parallel()

	tpls := templates(t, defaultsWith("Subject {{.Name\nBody"), "")

	if _, err := tpls.Render("invite.tmpl", nil); err == nil {
		t.Error("Render() error = nil, want the parse failure surfaced")
	}
}

func TestNewTemplatesRefusesNilDefaults(t *testing.T) {
	t.Parallel()

	if _, err := mailkit.NewTemplates(nil, ""); err == nil {
		t.Error("NewTemplates() error = nil, want nil defaults refused")
	}
}

func TestNewTemplatesRefusesAMissingOverrideDirectory(t *testing.T) {
	t.Parallel()

	missing := filepath.Join(t.TempDir(), "no-such-directory")

	if _, err := mailkit.NewTemplates(defaultsWith("Subject\nBody"), missing); err == nil {
		t.Error("NewTemplates() error = nil, want the missing directory refused at boot")
	}
}

func TestNewTemplatesRefusesAFileAsOverrideDirectory(t *testing.T) {
	t.Parallel()

	file := filepath.Join(t.TempDir(), "a-file")
	if err := os.WriteFile(file, []byte("held"), 0o600); err != nil {
		t.Fatalf("writing the file: %v", err)
	}

	if _, err := mailkit.NewTemplates(defaultsWith("Subject\nBody"), file); err == nil {
		t.Error("NewTemplates() error = nil, want a plain file refused as the directory")
	}
}
