// Package pg holds what only Postgres has. A library that imports it is a
// Postgres library, and its import list says so. Every construct here records
// Args.Unsupported when rendered through another dialect, so Build fails
// closed instead of the database failing at runtime.
//
// The dialect itself is sqlb.PostgresDialect in the core; it is ten lines
// and every engine needs its dialect available without an extra import.
//
// Files: json.go, sqljson.go, jsontable.go, array.go, text.go, time.go,
// range.go, fts.go, math.go, source.go, lock.go, write.go (ON CONFLICT),
// merge.go, recursive.go, misc.go, more.go, ext.go.
//
// Extensions live in sub-packages, one per CREATE EXTENSION: pg/vector,
// pg/trgm, pg/fuzzy, pg/ltree, pg/crypto, pg/earth, pg/unaccent, and
// pg/postgis as the template. Each builds on ext.go and imports nothing
// else from here.
package pg

import (
	"github.com/ubgo/sqlb/dialect/postgres"

	"github.com/ubgo/sqlb"
)

// Dialect is the Postgres dialect, re-exported so a caller that already
// imports this package for its constructs does not need a second import to
// name the engine. The definition lives in github.com/ubgo/sqlb/dialect/postgres, which imports the render
// layer and nothing else.
type Dialect = postgres.Dialect

// Is reports whether d is the Postgres dialect.
func Is(d sqlb.Dialect) bool { return sqlb.DialectName(d) == sqlb.DialectPostgres }

// The guard rule and the constructors built on it live in the core, so pg,
// sqlite, ch and any package outside this module share one implementation.
// These wrappers exist only so a call site inside this package reads without
// repeating the engine name.

func guard(a *sqlb.Args, construct string) bool {
	return sqlb.EngineGuard(a, sqlb.DialectPostgres, construct)
}

func expr[T any](construct string, render func(a *sqlb.Args) string) sqlb.Expr[T] {
	return sqlb.EngineExpr[T](sqlb.DialectPostgres, construct, render)
}

// litExpr makes a guarded typed expression from a constant fragment. Use it
// rather than sqlb.Literal for anything only Postgres accepts: Literal has no
// dialect to consult, so such a constant would render under every dialect and
// be rejected by the database instead of by Build.
func litExpr[T any](construct, sql string) sqlb.Expr[T] {
	return sqlb.EngineLit[T](sqlb.DialectPostgres, construct, sql)
}

// litSQLer is litExpr for a bare fragment that is not an expression, such as
// an argument decorator.
func litSQLer(construct string, render func(a *sqlb.Args) string) sqlb.SQLer {
	return sqlb.EngineRaw(sqlb.DialectPostgres, construct, render)
}

func pred(construct string, render func(a *sqlb.Args) string) sqlb.Predicate {
	return sqlb.EnginePred(sqlb.DialectPostgres, construct, render)
}

func binop[R any](construct, op string, l, r sqlb.SQLer) sqlb.Expr[R] {
	return sqlb.EngineBinOp[R](sqlb.DialectPostgres, construct, op, l, r)
}

func binpred(construct, op string, l, r sqlb.SQLer) sqlb.Predicate {
	return sqlb.EngineBinPred(sqlb.DialectPostgres, construct, op, l, r)
}

func fn[R any](construct, name string, args ...sqlb.SQLer) sqlb.Expr[R] {
	return sqlb.EngineFn[R](sqlb.DialectPostgres, construct, name, args...)
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

func updateClause(slot sqlb.Slot, construct string, render func(a *sqlb.Args) string) UpdateClause {
	return sqlb.EngineClause[Dialect, sqlb.UpdateKind](construct, slot, render)
}

func deleteClause(slot sqlb.Slot, construct string, render func(a *sqlb.Args) string) DeleteClause {
	return sqlb.EngineClause[Dialect, sqlb.DeleteKind](construct, slot, render)
}

// list renders SQLers comma-separated.
func list(a *sqlb.Args, items []sqlb.SQLer) string { return sqlb.EngineList(a, items) }

// lit is an unbound SQL literal, for constants such as a config name or a
// bounds string that Postgres requires as a literal, never for request data.
//
// Deliberately unguarded: it is internal and every caller is a constructor
// that has already guarded. A Postgres-only constant exposed to callers goes
// through litExpr instead, which does guard.
func lit(s string) sqlb.SQLer { return sqlb.Raw(func(*sqlb.Args) string { return s }) }

// quoteLit renders a string as a single-quoted SQL literal with quotes
// doubled. Used only for author-chosen constants; see lit.
func quoteLit(s string) sqlb.SQLer { return sqlb.EngineQuoteLit(s) }
