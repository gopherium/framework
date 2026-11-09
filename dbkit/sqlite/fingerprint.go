// SPDX-License-Identifier: Apache-2.0

package sqlite

import (
	"cmp"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"hash"
	"math"
	"slices"
	"strings"
)

const (
	// fingerprintVersion opens the hash of every fingerprint.
	fingerprintVersion = "dbkit fingerprint 1"
	// notInternal keeps out the objects SQLite keeps for itself, all but sqlite_sequence.
	notInternal = "(name NOT LIKE 'sqlite\\_%' ESCAPE '\\' OR name = 'sqlite_sequence')"
	// schemaRead reads each object of the main schema, in name order.
	schemaRead = "SELECT type, name, tbl_name, ifnull(sql, '') FROM main.sqlite_schema WHERE " + notInternal +
		" ORDER BY name, type"
	// tableRead reads each table of the main schema with whether it has no rowid and its type, in name order.
	tableRead = "SELECT name, wr, type FROM pragma_table_list WHERE schema = 'main' " +
		"AND type IN ('table', 'shadow', 'virtual') AND " + notInternal + " ORDER BY name"
	// columnRead reads the columns of the table its one argument names with their key position and hidden kind, in order.
	columnRead = "SELECT name, pk, hidden FROM pragma_table_xinfo(?, 'main') ORDER BY cid"
	// virtualType is the type pragma_table_list gives a virtual table.
	virtualType = "virtual"
	// shadowType is the type pragma_table_list gives a table that holds the data of a virtual table.
	shadowType = "shadow"
)

var (
	// rowAliases are the names of the rowid of a table, in the order Fingerprint tries them.
	rowAliases = []string{"rowid", "_rowid_", "oid"}
	// walked runs after Fingerprint reads each table, with the table's name.
	walked = func(string) {}
	// errNoFingerprintHandle refuses a fingerprint of a nil handle.
	errNoFingerprintHandle = errors.New("dbkit: Fingerprint needs a database handle, got nil")
)

// Leave names the tables and the rows Fingerprint leaves out, by names whose ASCII letters match in any case.
type Leave struct {
	// Tables names the tables whose rows, not CREATE text, Fingerprint leaves out, a virtual table for its shadow tables.
	Tables []string
	// Rows names rows Fingerprint leaves out by the value of one column.
	Rows []LeaveRows
}

// LeaveRows names the rows of one table whose column holds one of some values.
type LeaveRows struct {
	// Table is the table that holds the rows, never a virtual table.
	Table string
	// Column is the column whose value decides, where a NULL value never matches.
	Column string
	// Values are the values of Column whose rows Fingerprint leaves out, where NULL matches no row.
	Values []any
}

// fingerprintTable is one table Fingerprint reads.
type fingerprintTable struct {
	// name is the table's name.
	name string
	// withoutRowid reports that the table has no rowid.
	withoutRowid bool
	// virtual reports that the table is a virtual table, whose rows its shadow tables hold.
	virtual bool
	// shadow reports that the table holds the data of a virtual table.
	shadow bool
}

// fingerprintColumn is one column of a table Fingerprint reads.
type fingerprintColumn struct {
	// name is the column's name.
	name string
	// key is the column's position in the primary key, or 0 outside it.
	key int64
	// generated reports that the column takes its value from an expression.
	generated bool
}

// statement is one query and its arguments.
type statement struct {
	// text is the query.
	text string
	// args are the arguments of the query.
	args []any
}

// fingerprintRead is the transaction one fingerprint reads in and the hash it writes to.
type fingerprintRead struct {
	// tx is the read-only transaction of the read.
	tx *sql.Tx
	// h is the hash of the fingerprint.
	h hash.Hash
}

// Fingerprint returns the SHA-256, in lowercase hex, of the schema and rows of the main database of db without leave.
func Fingerprint(ctx context.Context, db *sql.DB, leave Leave) (string, error) {
	if db == nil {
		return "", errNoFingerprintHandle
	}
	tx, err := db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return "", fmt.Errorf("dbkit: begin the fingerprint read: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	r := fingerprintRead{tx: tx, h: sha256.New()}
	writeText(r.h, fingerprintVersion)
	if err := r.hashSchema(ctx); err != nil {
		return "", fmt.Errorf("dbkit: read the schema for the fingerprint: %w", err)
	}
	tables, err := r.readTables(ctx)
	if err != nil {
		return "", fmt.Errorf("dbkit: read the tables for the fingerprint: %w", err)
	}
	if err := leave.check(tables); err != nil {
		return "", err
	}
	tables = slices.DeleteFunc(tables, func(table fingerprintTable) bool { return table.virtual })
	for _, table := range tables {
		if err := r.hashTable(ctx, table, leave); err != nil {
			return "", err
		}
		walked(table.name)
	}
	return hex.EncodeToString(r.h.Sum(nil)), nil
}

// check returns the error for a table leave names that tables does not hold, or rows it names in a virtual table.
func (l Leave) check(tables []fingerprintTable) error {
	for _, name := range l.Tables {
		if _, err := findTable(tables, name); err != nil {
			return err
		}
	}
	return l.checkRows(tables)
}

// checkRows returns the error for rows Rows names in a table tables does not hold, or in a virtual table.
func (l Leave) checkRows(tables []fingerprintTable) error {
	for _, rows := range l.Rows {
		table, err := findTable(tables, rows.Table)
		if err != nil {
			return err
		}
		if table.virtual {
			return fmt.Errorf("dbkit: the fingerprint cannot leave out rows of the virtual table %q, "+
				"leave out the whole table instead", rows.Table)
		}
	}
	return nil
}

// findTable returns the table of tables named name, or the error that tables holds none.
func findTable(tables []fingerprintTable, name string) (fingerprintTable, error) {
	i := slices.IndexFunc(tables, func(table fingerprintTable) bool { return sameName(table.name, name) })
	if i < 0 {
		return fingerprintTable{}, fmt.Errorf("dbkit: the fingerprint leaves out the table %q, "+
			"which the database does not hold", name)
	}
	return tables[i], nil
}

// leavesTable reports whether Tables names table, or the virtual table whose data table holds.
func (l Leave) leavesTable(table fingerprintTable) bool {
	if l.namesTable(table.name) {
		return true
	}
	return table.shadow && l.namesTable(shadowOwner(table.name))
}

// namesTable reports whether Tables names the table named table.
func (l Leave) namesTable(table string) bool {
	return slices.ContainsFunc(l.Tables, func(name string) bool { return sameName(name, table) })
}

// shadowOwner returns the name of the virtual table whose data the shadow table named shadow holds.
func shadowOwner(shadow string) string {
	return shadow[:max(strings.LastIndexByte(shadow, '_'), 0)]
}

// hashSchema writes each object of the main schema to the hash.
func (r fingerprintRead) hashSchema(ctx context.Context) error {
	var kind, name, table, text string
	return r.eachRow(ctx, statement{text: schemaRead}, []any{&kind, &name, &table, &text}, func() {
		writeTag(r.h, 'S')
		for _, field := range []string{kind, name, table, text} {
			writeText(r.h, field)
		}
	})
}

// readTables returns each table, shadow table and virtual table of the main schema, in name order.
func (r fingerprintRead) readTables(ctx context.Context) ([]fingerprintTable, error) {
	var tables []fingerprintTable
	var name, kind string
	var withoutRowid int64
	err := r.eachRow(ctx, statement{text: tableRead}, []any{&name, &withoutRowid, &kind}, func() {
		tables = append(tables, fingerprintTable{
			name: name, withoutRowid: withoutRowid != 0, virtual: kind == virtualType, shadow: kind == shadowType,
		})
	})
	return tables, err
}

// readColumns returns the columns of the table named table, generated ones too, in order.
func (r fingerprintRead) readColumns(ctx context.Context, table string) ([]fingerprintColumn, error) {
	var columns []fingerprintColumn
	var column fingerprintColumn
	var hidden int64
	read := statement{text: columnRead, args: []any{table}}
	err := r.eachRow(ctx, read, []any{&column.name, &column.key, &hidden}, func() {
		column.generated = hidden != 0
		columns = append(columns, column)
	})
	return columns, err
}

// hashTable writes the name, the columns and the rows of table to the hash, without the rows leave keeps out.
func (r fingerprintRead) hashTable(ctx context.Context, table fingerprintTable, leave Leave) error {
	columns, err := r.readColumns(ctx, table.name)
	if err != nil {
		return fmt.Errorf("dbkit: read the columns of %q for the fingerprint: %w", table.name, err)
	}
	stored := storedColumns(columns)
	r.hashColumns(table.name, stored)
	filter, err := leaveFilter(table.name, columns, leave.Rows)
	if err != nil {
		return err
	}
	if leave.leavesTable(table) {
		return nil
	}
	order, err := rowOrder(table, columns)
	if err != nil {
		return err
	}
	read, width := rowRead(table, order, stored, filter)
	if err := r.hashRows(ctx, read, width); err != nil {
		return fmt.Errorf("dbkit: read the rows of %q for the fingerprint: %w", table.name, err)
	}
	return nil
}

// hashColumns writes the name of the table named table and the names of its columns to the hash.
func (r fingerprintRead) hashColumns(table string, columns []fingerprintColumn) {
	writeTag(r.h, 'T')
	writeText(r.h, table)
	writeCount(r.h, len(columns))
	for _, column := range columns {
		writeText(r.h, column.name)
	}
}

// storedColumns returns the columns of columns that take no value from an expression.
func storedColumns(columns []fingerprintColumn) []fingerprintColumn {
	return slices.DeleteFunc(slices.Clone(columns), func(c fingerprintColumn) bool { return c.generated })
}

// hashRows writes each row read reads to the hash, as a storage class and a raw value for each of width columns.
func (r fingerprintRead) hashRows(ctx context.Context, read statement, width int) error {
	kinds := make([]string, width)
	raws := make([]any, width)
	dest := make([]any, 0, 2*width)
	for i := range width {
		dest = append(dest, &kinds[i], &raws[i])
	}
	return r.eachRow(ctx, read, dest, func() {
		writeTag(r.h, 'R')
		for i := range width {
			writeValue(r.h, kinds[i], raws[i])
		}
	})
}

// rowRead returns the statement that reads each row of table filter keeps in order, and the values it reads per row.
func rowRead(table fingerprintTable, order string, columns []fingerprintColumn, filter statement) (statement, int) {
	values, width := rawColumns(columns), len(columns)
	if !table.withoutRowid {
		values, width = "typeof("+order+"), "+order+", "+values, width+1
	}
	text := "SELECT " + values + " FROM main." + quoteName(table.name) + filter.text + " ORDER BY " + order
	return statement{text: text, args: filter.args}, width
}

// rowOrder returns what orders the rows of table, its key columns with no rowid or a rowid alias no column hides.
func rowOrder(table fingerprintTable, columns []fingerprintColumn) (string, error) {
	if table.withoutRowid {
		return keyOrder(columns), nil
	}
	for _, alias := range rowAliases {
		if !hasColumn(columns, alias) {
			return alias, nil
		}
	}
	return "", fmt.Errorf("dbkit: the fingerprint cannot order the rows of %q, whose columns hide rowid, _rowid_ and oid",
		table.name)
}

// keyOrder returns the key columns of columns, quoted, in key order.
func keyOrder(columns []fingerprintColumn) string {
	keys := slices.DeleteFunc(slices.Clone(columns), func(c fingerprintColumn) bool { return c.key == 0 })
	slices.SortFunc(keys, func(a, b fingerprintColumn) int { return cmp.Compare(a.key, b.key) })
	names := make([]string, len(keys))
	for i, key := range keys {
		names[i] = quoteName(key.name)
	}
	return strings.Join(names, ", ")
}

// leaveFilter returns the WHERE clause that keeps out the rows leave names in the table named table, and its arguments.
func leaveFilter(table string, columns []fingerprintColumn, leave []LeaveRows) (statement, error) {
	var clauses []string
	var args []any
	for _, rows := range leave {
		if !sameName(rows.Table, table) {
			continue
		}
		if !hasColumn(columns, rows.Column) {
			return statement{}, fmt.Errorf("dbkit: the fingerprint leaves out rows by the column %q, "+
				"which the table %q does not hold", rows.Column, table)
		}
		clauses = append(clauses, leaveClause(rows))
		args = append(args, rows.Values...)
	}
	if len(clauses) == 0 {
		return statement{}, nil
	}
	return statement{text: " WHERE " + strings.Join(clauses, " AND "), args: args}, nil
}

// hasColumn reports whether columns holds a column named name.
func hasColumn(columns []fingerprintColumn, name string) bool {
	return slices.ContainsFunc(columns, func(c fingerprintColumn) bool { return sameName(c.name, name) })
}

// sameName reports whether a and b name the same SQLite object, their ASCII letters matching in any case.
func sameName(a, b string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range len(a) {
		if lowerASCII(a[i]) != lowerASCII(b[i]) {
			return false
		}
	}
	return true
}

// lowerASCII returns c in lower case when it is an ASCII capital letter, and c itself otherwise.
func lowerASCII(c byte) byte {
	if 'A' <= c && c <= 'Z' {
		return c + 'a' - 'A'
	}
	return c
}

// leaveClause returns the condition that keeps a row whose column holds none of the values of rows, NULL matching none.
func leaveClause(rows LeaveRows) string {
	marks := strings.TrimSuffix(strings.Repeat("?, ", len(rows.Values)), ", ")
	return "coalesce(" + quoteName(rows.Column) + " IN (" + marks + "), 0) = 0"
}

// rawColumns returns the storage class and the raw value of each of columns, text read as its bytes.
func rawColumns(columns []fingerprintColumn) string {
	parts := make([]string, len(columns))
	for i, column := range columns {
		name := quoteName(column.name)
		parts[i] = "typeof(" + name + "), CASE typeof(" + name + ") WHEN 'text' THEN CAST(" + name +
			" AS BLOB) ELSE " + name + " END"
	}
	return strings.Join(parts, ", ")
}

// quoteName returns name as an SQL identifier.
func quoteName(name string) string {
	return `"` + strings.ReplaceAll(name, `"`, `""`) + `"`
}

// eachRow runs s in the transaction, scans each row into dest and calls use, and returns the first error.
func (r fingerprintRead) eachRow(ctx context.Context, s statement, dest []any, use func()) (err error) {
	rows, err := r.tx.QueryContext(ctx, s.text, s.args...)
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, rows.Close()) }()
	for rows.Next() {
		if err := rows.Scan(dest...); err != nil {
			return err
		}
		use()
	}
	return rows.Err()
}

// writeValue writes one value to h, tagged with its storage class kind and read from raw.
func writeValue(h hash.Hash, kind string, raw any) {
	switch kind {
	case "null":
		writeTag(h, 'n')
	case "integer":
		n, _ := raw.(int64)
		writeField(h, 'i', binary.AppendVarint(nil, n))
	case "real":
		f, _ := raw.(float64)
		writeField(h, 'r', binary.BigEndian.AppendUint64(nil, math.Float64bits(f)))
	default:
		b, _ := raw.([]byte)
		writeField(h, kind[0], b)
	}
}

// writeField writes tag and then b with its length to h.
func writeField(h hash.Hash, tag byte, b []byte) {
	writeTag(h, tag)
	writeCount(h, len(b))
	h.Write(b)
}

// writeText writes s with its length to h.
func writeText(h hash.Hash, s string) {
	writeCount(h, len(s))
	h.Write([]byte(s))
}

// writeCount writes n to h.
func writeCount(h hash.Hash, n int) {
	h.Write(binary.AppendVarint(nil, int64(n)))
}

// writeTag writes the one byte tag to h.
func writeTag(h hash.Hash, tag byte) {
	h.Write([]byte{tag})
}
