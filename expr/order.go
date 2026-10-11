// An ORDER BY term is an expression plus a direction, which is why it lives in
// the expression layer rather than in the statement that happens to use it.
// Both SELECT and a window function's ORDER BY are built from these.

package expr

import (
	"fmt"
	"strings"

	"github.com/ubgo/sqlb/render"
)

const kwUsing = "USING"

// OrderTerm is one ORDER BY item. Nulls and Using are hooks the core
// renders when set; the engine package that has NULLS FIRST or USING op
// sets them. The core names neither, because MySQL has no NULLS FIRST.
type OrderTerm struct {
	Expr      func(a *render.Args) string
	Direction SortDirection
	// Nulls places NULLs relative to values. Empty means the engine's own
	// default, which differs between them: Postgres sorts NULLs last when
	// ascending, SQLite and MySQL first.
	Nulls NullsOrder
	// Using renders "USING op" instead of ASC/DESC, for an ordering defined by
	// an operator rather than by a direction.
	//
	// It is a bare string and stays one, for the reason expr.Binary's operator
	// does: the legal operators are whatever the engine's operator classes
	// define, so a list here would refuse a valid one. It is an author's
	// constant, never a value from a request, and it reaches the statement
	// text unquoted because an operator cannot be quoted.
	Using string
}

// OrderBy makes an order term from any typed expression.
func OrderBy[T any](e Expr[T], dir SortDirection) OrderTerm {
	return OrderTerm{Expr: e.SQL, Direction: dir}
}

// WriteSQL renders the term into b. Exported because ORDER BY is written by
// the statement layer and by a window function, which are different packages.
func (t OrderTerm) WriteSQL(b *strings.Builder, a *render.Args) { t.writeSQL(b, a) }

func (t OrderTerm) writeSQL(b *strings.Builder, a *render.Args) {
	b.WriteString(t.Expr(a))
	if t.Using != "" {
		b.WriteString(sepSpace)
		b.WriteString(kwUsing)
		b.WriteString(sepSpace)
		b.WriteString(t.Using)
	} else {
		if !t.Direction.valid() {
			a.Fail(fmt.Errorf("%w: sort direction %q", ErrUnknownValue, string(t.Direction)))
			return
		}
		b.WriteString(sepSpace)
		b.WriteString(string(t.Direction))
	}
	if t.Nulls != "" {
		if !t.Nulls.valid() {
			a.Fail(fmt.Errorf("%w: NULL placement %q", ErrUnknownValue, string(t.Nulls)))
			return
		}
		b.WriteString(sepSpace)
		b.WriteString(string(t.Nulls))
	}
}
