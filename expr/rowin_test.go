package expr_test

import (
	"strings"
	"testing"

	"github.com/ubgo/sqlb/dialect/postgres"
	"github.com/ubgo/sqlb/expr"
	"github.com/ubgo/sqlb/render"
)

// TestRowIn_CompositeKeyMembership is the shape the construct exists for: one
// question about a pair, rather than a chain of ORs that asks a different one.
//
// The three cases are the three ways this could silently render wrong SQL. A
// missing pair of parentheses around the list makes it a flat value list, which
// an engine reads as an arity error at best and as a different query at worst;
// an unbound literal would put a caller's value in the SQL text; and the empty
// list is the case that decides whether a filter nobody narrowed returns
// everything or nothing.
func TestRowIn_CompositeKeyMembership(t *testing.T) {
	pairs := expr.Table{Name: "pairs", Alias: "p"}
	a := expr.Col[int64](pairs, "a")
	b := expr.Col[string](pairs, "b")

	for _, tc := range []struct {
		name string
		pred expr.Predicate
		want string
		args int
	}{
		{
			name: "two pairs",
			pred: expr.RowIn(expr.Row(a, b),
				expr.Row(expr.Param(int64(1)), expr.Param("x")),
				expr.Row(expr.Param(int64(2)), expr.Param("y")),
			),
			want: `("p"."a", "p"."b") IN (($1, $2), ($3, $4))`,
			args: 4,
		},
		{
			name: "not in",
			pred: expr.RowNotIn(expr.Row(a, b),
				expr.Row(expr.Param(int64(1)), expr.Param("x")),
			),
			want: `("p"."a", "p"."b") NOT IN (($1, $2))`,
			args: 2,
		},
		{
			// Fails closed, like In. Widening here would return the rows the
			// caller asked to restrict to a known set.
			name: "empty in",
			pred: expr.RowIn(expr.Row(a, b)),
			want: `FALSE`,
		},
		{
			// And open, like NotIn: "none of nothing" excludes nothing.
			name: "empty not in",
			pred: expr.RowNotIn(expr.Row(a, b)),
			want: `TRUE`,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			args := render.NewArgs(postgres.Dialect{})
			var sb strings.Builder
			tc.pred.WriteSQL(&sb, args)
			if got := sb.String(); got != tc.want {
				t.Errorf("\n got %s\nwant %s", got, tc.want)
			}
			if got := len(args.Values()); got != tc.args {
				t.Errorf("bound %d values, want %d: %v", got, tc.args, args.Values())
			}
		})
	}
}

// TestRowIn_NestsInsideAnotherConstruct is the second of the three questions
// every construct here has to answer: correct alone is not correct in use.
//
// A predicate that rendered without its own parentheses reads correctly on its
// own and changes meaning the moment an OR is put beside it, which is the
// failure that makes a permission check pass for the wrong rows. Or supplies
// the grouping, so what this pins is that RowIn does not add a second layer
// and does not omit the row's own.
func TestRowIn_NestsInsideAnotherConstruct(t *testing.T) {
	pairs := expr.Table{Name: "pairs", Alias: "p"}
	a := expr.Col[int64](pairs, "a")
	b := expr.Col[string](pairs, "b")

	args := render.NewArgs(postgres.Dialect{})
	var sb strings.Builder
	expr.And(
		expr.Or(
			expr.RowIn(expr.Row(a, b), expr.Row(expr.Param(int64(1)), expr.Param("x"))),
			expr.Eq(a, 9),
		),
		expr.Greater(a, int64(0)),
	).WriteSQL(&sb, args)

	const want = `((("p"."a", "p"."b") IN (($1, $2)) OR "p"."a" = $3) AND "p"."a" > $4)`
	if got := sb.String(); got != want {
		t.Errorf("\n got %s\nwant %s", got, want)
	}
}
