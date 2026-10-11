package pg

import "github.com/ubgo/sqlb"

// The exported constructors an extension package builds on. Each renders
// only under the Postgres dialect and records Args.Unsupported otherwise, so
// a helper in pg/vector or pg/ltree is one line and fails closed like the
// helpers here. construct is the name that appears in the error, "vector.L2".

// Guard reports whether rendering may continue under the dialect in a. When
// it returns false it has already recorded the construct as unsupported.
func Guard(a *sqlb.Args, construct string) bool { return guard(a, construct) }

// Expr makes a guarded typed expression from a render function.
func Expr[T any](construct string, render func(a *sqlb.Args) string) sqlb.Expr[T] {
	return expr[T](construct, render)
}

// Pred makes a guarded predicate from a render function.
func Pred(construct string, render func(a *sqlb.Args) string) sqlb.Predicate {
	return pred(construct, render)
}

// BinOp renders "left op right" as a guarded expression typed R.
func BinOp[R any](construct, op string, l, r sqlb.SQLer) sqlb.Expr[R] {
	return binop[R](construct, op, l, r)
}

// BinPred renders "left op right" as a guarded predicate.
//
// op, like BinOp's and Func's name, is written into the statement as is: an
// operator cannot be quoted and a function name may be schema-qualified. These
// are the extension author's text and the one place in this package where a
// string reaches the statement unchecked, which is what makes them the escape
// hatch rather than the ordinary path. A value from a request never reaches
// one; pg.AnyExpr and pg.Exclude, which a caller does reach for, take a typed
// operator that is checked.
func BinPred(construct, op string, l, r sqlb.SQLer) sqlb.Predicate {
	return binpred(construct, op, l, r)
}

// Func renders a guarded function call typed R.
func Func[R any](construct, name string, args ...sqlb.SQLer) sqlb.Expr[R] {
	return fn[R](construct, name, args...)
}

// ClauseIn makes a guarded clause for a statement slot, typed to this engine
// and to the statement kind K, which the caller names: an extension package
// writing a SELECT clause instantiates ClauseIn[sqlb.SelectKind].
func ClauseIn[K any](slot sqlb.Slot, construct string, render func(a *sqlb.Args) string) sqlb.Clause[Dialect, K] {
	return sqlb.EngineClause[Dialect, K](construct, slot, render)
}

// QuoteLit renders an author-chosen constant as a string literal, written by
// the dialect's own quoter: plainly when it holds no backslash, and in the
// E'...' form when it does, so it reads the same whatever the server's
// standard_conforming_strings. Never for request data; that goes through
// sqlb.Param.
func QuoteLit(s string) sqlb.SQLer { return quoteLit(s) }

// List renders SQLers comma-separated.
func List(a *sqlb.Args, items []sqlb.SQLer) string { return list(a, items) }
