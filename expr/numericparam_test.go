package expr_test

import (
	"testing"

	"github.com/ubgo/sqlb/dialect/mysql"
	"github.com/ubgo/sqlb/dialect/postgres"
	"github.com/ubgo/sqlb/dialect/sqlite"
	"github.com/ubgo/sqlb/expr"
	"github.com/ubgo/sqlb/render"
)

// age is a named integer type, which must be typed like the int64 it is.
type age int64

// TestNumericParam_TypesABareIntegerOnMySQL pins where MySQL's parameter
// typing applies: a bare integer parameter in arithmetic or COALESCE, never a
// column, a float, a string, a NULL, or anything on another engine. MySQL
// widens COALESCE(sum(x), ?) and sum(x) + ? to DECIMAL(65,30), which is what
// sqlbtest's crud_transcript case found; see render.NumericParameters.
func TestNumericParam_TypesABareIntegerOnMySQL(t *testing.T) {
	tb := expr.Table{Name: "t", Alias: "t"}
	n := expr.Col[int64](tb, "n")
	f := expr.Col[float64](tb, "f")
	s := expr.Col[string](tb, "s")
	u := expr.Col[uint64](tb, "u")
	a := expr.Col[age](tb, "a")
	np := expr.Col[*int64](tb, "np")
	for _, tc := range []struct {
		name               string
		e                  render.SQLer
		mysql, pg, sqlite3 string
	}{
		{"COALESCE of a sum", expr.Coalesce(expr.Sum(n), expr.Param(int64(0))),
			"COALESCE(sum(`t`.`n`), CAST(? AS SIGNED))", `COALESCE(sum("t"."n"), $1)`, `COALESCE(sum("t"."n"), ?)`},
		{"arithmetic, parameter on the right", expr.Add(expr.Sum(n), expr.Param(int64(1))),
			"(sum(`t`.`n`) + CAST(? AS SIGNED))", `(sum("t"."n") + $1)`, `(sum("t"."n") + ?)`},
		{"arithmetic, parameter on the left", expr.Sub(expr.Param(int64(1)), n),
			"(CAST(? AS SIGNED) - `t`.`n`)", `($1 - "t"."n")`, `(? - "t"."n")`},
		{"two parameters", expr.Mul(expr.Param(int64(2)), expr.Param(int64(3))),
			"(CAST(? AS SIGNED) * CAST(? AS SIGNED))", `($1 * $2)`, `(? * ?)`},
		{"unsigned", expr.Add(u, expr.Param(uint64(1))),
			"(`t`.`u` + CAST(? AS UNSIGNED))", `("t"."u" + $1)`, `("t"."u" + ?)`},
		{"a named integer type", expr.Add(a, expr.Param(age(1))),
			"(`t`.`a` + CAST(? AS SIGNED))", `("t"."a" + $1)`, `("t"."a" + ?)`},
		{"a non-nil pointer to an integer", expr.Coalesce(np, expr.Param(func() *int64 { v := int64(1); return &v }())),
			"COALESCE(`t`.`np`, CAST(? AS SIGNED))", `COALESCE("t"."np", $1)`, `COALESCE("t"."np", ?)`},
		{"a nil pointer stays bare", expr.Coalesce(np, expr.Param[*int64](nil)),
			"COALESCE(`t`.`np`, ?)", `COALESCE("t"."np", $1)`, `COALESCE("t"."np", ?)`},
		{"a float stays bare", expr.Add(f, expr.Param(0.5)),
			"(`t`.`f` + ?)", `("t"."f" + $1)`, `("t"."f" + ?)`},
		{"a string stays bare", expr.Coalesce(s, expr.Param("x")),
			"COALESCE(`t`.`s`, ?)", `COALESCE("t"."s", $1)`, `COALESCE("t"."s", ?)`},
		{"columns stay bare", expr.Add(n, n),
			"(`t`.`n` + `t`.`n`)", `("t"."n" + "t"."n")`, `("t"."n" + "t"."n")`},
		{"a nested expression is not a bare parameter", expr.Coalesce(n, expr.Add(n, expr.Param(int64(1)))),
			"COALESCE(`t`.`n`, (`t`.`n` + CAST(? AS SIGNED)))", `COALESCE("t"."n", ("t"."n" + $1))`, `COALESCE("t"."n", ("t"."n" + ?))`},
	} {
		for _, d := range []struct {
			d    render.Dialect
			want string
		}{{mysql.Dialect{}, tc.mysql}, {postgres.Dialect{}, tc.pg}, {sqlite.Dialect{}, tc.sqlite3}} {
			args := render.NewArgs(d.d)
			if got := tc.e.SQL(args); got != d.want || args.Err() != nil {
				t.Errorf("%s under %T:\n got %s %v\nwant %s", tc.name, d.d, got, args.Err(), d.want)
			}
		}
	}
}
