// Package fuzzy is the fuzzystrmatch extension: phonetic and edit-distance
// matching. Needs CREATE EXTENSION fuzzystrmatch.
package fuzzy

import (
	"github.com/ubgo/sqlb"
	"github.com/ubgo/sqlb/pg"
)

const construct = "fuzzy"

// Dmetaphone renders dmetaphone(e), the primary double-metaphone code.
// Two names match phonetically when their codes are equal.
func Dmetaphone(e sqlb.Expr[string]) sqlb.Expr[string] {
	return pg.Func[string](construct+".Dmetaphone", "dmetaphone", e)
}

// DmetaphoneAlt renders dmetaphone_alt(e), the alternate code. A name with
// two accepted pronunciations matches on either code.
func DmetaphoneAlt(e sqlb.Expr[string]) sqlb.Expr[string] {
	return pg.Func[string](construct+".DmetaphoneAlt", "dmetaphone_alt", e)
}

// Soundex renders soundex(e), the four-character English code. Metaphone
// and Dmetaphone are more accurate; Soundex is here for existing data.
func Soundex(e sqlb.Expr[string]) sqlb.Expr[string] {
	return pg.Func[string](construct+".Soundex", "soundex", e)
}

// Metaphone renders metaphone(e, maxLen). maxLen caps the code length and
// so the strictness of the match.
func Metaphone(e sqlb.Expr[string], maxLen int) sqlb.Expr[string] {
	return pg.Func[string](construct+".Metaphone", "metaphone", e, sqlb.Param(maxLen))
}

// Levenshtein renders levenshtein(a, b), the full edit distance. Use
// LevenshteinLessEqual when a threshold is known, because it stops early.
func Levenshtein(a, b sqlb.Expr[string]) sqlb.Expr[int32] {
	return pg.Func[int32](construct+".Levenshtein", "levenshtein", a, b)
}

// LevenshteinLessEqual stops counting at maxDistance, which is what makes
// it cheap for a threshold search: the server abandons a pair once it knows
// the distance exceeds the bound instead of computing the exact value.
func LevenshteinLessEqual(a, b sqlb.Expr[string], maxDistance int) sqlb.Expr[int32] {
	return pg.Func[int32](construct+".LevenshteinLessEqual", "levenshtein_less_equal", a, b, sqlb.Param(maxDistance))
}

// Difference renders difference(a, b): how many soundex positions agree, 0 to 4.
func Difference(a, b sqlb.Expr[string]) sqlb.Expr[int32] {
	return pg.Func[int32](construct+".Difference", "difference", a, b)
}
