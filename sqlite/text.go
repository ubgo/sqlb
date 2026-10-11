package sqlite

import "github.com/ubgo/sqlb"

// Glob renders e GLOB $n, Unix shell wildcard matching. It is always
// case-sensitive, where LIKE is case-insensitive for ASCII by default, which
// is the usual reason to reach for it.
func Glob(e sqlb.Expr[string], pattern string) sqlb.Predicate {
	return binpred(constructText, "GLOB", e, sqlb.Param(pattern))
}

// NotGlob renders e NOT GLOB $n.
func NotGlob(e sqlb.Expr[string], pattern string) sqlb.Predicate {
	return binpred(constructText, "NOT GLOB", e, sqlb.Param(pattern))
}

// Match renders e MATCH $n, the full-text search operator.
//
// It works only against an FTS5 or FTS4 virtual table, and the left operand
// is the table name rather than a column when searching every column. A MATCH
// against an ordinary table is an error at run time, not at Build: the
// builder cannot know which tables are virtual.
func Match(e sqlb.Expr[string], query string) sqlb.Predicate {
	return binpred(constructText, "MATCH", e, sqlb.Param(query))
}

// Rank references the FTS5 rank column, which is the relevance of the current
// match. It is negative, and more negative is better, so ordering by it
// ascending puts the best match first.
func Rank(table sqlb.Table) sqlb.Expr[float64] {
	return expr[float64](constructText, func(a *sqlb.Args) string {
		return table.Qualifier(a.Dialect()) + "." + a.Dialect().QuoteIdent("rank")
	})
}

// BM25 renders bm25(table), the FTS5 relevance score, optionally weighted per
// column. Like Rank it is negative and smaller is better.
func BM25(table sqlb.Table, weights ...float64) sqlb.Expr[float64] {
	return expr[float64](constructText, func(a *sqlb.Args) string {
		out := a.Dialect().QuoteIdent(table.Name)
		for _, w := range weights {
			out += ", " + a.Next(w)
		}
		return "bm25(" + out + ")"
	})
}

// Highlight renders highlight(table, column, openTag, closeTag), the matched
// text with markers around the matching terms. The column is an index,
// 0-based, counting the FTS5 table's own columns.
func Highlight(table sqlb.Table, column int, openTag, closeTag string) sqlb.Expr[string] {
	return expr[string](constructText, func(a *sqlb.Args) string {
		return "highlight(" + a.Dialect().QuoteIdent(table.Name) + ", " +
			a.Next(column) + ", " + a.Next(openTag) + ", " + a.Next(closeTag) + ")"
	})
}

// Snippet renders snippet(table, column, open, close, ellipsis, tokens), a
// short extract around the match rather than the whole column.
func Snippet(table sqlb.Table, column int, openTag, closeTag, ellipsis string, tokens int) sqlb.Expr[string] {
	return expr[string](constructText, func(a *sqlb.Args) string {
		return "snippet(" + a.Dialect().QuoteIdent(table.Name) + ", " +
			a.Next(column) + ", " + a.Next(openTag) + ", " + a.Next(closeTag) + ", " +
			a.Next(ellipsis) + ", " + a.Next(tokens) + ")"
	})
}

// Instr renders instr(haystack, needle): the 1-based position, or 0 when
// absent. SQLite's spelling of what Postgres calls strpos.
func Instr(haystack sqlb.Expr[string], needle string) sqlb.Expr[int32] {
	return fn[int32](constructText, "instr", haystack, sqlb.Param(needle))
}

// Hex renders hex(e), the uppercase hexadecimal of a blob or text.
func Hex(e sqlb.SQLer) sqlb.Expr[string] { return fn[string](constructText, "hex", e) }

// QuoteSQL renders quote(e), the value as a SQL literal. For building a
// statement inside SQLite itself; the builder quotes its own values.
func QuoteSQL(e sqlb.SQLer) sqlb.Expr[string] { return fn[string](constructText, "quote", e) }

// Typeof renders typeof(e): "null", "integer", "real", "text" or "blob".
//
// SQLite columns have affinity rather than a type, so one column can hold
// values of several storage classes. This is how a query finds out which,
// and there is no equivalent in an engine with real column types.
func Typeof(e sqlb.SQLer) sqlb.Expr[string] { return fn[string](constructText, "typeof", e) }

// IfNull renders ifnull(a, b), COALESCE with exactly two arguments. The core
// has Coalesce, which every engine spells the same; this is here because the
// name is not universal.
func IfNull[T any](a, b sqlb.Expr[T]) sqlb.Expr[T] {
	return fn[T](constructText, "ifnull", a, b)
}

// IIF renders iif(cond, then, else) (3.32), the expression form of CASE.
func IIF[T any](cond sqlb.Predicate, then, els sqlb.Expr[T]) sqlb.Expr[T] {
	return expr[T](constructText, func(a *sqlb.Args) string {
		return "iif(" + sqlb.PredicateSQL(cond, a) + ", " + then.SQL(a) + ", " + els.SQL(a) + ")"
	})
}

// Format renders format(fmt, args...) (3.38), printf-style formatting. The
// format string is an author constant.
func Format(format string, args ...sqlb.SQLer) sqlb.Expr[string] {
	all := append([]sqlb.SQLer{quoteLit(format)}, args...)
	return fn[string](constructText, "format", all...)
}

// GroupConcat renders group_concat(e, sep). SQLite's string_agg, and not
// universal: Postgres spells it string_agg and the separator argument is
// optional here but not there.
func GroupConcat(e sqlb.Expr[string], sep string, opts ...sqlb.AggOption) sqlb.Expr[string] {
	return expr[string](constructAgg, sqlb.Aggregate[string]("group_concat", []sqlb.SQLer{e, sqlb.Param(sep)}, opts...).SQL)
}

// Length renders length(e), the number of characters in a text value, which
// is what a CHECK on a column's length means. It is here rather than in the
// core because MySQL's length() counts bytes. It is int32, as Instr is, so
// the two compose in Substr.
func Length(e sqlb.Expr[string]) sqlb.Expr[int32] {
	return fn[int32](constructText, "length", e)
}

// Substr renders substr(e, start), the text from the 1-based position start
// to the end.
func Substr(e sqlb.Expr[string], start sqlb.Expr[int32]) sqlb.Expr[string] {
	return fn[string](constructText, "substr", e, start)
}

// SubstrN renders substr(e, start, n), at most n characters from start.
func SubstrN(e sqlb.Expr[string], start, n sqlb.Expr[int32]) sqlb.Expr[string] {
	return fn[string](constructText, "substr", e, start, n)
}

// IsNot renders l IS NOT r, SQLite's null-safe inequality: true when exactly
// one side is NULL, where <> would be NULL. It is what a trigger's WHEN
// wants, OLD.status IS NOT NEW.status firing when a status becomes NULL.
func IsNot[T any](l, r sqlb.Expr[T]) sqlb.Predicate {
	return binpred(constructText, "IS NOT", l, r)
}

// IsEqual renders l IS r, SQLite's null-safe equality: two NULLs are equal.
// It is not Is, which reports whether a dialect is SQLite.
func IsEqual[T any](l, r sqlb.Expr[T]) sqlb.Predicate {
	return binpred(constructText, "IS", l, r)
}
