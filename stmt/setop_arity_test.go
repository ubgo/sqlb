package stmt_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/ubgo/sqlb/dialect/postgres"
	"github.com/ubgo/sqlb/expr"
	"github.com/ubgo/sqlb/stmt"
)

// TestSetOp_BranchesMustAgreeInWidth pins the refusal that replaces an engine
// error about SQL the caller never wrote.
//
// The path that found it: scan.All and the binding API append the reader's
// columns to the statement they are handed, which is the LEFT branch, so a
// left branch that also declares its own select list ends up one column wider
// than the right. Postgres then reports "SELECTs to the left and right of
// EXCEPT do not have the same number of result columns" against a query with
// columns the caller did not type. Build now names both counts instead.
func TestSetOp_BranchesMustAgreeInWidth(t *testing.T) {
	var (
		paid   = expr.Table{Name: "paid", Alias: "p"}
		failed = expr.Table{Name: "failed", Alias: "f"}
		pID    = expr.Col[int64](paid, "tenant")
		pAmt   = expr.Col[int64](paid, "amount")
		fID    = expr.Col[int64](failed, "tenant")
	)
	sel := func() *stmt.Select[postgres.Dialect] { return stmt.NewSelect(postgres.Dialect{}) }

	// Two items on the left, one on the right: the shape scan.All produces.
	_, err := sel().Exprs(pID, pAmt).From(paid).
		Except(sel().Exprs(fID).From(failed)).Build()
	if !errors.Is(err, stmt.ErrSetOpArity) {
		t.Fatalf("want ErrSetOpArity, got %v", err)
	}
	// The counts are in the message, because "they differ" without saying how
	// sends the reader back to count columns by hand.
	for _, want := range []string{"2", "1"} {
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("error %v does not name the count %s", err, want)
		}
	}

	// Equal widths build, which is the case that must not regress.
	if _, err := sel().Exprs(pID).From(paid).
		Except(sel().Exprs(fID).From(failed)).Build(); err != nil {
		t.Fatalf("matching widths must build: %v", err)
	}

	// checkSetOpArity skips a zero-width branch rather than comparing it. That
	// branch is unreachable today, and this is what says so: a statement with
	// no bindings and no expressions is refused by ErrNoColumns first, so the
	// skip is defensive rather than dead. If this ever stops being
	// ErrNoColumns, the skip is the thing to re-examine.
	if _, err := sel().From(paid).
		Except(sel().Exprs(fID).From(failed)).Build(); !errors.Is(err, stmt.ErrNoColumns) {
		t.Fatalf("want ErrNoColumns for a branchless select list, got %v", err)
	}
}
