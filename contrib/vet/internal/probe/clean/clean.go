// Package clean has nothing for the analyzers to report.
package clean

import (
	"github.com/ubgo/sqlb/expr"
	"github.com/ubgo/sqlb/pg"
	"github.com/ubgo/sqlb/stmt"
)

var (
	users = expr.Table{Name: "users", Alias: "u"}
	id    = expr.Col[int64](users, "id")
)

// Fine uses a Postgres expression in a Postgres statement and assigns every
// builder result.
func Fine() (stmt.Query, error) {
	q := pg.Select(pg.ArrayAgg(id)).From(users)
	q = q.Where(expr.Eq(id, int64(1)))
	return q.Build()
}
