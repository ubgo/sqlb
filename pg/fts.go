package pg

import "github.com/ubgo/sqlb"

const (
	constructFTS = "pg.fts"

	opMatches    = "@@"
	opTSAnd      = "&&"
	opTSOr       = "||"
	opTSNot      = "!!"
	opTSFollowed = "<->"

	fnToTSVector       = "to_tsvector"
	fnToTSQuery        = "to_tsquery"
	fnPlainToTSQuery   = "plainto_tsquery"
	fnPhraseToTSQuery  = "phraseto_tsquery"
	fnWebsearchToTSQ   = "websearch_to_tsquery"
	fnTSRank           = "ts_rank"
	fnTSRankCD         = "ts_rank_cd"
	fnTSHeadline       = "ts_headline"
	fnSetWeight        = "setweight"
	fnStrip            = "strip"
	fnArrayToTSVector  = "array_to_tsvector"
	fnTSVectorConcatOp = "||"
)

// TSVector is a Postgres tsvector in text form: a document reduced to its
// lexemes, with their positions and weights.
//
// It and TSQuery are distinct Go types so the compiler refuses to pass a
// query where a document is expected. SQL accepts that mistake and answers
// it wrongly, because both are text on the wire.
type TSVector string

// TSQuery is a Postgres tsquery in text form: the search expression matched
// against a TSVector. See TSVector for why the two are separate types.
type TSQuery string

// Weight is a tsvector label: A, B, C or D.
type Weight string

// The four weight labels. A is the strongest and D the weakest; ts_rank
// weights them {D, C, B, A} = {0.1, 0.2, 0.4, 1.0} unless told otherwise.
const (
	WeightA Weight = "A"
	WeightB Weight = "B"
	WeightC Weight = "C"
	WeightD Weight = "D"
)

// ToTSVector renders to_tsvector(config, e). The config is a constant.
func ToTSVector(config string, e sqlb.Expr[string]) sqlb.Expr[TSVector] {
	return fn[TSVector](constructFTS, fnToTSVector, quoteLit(config), e)
}

// WebsearchToTSQuery is the safe form for user input: it never raises a
// syntax error. ToTSQuery can, and the doc on it says so.
func WebsearchToTSQuery(config, text string) sqlb.Expr[TSQuery] {
	return fn[TSQuery](constructFTS, fnWebsearchToTSQ, quoteLit(config), sqlb.Param(text))
}

// PlainToTSQuery renders plainto_tsquery(config, $n): every word ANDed, with
// operators in the input treated as text. Safe for raw user input.
func PlainToTSQuery(config, text string) sqlb.Expr[TSQuery] {
	return fn[TSQuery](constructFTS, fnPlainToTSQuery, quoteLit(config), sqlb.Param(text))
}

// PhraseToTSQuery renders phraseto_tsquery(config, $n): the words in order
// and adjacent, which is a phrase search.
func PhraseToTSQuery(config, text string) sqlb.Expr[TSQuery] {
	return fn[TSQuery](constructFTS, fnPhraseToTSQuery, quoteLit(config), sqlb.Param(text))
}

// ToTSQuery raises a syntax error on malformed input. Use it for
// author-written queries, never for raw user input.
func ToTSQuery(config, text string) sqlb.Expr[TSQuery] {
	return fn[TSQuery](constructFTS, fnToTSQuery, quoteLit(config), sqlb.Param(text))
}

// Matches renders vector @@ query.
func Matches(v sqlb.Expr[TSVector], q sqlb.Expr[TSQuery]) sqlb.Predicate {
	return binpred(constructFTS, opMatches, v, q)
}

// TSRank and TSRankCD.
func TSRank(v sqlb.Expr[TSVector], q sqlb.Expr[TSQuery]) sqlb.Expr[float32] {
	return fn[float32](constructFTS, fnTSRank, v, q)
}

// TSRankCD renders ts_rank_cd(v, q), the cover-density ranking. It accounts
// for how close the matched terms are to each other, which ts_rank ignores.
func TSRankCD(v sqlb.Expr[TSVector], q sqlb.Expr[TSQuery]) sqlb.Expr[float32] {
	return fn[float32](constructFTS, fnTSRankCD, v, q)
}

// TSHeadline renders ts_headline(config, text, query, options).
func TSHeadline(config string, text sqlb.Expr[string], q sqlb.Expr[TSQuery], options string) sqlb.Expr[string] {
	return fn[string](constructFTS, fnTSHeadline, quoteLit(config), text, q, sqlb.Param(options))
}

// SetWeight renders setweight(v, 'A').
func SetWeight(v sqlb.Expr[TSVector], w Weight) sqlb.Expr[TSVector] {
	return fn[TSVector](constructFTS, fnSetWeight, v, closedLit(w, w.valid(), "weight"))
}

// Strip renders strip(v), dropping positions and weights. It makes a vector
// smaller to store, at the cost of phrase search and weighted ranking.
func Strip(v sqlb.Expr[TSVector]) sqlb.Expr[TSVector] { return fn[TSVector](constructFTS, fnStrip, v) }

// TSVectorConcat renders a || b for weighted vectors.
func TSVectorConcat(l, r sqlb.Expr[TSVector]) sqlb.Expr[TSVector] {
	return binop[TSVector](constructFTS, fnTSVectorConcatOp, l, r)
}

// TSAnd renders l && r, both terms required.
//
// The four combinators build a query from smaller ones, which is how a filter
// interface composes a search without ever concatenating query text.
func TSAnd(l, r sqlb.Expr[TSQuery]) sqlb.Expr[TSQuery] {
	return binop[TSQuery](constructFTS, opTSAnd, l, r)
}

// TSOr renders l || r, either term.
func TSOr(l, r sqlb.Expr[TSQuery]) sqlb.Expr[TSQuery] {
	return binop[TSQuery](constructFTS, opTSOr, l, r)
}

// TSNot renders !! q, excluding the term.
func TSNot(q sqlb.Expr[TSQuery]) sqlb.Expr[TSQuery] {
	return expr[TSQuery](constructFTS, func(a *sqlb.Args) string { return opTSNot + " " + q.SQL(a) })
}

// TSFollowedBy renders l <-> r: l immediately followed by r, the phrase
// operator.
func TSFollowedBy(l, r sqlb.Expr[TSQuery]) sqlb.Expr[TSQuery] {
	return binop[TSQuery](constructFTS, opTSFollowed, l, r)
}
