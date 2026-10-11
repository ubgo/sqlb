// Package expr is the typed half of the builder: a table, a column with a Go
// type, a bound parameter, a predicate, an aggregate, a window, and the
// operators that join them.
//
// Everything here renders through the layer below and knows nothing about
// statements. An Expr does not know it will appear in a SELECT, and a Predicate
// does not know whether it is a WHERE or a JOIN condition, which is what lets
// the statement layer put them wherever the grammar allows.
//
// The one type that cannot be implemented from outside is Expr itself. It is a
// concrete generic struct rather than an interface, because being impossible to
// satisfy is exactly what makes its type parameter a guarantee rather than a
// suggestion. A package that needs an untyped fragment uses render.Raw and says
// so in its signature.
package expr

import (
	"errors"

	"github.com/ubgo/sqlb/render"
)

// Table is a physical table with a fixed alias.
//
// The alias lives here, not on the statement, because a plugin in another
// module declares its join at package level before any statement exists.
// Build rejects two tables that share one alias in a statement, because the
// collision otherwise surfaces as an ambiguous-column error from the database
// pointing at a column neither author wrote.
type Table struct {
	Name  string
	Alias string
	// Schema is optional. When set, the table renders as "schema"."name".
	Schema string
}

// SQL renders "name AS alias", both quoted for the dialect, with the schema
// prefix when set.
//
// An empty Alias renders no AS clause at all, because the alternative is
// worse than it looks: a quoted empty identifier. Postgres rejects
// "zero-length delimited identifier" outright, and the shape that produces
// one is ordinary — a CTE is referenced by its name and has no alias to give,
// so Table{Name: "regional_sales"} is the natural thing to write.
func (t Table) SQL(d render.Dialect) string {
	if t.Alias == "" {
		return t.qualified(d)
	}
	return t.qualified(d) + sepSpace + kwAs + sepSpace + d.QuoteIdent(t.Alias)
}

// Qualifier is the identifier a column of this table qualifies with: the
// alias when there is one, otherwise the table's own name.
//
// Schema is deliberately not part of it. A column is qualified by how the
// table is named in the FROM clause, so a table declared as "public"."users"
// is still referenced as "users"."id".
func (t Table) Qualifier(d render.Dialect) string {
	if t.Alias != "" {
		return d.QuoteIdent(t.Alias)
	}
	return d.QuoteIdent(t.Name)
}

// Qualified renders the table's physical name, with its schema prefix when
// set. Exported because the statement layer builds INSERT and UPDATE targets
// from it, and a layer boundary that forces a duplicate of this rule would be
// worse than exporting it.
//
// This is the name of the thing, not the name a column qualifies with; those
// differ the moment a table has an alias, and Qualifier is the other one. The
// comment here used to claim it returned the alias when one was set, which it
// has never done — a write target must name the table, not its alias.
func (t Table) Qualified(d render.Dialect) string { return t.qualified(d) }

func (t Table) qualified(d render.Dialect) string {
	if t.Schema != "" {
		return d.QuoteIdent(t.Schema) + "." + d.QuoteIdent(t.Name)
	}
	return d.QuoteIdent(t.Name)
}

// Expr is a SQL expression that evaluates to the Go type T.
//
// It is a concrete generic struct, not an interface, because an interface whose
// method set never mentions T makes T a phantom parameter that Go cannot infer;
// every call site would then need an explicit type argument. The struct keeps
// inference working.
//
// The render function receives *Args rather than only the Dialect so an
// expression can bind a parameter (see Param) and can record that it is not
// supported by the dialect it is being rendered through (see Args.Unsupported).
type Expr[T any] struct {
	render func(a *render.Args) string

	// table and name are set only for a column made by Col. They exist so a
	// tool can list the columns a statement touches; they are not used by
	// rendering.
	table Table
	name  string
}

// New makes an expression from a render function. It is the single typed
// escape hatch: the caller states the Go type the expression evaluates to, and
// the compiler holds every downstream use to that statement.
func New[T any](render func(a *render.Args) string) Expr[T] {
	return Expr[T]{render: render}
}

// SQL renders the expression for the dialect carried by a.
//
// The zero Expr[T]{} has no render function; it records ErrZeroExpr and
// renders nothing rather than panicking, since Expr is a struct and a caller
// can write the literal. Build then fails with the error.
func (e Expr[T]) SQL(a *render.Args) string {
	if e.render == nil {
		a.Fail(ErrZeroExpr)
		return ""
	}
	return e.render(a)
}

// ErrZeroExpr is recorded when a zero Expr[T]{}, one no constructor made, is
// rendered. Until 2026-09-22 rendering one called a nil function and panicked
// inside Build.
var ErrZeroExpr = errors.New("sqlb: a zero Expr was rendered; make expressions with Col, Param or a constructor")

// Name returns the column name for an expression made by Col, else "".
func (e Expr[T]) Name() string { return e.name }

// Table returns the table for an expression made by Col, else the zero Table.
func (e Expr[T]) Table() Table { return e.table }

// Col declares a column of table t with Go type T. It renders "alias.name".
func Col[T any](t Table, name string) Expr[T] {
	return Expr[T]{
		render: func(a *render.Args) string {
			d := a.Dialect()
			switch {
			case a.Unqualified():
				return d.QuoteIdent(name)
			case a.ByTableName():
				return t.Qualified(d) + "." + d.QuoteIdent(name)
			}
			return t.Qualifier(d) + "." + d.QuoteIdent(name)
		},
		table: t,
		name:  name,
	}
}

// Param binds a Go value as an expression of its own type.
//
// With Param, Eq(col, v) is the same as EqExpr(col, Param(v)), and every
// function that accepts an Expr[T] also accepts a bound value. That is what a
// JSON path with a bound key, a vector distance with a bound vector, or any
// SQL function with a bound argument needs.
func Param[T any](v T) Expr[T] {
	return New[T](func(a *render.Args) string { return a.Next(v) })
}

// Lower wraps a string expression in lower(). Used on both sides of an
// identifier comparison so the comparison matches a lower() uniqueness index.
func Lower(e Expr[string]) Expr[string] {
	return New[string](func(a *render.Args) string { return lowerSQL(e.SQL(a)) })
}

// Upper wraps a string expression in upper(). It is core for the reason
// Lower is: Postgres, MySQL 8, SQLite and ClickHouse all spell it upper() and
// mean the same thing by it for ASCII, which is the case a CHECK or a
// generated column relies on.
func Upper(e Expr[string]) Expr[string] {
	return New[string](func(a *render.Args) string { return "upper(" + e.SQL(a) + ")" })
}

// Literal is a constant SQL fragment typed as T.
//
// It is the documented exception to "no raw fragments". It exists for
// constants such as ” or FALSE as COALESCE fallbacks and must never be fed
// request data; use Param for anything that varies.
func Literal[T any](sql string) Expr[T] {
	return New[T](func(*render.Args) string { return sql })
}

// Nullable reinterprets an expression of T as *T without changing the SQL.
//
// The same physical column is non-null under an INNER join and nullable under
// a LEFT join. Discovered when an integration test failed with "couldn't
// convert <nil> into type time.Time", a failure no test over SQL text catches.
func Nullable[T any](e Expr[T]) Expr[*T] {
	return Expr[*T](e)
}

// AssertNotNull reinterprets a nullable expression as T without changing the
// SQL. It is the opposite of Nullable and it is a promise: the author knows
// a run-time fact the builder cannot prove, such as "lag() is null only on
// the first row, where this branch is never taken". Scanning a NULL through
// it fails at run time, so write the reason beside every use.
func AssertNotNull[T any](e Expr[*T]) Expr[T] {
	return Expr[T](e)
}
