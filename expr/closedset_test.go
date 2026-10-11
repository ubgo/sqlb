package expr_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/ubgo/sqlb/dialect/postgres"
	"github.com/ubgo/sqlb/expr"
	"github.com/ubgo/sqlb/render"
)

// hostile is the shape that made this test necessary: ten named string types
// in this repository carried constants, no validation and a verbatim render,
// so a value like this one reached the statement text. Two of them were a live
// injection on a real server.
const hostile = "ASC; DROP TABLE users; --"

// render1 renders one SQLer and returns the error Args collected, which is
// where a closed-set refusal lands: WriteSQL cannot return one.
func render1(w interface {
	WriteSQL(*strings.Builder, *render.Args)
}) error {
	var b strings.Builder
	a := render.NewArgs(postgres.Dialect{})
	w.WriteSQL(&b, a)
	return a.Err()
}

// TestClosedSets_RefuseAValueOutsideTheirConstants pins both halves at every
// core site that writes a closed set verbatim. Both halves matter: a check
// that refuses everything passes the first and is useless.
func TestClosedSets_RefuseAValueOutsideTheirConstants(t *testing.T) {
	col := expr.Col[int64](expr.Table{Name: "users", Alias: "u"}, "age")

	refused := map[string]interface {
		WriteSQL(*strings.Builder, *render.Args)
	}{
		"a sort direction": expr.OrderTerm{Expr: col.SQL, Direction: expr.SortDirection(hostile)},
		"a NULL placement": expr.OrderTerm{Expr: col.SQL, Direction: expr.SortAsc, Nulls: expr.NullsOrder(hostile)},
	}
	for name, w := range refused {
		t.Run(name, func(t *testing.T) {
			if err := render1(w); !errors.Is(err, expr.ErrUnknownValue) {
				t.Fatalf("want ErrUnknownValue, got %v", err)
			}
		})
	}

	accepted := map[string]interface {
		WriteSQL(*strings.Builder, *render.Args)
	}{
		"ascending":            expr.OrderTerm{Expr: col.SQL, Direction: expr.SortAsc},
		"descending":           expr.OrderTerm{Expr: col.SQL, Direction: expr.SortDesc},
		"nulls first":          expr.OrderTerm{Expr: col.SQL, Direction: expr.SortAsc, Nulls: expr.NullsFirst},
		"nulls last":           expr.OrderTerm{Expr: col.SQL, Direction: expr.SortAsc, Nulls: expr.NullsLast},
		"no NULL placement":    expr.OrderTerm{Expr: col.SQL, Direction: expr.SortDesc},
		"an operator ordering": expr.OrderTerm{Expr: col.SQL, Using: "<"},
	}
	for name, w := range accepted {
		t.Run(name, func(t *testing.T) {
			if err := render1(w); err != nil {
				t.Fatalf("a legitimate value must build: %v", err)
			}
		})
	}

	// A frame mode is NOT in the refused set, and that is deliberate. GROUPS
	// is a Postgres and SQLite mode that MySQL 8 lacks, so by the universality
	// rule the vocabulary is the engine's and only the mechanism is here —
	// examples/pgsurface writes GROUPS today. A closed check on FrameMode was
	// added on 2026-09-24 and reverted the same hour when this repository's own
	// example stopped building, which is what this case records.
	if err := render1(expr.NewWindow().Frame("GROUPS", expr.UnboundedPreceding(), expr.CurrentRow())); err != nil {
		t.Fatalf("an engine's own frame mode must build: %v", err)
	}

	// Every constant of every validated set still builds, walked from the
	// package's own iteration slices rather than typed out here, so a
	// constant added later is covered the day it lands.
	for _, d := range expr.SortDirectionValues {
		if err := render1(expr.OrderTerm{Expr: col.SQL, Direction: d}); err != nil {
			t.Errorf("SortDirection %q must build: %v", string(d), err)
		}
	}
	for _, n := range expr.NullsOrderValues {
		if err := render1(expr.OrderTerm{Expr: col.SQL, Direction: expr.SortAsc, Nulls: n}); err != nil {
			t.Errorf("NullsOrder %q must build: %v", string(n), err)
		}
	}
}
