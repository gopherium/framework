// SPDX-License-Identifier: Apache-2.0

// Package graphwire generates an application's graph resolver root from the
// plugin manifests and SDL of every directory under each configured plugin root.
package graphwire

import (
	"errors"
	"fmt"
	"go/format"
	"go/token"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"

	"github.com/vektah/gqlparser/v2/ast"
	"github.com/vektah/gqlparser/v2/parser"

	"github.com/gopherium/framework/pluginkit/wire"
)

// Config parameterizes the resolver root generation for the consuming application.
type Config struct {
	// ExecImport is the gqlgen exec package declaring ResolverRoot.
	ExecImport string
	// CoreImport is the package exporting the core resolver sets.
	CoreImport string
	// CoreSchemaGlobs locate the core SDL files relative to the root.
	CoreSchemaGlobs []string
	// WiringPath is the generated file destination relative to the root.
	WiringPath string
	// License is the SPDX identifier of the generated header.
	License string
	// Package names the generated package, exporting its identifiers and
	// the FromPlugins assembler. Empty generates an unexported package main.
	Package string
	// SDKImport is the package declaring the plugin interface, required
	// with Package for the FromPlugins assembler.
	SDKImport string
	// Roots lists the plugin root directories scanned in order, empty scanning plugins.
	Roots []string
}

// validateConfig checks that every Config field is set.
func validateConfig(cfg Config) error {
	fields := []struct {
		name string
		set  bool
	}{
		{"ExecImport", cfg.ExecImport != ""},
		{"CoreImport", cfg.CoreImport != ""},
		{"CoreSchemaGlobs", len(cfg.CoreSchemaGlobs) > 0},
		{"WiringPath", cfg.WiringPath != ""},
		{"License", cfg.License != ""},
	}
	for _, field := range fields {
		if !field.set {
			return fmt.Errorf("graphwire: Config.%s is required", field.name)
		}
	}
	if cfg.Package != "" && cfg.SDKImport == "" {
		return errors.New("graphwire: Config.SDKImport is required with Config.Package")
	}
	return nil
}

// manifest is one plugin.json as wire reads it, with the plugin root it came from.
type manifest struct {
	wire.Manifest
	root string
}

// roots returns the plugin root directories scanned in order, defaulting to plugins.
func (c Config) roots() []string {
	if len(c.Roots) == 0 {
		return []string{"plugins"}
	}
	return c.Roots
}

// loadRoots loads the manifests under every plugin root in order, rejecting an id present in more than one root.
func loadRoots(dir string, roots []string) ([]manifest, error) {
	var manifests []manifest
	seen := make(map[string]string, len(roots))
	for _, pluginRoot := range roots {
		loaded, err := loadManifests(filepath.Join(dir, pluginRoot))
		if err != nil {
			return nil, err
		}
		for i, m := range loaded {
			if previous, ok := seen[m.ID]; ok {
				return nil, fmt.Errorf("graphwire: plugin %s appears under %s and %s", m.ID, previous, pluginRoot)
			}
			seen[m.ID] = pluginRoot
			loaded[i].root = pluginRoot
		}
		manifests = append(manifests, loaded...)
	}
	return manifests, nil
}

// loadManifests loads the plugin manifest in each subdirectory of dir.
func loadManifests(dir string) ([]manifest, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, fmt.Errorf("graphwire: reading plugins directory: %w", err)
	}
	manifests := make([]manifest, 0, len(entries))
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		m, err := loadManifest(dir, entry.Name())
		if err != nil {
			return nil, err
		}
		manifests = append(manifests, m)
	}
	return manifests, nil
}

// loadManifest reads and checks the plugin.json in directory name under dir through wire, its path before any error.
func loadManifest(dir, name string) (manifest, error) {
	read, err := wire.Read(os.DirFS(filepath.Join(dir, name)))
	if err == nil {
		err = validateManifest(read, name)
	}
	if err != nil {
		return manifest{}, fmt.Errorf("graphwire: %s: %w", filepath.Join(dir, name, "plugin.json"), err)
	}
	return manifest{Manifest: read}, nil
}

// validateManifest checks that m names its directory dir and that a graphql plugin has a backend.
func validateManifest(m wire.Manifest, dir string) error {
	if m.ID != dir {
		return fmt.Errorf("id %q does not match directory %q", m.ID, dir)
	}
	if m.GraphQL && m.Backend == "" {
		return errors.New("graphql plugins require a backend")
	}
	return nil
}

// goName returns id as a valid Go identifier.
func goName(id string) string {
	return strings.ReplaceAll(id, "-", "_")
}

// contributor is one package contributing resolver sets to the graph.
type contributor struct {
	alias string
	path  string
	field string
	param string
	types []string
}

// isRootType reports whether name is a GraphQL operation root type.
func isRootType(name string) bool {
	return name == "Query" || name == "Mutation" || name == "Subscription"
}

// forcesResolver reports whether field carries goField(forceResolver: true).
func forcesResolver(field *ast.FieldDefinition) bool {
	directive := field.Directives.ForName("goField")
	if directive == nil {
		return false
	}
	arg := directive.Arguments.ForName("forceResolver")
	return arg != nil && arg.Value != nil && arg.Value.Raw == "true"
}

// collectResolverType records def when it carries resolver backed fields.
func collectResolverType(set map[string]bool, def *ast.Definition) {
	if def.Kind != ast.Object {
		return
	}
	if needsResolverSet(def) {
		set[def.Name] = true
	}
}

// needsResolverSet reports whether def gets a gqlgen resolver interface.
func needsResolverSet(def *ast.Definition) bool {
	if isRootType(def.Name) {
		return len(def.Fields) > 0
	}
	for _, field := range def.Fields {
		if len(field.Arguments) > 0 || forcesResolver(field) {
			return true
		}
	}
	return false
}

// resolverTypes returns the GraphQL types of the SDL files needing resolver sets.
func resolverTypes(files []string) ([]string, error) {
	set := map[string]bool{}
	for _, path := range files {
		if err := collectFileTypes(set, path); err != nil {
			return nil, err
		}
	}
	names := make([]string, 0, len(set))
	for name := range set {
		names = append(names, name)
	}
	sort.Strings(names)
	return names, nil
}

// collectFileTypes records the resolver bearing types of one SDL file.
func collectFileTypes(set map[string]bool, path string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("graphwire: %s: %w", path, err)
	}
	doc, err := parser.ParseSchema(&ast.Source{Name: path, Input: string(data)})
	if err != nil {
		return fmt.Errorf("graphwire: %s: %w", path, err)
	}
	for _, def := range append(doc.Definitions, doc.Extensions...) {
		collectResolverType(set, def)
	}
	return nil
}

// globFiles expands the globs under root, in glob order.
func globFiles(root string, globs []string) ([]string, error) {
	var files []string
	for _, pattern := range globs {
		matches, err := filepath.Glob(filepath.Join(root, filepath.FromSlash(pattern)))
		if err != nil {
			return nil, fmt.Errorf("graphwire: glob %s: %w", pattern, err)
		}
		sort.Strings(matches)
		files = append(files, matches...)
	}
	return files, nil
}

// coreContributor scans the core SDL into its contributor entry.
func coreContributor(root string, cfg Config) (contributor, error) {
	files, err := globFiles(root, cfg.CoreSchemaGlobs)
	if err != nil {
		return contributor{}, err
	}
	if len(files) == 0 {
		return contributor{}, errors.New("graphwire: no core schema files match the configured globs")
	}
	types, err := resolverTypes(files)
	if err != nil {
		return contributor{}, err
	}
	return contributor{
		alias: coreImportName(cfg),
		path:  cfg.CoreImport,
		field: "core",
		param: "core",
		types: types,
	}, nil
}

// coreImportName returns the Go name the generated wiring imports the core package under.
func coreImportName(cfg Config) string {
	return goName(path.Base(cfg.CoreImport))
}

// reservedNames are the Go names the generated wiring owns, keyed by whether it writes a named package.
var reservedNames = map[bool]map[string]bool{
	false: {"core": true, "graph": true, "init": true, "main": true},
	true:  {"core": true, "graph": true, "init": true, "sdk": true, "errors": true, "error": true, "nil": true},
}

// refuseCollision rejects a graphql plugin id whose Go name collides with a name of the generated wiring.
func refuseCollision(cfg Config, id string) error {
	name := goName(id)
	if token.IsKeyword(name) || reservedNames[namingFor(cfg).packageMode][name] || name == coreImportName(cfg) {
		return fmt.Errorf("graphwire: plugin %s: its Go name %s collides with the generated wiring", id, name)
	}
	return nil
}

// pluginContributors scans every graphql flagged plugin into contributor entries.
func pluginContributors(root string, cfg Config, manifests []manifest) ([]contributor, error) {
	var contributors []contributor
	for _, m := range manifests {
		if !m.GraphQL {
			continue
		}
		if err := refuseCollision(cfg, m.ID); err != nil {
			return nil, err
		}
		scanned, err := scanPlugin(root, m)
		if err != nil {
			return nil, err
		}
		contributors = append(contributors, scanned)
	}
	return contributors, nil
}

// scanPlugin reads one graphql plugin's SDL into its contributor entry.
func scanPlugin(root string, m manifest) (contributor, error) {
	files, err := globFiles(root, []string{m.root + "/" + m.ID + "/graph/*.graphqls"})
	if err != nil {
		return contributor{}, err
	}
	if len(files) == 0 {
		return contributor{}, fmt.Errorf("graphwire: plugin %s declares graphql but has no graph/*.graphqls", m.ID)
	}
	types, err := resolverTypes(files)
	if err != nil {
		return contributor{}, err
	}
	if len(types) == 0 {
		return contributor{}, fmt.Errorf("graphwire: plugin %s declares graphql but contributes no resolver types", m.ID)
	}
	name := goName(m.ID)
	return contributor{
		alias: name,
		path:  m.Backend,
		field: name,
		param: name + "Plugin",
		types: types,
	}, nil
}

// Run loads the manifests and SDL under root and writes the resolver root wiring per cfg.
func Run(root string, cfg Config) error {
	if err := validateConfig(cfg); err != nil {
		return err
	}
	manifests, err := loadRoots(root, cfg.roots())
	if err != nil {
		return err
	}
	core, err := coreContributor(root, cfg)
	if err != nil {
		return err
	}
	plugins, err := pluginContributors(root, cfg, manifests)
	if err != nil {
		return err
	}
	return writeWiring(root, cfg, core, plugins)
}

// writeWiring formats and writes the generated resolver root file.
func writeWiring(root string, cfg Config, core contributor, plugins []contributor) error {
	source, err := format.Source(generate(cfg, core, plugins))
	if err != nil {
		return fmt.Errorf("graphwire: formatting the wiring: %w", err)
	}
	path := filepath.Join(root, filepath.FromSlash(cfg.WiringPath))
	if err := os.WriteFile(path, source, 0o644); err != nil {
		return fmt.Errorf("graphwire: %w", err)
	}
	return nil
}
