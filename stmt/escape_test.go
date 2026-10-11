package stmt_test

import (
	"strings"
	"testing"

	"github.com/ubgo/sqlb/dialect/postgres"
	"github.com/ubgo/sqlb/expr"
	"github.com/ubgo/sqlb/stmt"
)

var (
	other   = expr.Table{Name: "other", Alias: "o"}
	otherID = expr.Col[string](other, "id")
)

// TestJoinType_EscapeHatch pins the documented way to write a join the core
// does not name.
//
// FULL OUTER JOIN is absent from the constants because MySQL lacks it and
// emulates it with a UNION of a LEFT and a RIGHT join, so a constant would
// work on three engines and fail on the fourth. JoinType is an ordinary string
// type precisely so the keyword stays reachable, and this is what says that
// has not quietly stopped being true.
//
// Deliberately unguarded: a caller who typed the keyword has said what they
// mean. An engine package wanting a checked version wraps it and records
// Args.Unsupported, the way every other engine construct does.
func TestJoinType_EscapeHatch(t *testing.T) {
	for _, kw := range []string{"FULL OUTER JOIN", "NATURAL JOIN", "LEFT OUTER JOIN"} {
		t.Run(kw, func(t *testing.T) {
			q, err := stmt.NewSelect(postgres.Dialect{}).
				Exprs(personID).
				From(people).
				Join(stmt.Join{
					Type: stmt.JoinType(kw), Table: other, Source: other,
					On: []expr.Predicate{expr.EqExpr(personID, otherID)},
				}).
				Build()
			if err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(q.SQL, kw+` "other" AS "o"`) {
				t.Fatalf("keyword not rendered: %s", q.SQL)
			}
		})
	}
}

// TestJoinTypeValues_IsTheNamedSetOnly pins that the canonical list stays the
// four the core can promise, so a reader walking it is not misled into
// thinking FULL is portable.
func TestJoinTypeValues_IsTheNamedSetOnly(t *testing.T) {
	if len(stmt.JoinTypeValues) != 4 {
		t.Fatalf("JoinTypeValues = %v, want the four universal joins", stmt.JoinTypeValues)
	}
	for _, j := range stmt.JoinTypeValues {
		if strings.Contains(string(j), "FULL") || strings.Contains(string(j), "NATURAL") {
			t.Errorf("%q is in the named set but is not accepted by all four engines", j)
		}
	}
}

// TestSetOp_EscapeHatch pins the other documented escape hatch. INTERSECT ALL
// and EXCEPT ALL are not named because SQLite lacks them.
func TestSetOp_EscapeHatch(t *testing.T) {
	right := stmt.NewSelect(postgres.Dialect{}).Exprs(personID).From(people)

	q, err := stmt.NewSelect(postgres.Dialect{}).
		Exprs(personID).From(people).
		SetOp("INTERSECT ALL", right).
		Build()
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(q.SQL, "INTERSECT ALL SELECT") {
		t.Fatalf("keyword not rendered: %s", q.SQL)
	}
}

// TestSetOp_DerivedTableOnTheLeft pins a shape that reads oddly but is common:
// a set operation whose left branch is itself a subquery, which is how a
// LIMIT is applied to one branch of a UNION.
func TestSetOp_DerivedTableOnTheLeft(t *testing.T) {
	inner := stmt.NewSelect(postgres.Dialect{}).Exprs(personID).From(people).Limit(1)
	d := expr.Table{Name: "t1", Alias: "t1"}
	dID := expr.Col[string](d, "id")

	left := stmt.NewSelect(postgres.Dialect{}).Exprs(dID).From(expr.Derived(inner, "t1"))
	right := stmt.NewSelect(postgres.Dialect{}).Exprs(otherID).From(other)

	q, err := left.Union(right).Build()
	if err != nil {
		t.Fatal(err)
	}

	want := `SELECT "t1"."id" FROM (SELECT "p"."id" FROM "people" AS "p" LIMIT $1) AS "t1" ` +
		`UNION SELECT "o"."id" FROM "other" AS "o"`
	if q.SQL != want {
		t.Fatalf("got  %s\nwant %s", q.SQL, want)
	}
	// The inner LIMIT stays with the inner query, not the compound.
	if len(q.Args) != 1 || q.Args[0] != 1 {
		t.Fatalf("args = %v, want the inner LIMIT only", q.Args)
	}
}

// TestSubquery_AliasedInSelectList pins a scalar subquery used as a select
// item with a name, which is how a per-row aggregate is reported.
func TestSubquery_AliasedInSelectList(t *testing.T) {
	inner := stmt.NewSelect(postgres.Dialect{}).
		Exprs(expr.Count()).From(other).Where(expr.EqExpr(otherID, personID))

	q, err := stmt.NewSelect(postgres.Dialect{}).
		Exprs(personID, expr.As(stmt.Subquery[int64](inner), "n")).
		From(people).
		Build()
	if err != nil {
		t.Fatal(err)
	}

	want := `SELECT "p"."id", (SELECT count(*) FROM "other" AS "o" ` +
		`WHERE ("o"."id" = "p"."id")) AS "n" FROM "people" AS "p"`
	if q.SQL != want {
		t.Fatalf("got  %s\nwant %s", q.SQL, want)
	}
}

// TestOrderBy_Case pins ordering by a computed rank, where the sort key is a
// CASE rather than a column and every threshold is bound.
func TestOrderBy_Case(t *testing.T) {
	priority := expr.Case[int]().
		When(expr.Eq(role, 1), expr.Param(0)).
		When(expr.Eq(role, 2), expr.Param(1)).
		Else(expr.Param(9))

	q, err := stmt.NewSelect(postgres.Dialect{}).
		Exprs(personID).
		From(people).
		Order(expr.OrderBy(priority, expr.SortAsc), expr.OrderBy(personID, expr.SortAsc)).
		Build()
	if err != nil {
		t.Fatal(err)
	}

	if !strings.Contains(q.SQL, `ORDER BY CASE WHEN "p"."role_id" = $1 THEN $2`) {
		t.Fatalf("the CASE is not the ordering term: %s", q.SQL)
	}
	if len(q.Args) != 5 {
		t.Fatalf("args = %v, want five bound values", q.Args)
	}
}
