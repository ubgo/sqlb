package expr

import (
	"errors"
	"strings"

	"github.com/ubgo/sqlb/render"
)

// The search predicates: LIKE with the caller's text treated as text.
//
// Like takes a pattern, which means every character the caller passes is
// pattern syntax. That is right for a pattern and wrong for a search box,
// because the % and _ in what somebody typed then mean "anything" rather than
// themselves. Searching for "50%" matches "50", "500" and "150x"; searching
// for "a_b" matches "axb". Nothing fails, the results are simply wrong, and
// the query looks correct in a log.
//
// These take a value rather than a pattern. They escape it, add the wildcards,
// and write an ESCAPE clause so the escaping means something.

// likeEscape is the character these predicates escape with. It is written out
// in an ESCAPE clause rather than relied on as a default.
//
// The default is not portable and the difference is silent. Postgres and MySQL
// treat a backslash in a pattern as an escape character; SQLite has no default
// escape character at all, so a backslash-escaped pattern matches nothing
// there unless ESCAPE says otherwise. That was verified against a real SQLite
// database: without the clause, a search that should return one row returned
// none.
//
// It is '!' rather than a backslash because the clause is statement text, and
// a backslash inside a MySQL string literal escapes the closing quote in the
// default sql_mode: ESCAPE '\' there is an unterminated string, a syntax
// error on every search. Measured on MySQL 8.4 under both settings of
// NO_BACKSLASH_ESCAPES; '!' is ordinary text in every engine's literal, so
// the clause reads the same everywhere and needs no quoting rule.
const likeEscape = `!`

// ErrNoLikeEscape is recorded when a dialect cannot express an ESCAPE clause.
//
// Refusing is the right failure. The alternative is to render the pattern
// without the clause and match the wrong rows, and a search that returns too
// much looks like it worked.
var ErrNoLikeEscape = errors.New("sqlb: dialect does not support ESCAPE on LIKE, so a user's text cannot be escaped safely")

// EscapeLike escapes the characters LIKE treats as syntax, so a value can be
// used inside a pattern the caller builds.
//
// Exported because Contains, StartsWith and EndsWith cover the three common
// shapes and not every shape. A caller assembling something else needs the
// same escaping and should not have to rediscover which characters matter.
// Doing so by hand also means writing the ESCAPE clause, which is what these
// predicates exist to stop you forgetting.
func EscapeLike(s string) string {
	// The escape character itself goes first: escaping it after the others
	// would double the ones they introduced.
	var b strings.Builder
	b.Grow(len(s))
	for _, r := range s {
		if r == '!' || r == '%' || r == '_' {
			b.WriteString(likeEscape)
		}
		b.WriteRune(r)
	}
	return b.String()
}

// escapeClause is the clause every escaping predicate appends, with its
// leading space. One constant for Contains and the fold predicates, so the two
// families cannot drift to different escape characters.
const escapeClause = sepSpace + "ESCAPE '" + likeEscape + "'"

// canEscape reports whether the dialect accepts ESCAPE on LIKE, recording
// ErrNoLikeEscape when it does not. Shared by every predicate that escapes a
// value, because the refusal is the whole safety argument and must not be
// restated per family.
func canEscape(a *render.Args) bool {
	if c, ok := a.Dialect().(render.LikeEscaping); ok && c.SupportsLikeEscape() {
		return true
	}
	a.Fail(ErrNoLikeEscape)
	return false
}

// escaped renders "e LIKE pattern ESCAPE '!'", or fails when the dialect
// cannot express the clause.
func escaped(e Expr[string], op, pattern string) Predicate {
	return PredicateOf(func(a *render.Args) string {
		if !canEscape(a) {
			return ""
		}
		return e.SQL(a) + sepSpace + op + sepSpace + a.Next(pattern) + escapeClause
	})
}

// Contains renders e LIKE '%value%' with value escaped, the predicate behind a
// search box.
func Contains(e Expr[string], value string) Predicate {
	return escaped(e, OpLike, "%"+EscapeLike(value)+"%")
}

// NotContains renders e NOT LIKE '%value%' with value escaped.
func NotContains(e Expr[string], value string) Predicate {
	return escaped(e, OpNotLike, "%"+EscapeLike(value)+"%")
}

// StartsWith renders e LIKE 'value%' with value escaped.
//
// Worth preferring to Contains where it fits: a prefix match can use a B-tree
// index on the column and a leading wildcard cannot, so on a table of any size
// the two differ by an index scan versus a sequential one.
func StartsWith(e Expr[string], value string) Predicate {
	return escaped(e, OpLike, EscapeLike(value)+"%")
}

// EndsWith renders e LIKE '%value' with value escaped.
func EndsWith(e Expr[string], value string) Predicate {
	return escaped(e, OpLike, "%"+EscapeLike(value))
}
