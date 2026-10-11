package stmt_test

import (
	"strings"
	"testing"

	"github.com/ubgo/sqlb/dialect/postgres"
	"github.com/ubgo/sqlb/expr"
	"github.com/ubgo/sqlb/render"
	"github.com/ubgo/sqlb/stmt"
)

// TestEngineBinOp_Parenthesises pins the fix for a bug that produced silently
// wrong results.
//
// EngineBinOp used to render "left op right" bare while the core's Binary
// rendered "(left op right)". Nesting an engine operator inside a core one
// therefore produced SQL whose meaning came from the engine's precedence
// table rather than from the author's nesting. pg.BitAnd inside expr.Mul
// rendered "tag & $1 * $2", which Postgres reads as tag & (16 * 2) because *
// binds tighter than &, not as (tag & 16) * 2 as written.
//
// Nothing failed. The statement was valid SQL returning the wrong rows, which
// is the worst shape a bug can take, so the invariant is pinned here rather
// than left to the catalogues that happened to cover it.
func TestEngineBinOp_Parenthesises(t *testing.T) {
	a := render.NewArgs(postgres.Dialect{})
	e := stmt.EngineBinOp[int]("postgres", "test.op", "&",
		render.Raw(func(*render.Args) string { return "l" }),
		render.Raw(func(*render.Args) string { return "r" }),
	)

	if got, want := e.SQL(a), "(l & r)"; got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

// TestEngineBinOp_NestsCorrectly is the same invariant stated as the failure it
// prevents: an engine operator inside a core operator must keep the author's
// nesting, whatever the engine's precedence table says.
func TestEngineBinOp_NestsCorrectly(t *testing.T) {
	tbl := expr.Table{Name: "t", Alias: "t"}
	col := expr.Col[int](tbl, "tag")

	inner := stmt.EngineBinOp[int]("postgres", "test.op", "&", col, expr.Param(16))
	outer := expr.Mul(inner, expr.Param(2))

	q, err := stmt.NewSelect(postgres.Dialect{}).Exprs(outer).From(tbl).Build()
	if err != nil {
		t.Fatal(err)
	}

	want := `SELECT (("t"."tag" & $1) * $2) FROM "t" AS "t"`
	if q.SQL != want {
		t.Fatalf("got  %s\nwant %s", q.SQL, want)
	}
	// Without the inner parentheses this would read as tag & (16 * 2).
	if !strings.Contains(q.SQL, `("t"."tag" & $1)`) {
		t.Error("the engine operator lost its parentheses; precedence now decides the meaning")
	}
}

// TestEngineBinPred_Parenthesises covers the predicate half of the same pair,
// which is the shape the original grep for this bug had to check.
func TestEngineBinPred_Parenthesises(t *testing.T) {
	a := render.NewArgs(postgres.Dialect{})
	p := stmt.EngineBinPred("postgres", "test.op", "@>",
		render.Raw(func(*render.Args) string { return "l" }),
		render.Raw(func(*render.Args) string { return "r" }),
	)

	var b strings.Builder
	p.WriteSQL(&b, a)
	if got := b.String(); !strings.HasPrefix(got, "(") || !strings.HasSuffix(got, ")") {
		t.Fatalf("got %q, want it parenthesised", got)
	}
}
