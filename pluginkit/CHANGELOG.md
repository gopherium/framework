# Changelog

Releases of this module are tagged `pluginkit/vX.Y.Z`. Releases up to
0.5.1 were tagged `vX.Y.Z` in `github.com/gopherium/pluginkit`.

## 0.6.0 - 2026-09-26

- The module moved to `github.com/gopherium/framework/pluginkit`.
- The module needs Go 1.27.1.
- `Host.Migrate` applies every `Migrator` in order without starting any plugin.
- `Host.Start` takes a stop grace and refuses one that is not above zero, a breaking change.
- A failed `Host.Start` stops the started plugins within the stop grace, even after its context ends.
- The generated wiring registers every plugin it can and returns one error naming each failure.
- The generated wiring imports the SDK as `sdk`, so an SDK package with another name compiles.
- `wire.Config` gains an optional `Reserved` list of ids no plugin may take.
- `wire.Run` refuses a plugin id the generated Go or TypeScript wiring cannot use as an import name.

## 0.5.0 - 2026-08-14

- `wire.Config` gains optional `GoRegistryPath` and `GoRegistryPackage`
  fields writing an importable registry whose `All` registers every
  plugin, for test code that must compose the full set.

## 0.4.0 - 2026-08-14

- `wire.Config` gains an optional `Roots` list naming the plugin root
  directories scanned in order, defaulting to `plugins`. An id present
  in more than one root is rejected.

## 0.3.0 - 2026-08-05

- New optional `Seeder` capability, `Seed(ctx) error`, for plugins that can
  fill their own schema with development data.
- `Host.Seed` asks every `Seeder` in registration order and stops at the
  first failure, with the same panic isolation as the other host calls.
  Seeding stays outside `Start`, so booting never writes sample data.

## 0.2.0 - 2026-07-26

- `wire.Config` gains an optional `TSLicense` field for applications whose
  generated TypeScript wiring carries a different license than the Go one.
  It defaults to `License` when empty.

## 0.1.0 - 2026-07-26

Initial release:

- Lifecycle contract: `Plugin`, `Migrator`, `RouteProvider`,
  `PublicPathProvider`.
- `Host`: migrate-before-start, in-order start with reverse-order stop,
  rollback on failed start, panic isolation per plugin call.
- `Protect`: exact-match public-path passthrough around caller-supplied
  middleware.
- `wire`: plugin manifest loading/validation and Go + TypeScript wiring
  generation, parameterized by the consuming application.
