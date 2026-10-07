# dbkit

One database handle per engine, shared by every part of a Go program.

A program opens one `*sql.DB` for its site and hands it to everyone: its
own stores, its accounts, its command line and its plugins. On SQLite
that rule is what keeps the file safe. A second copy of the SQLite
library keeps its own lock state, and code that opens and closes the
database file drops SQLite's locks. Many connections through one copy
of SQLite are safe.

This root module holds what every engine shares: the engine an address
names, the error classes, the function list, the time type for SQLite
columns, the snapshot interface, and the share, a capped and counted
view of the one handle lent to one part of a program. It imports the
standard library only. Each engine lives in its own nested module, so
a program on PostgreSQL alone never builds a SQLite driver.

The guide is at <https://docs.gopherium.org>.

## The import rule

Only dbkit imports a SQLite driver, and only dbkit opens a handle. Give
your program both rules in its `.golangci.yml`. depguard refuses the
drivers, and forbidigo refuses `sql.Open` and `sql.OpenDB`, which need
no driver import to bypass every connection rule.

```yaml
version: "2"

linters:
  enable:
    - depguard
    - forbidigo
  settings:
    depguard:
      rules:
        one-sqlite-driver:
          deny:
            - pkg: modernc.org/sqlite
              desc: open SQLite through github.com/gopherium/framework/dbkit/sqlite
            - pkg: modernc.org/libc
              desc: open SQLite through github.com/gopherium/framework/dbkit/sqlite
            - pkg: github.com/mattn/go-sqlite3
              desc: open SQLite through github.com/gopherium/framework/dbkit/sqlite
            - pkg: github.com/ncruces/go-sqlite3
              desc: open SQLite through github.com/gopherium/framework/dbkit/sqlite
            - pkg: github.com/glebarez/go-sqlite
              desc: open SQLite through github.com/gopherium/framework/dbkit/sqlite
            - pkg: turso.tech/database/tursogo
              desc: open SQLite through github.com/gopherium/framework/dbkit/sqlite
    forbidigo:
      analyze-types: true
      forbid:
        - pattern: ^sql\.Open(DB)?$
          pkg: ^database/sql$
          msg: open the database through dbkit
```

`analyze-types` makes forbidigo catch an aliased import of
`database/sql` too.

## License

Apache-2.0.
