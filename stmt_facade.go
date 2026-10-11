package sqlb

import (
	"github.com/ubgo/sqlb/render"
	"github.com/ubgo/sqlb/stmt"
)

// Slot is a fixed position in a statement where an engine-specific clause may
type Slot = stmt.Slot

// Clause is a statement part that is neither an expression nor a predicate.
type Clause[D render.Dialect, K any] = stmt.Clause[D, K]

// AnyClause is a clause with no dialect or kind in its type: what a Join
// carries for a per-table hint, and what every typed clause also satisfies.
type AnyClause = stmt.AnyClause

// The statement kinds a clause is typed to.
type (
	// SelectKind marks a clause for SELECT.
	SelectKind = stmt.SelectKind
	// InsertKind marks a clause for INSERT.
	InsertKind = stmt.InsertKind
	// UpdateKind marks a clause for UPDATE.
	UpdateKind = stmt.UpdateKind
	// DeleteKind marks a clause for DELETE.
	DeleteKind = stmt.DeleteKind
)

// JoinType is the SQL join keyword. It is a distinct string type rather than a
type JoinType = stmt.JoinType

// Option extends a statement that a library built, from code that the library
type Option = stmt.Option

// Join brings a source into a statement. Table is the common case; Source
type Join = stmt.Join

// CTE is one common table expression. Columns renders the optional column
type CTE = stmt.CTE

// Select accumulates a statement. Every method appends; nothing removes. That
type Select[D render.Dialect] = stmt.Select[D]

// AnySelect is a Select whose engine is only known at runtime; it accepts
// every universal construct and no engine clause.
type AnySelect = stmt.AnySelect

// Query is a rendered statement: SQL text, the bound values in placeholder
type Query = stmt.Query

// Assignment is one column = value pair for INSERT and UPDATE. Built with
type Assignment = stmt.Assignment

// Insert accumulates an INSERT statement.
type Insert[D render.Dialect] = stmt.Insert[D]

// Update accumulates an UPDATE statement. A WHERE or an explicit All() is
type Update[D render.Dialect] = stmt.Update[D]

// Delete accumulates a DELETE statement with the same WHERE-or-All rule as Update.
type Delete[D render.Dialect] = stmt.Delete[D]

// InsertReady is an INSERT with a body, the only form that has Build; Row,
// Select and Body return it.
type InsertReady[D render.Dialect] = stmt.InsertReady[D]

// FilteredUpdate is an UPDATE whose rows are stated, the only form that has
// Build; Where and All return it.
type FilteredUpdate[D render.Dialect] = stmt.FilteredUpdate[D]

// FilteredDelete is a DELETE whose rows are stated, the only form that has
// Build; Where and All return it.
type FilteredDelete[D render.Dialect] = stmt.FilteredDelete[D]

// EngineGuard reports whether a is rendering for engine, and records the
func EngineGuard(a *Args, engine, construct string) bool {
	return stmt.EngineGuard(a, engine, construct)
}

// EngineExpr makes a guarded expression typed T from a render function. It is
func EngineExpr[T any](engine, construct string, render func(a *Args) string) Expr[T] {
	return stmt.EngineExpr[T](engine, construct, render)
}

// EnginePred makes a guarded predicate from a render function.
func EnginePred(engine, construct string, render func(a *Args) string) Predicate {
	return stmt.EnginePred(engine, construct, render)
}

// EngineRaw makes a guarded fragment that is neither an expression nor a
func EngineRaw(engine, construct string, render func(a *Args) string) SQLer {
	return stmt.EngineRaw(engine, construct, render)
}

// EngineBinOp renders "left op right" as a guarded expression typed R.
func EngineBinOp[R any](engine, construct, op string, l, r SQLer) Expr[R] {
	return stmt.EngineBinOp[R](engine, construct, op, l, r)
}

// EngineBinPred renders "left op right" as a guarded predicate.
func EngineBinPred(engine, construct, op string, l, r SQLer) Predicate {
	return stmt.EngineBinPred(engine, construct, op, l, r)
}

// EngineFn renders a guarded function call typed R.
func EngineFn[R any](engine, construct, name string, args ...SQLer) Expr[R] {
	return stmt.EngineFn[R](engine, construct, name, args...)
}

// EngineLit makes a guarded expression from a constant fragment.
func EngineLit[T any](engine, construct, sql string) Expr[T] {
	return stmt.EngineLit[T](engine, construct, sql)
}

// EngineClause makes a guarded clause for a statement slot.
func EngineClause[D render.Dialect, K any](construct string, slot Slot, render func(a *Args) string) Clause[D, K] {
	return stmt.EngineClause[D, K](construct, slot, render)
}

// EngineSource makes a guarded FROM source, for a set-returning function or
func EngineSource(engine, construct, alias string, render func(a *Args) string) Source {
	return stmt.EngineSource(engine, construct, alias, render)
}

// EngineList renders SQLers comma-separated, for a constructor assembling its
func EngineList(a *Args, items []SQLer) string { return stmt.EngineList(a, items) }

// Guard is the execution guard an engine package's statement builder owns, so
// every Query it and its derived statements build shares one.
type Guard = stmt.Guard

// NewGuard creates a guard for one builder and everything derived from it.
func NewGuard() Guard { return stmt.NewGuard() }

// NewGuardedQuery is NewQuery for a builder that owns a Guard.
func NewGuardedQuery(g Guard, sql string, args, targets []any) Query {
	return stmt.NewGuardedQuery(g, sql, args, targets)
}

// EngineQuoteLit renders s as a single-quoted SQL literal with embedded
func EngineQuoteLit(s string) SQLer { return stmt.EngineQuoteLit(s) }

// WithJoin adds joins to the statement.
func WithJoin(js ...Join) Option { return stmt.WithJoin(js...) }

// WithWhere adds predicates. They are ANDed with the library's own.
func WithWhere(ps ...Predicate) Option { return stmt.WithWhere(ps...) }

// WithColumns adds bindings after the library's own, so the library's scan
func WithColumns(bs ...Binding) Option { return stmt.WithColumns(bs...) }

// WithOrder adds order terms after the library's own.
func WithOrder(ts ...OrderTerm) Option { return stmt.WithOrder(ts...) }

// NewQuery assembles a rendered statement with its bound values and scan
func NewQuery(sql string, args, targets []any) Query { return stmt.NewQuery(sql, args, targets) }

// NewSelect starts a statement for d.
func NewSelect[D render.Dialect](d D) *Select[D] { return stmt.NewSelect(d) }

// Subquery uses a Select as a scalar expression: (SELECT ...). The author
func Subquery[T any, D render.Dialect](s *Select[D]) Expr[*T] { return stmt.Subquery[T](s) }

// Exists and NotExists. The subquery may be correlated: it references the
func Exists[D render.Dialect](s *Select[D]) Predicate { return stmt.Exists(s) }

// NotExists renders NOT EXISTS (SELECT ...), the anti-join. It is usually
func NotExists[D render.Dialect](s *Select[D]) Predicate { return stmt.NotExists(s) }

// SubqueryCompare renders "e op [quantifier] (SELECT ...)". It is the
func SubqueryCompare[D render.Dialect](e SQLer, op, quantifier string, s *Select[D]) Predicate {
	return stmt.SubqueryCompare(e, op, quantifier, s)
}

// InSubquery renders e IN (SELECT ...). The subquery must select one item
func InSubquery[T any, D render.Dialect](e Expr[T], s *Select[D]) Predicate {
	return stmt.InSubquery[T](e, s)
}

// NotInSubquery renders e NOT IN (SELECT ...). Beware the NULL rule: if the
func NotInSubquery[T any, D render.Dialect](e Expr[T], s *Select[D]) Predicate {
	return stmt.NotInSubquery[T](e, s)
}

// AssignTarget makes an assignment whose target is an expression over col,
func AssignTarget(col string, target func(a *Args) string, value SQLer) Assignment {
	return stmt.AssignTarget(col, target, value)
}

// Set assigns a bound value to a column of the same Go type.
func Set[T any](col Expr[T], v T) Assignment { return stmt.Set[T](col, v) }

// SetExpr assigns an expression of the column's type: SET n = n + 1.
func SetExpr[T any](col Expr[T], e Expr[T]) Assignment { return stmt.SetExpr[T](col, e) }

// SetDefault assigns DEFAULT.
func SetDefault[T any](col Expr[T]) Assignment { return stmt.SetDefault[T](col) }

// NewInsert starts an INSERT for d.
func NewInsert[D render.Dialect](d D) *Insert[D] { return stmt.NewInsert(d) }

// ReturningDialect is a dialect that has RETURNING; see stmt.ReturningDialect.
type ReturningDialect = stmt.ReturningDialect

// InsertReturning adds RETURNING to an INSERT on a dialect that has it.
func InsertReturning[D ReturningDialect](s *InsertReady[D], bs ...Binding) *InsertReady[D] {
	return stmt.InsertReturning(s, bs...)
}

// UpdateReturning adds RETURNING to an UPDATE on a dialect that has it.
func UpdateReturning[D ReturningDialect](s *FilteredUpdate[D], bs ...Binding) *FilteredUpdate[D] {
	return stmt.UpdateReturning(s, bs...)
}

// DeleteReturning adds RETURNING to a DELETE on a dialect that has it.
func DeleteReturning[D ReturningDialect](s *FilteredDelete[D], bs ...Binding) *FilteredDelete[D] {
	return stmt.DeleteReturning(s, bs...)
}

// NewUpdate starts an UPDATE for d.
func NewUpdate[D render.Dialect](d D) *Update[D] { return stmt.NewUpdate(d) }

// NewDelete starts a DELETE for d.
func NewDelete[D render.Dialect](d D) *Delete[D] { return stmt.NewDelete(d) }

// Constants re-exported from the stmt layer. Go cannot alias a constant,
// so each is a new constant with the same value.
const (
	JoinCross        = stmt.JoinCross
	JoinInner        = stmt.JoinInner
	JoinLeft         = stmt.JoinLeft
	JoinRight        = stmt.JoinRight
	SlotAfterColumns = stmt.SlotAfterColumns
	SlotAfterFrom    = stmt.SlotAfterFrom
	SlotAfterGroupBy = stmt.SlotAfterGroupBy
	SlotAfterHaving  = stmt.SlotAfterHaving
	SlotAfterInsert  = stmt.SlotAfterInsert
	SlotAfterOffset  = stmt.SlotAfterOffset
	SlotAfterOrder   = stmt.SlotAfterOrder
	SlotAfterSelect  = stmt.SlotAfterSelect
	SlotAfterSet     = stmt.SlotAfterSet
	SlotAfterTarget  = stmt.SlotAfterTarget
	SlotAfterVerb    = stmt.SlotAfterVerb
	SlotAfterWhere   = stmt.SlotAfterWhere
	SlotBeforeOrder  = stmt.SlotBeforeOrder
	SlotBeforeValues = stmt.SlotBeforeValues
	SlotBeforeWhere  = stmt.SlotBeforeWhere
	SlotInsertBody   = stmt.SlotInsertBody
	SlotTail         = stmt.SlotTail
)

// Variables re-exported from the stmt layer.
var (
	ErrDuplicateAlias  = stmt.ErrDuplicateAlias
	ErrJoinWithoutFrom = stmt.ErrJoinWithoutFrom
	ErrNoAssignments   = stmt.ErrNoAssignments
	ErrNoColumns       = stmt.ErrNoColumns
	ErrNoReturning     = stmt.ErrNoReturning
	ErrNoTargets       = stmt.ErrNoTargets
	ErrNoWhere         = stmt.ErrNoWhere
	ErrNotAColumn      = stmt.ErrNotAColumn
	ErrQueryInUse      = stmt.ErrQueryInUse
	ErrRowWidth        = stmt.ErrRowWidth
	ErrSetOpArity      = stmt.ErrSetOpArity
	ErrUnknownSlot     = stmt.ErrUnknownSlot
	JoinTypeValues     = stmt.JoinTypeValues
	SlotValues         = stmt.SlotValues
)
