// Package static holds one instance of each mistake the static analyzers
// report, and one correct use of each construct beside it.
package static

import (
	"github.com/ubgo/sqlb"
	"github.com/ubgo/sqlb/pg"
	"github.com/ubgo/sqlb/sqlite"
)

// Users and Posts are the tables the probe's statements are written against.
var (
	Users = sqlb.Table{Name: "users", Alias: "u"}
	ID    = sqlb.Col[int64](Users, "id")
	Name  = sqlb.Col[string](Users, "name")
	Posts = sqlb.Table{Name: "posts", Alias: "p"}
	PUser = sqlb.Col[int64](Posts, "user_id")
)

// LiteralFromVariable is interpolation: the text came from a variable.
func LiteralFromVariable(s string) sqlb.Expr[string] {
	return sqlb.Literal[string](s) // want sqlbliteral
}

// LiteralConstant is what Literal is for.
func LiteralConstant() sqlb.Expr[string] { return sqlb.Literal[string]("'fixed'") }

// StructLiteral builds an Update without its constructor.
func StructLiteral() *sqlb.FilteredUpdate[sqlb.PostgresDialect] {
	return &sqlb.FilteredUpdate[sqlb.PostgresDialect]{} // want sqlbstructlit
}

// QueryLiteral builds a Query without its constructor.
func QueryLiteral() sqlb.Query { return sqlb.Query{SQL: "SELECT 1"} } // want sqlbstructlit

// SetOpSQLiteLacks is INTERSECT ALL on a statement fixed to SQLite.
func SetOpSQLiteLacks() *sqlite.SelectQuery {
	a := sqlite.Select().Exprs(ID).From(Users)
	return a.SetOp("INTERSECT ALL", a) // want sqlbkeyword
}

// SetOpPostgresHas is the same keyword on Postgres, which accepts it.
func SetOpPostgresHas() *pg.SelectQuery {
	a := pg.Select().Exprs(ID).From(Users)
	return a.SetOp("INTERSECT ALL", a)
}

// JoinOtherEngineSpelling is T-SQL's spelling of LATERAL.
func JoinOtherEngineSpelling() *pg.SelectQuery {
	return pg.Select().Exprs(ID).From(Users).
		Join(sqlb.Join{Type: sqlb.JoinType("CROSS APPLY"), Table: Posts, On: []sqlb.Predicate{sqlb.EqExpr(PUser, ID)}}) // want sqlbkeyword
}

// JoinNatural is refused everywhere, even where the engine has it.
func JoinNatural() *pg.SelectQuery {
	return pg.Select().Exprs(ID).From(Users).
		Join(sqlb.Join{Type: sqlb.JoinType("NATURAL JOIN"), Table: Posts}) // want sqlbkeyword
}

// JoinFullOuter is a keyword the constants omit that both engines have.
func JoinFullOuter() *pg.SelectQuery {
	return pg.Select().Exprs(ID).From(Users).
		Join(sqlb.Join{Type: sqlb.JoinType("FULL OUTER JOIN"), Table: Posts, On: []sqlb.Predicate{sqlb.EqExpr(PUser, ID)}})
}

// RowArity compares a pair against a triple.
func RowArity() sqlb.Predicate {
	return sqlb.RowIn(sqlb.Row(ID, Name), sqlb.Row(sqlb.Param[int64](1), sqlb.Param("a"), sqlb.Param("b"))) // want sqlbrow
}

// RowTypes compares an int64 column against a string value.
func RowTypes() sqlb.Predicate {
	return sqlb.RowEq(sqlb.Row(ID, Name), sqlb.Row(sqlb.Param("1"), sqlb.Param("a"))) // want sqlbrow
}

// RowRight lines up.
func RowRight() sqlb.Predicate {
	return sqlb.RowIn(sqlb.Row(ID, Name), sqlb.Row(sqlb.Param[int64](1), sqlb.Param("a")))
}

// DynamicColumn is a Col whose name came from outside.
func DynamicColumn(name string) sqlb.Expr[string] { return sqlb.Col[string](Users, name) }
