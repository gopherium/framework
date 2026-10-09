# Changelog

All notable changes to the `dbkit/postgres` module are documented in
this file. The format follows
[Keep a Changelog](https://keepachangelog.com/en/1.1.0/), and the
module follows [Semantic Versioning](https://semver.org/). While at
v0.x, minor releases may contain breaking changes.

Releases of this module are tagged `dbkit/postgres/vX.Y.Z`.

## [Unreleased]

### Added

- `CheckAddress`, which checks a PostgreSQL address before pgx reads it, every refused address marked `dbkit.ErrAddress` and never echoed.
- `CheckAddress` refuses a URL address with more than one `@` before the first `/`, or an `@` there after a `?` or `#`, and says to write `?` as `%3F`, `#` as `%23` and `@` as `%40`.
- `CheckAddress` refuses a raw `#` anywhere in a URL address and says to write `%23`.
- `CheckAddress` refuses a raw space or control character, a `%` that starts no escape or escapes a NUL, and a user or password character pgx 5.10 cannot read, in a URL address.
- `CheckAddress` refuses a raw `+`, a semicolon, a pair with no `=` or two, a key set twice (`dbname` and `database` count as one) and the `ssl` key in the query of a URL address.
- `CheckAddress` refuses a URL host list with an empty host or with a port on some hosts only, and a path whose database name starts with `/`.
- `CheckAddress` refuses a backslash, and an empty host in a `host` list, in a keyword and value address.
- `CheckAddress` refuses an empty or blank address, which pgx would fill from the `PG*` environment variables.
- `CheckAddress` accepts a keyword and value address, such as `host=db user=u`.
- `Options`, with `MaxConns` required, from 2 to 2147483647.
- `Open`, a `Handle` with one `pgxpool.Pool` and a `database/sql` view of it, both under the one cap of `MaxConns`.
- `Open` connects to nothing, the pool keeps no minimum of idle connections, and the view keeps no idle connection of its own.
- `Open` refuses `pool_max_conns`, `pool_min_conns` and `pool_min_idle_conns` in the address and names the key.
- `Open` refuses `pool_health_check_period` and `pool_max_conn_lifetime` at zero or below, and `pool_max_conn_lifetime_jitter` below zero, and names the key but never its value.
- `Open` refuses `pool_ping_timeout`, which pgx 5.10 sends to the server as a setting.
- `Open` refuses an address pgx cannot parse with a fixed message that holds none of pgx's text.
- `Handle.Close`, which closes the view, then the pool.
- `Classify`, which wraps a server error in its `dbkit` error class: 23505 unique, 23503 foreign key, 23502 not null and 23514 check.
- `Classify` reads 55P03, 40001 and 40P01 as `dbkit.ErrBusy` and leaves 57014 (`statement_timeout`) unclassed.
- `Migrate` and `Migrations`, one owner's goose migrations under goose's session advisory lock with its default lock id.
- `Migrate` takes Go migrations only from `Migrations.Go`, never from goose's global registry.
- `Migrate` refuses a nil handle, a nil entry in `Go`, and a `Table` that is not a lowercase plain name of at most 63 bytes, alone or after a schema and a dot.
- `Migrate` refuses a `Table` with a part that is a reserved PostgreSQL keyword, or with a schema that starts with `pg_`.
- `Migrate` creates the absent schema of a schema-qualified `Table` before the version table, and needs no CREATE right on the database when that schema exists.
- `Migrate` names the owner's `Table` in every error of goose.
- `pgtest.New`, the escaped address of a fresh database cut from a template that its migrator builds once per hash.
- `pgtest.New` refuses a keyword and value address, and an address whose host holds a colon, such as an IPv6 literal, marked `dbkit.ErrAddress`.
- `pgtest.New` parses the address with pgx first, and refuses one pgx cannot parse with a fixed message that names no part of it.
- `pgtest.New` refuses `password` and `sslpassword` in the address query, marked `dbkit.ErrAddress`.
- `pgtest.New` refuses `user`, `host`, `port`, `dbname` and `database` in the address query, which would move every test database.
- `pgtest.New` gives pgtestdb the password pgx finds in `PGPASSWORD` or the passfile when the address has none.
- `pgtest.New` keeps every query option of the address, such as `sslmode`, on each test database.
- `pgtest.Migrator`, a pgtestdb migrator built from a hash and a migrate function, whose `Hash` refuses an empty hash or a nil function.
- `pgtest.URL`, the address of a `pgtestdb.Config` with its user, password and database name escaped.
- `pgtest.Sweep`, which drops each pgtestdb instance older than `olderThan` with no session on it, and returns the names it dropped.
- `pgtest.Sweep` refuses an `olderThan` at or below zero, and keeps templates and every database that is not an instance.
- `pgtest.Sweep` runs a plain `DROP DATABASE` and leaves out a database that is gone before its drop.
- `pgtest.Sweep` names the rights it needs when its role may not run `pg_stat_file`.

### Security

- Requires `golang.org/x/text` v0.41.0, which fixes GO-2026-6629.
