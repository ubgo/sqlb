package sqlite

import "github.com/ubgo/sqlb"

// Rowid references a table's implicit rowid column.
//
// Every ordinary SQLite table has one unless it was declared WITHOUT ROWID.
// It is not stable: VACUUM can renumber it, and INSERT OR REPLACE gives the
// replacing row a new one. Never store it as a reference; use it for a
// keyset page inside one statement, where those things cannot happen.
func Rowid(t sqlb.Table) sqlb.Expr[int64] {
	return expr[int64](constructMisc, func(a *sqlb.Args) string {
		return t.Qualifier(a.Dialect()) + "." + a.Dialect().QuoteIdent("rowid")
	})
}

// LastInsertRowid renders last_insert_rowid(), the rowid of the most recent
// insert on this connection.
//
// On this connection is the trap. database/sql hands out a pool, so a second
// statement can run on a different connection and see a different answer, or
// none. Use RETURNING instead, which SQLite has had since 3.35 and which the
// core exposes through the dialect's Returning capability.
func LastInsertRowid() sqlb.Expr[int64] {
	return fn[int64](constructMisc, "last_insert_rowid")
}

// Changes renders changes(), the rows modified by the most recent statement
// on this connection. The same pooling caveat as LastInsertRowid applies.
func Changes() sqlb.Expr[int64] { return fn[int64](constructMisc, "changes") }

// TotalChanges renders total_changes(), rows modified since the connection
// was opened.
func TotalChanges() sqlb.Expr[int64] { return fn[int64](constructMisc, "total_changes") }

// Random renders random(), a signed 64-bit integer. Not a float in [0,1) as
// random() is in Postgres, which is exactly the kind of same-name difference
// that keeps this out of the core.
func Random() sqlb.Expr[int64] { return fn[int64](constructMisc, "random") }

// RandomBlob renders randomblob(n), n cryptographically random bytes.
func RandomBlob(n int) sqlb.Expr[[]byte] {
	return fn[[]byte](constructMisc, "randomblob", sqlb.Param(n))
}

// Abs renders abs(e). SQLite returns NULL rather than raising on the most
// negative 64-bit integer, where other engines raise.
func Abs[T sqlb.Number](e sqlb.Expr[T]) sqlb.Expr[T] { return fn[T](constructMisc, "abs", e) }

// Max renders max(a, b, ...), the scalar many-argument form, which is a
// different function from the aggregate of the same name. SQLite decides
// which by argument count; the Go name says which you asked for.
func Max[T any](first sqlb.Expr[T], rest ...sqlb.Expr[T]) sqlb.Expr[T] {
	return fn[T](constructMisc, "max", exprList(first, rest)...)
}

// Min renders min(a, b, ...), the scalar form.
func Min[T any](first sqlb.Expr[T], rest ...sqlb.Expr[T]) sqlb.Expr[T] {
	return fn[T](constructMisc, "min", exprList(first, rest)...)
}

func exprList[T any](first sqlb.Expr[T], rest []sqlb.Expr[T]) []sqlb.SQLer {
	out := make([]sqlb.SQLer, 0, len(rest)+1)
	out = append(out, first)
	for _, e := range rest {
		out = append(out, e)
	}
	return out
}

// Version renders sqlite_version(), the library version as text. A health
// check reads it; nothing should branch on it, because the shape of the
// string is not a contract.
func Version() sqlb.Expr[string] { return fn[string](constructMisc, "sqlite_version") }

// IndexedBy forces a query to use one index on a table reference, and
// NotIndexed forbids every index on one.
//
// INDEXED BY is per table reference, not per statement, so where you put the
// clause decides which table it binds to. On the driving table it goes in the
// statement, which renders it in SlotAfterFrom:
//
//	sel.From(t).Clause(sqlite.IndexedBy("idx_t_name"))
//
// On a joined table it goes in that join, because a statement slot names one
// position per statement and a join needs one of its own:
//
//	sel.Join(sqlb.Join{Type: sqlb.JoinInner, Table: other, On: ...,
//		Clauses: []sqlb.AnyClause{sqlite.IndexedBy("idx_other_id")}})
//
// Putting the joined table's hint in the statement binds it to the driving
// table instead, which names an index that table does not have.
//
// Both are for the case where the planner has chosen wrong and you can prove
// it. They are a maintenance hazard: dropping or renaming the named index
// turns every query carrying this clause into an error rather than a slower
// query, which is why SQLite documents them as a debugging aid rather than a
// tuning tool.
func IndexedBy(index string) SelectClause {
	return selectClause(sqlb.SlotAfterFrom, constructMisc, func(a *sqlb.Args) string {
		return "INDEXED BY " + a.Dialect().QuoteIdent(index)
	})
}

// NotIndexed renders NOT INDEXED, forbidding every index on the source.
func NotIndexed() SelectClause {
	return selectClause(sqlb.SlotAfterFrom, constructMisc, func(*sqlb.Args) string {
		return "NOT INDEXED"
	})
}
