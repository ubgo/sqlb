// Package trgm is the pg_trgm extension: trigram similarity operators and
// functions. Needs CREATE EXTENSION pg_trgm. The operators are served by a
// GIN index with gin_trgm_ops, and the distance operators by GiST with
// gist_trgm_ops.
package trgm

import (
	"github.com/ubgo/sqlb"
	"github.com/ubgo/sqlb/pg"
)

const (
	construct = "trgm"

	opSimilar           = "%"
	opWordSimilar       = "<%"
	opStrictWordSimilar = "<<%"
	opDistance          = "<->"
	opWordDistance      = "<<->"
)

// Similar renders a % b: similarity above pg_trgm.similarity_threshold.
func Similar(a, b sqlb.Expr[string]) sqlb.Predicate {
	return pg.BinPred(construct+".Similar", opSimilar, a, b)
}

// WordSimilar renders a <% b: a matches a word inside b, above
// pg_trgm.word_similarity_threshold.
func WordSimilar(a, b sqlb.Expr[string]) sqlb.Predicate {
	return pg.BinPred(construct+".WordSimilar", opWordSimilar, a, b)
}

// StrictWordSimilar renders a <<% b, the word-boundary form of WordSimilar:
// the match must cover whole words, not a prefix of one.
func StrictWordSimilar(a, b sqlb.Expr[string]) sqlb.Predicate {
	return pg.BinPred(construct+".StrictWordSimilar", opStrictWordSimilar, a, b)
}

// Distance renders a <-> b, the KNN distance for ORDER BY with a GiST index.
func Distance(a, b sqlb.Expr[string]) sqlb.Expr[float32] {
	return pg.BinOp[float32](construct+".Distance", opDistance, a, b)
}

// WordDistance renders a <<-> b, the KNN distance matching WordSimilar.
func WordDistance(a, b sqlb.Expr[string]) sqlb.Expr[float32] {
	return pg.BinOp[float32](construct+".WordDistance", opWordDistance, a, b)
}

// Similarity renders similarity(a, b), the trigram overlap from 0 to 1. It
// is the value behind the % operator, for a SELECT item or a threshold of
// the caller's own.
func Similarity(a, b sqlb.Expr[string]) sqlb.Expr[float32] {
	return pg.Func[float32](construct+".Similarity", "similarity", a, b)
}

// WordSimilarity renders word_similarity(a, b), the value behind <%.
func WordSimilarity(a, b sqlb.Expr[string]) sqlb.Expr[float32] {
	return pg.Func[float32](construct+".WordSimilarity", "word_similarity", a, b)
}

// StrictWordSimilarity renders strict_word_similarity(a, b), the value
// behind <<%.
func StrictWordSimilarity(a, b sqlb.Expr[string]) sqlb.Expr[float32] {
	return pg.Func[float32](construct+".StrictWordSimilarity", "strict_word_similarity", a, b)
}

// ShowTrgm renders show_trgm(e), the trigrams themselves. It is a debugging
// call: it shows why two strings do or do not match.
func ShowTrgm(e sqlb.Expr[string]) sqlb.Expr[[]string] {
	return pg.Func[[]string](construct+".ShowTrgm", "show_trgm", e)
}
