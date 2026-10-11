// Package ddl builds the statements that create, change and remove what a
// library's tables are: CREATE TABLE, ALTER TABLE, DROP TABLE and the rest.
//
// It is authored DDL, never derived. A builder renders the statement its author
// wrote, over the same sqlb.Col[T] values the library's queries use, so a
// column cannot be created under one name or type and queried under another;
// nothing here inspects Go declarations and decides what to emit. Which
// statements run, in what order and when, stays the application's decision.
// The scope and its limits are ADR-0003.
//
// The package sits above stmt rather than beside it. A view's body is a
// stmt.Select and a SQLite trigger's body is stmt statements, and engine
// clauses reuse stmt's Clause machinery, so ddl imports stmt; it does not live
// in stmt because stmt imports only render and expr, and DDL needs the
// per-engine type spelling table in schema. The only thing in this package that
// is universal to Postgres, MySQL 8, SQLite and ClickHouse is what ADR-0002
// admits to a core; every engine construct lives in pg or sqlite.
//
// Every value inside a DDL statement is a literal, because no engine accepts a
// placeholder in DDL. The builders render through render.NewInlineArgs, which
// writes each value with the dialect's LiteralQuoting and fails closed on one
// it cannot write faithfully.
package ddl

import "errors"

// ErrZeroValue is returned by Build for a clause or statement written as a
// struct literal rather than made by its constructor, with a field the
// constructor always sets left empty: a key with no columns, a foreign key
// with no column pairs, a statement wrapping nothing. Every exported clause
// and statement type is a struct, so T{} compiles; before this, several such
// literals panicked inside Build or rendered PRIMARY KEY () for the server to
// reject. sqlbtest.ZeroValues builds every exported type's zero value to hold
// the rule.
var ErrZeroValue = errors.New("sqlb/ddl: built from a zero value rather than its constructor")

// Statement is one DDL statement. It carries no arguments: every value inside
// it was written as a literal, so it is passed to a driver as text alone.
type Statement struct {
	SQL string

	// Transactional is false for a statement Postgres refuses inside a
	// transaction block: CREATE INDEX CONCURRENTLY, DROP INDEX CONCURRENTLY and
	// DETACH PARTITION ... CONCURRENTLY, each measured against Postgres 17. A
	// runner must execute such a statement on its own, never in a batch.
	Transactional bool

	// OutsideTransaction is true for a statement that is a silent no-op inside
	// a transaction. SQLite's PRAGMA foreign_keys is the one known case: set to
	// OFF after BEGIN it still reads 1, with no error, which is how a table
	// rebuild wrapped in one transaction quietly loses its guard. A runner must
	// execute it before opening any transaction.
	OutsideTransaction bool

	// MustReturnNoRows is true for a check rather than a change: a statement
	// that reports a problem by returning rows instead of failing. SQLite's
	// PRAGMA foreign_key_check is the case, at the end of a table rebuild; a
	// runner must query it and fail on any row, because executing it and
	// ignoring the result reports success whatever it found.
	MustReturnNoRows bool

	// SkipIf is a query, and a runner that finds it returns a row skips the
	// statement. It is how an engine without IF NOT EXISTS for a statement
	// still writes a repeatable one: MySQL's CREATE INDEX has none, so its
	// IfNotExists sets SkipIf to a query for the index in the catalogue. A
	// runner that ignores the field runs the statement anyway, and a second
	// run fails loudly with a duplicate, rather than doing something else;
	// contrib/migrate's Execute honours it. Empty means always run.
	SkipIf string
}

// Script is the statements one builder produced, in the order they run.
//
// Most builders produce one. SQLite accepts one action per ALTER TABLE, so an
// ALTER TABLE with three actions is three statements there and one on
// Postgres, and the author writes the same code either way; a SQLite table
// rebuild is eleven. Every builder returns a Script so a caller never has to
// know which ones split.
type Script []Statement

// SQL returns each statement's text in order, for a caller that executes them
// itself or writes them to a migration file.
func (s Script) SQL() []string {
	out := make([]string, len(s))
	for i, st := range s {
		out[i] = st.SQL
	}
	return out
}

// Transactional reports whether every statement may run inside one
// transaction, which is the question a runner asks before wrapping a script in
// one.
func (s Script) Transactional() bool {
	for _, st := range s {
		if !st.Transactional || st.OutsideTransaction {
			return false
		}
	}
	return true
}
