# Changelog

All notable changes to the `gonsole/auth` module are documented in this
file. The format follows
[Keep a Changelog](https://keepachangelog.com/en/1.1.0/), and the
module follows [Semantic Versioning](https://semver.org/). While at
v0.x, minor releases may contain breaking changes.

Releases of this module are tagged `gonsole/auth/vX.Y.Z`.

## [Unreleased]

### Changed

- `account:create-admin` and `account:grant-role` refuse a missing or blank flag before a schema step or account check.
- Requires `gonsole` 0.6.0.

## [0.3.0] - 2026-10-03

### Changed

- `account:role` and `account:grant-role` refuse a role that carries a capability the acting account's role lacks.
- `account:role`, `account:disable` and `account:enable` refuse an account whose role carries a capability the acting account's role lacks.
- `account:disable` refuses an acting account that disables itself.
- `account:role` refuses an acting account that changes its own role.
- A guarded account command fails when `-as` is blank or names no account, under any `Authorize`.
- A guarded account command can no longer give or change a role that carries a capability no role holding `Config.Capability` carries.

## [0.2.1] - 2026-10-01

### Fixed

- `RecordMigration` no longer fails when two processes create the `gonsole` schema at once.
- `Migration` applies gouncer's account schema under goose's migration lock.

### Changed

- Requires `authkit/postgres` 0.11.2.

## [0.2.0] - 2026-09-30

### Added

- `Authorize`, refusing an acting account that is unknown, disabled, never activated or whose role lacks the capability.
- `Record`, storing each applied guarded command with its actor, arguments and flags.
- `RecordMigration`, the schema step that applies the `gonsole.records` table under goose's migration lock.
- `Records`, the command `account:records`, listing the latest records or one JSON document.
- `Roles.Capabilities`, the capabilities each role carries.
- `Config.RecordTimeout`, `Config.RecordsLimit` and `Config.Validate`, read with the `COMMAND_RECORD_TIMEOUT` and `COMMAND_RECORDS_LIMIT` settings.

## [0.1.0] - 2026-09-25

### Added

- `Migration`, the schema step that applies gouncer's account schema.
- `Account` and `EnsureAccounts`, creating each demo account unless its address is taken.
- `Config` and `Roles`, the capability and the role vocabulary a program hands its account commands.
- `account:create-admin`, creating one account under a known role, its password read from stdin.
- `account:grant-role`, giving a known role to every account holding none, a dry run until `-yes`.
- `account:list`, listing every account with its role and standing, or one JSON document.
- `account:role`, setting one account's role, a dry run until `-yes`.
- `account:disable` and `account:enable`, changing whether one account may log in, each a dry run until `-yes`.
- The last enabled account under a privileged role is never demoted or disabled.
- Every command finds an account by its address trimmed and in lower case.
- `Commands`, every account command in the order it is declared.

[0.3.0]: https://github.com/gopherium/framework/releases/tag/gonsole%2Fauth%2Fv0.3.0
[0.2.1]: https://github.com/gopherium/framework/releases/tag/gonsole%2Fauth%2Fv0.2.1
[0.2.0]: https://github.com/gopherium/framework/releases/tag/gonsole%2Fauth%2Fv0.2.0
[0.1.0]: https://github.com/gopherium/framework/releases/tag/gonsole%2Fauth%2Fv0.1.0
