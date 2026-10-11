// Package ltree is the ltree extension: a materialized-path label tree with
// its own operators. Needs CREATE EXTENSION ltree; the @> and ~ operators
// are served by a GiST index with gist_ltree_ops.
package ltree

import (
	"github.com/ubgo/sqlb"
	"github.com/ubgo/sqlb/pg"
)

const (
	construct = "ltree"

	opAncestor   = "@>"
	opDescendant = "<@"
	opLQuery     = "~"
	opTxtQuery   = "@"
	opConcat     = "||"

	castLtree    = "::ltree"
	castLQuery   = "::lquery"
	castTxtQuery = "::ltxtquery"
)

// Path is an ltree value, "top.science.astronomy". It scans and values as
// text, which is ltree's I/O form on every driver.
type Path string

// Lit renders a bound path with an explicit cast, $n::ltree, so an operator
// between a column and the parameter resolves without ambiguity.
func Lit(path string) sqlb.Expr[Path] {
	return pg.Expr[Path](construct+".Lit", func(a *sqlb.Args) string { return sqlb.Param(path).SQL(a) + castLtree })
}

// AncestorOf renders a @> b: a is an ancestor of b, or equal.
func AncestorOf(a, b sqlb.Expr[Path]) sqlb.Predicate {
	return pg.BinPred(construct+".AncestorOf", opAncestor, a, b)
}

// DescendantOf renders a <@ b: a is below b, or equal.
func DescendantOf(a, b sqlb.Expr[Path]) sqlb.Predicate {
	return pg.BinPred(construct+".DescendantOf", opDescendant, a, b)
}

// MatchesLQuery renders path ~ $n::lquery, the pattern "*.science.*{1,}".
func MatchesLQuery(path sqlb.Expr[Path], lquery string) sqlb.Predicate {
	return pg.BinPred(construct+".MatchesLQuery", opLQuery, path, castParam(lquery, castLQuery))
}

// MatchesTxtQuery renders path @ $n::ltxtquery, the boolean label search
// "science & !astronomy".
func MatchesTxtQuery(path sqlb.Expr[Path], q string) sqlb.Predicate {
	return pg.BinPred(construct+".MatchesTxtQuery", opTxtQuery, path, castParam(q, castTxtQuery))
}

// castParam binds a Go string and casts it, so the operator on the left
// sees the extension type and not text.
func castParam(v, cast string) sqlb.SQLer {
	return sqlb.Raw(func(a *sqlb.Args) string { return sqlb.Param(v).SQL(a) + cast })
}

// NLevel renders nlevel(p), the number of labels in the path. It is the
// depth of the node, so it orders a subtree by level.
func NLevel(p sqlb.Expr[Path]) sqlb.Expr[int32] {
	return pg.Func[int32](construct+".NLevel", "nlevel", p)
}

// Subpath renders subpath(p, offset, len): len labels from offset, 0-based.
// A negative offset counts from the end.
func Subpath(p sqlb.Expr[Path], offset, length int) sqlb.Expr[Path] {
	return pg.Func[Path](construct+".Subpath", "subpath", p, sqlb.Param(offset), sqlb.Param(length))
}

// Subltree renders subltree(p, start, end): the labels from start up to but
// not including end. Subpath takes a count where this takes an end.
func Subltree(p sqlb.Expr[Path], start, end int) sqlb.Expr[Path] {
	return pg.Func[Path](construct+".Subltree", "subltree", p, sqlb.Param(start), sqlb.Param(end))
}

// Index renders index(p, $n::ltree): the position of sub in p, or -1.
func Index(p sqlb.Expr[Path], sub string) sqlb.Expr[int32] {
	return pg.Func[int32](construct+".Index", "index", p, castParam(sub, castLtree))
}

// LCA renders lca(p...): the longest common ancestor.
func LCA(paths ...sqlb.Expr[Path]) sqlb.Expr[Path] {
	args := make([]sqlb.SQLer, len(paths))
	for i, p := range paths {
		args[i] = p
	}
	return pg.Func[Path](construct+".LCA", "lca", args...)
}

// Concat renders a || b for two paths.
func Concat(a, b sqlb.Expr[Path]) sqlb.Expr[Path] {
	return pg.BinOp[Path](construct+".Concat", opConcat, a, b)
}
