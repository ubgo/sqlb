package expr_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/ubgo/sqlb/dialect/postgres"
	"github.com/ubgo/sqlb/expr"
	"github.com/ubgo/sqlb/render"
)

// The four doors in this layer that write a caller's string into statement
// text unquoted: Binary and Compare take an operator, Fn and Aggregate take a
// function name. Both halves at each, as the audit requires.
//
// The doors are here and not only in stmt because this is the layer an
// ordinary caller reaches. stmt's Engine* are for an engine package; these are
// what a query builds on, and examples/pgsurface/periodcompare hands Compare
// an operator derived from a request.

const shpInjection = `= ANY (SELECT 1); DROP TABLE users; --`

var (
	shpT  = expr.Table{Name: "users", Alias: "u"}
	shpID = expr.Col[int64](shpT, "id")
)

// renderExpr renders one expression through a real dialect and returns its SQL
// or the first error, which is what a caller gets from Build. It is separate
// from closedset_test.go's render1, which takes a WriteSQL clause rather than
// an SQLer.
func renderExpr(s render.SQLer) (string, error) {
	a := render.NewArgs(postgres.Dialect{})
	out := s.SQL(a)
	return out, a.Err()
}

// renderPred is renderExpr for a predicate.
func renderPred(p expr.Predicate) (string, error) {
	a := render.NewArgs(postgres.Dialect{})
	out := expr.PredicateSQL(p, a)
	return out, a.Err()
}

// TestShape_ExprDoorsRefuseAHostileValue pins the first half.
func TestShape_ExprDoorsRefuseAHostileValue(t *testing.T) {
	cases := map[string]func() (string, error){
		"Binary": func() (string, error) {
			return renderExpr(expr.Binary[int64](shpID, shpInjection, shpID))
		},
		"Compare": func() (string, error) {
			return renderPred(expr.Compare(shpID, shpInjection, shpID))
		},
		"Fn": func() (string, error) {
			return renderExpr(expr.Fn[int64](`f(1); DROP TABLE users; --`, shpID))
		},
		"Aggregate": func() (string, error) {
			return renderExpr(expr.Aggregate[int64](`count(*); DROP TABLE users; --`, []render.SQLer{shpID}))
		},
	}
	for name, run := range cases {
		t.Run(name, func(t *testing.T) {
			sql, err := run()
			if err == nil {
				t.Fatalf("a hostile value rendered: %s", sql)
			}
			if !errors.Is(err, expr.ErrUnknownValue) {
				t.Errorf("refused with %v, which is not the typed refusal", err)
			}
		})
	}
}

// TestShape_ExprDoorsTakeEveryRealOperator pins the second half, and pins it
// against the layer's own constant list rather than a sample: OpValues is what
// the builder writes from, so a constant added to it is covered here the day
// it lands.
func TestShape_ExprDoorsTakeEveryRealOperator(t *testing.T) {
	for _, op := range expr.OpValues {
		if _, err := renderPred(expr.Compare(shpID, op, shpID)); err != nil {
			t.Errorf("Compare refused the core's own operator %q: %v", op, err)
		}
	}
	// The word operators the core names outside OpValues, which are the ones a
	// characters-only rule would have refused.
	for _, op := range []string{
		expr.OpIsDistinctFrom, expr.OpIsNotDistinctFrom, expr.OpIsTrue, expr.OpIsFalse,
		expr.OpIsUnknown, expr.OpLike, expr.OpNotLike, expr.OpConcat,
		expr.OpAdd, expr.OpSub, expr.OpMul, expr.OpDiv, expr.OpMod,
	} {
		if _, err := renderExpr(expr.Binary[int64](shpID, op, shpID)); err != nil {
			t.Errorf("Binary refused the core's own operator %q: %v", op, err)
		}
	}
	for _, name := range []string{"count", "lower", "pg_catalog.date_trunc", "ST_DWithin", "uuid_generate_v7"} {
		if _, err := renderExpr(expr.Fn[int64](name, shpID)); err != nil {
			t.Errorf("Fn refused the function name %q: %v", name, err)
		}
	}
}

// TestShape_QuotedIdentifiersAreStillTheAnswerForNames pins the boundary
// between the three shapes and everything else: a name that is an identifier
// goes through the dialect and may hold anything, which is why As and
// OutputCol need no shape check and must not have one.
func TestShape_QuotedIdentifiersAreStillTheAnswerForNames(t *testing.T) {
	hostile := `x"; DROP TABLE users; --`
	got, err := renderExpr(expr.As(shpID, hostile))
	if err != nil {
		t.Fatalf("an alias was refused: %v", err)
	}
	if !strings.Contains(got, `"x""; DROP TABLE users; --"`) {
		t.Errorf("the alias was not quoted as one identifier: %s", got)
	}
}
