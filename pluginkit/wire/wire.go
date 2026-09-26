// SPDX-License-Identifier: Apache-2.0

// Package wire generates an application's plugin wiring files from the
// plugin.json manifest of every directory under each configured plugin root.
package wire

import (
	"encoding/json"
	"errors"
	"fmt"
	"go/token"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
)

// Config parameterizes generation for the consuming application.
type Config struct {
	SDKImport    string
	FrontendSDK  string
	GoWiringPath string
	TSWiringPath string
	License      string
	TSLicense    string
	Roots        []string

	GoRegistryPath    string
	GoRegistryPackage string

	// Reserved lists ids no plugin may take.
	Reserved []string
}

// roots returns the plugin root directories scanned in order, defaulting to plugins.
func (c Config) roots() []string {
	if len(c.Roots) == 0 {
		return []string{"plugins"}
	}
	return c.Roots
}

// tsLicense returns the license for the TypeScript wiring file.
func (c Config) tsLicense() string {
	if c.TSLicense != "" {
		return c.TSLicense
	}
	return c.License
}

// validateConfig checks that every Config field is set.
func validateConfig(cfg Config) error {
	if cfg.SDKImport == "" || cfg.FrontendSDK == "" || cfg.GoWiringPath == "" ||
		cfg.TSWiringPath == "" || cfg.License == "" {
		return errors.New("wire: every Config field is required")
	}
	if (cfg.GoRegistryPath == "") != (cfg.GoRegistryPackage == "") {
		return errors.New("wire: GoRegistryPath and GoRegistryPackage are required together")
	}
	return nil
}

type manifest struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	Backend  string `json:"backend"`
	Frontend string `json:"frontend"`
}

var idPattern = regexp.MustCompile(`^[a-z][a-z0-9-]*$`)

// loadManifests loads and validates the plugin manifest in each subdirectory of dir.
func loadManifests(dir string) ([]manifest, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, fmt.Errorf("pluginwire: reading plugins directory: %w", err)
	}
	manifests := make([]manifest, 0, len(entries))
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		path := filepath.Join(dir, entry.Name(), "plugin.json")
		data, err := os.ReadFile(path)
		if err != nil {
			return nil, fmt.Errorf("pluginwire: %s: %w", path, err)
		}
		var m manifest
		if err := json.Unmarshal(data, &m); err != nil {
			return nil, fmt.Errorf("pluginwire: %s: %w", path, err)
		}
		if err := validate(m, entry.Name()); err != nil {
			return nil, fmt.Errorf("pluginwire: %s: %w", path, err)
		}
		manifests = append(manifests, m)
	}
	return manifests, nil
}

// goOwned are the names an import alias of the generated Go wiring cannot take, beside the Go keywords.
var goOwned = map[string]bool{
	"errors": true, "fmt": true, "sdk": true, "deps": true, "plugins": true, "failed": true, "err": true,
	"make": true, "append": true, "nil": true, "error": true, "init": true, "main": true,
}

// tsOwned are the names an import alias of the generated TypeScript wiring cannot take.
var tsOwned = map[string]bool{
	"await": true, "break": true, "case": true, "catch": true, "class": true, "const": true, "continue": true,
	"debugger": true, "default": true, "delete": true, "do": true, "else": true, "enum": true, "export": true,
	"extends": true, "false": true, "finally": true, "for": true, "function": true, "if": true, "import": true,
	"in": true, "instanceof": true, "new": true, "null": true, "return": true, "super": true, "switch": true,
	"this": true, "throw": true, "true": true, "try": true, "typeof": true, "var": true, "void": true,
	"while": true, "with": true, "yield": true, "implements": true, "interface": true, "let": true,
	"package": true, "private": true, "protected": true, "public": true, "static": true, "eval": true,
	"arguments": true, "plugins": true,
}

// refuseReserved rejects an id the application reserves or an import alias of the generated wiring cannot take.
func refuseReserved(m manifest, reserved []string) error {
	switch {
	case slices.Contains(reserved, m.ID):
		return fmt.Errorf("id %q is reserved", m.ID)
	case m.Backend != "" && (token.IsKeyword(m.ID) || goOwned[m.ID]):
		return fmt.Errorf("id %q collides with the generated Go wiring", m.ID)
	case m.Frontend != "" && tsOwned[m.ID]:
		return fmt.Errorf("id %q collides with the generated TypeScript wiring", m.ID)
	}
	return nil
}

// loadRoots loads the manifests under every root in order, rejecting a reserved id or one present in two roots.
func loadRoots(dir string, roots, reserved []string) ([]manifest, error) {
	var manifests []manifest
	seen := make(map[string]string, len(roots))
	for _, pluginRoot := range roots {
		loaded, err := loadManifests(filepath.Join(dir, pluginRoot))
		if err != nil {
			return nil, err
		}
		for _, m := range loaded {
			if previous, ok := seen[m.ID]; ok {
				return nil, fmt.Errorf("pluginwire: plugin %s appears under %s and %s", m.ID, previous, pluginRoot)
			}
			if err := refuseReserved(m, reserved); err != nil {
				return nil, fmt.Errorf("pluginwire: %s: %w", filepath.Join(dir, pluginRoot, m.ID, "plugin.json"), err)
			}
			seen[m.ID] = pluginRoot
		}
		manifests = append(manifests, loaded...)
	}
	return manifests, nil
}

// validate checks that manifest m has a well-formed id matching dir, a name, and at least one of backend or frontend.
func validate(m manifest, dir string) error {
	if !idPattern.MatchString(m.ID) {
		return fmt.Errorf("id %q must match %s", m.ID, idPattern)
	}
	if m.ID != dir {
		return fmt.Errorf("id %q does not match directory %q", m.ID, dir)
	}
	if m.Name == "" {
		return errors.New("name is required")
	}
	if m.Backend == "" && m.Frontend == "" {
		return errors.New("at least one of backend or frontend is required")
	}
	return nil
}

// goName returns id as a valid Go identifier.
func goName(id string) string {
	return strings.ReplaceAll(id, "-", "_")
}

// generatedHeader renders the SPDX and generated-code header for license.
func generatedHeader(license string) string {
	return "// SPDX-License-Identifier: " + license + "\n\n" +
		"// Code generated by pluginwire. DO NOT EDIT.\n\n"
}

// generateGo renders the generated Go plugin-wiring file.
func generateGo(cfg Config, manifests []manifest) []byte {
	doc := "// registerPlugins registers every compiled plugin, answering the ones that registered and an error naming " +
		"each failure.\n"
	return renderRegistration(cfg, manifests, "main", doc, "registerPlugins")
}

// generateRegistry renders the generated importable plugin registry file.
func generateRegistry(cfg Config, manifests []manifest) []byte {
	doc := "// All registers every plugin, answering the ones that registered and an error naming each failure.\n"
	return renderRegistration(cfg, manifests, cfg.GoRegistryPackage, doc, "All")
}

// renderRegistration renders one Go file registering every backend plugin through the named function.
func renderRegistration(cfg Config, manifests []manifest, pkg, doc, funcName string) []byte {
	backends := make([]manifest, 0, len(manifests))
	for _, m := range manifests {
		if m.Backend != "" {
			backends = append(backends, m)
		}
	}

	var b strings.Builder
	b.WriteString(generatedHeader(cfg.License))
	fmt.Fprintf(&b, "package %s\n\nimport (\n", pkg)
	if len(backends) > 0 {
		b.WriteString("\t\"errors\"\n\t\"fmt\"\n\n")
	}
	for _, m := range backends {
		fmt.Fprintf(&b, "\t%s %q\n", goName(m.ID), m.Backend)
	}
	if len(backends) > 0 {
		b.WriteString("\n")
	}
	fmt.Fprintf(&b, "\tsdk %q\n)\n\n", cfg.SDKImport)
	b.WriteString(doc)
	if len(backends) == 0 {
		fmt.Fprintf(&b, "func %s(_ sdk.Deps) ([]sdk.Plugin, error) {\n\treturn []sdk.Plugin{}, nil\n}\n", funcName)
		return []byte(b.String())
	}
	fmt.Fprintf(
		&b,
		"func %s(deps sdk.Deps) ([]sdk.Plugin, error) {\n\tplugins := make([]sdk.Plugin, 0, %d)\n\tvar failed []error\n",
		funcName,
		len(backends),
	)
	for _, m := range backends {
		name := goName(m.ID)
		fmt.Fprintf(
			&b,
			"\t%sPlugin, err := %s.Register(deps)\n\tif err != nil {\n"+
				"\t\tfailed = append(failed, fmt.Errorf(\"plugin %s: %%w\", err))\n\t} else {\n"+
				"\t\tplugins = append(plugins, %sPlugin)\n\t}\n",
			name,
			name,
			m.ID,
			name,
		)
	}
	b.WriteString("\treturn plugins, errors.Join(failed...)\n}\n")
	return []byte(b.String())
}

// generateTS renders the generated TypeScript plugin-wiring file.
func generateTS(cfg Config, manifests []manifest) []byte {
	frontends := make([]manifest, 0, len(manifests))
	for _, m := range manifests {
		if m.Frontend != "" {
			frontends = append(frontends, m)
		}
	}

	var b strings.Builder
	b.WriteString(generatedHeader(cfg.tsLicense()))
	fmt.Fprintf(&b, "import type { FrontendPlugin } from '%s'\n", cfg.FrontendSDK)
	names := make([]string, 0, len(frontends))
	for _, m := range frontends {
		name := goName(m.ID)
		names = append(names, name)
		fmt.Fprintf(&b, "import { plugin as %s } from '%s'\n", name, m.Frontend)
	}
	fmt.Fprintf(&b, "\nexport const plugins: FrontendPlugin[] = [%s]\n", strings.Join(names, ", "))
	return []byte(b.String())
}

// Run loads the plugin manifests under root and writes the application's
// generated Go and TypeScript wiring files per cfg.
func Run(root string, cfg Config) error {
	if err := validateConfig(cfg); err != nil {
		return err
	}
	manifests, err := loadRoots(root, cfg.roots(), cfg.Reserved)
	if err != nil {
		return err
	}
	goPath := filepath.Join(root, filepath.FromSlash(cfg.GoWiringPath))
	if err := os.WriteFile(goPath, generateGo(cfg, manifests), 0o644); err != nil {
		return fmt.Errorf("pluginwire: %w", err)
	}
	tsPath := filepath.Join(root, filepath.FromSlash(cfg.TSWiringPath))
	if err := os.WriteFile(tsPath, generateTS(cfg, manifests), 0o644); err != nil {
		return fmt.Errorf("pluginwire: %w", err)
	}
	if cfg.GoRegistryPath == "" {
		return nil
	}
	registryPath := filepath.Join(root, filepath.FromSlash(cfg.GoRegistryPath))
	if err := os.WriteFile(registryPath, generateRegistry(cfg, manifests), 0o644); err != nil {
		return fmt.Errorf("pluginwire: %w", err)
	}
	return nil
}
