package pg

import "github.com/ubgo/sqlb"

const (
	constructRecursive = "pg.recursive"

	kwSearchDepthFirst   = "SEARCH DEPTH FIRST BY"
	kwSearchBreadthFirst = "SEARCH BREADTH FIRST BY"
	kwSet                = "SET"
	kwCycle              = "CYCLE"
	kwUsingPath          = "USING"
)

// SearchDepthFirst is a recursive-CTE suffix (PG14): SEARCH DEPTH FIRST BY
// cols SET ordercol. Sorting by ordercol yields depth-first order.
func SearchDepthFirst(setCol string, by ...string) sqlb.SQLer {
	return search(kwSearchDepthFirst, setCol, by)
}

// SearchBreadthFirst is the breadth-first form.
func SearchBreadthFirst(setCol string, by ...string) sqlb.SQLer {
	return search(kwSearchBreadthFirst, setCol, by)
}

func search(kw, setCol string, by []string) sqlb.SQLer {
	return sqlb.Raw(func(a *sqlb.Args) string {
		if !guard(a, constructRecursive) {
			return ""
		}
		d := a.Dialect()
		return kw + " " + sqlb.QuoteList(d, by) + " " + kwSet + " " + d.QuoteIdent(setCol)
	})
}

// Cycle is a recursive-CTE suffix (PG14): CYCLE cols SET flagcol USING
// pathcol. Recursion stops at the repeated row and flags it.
func Cycle(flagCol, pathCol string, cols ...string) sqlb.SQLer {
	return sqlb.Raw(func(a *sqlb.Args) string {
		if !guard(a, constructRecursive) {
			return ""
		}
		d := a.Dialect()
		return kwCycle + " " + sqlb.QuoteList(d, cols) + " " + kwSet + " " + d.QuoteIdent(flagCol) + " " + kwUsingPath + " " + d.QuoteIdent(pathCol)
	})
}

// Both renders "search cycle" so one CTE.Suffix carries both clauses.
//
// Deliberately unguarded: it renders nothing of its own beyond a space, and
// both operands are already guarded constructors, so the dialect error comes
// from whichever of them renders first.
func Both(first, second sqlb.SQLer) sqlb.SQLer {
	return sqlb.Raw(func(a *sqlb.Args) string { return first.SQL(a) + " " + second.SQL(a) })
}
