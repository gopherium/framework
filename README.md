# Gopherium Framework

**The gopherium brick shelf.** Self contained building bricks for Go
applications, one separately versioned module per brick. Each brick is
small, framework-free, and usable on its own. You pin the modules you
need and ignore the rest.

> **Stability: v0.** Module APIs may change between minor releases
> while they mature. Pin a version and read the module's CHANGELOG
> before upgrading. Production use is at your own risk until v1.

## Modules

- [`dbkit`](dbkit/) names the database engines, their error classes and
  shared types, and lends capped shares of a program's one database
  handle.
- [`dbkit/postgres`](dbkit/postgres/) opens a program's one PostgreSQL
  pool with a `database/sql` view on the same cap, classifies its
  errors, runs its migrations and ships test helpers.
- [`dbkit/sqlite`](dbkit/sqlite/) opens a program's one SQLite handle
  with every connection rule on, classifies its errors and ships test
  helpers.
- [`gonsole`](gonsole/) runs the command line of a Go program built from
  core commands, settings and compiled plugins.
- [`gonsole/auth`](gonsole/auth/) offers the account commands of a program
  whose accounts live in gouncer's Postgres store.
- [`gottext`](gottext/) reads, writes and syncs gettext catalogs for
  TypeScript applications, published to npm as `@gopherium/gottext`.
- [`mailkit`](mailkit/) renders mail from template files and sends it
  over SMTP.
- [`pluginkit`](pluginkit/) migrates, starts and stops the compiled
  plugins of an application, guards their routes, and generates their
  wiring.
- [`pluginkit/graphwire`](pluginkit/graphwire/) generates the GraphQL
  resolver root of an application from the schemas of its plugins.

## Design

One repository, one self-contained brick per directory. A Go brick
carries its own go.mod and a TypeScript brick its own package.json,
each with its own CHANGELOG and lint configuration, released
independently under a path-prefixed tag such as `mailkit/v0.1.0` or
`gottext/v0.4.0`. Bricks share code only through those published tags,
never through sibling source, so what you pin is what you get.

## Reporting security issues

See [SECURITY.md](SECURITY.md). Do not open public issues for
vulnerabilities.

## License

Apache-2.0. Copyright © 2026 Manuel 'SirLouen' Camargo.
