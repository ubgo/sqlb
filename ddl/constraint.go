package ddl

import (
	"errors"
	"strings"

	"github.com/ubgo/sqlb/expr"
	"github.com/ubgo/sqlb/render"
)

// Constraint is a table constraint every engine writes the same way. Only a
// named CHECK qualifies: primary keys, unique constraints and foreign keys
// differ between engines, ClickHouse has none of the three, and so by ADR-0002
// they are engine clauses in pg and sqlite rather than core constructs.
type Constraint interface {
	WriteConstraint(b *strings.Builder, a *render.Args)
}

// ErrEmptyOneOf is returned for OneOf with no values. It would render
// CHECK (col IN ()), which the empty-IN rule writes as FALSE, and a table that
// refuses every row is never what was meant.
var ErrEmptyOneOf = errors.New("sqlb/ddl: OneOf needs at least one value")

// TableConstraint is a named constraint of one table, declared once and used
// everywhere the constraint is named: to create it, validate it, rename it,
// comment on it, drop it, and target it in an upsert. A constraint's name was
// a string at each of those sites until 2026-09-22, so a typo in the one that
// drops it was an error from the server at deploy time, and naming it on the
// wrong table was the same. Declared once, the name cannot drift, and every
// statement that takes it checks it is on the statement's own table:
//
//	var UsersEmailKey = ddl.TableConstraint{Table: Users, Name: "users_email_key"}
//
// Both fields are required; a zero Name or Table fails Build with
// ErrZeroValue. A constraint that should stay unnamed, a column's inline
// REFERENCES say, is written with that construct instead.
type TableConstraint struct {
	Table expr.Table
	Name  string
}

// ClauseTable is the table the constraint belongs to, which CREATE TABLE and
// ALTER TABLE check against their own: a constraint of one table added to
// another fails with ErrForeignColumn.
func (c TableConstraint) ClauseTable() expr.Table { return c.Table }

// Valid reports whether both fields are set.
func (c TableConstraint) Valid() bool { return c.Name != "" && c.Table.Name != "" }

// Check is a table CHECK constraint. The predicate is an ordinary expression:
// its columns are written by their bare names and its values as literals,
// because inside CREATE TABLE there is no alias and no parameter.
func Check(c TableConstraint, p expr.Predicate) Constraint { return check{c: c, pred: p} }

// OneOf constrains a column to a closed set of values of its own Go type, as
// CHECK (col IN (...)). It is the portable enum: the same constants that make a
// Postgres enum type make this on SQLite, and on Postgres too when a type is
// more than the schema wants.
func OneOf[T any](c TableConstraint, col expr.Expr[T], values ...T) Constraint {
	if len(values) == 0 {
		return failed{ErrEmptyOneOf}
	}
	return check{c: c, pred: expr.In(col, values...)}
}

type check struct {
	c    TableConstraint
	pred expr.Predicate
}

// ClauseTable is the check's table, for the statement's table check.
func (c check) ClauseTable() expr.Table { return c.c.Table }

func (c check) WriteConstraint(b *strings.Builder, a *render.Args) {
	if !c.c.Valid() || c.pred == nil {
		a.Fail(ErrZeroValue)
		return
	}
	b.WriteString(kwConstraint)
	b.WriteString(sepSpace)
	b.WriteString(a.Dialect().QuoteIdent(c.c.Name))
	b.WriteString(sepSpace)
	b.WriteString(kwCheck)
	b.WriteString(sepSpace)
	b.WriteString(parOpen)
	b.WriteString(a.Bare(func() string { return expr.PredicateSQL(c.pred, a) }))
	b.WriteString(parClose)
}

// failed is a constraint that could not be made; it fails the statement at
// Build rather than at the constructor, where there is nowhere to return an
// error.
type failed struct{ err error }

func (f failed) WriteConstraint(_ *strings.Builder, a *render.Args) { a.Fail(f.err) }
