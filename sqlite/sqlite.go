// Package sqlite holds what only SQLite has.
//
// It is the counterpart of pg and ch, and it exists so the three engines this
// repository can render for have the same shape: a dialect in the core, a
// construct package here, and a driver module under contrib for whoever needs
// to execute the result.
//
// SQLite is the one engine whose constructs this repository can prove. The
// driver in contrib/driver-sqlite is pure Go and needs no server, so every
// helper here is executed against a real database in that module's tests, not
// only rendered and compared as text. No other engine package can say that
// yet.
//
// Files: sqlite.go (the guarded constructors), write.go (conflict
// resolution), json.go (the JSON1 extension), text.go, time.go, misc.go,
// pragma.go (the PRAGMA statements), and the virtual tables in fts.go,
// rtree.go and virtual.go.
package sqlite

import (
	"github.com/ubgo/sqlb/dialect/sqlite"

	"github.com/ubgo/sqlb"
)

// Dialect is the SQLite dialect, re-exported so a caller that already
// imports this package for its constructs does not need a second import to
// name the engine. The definition lives in github.com/ubgo/sqlb/dialect/sqlite, which imports the render
// layer and nothing else.
type Dialect = sqlite.Dialect

// Is reports whether d is the SQLite dialect.
func Is(d sqlb.Dialect) bool { return sqlb.DialectName(d) == sqlb.DialectSQLite }

// Construct names for ErrUnsupportedDialect messages. One per area, so an
// error says which family of constructs was rendered in the wrong place.
const (
	constructWrite = "sqlite.write"
	constructJSON  = "sqlite.json"
	constructText  = "sqlite.text"
	constructTime  = "sqlite.time"
	constructMisc  = "sqlite.misc"
	constructAgg   = "sqlite.agg"
)

// The guard rule and the constructors built on it live in the core, so pg,
// sqlite, ch and any package outside this module share one implementation.
// These wrappers exist only so a call site inside this package reads without
// repeating the engine name.

func guard(a *sqlb.Args, construct string) bool {
	return sqlb.EngineGuard(a, sqlb.DialectSQLite, construct)
}

func expr[T any](construct string, render func(a *sqlb.Args) string) sqlb.Expr[T] {
	return sqlb.EngineExpr[T](sqlb.DialectSQLite, construct, render)
}

func pred(construct string, render func(a *sqlb.Args) string) sqlb.Predicate {
	return sqlb.EnginePred(sqlb.DialectSQLite, construct, render)
}

func binop[R any](construct, op string, l, r sqlb.SQLer) sqlb.Expr[R] {
	return sqlb.EngineBinOp[R](sqlb.DialectSQLite, construct, op, l, r)
}

func binpred(construct, op string, l, r sqlb.SQLer) sqlb.Predicate {
	return sqlb.EngineBinPred(sqlb.DialectSQLite, construct, op, l, r)
}

func fn[R any](construct, name string, args ...sqlb.SQLer) sqlb.Expr[R] {
	return sqlb.EngineFn[R](sqlb.DialectSQLite, construct, name, args...)
}

// The clause types this engine's constructors return, one per statement
// kind. Each is an alias, so a value is exactly the core's typed clause and a
// statement of the wrong dialect or the wrong kind refuses it at compile time.
type (
	// SelectClause is a clause for a SELECT on this engine.
	SelectClause = sqlb.Clause[Dialect, sqlb.SelectKind]
	// InsertClause is a clause for an INSERT on this engine.
	InsertClause = sqlb.Clause[Dialect, sqlb.InsertKind]
	// UpdateClause is a clause for an UPDATE on this engine.
	UpdateClause = sqlb.Clause[Dialect, sqlb.UpdateKind]
	// DeleteClause is a clause for a DELETE on this engine.
	DeleteClause = sqlb.Clause[Dialect, sqlb.DeleteKind]
)

func selectClause(slot sqlb.Slot, construct string, render func(a *sqlb.Args) string) SelectClause {
	return sqlb.EngineClause[Dialect, sqlb.SelectKind](construct, slot, render)
}

func insertClause(slot sqlb.Slot, construct string, render func(a *sqlb.Args) string) InsertClause {
	return sqlb.EngineClause[Dialect, sqlb.InsertKind](construct, slot, render)
}

// list renders SQLers comma-separated.
func list(a *sqlb.Args, items []sqlb.SQLer) string { return sqlb.EngineList(a, items) }

// quoteLit renders an author-chosen constant as a single-quoted literal with
// quotes doubled. Never for request data, which goes through sqlb.Param.
func quoteLit(s string) sqlb.SQLer { return sqlb.EngineQuoteLit(s) }

// The exported constructors an application builds its own helpers on, the
// same set pg exports. A helper this package does not have can be written at
// the call site and still fail closed under another dialect.

// Guard reports whether rendering may continue under the dialect in a. When
// it returns false it has already recorded the construct as unsupported.
func Guard(a *sqlb.Args, construct string) bool { return guard(a, construct) }

// Expr makes a guarded typed expression from a render function.
func Expr[T any](construct string, render func(a *sqlb.Args) string) sqlb.Expr[T] {
	return expr[T](construct, render)
}

// Pred makes a guarded predicate from a render function.
func Pred(construct string, render func(a *sqlb.Args) string) sqlb.Predicate {
	return pred(construct, render)
}

// BinOp renders "left op right" as a guarded expression typed R.
func BinOp[R any](construct, op string, l, r sqlb.SQLer) sqlb.Expr[R] {
	return binop[R](construct, op, l, r)
}

// BinPred renders "left op right" as a guarded predicate.
func BinPred(construct, op string, l, r sqlb.SQLer) sqlb.Predicate {
	return binpred(construct, op, l, r)
}

// Func renders a guarded function call typed R.
func Func[R any](construct, name string, args ...sqlb.SQLer) sqlb.Expr[R] {
	return fn[R](construct, name, args...)
}

// ClauseIn makes a guarded clause for a statement slot, typed to this engine
// and to the statement kind K, which the caller names: an extension package
// writing a SELECT clause instantiates ClauseIn[sqlb.SelectKind].
func ClauseIn[K any](slot sqlb.Slot, construct string, render func(a *sqlb.Args) string) sqlb.Clause[Dialect, K] {
	return sqlb.EngineClause[Dialect, K](construct, slot, render)
}

// QuoteLit renders an author-chosen constant as a single-quoted literal.
func QuoteLit(s string) sqlb.SQLer { return quoteLit(s) }

// List renders SQLers comma-separated.
func List(a *sqlb.Args, items []sqlb.SQLer) string { return list(a, items) }
