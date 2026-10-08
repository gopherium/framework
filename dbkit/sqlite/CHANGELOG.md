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
- `Migrate` and `Migrations`, one owner's goose migrations under a lock file beside the database, with `Table`, `LockWait` and `LockPoll` required.
- `Migrate` takes Go migrations only from `Migrations.Go`, never from goose's global registry.
- `Migrate` refuses a SQL migration marked `-- +goose NO TRANSACTION`, naming the file, before any migration runs.
- The migration lock waits up to `LockWait`, ends with the context, and is gone when its holder dies. Its file has mode 0600 and is never deleted.
- `Migrate` takes the migration lock before goose uses the handle, so a waiting run holds no connection and a first run waits for the lock.
- `Migrate` refuses a `Table` that is a SQLite keyword or starts with `sqlite_`, in any case.
- A run as root gives the owner and group of the database file only to a migration or snapshot lock file it creates.
- `Rebuild`, a table rebuild with foreign keys off from a goose `RunDB` migration, skipped when `done` finds the new shape in place.
- `Rebuild` fails on any row `PRAGMA foreign_key_check` reports, and never returns a connection with foreign keys off to the pool.
- After its context ends, `Rebuild` rolls back before foreign keys go back on, and its connection returns to the pool.
- `NewSnapshotter`, a copy through `VACUUM INTO`, checked and moved into place, then a `PRAGMA wal_checkpoint(TRUNCATE)` it reports.
- `Snapshot` refuses a target that exists, is relative, differs from its `filepath.Clean` form, starts with `file:`, holds `?` or ends with `.dbkit-snapshot.partial` or `.dbkit-snapshot.partial-journal`.
- `Snapshot` removes only the regular `.dbkit-snapshot.partial` and `.dbkit-snapshot.partial-journal` files left in its folder, never the live database file.
- `Snapshot` holds a `.dbkit-snapshot.lock` file in its folder and fails with `ErrSnapshotRunning` while another snapshot holds it.
- Both locks fail on a lock path that holds a hard link, a FIFO, a device or anything but a regular file with one link.
- A snapshot is never readable more widely than the live database file.
- The final move of a snapshot never replaces a file that appeared at the target during the copy.
- `sqlitetest.NewTemplate`, a file migrated once, with `Template.Open`, `Template.OpenWithFaults` and `Template.Close`.
- `NewTemplate` closes its handle and removes its folder when migrate fails, panics or stops its goroutine.
- `Faults.Pass`, which stops failing a statement chosen with `FailStatement`.
