# Changelog

All notable changes to the `dbkit/sqlite` module are documented in
this file. The format follows
[Keep a Changelog](https://keepachangelog.com/en/1.1.0/), and the
module follows [Semantic Versioning](https://semver.org/). While at
v0.x, minor releases may contain breaking changes.

Releases of this module are tagged `dbkit/sqlite/vX.Y.Z`.

## [Unreleased]

### Added

- `Open`, one SQLite handle from a `sqlite:` address, which connects to nothing until the first query.
- `Options`, with `BusyTimeout`, `CacheSize`, `MaxConns` and `Synchronous` required, each bad value refused by name.
- `Synchronous`, with `SynchronousNormal` and `SynchronousFull`.
- Every connection writes through `BEGIN IMMEDIATE`, in WAL mode, with foreign keys and defensive mode on.
- A plain `time.Time` argument, or one a function returns, is stored as `INTEGER` UTC microseconds.
- `Open` refuses a relative path without `BaseFolder`, a missing file without `Create`, and a link to a missing file.
- `Open` refuses a database file or folder on a network file system.
- `Options.Functions` live on one private driver value per list, never on the shared `sqlite` name.
- A panic in one of `Options.Functions` fails its statement with an error naming the function.
- Each new connection runs `PRAGMA optimize` and notes a busy answer to `Options.Logger`.
- `Open` switches SQLite to OFD locks where Linux allows it.
- `Open` runs on Linux and macOS, and refuses every database on other systems.
- `Classify`, which wraps a SQLite error in its `dbkit` error class.
- `LibcVersion`, the pinned `modernc.org/libc` version.
- `sqlitetest.Open`, a test handle on a fresh file with every rule on, closed when the test ends.
- `sqlitetest.OpenWithFaults`, a test handle whose connections answer with the failures of a `Faults`.
- `sqlitetest.Faults`, the failures a handle from `OpenWithFaults` answers with.
- `Faults.FailStatement`, which fails every statement with the exact chosen text, inside transactions too.
- `Faults.FailNextCommit`, which rolls the next commit back and answers the chosen error once.
- `sqlitetest.CheckLibc`, which fails a test when the build's `modernc.org/libc` differs from `LibcVersion`.
