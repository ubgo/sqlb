package expr_test

import (
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/ubgo/sqlb/dialect/clickhouse"
	"github.com/ubgo/sqlb/dialect/mysql"
	"github.com/ubgo/sqlb/dialect/postgres"
	"github.com/ubgo/sqlb/dialect/sqlite"
	"github.com/ubgo/sqlb/expr"
	"github.com/ubgo/sqlb/render"
)

// renderUnder renders p under d and returns the text, the bound values and the
// first recorded failure.
func renderUnder(d render.Dialect, p expr.Predicate) (string, []any, error) {
	a := render.NewArgs(d)
	var b strings.Builder
	p.WriteSQL(&b, a)
	return b.String(), a.Values(), a.Err()
}

var foldCol = expr.Col[string](expr.Table{Name: "user", Alias: "u"}, "email")

// TestFold_RendersPerDialect pins every predicate under every shipped dialect
// that can take it. The pattern forms are the only ones whose spelling
// changes, and only on the dialect declaring ILIKE; the bound value is the
// same on every engine, which is what makes the answers comparable.
func TestFold_RendersPerDialect(t *testing.T) {
	type want struct {
		sql  string
		args []any
	}
	for _, tc := range []struct {
		name   string
		pred   expr.Predicate
		pg     want
		sqlite want
		mysql  want
	}{
		{"EqFold", expr.EqFold(foldCol, "Ada@X"),
			want{`lower("u"."email") = lower($1)`, []any{"Ada@X"}},
			want{`lower("u"."email") = lower(?)`, []any{"Ada@X"}},
			want{"lower(`u`.`email`) = lower(?)", []any{"Ada@X"}}},
		{"InFold", expr.InFold(foldCol, "A", "b"),
			want{`lower("u"."email") IN (lower($1), lower($2))`, []any{"A", "b"}},
			want{`lower("u"."email") IN (lower(?), lower(?))`, []any{"A", "b"}},
			want{"lower(`u`.`email`) IN (lower(?), lower(?))", []any{"A", "b"}}},
		{"NotInFold", expr.NotInFold(foldCol, "A"),
			want{`lower("u"."email") NOT IN (lower($1))`, []any{"A"}},
			want{`lower("u"."email") NOT IN (lower(?))`, []any{"A"}},
			want{"lower(`u`.`email`) NOT IN (lower(?))", []any{"A"}}},
		{"ContainsFold", expr.ContainsFold(foldCol, "50%"),
			want{`"u"."email" ILIKE $1 ESCAPE '!'`, []any{"%50!%%"}},
			want{`lower("u"."email") LIKE lower(?) ESCAPE '!'`, []any{"%50!%%"}},
			want{"lower(`u`.`email`) LIKE lower(?) ESCAPE '!'", []any{"%50!%%"}}},
		{"StartsWithFold", expr.StartsWithFold(foldCol, "a_b"),
			want{`"u"."email" ILIKE $1 ESCAPE '!'`, []any{"a!_b%"}},
			want{`lower("u"."email") LIKE lower(?) ESCAPE '!'`, []any{"a!_b%"}},
			want{"lower(`u`.`email`) LIKE lower(?) ESCAPE '!'", []any{"a!_b%"}}},
		{"EndsWithFold", expr.EndsWithFold(foldCol, "wow!"),
			want{`"u"."email" ILIKE $1 ESCAPE '!'`, []any{"%wow!!"}},
			want{`lower("u"."email") LIKE lower(?) ESCAPE '!'`, []any{"%wow!!"}},
			want{"lower(`u`.`email`) LIKE lower(?) ESCAPE '!'", []any{"%wow!!"}}},
	} {
		for _, d := range []struct {
			d render.Dialect
			w want
		}{{postgres.Dialect{}, tc.pg}, {sqlite.Dialect{}, tc.sqlite}, {mysql.Dialect{}, tc.mysql}} {
			sql, args, err := renderUnder(d.d, tc.pred)
			if err != nil {
				t.Errorf("%s under %T: %v", tc.name, d.d, err)
				continue
			}
			if sql != d.w.sql || !reflect.DeepEqual(args, d.w.args) {
				t.Errorf("%s under %T:\n got %s %v\nwant %s %v", tc.name, d.d, sql, args, d.w.sql, d.w.args)
			}
		}
	}
}

// TestFold_EmptyListsFailClosed pins that an empty InFold matches nothing and
// an empty NotInFold matches everything, as In and NotIn do. The opposite would
// be "IN ()", a syntax error, or a dropped predicate, which in an auth query
// returns the rows the caller asked to exclude.
func TestFold_EmptyListsFailClosed(t *testing.T) {
	for _, tc := range []struct {
		pred expr.Predicate
		want string
	}{{expr.InFold(foldCol), "FALSE"}, {expr.NotInFold(foldCol), "TRUE"}} {
		sql, args, err := renderUnder(postgres.Dialect{}, tc.pred)
		if err != nil || sql != tc.want || len(args) != 0 {
			t.Errorf("got %q %v %v, want %q and no arguments", sql, args, err, tc.want)
		}
	}
}

// ilikeDeclined is an outside dialect implementing the capability and
// answering false, which must get the portable spelling, as a dialect that
// omits the interface does.
type ilikeDeclined struct{ sqlite.Dialect }

func (ilikeDeclined) SupportsILike() bool { return false }

// ilikeOutside is an outside dialect that declares ILIKE: a CockroachDB, say.
// It must get ILIKE without this module naming it, which is the reason this is
// a capability rather than a check of the engine's name.
type ilikeOutside struct{ sqlite.Dialect }

func (ilikeOutside) SupportsILike() bool { return true }

func TestFold_TheCapabilityChoosesTheSpelling(t *testing.T) {
	pred := expr.ContainsFold(foldCol, "x")
	for _, tc := range []struct {
		d    render.Dialect
		want string
	}{
		{ilikeDeclined{}, `lower("u"."email") LIKE lower(?) ESCAPE '!'`},
		{ilikeOutside{}, `"u"."email" ILIKE ? ESCAPE '!'`},
	} {
		sql, _, err := renderUnder(tc.d, pred)
		if err != nil || sql != tc.want {
			t.Errorf("%T: got %q %v, want %q", tc.d, sql, err, tc.want)
		}
	}
}

// TestFold_PatternFormsNeedEscaping pins that a dialect without ESCAPE refuses
// the pattern forms rather than rendering a pattern whose typed % matches
// everything. ClickHouse is the shipped dialect that declines it. EqFold and
// the lists take no pattern and render there.
func TestFold_PatternFormsNeedEscaping(t *testing.T) {
	for name, p := range map[string]expr.Predicate{
		"ContainsFold":   expr.ContainsFold(foldCol, "x"),
		"StartsWithFold": expr.StartsWithFold(foldCol, "x"),
		"EndsWithFold":   expr.EndsWithFold(foldCol, "x"),
	} {
		if _, _, err := renderUnder(clickhouse.Dialect{}, p); !errors.Is(err, expr.ErrNoLikeEscape) {
			t.Errorf("%s under ClickHouse: %v, want ErrNoLikeEscape", name, err)
		}
	}
	for name, p := range map[string]expr.Predicate{
		"EqFold":    expr.EqFold(foldCol, "x"),
		"InFold":    expr.InFold(foldCol, "x"),
		"NotInFold": expr.NotInFold(foldCol, "x"),
	} {
		if _, _, err := renderUnder(clickhouse.Dialect{}, p); err != nil {
			t.Errorf("%s under ClickHouse: %v, want it to render", name, err)
		}
	}
}

// TestFold_ComposesInsideAndOrNot checks the one composition that could go
// wrong: a fold predicate under NOT and beside another predicate, where a
// missing parenthesis would let NOT bind to the column alone.
func TestFold_ComposesInsideAndOrNot(t *testing.T) {
	p := expr.And(expr.Not(expr.ContainsFold(foldCol, "a")), expr.EqFold(foldCol, "b"))
	sql, args, err := renderUnder(postgres.Dialect{}, p)
	want := `(NOT ("u"."email" ILIKE $1 ESCAPE '!') AND lower("u"."email") = lower($2))`
	if err != nil || sql != want || !reflect.DeepEqual(args, []any{"%a%", "b"}) {
		t.Errorf("got %s %v %v\nwant %s", sql, args, err, want)
	}
}

// TestFold_ReusedPredicateRendersTheSame pins the third question: a predicate
// held in a variable and rendered twice, or under two dialects, gives the same
// answer each time, because it captures its value and nothing mutable.
func TestFold_ReusedPredicateRendersTheSame(t *testing.T) {
	p := expr.InFold(foldCol, "a", "b")
	first, _, _ := renderUnder(sqlite.Dialect{}, p)
	_, _, _ = renderUnder(postgres.Dialect{}, p)
	second, _, _ := renderUnder(sqlite.Dialect{}, p)
	if first != second {
		t.Errorf("rendered %q then %q", first, second)
	}
}

// TestFold_ZeroExprIsRefused pins that a zero Expr, which has no render
// function, fails the build rather than panicking inside lower().
func TestFold_ZeroExprIsRefused(t *testing.T) {
	var zero expr.Expr[string]
	for name, p := range map[string]expr.Predicate{
		"EqFold":       expr.EqFold(zero, "x"),
		"InFold":       expr.InFold(zero, "x"),
		"ContainsFold": expr.ContainsFold(zero, "x"),
	} {
		if _, _, err := renderUnder(postgres.Dialect{}, p); !errors.Is(err, expr.ErrZeroExpr) {
			t.Errorf("%s over a zero Expr: %v, want ErrZeroExpr", name, err)
		}
	}
}
