package pg

import (
	"strings"

	"github.com/ubgo/sqlb"
)

const (
	constructAgg = "pg.agg"

	fnArrayAgg       = "array_agg"
	fnStringAgg      = "string_agg"
	fnBoolAnd        = "bool_and"
	fnBoolOr         = "bool_or"
	fnPercentileCont = "percentile_cont"
	fnPercentileDisc = "percentile_disc"
	fnMode           = "mode"
	fnGrouping       = "grouping"
	fnStddev         = "stddev"
	fnVariance       = "variance"
)

// Filter adds FILTER (WHERE p) after an aggregate. Postgres and SQLite have
// it; MySQL and ClickHouse do not, so it guards.
func Filter(p sqlb.Predicate) sqlb.AggOption {
	return func(s *sqlb.AggSpec) {
		s.Suffix = func(a *sqlb.Args) string {
			if !guard(a, constructAgg) {
				return ""
			}
			return " FILTER (WHERE " + sqlb.PredicateSQL(p, a) + ")"
		}
	}
}

// OrderWithin adds ORDER BY inside the aggregate: array_agg(x ORDER BY y).
func OrderWithin(ts ...sqlb.OrderTerm) sqlb.AggOption {
	return func(s *sqlb.AggSpec) {
		s.Inner = func(a *sqlb.Args) string {
			if !guard(a, constructAgg) {
				return ""
			}
			var b strings.Builder
			b.WriteString(" ORDER BY ")
			for i, t := range ts {
				if i > 0 {
					b.WriteString(", ")
				}
				b.WriteString(orderTermSQL(t, a))
			}
			return b.String()
		}
	}
}

// orderTermSQL renders an OrderTerm outside the core, which keeps the
// term's fields exported for exactly this use.
func orderTermSQL(t sqlb.OrderTerm, a *sqlb.Args) string {
	s := t.Expr(a)
	if t.Using != "" {
		s += " USING " + t.Using
	} else {
		s += " " + string(t.Direction)
	}
	if t.Nulls != "" {
		s += " " + string(t.Nulls)
	}
	return s
}

// ArrayAgg collects values into a slice.
func ArrayAgg[T any](e sqlb.Expr[T], opts ...sqlb.AggOption) sqlb.Expr[[]T] {
	return guardedAgg[[]T](fnArrayAgg, []sqlb.SQLer{e}, opts)
}

// StringAgg joins strings with a bound separator.
func StringAgg(e sqlb.Expr[string], sep string, opts ...sqlb.AggOption) sqlb.Expr[string] {
	return guardedAgg[string](fnStringAgg, []sqlb.SQLer{e, sqlb.Param(sep)}, opts)
}

// BoolAnd renders bool_and(e): true when every row in the group is true.
func BoolAnd(e sqlb.Expr[bool], opts ...sqlb.AggOption) sqlb.Expr[bool] {
	return guardedAgg[bool](fnBoolAnd, []sqlb.SQLer{e}, opts)
}

// BoolOr renders bool_or(e): true when any row is.
func BoolOr(e sqlb.Expr[bool], opts ...sqlb.AggOption) sqlb.Expr[bool] {
	return guardedAgg[bool](fnBoolOr, []sqlb.SQLer{e}, opts)
}

// Stddev renders stddev(e), the sample standard deviation.
func Stddev[T sqlb.Number](e sqlb.Expr[T], opts ...sqlb.AggOption) sqlb.Expr[float64] {
	return guardedAgg[float64](fnStddev, []sqlb.SQLer{e}, opts)
}

// Variance renders variance(e), the sample variance.
func Variance[T sqlb.Number](e sqlb.Expr[T], opts ...sqlb.AggOption) sqlb.Expr[float64] {
	return guardedAgg[float64](fnVariance, []sqlb.SQLer{e}, opts)
}

func guardedAgg[R any](name string, args []sqlb.SQLer, opts []sqlb.AggOption) sqlb.Expr[R] {
	inner := sqlb.Aggregate[R](name, args, opts...)
	return expr[R](constructAgg, inner.SQL)
}

// WithinGroup renders an ordered-set aggregate: name(args) WITHIN GROUP (ORDER BY ...).
func WithinGroup[R any](name string, args []sqlb.SQLer, order ...sqlb.OrderTerm) sqlb.Expr[R] {
	return expr[R](constructAgg, func(a *sqlb.Args) string {
		var b strings.Builder
		b.WriteString(name)
		b.WriteString("(")
		b.WriteString(list(a, args))
		b.WriteString(") WITHIN GROUP (ORDER BY ")
		for i, t := range order {
			if i > 0 {
				b.WriteString(", ")
			}
			b.WriteString(orderTermSQL(t, a))
		}
		b.WriteString(")")
		return b.String()
	})
}

// PercentileCont renders percentile_cont(f) WITHIN GROUP (ORDER BY e).
func PercentileCont(fraction float64, order sqlb.OrderTerm) sqlb.Expr[float64] {
	return WithinGroup[float64](fnPercentileCont, []sqlb.SQLer{sqlb.Param(fraction)}, order)
}

// PercentileDisc keeps the ordered expression's type.
func PercentileDisc[T any](fraction float64, e sqlb.Expr[T], dir sqlb.SortDirection) sqlb.Expr[T] {
	return WithinGroup[T](fnPercentileDisc, []sqlb.SQLer{sqlb.Param(fraction)}, sqlb.OrderBy(e, dir))
}

// Mode renders mode() WITHIN GROUP (ORDER BY e).
func Mode[T any](e sqlb.Expr[T]) sqlb.Expr[T] {
	return WithinGroup[T](fnMode, nil, sqlb.OrderBy(e, sqlb.SortAsc))
}

// GroupingSets renders GROUPING SETS ((a), (b)), one result group per set.
//
// This and the two below are GROUP BY items rather than expressions. The trap
// they share: a column not in the current set is NULL on that row, so every
// grouped column must be bound through Nullable or the scan fails on the
// super-aggregate rows.
func GroupingSets(sets ...[]sqlb.SQLer) sqlb.SQLer {
	return sqlb.Raw(func(a *sqlb.Args) string {
		if !guard(a, constructAgg) {
			return ""
		}
		out := make([]string, len(sets))
		for i, set := range sets {
			out[i] = "(" + list(a, set) + ")"
		}
		return "GROUPING SETS (" + strings.Join(out, ", ") + ")"
	})
}

// Cube renders CUBE (a, b), every combination of the items, which is 2^n
// groups. Watch the count before reaching for it on more than a few columns.
func Cube(items ...sqlb.SQLer) sqlb.SQLer { return groupingList("CUBE", items) }

// Rollup renders ROLLUP (a, b), the hierarchical subtotals: (a, b), (a) and
// the grand total, which is n+1 groups rather than 2^n.
func Rollup(items ...sqlb.SQLer) sqlb.SQLer { return groupingList("ROLLUP", items) }

func groupingList(kw string, items []sqlb.SQLer) sqlb.SQLer {
	return sqlb.Raw(func(a *sqlb.Args) string {
		if !guard(a, constructAgg) {
			return ""
		}
		return kw + " (" + list(a, items) + ")"
	})
}

// Grouping renders grouping(e): 1 on a super-aggregate row, else 0.
func Grouping(items ...sqlb.SQLer) sqlb.Expr[int32] {
	return fn[int32](constructAgg, fnGrouping, items...)
}
