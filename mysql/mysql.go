// Package mysql holds what only MySQL has, and is where a MySQL statement
// starts.
//
// It is the counterpart of pg and sqlite: the statements begin here, every
// MySQL construct is beside them, and contrib/driver-mysql executes what this
// package renders against a real MySQL 8.4, which is the only thing that
// turns a construct from asserted into proven.
//
// Files: mysql.go (the guarded constructors), entry.go (the statements),
// write.go (upserts, INSERT IGNORE, UPDATE and DELETE with LIMIT), read.go
// (locking, WITH ROLLUP, functions), and the DDL in table.go and objects.go.
package mysql

import (
	"github.com/ubgo/sqlb"
	"github.com/ubgo/sqlb/dialect/mysql"
)

// Dialect is the MySQL dialect, re-exported so a caller importing this
// package for its constructs needs no second import to name the engine.
type Dialect = mysql.Dialect

// Is reports whether d is the MySQL dialect.
func Is(d sqlb.Dialect) bool { return sqlb.DialectName(d) == sqlb.DialectMySQL }

// Construct names for ErrUnsupportedDialect messages, one per area.
const (
	constructWrite = "mysql.write"
	constructRead  = "mysql.read"
	constructJSON  = "mysql.json"
	constructText  = "mysql.text"
	constructTime  = "mysql.time"
)

func expr[T any](construct string, render func(a *sqlb.Args) string) sqlb.Expr[T] {
	return sqlb.EngineExpr[T](sqlb.DialectMySQL, construct, render)
}

func pred(construct string, render func(a *sqlb.Args) string) sqlb.Predicate {
	return sqlb.EnginePred(sqlb.DialectMySQL, construct, render)
}

func binop[R any](construct, op string, l, r sqlb.SQLer) sqlb.Expr[R] {
	return sqlb.EngineBinOp[R](sqlb.DialectMySQL, construct, op, l, r)
}

func binpred(construct, op string, l, r sqlb.SQLer) sqlb.Predicate {
	return sqlb.EngineBinPred(sqlb.DialectMySQL, construct, op, l, r)
}

func fn[R any](construct, name string, args ...sqlb.SQLer) sqlb.Expr[R] {
	return sqlb.EngineFn[R](sqlb.DialectMySQL, construct, name, args...)
}

// The clause types this engine's constructors return, one per statement
// kind, so a clause for the wrong dialect or the wrong statement does not
// compile.
type (
	// SelectClause is a clause for a SELECT on MySQL.
	SelectClause = sqlb.Clause[Dialect, sqlb.SelectKind]
	// InsertClause is a clause for an INSERT on MySQL.
	InsertClause = sqlb.Clause[Dialect, sqlb.InsertKind]
	// UpdateClause is a clause for an UPDATE on MySQL.
	UpdateClause = sqlb.Clause[Dialect, sqlb.UpdateKind]
	// DeleteClause is a clause for a DELETE on MySQL.
	DeleteClause = sqlb.Clause[Dialect, sqlb.DeleteKind]
)

func clause[K any](slot sqlb.Slot, construct string, render func(a *sqlb.Args) string) sqlb.Clause[Dialect, K] {
	return sqlb.EngineClause[Dialect, K](construct, slot, render)
}

// Guard reports whether rendering may continue under the dialect in a,
// recording the construct as unsupported when it may not.
func Guard(a *sqlb.Args, construct string) bool {
	return sqlb.EngineGuard(a, sqlb.DialectMySQL, construct)
}

// Expr makes a guarded typed expression, for a helper this package lacks.
func Expr[T any](construct string, render func(a *sqlb.Args) string) sqlb.Expr[T] {
	return expr[T](construct, render)
}

// Func renders a guarded function call typed R.
func Func[R any](construct, name string, args ...sqlb.SQLer) sqlb.Expr[R] {
	return fn[R](construct, name, args...)
}

// ClauseIn makes a guarded clause for a statement slot, typed to MySQL and to
// the statement kind K.
func ClauseIn[K any](slot sqlb.Slot, construct string, render func(a *sqlb.Args) string) sqlb.Clause[Dialect, K] {
	return clause[K](slot, construct, render)
}
