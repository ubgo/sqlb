package pg

import (
	"strings"

	"github.com/ubgo/sqlb"
)

const (
	constructSelect = "pg.select"

	kwNullsFirst      = "NULLS FIRST"
	kwNullsLast       = "NULLS LAST"
	kwLimitAll        = "LIMIT ALL"
	kwMaterialized    = "MATERIALIZED"
	kwNotMaterialized = "NOT MATERIALIZED"
	kwIntersectAll    = "INTERSECT ALL"
	kwExceptAll       = "EXCEPT ALL"
	kwLateral         = "LATERAL"
	kwValues          = "VALUES"
	kwWithOrdinality  = "WITH ORDINALITY"
	kwAny             = "ANY"
	kwAll             = "ALL"
)

// JoinFull is FULL OUTER JOIN. MySQL has none, so it is here.
const JoinFull sqlb.JoinType = "FULL JOIN"

// OrderByNulls makes an order term with NULLS FIRST or NULLS LAST.
func OrderByNulls[T any](e sqlb.Expr[T], dir sqlb.SortDirection, first bool) sqlb.OrderTerm {
	nulls := sqlb.NullsLast
	if first {
		nulls = sqlb.NullsFirst
	}
	return sqlb.OrderTerm{Expr: guardedRender(e.SQL), Direction: dir, Nulls: nulls}
}

// OrderUsing makes an "ORDER BY expr USING op" term, for operator classes
// such as pgvector's distance operators.
func OrderUsing[T any](e sqlb.Expr[T], op string) sqlb.OrderTerm {
	return sqlb.OrderTerm{Expr: guardedRender(e.SQL), Using: op}
}

func guardedRender(render func(a *sqlb.Args) string) func(a *sqlb.Args) string {
	return func(a *sqlb.Args) string {
		if !guard(a, constructSelect) {
			return ""
		}
		return render(a)
	}
}

// LimitAll renders LIMIT ALL in the after-order slot: explicit "no limit".
func LimitAll() SelectClause {
	return selectClause(sqlb.SlotAfterOrder, constructSelect, func(*sqlb.Args) string { return kwLimitAll })
}

// Materialized is a CTE prefix: MATERIALIZED (true) or NOT MATERIALIZED (false).
func Materialized(on bool) sqlb.SQLer {
	return sqlb.Raw(func(a *sqlb.Args) string {
		if !guard(a, constructSelect) {
			return ""
		}
		if on {
			return kwMaterialized
		}
		return kwNotMaterialized
	})
}

// IntersectAll renders INTERSECT ALL, which keeps duplicate rows where
// INTERSECT collapses them. SQLite lacks both ALL forms.
func IntersectAll(s, other *sqlb.Select[Dialect]) *sqlb.Select[Dialect] {
	return s.SetOp(kwIntersectAll, other)
}

// ExceptAll renders EXCEPT ALL, subtracting one occurrence per duplicate
// rather than every occurrence.
func ExceptAll(s, other *sqlb.Select[Dialect]) *sqlb.Select[Dialect] {
	return s.SetOp(kwExceptAll, other)
}

// ArraySubquery renders ARRAY(SELECT ...), typed as a slice of the author's
// element type.
func ArraySubquery[T any](s *sqlb.Select[Dialect]) sqlb.Expr[[]T] {
	return expr[[]T](constructSelect, func(a *sqlb.Args) string {
		var b strings.Builder
		b.WriteString("ARRAY(")
		s.WriteSQL(&b, a)
		b.WriteString(")")
		return b.String()
	})
}

// AnyOf renders e op ANY (SELECT ...); AllOf renders e op ALL (SELECT ...).
// SQLite has neither.
func AnyOf[T any](e sqlb.Expr[T], op string, s *sqlb.Select[Dialect]) sqlb.Predicate {
	return pred(constructSelect, func(a *sqlb.Args) string {
		return sqlb.PredicateSQL(sqlb.SubqueryCompare(e, op, kwAny, s), a)
	})
}

// AllOf renders e op ALL (SELECT ...): the comparison holds against every
// row the subquery returns, and an empty subquery makes it true.
func AllOf[T any](e sqlb.Expr[T], op string, s *sqlb.Select[Dialect]) sqlb.Predicate {
	return pred(constructSelect, func(a *sqlb.Args) string {
		return sqlb.PredicateSQL(sqlb.SubqueryCompare(e, op, kwAll, s), a)
	})
}

// Lateral marks a source as LATERAL, so its body may reference columns of
// sources to its left. Postgres and MySQL 8; not SQLite or ClickHouse.
func Lateral(src sqlb.Source) sqlb.Source {
	return sqlb.SourceOf(src.SourceAlias(), func(a *sqlb.Args) string {
		if !guard(a, constructSelect) {
			return ""
		}
		return kwLateral + " " + src.SourceSQL(a)
	})
}

// Values makes a VALUES list a source: "(VALUES ($1, $2), ($3, $4)) AS alias (a, b)".
// Every cell is a SQLer, so a bound Param and a typed expression both work.
func Values(alias string, columns []string, rows ...[]sqlb.SQLer) sqlb.Source {
	return sqlb.SourceOf(alias, func(a *sqlb.Args) string {
		if !guard(a, constructSelect) {
			return ""
		}
		d := a.Dialect()
		var b strings.Builder
		b.WriteString("(")
		b.WriteString(kwValues)
		b.WriteString(" ")
		for i, row := range rows {
			if i > 0 {
				b.WriteString(", ")
			}
			b.WriteString("(" + list(a, row) + ")")
		}
		b.WriteString(") AS ")
		b.WriteString(d.QuoteIdent(alias))
		if len(columns) > 0 {
			b.WriteString(" (" + sqlb.QuoteList(d, columns) + ")")
		}
		return b.String()
	})
}

// FuncSource makes a set-returning function call a source:
// "generate_series($1, $2) AS g (n)". Column names are optional; with none the
// alias is also the column name, as Postgres does for a scalar function.
func FuncSource(call sqlb.SQLer, alias string, columns ...string) sqlb.Source {
	return funcSource{call: call, alias: alias, columns: columns}
}

// WithOrdinality adds "WITH ORDINALITY", which appends a bigint row number
// column. Name it as the last entry of columns.
func WithOrdinality(src sqlb.Source) sqlb.Source {
	if f, ok := src.(funcSource); ok {
		f.ordinality = true
		return f
	}
	return src
}

type funcSource struct {
	call       sqlb.SQLer
	alias      string
	columns    []string
	ordinality bool
}

func (f funcSource) SourceAlias() string { return f.alias }
func (f funcSource) SourceSQL(a *sqlb.Args) string {
	if !guard(a, constructSelect) {
		return ""
	}
	d := a.Dialect()
	var b strings.Builder
	b.WriteString(f.call.SQL(a))
	if f.ordinality {
		b.WriteString(" ")
		b.WriteString(kwWithOrdinality)
	}
	b.WriteString(" AS ")
	b.WriteString(d.QuoteIdent(f.alias))
	if len(f.columns) > 0 {
		b.WriteString(" (" + sqlb.QuoteList(d, f.columns) + ")")
	}
	return b.String()
}
