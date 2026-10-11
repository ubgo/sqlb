package mysql

import (
	"github.com/ubgo/sqlb"
	"github.com/ubgo/sqlb/ddl"
)

// The statement constructors. Each is exactly the long form with the dialect
// written out, which TestEntry_IsTheLongFormExactly pins; what it adds is the
// dialect in the type, so a Postgres clause on a MySQL statement does not
// compile. There are no InsertReturning and siblings: MySQL has no RETURNING,
// so the names do not exist here and the line cannot be written.

// SelectQuery is the statement type Select returns.
type SelectQuery = sqlb.Select[Dialect]

// InsertQuery is the statement Insert returns, with no Build until a row, a
// query or a body completes it.
type InsertQuery = sqlb.Insert[Dialect]

// InsertReady is a complete INSERT.
type InsertReady = sqlb.InsertReady[Dialect]

// UpdateQuery is the statement Update returns, with no Build until Where or
// All says which rows.
type UpdateQuery = sqlb.Update[Dialect]

// FilteredUpdate is an UPDATE that has said which rows.
type FilteredUpdate = sqlb.FilteredUpdate[Dialect]

// DeleteQuery is the statement Delete returns, with no Build until Where or
// All.
type DeleteQuery = sqlb.Delete[Dialect]

// FilteredDelete is a DELETE that has said which rows.
type FilteredDelete = sqlb.FilteredDelete[Dialect]

// Select starts a SELECT for MySQL. Items passed here carry no scan targets;
// a query to be scanned calls Select() and adds bindings with Columns.
func Select(items ...sqlb.SQLer) *SelectQuery { return sqlb.NewSelect(Dialect{}).Exprs(items...) }

// Insert starts an INSERT into t for MySQL.
func Insert(t sqlb.Table) *InsertQuery { return sqlb.NewInsert(Dialect{}).Into(t) }

// Update starts an UPDATE of t for MySQL.
func Update(t sqlb.Table) *UpdateQuery { return sqlb.NewUpdate(Dialect{}).Table(t) }

// Delete starts a DELETE from t for MySQL.
func Delete(t sqlb.Table) *DeleteQuery { return sqlb.NewDelete(Dialect{}).From(t) }

// CreateTableQuery is a CREATE TABLE with no column yet, which has no Build.
type CreateTableQuery = ddl.CreateTable[Dialect]

// CreateTableReady is a CREATE TABLE with at least one column.
type CreateTableReady = ddl.CreateTableReady[Dialect]

// AlterTableQuery is an ALTER TABLE with no action yet, which has no Build.
type AlterTableQuery = ddl.AlterTable[Dialect]

// AlterTableReady is an ALTER TABLE with at least one action.
type AlterTableReady = ddl.AlterTableReady[Dialect]

// DropTableQuery is a DROP TABLE, complete from its constructor.
type DropTableQuery = ddl.DropTable[Dialect]

// CreateTable starts a CREATE TABLE of t for MySQL.
func CreateTable(t sqlb.Table) *CreateTableQuery { return ddl.NewCreateTable(Dialect{}, t) }

// AlterTable starts an ALTER TABLE of t for MySQL.
func AlterTable(t sqlb.Table) *AlterTableQuery { return ddl.NewAlterTable(Dialect{}, t) }

// DropTable drops one or more tables for MySQL, one statement per table.
func DropTable(first sqlb.Table, more ...sqlb.Table) *DropTableQuery {
	return ddl.NewDropTable(Dialect{}, first, more...)
}
