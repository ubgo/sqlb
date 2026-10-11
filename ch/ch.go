// Package ch holds the ClickHouse dialect and what only ClickHouse has:
// PREWHERE, FINAL, SAMPLE, ARRAY JOIN, LIMIT BY, SETTINGS, the argMax and
// uniq aggregates, the -If combinator and the toStartOf* time functions.
//
// Four ClickHouse rules a library author must know are in the builder spec:
// no transactions, LEFT JOIN misses give defaults not NULL under the default
// join_use_nulls, UPDATE and DELETE are asynchronous mutations, and inserts
// are batches.
package ch

import (
	"errors"
	"github.com/ubgo/sqlb/dialect/clickhouse"
	"strconv"
	"strings"
	"time"

	"github.com/ubgo/sqlb"
)

// DialectClickHouse is the engine name reported by the dialect. It is an
// alias for the core constant, kept so an engine guard in this package reads
// without an extra import.
const DialectClickHouse = sqlb.DialectClickHouse

// Dialect is sqlb.ClickHouseDialect.
//
// The dialect lives in the core beside PostgresDialect and SQLiteDialect, so
// all three are in one place and a caller naming an engine does not have to
// import that engine's construct package. This alias exists so ch.Dialect{}
// still reads naturally next to ch.Final() and the rest.
type Dialect = clickhouse.Dialect

// Is reports whether d is the ClickHouse dialect.
func Is(d sqlb.Dialect) bool { return sqlb.DialectName(d) == DialectClickHouse }

// Construct names for ErrUnsupportedDialect messages.
const (
	constructPrewhere  = "ch.Prewhere"
	constructFinal     = "ch.Final"
	constructSample    = "ch.Sample"
	constructArrayJoin = "ch.ArrayJoin"
	constructLimitBy   = "ch.LimitBy"
	constructSettings  = "ch.Settings"
	constructArgMax    = "ch.ArgMax"
	constructUniq      = "ch.Uniq"
	constructUniqExact = "ch.UniqExact"
	constructSumIf     = "ch.SumIf"
	constructCountIf   = "ch.CountIf"
	constructCount     = "ch.Count"
	constructToStart   = "ch.ToStartOf"
)

// ClickHouse keywords and function names.
const (
	kwPrewhere  = "PREWHERE"
	kwFinal     = "FINAL"
	kwSample    = "SAMPLE"
	kwArrayJoin = "ARRAY JOIN"
	kwLimit     = "LIMIT"
	kwBy        = "BY"
	kwSettings  = "SETTINGS"
	kwAs        = "AS"

	fnArgMax        = "argMax"
	fnUniq          = "uniq"
	fnUniqExact     = "uniqExact"
	fnSumIf         = "sumIf"
	fnCountIf       = "countIf"
	fnCount         = "count"
	fnToStartOfHour = "toStartOfHour"
	fnToStartOfDay  = "toStartOfDay"
)

// Setting names. Named because a typo in a setting is silently ignored by
// the server, which is the worst failure mode.
const (
	SettingJoinUseNulls = "join_use_nulls"
	SettingMaxThreads   = "max_threads"
)

// ErrEmptySetting is recorded for SETTINGS with no settings, or a Setting{}
// with no name: Setting has exported fields and is written as a literal, and
// either rendered SQL ClickHouse rejects.
var ErrEmptySetting = errors.New("sqlb/ch: SETTINGS needs at least one named setting")

// Setting is one SETTINGS name = value pair. A slice keeps render order
// deterministic; a map would not.
type Setting struct {
	Name  string
	Value any
}

// The guard rule lives in the core, so pg, sqlite, ch and any package outside
// this module share one implementation.
func guard(a *sqlb.Args, construct string) bool {
	return sqlb.EngineGuard(a, sqlb.DialectClickHouse, construct)
}

// SelectClause is a clause for a SELECT on ClickHouse: an alias for the core's
// typed clause, so a statement of another dialect refuses it at compile time.
// ClickHouse constructs here are all SELECT clauses.
type SelectClause = sqlb.Clause[Dialect, sqlb.SelectKind]

func selectClause(slot sqlb.Slot, construct string, render func(a *sqlb.Args) string) SelectClause {
	return sqlb.EngineClause[Dialect, sqlb.SelectKind](construct, slot, render)
}

// Prewhere renders PREWHERE (preds) before WHERE. ClickHouse reads only the
// PREWHERE columns first and filters before reading the rest, which is the
// cheapest filter position for a wide table.
func Prewhere(preds ...sqlb.Predicate) SelectClause {
	return selectClause(sqlb.SlotBeforeWhere, constructPrewhere, func(a *sqlb.Args) string {
		return kwPrewhere + " " + sqlb.PredicateSQL(sqlb.And(preds...), a)
	})
}

// FINAL and SAMPLE are per table reference, not per statement. On the driving
// table the clause goes in the statement; on a joined table it goes in that
// join through sqlb.Join.Clauses. Putting a joined table's clause in the
// statement applies it to the driving table instead, which is a different
// query that still runs.

// Final renders FINAL after the driving table, forcing a ReplacingMergeTree
// to collapse rows at read time.
func Final() SelectClause {
	return selectClause(sqlb.SlotAfterFrom, constructFinal, func(*sqlb.Args) string {
		return kwFinal
	})
}

// Sample renders SAMPLE ratio, for approximate queries over a sampled table.
func Sample(ratio float64) SelectClause {
	return selectClause(sqlb.SlotAfterFrom, constructSample, func(*sqlb.Args) string {
		return kwSample + " " + strconv.FormatFloat(ratio, 'g', -1, 64)
	})
}

// ArrayJoin renders ARRAY JOIN col AS alias and returns the element
// expression, typed T, in the same call so the element type is checked.
func ArrayJoin[T any](col sqlb.Expr[[]T], alias string) (SelectClause, sqlb.Expr[T]) {
	c := selectClause(sqlb.SlotAfterFrom, constructArrayJoin, func(a *sqlb.Args) string {
		return kwArrayJoin + " " + col.SQL(a) + " " + kwAs + " " + a.Dialect().QuoteIdent(alias)
	})
	elem := sqlb.New[T](func(a *sqlb.Args) string { return a.Dialect().QuoteIdent(alias) })
	return c, elem
}

// LimitBy renders LIMIT n BY exprs after ORDER BY: the first n rows per group,
// which is how "latest event per user" is written without a window function.
func LimitBy(n int, exprs ...sqlb.SQLer) SelectClause {
	return selectClause(sqlb.SlotAfterOrder, constructLimitBy, func(a *sqlb.Args) string {
		parts := make([]string, len(exprs))
		for i, e := range exprs {
			parts[i] = e.SQL(a)
		}
		return kwLimit + " " + a.Next(n) + " " + kwBy + " " + strings.Join(parts, ", ")
	})
}

// Settings renders SETTINGS name = value, ... at the tail. Values are bound.
func Settings(settings ...Setting) SelectClause {
	return selectClause(sqlb.SlotTail, constructSettings, func(a *sqlb.Args) string {
		if len(settings) == 0 {
			a.Fail(ErrEmptySetting)
			return ""
		}
		parts := make([]string, len(settings))
		for i, s := range settings {
			if s.Name == "" {
				a.Fail(ErrEmptySetting)
				return ""
			}
			parts[i] = s.Name + " = " + a.Next(s.Value)
		}
		return kwSettings + " " + strings.Join(parts, ", ")
	})
}

// JoinUseNulls is the setting that makes a LEFT JOIN miss a NULL instead of a
// default value, so Nullable[T] scans as intended.
func JoinUseNulls() Setting { return Setting{Name: SettingJoinUseNulls, Value: 1} }

func fn1[T any](construct, name string, arg sqlb.SQLer) sqlb.Expr[T] {
	return sqlb.New[T](func(a *sqlb.Args) string {
		if !guard(a, construct) {
			return ""
		}
		return name + "(" + arg.SQL(a) + ")"
	})
}

// ArgMax renders argMax(value, by): the value from the row where by is
// largest. Typed as the value's type.
func ArgMax[T, U any](value sqlb.Expr[T], by sqlb.Expr[U]) sqlb.Expr[T] {
	return sqlb.New[T](func(a *sqlb.Args) string {
		if !guard(a, constructArgMax) {
			return ""
		}
		return fnArgMax + "(" + value.SQL(a) + ", " + by.SQL(a) + ")"
	})
}

// Uniq renders uniq(e), the approximate distinct count.
func Uniq[T any](e sqlb.Expr[T]) sqlb.Expr[uint64] { return fn1[uint64](constructUniq, fnUniq, e) }

// UniqExact renders uniqExact(e), the exact distinct count.
func UniqExact[T any](e sqlb.Expr[T]) sqlb.Expr[uint64] {
	return fn1[uint64](constructUniqExact, fnUniqExact, e)
}

// SumIf renders sumIf(e, cond), the -If combinator on sum.
func SumIf[T any](e sqlb.Expr[T], cond sqlb.Predicate) sqlb.Expr[T] {
	return sqlb.New[T](func(a *sqlb.Args) string {
		if !guard(a, constructSumIf) {
			return ""
		}
		return fnSumIf + "(" + e.SQL(a) + ", " + sqlb.PredicateSQL(cond, a) + ")"
	})
}

// CountIf renders countIf(cond).
func CountIf(cond sqlb.Predicate) sqlb.Expr[uint64] {
	return sqlb.New[uint64](func(a *sqlb.Args) string {
		if !guard(a, constructCountIf) {
			return ""
		}
		return fnCountIf + "(" + sqlb.PredicateSQL(cond, a) + ")"
	})
}

// Count renders count().
func Count() sqlb.Expr[uint64] {
	return sqlb.New[uint64](func(a *sqlb.Args) string {
		if !guard(a, constructCount) {
			return ""
		}
		return fnCount + "()"
	})
}

// ToStartOfHour truncates a time to the hour. Typed time.Time in and out, so
// a bucket cannot be compared with a string.
func ToStartOfHour(e sqlb.Expr[time.Time]) sqlb.Expr[time.Time] {
	return fn1[time.Time](constructToStart, fnToStartOfHour, e)
}

// ToStartOfDay truncates a time to the day.
func ToStartOfDay(e sqlb.Expr[time.Time]) sqlb.Expr[time.Time] {
	return fn1[time.Time](constructToStart, fnToStartOfDay, e)
}
