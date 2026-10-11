// The names this layer borrows from below.
//
// Rather than qualify every use of Args, Table or Predicate across fourteen
// hundred lines, the statement layer re-declares the handful of names it builds
// on. They are aliases, so they are the same types, not copies: a Predicate
// written against expr satisfies a Where here with no conversion.
//
// This is not merely a convenience. A statement is assembled out of expressions
// and rendered through a dialect, so these names are genuinely part of the
// vocabulary a caller needs to use this package, and re-exporting them is what
// lets stmt be imported on its own without a second import for every argument
// type. The list is deliberately short: if it grows, the layer boundary is
// drifting and the right response is to look at why, not to add another line.

package stmt

import (
	"github.com/ubgo/sqlb/expr"
	"github.com/ubgo/sqlb/render"
)

// From the render layer.
type (
	// Args collects bound values during one render and carries the first error.
	Args = render.Args
	// Dialect is how an engine writes a placeholder and quotes an identifier.
	Dialect = render.Dialect
	// SQLer is anything that renders itself with the statement's Args.
	SQLer = render.SQLer
	// Named is satisfied by dialects that can name their engine.
	Named = render.Named
	// Returning is satisfied by dialects that support RETURNING.
	Returning = render.Returning
	// InsertAliasing is satisfied by dialects accepting INSERT INTO t AS alias.
	InsertAliasing = render.InsertAliasing
)

// From the expression layer.
type (
	// Table is a physical table with a fixed alias.
	Table = expr.Table
	// Expr is a SQL expression evaluating to the Go type T.
	Expr[T any] = expr.Expr[T]
	// Predicate is a boolean condition for WHERE or ON.
	Predicate = expr.Predicate
	// Source is anything that can appear in FROM or JOIN.
	Source = expr.Source
	// Statement is anything that renders as a complete statement.
	Statement = expr.Statement
	// OrderTerm is one ORDER BY item.
	OrderTerm = expr.OrderTerm
	// SortDirection is the ORDER BY direction.
	SortDirection = expr.SortDirection
	// Window is a window definition for a window function.
	Window = expr.Window
)

// ErrUnsupportedDialect is returned by Build when a construct was rendered
// through a dialect that does not support it.
var ErrUnsupportedDialect = render.ErrUnsupportedDialect

// The operator and sort vocabulary this layer emits. Constants cannot be
// aliased, so each is a new constant with the same value.
const (
	OpEq     = expr.OpEq
	OpIn     = expr.OpIn
	OpNotIn  = expr.OpNotIn
	LogicAnd = expr.LogicAnd
	SortAsc  = expr.SortAsc
	SortDesc = expr.SortDesc
)

// Param binds a Go value as an expression of its own type.
func Param[T any](v T) Expr[T] { return expr.Param[T](v) }

// DialectName reports the engine name of d, or DialectUnknown.
func DialectName(d Dialect) string { return render.DialectName(d) }

// New makes a typed expression from a render function.
func New[T any](f func(a *Args) string) Expr[T] { return expr.New[T](f) }

// PredicateOf makes a Predicate from a render function.
func PredicateOf(f func(a *Args) string) Predicate { return expr.PredicateOf(f) }

// SourceOf makes a Source from a render function and an alias.
func SourceOf(alias string, f func(a *Args) string) Source { return expr.SourceOf(alias, f) }

// QuoteList renders identifiers comma-separated and quoted for the dialect.
func QuoteList(d Dialect, names []string) string { return expr.QuoteList(d, names) }

// NewArgs starts an empty argument list for d.
func NewArgs(d Dialect) *Args { return render.NewArgs(d) }

// Raw makes a SQLer from a render function.
func Raw(f func(a *Args) string) SQLer { return render.Raw(f) }

// And combines predicates with AND.
func And(ps ...Predicate) Predicate { return expr.And(ps...) }
