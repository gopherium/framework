# Changelog

All notable changes to the `gonsole/auth/postgres` module are documented
in this file. The format follows
[Keep a Changelog](https://keepachangelog.com/en/1.1.0/), and the
module follows [Semantic Versioning](https://semver.org/). While at
v0.x, minor releases may contain breaking changes.

Releases of this module are tagged `gonsole/auth/postgres/vX.Y.Z`.

## [Unreleased]

### Changed

- The module needs Go 1.27.3.

## [0.2.0] - 2026-10-14

### Changed

- **Breaking.** Requires `gonsole/auth` 0.8.0.

### Fixed

- The record store answers each record's time in UTC, so `account:records -json` prints a UTC time, as on SQLite.

## [0.1.0] - 2026-10-12

### Added

- `Stores`, the account and record stores of a call on a pool of the database setting, closed after the call, for `auth.Config.Stores`.
- `Migration` and `RecordMigration`, the two schema steps, each under goose's migration lock.
- The records keep the version table of `gonsole/auth`, `gonsole.goose_db_version`.
- `NewRecordStore`, the record store of the `gonsole.records` table, which lists no record for a limit below zero.
- A constraint, lock or serialization failure of the record store or the records step carries dbkit's error class, such as `dbkit.ErrUnique`.
- An address pgx cannot parse fails `Stores` and the records step with one message that holds no part of the address.

[0.2.0]: https://github.com/gopherium/framework/releases/tag/gonsole%2Fauth%2Fpostgres%2Fv0.2.0
[0.1.0]: https://github.com/gopherium/framework/releases/tag/gonsole%2Fauth%2Fpostgres%2Fv0.1.0
