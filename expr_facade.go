package sqlb

import (
	"github.com/ubgo/sqlb/expr"
	"github.com/ubgo/sqlb/render"
)

// AggSpec is what an aggregate call can carry beyond its arguments. The
type AggSpec = expr.AggSpec

// AggOption modifies an aggregate call.
type AggOption = expr.AggOption

// SortDirection is the ORDER BY direction.
type SortDirection = expr.SortDirection

// NullsOrder is where NULLs sort relative to values; see expr.NullsOrder.
type NullsOrder = expr.NullsOrder

// Table is a physical table with a fixed alias.
type Table = expr.Table

// Expr is a SQL expression that evaluates to the Go type T.
type Expr[T any] = expr.Expr[T]

// Number is the set of Go types that arithmetic operators accept.
type Number = expr.Number

// CaseBuilder accumulates a searched CASE. Else yields Expr[T]; End yields
type CaseBuilder[T any] = expr.CaseBuilder[T]

// OrderTerm is one ORDER BY item. Nulls and Using are hooks the core
type OrderTerm = expr.OrderTerm

// Predicate is a boolean condition for WHERE or ON.
type Predicate = expr.Predicate

// Source is anything that can stand in FROM or be joined: a table, a derived
type Source = expr.Source

// Statement is any statement that renders into a parent's Args: a Select used
type Statement = expr.Statement

// FrameMode selects ROWS or RANGE for a window frame. GROUPS is Postgres
type FrameMode = expr.FrameMode

// FrameExclude is the EXCLUDE part of a frame. The constants are in pg,
type FrameExclude = expr.FrameExclude

// FrameBound is one end of a frame. Use the constructors below.
type FrameBound = expr.FrameBound

// Window is a window specification: PARTITION BY, ORDER BY and a frame. It
type Window = expr.Window

// WinFn is a window function or an aggregate that has not yet been given its
type WinFn[T any] = expr.WinFn[T]

// Distinct adds DISTINCT inside the aggregate: count(DISTINCT x).
func Distinct() AggOption { return expr.Distinct() }

// Aggregate renders name(args...) with the options applied, typed R. It is
func Aggregate[R any](name string, args []render.SQLer, opts ...AggOption) Expr[R] {
	return expr.Aggregate[R](name, args, opts...)
}

// Count renders count(*), int64 and never null.
func Count(opts ...AggOption) Expr[int64] { return expr.Count(opts...) }

// CountOf renders count(e).
func CountOf[T any](e Expr[T], opts ...AggOption) Expr[int64] { return expr.CountOf[T](e, opts...) }

// Sum keeps the operand type. SQL widens sum(int4) to int8; database/sql
func Sum[T Number](e Expr[T], opts ...AggOption) Expr[T] { return expr.Sum[T](e, opts...) }

// Avg is float64. SQL returns numeric for integer input; scanning numeric
func Avg[T Number](e Expr[T], opts ...AggOption) Expr[float64] { return expr.Avg[T](e, opts...) }

// Min renders min(e). Typed as T rather than *T: a group that exists has at
func Min[T any](e Expr[T], opts ...AggOption) Expr[T] { return expr.Min[T](e, opts...) }

// Max renders max(e).
func Max[T any](e Expr[T], opts ...AggOption) Expr[T] { return expr.Max[T](e, opts...) }

// New makes an expression from a render function. It is the single typed
func New[T any](render func(a *render.Args) string) Expr[T] { return expr.New[T](render) }

// Col declares a column of table t with Go type T. It renders "alias.name".
func Col[T any](t Table, name string) Expr[T] { return expr.Col[T](t, name) }

// Param binds a Go value as an expression of its own type.
func Param[T any](v T) Expr[T] { return expr.Param[T](v) }

// Lower wraps a string expression in lower(). Used on both sides of an
func Lower(e Expr[string]) Expr[string] { return expr.Lower(e) }

// Upper wraps a string expression in upper(), universal as lower() is.
func Upper(e Expr[string]) Expr[string] { return expr.Upper(e) }

// Literal is a constant SQL fragment typed as T.
func Literal[T any](sql string) Expr[T] { return expr.Literal[T](sql) }

// Nullable reinterprets an expression of T as *T without changing the SQL.
func Nullable[T any](e Expr[T]) Expr[*T] { return expr.Nullable[T](e) }

// AssertNotNull reinterprets a nullable expression as T without changing the
func AssertNotNull[T any](e Expr[*T]) Expr[T] { return expr.AssertNotNull[T](e) }

// Binary renders "(left op right)" typed R. Engine packages use it for their
func Binary[R any](left render.SQLer, op string, right render.SQLer) Expr[R] {
	return expr.Binary[R](left, op, right)
}

// Add renders l + r.
func Add[T Number](l, r Expr[T]) Expr[T] { return expr.Add[T](l, r) }

// Sub renders l - r.
func Sub[T Number](l, r Expr[T]) Expr[T] { return expr.Sub[T](l, r) }

// Mul renders l * r.
func Mul[T Number](l, r Expr[T]) Expr[T] { return expr.Mul[T](l, r) }

// Div renders l / r, which truncates on integer operands. Cast one side for
func Div[T Number](l, r Expr[T]) Expr[T] { return expr.Div[T](l, r) }

// Mod renders l % r.
func Mod[T Number](l, r Expr[T]) Expr[T] { return expr.Mod[T](l, r) }

// Neg renders (-e), parenthesised so it composes without a precedence
func Neg[T Number](e Expr[T]) Expr[T] { return expr.Neg[T](e) }

// Concat renders concat(a, b, ...), the function form, because the ||
func Concat(parts ...Expr[string]) Expr[string] { return expr.Concat(parts...) }

// As aliases a select item: expr AS name. Needed for a CTE or derived table
func As[T any](e Expr[T], name string) Expr[T] { return expr.As[T](e, name) }

// OutputCol references a select-list alias by name, typed T. It is how an
func OutputCol[T any](name string) Expr[T] { return expr.OutputCol[T](name) }

// Cast renders CAST(e AS sqlType) and re-types the expression as R. The SQL
func Cast[R, T any](e Expr[T], sqlType string) Expr[R] { return expr.Cast[R, T](e, sqlType) }

// Fn renders a function call name(args...) typed R. It is the typed door for
func Fn[R any](name string, args ...render.SQLer) Expr[R] { return expr.Fn[R](name, args...) }

// True renders TRUE. It is the ON clause of a LATERAL join, which has no
func True() Predicate { return expr.True() }

// False renders FALSE, which fails a query closed. It is what an empty IN
func False() Predicate { return expr.False() }

// Between renders e BETWEEN lo AND hi with bound ends.
func Between[T any](e Expr[T], lo, hi T) Predicate { return expr.Between[T](e, lo, hi) }

// NotBetween is the negated form.
func NotBetween[T any](e Expr[T], lo, hi T) Predicate { return expr.NotBetween[T](e, lo, hi) }

// Like and NotLike. The pattern is bound, never in the SQL text.
func Like(e Expr[string], pattern string) Predicate { return expr.Like(e, pattern) }

// NotLike renders e NOT LIKE $n, the negation of Like.
func NotLike(e Expr[string], pattern string) Predicate { return expr.NotLike(e, pattern) }

// PredicateOf makes a Predicate from a render function. Engine packages use
func PredicateOf(render func(a *render.Args) string) Predicate { return expr.PredicateOf(render) }

// Compare renders "left op right" as a predicate. Engine packages use it for
func Compare(left render.SQLer, op string, right render.SQLer) Predicate {
	return expr.Compare(left, op, right)
}

// BoolExpr turns a predicate into a boolean expression, so it can be
func BoolExpr(p Predicate) Expr[bool] { return expr.BoolExpr(p) }

// Coalesce renders COALESCE(first, rest...). The result type is T; the
func Coalesce[T any](first Expr[T], rest ...Expr[T]) Expr[T] { return expr.Coalesce[T](first, rest...) }

// NullIf renders NULLIF(a, b). It is always nullable, so the result is *T.
func NullIf[T any](l, r Expr[T]) Expr[*T] { return expr.NullIf[T](l, r) }

// Row renders a row constructor (a, b, c), for row comparisons and keyset
func Row(items ...render.SQLer) render.SQLer { return expr.Row(items...) }

// RowLess renders l < r for two row constructors, the shape a keyset page
func RowLess(l, r render.SQLer) Predicate { return expr.RowLess(l, r) }

// RowGreater renders l > r for two row constructors.
func RowGreater(l, r render.SQLer) Predicate { return expr.RowGreater(l, r) }

// RowEq renders l = r for two row constructors.
func RowEq(l, r render.SQLer) Predicate { return expr.RowEq(l, r) }

// RowIn renders (a, b) IN ((?, ?), ...), the composite-key membership test.
// An empty list renders FALSE.
func RowIn(row render.SQLer, rows ...render.SQLer) Predicate { return expr.RowIn(row, rows...) }

// RowNotIn renders (a, b) NOT IN ((?, ?), ...). An empty list renders TRUE,
// and a NULL anywhere in the list makes the whole predicate NULL.
func RowNotIn(row render.SQLer, rows ...render.SQLer) Predicate { return expr.RowNotIn(row, rows...) }

// Case starts a searched CASE expression.
func Case[T any]() CaseBuilder[T] { return expr.Case[T]() }

// OrderBy makes an order term from any typed expression.
func OrderBy[T any](e Expr[T], dir SortDirection) OrderTerm { return expr.OrderBy[T](e, dir) }

// Eq and its family compare an expression with a bound value of the same Go
func Eq[T any](e Expr[T], v T) Predicate { return expr.Eq[T](e, v) }

// NotEq renders e <> $n. NULL <> v is NULL, not true, so this does not match
func NotEq[T any](e Expr[T], v T) Predicate { return expr.NotEq[T](e, v) }

// Less renders e < $n.
func Less[T any](e Expr[T], v T) Predicate { return expr.Less[T](e, v) }

// LessEq renders e <= $n.
func LessEq[T any](e Expr[T], v T) Predicate { return expr.LessEq[T](e, v) }

// Greater renders e > $n.
func Greater[T any](e Expr[T], v T) Predicate { return expr.Greater[T](e, v) }

// GreaterEq renders e >= $n.
func GreaterEq[T any](e Expr[T], v T) Predicate { return expr.GreaterEq[T](e, v) }

// EqExpr compares two expressions of the same Go type. It is the join
func EqExpr[T any](left, right Expr[T]) Predicate { return expr.EqExpr[T](left, right) }

// NotEqExpr renders left <> right.
func NotEqExpr[T any](left, right Expr[T]) Predicate { return expr.NotEqExpr[T](left, right) }

// LessExpr renders left < right.
func LessExpr[T any](left, right Expr[T]) Predicate { return expr.LessExpr[T](left, right) }

// LessEqExpr renders left <= right.
func LessEqExpr[T any](left, right Expr[T]) Predicate { return expr.LessEqExpr[T](left, right) }

// GreaterExpr renders left > right.
func GreaterExpr[T any](left, right Expr[T]) Predicate { return expr.GreaterExpr[T](left, right) }

// GreaterEqExpr renders left >= right.
func GreaterEqExpr[T any](left, right Expr[T]) Predicate { return expr.GreaterEqExpr[T](left, right) }

// IsNull and IsNotNull test for NULL. Typed on T only so they accept any Expr.
func IsNull[T any](e Expr[T]) Predicate { return expr.IsNull[T](e) }

// IsNotNull renders e IS NOT NULL.
func IsNotNull[T any](e Expr[T]) Predicate { return expr.IsNotNull[T](e) }

// In binds every value. An empty list renders FALSE.
func In[T any](e Expr[T], values ...T) Predicate { return expr.In[T](e, values...) }

// NotIn binds every value. An empty list renders TRUE.
func NotIn[T any](e Expr[T], values ...T) Predicate { return expr.NotIn[T](e, values...) }

// And groups predicates with AND. Empty renders TRUE.
func And(preds ...Predicate) Predicate { return expr.And(preds...) }

// Or groups predicates with OR. Empty renders TRUE, which is the safe value
func Or(preds ...Predicate) Predicate { return expr.Or(preds...) }

// Not negates a predicate, always parenthesised.
func Not(p Predicate) Predicate { return expr.Not(p) }

// PredicateSQL renders p to a string with the statement's Args. Engine
func PredicateSQL(p Predicate, a *render.Args) string { return expr.PredicateSQL(p, a) }

// InExprs renders e IN (x, y, ...) over expressions, for a membership test
func InExprs[T any](e Expr[T], items ...Expr[T]) Predicate { return expr.InExprs[T](e, items...) }

// Ref returns a Table value that references a source by its alias only, for
func Ref(src Source) Table { return expr.Ref(src) }

// Derived makes a subquery a FROM source: "(SELECT ...) AS alias".
func Derived(body Statement, alias string) Source { return expr.Derived(body, alias) }

// SourceOf makes a Source from a render function and an alias. Engine
func SourceOf(alias string, render func(a *render.Args) string) Source {
	return expr.SourceOf(alias, render)
}

// QuoteList renders identifiers comma-separated and quoted, for column lists.
func QuoteList(d render.Dialect, names []string) string { return expr.QuoteList(d, names) }

// UnboundedPreceding starts a frame at the first row of the partition.
func UnboundedPreceding() FrameBound { return expr.UnboundedPreceding() }

// UnboundedFollowing ends a frame at the last row of the partition.
func UnboundedFollowing() FrameBound { return expr.UnboundedFollowing() }

// CurrentRow bounds a frame at the current row. Under RANGE that means the
func CurrentRow() FrameBound { return expr.CurrentRow() }

// Preceding bounds a frame n rows back under ROWS, or n units back under
func Preceding(n int) FrameBound { return expr.Preceding(n) }

// Following bounds a frame n rows forward.
func Following(n int) FrameBound { return expr.Following(n) }

// PrecedingExpr and FollowingExpr take a constant SQL expression as the
func PrecedingExpr(sql string) FrameBound { return expr.PrecedingExpr(sql) }

// FollowingExpr bounds a frame with an expression rather than a count, for a
func FollowingExpr(sql string) FrameBound { return expr.FollowingExpr(sql) }

// NewWindow starts an empty window specification.
func NewWindow() Window { return expr.NewWindow() }

// NewWinFn makes a window function from a render function. Engine packages
func NewWinFn[T any](render func(a *render.Args) string) WinFn[T] { return expr.NewWinFn[T](render) }

// Over turns any expression, in practice an aggregate, into a window
func Over[T any](e Expr[T], w Window) Expr[T] { return expr.Over[T](e, w) }

// OverNamed is Over with a named window.
func OverNamed[T any](e Expr[T], name string) Expr[T] { return expr.OverNamed[T](e, name) }

// RowNumber renders row_number(), a distinct number per row.
func RowNumber() WinFn[int64] { return expr.RowNumber() }

// Rank renders rank(), which leaves gaps after a tie.
func Rank() WinFn[int64] { return expr.Rank() }

// DenseRank renders dense_rank(), which does not leave gaps.
func DenseRank() WinFn[int64] { return expr.DenseRank() }

// FirstValue renders first_value(e).
func FirstValue[T any](e Expr[T]) WinFn[*T] { return expr.FirstValue[T](e) }

// LastValue renders last_value(e). With the default frame it returns the
func LastValue[T any](e Expr[T]) WinFn[*T] { return expr.LastValue[T](e) }

// NthValue renders nth_value(e, n), 1-based within the frame.
func NthValue[T any](e Expr[T], n int) WinFn[*T] { return expr.NthValue[T](e, n) }

// Constants re-exported from the expr layer. Go cannot alias a constant,
// so each is a new constant with the same value.
const (
	FrameRange          = expr.FrameRange
	FrameRows           = expr.FrameRows
	LogicAnd            = expr.LogicAnd
	LogicNot            = expr.LogicNot
	LogicOr             = expr.LogicOr
	OpAdd               = expr.OpAdd
	OpConcat            = expr.OpConcat
	OpDiv               = expr.OpDiv
	OpEq                = expr.OpEq
	OpGreater           = expr.OpGreater
	OpGreaterEq         = expr.OpGreaterEq
	OpIn                = expr.OpIn
	OpIsDistinctFrom    = expr.OpIsDistinctFrom
	OpIsFalse           = expr.OpIsFalse
	OpIsNotDistinctFrom = expr.OpIsNotDistinctFrom
	OpIsNotNull         = expr.OpIsNotNull
	OpIsNull            = expr.OpIsNull
	OpIsTrue            = expr.OpIsTrue
	OpIsUnknown         = expr.OpIsUnknown
	OpLess              = expr.OpLess
	OpLessEq            = expr.OpLessEq
	OpLike              = expr.OpLike
	OpMod               = expr.OpMod
	OpMul               = expr.OpMul
	OpNotEq             = expr.OpNotEq
	OpNotIn             = expr.OpNotIn
	OpNotLike           = expr.OpNotLike
	OpSub               = expr.OpSub
	SortAsc             = expr.SortAsc
	SortDesc            = expr.SortDesc
	NullsFirst          = expr.NullsFirst
	NullsLast           = expr.NullsLast
)

// Variables re-exported from the expr layer.
var (
	OpValues            = expr.OpValues
	SortDirectionValues = expr.SortDirectionValues
	NullsOrderValues    = expr.NullsOrderValues
	FrameModeValues     = expr.FrameModeValues
	ErrUnknownValue     = expr.ErrUnknownValue
)

// EscapeLike escapes the characters LIKE treats as syntax, so a value can be
// used inside a pattern the caller builds.
func EscapeLike(s string) string { return expr.EscapeLike(s) }

// Contains renders e LIKE '%value%' with value escaped, the predicate behind a
// search box.
func Contains(e Expr[string], value string) Predicate { return expr.Contains(e, value) }

// EqFold matches e against value regardless of case, lower(e) = lower(value)
// on every engine. See expr.EqFold for the index and collation notes.
func EqFold(e Expr[string], value string) Predicate { return expr.EqFold(e, value) }

// InFold matches e against any of values regardless of case; an empty list is
// FALSE.
func InFold(e Expr[string], values ...string) Predicate { return expr.InFold(e, values...) }

// NotInFold matches e against none of values regardless of case; an empty
// list is TRUE.
func NotInFold(e Expr[string], values ...string) Predicate { return expr.NotInFold(e, values...) }

// ContainsFold is Contains regardless of case: ILIKE on a dialect that has it,
// lower() LIKE lower() elsewhere.
func ContainsFold(e Expr[string], value string) Predicate { return expr.ContainsFold(e, value) }

// StartsWithFold is StartsWith regardless of case.
func StartsWithFold(e Expr[string], value string) Predicate { return expr.StartsWithFold(e, value) }

// EndsWithFold is EndsWith regardless of case.
func EndsWithFold(e Expr[string], value string) Predicate { return expr.EndsWithFold(e, value) }

// NotContains renders e NOT LIKE '%value%' with value escaped.
func NotContains(e Expr[string], value string) Predicate { return expr.NotContains(e, value) }

// StartsWith renders e LIKE 'value%' with value escaped. Prefer it to Contains
// where it fits: a prefix match can use an index and a leading wildcard cannot.
func StartsWith(e Expr[string], value string) Predicate { return expr.StartsWith(e, value) }

// EndsWith renders e LIKE '%value' with value escaped.
func EndsWith(e Expr[string], value string) Predicate { return expr.EndsWith(e, value) }

// ErrNoLikeEscape is recorded when a dialect cannot express an ESCAPE clause,
// so a user's text cannot be escaped safely.
var ErrNoLikeEscape = expr.ErrNoLikeEscape

// ErrZeroExpr is recorded when a zero Expr is rendered. See expr.ErrZeroExpr.
var ErrZeroExpr = expr.ErrZeroExpr

// CheckedOperator writes an operator, or fails the render naming it.
//
// An operator cannot be quoted, so it reaches the statement text as the author
// wrote it; the shape check is what keeps that from being a hole. See
// expr.CheckedOperator.
func CheckedOperator(a *render.Args, op, what string) string {
	return expr.CheckedOperator(a, op, what)
}

// CheckedKeyword writes a keyword, or fails the render naming it. It is what
// makes JoinType and SetOp open in their set while checked in their text. See
// expr.CheckedKeyword.
func CheckedKeyword(a *render.Args, s, what string) string { return expr.CheckedKeyword(a, s, what) }

// CheckedFunctionName writes a function name, or fails the render naming it.
// A name may be schema-qualified, which is why it is not one identifier. See
// expr.CheckedFunctionName.
func CheckedFunctionName(a *render.Args, name, what string) string {
	return expr.CheckedFunctionName(a, name, what)
}
