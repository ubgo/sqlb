package stmt_test

import (
	"errors"
	"testing"

	"github.com/ubgo/sqlb/dialect/postgres"
	"github.com/ubgo/sqlb/expr"
	"github.com/ubgo/sqlb/stmt"
)

// TestDuplicateAlias_KeysOnTheEffectiveName checks the collision rule from
// both sides for tables that carry no alias.
//
// The check keys on SourceAlias, which used to be "" for every unaliased
// table. Two different tables therefore collided under the empty key — a false
// positive that refused a correct statement — while a genuine self-join with
// no aliases, which really is ambiguous, produced the same message for the
// opposite reason. Falling back to the table's own name separates them.
func TestDuplicateAlias_KeysOnTheEffectiveName(t *testing.T) {
	orders := expr.Table{Name: "orders"}
	customers := expr.Table{Name: "customers"}
	oID := expr.Col[int64](orders, "customer_id")
	cID := expr.Col[int64](customers, "id")

	t.Run("two different unaliased tables are fine", func(t *testing.T) {
		_, err := stmt.NewSelect(postgres.Dialect{}).
			Exprs(cID).
			From(orders).
			Join(stmt.Join{
				Type:  stmt.JoinInner,
				Table: customers,
				On:    []stmt.Predicate{expr.EqExpr(oID, cID)},
			}).
			Build()
		if err != nil {
			t.Fatalf("refused a statement with no collision: %v", err)
		}
	})

	t.Run("the same unaliased table twice is a collision", func(t *testing.T) {
		_, err := stmt.NewSelect(postgres.Dialect{}).
			Exprs(oID).
			From(orders).
			Join(stmt.Join{
				Type:  stmt.JoinInner,
				Table: orders,
				On:    []stmt.Predicate{expr.EqExpr(oID, oID)},
			}).
			Build()
		if !errors.Is(err, stmt.ErrDuplicateAlias) {
			t.Fatalf("got %v, want ErrDuplicateAlias: a self-join with no aliases is ambiguous", err)
		}
	})
}
