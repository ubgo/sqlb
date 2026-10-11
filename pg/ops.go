package pg

import "github.com/ubgo/sqlb"

const (
	constructOps = "pg.ops"

	opIsDistinctFrom    = "IS DISTINCT FROM"
	opIsNotDistinctFrom = "IS NOT DISTINCT FROM"
	opIsTrue            = "IS TRUE"
	opIsFalse           = "IS FALSE"
	opIsUnknown         = "IS UNKNOWN"
	opConcat            = "||"
	fnGreatest          = "GREATEST"
	fnLeast             = "LEAST"
)

// IsDistinctFrom is the null-safe inequality; IsNotDistinctFrom the null-safe
// equality. MySQL spells the second <=>, so both are here.
func IsDistinctFrom[T any](l, r sqlb.Expr[T]) sqlb.Predicate {
	return binpred(constructOps, opIsDistinctFrom, l, r)
}

// IsNotDistinctFrom renders l IS NOT DISTINCT FROM r, equality that treats
// two NULLs as equal. The operator a merge key needs when the key is
// nullable, because = answers NULL there.
func IsNotDistinctFrom[T any](l, r sqlb.Expr[T]) sqlb.Predicate {
	return binpred(constructOps, opIsNotDistinctFrom, l, r)
}

// IsTrue renders e IS TRUE.
//
// SQL booleans have three values, so a NULL boolean is neither true nor
// false. These three test for each without the NULL propagating, which a
// plain comparison would. ClickHouse has none of them.
func IsTrue(e sqlb.Expr[bool]) sqlb.Predicate {
	return pred(constructOps, func(a *sqlb.Args) string { return e.SQL(a) + " " + opIsTrue })
}

// IsFalse renders e IS FALSE, which is true for false and not for NULL.
func IsFalse(e sqlb.Expr[bool]) sqlb.Predicate {
	return pred(constructOps, func(a *sqlb.Args) string { return e.SQL(a) + " " + opIsFalse })
}

// IsUnknown renders e IS UNKNOWN, true only for NULL.
func IsUnknown(e sqlb.Expr[bool]) sqlb.Predicate {
	return pred(constructOps, func(a *sqlb.Args) string { return e.SQL(a) + " " + opIsUnknown })
}

// LikeEscape renders LIKE $1 ESCAPE $2 so a literal % or _ can be matched.
func LikeEscape(e sqlb.Expr[string], pattern, escape string) sqlb.Predicate {
	return pred(constructOps, func(a *sqlb.Args) string {
		return e.SQL(a) + " LIKE " + a.Next(pattern) + " ESCAPE " + a.Next(escape)
	})
}

// Collate renders e COLLATE "name".
func Collate(e sqlb.Expr[string], collation string) sqlb.Expr[string] {
	return expr[string](constructOps, func(a *sqlb.Args) string {
		return e.SQL(a) + " COLLATE " + a.Dialect().QuoteIdent(collation)
	})
}

// ConcatOp renders (a || b), the operator form. The core's Concat renders
// the concat() function, which every engine has.
func ConcatOp(l, r sqlb.Expr[string]) sqlb.Expr[string] {
	return expr[string](constructOps, func(a *sqlb.Args) string {
		return "(" + l.SQL(a) + " " + opConcat + " " + r.SQL(a) + ")"
	})
}

// Greatest renders greatest(a, b, ...), the largest argument, skipping NULLs.
// SQLite has neither this nor Least.
func Greatest[T any](first sqlb.Expr[T], rest ...sqlb.Expr[T]) sqlb.Expr[T] {
	return fn[T](constructOps, fnGreatest, exprList(first, rest)...)
}

// Least renders least(a, b, ...), the smallest argument, skipping NULLs.
func Least[T any](first sqlb.Expr[T], rest ...sqlb.Expr[T]) sqlb.Expr[T] {
	return fn[T](constructOps, fnLeast, exprList(first, rest)...)
}

func exprList[T any](first sqlb.Expr[T], rest []sqlb.Expr[T]) []sqlb.SQLer {
	out := make([]sqlb.SQLer, 0, 1+len(rest))
	out = append(out, first)
	for _, r := range rest {
		out = append(out, r)
	}
	return out
}
