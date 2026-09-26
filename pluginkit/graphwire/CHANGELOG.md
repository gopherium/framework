# Changelog

All notable changes to the `graphwire` module are documented in this
file. The format follows
[Keep a Changelog](https://keepachangelog.com/en/1.1.0/), and the module
follows [Semantic Versioning](https://semver.org/). While at v0.x, minor
releases may contain breaking changes.

Releases of this module are tagged `pluginkit/graphwire/vX.Y.Z`.
Releases up to 0.3.0 were tagged `graphwire/vX.Y.Z` in
`github.com/gopherium/pluginkit`. The module lives beside the
stdlib-only `pluginkit` module so its gqlparser dependency never enters
`pluginkit` itself.

## [Unreleased]

### Changed

- The module moved to `github.com/gopherium/framework/pluginkit/graphwire`.
- The module needs Go 1.27.1.

### Fixed

- `Run` refuses a graphql plugin whose Go name collides with a name of the generated wiring.

## [0.3.0] - 2026-08-14

### Added

- `Config` gains an optional `Roots` list naming the plugin root
  directories scanned in order, defaulting to `plugins`. Each plugin's
  SDL is read from the root its manifest came from, and an id present
  in more than one root is rejected.

## [0.2.1] - 2026-08-07

### Fixed

- Composite resolver structs embedded two sets sharing an unqualified
  type name, such as a core and a plugin `QueryResolvers`, which Go
  rejects as a duplicate field. The generator now names each contributed
  set through a package local type alias before embedding it, so shared
  types compile with any number of contributors.

## [0.2.0] - 2026-08-07

### Added

- `Config.Package` and `Config.SDKImport`, generating into a named
  importable package with exported identifiers instead of package main,
  plus the `FromPlugins` assembler that locates each graphql plugin's
  resolver sets among the registered plugins by type assertion and
  composes the root, failing loudly when a flagged plugin is absent.

## [0.1.0] - 2026-08-07

### Added

- `Run` and `Config`, generating an application's graph resolver root
  from the plugin manifests and SDL under plugins/. Plugins opt in with
  `"graphql": true` in plugin.json and export one `<Type>Resolvers` set
  per GraphQL type they define or extend. The generator parses SDL with
  gqlparser, derives the resolver bearing types the same way gqlgen does
  (root types, argumented fields, and goField forceResolver fields), and
  emits one composite struct per shared type plus the ResolverRoot
  accessors.
