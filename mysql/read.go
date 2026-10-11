package mysql

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/ubgo/sqlb"
)

const (
	kwForUpdate  = "FOR UPDATE"
	kwForShare   = "FOR SHARE"
	kwWithRollup = "WITH ROLLUP"
	kwOf         = "OF"
	kwSeparator  = "SEPARATOR"
)

// LockOption is what a locking read does when a row is already locked.
type LockOption int

// The lock options. NoWait fails at once; SkipLocked leaves the row out, which
// is how a job queue hands each worker a different row.
const (
	NoWait LockOption = iota + 1
	SkipLocked
)

func (o LockOption) keyword() string {
	switch o {
	case NoWait:
		return "NOWAIT"
	case SkipLocked:
		return "SKIP LOCKED"
	}
	return ""
}

func lock(kw string, opt []LockOption, of []sqlb.Table) SelectClause {
	return clause[sqlb.SelectKind](sqlb.SlotTail, constructRead, func(a *sqlb.Args) string {
		s := kw
		if len(of) > 0 {
			names := make([]string, len(of))
			for i, t := range of {
				names[i] = t.Qualifier(a.Dialect())
			}
			s += " " + kwOf + " " + strings.Join(names, ", ")
		}
		for _, o := range opt {
			kw := o.keyword()
			if kw == "" {
				a.Fail(fmt.Errorf("sqlb/mysql: lock option %d outside its constants", o))
				return ""
			}
			s += " " + kw
		}
		return s
	})
}

// ForUpdate locks the rows read for writing until the transaction ends.
func ForUpdate(opts ...LockOption) SelectClause { return lock(kwForUpdate, opts, nil) }

// ForUpdateOf locks only the rows of the named tables in a join.
func ForUpdateOf(t sqlb.Table, opts ...LockOption) SelectClause {
	return lock(kwForUpdate, opts, []sqlb.Table{t})
}

// ForShare locks the rows read against writes by others.
func ForShare(opts ...LockOption) SelectClause { return lock(kwForShare, opts, nil) }

// WithRollup adds the super-aggregate rows to a GROUP BY: one per level, with
// the grouping column NULL. MySQL writes it as a suffix of GROUP BY where
// Postgres writes ROLLUP(...) inside it, which is why each engine has its own.
func WithRollup() SelectClause {
	return clause[sqlb.SelectKind](sqlb.SlotAfterGroupBy, constructRead, func(*sqlb.Args) string { return kwWithRollup })
}

// GroupConcat renders group_concat(e SEPARATOR sep), the aggregate that joins a
// group's values into one string. The separator is written as a literal,
// because MySQL takes no parameter there, so it is an author's constant and a
// backslash in it is refused. The result is truncated silently at
// group_concat_max_len, 1024 bytes by default, which is the thing to know.
func GroupConcat(e sqlb.Expr[string], sep string) sqlb.Expr[string] {
	return expr[string](constructRead, func(a *sqlb.Args) string {
		return "group_concat(" + e.SQL(a) + " " + kwSeparator + " " + a.Literal(sep) + ")"
	})
}

// IfNull renders ifnull(e, fallback).
func IfNull[T any](e sqlb.Expr[*T], fallback sqlb.Expr[T]) sqlb.Expr[T] {
	return fn[T](constructRead, "ifnull", e, fallback)
}

// UTCNow renders utc_timestamp(6), the current instant in UTC to the
// microsecond, which is what a DATETIME(6) written and read in UTC wants;
// now() is in the session's time zone.
func UTCNow() sqlb.Expr[time.Time] {
	return expr[time.Time](constructTime, func(*sqlb.Args) string { return "utc_timestamp(6)" })
}

// UUID renders uuid(), a version 1 UUID as text.
func UUID() sqlb.Expr[string] {
	return expr[string](constructText, func(*sqlb.Args) string { return "uuid()" })
}

// RegexpLike renders regexp_like(e, pattern), a regular-expression match.
func RegexpLike(e sqlb.Expr[string], pattern string) sqlb.Predicate {
	return pred(constructText, func(a *sqlb.Args) string {
		return "regexp_like(" + e.SQL(a) + ", " + a.Next(pattern) + ")"
	})
}

// CharLength renders char_length(e), the length in characters. MySQL's
// length() counts bytes, which is why a length check uses this.
func CharLength(e sqlb.Expr[string]) sqlb.Expr[int64] {
	return fn[int64](constructText, "char_length", e)
}

// JSONExtract renders json_extract(col, path), the JSON value at a path.
func JSONExtract(col sqlb.Expr[json.RawMessage], path string) sqlb.Expr[json.RawMessage] {
	return expr[json.RawMessage](constructJSON, func(a *sqlb.Args) string {
		return "json_extract(" + col.SQL(a) + ", " + a.Next(path) + ")"
	})
}

// JSONValue renders json_unquote(json_extract(col, path)), the value at a path
// as text, NULL when the path is absent: the ->> operator, written as the
// functions it abbreviates so a bound path is allowed.
func JSONValue(col sqlb.Expr[json.RawMessage], path string) sqlb.Expr[*string] {
	return expr[*string](constructJSON, func(a *sqlb.Args) string {
		return "json_unquote(json_extract(" + col.SQL(a) + ", " + a.Next(path) + "))"
	})
}

// JSONContains renders json_contains(col, candidate), whether the document
// contains the candidate, which must be valid JSON.
func JSONContains(col sqlb.Expr[json.RawMessage], candidate json.RawMessage) sqlb.Predicate {
	return pred(constructJSON, func(a *sqlb.Args) string {
		return "json_contains(" + col.SQL(a) + ", " + a.Next(string(candidate)) + ")"
	})
}

// MatchMode is how a full-text search reads its query.
type MatchMode int

// The modes. NaturalLanguage ranks by relevance; Boolean reads +, - and *
// operators in the query.
const (
	NaturalLanguage MatchMode = iota + 1
	Boolean
)

func (m MatchMode) keyword() string {
	switch m {
	case NaturalLanguage:
		return "IN NATURAL LANGUAGE MODE"
	case Boolean:
		return "IN BOOLEAN MODE"
	}
	return ""
}

// Match renders MATCH(cols) AGAINST(query IN mode), a full-text search over
// columns with a FULLTEXT index, which the columns must be exactly. It is a
// relevance score, so it is both a filter and an ORDER BY term.
func Match(query string, mode MatchMode, first sqlb.Expr[string], more ...sqlb.Expr[string]) sqlb.Expr[float64] {
	cols := append([]sqlb.Expr[string]{first}, more...)
	return expr[float64](constructText, func(a *sqlb.Args) string {
		kw := mode.keyword()
		if kw == "" {
			a.Fail(fmt.Errorf("sqlb/mysql: match mode %d outside its constants", mode))
			return ""
		}
		names := make([]string, len(cols))
		for i, c := range cols {
			names[i] = c.SQL(a)
		}
		return "MATCH(" + strings.Join(names, ", ") + ") AGAINST(" + a.Next(query) + " " + kw + ")"
	})
}

// integerKind is the Go types DIV divides.
type integerKind interface {
	~int | ~int8 | ~int16 | ~int32 | ~int64 | ~uint | ~uint8 | ~uint16 | ~uint32 | ~uint64
}

// IntDiv renders l DIV r, integer division. MySQL's / always returns a
// decimal, so 7 / 2 is 3.5000 there, where Postgres and SQLite truncate an
// integer division; DIV is how MySQL says the other thing.
func IntDiv[T integerKind](l, r sqlb.Expr[T]) sqlb.Expr[T] {
	return binop[T](constructRead, "DIV", l, r)
}

// NullSafeEq renders l <=> r, MySQL's null-safe equality: true when both are
// NULL, where = would be NULL. It is the MySQL spelling of IS NOT DISTINCT
// FROM, which MySQL does not have.
func NullSafeEq[T any](l, r sqlb.Expr[T]) sqlb.Predicate {
	return binpred(constructRead, "<=>", l, r)
}
