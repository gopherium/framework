// SPDX-License-Identifier: Apache-2.0

package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/pressly/goose/v3"
)

// Migrations are the migrations of one owner and how a run of them waits for the migration lock.
type Migrations struct {
	// Table is the owner's version table, a plain identifier that is no SQLite keyword and starts with no sqlite_.
	Table string
	// FS holds the owner's SQL migration files at its root, and nil means none.
	FS fs.FS
	// Go are the owner's Go migrations, each built with goose.NewGoMigration.
	Go []*goose.Migration
	// LockWait is how long a run waits for the migration lock, above zero.
	LockWait time.Duration
	// LockPoll is how often a waiting run tries the migration lock, above zero.
	LockPoll time.Duration
}

const (
	// mainPath reads the path of the main database file of a connection.
	mainPath = "SELECT file FROM pragma_database_list WHERE name = 'main'"
	// lockSuffix ends the name of the migration lock file beside a database file.
	lockSuffix = ".migrate.lock"
	// migrationLockName names the migration lock in its errors.
	migrationLockName = "migration lock"
	// reservedPrefix starts every table name SQLite keeps for itself, in upper case.
	reservedPrefix = "SQLITE_"
)

var (
	// plainIdentifier matches a name of ASCII letters, digits and underscores that starts with no digit.
	plainIdentifier = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)
	// keywords are the SQLite keywords in upper case.
	keywords = []string{
		"ABORT", "ACTION", "ADD", "AFTER", "ALL", "ALTER", "ALWAYS", "ANALYZE", "AND", "AS", "ASC", "ATTACH",
		"AUTOINCREMENT", "BEFORE", "BEGIN", "BETWEEN", "BY", "CASCADE", "CASE", "CAST", "CHECK", "COLLATE",
		"COLUMN", "COMMIT", "CONFLICT", "CONSTRAINT", "CREATE", "CROSS", "CURRENT", "CURRENT_DATE", "CURRENT_TIME",
		"CURRENT_TIMESTAMP", "DATABASE", "DEFAULT", "DEFERRABLE", "DEFERRED", "DELETE", "DESC", "DETACH",
		"DISTINCT", "DO", "DROP", "EACH", "ELSE", "END", "ESCAPE", "EXCEPT", "EXCLUDE", "EXCLUSIVE", "EXISTS",
		"EXPLAIN", "FAIL", "FILTER", "FIRST", "FOLLOWING", "FOR", "FOREIGN", "FROM", "FULL", "GENERATED", "GLOB",
		"GROUP", "GROUPS", "HAVING", "IF", "IGNORE", "IMMEDIATE", "IN", "INDEX", "INDEXED", "INITIALLY", "INNER",
		"INSERT", "INSTEAD", "INTERSECT", "INTO", "IS", "ISNULL", "JOIN", "KEY", "LAST", "LEFT", "LIKE", "LIMIT",
		"MATCH", "MATERIALIZED", "NATURAL", "NO", "NOT", "NOTHING", "NOTNULL", "NULL", "NULLS", "OF", "OFFSET",
		"ON", "OR", "ORDER", "OTHERS", "OUTER", "OVER", "PARTITION", "PLAN", "PRAGMA", "PRECEDING", "PRIMARY",
		"QUERY", "RAISE", "RANGE", "RECURSIVE", "REFERENCES", "REGEXP", "REINDEX", "RELEASE", "RENAME", "REPLACE",
		"RESTRICT", "RETURNING", "RIGHT", "ROLLBACK", "ROW", "ROWS", "SAVEPOINT", "SELECT", "SET", "TABLE", "TEMP",
		"TEMPORARY", "THEN", "TIES", "TO", "TRANSACTION", "TRIGGER", "UNBOUNDED", "UNION", "UNIQUE", "UPDATE",
		"USING", "VACUUM", "VALUES", "VIEW", "VIRTUAL", "WHEN", "WHERE", "WINDOW", "WITH", "WITHOUT",
	}
	// errNoHandle refuses a nil handle.
	errNoHandle = errors.New("dbkit: Migrate needs a database handle, got nil")
	// errNoFile refuses a handle whose main database has no file.
	errNoFile = errors.New("dbkit: Migrate needs a database file, and the handle has none")
)

// fileLocker holds an exclusive lock on one lock file.
type fileLocker struct {
	// name names the lock in its errors.
	name string
	// path is the path of the lock file.
	path string
	// database is the path of the database file whose owner a run as root gives a lock file it creates.
	database string
	// wait is how long lock waits for the lock.
	wait time.Duration
	// poll is how often lock tries the lock while it waits.
	poll time.Duration
	// file is the open lock file while the lock is held.
	file *os.File
}

// unlock releases the lock and keeps the lock file.
func (l *fileLocker) unlock() error {
	return l.file.Close()
}

// noLocker is a goose session locker that locks nothing.
type noLocker struct{}

// SessionLock locks nothing.
func (noLocker) SessionLock(context.Context, *sql.Conn) error {
	return nil
}

// SessionUnlock unlocks nothing.
func (noLocker) SessionUnlock(context.Context, *sql.Conn) error {
	return nil
}

// check returns the error for the first option Migrate refuses.
func (m Migrations) check() error {
	switch {
	case m.Table == "":
		return errors.New("dbkit: the option Table must name the version table")
	case !plainTable(m.Table):
		return fmt.Errorf("dbkit: the option Table must be a plain identifier of ASCII letters, digits and "+
			"underscores, got %q", m.Table)
	case m.LockWait <= 0:
		return fmt.Errorf("dbkit: the option LockWait must stand above zero, got %v", m.LockWait)
	case m.LockPoll <= 0:
		return fmt.Errorf("dbkit: the option LockPoll must stand above zero, got %v", m.LockPoll)
	}
	if i := slices.Index(m.Go, nil); i >= 0 {
		return fmt.Errorf("dbkit: the option Go holds nil at index %d", i)
	}
	return nil
}

// plainTable reports whether name is a plain identifier that is no SQLite keyword and has no reserved prefix.
func plainTable(name string) bool {
	upper := strings.ToUpper(name)
	return plainIdentifier.MatchString(name) && !slices.Contains(keywords, upper) &&
		!strings.HasPrefix(upper, reservedPrefix)
}

// checkTransactions returns the error for the first SQL migration of fsys that goose would run outside a transaction.
func checkTransactions(fsys fs.FS) error {
	if fsys == nil {
		return nil
	}
	names, err := fs.Glob(fsys, "*.sql")
	if err != nil {
		return fmt.Errorf("dbkit: list the SQL migrations: %w", err)
	}
	for _, name := range names {
		text, err := fs.ReadFile(fsys, name)
		if err != nil {
			return fmt.Errorf("dbkit: read the SQL migration %s: %w", name, err)
		}
		if noTransaction(string(text)) {
			return fmt.Errorf("dbkit: the SQL migration %s is marked NO TRANSACTION, "+
				"and Migrate runs every SQL migration in one transaction", name)
		}
	}
	return nil
}

// noTransaction reports whether text holds a line goose reads as its NO TRANSACTION annotation.
func noTransaction(text string) bool {
	for line := range strings.Lines(text) {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "--") || !strings.Contains(line, "+goose") {
			continue
		}
		annotation := strings.Replace(strings.ReplaceAll(line, "--", ""), "+goose", "", 1)
		if strings.EqualFold(strings.TrimSpace(annotation), "NO TRANSACTION") {
			return true
		}
	}
	return false
}

// databasePath returns the path of the main database file of db.
func databasePath(ctx context.Context, db *sql.DB) (string, error) {
	var path string
	if err := db.QueryRowContext(ctx, mainPath).Scan(&path); err != nil {
		return "", fmt.Errorf("dbkit: read the database path: %w", err)
	}
	if path == "" {
		return "", errNoFile
	}
	return path, nil
}

// Migrate applies every pending migration of m to the database of db while it holds the lock file beside it.
func Migrate(ctx context.Context, db *sql.DB, m Migrations) (err error) {
	if db == nil {
		return errNoHandle
	}
	if err := m.check(); err != nil {
		return err
	}
	if err := checkTransactions(m.FS); err != nil {
		return err
	}
	path, err := databasePath(ctx, db)
	if err != nil {
		return err
	}
	provider, err := goose.NewProvider(goose.DialectSQLite3, db, m.FS,
		goose.WithTableName(m.Table),
		goose.WithDisableGlobalRegistry(true),
		goose.WithGoMigrations(m.Go...),
		goose.WithSessionLocker(noLocker{}),
	)
	if err != nil {
		return fmt.Errorf("dbkit: build the migration runner of %s: %w", m.Table, err)
	}
	locker := &fileLocker{
		name: migrationLockName, path: path + lockSuffix, database: path, wait: m.LockWait, poll: m.LockPoll,
	}
	if err := locker.lock(ctx); err != nil {
		return fmt.Errorf("dbkit: run the migrations of %s: %w", m.Table, err)
	}
	defer func() { err = errors.Join(err, locker.unlock()) }()
	if _, err := provider.Up(ctx); err != nil {
		return fmt.Errorf("dbkit: run the migrations of %s: %w", m.Table, err)
	}
	return nil
}
