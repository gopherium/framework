# Changelog

All notable changes to the `dbkit` module are documented in this
file. The format follows
[Keep a Changelog](https://keepachangelog.com/en/1.1.0/), and the
module follows [Semantic Versioning](https://semver.org/). While at
v0.x, minor releases may contain breaking changes.

Releases of this module are tagged `dbkit/vX.Y.Z`.

## [0.1.0] - 2026-10-07

### Added

- `Engine`, with `Postgres` and `SQLite`.
- `EngineOf` and `SQLitePath`, reading the engine and the SQLite file from an address, every refused address marked `ErrAddress`.
- `ErrBusy`, `ErrUnique`, `ErrForeignKey`, `ErrNotNull` and `ErrCheck`, the error classes each engine's `Classify` returns.
- `Function`, `FunctionList` and `NewFunctionList`, the Go functions SQL may call, and `CaseFold`.
- `Time`, a time stored as UTC microseconds in an `INTEGER` column.
- `Snapshotter`, `Snapshot` and `Checkpoint`, the copy of a live database.
- `Share` and `NewShare`, a capped, timed and counted share of one handle, with `Rows`, `Row` and `Tx`.
- The share rewrites `$N` to `?N` on SQLite and refuses there, with `ErrPlaceholder`, a bare `?`, a `?N` and a `$N` after a named parameter.
- `Rows.Scan` and `Row.Scan` refuse a `*sql.RawBytes` destination.
- `Observer`, `Statement` and `Kind`, seeing every statement before it runs, and `ErrRefused`.
- `QueryBudget`, `QueryCount` and `QueriesIn`, counting statements per request.
- `ErrShareFull`, when no slot frees before the statement deadline.

[0.1.0]: https://github.com/gopherium/framework/releases/tag/dbkit%2Fv0.1.0
