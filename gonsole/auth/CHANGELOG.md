# Changelog

All notable changes to the `gonsole/auth` module are documented in this
file. The format follows
[Keep a Changelog](https://keepachangelog.com/en/1.1.0/), and the
module follows [Semantic Versioning](https://semver.org/). While at
v0.x, minor releases may contain breaking changes.

Releases of this module are tagged `gonsole/auth/vX.Y.Z`.

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

[0.1.0]: https://github.com/gopherium/framework/releases/tag/gonsole%2Fauth%2Fv0.1.0
