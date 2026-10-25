# Changelog

All notable changes to the `postgresblob` module are documented in this
file. The format follows
[Keep a Changelog](https://keepachangelog.com/en/1.1.0/), and the
module follows [Semantic Versioning](https://semver.org/). While at
v0.x, minor releases may contain breaking changes.

Releases of this module are tagged `postgresblob/vX.Y.Z`.

## [0.1.0] - 2026-10-25

### Added

- A driver of `driver.Bucket` from gocloud.dev/blob v0.47.0 that keeps each object in two PostgreSQL tables of one schema.
- `OpenBucket`, a bucket on a `*pgxpool.Pool` it never closes, refused when the database encoding is not UTF8.
- `Options` with `Schema`, `ChunkSize`, `MaxSize` and `CleanupTimeout`, each zero field taking its default.
- `MaxChunkSize`, 256 MiB, the largest `ChunkSize` a bucket takes.
- `Migrate`, which creates the schema and its `objects` and `chunks` tables through `dbkit/postgres` under goose's lock.
- Writes stream in chunks of 1 MiB by default and stay hidden until `Close`, which replaces the old object in one transaction.
- Writes, copies and deletes take a transaction lock of each key they touch, so `IfNotExist` lets exactly one of many racing writers win.
- Keys are bytes, so any UTF-8 key up to `MaxKeyBytes` (1,024 bytes) works, NUL included, and listings follow byte order.
- Each `Read` runs one statement for at most one chunk, or 1 MiB when chunks are smaller.
- A read whose object was replaced or deleted fails with `FailedPrecondition`.
- `MaxSize` caps each object, and a write or copy past it fails with `ResourceExhausted` and keeps the old object.
- A writer whose context ends or whose write fails deletes its pending row within `CleanupTimeout`.
- `Sweep` and `SweepOptions`, which delete at most `Limit` unfinished uploads untouched for longer than `OlderThan`.
- An upload `Sweep` deleted fails with `FailedPrecondition` at its next chunk or at `Close`.
- `Copy` in one transaction, onto the same key too.
- `URLOpener` on the `postgres` scheme, which takes `schema` out of the query and closes the pool it opened.
- A URL pgx cannot parse fails with one message that holds no part of the URL.
- `ErrorCode` names 42P01, 23505, 42501, 22021, the 53 class, 54000 and 57014, and `ErrorAs` exposes `*pgconn.PgError`.
- `SignedURL` answers `Unimplemented`.
- The tests run go-cloud's `drivertest` unmodified three ways: plain, through `driver.NewPrefixedBucket` and with 7 byte chunks.

### Security

- Requires `golang.org/x/net` v0.60.0, which fixes GO-2026-6603, GO-2026-6610, GO-2026-6611, GO-2026-6612 and GO-2026-6617.

[0.1.0]: https://github.com/gopherium/framework/releases/tag/postgresblob%2Fv0.1.0
