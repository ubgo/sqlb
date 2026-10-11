package pg_test

import (
	"strings"
	"testing"

	"github.com/ubgo/sqlb"
	"github.com/ubgo/sqlb/pg"
)

// TestEngineOperators_NestInsideCoreOperators pins the shape of a bug that
// produced valid SQL returning the wrong rows.
//
// Engine operators used to render bare while the core's arithmetic rendered
// parenthesised, so nesting one inside the other handed the meaning to the
// engine's precedence table instead of to the author's nesting. BitAnd inside
// Mul produced "tag & $1 * $2", which Postgres reads as tag & (16 * 2) because
// * binds tighter than &, rather than (tag & 16) * 2 as written.
//
// The catalogues cover each operator on its own, which is where the bug hid:
// it only appears under composition. This is the composed case, and it lives
// beside the operators rather than in a comparison document, so it survives
// that document being deleted.
func TestEngineOperators_NestInsideCoreOperators(t *testing.T) {
	tbl := sqlb.Table{Name: "flags", Alias: "f"}
	tag := sqlb.Col[int](tbl, "tag")

	for _, tc := range []struct {
		name string
		expr sqlb.Expr[int]
		want string
	}{
		{
			"bitand inside mul",
			sqlb.Mul(pg.BitAnd(tag, sqlb.Param(16)), sqlb.Param(2)),
			`(("f"."tag" & $1) * $2)`,
		},
		{
			"bitor inside add",
			sqlb.Add(pg.BitOr(tag, sqlb.Param(1)), sqlb.Param(3)),
			`(("f"."tag" | $1) + $2)`,
		},
		{
			"shift inside mul",
			sqlb.Mul(pg.ShiftLeft(tag, 2), sqlb.Param(5)),
			`(("f"."tag" << $1) * $2)`,
		},
		{
			// ~ binds tighter than * in Postgres, so this was already correct.
			// It is parenthesised anyway: correct by construction beats correct
			// by precedence table, and the parentheses cost nothing.
			"unary bitwise NOT inside mul",
			sqlb.Mul(pg.BitNot(tag), sqlb.Param(2)),
			`((~"f"."tag") * $1)`,
		},
		{
			"core operator inside an engine operator",
			pg.BitAnd(sqlb.Add(tag, sqlb.Param(1)), sqlb.Param(16)),
			`(("f"."tag" + $1) & $2)`,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			q, err := sqlb.NewSelect(sqlb.PostgresDialect{}).Exprs(tc.expr).From(tbl).Build()
			if err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(q.SQL, tc.want) {
				t.Fatalf("got  %s\nwant it to contain %s", q.SQL, tc.want)
			}
		})
	}
}

// TestBitwise_FailsUnderAnotherDialect pins that the bitwise family stays
// engine-scoped.
//
// SQLite spells bitwise AND the same way Postgres does, and that is exactly why
// the guard matters: a library that rendered these for three engines and broke
// on ClickHouse, which spells them bitAnd(), would fail at the database rather
// than at Build. Three of four engines agreeing is not the rule the core lives
// by.
func TestBitwise_FailsUnderAnotherDialect(t *testing.T) {
	tbl := sqlb.Table{Name: "flags", Alias: "f"}
	tag := sqlb.Col[int](tbl, "tag")

	_, err := sqlb.NewSelect(sqlb.SQLiteDialect{}).
		Exprs(pg.BitAnd(tag, sqlb.Param(16))).
		From(tbl).
		Build()

	if err == nil {
		t.Fatal("a Postgres-only operator rendered under SQLite")
	}
	if !strings.Contains(err.Error(), "sqlite") {
		t.Errorf("the error should name the dialect it failed under: %v", err)
	}
}

// TestDistinctOn_TakesAnyExpression pins that DISTINCT ON is not restricted to
// bare columns.
//
// The restriction would be easy to introduce without noticing, because a bare
// column is what every example uses, and a builder that took a column name
// rather than an expression would pass every one of them. Postgres accepts any
// expression there, and deduplicating on a COALESCE or a function result is
// the case that makes the clause worth having.
func TestDistinctOn_TakesAnyExpression(t *testing.T) {
	tbl := sqlb.Table{Name: "events", Alias: "e"}
	name := sqlb.Col[*string](tbl, "name")
	at := sqlb.Col[int64](tbl, "at")

	for _, tc := range []struct {
		name string
		on   sqlb.SQLer
		want string
	}{
		{"bare column", at, `DISTINCT ON ("e"."at")`},
		{"function result over a nullable column", sqlb.Coalesce(name, sqlb.Nullable(sqlb.Param("empty"))), `DISTINCT ON (COALESCE("e"."name", $1))`},
		{"arithmetic", sqlb.Div(at, sqlb.Param(int64(3600))), `DISTINCT ON (("e"."at" / $1))`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			q, err := sqlb.NewSelect(sqlb.PostgresDialect{}).
				Exprs(at).From(tbl).Clause(pg.DistinctOn(tc.on)).Build()
			if err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(q.SQL, tc.want) {
				t.Fatalf("got  %s\nwant it to contain %s", q.SQL, tc.want)
			}
		})
	}
}
