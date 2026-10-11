package expr

import (
	"strings"

	"github.com/ubgo/sqlb/render"
)

// AggSpec is what an aggregate call can carry beyond its arguments. The
// fields are hooks, not decisions: the core renders whatever they hold, and
// an engine package decides what goes in. Distinct is here because every
// engine has count(DISTINCT x). FILTER and ORDER BY inside the call are not
// universal, so pg sets Inner and Suffix instead.
type AggSpec struct {
	Distinct bool
	// Inner renders after the arguments, inside the parentheses.
	Inner func(a *render.Args) string
	// Suffix renders after the closing parenthesis.
	Suffix func(a *render.Args) string
}

// AggOption modifies an aggregate call.
type AggOption func(*AggSpec)

// Distinct adds DISTINCT inside the aggregate: count(DISTINCT x).
func Distinct() AggOption { return func(s *AggSpec) { s.Distinct = true } }

// Aggregate renders name(args...) with the options applied, typed R. It is
// the constructor every specific aggregate uses, and the typed door for an
// aggregate the package does not name.
func Aggregate[R any](name string, args []render.SQLer, opts ...AggOption) Expr[R] {
	var spec AggSpec
	for _, o := range opts {
		o(&spec)
	}
	return New[R](func(a *render.Args) string {
		var b strings.Builder
		b.WriteString(CheckedFunctionName(a, name, "aggregate"))
		b.WriteString("(")
		if spec.Distinct {
			b.WriteString(kwDistinct)
			b.WriteString(sepSpace)
		}
		for i, arg := range args {
			if i > 0 {
				b.WriteString(sepComma)
			}
			b.WriteString(arg.SQL(a))
		}
		if spec.Inner != nil {
			b.WriteString(spec.Inner(a))
		}
		b.WriteString(")")
		if spec.Suffix != nil {
			b.WriteString(spec.Suffix(a))
		}
		return b.String()
	})
}

// The five aggregates every engine has.

// Count renders count(*), int64 and never null.
func Count(opts ...AggOption) Expr[int64] {
	return Aggregate[int64](fnCount, []render.SQLer{render.Raw(func(*render.Args) string { return "*" })}, opts...)
}

// CountOf renders count(e).
func CountOf[T any](e Expr[T], opts ...AggOption) Expr[int64] {
	return Aggregate[int64](fnCount, []render.SQLer{e}, opts...)
}

// Sum keeps the operand type. SQL widens sum(int4) to int8; database/sql
// scans an int64 into any Go integer that fits, so the Go type is the
// author's choice of width. Sum of an empty group is NULL: wrap in Nullable
// for an ungrouped query.
func Sum[T Number](e Expr[T], opts ...AggOption) Expr[T] {
	return Aggregate[T](fnSum, []render.SQLer{e}, opts...)
}

// Avg is float64. SQL returns numeric for integer input; scanning numeric
// into float64 loses precision beyond 2^53, which is acceptable for an
// average and stated here.
func Avg[T Number](e Expr[T], opts ...AggOption) Expr[float64] {
	return Aggregate[float64](fnAvg, []render.SQLer{e}, opts...)
}

// Min renders min(e). Typed as T rather than *T: a group that exists has at
// least one row, and a group with no rows produces no result row at all.
func Min[T any](e Expr[T], opts ...AggOption) Expr[T] {
	return Aggregate[T](fnMin, []render.SQLer{e}, opts...)
}

// Max renders max(e).
func Max[T any](e Expr[T], opts ...AggOption) Expr[T] {
	return Aggregate[T](fnMax, []render.SQLer{e}, opts...)
}
