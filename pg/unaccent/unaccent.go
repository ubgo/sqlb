// Package unaccent is the unaccent extension: strip diacritics before
// comparing or indexing text. Needs CREATE EXTENSION unaccent.
package unaccent

import (
	"github.com/ubgo/sqlb"
	"github.com/ubgo/sqlb/pg"
)

const construct = "unaccent"

// Unaccent renders unaccent(e). It is STABLE, not IMMUTABLE, so it cannot
// sit in an expression index directly; wrap it in your own IMMUTABLE SQL
// function for that.
func Unaccent(e sqlb.Expr[string]) sqlb.Expr[string] {
	return pg.Func[string](construct+".Unaccent", "unaccent", e)
}

// WithDictionary renders unaccent(dict, e) for a custom rules dictionary.
func WithDictionary(dict string, e sqlb.Expr[string]) sqlb.Expr[string] {
	return pg.Expr[string](construct+".WithDictionary", func(a *sqlb.Args) string {
		return "unaccent(" + pg.QuoteLit(dict).SQL(a) + "::regdictionary, " + e.SQL(a) + ")"
	})
}
