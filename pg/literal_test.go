package pg_test

import (
	"encoding/json"
	"testing"

	"github.com/ubgo/sqlb"
	"github.com/ubgo/sqlb/pg"
)

// litDoc is a JSON document for the rendering tests; its spelling does not
// matter here, only the path literals around it.
var litDoc = sqlb.Literal[json.RawMessage](`'{}'::jsonb`)

func renderSelect(t *testing.T, where sqlb.Predicate) string {
	t.Helper()
	q, err := pg.Select(sqlb.Literal[int]("1")).Where(where).Build()
	if err != nil {
		t.Fatal(err)
	}
	return q.SQL
}

// TestLiteral_BackslashPathTakesTheEForm pins, without a server, that a path
// holding a backslash is written as E'...' with the backslash doubled. The
// execution proof, under both settings of standard_conforming_strings, is in
// contrib/driver-pgx; this is what fails first, and in every run.
func TestLiteral_BackslashPathTakesTheEForm(t *testing.T) {
	got := renderSelect(t, pg.JSONExists(litDoc, `$.a ? (@ like_regex "^\\d$")`))
	want := `SELECT 1 WHERE (JSON_EXISTS('{}'::jsonb, E'$.a ? (@ like_regex "^\\\\d$")'))`
	if got != want {
		t.Fatalf("got  %s\nwant %s", got, want)
	}
}

// TestLiteral_PlainPathIsUnchanged pins the other half of the rule: a path
// with no backslash is written plainly, exactly as it was before the quoter
// moved into the dialect, so no existing rendering changed.
func TestLiteral_PlainPathIsUnchanged(t *testing.T) {
	got := renderSelect(t, pg.JSONExists(litDoc, `$.a ? (@ == "it's")`))
	want := `SELECT 1 WHERE (JSON_EXISTS('{}'::jsonb, '$.a ? (@ == "it''s")'))`
	if got != want {
		t.Fatalf("got  %s\nwant %s", got, want)
	}
}

// TestLiteral_QuoteLitWritesThroughTheDialect pins that the exported helper an
// extension package uses follows the same rule.
func TestLiteral_QuoteLitWritesThroughTheDialect(t *testing.T) {
	for _, tc := range []struct{ in, want string }{
		{"english", `'english'`},
		{`a\b`, `E'a\\b'`},
	} {
		a := sqlb.NewArgs(pg.Dialect{})
		if got := pg.QuoteLit(tc.in).SQL(a); got != tc.want || a.Err() != nil {
			t.Errorf("QuoteLit(%q) = %s, %v; want %s", tc.in, got, a.Err(), tc.want)
		}
	}
}

// TestJSONTable_EveryColumnPathIsWrittenByTheQuoter covers the three column
// kinds whose path is a literal: a plain column, an EXISTS column and a NESTED
// PATH. Until 2026-09-21 those three rendered their path with no Args at all,
// which worked only because the old helper never looked at the dialect; with
// the quoter in the dialect they would have panicked. Nothing tested a nested
// path before this.
func TestJSONTable_EveryColumnPathIsWrittenByTheQuoter(t *testing.T) {
	jt := pg.JSONTable(litDoc, "$.items[*]", "jt",
		pg.JSONTableColumn{Name: "id", Type: "int", Path: "$.id"},
		pg.JSONTableColumn{Name: "has_tag", Type: "bool", Path: `$.tag ? (@ like_regex "^\\w+$")`, Exists: true},
		pg.JSONTableColumn{Nested: &pg.JSONTableNested{Path: `$.parts[*]`, Columns: []pg.JSONTableColumn{
			{Name: "part", Type: "text", Path: `$.name`},
		}}},
	)
	q, err := pg.Select(sqlb.Literal[int]("1")).From(jt).Build()
	if err != nil {
		t.Fatal(err)
	}
	want := `SELECT 1 FROM JSON_TABLE('{}'::jsonb, '$.items[*]' COLUMNS (` +
		`"id" int PATH '$.id', ` +
		`"has_tag" bool EXISTS PATH E'$.tag ? (@ like_regex "^\\\\w+$")', ` +
		`NESTED PATH '$.parts[*]' COLUMNS ("part" text PATH '$.name'))) AS "jt"`
	if q.SQL != want {
		t.Fatalf("got  %s\nwant %s", q.SQL, want)
	}
}
