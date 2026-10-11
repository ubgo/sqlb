package snippets_test

import (
	"fmt"
	"strings"
	"time"

	"github.com/ubgo/sqlb"
	"github.com/ubgo/sqlb/pg"
)

var d = sqlb.PostgresDialect{}

func sel() *sqlb.Select[sqlb.PostgresDialect] { return sqlb.NewSelect(d) }

func show(q sqlb.Query, err error) {
	if err != nil {
		fmt.Println("error:", err)
		return
	}
	sql := q.SQL
	sql = strings.ReplaceAll(sql, `), "`, "),\n\"")
	sql = strings.ReplaceAll(sql, ") SELECT ", ")\nSELECT ")
	fmt.Println(sql)
	if len(q.Args) > 0 {
		fmt.Println("args:", len(q.Args))
	}
}

// tbl is a shorthand for an inline table.
func tbl(name, alias string) sqlb.Table { return sqlb.Table{Name: name, Alias: alias} }

// ref makes a typed column against a CTE or derived table by alias.
func ref[T any](src sqlb.Source, name string) sqlb.Expr[T] { return sqlb.Col[T](sqlb.Ref(src), name) }

// raw is an untyped constant fragment.
func raw(sql string) sqlb.SQLer { return sqlb.Raw(func(*sqlb.Args) string { return sql }) }

// join helpers keep the examples short.
func inner(t sqlb.Table, on ...sqlb.Predicate) sqlb.Join {
	return sqlb.Join{Type: sqlb.JoinInner, Table: t, On: on}
}
func left(t sqlb.Table, on ...sqlb.Predicate) sqlb.Join {
	return sqlb.Join{Type: sqlb.JoinLeft, Table: t, On: on}
}
func cross(src sqlb.Source) sqlb.Join { return sqlb.Join{Type: sqlb.JoinCross, Source: src} }
func leftLateral(src sqlb.Source) sqlb.Join {
	return sqlb.Join{Type: sqlb.JoinLeft, Source: pg.Lateral(src), On: []sqlb.Predicate{sqlb.True()}}
}
func crossLateral(src sqlb.Source) sqlb.Join {
	return sqlb.Join{Type: sqlb.JoinCross, Source: pg.Lateral(src)}
}

func asc[T any](e sqlb.Expr[T]) sqlb.OrderTerm  { return sqlb.OrderBy(e, sqlb.SortAsc) }
func desc[T any](e sqlb.Expr[T]) sqlb.OrderTerm { return sqlb.OrderBy(e, sqlb.SortDesc) }

var (
	t0 = time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	t1 = time.Date(2024, 1, 31, 0, 0, 0, 0, time.UTC)
)

var sink struct {
	s    string
	i32  int32
	i64  int64
	f64  float64
	b    bool
	ss   []string
	t    time.Time
	j    []byte
	f64p *float64
}
