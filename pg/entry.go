package pg

import (
	"github.com/ubgo/sqlb"
	"github.com/ubgo/sqlb/ddl"
)

// The statement constructors. A Postgres user imports this one package: the
// statements start here and every Postgres construct that goes inside them is
// here too, so no one has to learn which of two packages a name lives in.
//
// They are spellings, not builders. Each is exactly the long form with the
// dialect written out, sqlb.NewSelect(Dialect{}) and its siblings, which
// TestEntry_IsTheLongFormExactly pins byte for byte; every method a statement
// has lives once on the generic types in stmt. What the constructor adds is the
// dialect in the type: a *SelectQuery is a Postgres statement to the compiler,
// which is what makes a SQLite clause on it a compile error.
//
// Until 2026-09-21 these lived in a separate package, pgq. They moved here
// because a one-letter difference between two packages that both read as
// "Postgres" was the only thing telling a reader where a name lived.

// SelectQuery is the statement type Select returns. The aliases below carry a
// Query suffix because Go cannot give a type and a function one name in a
// package; an alias is the type itself, so a value passes through either name
// with no conversion.
type SelectQuery = sqlb.Select[Dialect]

// InsertQuery is the statement Insert returns. It has no Build until a row, a
// query or a body completes it.
type InsertQuery = sqlb.Insert[Dialect]

// InsertReady is a complete INSERT, the only form with Build.
type InsertReady = sqlb.InsertReady[Dialect]

// UpdateQuery is the statement Update returns. It has no Build until Where or
// All says which rows, so an unfiltered UPDATE does not compile.
type UpdateQuery = sqlb.Update[Dialect]

// FilteredUpdate is an UPDATE that has said which rows, the only form with Build.
type FilteredUpdate = sqlb.FilteredUpdate[Dialect]

// DeleteQuery is the statement Delete returns, with no Build until Where or All.
type DeleteQuery = sqlb.Delete[Dialect]

// FilteredDelete is a DELETE that has said which rows, the only form with Build.
type FilteredDelete = sqlb.FilteredDelete[Dialect]

// Select starts a SELECT for Postgres. Items passed here are added without scan
// targets, for a subquery, a CTE body or a view; a query that will be scanned
// calls Select() with no items and adds bindings with Columns.
func Select(items ...sqlb.SQLer) *SelectQuery {
	return sqlb.NewSelect(Dialect{}).Exprs(items...)
}

// Insert starts an INSERT into t for Postgres.
func Insert(t sqlb.Table) *InsertQuery { return sqlb.NewInsert(Dialect{}).Into(t) }

// Update starts an UPDATE of t for Postgres.
func Update(t sqlb.Table) *UpdateQuery { return sqlb.NewUpdate(Dialect{}).Table(t) }

// Delete starts a DELETE from t for Postgres.
func Delete(t sqlb.Table) *DeleteQuery { return sqlb.NewDelete(Dialect{}).From(t) }

// InsertReturning, UpdateReturning and DeleteReturning add RETURNING to a
// complete write. They are the core's functions constrained to a dialect with
// the capability, instantiated for Postgres; an engine without RETURNING has no
// such names in its package, so the line cannot be written there.
var (
	InsertReturning = sqlb.InsertReturning[Dialect]
	UpdateReturning = sqlb.UpdateReturning[Dialect]
	DeleteReturning = sqlb.DeleteReturning[Dialect]
)

// The DDL statement constructors, spellings of the ddl package's with the
// dialect written in, exactly as Select is of sqlb.NewSelect. The types are
// aliases, so a value passes through either name with no conversion.

// CreateTableQuery is a CREATE TABLE with no column yet, which has no Build.
type CreateTableQuery = ddl.CreateTable[Dialect]

// CreateTableReady is a CREATE TABLE with at least one column, the only form
// with Build.
type CreateTableReady = ddl.CreateTableReady[Dialect]

// AlterTableQuery is an ALTER TABLE with no action yet, which has no Build.
type AlterTableQuery = ddl.AlterTable[Dialect]

// AlterTableReady is an ALTER TABLE with at least one action.
type AlterTableReady = ddl.AlterTableReady[Dialect]

// DropTableQuery is a DROP TABLE, complete from its constructor.
type DropTableQuery = ddl.DropTable[Dialect]

// CreateTable starts a CREATE TABLE of t for Postgres.
func CreateTable(t sqlb.Table) *CreateTableQuery { return ddl.NewCreateTable(Dialect{}, t) }

// AlterTable starts an ALTER TABLE of t for Postgres.
func AlterTable(t sqlb.Table) *AlterTableQuery { return ddl.NewAlterTable(Dialect{}, t) }

// DropTable drops one or more tables for Postgres, one statement per table.
func DropTable(first sqlb.Table, more ...sqlb.Table) *DropTableQuery {
	return ddl.NewDropTable(Dialect{}, first, more...)
}
