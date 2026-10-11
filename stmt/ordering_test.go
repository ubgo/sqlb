package stmt_test

import (
	"testing"

	"github.com/ubgo/sqlb/dialect/postgres"
	"github.com/ubgo/sqlb/expr"
	"github.com/ubgo/sqlb/render"
	"github.com/ubgo/sqlb/stmt"
)

var (
	people   = expr.Table{Name: "people", Alias: "p"}
	personID = expr.Col[string](people, "id")
	role     = expr.Col[int](people, "role_id")
	salary   = expr.Col[int64](people, "salary")
)

func sqlOf[D render.Dialect](t *testing.T, s *stmt.Select[D]) string {
	t.Helper()
	q, err := s.Build()
	if err != nil {
		t.Fatal(err)
	}
	return q.SQL
}

// TestOrderBy_Aggregate pins that an aggregate may be an ordering term.
//
// "ORDER BY count(*) DESC LIMIT n" after a GROUP BY is the shape behind a
// leaderboard, a top-N report and most dashboards, and it is the one thing an
// ordering API that only accepts columns cannot express. Ordering here takes
// any expression, so no special case is needed and none exists.
func TestOrderBy_Aggregate(t *testing.T) {
	got := sqlOf(t, stmt.NewSelect(postgres.Dialect{}).
		Exprs(role, expr.As(expr.Count(), "n")).
		From(people).
		GroupBy(role).
		Order(expr.OrderBy(expr.Count(), expr.SortDesc)).
		Limit(10))

	want := `SELECT "p"."role_id", count(*) AS "n" FROM "people" AS "p" ` +
		`GROUP BY "p"."role_id" ORDER BY count(*) DESC LIMIT $1`
	if got != want {
		t.Fatalf("got  %s\nwant %s", got, want)
	}
}

// TestOrderBy_BindsItsValues pins the property that makes a computed ordering
// safe.
//
// Ordering rows into a caller-supplied sequence is normally written by
// interpolating the values into the SQL, which breaks on a value containing a
// quote. An ordering term is an ordinary expression here, so its values go
// through Args like any others and the statement cannot be broken by its own
// data.
func TestOrderBy_BindsItsValues(t *testing.T) {
	order := []string{"c", "a", "b"}

	q, err := stmt.NewSelect(postgres.Dialect{}).
		Exprs(personID).
		From(people).
		Order(expr.OrderBy(expr.Fn[int]("array_position", expr.Param(order), personID), expr.SortAsc)).
		Build()
	if err != nil {
		t.Fatal(err)
	}

	want := `SELECT "p"."id" FROM "people" AS "p" ORDER BY array_position($1, "p"."id") ASC`
	if q.SQL != want {
		t.Fatalf("got  %s\nwant %s", q.SQL, want)
	}
	if len(q.Args) != 1 {
		t.Fatalf("the ordering values were not bound: %v", q.Args)
	}
}

// TestSelect_AggregatesWithoutGroupBy pins that an aggregate needs no GROUP BY
// to be a select item, and that several may be aliased side by side.
//
// Both are ordinary here and both are recurring questions of APIs that treat
// aggregation as a separate mode rather than as expressions that happen to
// aggregate.
func TestSelect_AggregatesWithoutGroupBy(t *testing.T) {
	got := sqlOf(t, stmt.NewSelect(postgres.Dialect{}).
		Exprs(
			expr.As(expr.Count(), "n"),
			expr.As(expr.Sum(salary), "total"),
			expr.As(expr.Min(salary), "lo"),
			expr.As(expr.Max(salary), "hi"),
			expr.As(expr.Avg(salary), "mean"),
		).
		From(people))

	want := `SELECT count(*) AS "n", sum("p"."salary") AS "total", ` +
		`min("p"."salary") AS "lo", max("p"."salary") AS "hi", ` +
		`avg("p"."salary") AS "mean" FROM "people" AS "p"`
	if got != want {
		t.Fatalf("got  %s\nwant %s", got, want)
	}
}

// TestJoin_ToADerivedTable pins joining a statement to a subquery, which is
// how greatest-n-per-group is written and what makes a self join with an
// aggregate possible.
//
//	SELECT a.id, a.rev FROM t a
//	INNER JOIN (SELECT id, MAX(rev) rev FROM t GROUP BY id) b
//	ON a.id = b.id AND a.rev = b.rev
//
// Three things have to hold at once: the same physical table appears twice
// under different aliases, the derived table carries its own GROUP BY, and the
// join condition compares columns of the outer table to columns of the derived
// one. Each is ordinary on its own; this pins them composed, which is where a
// builder that treats a subquery as a special case comes apart.
func TestJoin_ToADerivedTable(t *testing.T) {
	outer := expr.Table{Name: "revisions", Alias: "a"}
	inner := expr.Table{Name: "revisions", Alias: "i"}
	derived := expr.Table{Name: "b", Alias: "b"}

	aID, aRev := expr.Col[int64](outer, "id"), expr.Col[int](outer, "rev")
	iID, iRev := expr.Col[int64](inner, "id"), expr.Col[int](inner, "rev")
	bID, bRev := expr.Col[int64](derived, "id"), expr.Col[int](derived, "rev")

	maxRev := stmt.NewSelect(postgres.Dialect{}).
		Exprs(expr.As(iID, "id"), expr.As(expr.Max(iRev), "rev")).
		From(inner).
		GroupBy(iID)

	got := sqlOf(t, stmt.NewSelect(postgres.Dialect{}).
		Exprs(aID, aRev).
		From(outer).
		Join(stmt.Join{
			Type:   stmt.JoinInner,
			Table:  derived,
			Source: expr.Derived(maxRev, "b"),
			On:     []expr.Predicate{expr.EqExpr(aID, bID), expr.EqExpr(aRev, bRev)},
		}))

	want := `SELECT "a"."id", "a"."rev" FROM "revisions" AS "a" ` +
		`INNER JOIN (SELECT "i"."id" AS "id", max("i"."rev") AS "rev" ` +
		`FROM "revisions" AS "i" GROUP BY "i"."id") AS "b" ` +
		`ON ("a"."id" = "b"."id" AND "a"."rev" = "b"."rev")`
	if got != want {
		t.Fatalf("got  %s\nwant %s", got, want)
	}
}
