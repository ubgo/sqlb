package expr

import (
	"strings"

	"github.com/ubgo/sqlb/render"
)

// Predicate is a boolean condition for WHERE or ON.
//
// It is an open interface so a consumer can add an operator the package does
// not ship. It has no type parameter, so an interface costs nothing here,
// unlike Expr. Constructors below are the only way to build a typed
// comparison; a raw fragment cannot be smuggled in as one.
type Predicate interface {
	WriteSQL(b *strings.Builder, args *render.Args)
}

// compareExpr renders "left op right". Every value comparison is this with
// Param on the right, so there is one render path for both forms.
type compareExpr struct {
	left  render.SQLer
	op    string
	right render.SQLer
}

func (c compareExpr) WriteSQL(b *strings.Builder, args *render.Args) {
	b.WriteString(c.left.SQL(args))
	b.WriteString(sepSpace)
	b.WriteString(CheckedOperator(args, c.op, "operator"))
	b.WriteString(sepSpace)
	b.WriteString(c.right.SQL(args))
}

// Eq and its family compare an expression with a bound value of the same Go
// type. Comparing a time column with a string does not compile.
func Eq[T any](e Expr[T], v T) Predicate { return compareExpr{e, OpEq, Param(v)} }

// NotEq renders e <> $n. NULL <> v is NULL, not true, so this does not match
// rows where the column is NULL; add IsNull when it should.
func NotEq[T any](e Expr[T], v T) Predicate { return compareExpr{e, OpNotEq, Param(v)} }

// Less renders e < $n.
func Less[T any](e Expr[T], v T) Predicate { return compareExpr{e, OpLess, Param(v)} }

// LessEq renders e <= $n.
func LessEq[T any](e Expr[T], v T) Predicate { return compareExpr{e, OpLessEq, Param(v)} }

// Greater renders e > $n.
func Greater[T any](e Expr[T], v T) Predicate { return compareExpr{e, OpGreater, Param(v)} }

// GreaterEq renders e >= $n.
func GreaterEq[T any](e Expr[T], v T) Predicate { return compareExpr{e, OpGreaterEq, Param(v)} }

// EqExpr compares two expressions of the same Go type. It is the join
// condition, and it is where the compiler earns its keep: a join between a
// string column and a bool column does not compile.
func EqExpr[T any](left, right Expr[T]) Predicate { return compareExpr{left, OpEq, right} }

// NotEqExpr renders left <> right.
//
// This family compares two expressions rather than an expression and a value,
// for conditions between columns or against a Param passed as an expression.
// Both sides share T, so a mismatched join does not compile.
func NotEqExpr[T any](left, right Expr[T]) Predicate { return compareExpr{left, OpNotEq, right} }

// LessExpr renders left < right.
func LessExpr[T any](left, right Expr[T]) Predicate { return compareExpr{left, OpLess, right} }

// LessEqExpr renders left <= right.
func LessEqExpr[T any](left, right Expr[T]) Predicate { return compareExpr{left, OpLessEq, right} }

// GreaterExpr renders left > right.
func GreaterExpr[T any](left, right Expr[T]) Predicate { return compareExpr{left, OpGreater, right} }

// GreaterEqExpr renders left >= right.
func GreaterEqExpr[T any](left, right Expr[T]) Predicate {
	return compareExpr{left, OpGreaterEq, right}
}

type nullCheck struct {
	expr render.SQLer
	op   string
}

func (n nullCheck) WriteSQL(b *strings.Builder, args *render.Args) {
	b.WriteString(n.expr.SQL(args))
	b.WriteString(sepSpace)
	b.WriteString(n.op)
}

// IsNull and IsNotNull test for NULL. Typed on T only so they accept any Expr.
func IsNull[T any](e Expr[T]) Predicate { return nullCheck{e, OpIsNull} }

// IsNotNull renders e IS NOT NULL.
func IsNotNull[T any](e Expr[T]) Predicate { return nullCheck{e, OpIsNotNull} }

type inList[T any] struct {
	expr   render.SQLer
	op     string
	values []T
}

func (l inList[T]) WriteSQL(b *strings.Builder, args *render.Args) {
	if len(l.values) == 0 {
		// Fail closed. An empty IN that rendered as "IN ()" is a syntax
		// error; dropping the predicate would widen the result set, which in
		// an auth query returns rows the caller asked to exclude.
		if l.op == OpIn {
			b.WriteString(sqlFalse)
		} else {
			b.WriteString(sqlTrue)
		}
		return
	}
	b.WriteString(l.expr.SQL(args))
	b.WriteString(sepSpace)
	b.WriteString(l.op)
	b.WriteString(" (")
	for i, v := range l.values {
		if i > 0 {
			b.WriteString(sepComma)
		}
		b.WriteString(args.Next(v))
	}
	b.WriteString(")")
}

const (
	sqlTrue  = "TRUE"
	sqlFalse = "FALSE"
)

// In binds every value. An empty list renders FALSE.
func In[T any](e Expr[T], values ...T) Predicate { return inList[T]{e, OpIn, values} }

// NotIn binds every value. An empty list renders TRUE.
func NotIn[T any](e Expr[T], values ...T) Predicate { return inList[T]{e, OpNotIn, values} }

type group struct {
	logic string
	preds []Predicate
}

func (g group) WriteSQL(b *strings.Builder, args *render.Args) {
	if len(g.preds) == 0 {
		b.WriteString(sqlTrue)
		return
	}
	// Always parenthesised, also with one member. Saving the parentheses
	// costs a precedence bug where an OR inside an AND widens a security check.
	b.WriteString("(")
	for i, p := range g.preds {
		if i > 0 {
			b.WriteString(sepSpace)
			b.WriteString(g.logic)
			b.WriteString(sepSpace)
		}
		p.WriteSQL(b, args)
	}
	b.WriteString(")")
}

// And groups predicates with AND. Empty renders TRUE.
func And(preds ...Predicate) Predicate { return group{LogicAnd, preds} }

// Or groups predicates with OR. Empty renders TRUE, which is the safe value
// for a filter that nobody narrowed; an empty OR that rendered FALSE would
// hide every row without an error.
func Or(preds ...Predicate) Predicate { return group{LogicOr, preds} }

type negation struct{ inner Predicate }

func (n negation) WriteSQL(b *strings.Builder, args *render.Args) {
	b.WriteString(LogicNot)
	b.WriteString(" (")
	n.inner.WriteSQL(b, args)
	b.WriteString(")")
}

// Not negates a predicate, always parenthesised.
func Not(p Predicate) Predicate { return negation{p} }

// PredicateSQL renders p to a string with the statement's Args. Engine
// packages use it to embed a predicate inside a function call such as sumIf.
func PredicateSQL(p Predicate, a *render.Args) string {
	var b strings.Builder
	p.WriteSQL(&b, a)
	return b.String()
}

// InExprs renders e IN (x, y, ...) over expressions, for a membership test
// against columns rather than bound values.
func InExprs[T any](e Expr[T], items ...Expr[T]) Predicate {
	return predicateFunc(func(a *render.Args) string {
		out := make([]string, len(items))
		for i, it := range items {
			out[i] = it.SQL(a)
		}
		return e.SQL(a) + sepSpace + OpIn + " (" + strings.Join(out, sepComma) + ")"
	})
}
