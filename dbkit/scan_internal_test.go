// SPDX-License-Identifier: Apache-2.0

package dbkit

import (
	"strings"
	"testing"
	"unsafe"
)

// scanSink keeps each measured scan alive.
var scanSink scanned

// scanNoDollarQueries are queries that hold no dollar sign.
var scanNoDollarQueries = []string{
	"",
	"SELECT 1",
	"SELECT id, title FROM plugin_posts WHERE status = 'published' ORDER BY id",
	"SELECT ?, :n, @n FROM t -- note\n/* note */",
}

// scanNothingToSwapQueries are queries whose dollar signs SQLite never reads as numbered parameters.
var scanNothingToSwapQueries = []string{
	"SELECT col$1, ñ$1, $1abc, $1$2, $a, $ FROM t",
	"SELECT '$1', \"$1\", `$1`, [$1] -- $1",
	"SELECT /* $1 */ 'open $1",
	"SELECT $a($1), :b::c($1), @d, #e, \xef\xbb\xbfx$1 FROM t",
}

// scanAssertNotCopied fails t unless scanning query on engine returns query itself with no allocation.
func scanAssertNotCopied(t *testing.T, query string, engine Engine) {
	t.Helper()

	got := scan(query, engine).text
	if len(got) != len(query) || unsafe.StringData(got) != unsafe.StringData(query) {
		t.Errorf("scan(%q, %v).text = %q, want the same string back", query, engine, got)
	}
	allocs := testing.AllocsPerRun(100, func() { scanSink = scan(query, engine) })
	if allocs != 0 {
		t.Errorf("scan(%q, %v) allocations = %v, want 0", query, engine, allocs)
	}
}

// scanRewriteCases pairs each SQLite input with the text the driver must receive.
var scanRewriteCases = []struct {
	query string
	want  string
}{
	{"SELECT $2 || '-' || $1", "SELECT ?2 || '-' || ?1"},
	{"SELECT $9, 1 --\n, $1", "SELECT ?9, 1 --\n, ?1"},
	{"SELECT '$1 stays' || $2 -- $3 stays\n, $1", "SELECT '$1 stays' || ?2 -- $3 stays\n, ?1"},
	{"SELECT 'it''s $1', $1", "SELECT 'it''s $1', ?1"},
	{`SELECT "col$1", "a""$1", $1`, `SELECT "col$1", "a""$1", ?1`},
	{"SELECT `x$1`, [y$1], $1", "SELECT `x$1`, [y$1], ?1"},
	{"SELECT /* $1 */ $2", "SELECT /* $1 */ ?2"},
	{"SELECT /* a /* b */ $1", "SELECT /* a /* b */ ?1"},
	{"SELECT col$1, ñ$1 FROM t", "SELECT col$1, ñ$1 FROM t"},
	{"SELECT $1abc, $1$2", "SELECT $1abc, $1$2"},
	{"SELECT $10, $1, $1", "SELECT ?10, ?1, ?1"},
	{"SELECT 1-$1, 5/$1", "SELECT 1-?1, 5/?1"},
	{"SELECT $1 -- $2", "SELECT ?1 -- $2"},
	{"SELECT 'open $1", "SELECT 'open $1"},
	{"SELECT /* open $1", "SELECT /* open $1"},
	{"SELECT $, $$, $a, ?1, :n, @n", "SELECT $, $$, $a, ?1, :n, @n"},
	{`SELECT '-- no', '/* no */', "it's", $1`, `SELECT '-- no', '/* no */', "it's", ?1`},
	{"SELECT $1::text", "SELECT ?1::text"},
	{"", ""},
	{"$1", "?1"},
	{"SELECT $1/*$2*/", "SELECT ?1/*$2*/"},
	{"SELECT 7$1, x_$1", "SELECT 7$1, x_$1"},
	{`SELECT "open $1`, `SELECT "open $1`},
	{"SELECT `open $1", "SELECT `open $1"},
	{"SELECT [open $1", "SELECT [open $1"},
	{"SELECT /*/ $1 */ $2", "SELECT /*/ $1 */ ?2"},
	{"SELECT a-b, a/b, $1", "SELECT a-b, a/b, ?1"},
	{"SELECT ?1$2", "SELECT ?1?2"},
	{"SELECT \xef\xbb\xbf$1", "SELECT \xef\xbb\xbf?1"},
	{"SELECT x\xef\xbb\xbf$1", "SELECT x\xef\xbb\xbf$1"},
	{"SELECT $a($1), :b($1), $1", "SELECT $a($1), :b($1), ?1"},
	{"SELECT $a::b($1), @c::d, $1", "SELECT $a::b($1), @c::d, ?1"},
	{"SELECT $1(x)", "SELECT ?1(x)"},
	{"SELECT $a(x y), $1", "SELECT $a(x y), $1"},
	{"SELECT 1 -- a\r it's\n, $1", "SELECT 1 -- a\r it's\n, ?1"},
}

func TestRewrite(t *testing.T) {
	t.Parallel()

	for _, c := range scanRewriteCases {
		if got := scan(c.query, SQLite).text; got != c.want {
			t.Errorf("scan(%q, SQLite).text = %q, want %q", c.query, got, c.want)
		}
	}
}

func TestPostgresTextIsNeverRewritten(t *testing.T) {
	t.Parallel()

	for _, c := range scanRewriteCases {
		if got := scan(c.query, Postgres).text; got != c.query {
			t.Errorf("scan(%q, Postgres).text = %q, want the input unchanged", c.query, got)
		}
	}
}

func TestStatementWrites(t *testing.T) {
	t.Parallel()

	type writeCase struct {
		query  string
		writes bool
	}
	common := []writeCase{
		{"SELECT 1", false},
		{"-- note\n/* note */ select 1", false},
		{"VALUES (1), (2)", false},
		{"WITH x AS (SELECT 1) SELECT * FROM x", false},
		{"SELECT * FROM t FOR UPDATE", false},
		{"SELECT * FROM t FOR NO KEY UPDATE", false},
		{"SELECT * FROM t for /* lock */ update", false},
		{"SELECT 1 FOR /* a */ no -- b\n key update", false},
		{"SELECT key UPDATE t", true},
		{"SELECT no key UPDATE t", true},
		{"SELECT for key UPDATE t", true},
		{"SELECT for any key UPDATE t", true},
		{"SELECT for no keys UPDATE t", true},
		{"SELECT for no, key UPDATE t", true},
		{"SELECT for, no key UPDATE t", true},
		{"SELECT replace(a, 'b', 'c') FROM t", false},
		{"SELECT 'DELETE'", false},
		{`SELECT "DELETE" FROM t`, false},
		{"SELECT 1 -- DELETE", false},
		{"SELECT /* INSERT */ 1", false},
		{"SELECT inserted_at FROM t", false},
		{"SELECT 1;\n", false},
		{"SELECT 1; -- done\n/* end */ ", false},
		{"SELECT 1-2, 6/3, $1", false},
		{"INSERT INTO t VALUES (1)", true},
		{"insert into t values (1)", true},
		{"UPDATE t SET a = 1", true},
		{"DELETE FROM t", true},
		{"REPLACE INTO t VALUES (1)", true},
		{"CREATE TABLE t (a int)", true},
		{"DROP TABLE t", true},
		{"ALTER TABLE t ADD COLUMN b int", true},
		{"PRAGMA user_version", true},
		{"SELECT * INTO t2 FROM t", true},
		{"WITH d AS (DELETE FROM t RETURNING id) SELECT * FROM d", true},
		{"WITH x AS (SELECT 1) INSERT INTO t SELECT * FROM x", true},
		{"WITH x AS (UPDATE t SET a = 1 RETURNING a) SELECT * FROM x", true},
		{"SELECT update FROM t", true},
		{"SELECT * FROM t FOR 1 UPDATE", true},
		{"SELECT 1; DROP TABLE t", true},
		{"SELECT 1;;", true},
		{"SELECT 'open", true},
		{`SELECT "open`, true},
		{"SELECT /* open", true},
		{"", true},
		{" \t\n", true},
		{"-- only a note", true},
		{"(1)", true},
		{"SELECT '' FROM t", false},
		{"SELECT 1 --\n; DELETE FROM t", true},
		{"SELECT 1;\t", false},
		{"SELECT 1;\r", false},
		{"SELECT \x80delete FROM t", false},
		{"SELECT zdelete, Zdelete FROM t", false},
		{"SELECT x0delete, x9delete FROM t", false},
		{"SELECT id FROM t WHERE title LIKE $1 ESCAPE $2", false},
	}
	sqliteOnly := []writeCase{
		{"SELECT [DELETE]", false},
		{"SELECT `DELETE`", false},
		{"SELECT 1delete", false},
		{"SELECT E'x', $a FROM t", false},
		{"SELECT `open", true},
		{"SELECT [open", true},
		{"SELECT ?1delete FROM t", true},
		{"\xef\xbb\xbfSELECT 1", false},
		{"WITH x AS (SELECT 1) \xef\xbb\xbfDELETE FROM t", true},
		{"SELECT x\xef\xbb\xbfdelete FROM t", false},
		{"SELECT $a(x), :b::c, @d, #e FROM t", false},
		{"SELECT $a(--) ; DELETE FROM t", true},
		{"SELECT :a(') ; DELETE FROM t --'", true},
		{"SELECT @a::b(/*) ; DELETE FROM t --*/", true},
		{"SELECT #a(\") ; DELETE FROM t --\"", true},
		{"SELECT $a(x y)", true},
		{"SELECT $a(x", true},
		{"SELECT 'a\\d' FROM t", false},
		{"SELECT [] FROM t", false},
		{"SELECT 1 -- note\rDELETE FROM t", false},
		{"SELECT /* a /* b */ 1", false},
		{`SELECT id FROM t WHERE title LIKE $1 ESCAPE '\'`, false},
	}
	postgresOnly := []writeCase{
		{"SELECT $$it's$$, 1", true},
		{`SELECT E'\'', 1`, true},
		{"SELECT e'x'", true},
		{"SELECT $tag$x$tag$", true},
		{"SELECT $_x$ $_x$", true},
		{"SELECT $ñ$x$ñ$", true},
		{"SELECT $1$$x$$", true},
		{"SELECT [DELETE]", true},
		{"SELECT `DELETE`", true},
		{"SELECT x[1] FROM t", false},
		{"SELECT $1, $2, $ FROM t", false},
		{"SELECT be'x', E 'x', col$a FROM t", false},
		{"SELECT 1 -- note\rDELETE FROM t", true},
		{"SELECT 1 -- note\r\n", false},
		{"SELECT /* a /* b */ 1", true},
		{"SELECT /* /* */ '; */; DELETE FROM t --'", true},
		{"SELECT /* a /*/ 1", true},
		{"SELECT /**/ 1 /* a */", false},
		{"\xef\xbb\xbfSELECT 1", true},
		{"SELECT 'x\\'' ; DELETE FROM t --'", true},
		{"SELECT 'a\\d' FROM t", true},
		{"SELECT 'it''s', \"a\\b\" FROM t", false},
		{`SELECT id FROM t WHERE title LIKE $1 ESCAPE '\'`, true},
		{"SELECT 1into t2 FROM t", true},
		{"SELECT 1delete", true},
		{"SELECT 1e'x'", true},
		{"SELECT 1.5e10, 0x1F FROM t", false},
		{"WITH RECURSIVE r(n) AS (SELECT 1 UNION ALL SELECT n + 1 FROM r WHERE n < 3) " +
			"SEARCH DEPTH FIRST BY n SET key UPDATE t SET a = 1", true},
		{"WITH RECURSIVE r(n) AS (SELECT 1 UNION ALL SELECT n + 1 FROM r WHERE n < 3) " +
			"CYCLE n SET is_cycle USING key UPDATE t SET a = 2", true},
		{"WITH RECURSIVE r(n) AS (SELECT 1 UNION ALL SELECT n + 1 FROM r WHERE n < 3) " +
			"SEARCH BREADTH FIRST BY n SET key UPDATE t SET a = 3 RETURNING a", true},
	}
	runs := []struct {
		engine Engine
		cases  []writeCase
	}{
		{SQLite, common},
		{Postgres, common},
		{SQLite, sqliteOnly},
		{Postgres, postgresOnly},
	}
	for _, run := range runs {
		for _, c := range run.cases {
			if got := scan(c.query, run.engine).writes; got != c.writes {
				t.Errorf("scan(%q, %v).writes = %v, want %v", c.query, run.engine, got, c.writes)
			}
		}
	}
}

func TestPlaceholderFlag(t *testing.T) {
	t.Parallel()

	cases := []struct {
		query       string
		engine      Engine
		placeholder bool
	}{
		{"SELECT ?", SQLite, true},
		{"SELECT ?1", SQLite, true},
		{"SELECT ?, $1", SQLite, true},
		{"SELECT a FROM t WHERE b = ?", SQLite, true},
		{"SELECT '?'", SQLite, false},
		{`SELECT "a?"`, SQLite, false},
		{"SELECT [a?]", SQLite, false},
		{"SELECT `a?`", SQLite, false},
		{"SELECT 1 -- ?", SQLite, false},
		{"SELECT /* ? */ 1", SQLite, false},
		{"SELECT $1", SQLite, false},
		{"SELECT $a(?), :b(?)", SQLite, false},
		{"SELECT :a || $1", SQLite, true},
		{"SELECT $a, $1", SQLite, true},
		{"SELECT @a, $1", SQLite, true},
		{"SELECT #a, $1", SQLite, true},
		{"SELECT $1, :a, $2", SQLite, true},
		{"SELECT $1, :a", SQLite, false},
		{"SELECT data ? 'k' FROM t", Postgres, false},
		{"SELECT ?, ?1", Postgres, false},
		{"SELECT :a || $1", Postgres, false},
		{"SELECT $a, $1", Postgres, false},
		{"SELECT @a, $1", Postgres, false},
		{"SELECT #a, $1", Postgres, false},
		{"SELECT $1, :a, $2", Postgres, false},
		{"SELECT $1, :a", Postgres, false},
	}
	for _, c := range cases {
		if got := scan(c.query, c.engine).placeholder; got != c.placeholder {
			t.Errorf("scan(%q, %v).placeholder = %v, want %v", c.query, c.engine, got, c.placeholder)
		}
	}
}

func TestRewriteLeavesTextWithoutADollarUnchanged(t *testing.T) {
	for _, query := range scanNoDollarQueries {
		scanAssertNotCopied(t, query, SQLite)
		scanAssertNotCopied(t, query, Postgres)
	}
}

func TestTextWithNothingToSwapIsNotCopied(t *testing.T) {
	for _, query := range scanNothingToSwapQueries {
		scanAssertNotCopied(t, query, SQLite)
	}
	for _, c := range scanRewriteCases {
		scanAssertNotCopied(t, c.query, Postgres)
	}
}

func TestRewriteAllocatesAtMostOnce(t *testing.T) {
	queries := []string{
		"SELECT $1",
		"SELECT $2 || '-' || $1",
		"INSERT INTO plugin_posts (slug, title) VALUES ($1, $2) ON CONFLICT (slug) DO UPDATE SET title = $2",
		strings.Repeat("SELECT $1, '$2' -- $3\n", 64),
	}
	for _, query := range queries {
		allocs := testing.AllocsPerRun(100, func() { scanSink = scan(query, SQLite) })
		if allocs != 1 {
			t.Errorf("scan(%q, SQLite) allocations = %v, want exactly 1", query, allocs)
		}
	}
}

func FuzzRewriteOnlySwapsDollars(f *testing.F) {
	for _, c := range scanRewriteCases {
		f.Add(c.query)
	}
	f.Fuzz(func(t *testing.T, query string) {
		got := scan(query, SQLite).text
		if len(got) != len(query) {
			t.Fatalf("scan(%q, SQLite).text = %q, want the input's length %d", query, got, len(query))
		}
		for i := range len(query) {
			if got[i] != query[i] && (query[i] != '$' || got[i] != '?') {
				t.Fatalf("scan(%q, SQLite).text = %q, changed byte %d other than a dollar sign", query, got, i)
			}
		}
		if again := scan(got, SQLite).text; again != got {
			t.Fatalf("scan(%q, SQLite).text = %q, want the first output unchanged", got, again)
		}
		if same := scan(query, Postgres).text; same != query {
			t.Fatalf("scan(%q, Postgres).text = %q, want the input unchanged", query, same)
		}
	})
}

func BenchmarkRewrite(b *testing.B) {
	cases := []struct {
		name  string
		query string
	}{
		{"NoDollar", "SELECT id, title FROM plugin_posts WHERE status = 'published' ORDER BY id"},
		{"SeveralDollars", "UPDATE plugin_posts SET title = $2, body = $3 WHERE slug = $1 AND status = $4"},
		{"LongTextWithQuotesAndComments", strings.Repeat("SELECT \"a$1\", 'it''s $2', $3 -- $4\n/* $5 */ ", 32)},
	}
	for _, c := range cases {
		b.Run(c.name, func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				scanSink = scan(c.query, SQLite)
			}
		})
	}
}
