package pg

import (
	"time"

	"github.com/ubgo/sqlb"
)

const (
	constructRange = "pg.range"

	opRangeContains  = "@>"
	opRangeContained = "<@"
	opRangeOverlap   = "&&"
	opRangeLeft      = "<<"
	opRangeRight     = ">>"
	opRangeAdjacent  = "-|-"
	opRangeUnion     = "+"
	opRangeIntersect = "*"
	opRangeDiff      = "-"

	fnLowerBound = "lower"
	fnUpperBound = "upper"
	fnIsEmpty    = "isempty"
	fnLowerInc   = "lower_inc"
	fnUpperInc   = "upper_inc"
	fnRangeMerge = "range_merge"
	fnTstzRange  = "tstzrange"
	fnInt8Range  = "int8range"
	fnRangeAgg   = "range_agg"
)

// Range is a Postgres range in its text form, for example "[2026-01-01,2026-02-01)".
// It is typed by the Go element type so a tstzrange cannot be compared with
// an int8range.
type Range[T any] string

// Bounds is the inclusivity string a range constructor takes: "[)", "[]", "(]", "()".
type Bounds string

// The four inclusivity combinations. Half-open "[)" is the one to reach for:
// adjacent ranges meet without overlapping, which is what makes Adjacent and
// an exclusion constraint behave.
const (
	BoundsIncExc Bounds = "[)"
	BoundsIncInc Bounds = "[]"
	BoundsExcInc Bounds = "(]"
	BoundsExcExc Bounds = "()"
)

// TstzRange renders tstzrange($1, $2, 'bounds').
func TstzRange(from, to time.Time, bounds Bounds) sqlb.Expr[Range[time.Time]] {
	return fn[Range[time.Time]](constructRange, fnTstzRange, sqlb.Param(from), sqlb.Param(to), closedLit(bounds, bounds.valid(), "range bounds"))
}

// Int8Range renders int8range($1, $2, 'bounds').
func Int8Range(from, to int64, bounds Bounds) sqlb.Expr[Range[int64]] {
	return fn[Range[int64]](constructRange, fnInt8Range, sqlb.Param(from), sqlb.Param(to), closedLit(bounds, bounds.valid(), "range bounds"))
}

// RangeContains renders range @> $n for an element.
func RangeContains[T any](r sqlb.Expr[Range[T]], v T) sqlb.Predicate {
	// @> is overloaded on (range, element) and (range, range); a bare
	// parameter takes the range form and the driver cannot encode an integer
	// as int8range. The cast, from T, selects the element form.
	return binpred(constructRange, opRangeContains, r, typedParam(v))
}

// RangeContainsRange renders l @> r; RangeOverlaps l && r; Adjacent l -|- r;
// StrictlyLeft l << r; StrictlyRight l >> r.
func RangeContainsRange[T any](l, r sqlb.Expr[Range[T]]) sqlb.Predicate {
	return binpred(constructRange, opRangeContains, l, r)
}

// RangeContainedBy renders l <@ r: every point of l lies in r.
func RangeContainedBy[T any](l, r sqlb.Expr[Range[T]]) sqlb.Predicate {
	return binpred(constructRange, opRangeContained, l, r)
}

// RangeOverlaps renders l && r: the two share at least one point.
func RangeOverlaps[T any](l, r sqlb.Expr[Range[T]]) sqlb.Predicate {
	return binpred(constructRange, opRangeOverlap, l, r)
}

// Adjacent renders l -|- r: the two touch without overlapping. Only
// meaningful with half-open bounds; two inclusive ranges that touch overlap.
func Adjacent[T any](l, r sqlb.Expr[Range[T]]) sqlb.Predicate {
	return binpred(constructRange, opRangeAdjacent, l, r)
}

// StrictlyLeft renders l << r: every point of l is before every point of r.
func StrictlyLeft[T any](l, r sqlb.Expr[Range[T]]) sqlb.Predicate {
	return binpred(constructRange, opRangeLeft, l, r)
}

// StrictlyRight renders l >> r.
func StrictlyRight[T any](l, r sqlb.Expr[Range[T]]) sqlb.Predicate {
	return binpred(constructRange, opRangeRight, l, r)
}

// RangeUnion renders l + r. It raises unless the two ranges overlap or are
// adjacent, because the result must be a single range; RangeMerge is the
// forgiving version.
func RangeUnion[T any](l, r sqlb.Expr[Range[T]]) sqlb.Expr[Range[T]] {
	return binop[Range[T]](constructRange, opRangeUnion, l, r)
}

// RangeIntersect renders l * r, the common part, empty when they do not meet.
func RangeIntersect[T any](l, r sqlb.Expr[Range[T]]) sqlb.Expr[Range[T]] {
	return binop[Range[T]](constructRange, opRangeIntersect, l, r)
}

// RangeDiff renders l - r. It raises when the result would be two ranges,
// which happens when r sits strictly inside l.
func RangeDiff[T any](l, r sqlb.Expr[Range[T]]) sqlb.Expr[Range[T]] {
	return binop[Range[T]](constructRange, opRangeDiff, l, r)
}

// LowerBound and UpperBound are nullable: an unbounded side is NULL.
func LowerBound[T any](r sqlb.Expr[Range[T]]) sqlb.Expr[*T] {
	return fn[*T](constructRange, fnLowerBound, r)
}

// UpperBound renders upper(r), NULL when the range is unbounded above.
func UpperBound[T any](r sqlb.Expr[Range[T]]) sqlb.Expr[*T] {
	return fn[*T](constructRange, fnUpperBound, r)
}

// IsEmpty renders isempty(r). An empty range contains no points and is not
// the same as a range whose bounds are equal and inclusive.
func IsEmpty[T any](r sqlb.Expr[Range[T]]) sqlb.Predicate {
	return pred(constructRange, func(a *sqlb.Args) string { return fnIsEmpty + "(" + r.SQL(a) + ")" })
}

// LowerInc renders lower_inc(r): whether the lower bound is inclusive.
func LowerInc[T any](r sqlb.Expr[Range[T]]) sqlb.Expr[bool] {
	return fn[bool](constructRange, fnLowerInc, r)
}

// UpperInc renders upper_inc(r).
func UpperInc[T any](r sqlb.Expr[Range[T]]) sqlb.Expr[bool] {
	return fn[bool](constructRange, fnUpperInc, r)
}

// RangeMerge renders range_merge(l, r), the smallest range containing both.
// Unlike RangeUnion it never raises: it fills the gap instead.
func RangeMerge[T any](l, r sqlb.Expr[Range[T]]) sqlb.Expr[Range[T]] {
	return fn[Range[T]](constructRange, fnRangeMerge, l, r)
}

// RangeAgg renders range_agg(r), a multirange, typed as text here.
func RangeAgg[T any](r sqlb.Expr[Range[T]], opts ...sqlb.AggOption) sqlb.Expr[string] {
	return sqlb.Aggregate[string](fnRangeAgg, []sqlb.SQLer{r}, opts...)
}
