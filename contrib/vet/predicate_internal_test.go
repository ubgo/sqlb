package vet

import (
	"errors"
	"strings"
	"testing"

	"github.com/ubgo/sqlb/schema"
)

// TestNormalPredicate pins the normal form: it joins only spellings the
// engines ignore (space, case outside quotes, quotes an identifier does not
// need, wrapping parentheses) and keeps everything that changes meaning.
func TestNormalPredicate(t *testing.T) {
	same := [][2]string{
		{`"record_path" IS NULL`, `(record_path IS NULL)`},
		{`"record_path"   is   null`, `((record_path IS NULL))`},
		{`( "a" = 1 ) AND ( "b" = 2 )`, `(a = 1) AND (b = 2)`},
		{"`role` = 'owner'", `role = 'owner'`},
	}
	for _, c := range same {
		if a, b := normalPredicate(c[0]), normalPredicate(c[1]); a != b {
			t.Errorf("%q and %q: %q != %q", c[0], c[1], a, b)
		}
	}
	differ := [][2]string{
		{`role = 'Owner'`, `role = 'owner'`},        // a literal keeps its case
		{`"Role" = 1`, `role = 1`},                  // a quoted mixed-case name is another column
		{`(a = 1) AND (b = 2)`, `a = 1 AND b = 2)`}, // unbalanced, not stripped
		{`(a = 1) OR (b = 2)`, `(a = 1) AND (b = 2)`},
		{`status = 'open'`, `(status = 'open'::text)`}, // a cast is the engine's to judge
		{`a = 'it''s'`, `a = 'it'`},
	}
	for _, c := range differ {
		if normalPredicate(c[0]) == normalPredicate(c[1]) {
			t.Errorf("%q and %q joined: %q", c[0], c[1], normalPredicate(c[0]))
		}
	}
	if got := normalPredicate(`(a = 1) OR (b = 2)`); got != "(a = 1) or (b = 2)" {
		t.Errorf("two groups are not one wrapped group: %q", got)
	}
}

// TestSQLiteIndexPredicate pins reading the predicate out of the CREATE INDEX
// text SQLite stores: after the key list, past quoted names holding
// parentheses, and refused when there is none.
func TestSQLiteIndexPredicate(t *testing.T) {
	for in, want := range map[string]string{
		`CREATE INDEX i ON t (a) WHERE a IS NULL`:                            "a IS NULL",
		`CREATE INDEX "i(" ON "t)" ("a", b DESC) where (a > 1)`:              "(a > 1)",
		"CREATE UNIQUE INDEX [i] ON `t` (a COLLATE NOCASE)\n\tWHERE b = ')'": "b = ')'",
	} {
		got, err := sqliteIndexPredicate(in)
		if err != nil || got != want {
			t.Errorf("%s: %q, %v", in, got, err)
		}
	}
	for _, in := range []string{`CREATE INDEX i ON t (a)`, `CREATE INDEX i ON t (a`, `CREATE INDEX i ON t (a) WHEREVER`, `no parenthesis`} {
		if _, err := sqliteIndexPredicate(in); !errors.Is(err, errNoPredicate) {
			t.Errorf("%s: %v", in, err)
		}
	}
}

// TestCompareKeys_OrdersAndPredicates pins the index comparison on catalogue
// literals: an unstated order is not judged, an unstated direction is
// ascending, NULLS placement is judged only when the catalogue reports one,
// and partial-ness and predicates follow the rule in predicate.go.
func TestCompareKeys_OrdersAndPredicates(t *testing.T) {
	asc := schema.KeyOrder{Direction: schema.Ascending, Nulls: schema.NullsLast}
	desc := schema.KeyOrder{Direction: schema.Descending, Nulls: schema.NullsFirst}
	run := func(want, got schema.Index, cmp predicateComparer) []Finding {
		return compareKeys([]schema.Table{{Name: "t", Indexes: []schema.Index{want}}},
			KeyCatalogue{Tables: map[string]TableKeys{"t": {Indexes: []schema.Index{got}}}}, cmp)
	}
	cols := []string{"a", "b"}
	for name, c := range map[string]struct {
		want, got schema.Index
		cmp       predicateComparer
		kind      FindingKind
		warn      bool
		msg       string
	}{
		"orders not stated":           {schema.Index{Columns: cols}, schema.Index{Columns: cols, Orders: []schema.KeyOrder{desc, desc}}, textOnly, "", false, ""},
		"unstated is ascending":       {schema.Index{Columns: cols, Orders: []schema.KeyOrder{{}, {}}}, schema.Index{Columns: cols, Orders: []schema.KeyOrder{asc, asc}}, textOnly, "", false, ""},
		"direction differs":           {schema.Index{Columns: cols, Orders: []schema.KeyOrder{{}, {Direction: schema.Descending}}}, schema.Index{Columns: cols, Orders: []schema.KeyOrder{asc, asc}}, textOnly, FindingIndexOrder, false, "sorts b DESC NULLS FIRST, the database's sorts b ASC NULLS LAST"},
		"nulls default for DESC":      {schema.Index{Columns: cols, Orders: []schema.KeyOrder{{}, {Direction: schema.Descending}}}, schema.Index{Columns: cols, Orders: []schema.KeyOrder{asc, desc}}, textOnly, "", false, ""},
		"nulls not reported":          {schema.Index{Columns: cols, Orders: []schema.KeyOrder{{}, {Nulls: schema.NullsFirst}}}, schema.Index{Columns: cols, Orders: []schema.KeyOrder{{Direction: schema.Ascending}, {Direction: schema.Ascending}}}, textOnly, "", false, ""},
		"nulls differ":                {schema.Index{Columns: cols, Orders: []schema.KeyOrder{{}, {Nulls: schema.NullsFirst}}}, schema.Index{Columns: cols, Orders: []schema.KeyOrder{asc, asc}}, textOnly, FindingIndexOrder, false, "sorts b ASC NULLS FIRST"},
		"declared partial only":       {schema.Index{Columns: cols, Where: "a IS NULL"}, schema.Index{Columns: cols}, textOnly, FindingIndexPredicate, false, "is partial (WHERE a IS NULL) and the database's is not"},
		"database partial only":       {schema.Index{Columns: cols}, schema.Index{Columns: cols, Where: "(a IS NULL)"}, textOnly, FindingIndexPredicate, false, "the database's index on (a, b) is partial (WHERE (a IS NULL)) and the DDL's is not"},
		"same normal form":            {schema.Index{Columns: cols, Where: `"a" IS NULL`}, schema.Index{Columns: cols, Where: "(a IS NULL)"}, textOnly, "", false, ""},
		"not compared without engine": {schema.Index{Columns: cols, Where: "a = 'x'"}, schema.Index{Columns: cols, Where: "(a = 'x'::text)"}, textOnly, FindingIndexPredicateNotCompared, true, "partial on both sides, not compared"},
		"different on SQLite":         {schema.Index{Columns: cols, Where: "a = 'x'"}, schema.Index{Columns: cols, Where: "a = 'y'"}, textStrict, FindingIndexPredicate, false, "is WHERE a = 'x', the database's is WHERE a = 'y'"},
	} {
		got := run(c.want, c.got, c.cmp)
		if c.kind == "" {
			if len(got) != 0 {
				t.Errorf("%s: %v", name, got)
			}
			continue
		}
		if len(got) != 1 || got[0].Kind != c.kind || got[0].Warning != c.warn || !strings.Contains(got[0].Message, c.msg) {
			t.Errorf("%s: got %+v, want one %s containing %q", name, got, c.kind, c.msg)
		}
	}
}

// TestMatchIndex_PrefersTheUnusedAndTheNamed pins that two declared indexes
// over the same columns, one partial and one not, each find their own
// database index rather than both the first.
func TestMatchIndex_PrefersTheUnusedAndTheNamed(t *testing.T) {
	got := []schema.Index{{Name: "full", Columns: []string{"a"}}, {Name: "part", Columns: []string{"a"}, Where: "a > 0"}}
	used := map[int]bool{}
	if i := matchIndex(schema.Index{Name: "part", Columns: []string{"a"}, Where: "a > 0"}, got, used); i != 1 {
		t.Fatalf("partial matched %d", i)
	}
	used[1] = true
	if i := matchIndex(schema.Index{Columns: []string{"a"}, Where: "a > 0"}, got, used); i != 0 {
		t.Fatalf("an unnamed second declaration took the used index: %d", i)
	}
}
