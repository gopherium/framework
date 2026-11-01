// SPDX-License-Identifier: Apache-2.0

package postgres

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"slices"

	"github.com/jackc/pgx/v5/pgtype"
)

var (
	// ErrNotSuperuser refuses a hand-over on a connection that is no superuser.
	ErrNotSuperuser = errors.New("dbkit: handing over a database takes a superuser connection")
	// ErrSuperuserRole refuses a hand-over to a role that is a superuser.
	ErrSuperuserRole = errors.New("dbkit: a database goes to a plain role, and this role is a superuser")
)

// ownerQuery reads whether the connection and the role of $1 are superusers, the steps that make that role the owner
// of the connected database and what it holds, and the step that gives it the password of $2.
const ownerQuery = `
WITH site AS (
	SELECT oid, nspname, nspowner FROM pg_namespace
	WHERE nspname !~ '^pg_' AND nspname <> 'information_schema'
), kinds (relkind, word) AS (
	VALUES ('r', 'TABLE'), ('p', 'TABLE'), ('v', 'VIEW'), ('m', 'MATERIALIZED VIEW'), ('f', 'FOREIGN TABLE'),
		('S', 'SEQUENCE')
), steps (place, step) AS (
	SELECT 1, format('CREATE ROLE %I LOGIN', $1::text)
	WHERE NOT EXISTS (SELECT FROM pg_roles WHERE rolname = $1::text)
	UNION ALL
	SELECT 1, format('ALTER ROLE %I LOGIN', $1::text)
	FROM pg_roles WHERE rolname = $1::text AND NOT rolcanlogin
	UNION ALL
	SELECT 2, format('ALTER DATABASE %I OWNER TO %I', datname, $1::text)
	FROM pg_database WHERE datname = current_database() AND pg_get_userbyid(datdba) <> $1::text
	UNION ALL
	SELECT 3, format('ALTER SCHEMA %I OWNER TO %I', nspname, $1::text)
	FROM site WHERE pg_get_userbyid(nspowner) NOT IN ($1::text, 'pg_database_owner')
	UNION ALL
	SELECT 4, format('ALTER %s %I.%I OWNER TO %I', kinds.word, site.nspname, c.relname, $1::text)
	FROM pg_class c
	JOIN site ON site.oid = c.relnamespace
	JOIN kinds ON kinds.relkind = c.relkind::text
	WHERE pg_get_userbyid(c.relowner) <> $1::text
	AND NOT EXISTS (
		SELECT FROM pg_depend d
		WHERE d.classid = 'pg_class'::regclass AND d.objid = c.oid
		AND (d.deptype = 'e' OR (c.relkind = 'S' AND d.deptype IN ('a', 'i')))
	)
	UNION ALL
	SELECT 5, format('ALTER ROUTINE %I.%I(%s) OWNER TO %I', site.nspname, p.proname,
		pg_get_function_identity_arguments(p.oid), $1::text)
	FROM pg_proc p
	JOIN site ON site.oid = p.pronamespace
	WHERE pg_get_userbyid(p.proowner) <> $1::text
	AND NOT EXISTS (
		SELECT FROM pg_depend d WHERE d.classid = 'pg_proc'::regclass AND d.objid = p.oid AND d.deptype = 'e'
	)
	UNION ALL
	SELECT 6, format('ALTER TYPE %I.%I OWNER TO %I', site.nspname, t.typname, $1::text)
	FROM pg_type t
	JOIN site ON site.oid = t.typnamespace
	LEFT JOIN pg_class c ON c.oid = t.typrelid
	WHERE t.typtype IN ('c', 'd', 'e', 'r') AND coalesce(c.relkind::text, 'c') = 'c'
	AND pg_get_userbyid(t.typowner) <> $1::text
	AND NOT EXISTS (
		SELECT FROM pg_depend d WHERE d.classid = 'pg_type'::regclass AND d.objid = t.oid AND d.deptype = 'e'
	)
)
SELECT
	(SELECT rolsuper FROM pg_roles WHERE rolname = current_user),
	coalesce((SELECT rolsuper FROM pg_roles WHERE rolname = $1::text), false),
	ARRAY(SELECT step FROM steps ORDER BY place, step COLLATE "C"),
	CASE WHEN $2::text = '' THEN '{}'::text[] ELSE ARRAY[format('ALTER ROLE %I PASSWORD %L', $1::text, $2::text)] END`

// Owner is the plain role a database goes to.
type Owner struct {
	// Role is the name of the role, created when it is missing.
	Role string
	// Password is the password the role logs in with, left as it is when empty.
	Password string
}

// ownerReader is a connection or a transaction that answers one row.
type ownerReader interface {
	// QueryRowContext runs a query that returns at most one row.
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

// OwnerSteps returns the statements HandOver would run for role on the database of db, and changes nothing.
func OwnerSteps(ctx context.Context, db *sql.DB, role string) ([]string, error) {
	steps, _, err := ownerRead(ctx, db, Owner{Role: role})
	return steps, err
}

// HandOver gives the role of owner the database of db and its schemas, tables, views, sequences, routines and types,
// in one transaction, and returns the statements it ran, none of which holds the password.
func HandOver(ctx context.Context, db *sql.DB, owner Owner) ([]string, error) {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("dbkit: begin the hand-over: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	steps, password, err := ownerRead(ctx, tx, owner)
	if err != nil {
		return nil, err
	}
	for _, step := range slices.Concat(steps, password) {
		if _, err := tx.ExecContext(ctx, step); err != nil {
			return nil, fmt.Errorf("dbkit: hand over the database: %w", err)
		}
	}
	return steps, tx.Commit()
}

// ownerRead returns the steps that hand the database of db to owner and the step that sets its password.
func ownerRead(ctx context.Context, db ownerReader, owner Owner) (steps, password []string, err error) {
	var connected, taken bool
	types := pgtype.NewMap()
	row := db.QueryRowContext(ctx, ownerQuery, owner.Role, owner.Password)
	if err := row.Scan(&connected, &taken, types.SQLScanner(&steps), types.SQLScanner(&password)); err != nil {
		return nil, nil, fmt.Errorf("dbkit: read the owner steps: %w", err)
	}
	switch {
	case !connected:
		return nil, nil, ErrNotSuperuser
	case taken:
		return nil, nil, ErrSuperuserRole
	}
	return steps, password, nil
}
