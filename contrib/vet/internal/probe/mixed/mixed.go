// Package mixed uses one engine's expression in another engine's statement.
package mixed

import (
	"github.com/ubgo/sqlb/expr"
	"github.com/ubgo/sqlb/mysql"
	"github.com/ubgo/sqlb/pg"
	"github.com/ubgo/sqlb/sqlite"
	"github.com/ubgo/sqlb/stmt"
)

var (
	users = expr.Table{Name: "users", Alias: "u"}
	id    = expr.Col[int64](users, "id")
)

// Wrong is what the compiler cannot see: a Postgres expression in a SQLite
// statement. It builds, and Build refuses it; the analyzer reports it here.
func Wrong() (stmt.Query, error) {
	return sqlite.Select(pg.ArrayAgg(id)).From(users).Build()
}

// WrongThroughAlias is the same mistake with the dialect written as a value of
// sqlite.Dialect, which is an alias. The analyzer once looked a dialect up by
// the name types.TypeString prints, and an alias prints under its own name, so
// this form went unchecked.
func WrongThroughAlias() (stmt.Query, error) {
	return stmt.NewSelect(sqlite.Dialect{}).Exprs(pg.ArrayAgg(id)).From(users).Build()
}

// WrongMySQL is a MySQL expression in a Postgres statement, the mistake made
// through the fourth engine, which vet recognises only because mysql is in its
// engine and entry maps.
func WrongMySQL() (stmt.Query, error) {
	return pg.Select(mysql.UUID()).From(users).Build()
}
