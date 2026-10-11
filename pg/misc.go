package pg

import (
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/ubgo/sqlb"
)

const (
	constructMisc = "pg.misc"

	// kwIntervalLit is the type name an interval literal carries; the text
	// beside it is the caller's and goes through the dialect's quoting.
	kwIntervalLit = "interval"
)

// IntervalLit renders the literal interval 'text'. An author constant such as
// "7 days"; a request-controlled interval goes through sqlb.Param with a cast.
//
// The text is written by the dialect's literal quoting rather than by doubling
// the quote here. The difference is not cosmetic: with
// standard_conforming_strings off, Postgres reads a backslash in a plain
// literal as an escape, so doubling the quote alone leaves a string holding
// one backslash meaning something else and a lone backslash unterminated. The
// dialect knows which form reads the same under both settings, and it fails
// the build rather than guessing for an engine that has no rule.
func IntervalLit(text string) sqlb.Expr[Interval] {
	return expr[Interval](constructMisc, func(a *sqlb.Args) string { return kwIntervalLit + " " + a.Literal(text) })
}

// AddIntervalExpr renders (e + iv) for an interval expression, so a literal
// interval and a bound one use the same form.
func AddIntervalExpr(e sqlb.Expr[time.Time], iv sqlb.Expr[Interval]) sqlb.Expr[time.Time] {
	return binop[time.Time](constructMisc, "+", e, iv)
}

// SubIntervalExpr renders e - iv with an interval expression.
func SubIntervalExpr(e sqlb.Expr[time.Time], iv sqlb.Expr[Interval]) sqlb.Expr[time.Time] {
	return binop[time.Time](constructMisc, "-", e, iv)
}

// ArrayOf renders ARRAY[e1, e2, ...] from expressions, typed by the element.
func ArrayOf[T any](items ...sqlb.Expr[T]) sqlb.Expr[[]T] {
	return expr[[]T](constructArray, func(a *sqlb.Args) string {
		out := make([]string, len(items))
		for i, it := range items {
			out[i] = it.SQL(a)
		}
		return "ARRAY[" + strings.Join(out, ", ") + "]"
	})
}

// AnyExpr renders left op ANY(array) and AllExpr left op ALL(array), for an
// array expression such as a path column, where Any and AllOp take a value.
func AnyExpr[T any](left sqlb.Expr[T], op CompareOp, arr sqlb.Expr[[]T]) sqlb.Predicate {
	return pred(constructArray, func(a *sqlb.Args) string {
		return left.SQL(a) + " " + closedWord(a, op, op.valid(), "comparison operator") + " ANY(" + arr.SQL(a) + ")"
	})
}

// AllExpr renders left op ALL(array): the comparison holds against every
// element. An empty array makes it true, which is the opposite of AnyExpr.
func AllExpr[T any](left sqlb.Expr[T], op CompareOp, arr sqlb.Expr[[]T]) sqlb.Predicate {
	return pred(constructArray, func(a *sqlb.Args) string {
		return left.SQL(a) + " " + closedWord(a, op, op.valid(), "comparison operator") + " ALL(" + arr.SQL(a) + ")"
	})
}

// GenerateSeriesTime renders generate_series($from, $to, interval) AS alias (ts)
// over timestamps, with the step an author constant.
func GenerateSeriesTime(from, to time.Time, step string, alias string) (sqlb.Source, sqlb.Expr[time.Time]) {
	call := expr[time.Time](constructArray, func(a *sqlb.Args) string {
		return fnGenSeries + "(" + a.Next(from) + ", " + a.Next(to) + ", " + IntervalLit(step).SQL(a) + ")"
	})
	src := FuncSource(call, alias, "ts")
	return src, sqlb.Col[time.Time](sqlb.Ref(src), "ts")
}

// GenerateSeriesExpr renders generate_series(from, to, step) from expressions.
func GenerateSeriesExpr(from, to sqlb.SQLer, step sqlb.Expr[Interval], alias string) (sqlb.Source, sqlb.Expr[time.Time]) {
	call := expr[time.Time](constructArray, func(a *sqlb.Args) string {
		return fnGenSeries + "(" + from.SQL(a) + ", " + to.SQL(a) + ", " + step.SQL(a) + ")"
	})
	src := FuncSource(call, alias, "ts")
	return src, sqlb.Col[time.Time](sqlb.Ref(src), "ts")
}

// ErrIncompleteSpec is returned for a caller-written specification with a
// required part empty: a RecordSet or JSON_TABLE column list with no columns,
// a column with no name or type, a NESTED PATH with no path, a JSON object
// pair with no value. Those types have exported fields and are written as
// literals, so the zero value is the easy mistake; before this, a pair with no
// value panicked inside Build and an empty column rendered "" for the server
// to reject.
var ErrIncompleteSpec = errors.New("sqlb/pg: a column, column list or JSON pair is missing a required part")

// ColumnDef is one typed column of a record-set alias: AS x(sku text, ...).
type ColumnDef struct {
	Name string
	Type string
}

// RecordSet makes a function call a source with a typed column list, which
// jsonb_to_recordset and crosstab need. The types are author constants.
func RecordSet(call sqlb.SQLer, alias string, cols ...ColumnDef) sqlb.Source {
	return sqlb.SourceOf(alias, func(a *sqlb.Args) string {
		if !guard(a, constructSelect) {
			return ""
		}
		d := a.Dialect()
		if len(cols) == 0 {
			a.Fail(ErrIncompleteSpec)
			return ""
		}
		parts := make([]string, len(cols))
		for i, c := range cols {
			if c.Name == "" || c.Type == "" {
				a.Fail(ErrIncompleteSpec)
				return ""
			}
			parts[i] = d.QuoteIdent(c.Name) + " " + c.Type
		}
		return call.SQL(a) + " AS " + d.QuoteIdent(alias) + " (" + strings.Join(parts, ", ") + ")"
	})
}

// PathQueryVars renders jsonb_path_query(col, path, vars) AS alias (value).
func PathQueryVars(col sqlb.SQLer, jsonpath string, vars []byte, alias string) (sqlb.Source, sqlb.Expr[[]byte]) {
	call := sqlb.Raw(func(a *sqlb.Args) string {
		if !guard(a, constructJSON) {
			return ""
		}
		return fnJSONBPathQuery + "(" + col.SQL(a) + ", " + a.Next(jsonpath) + "::jsonpath, " + a.Next(vars) + "::jsonb)"
	})
	src := FuncSource(call, alias, "value")
	return src, sqlb.Col[[]byte](sqlb.Ref(src), "value")
}

// JSONBSetExpr is JSONBSet with the new value as an expression, for a
// subquery that rebuilds an array.
func JSONBSetExpr(col sqlb.SQLer, path []string, value sqlb.SQLer, createMissing bool) sqlb.Expr[json.RawMessage] {
	return expr[json.RawMessage](constructJSON, func(a *sqlb.Args) string {
		return fnJSONBSet + "(" + col.SQL(a) + ", " + a.Next(path) + ", " + value.SQL(a) + ", " + a.Next(createMissing) + ")"
	})
}
