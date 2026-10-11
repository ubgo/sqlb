// Package discard drops the result of a builder call.
package discard

import (
	"github.com/ubgo/sqlb/ddl"
	"github.com/ubgo/sqlb/expr"
	"github.com/ubgo/sqlb/pg"
	"github.com/ubgo/sqlb/stmt"
)

var (
	users = expr.Table{Name: "users", Alias: "u"}
	id    = expr.Col[int64](users, "id")
)

// Dropped filters and throws the filter away: q is unchanged.
func Dropped() (stmt.Query, error) {
	q := pg.Select(id).From(users)
	q.Where(expr.Eq(id, int64(1)))
	return q.Build()
}

// DroppedDDL is the same mistake on a DDL statement: IfNotExists returns a
// derived statement, so this still fails on the second boot.
func DroppedDDL() (int, error) {
	ct := pg.CreateTable(users).Column(ddl.Column(id))
	ct.IfNotExists()
	s, err := ct.Build()
	return len(s), err
}

// DroppedEngineStatement is the same mistake on a statement kind an engine
// package defines. Merge's methods return a derived statement, so Into here
// changes nothing and m is still a MERGE with no target.
func DroppedEngineStatement() (stmt.Query, error) {
	m := pg.NewMerge(pg.Dialect{})
	m.Into(users)
	return m.Build()
}
