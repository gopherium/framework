# postgresblob

A [gocloud.dev/blob](https://gocloud.dev/howto/blob/) driver that keeps
files in PostgreSQL.

gocloud.dev/blob is one Go API for buckets of files, with drivers for
S3, Google Cloud Storage, Azure, a local folder and memory. postgresblob
adds PostgreSQL, so a program that already runs PostgreSQL can keep its
files there without a second service.

If you know Laravel, gocloud.dev/blob plays the part of Flysystem, and
postgresblob is one more adapter behind it. Flysystem's own adapters
offer no disk made of database tables, and that is what this one is.

The guide is at <https://docs.gopherium.org/files/postgres/>.

## How it stores files

Each object is one row in the table `objects`, and its bytes are rows
in the table `chunks`, 1 MiB each by default. Writes send one chunk at
a time. Each read fetches at most one chunk, or 1 MiB when chunks are
smaller, so a large file never sits whole in memory unless you read it
all at once.

A new object stays hidden until its writer closes. Then it replaces
the old object under that key in one transaction. Writes, copies and
deletes of the same key wait for each other, so `IfNotExist` lets
exactly one of many writers win.

Keys are stored as bytes. Any UTF-8 key of up to 1,024 bytes works, a
NUL byte included, and a listing follows byte order.

## Creating the tables

```go
err := postgresblob.Migrate(ctx, db, "files")
```

`Migrate` takes a `*sql.DB` and creates the schema and both tables
through `dbkit/postgres`, under goose's migration lock. The schema
keeps its own version table, `files.goose_db_version`.

The schema must be a lowercase plain name of up to 63 bytes, made of
letters, digits and underscores, that does not start with a digit. It
must not be a word PostgreSQL reserves, such as `user` or `order`, and
must not start with `pg_`. An empty one means `blob`.

## Opening a bucket on your pool

```go
bucket, err := postgresblob.OpenBucket(ctx, pool, &postgresblob.Options{
	Schema:  "files",
	MaxSize: 25 << 20,
})
if err != nil {
	return err
}
defer bucket.Close()
```

`OpenBucket` takes the program's `*pgxpool.Pool` and never closes it.
It refuses a database whose encoding is not UTF8. Each option left at
zero takes its default:

| Option | Default | Meaning |
| --- | --- | --- |
| `Schema` | `blob` | the schema that holds the tables |
| `ChunkSize` | 1 MiB | the most bytes one chunk row holds, up to `MaxChunkSize` (256 MiB) |
| `MaxSize` | no cap | the most bytes one object may hold |
| `CleanupTimeout` | 10 seconds | how long a failed upload may take to delete its rows |

## Opening a bucket from a URL

```go
import _ "github.com/gopherium/framework/postgresblob"

bucket, err := blob.OpenBucket(ctx, "postgres://app@db.example.com/site?schema=files")
```

The `postgres` scheme opens a pool of its own, and `bucket.Close`
closes it. The opener takes `schema` out of the query and hands the
rest to `pgxpool.ParseConfig`, so pgx settings such as `sslmode` and
`pool_max_conns` work. pgx sends every other query parameter to the
server as a setting, and the server turns down a name it does not
know. go-cloud's own `prefix` parameter works too.

When pgx cannot parse the URL, the error holds no part of it. A URL
that Go's `net/url` cannot parse never reaches the driver, and
go-cloud's error for it repeats the whole URL, password included. So
keep those errors out of shared logs.

## The size cap

With `MaxSize` set, a write or a copy that would take an object past
the cap fails with `gcerrors.ResourceExhausted`. The old object under
that key stays as it was.

## Unfinished uploads

A writer whose context ends, or whose write fails, deletes its own
rows. A program that stops in the middle of an upload leaves a pending
row behind, and `Sweep` deletes those:

```go
deleted, err := postgresblob.Sweep(ctx, pool, postgresblob.SweepOptions{
	Schema:    "files",
	OlderThan: time.Hour,
	Limit:     100,
})
```

`Sweep` deletes at most `Limit` uploads that got no new chunk for
longer than `OlderThan`, the oldest first. Run it from a timer. When it
returns `Limit`, more may be waiting. An upload that `Sweep` deleted
fails with `gcerrors.FailedPrecondition` when it writes its next chunk
or closes.

## Errors

| Code | When |
| --- | --- |
| `NotFound` | no object has the key, or the schema has no tables (42P01) |
| `FailedPrecondition` | `IfNotExist` found an object, the object changed during a read, `Sweep` deleted the upload, or 23505 |
| `InvalidArgument` | an empty key, a key over 1,024 bytes, or 22021 |
| `ResourceExhausted` | a write or copy past `MaxSize`, the class 53, or 54000 |
| `PermissionDenied` | 42501 |
| `Canceled` | a canceled context, or 57014 |
| `Unimplemented` | `SignedURL` |
| `Unknown` | every other error |

Since a schema with no tables answers `NotFound`, call
`bucket.IsAccessible` when your program starts.

## As

`As` answers false on the bucket, its readers, attributes and list
objects. `ErrorAs` sets a `*pgconn.PgError` when the error came from
the server.

## What is not supported

- `SignedURL` answers `gcerrors.Unimplemented`.
- A read takes no snapshot. When another write replaces or deletes the
  object during a read, the read fails with
  `gcerrors.FailedPrecondition`, and never returns short data.
- Files grow the database and its backups, and the chunks are stored
  without compression.
- A large `Copy` runs as one long transaction, and writes to its
  source or its destination wait until it ends.

## Testing

The tests need a PostgreSQL server where they may create databases.
`compose.yaml` starts one on port 5434:

```sh
docker compose up -d
DBKIT_TEST_POSTGRES_URL='postgres://postgres:postgres@localhost:5434/postgres?sslmode=disable' go test ./...
```

They run go-cloud's `drivertest` three ways: plain, through
`driver.NewPrefixedBucket`, and with 7 byte chunks so the small test
files cross chunk edges.

## License

Apache-2.0.
