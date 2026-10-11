package expr

import (
	"strings"

	"github.com/ubgo/sqlb/render"
)

// The case-insensitive predicates: the same questions Eq, In, NotIn,
// Contains, StartsWith and EndsWith ask, answered regardless of letter case,
// with one spelling per engine chosen by the dialect rather than by the caller.
//
// They exist because the engines disagree about case and the disagreement is
// silent. Measured on 2026-09-28: 'Ada' LIKE 'ada%' is false on Postgres 17,
// true on SQLite, and true on MySQL 8.4 under its default collation,
// utf8mb4_0900_ai_ci, which also makes a plain 'café' = 'cafe' true. A library
// that writes Eq or Contains therefore returns different rows on different
// engines for the same data, and a caller who wants "ignore case" has to know
// which engine they are on to spell it. These say what is meant, once.
//
// The spelling:
//
//   - Eq and membership write lower(column) = lower(value) on every engine,
//     because that is the expression an index on lower(column) serves, which is
//     how a case-insensitive unique key is declared on all three.
//   - The pattern forms write column ILIKE pattern on a dialect implementing
//     render.CaseInsensitiveLike, which is Postgres, and lower(column) LIKE
//     lower(pattern) elsewhere. Both escape the value and write ESCAPE '!', as
//     Contains does, so a typed % or _ still means itself.
//
// Both sides are folded by the engine's own lower(), never by Go's
// strings.ToLower. The value and the column must be folded by the same rule or
// a match depends on which rule reached which side: SQLite's lower() folds ASCII
// only, so a value lowered in Go would fold 'É' while the column kept it, and an
// exact copy of a stored value would stop matching itself.
//
// Two limits, both the engine's and neither fixable here:
//
//   - Beyond ASCII the answer is the engine's collation. Measured: SQLite's
//     built-in lower() folds ASCII only, so 'ÉCOLE' does not match 'école';
//     Postgres folds by the column's collation, Unicode under en_US.UTF-8 and
//     ASCII only under "C"; MySQL folds Unicode. ASCII answers are identical on
//     every engine, and sqlbtest's case_fold case holds them to that.
//   - Accents are the collation's too. Under MySQL's default ai_ci collation
//     lower('café') = lower('cafe') is true, because the comparison itself is
//     accent-insensitive; on a utf8mb4_bin column, as on Postgres and SQLite,
//     it is false. Folding case cannot make a collation accent-sensitive.
//
// And one cost: lower(column) is not the column, so a plain index on it does
// not serve these. Declare the index on lower(column) for EqFold and InFold, on
// every engine; on Postgres an ILIKE with a leading wildcard is served by a
// pg_trgm GIN index, which github.com/ubgo/sqlb/pg/trgm declares.

// opILike is Postgres's case-insensitive LIKE. Unexported because it is not
// universal SQL: it is written only for a dialect that declares
// render.CaseInsensitiveLike, and a caller never names it.
const opILike = "ILIKE"

// fnLower is the SQL lower() function, which every engine this module ships
// spells the same way; Lower writes it as well.
const fnLower = "lower"

// lowerSQL wraps rendered SQL in lower().
func lowerSQL(sql string) string { return fnLower + "(" + sql + ")" }

// foldPattern renders a case-insensitive LIKE of e against pattern, which the
// caller has already escaped with EscapeLike and given its wildcards.
func foldPattern(e Expr[string], pattern string) Predicate {
	return PredicateOf(func(a *render.Args) string {
		if !canEscape(a) {
			return ""
		}
		if c, ok := a.Dialect().(render.CaseInsensitiveLike); ok && c.SupportsILike() {
			return e.SQL(a) + sepSpace + opILike + sepSpace + a.Next(pattern) + escapeClause
		}
		return lowerSQL(e.SQL(a)) + sepSpace + OpLike + sepSpace + lowerSQL(a.Next(pattern)) + escapeClause
	})
}

// foldList renders lower(e) IN (lower(v1), ...), or its negation. An empty
// list renders FALSE for IN and TRUE for NOT IN, exactly as In and NotIn do, so
// a caller filtering by an empty set gets no rows rather than every row.
func foldList(e Expr[string], op string, values []string) Predicate {
	return PredicateOf(func(a *render.Args) string {
		if len(values) == 0 {
			if op == OpIn {
				return sqlFalse
			}
			return sqlTrue
		}
		var b strings.Builder
		b.WriteString(lowerSQL(e.SQL(a)))
		b.WriteString(sepSpace)
		b.WriteString(op)
		b.WriteString(" (")
		for i, v := range values {
			if i > 0 {
				b.WriteString(sepComma)
			}
			b.WriteString(lowerSQL(a.Next(v)))
		}
		b.WriteString(")")
		return b.String()
	})
}

// EqFold matches e against value regardless of case: lower(e) = lower(value)
// on every engine.
//
// The spelling is the same on Postgres, which has ILIKE, because ILIKE with no
// wildcard would still read a typed % or _ as one, and because lower(e) is the
// expression a case-insensitive unique index is declared on. Declare that index
// and this is an index lookup; with only a plain index on e, it is a scan.
//
// A NULL e matches nothing, on every engine, as with Eq.
func EqFold(e Expr[string], value string) Predicate {
	return PredicateOf(func(a *render.Args) string {
		return lowerSQL(e.SQL(a)) + sepSpace + OpEq + sepSpace + lowerSQL(a.Next(value))
	})
}

// InFold matches e against any of values regardless of case. An empty list
// renders FALSE, as In does.
func InFold(e Expr[string], values ...string) Predicate { return foldList(e, OpIn, values) }

// NotInFold matches e against none of values regardless of case. An empty list
// renders TRUE, as NotIn does. A NULL e matches nothing, which is SQL's rule
// for NOT IN and the same on every engine.
func NotInFold(e Expr[string], values ...string) Predicate { return foldList(e, OpNotIn, values) }

// ContainsFold is Contains regardless of case: the value is escaped, wrapped in
// %, and matched with ILIKE on Postgres and lower() LIKE lower() elsewhere.
//
// The spelling differs by engine and the answer does not, for ASCII; see the
// block comment above for the collation's say beyond it. Its negation is
// Not(ContainsFold(...)), which is NOT (e ILIKE ...) and means the same thing,
// NULL included, so there is no NotContainsFold.
func ContainsFold(e Expr[string], value string) Predicate {
	return foldPattern(e, "%"+EscapeLike(value)+"%")
}

// StartsWithFold is StartsWith regardless of case.
//
// Unlike StartsWith, a plain B-tree index on e does not serve it on any
// engine, because the column is wrapped or the operator is ILIKE. On SQLite and
// MySQL an index on lower(e) can serve the prefix; on Postgres a pg_trgm GIN
// index serves ILIKE.
func StartsWithFold(e Expr[string], value string) Predicate {
	return foldPattern(e, EscapeLike(value)+"%")
}

// EndsWithFold is EndsWith regardless of case.
func EndsWithFold(e Expr[string], value string) Predicate {
	return foldPattern(e, "%"+EscapeLike(value))
}
