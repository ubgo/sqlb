package pg

import (
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/ubgo/sqlb"
)

// The guard sweep.
//
// Every constructor in this package renders through the same shape: check the
// dialect, and on a miss record the construct and return an empty fragment.
// The render tests exercise the first half. This exercises the second, for
// the constructors whose guard arm nothing else reaches.
//
// Why it is worth a test of its own: the guard arm is the one that runs when
// a multi-engine application makes a mistake, so it is both the least
// exercised path in the package and the one whose failure is worst. A
// constructor that forgets it does not error; it emits Postgres syntax into
// another engine's statement and the database reports it at run time.
func TestGuardArms_UnderAnotherDialect(t *testing.T) {
	tb := sqlb.Table{Name: "t", Alias: "t"}
	name := sqlb.Col[string](tb, "name")
	n := sqlb.Col[int32](tb, "n")
	doc := sqlb.Col[json.RawMessage](tb, "doc")
	day := sqlb.Col[time.Time](tb, "day")
	lite := sqlb.SQLiteDialect{}

	// Expression-shaped constructors: build a SELECT of the expression and
	// require the dialect error.
	t.Run("expressions", func(t *testing.T) {
		// PathQueryVars is not here. It returns a Source and an expression
		// that references that source's alias, so the guard lives on the
		// source; the expression alone is just a column reference and
		// renders anywhere. It is checked in the sources group below.
		for label, e := range map[string]sqlb.SQLer{
			"Filter":      sqlb.Count(Filter(sqlb.Eq(n, int32(1)))),
			"OrderWithin": ArrayAgg(name, OrderWithin(sqlb.OrderBy(name, sqlb.SortAsc))),
			"WithinGroup": PercentileCont(0.5, sqlb.OrderBy(n, sqlb.SortAsc)),
			"Ntile":       Ntile(4).Over(sqlb.NewWindow()),
		} {
			if _, err := sqlb.NewSelect(lite).Exprs(e).From(tb).Build(); !errors.Is(err, sqlb.ErrUnsupportedDialect) {
				t.Errorf("%s: want ErrUnsupportedDialect, got %v", label, err)
			}
		}
	})

	// Clause-shaped constructors are typed to Postgres and a statement kind, so
	// the wrong engine is a compile error; examples/typecheck pins it.

	// GROUPING SETS replaces the GROUP BY list, so it is checked through one.
	t.Run("grouping_sets", func(t *testing.T) {
		if _, err := sqlb.NewSelect(lite).Exprs(name, sqlb.Count()).From(tb).
			GroupBy(GroupingSets([]sqlb.SQLer{name}, []sqlb.SQLer{})).Build(); !errors.Is(err, sqlb.ErrUnsupportedDialect) {
			t.Errorf("GroupingSets: want ErrUnsupportedDialect, got %v", err)
		}
	})

	// ORDER BY ... NULLS FIRST is a term, not an expression.
	t.Run("order_terms", func(t *testing.T) {
		if _, err := sqlb.NewSelect(lite).Exprs(name).From(tb).
			Order(OrderByNulls(name, sqlb.SortAsc, true)).Build(); !errors.Is(err, sqlb.ErrUnsupportedDialect) {
			t.Errorf("OrderByNulls: want ErrUnsupportedDialect, got %v", err)
		}
	})

	// Sources render in the FROM clause.
	t.Run("sources", func(t *testing.T) {
		plain := Values("v", []string{"a"}, []sqlb.SQLer{sqlb.Param(1)})
		pathSrc, _ := PathQueryVars(doc, "$.a", []byte(`{"k":1}`), "pv")
		for name2, src := range map[string]sqlb.Source{
			"Values":         plain,
			"RecordSet":      RecordSet(doc, "r", ColumnDef{Name: "a", Type: "text"}),
			"UnnestMulti":    UnnestMulti("u", []sqlb.SQLer{sqlb.Param([]string{"a"})}, false, "a"),
			"WithOrdinality": WithOrdinality(plain),
			"Lateral":        Lateral(plain),
			"PathQueryVars":  pathSrc,
		} {
			if _, err := sqlb.NewSelect(lite).Exprs(sqlb.Col[string](sqlb.Ref(src), "a")).From(src).Build(); !errors.Is(err, sqlb.ErrUnsupportedDialect) {
				t.Errorf("%s: want ErrUnsupportedDialect, got %v", name2, err)
			}
		}
	})

	// CTE prefixes and suffixes.
	t.Run("cte_parts", func(t *testing.T) {
		body := sqlb.NewSelect(lite).Exprs(name).From(tb)
		for name2, cte := range map[string]sqlb.CTE{
			"Materialized": {Name: "c", Body: body, Prefix: Materialized(true)},
			"Cycle":        {Name: "c", Body: body, Suffix: Cycle("cyc", "path", "id")},
			"SearchDepth":  {Name: "c", Body: body, Suffix: SearchDepthFirst("ord", "id")},
		} {
			ref := sqlb.Table{Name: "c", Alias: "c"}
			if _, err := sqlb.NewSelect(lite).WithCTE(cte).Exprs(sqlb.Col[string](ref, "name")).From(ref).Build(); !errors.Is(err, sqlb.ErrUnsupportedDialect) {
				t.Errorf("%s: want ErrUnsupportedDialect, got %v", name2, err)
			}
		}
	})

	_ = day
}

// The list-separator and option arms.
//
// Every one of these is the "second item" or "option present" branch of a
// construct whose single-item form is already exercised. They are grouped
// because they share a failure mode: a missing separator or a mis-rendered
// option produces SQL that is syntactically plausible and semantically wrong,
// which the database accepts and answers incorrectly.
func TestMultiItemAndOptionArms(t *testing.T) {
	tb := sqlb.Table{Name: "t", Alias: "t"}
	other := sqlb.Table{Name: "o", Alias: "o"}
	name := sqlb.Col[string](tb, "name")
	n := sqlb.Col[int32](tb, "n")
	doc := sqlb.Col[json.RawMessage](tb, "doc")
	pgd := sqlb.PostgresDialect{}

	// Two ORDER BY terms inside an aggregate, one of them using an operator
	// and one specifying NULLS placement. All three arms at once.
	t.Run("aggregate order terms", func(t *testing.T) {
		e := ArrayAgg(name, OrderWithin(
			OrderUsing(n, "<"),
			OrderByNulls(name, sqlb.SortDesc, true),
		))
		q, err := sqlb.NewSelect(pgd).Exprs(e).From(tb).Build()
		if err != nil {
			t.Fatalf("build: %v", err)
		}
		want := `SELECT array_agg("t"."name" ORDER BY "t"."n" USING <, "t"."name" DESC NULLS FIRST) FROM "t" AS "t"`
		if q.SQL != want {
			t.Fatalf("got  %s\nwant %s", q.SQL, want)
		}
	})

	// WITHIN GROUP with two ordering terms.
	t.Run("within group", func(t *testing.T) {
		q, err := sqlb.NewSelect(pgd).Exprs(Mode(name)).From(tb).Build()
		if err != nil {
			t.Fatalf("build: %v", err)
		}
		if want := `SELECT mode() WITHIN GROUP (ORDER BY "t"."name" ASC) FROM "t" AS "t"`; q.SQL != want {
			t.Fatalf("got  %s\nwant %s", q.SQL, want)
		}
	})

	// GROUPING SETS with two sets, which is the only form that exercises the
	// separator between them.
	t.Run("grouping sets", func(t *testing.T) {
		q, err := sqlb.NewSelect(pgd).Exprs(name, sqlb.Count()).From(tb).
			GroupBy(GroupingSets([]sqlb.SQLer{name}, []sqlb.SQLer{n})).Build()
		if err != nil {
			t.Fatalf("build: %v", err)
		}
		if want := `SELECT "t"."name", count(*) FROM "t" AS "t" GROUP BY GROUPING SETS (("t"."name"), ("t"."n"))`; q.SQL != want {
			t.Fatalf("got  %s\nwant %s", q.SQL, want)
		}
	})

	// FOR UPDATE OF two tables.
	t.Run("lock of several tables", func(t *testing.T) {
		q, err := sqlb.NewSelect(pgd).Exprs(name).From(tb).
			Join(sqlb.Join{Type: sqlb.JoinInner, Table: other, On: []sqlb.Predicate{sqlb.EqExpr(name, sqlb.Col[string](other, "name"))}}).
			Clause(ForUpdate(Of(tb, other))).Build()
		if err != nil {
			t.Fatalf("build: %v", err)
		}
		if want := `SELECT "t"."name" FROM "t" AS "t" INNER JOIN "o" AS "o" ON ("t"."name" = "o"."name") FOR UPDATE OF "t", "o"`; q.SQL != want {
			t.Fatalf("got  %s\nwant %s", q.SQL, want)
		}
	})

	// ON CONFLICT on a two-column unique index.
	t.Run("conflict on two columns", func(t *testing.T) {
		q, err := sqlb.NewInsert(pgd).Into(tb).
			Row(sqlb.Set(name, "a"), sqlb.Set(n, int32(1))).
			Clause(OnConflict(name, n).DoNothing()).Build()
		if err != nil {
			t.Fatalf("build: %v", err)
		}
		if want := `INSERT INTO "t" AS "t" ("name", "n") VALUES ($1, $2) ON CONFLICT ("name", "n") DO NOTHING`; q.SQL != want {
			t.Fatalf("got  %s\nwant %s", q.SQL, want)
		}
	})

	// A JSON_TABLE EXISTS column, which renders a different shape from a
	// value column: it reports whether the path matched rather than
	// extracting it.
	t.Run("json_table exists column", func(t *testing.T) {
		jt := JSONTable(doc, "$.items[*]", "jt",
			JSONTableColumn{Name: "id", Type: "text", Path: "$.id"},
			JSONTableColumn{Name: "has_tag", Type: "boolean", Path: "$.tag", Exists: true},
		)
		q, err := sqlb.NewSelect(pgd).
			Exprs(sqlb.Col[string](sqlb.Ref(jt), "id"), sqlb.Col[bool](sqlb.Ref(jt), "has_tag")).
			From(jt).Build()
		if err != nil {
			t.Fatalf("build: %v", err)
		}
		if !contains(q.SQL, `"has_tag" boolean EXISTS PATH '$.tag'`) {
			t.Fatalf("EXISTS column did not render: %s", q.SQL)
		}
	})
}

// contains avoids pulling strings in for one call.
func contains(haystack, needle string) bool {
	for i := 0; i+len(needle) <= len(haystack); i++ {
		if haystack[i:i+len(needle)] == needle {
			return true
		}
	}
	return false
}
