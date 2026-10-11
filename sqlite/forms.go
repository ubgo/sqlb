package sqlite

import (
	"strings"

	"github.com/ubgo/sqlb"
	"github.com/ubgo/sqlb/ddl"
)

// CREATE TABLE ... AS SELECT, which is a statement of its own rather than a
// form of the ddl builder.
//
// ddl.CreateTable's typestate requires at least one column, and a table whose
// body is a SELECT has no column list at all — SQLite answers "near AS: syntax
// error" to one, measured on 3.50.3. That is the same reason pg.CreateTableLike
// and mysql.CreateTableAs are their own statements, and it is why this needs
// nothing from ddl.

const kwTable = "TABLE"

// CreateTableAsStatement is CREATE TABLE ... AS SELECT.
type CreateTableAsStatement struct {
	t                      sqlb.Table
	body                   *SelectQuery
	temporary, ifNotExists bool
}

// CreateTableAs creates t from the result of a query, deriving each column's
// name and type from the select list and filling the table in the same
// statement. It is how a staging copy or a backfill is made in one pass.
//
// **The new table keeps none of the source's constraints**, and that is the
// warning worth reading twice rather than a footnote. Measured on 3.50.3
// against a source declared `id INTEGER PRIMARY KEY, name TEXT NOT NULL`, the
// copy comes out `id INT, name TEXT`: the primary key is gone, the NOT NULL is
// gone, and the type is SQLite's reading of the expression rather than the
// declaration. A column computed by an expression gets no declared type at
// all, so it has blob affinity and will hold whatever it is given. A table
// whose shape matters is created by CreateTable, which declares it, and filled
// by an INSERT from a SELECT.
//
// There are no table options here and no column list, because SQLite has
// neither on this form: WITHOUT ROWID is a syntax error after a select, and a
// column list is a syntax error before the AS. Beware that STRICT is *not*
// refused — it parses as an alias for the last table in the select, so it
// silently does nothing, which is why it is not offered.
func CreateTableAs(t sqlb.Table, body *SelectQuery) *CreateTableAsStatement {
	return &CreateTableAsStatement{t: t, body: body}
}

// IfNotExists makes creating an existing table a no-op, and on this form that
// means the query does not run either: the table keeps the rows it had and
// nothing is appended. Measured, because "create it and fill it" is one
// statement here and a second run appending its rows again would be the
// difference between a repeatable install and one that doubles its data.
func (x *CreateTableAsStatement) IfNotExists() *CreateTableAsStatement {
	c := *x
	c.ifNotExists = true
	return &c
}

// Temporary makes the new table a temporary one, in the temp schema and gone
// when the connection closes; see Temporary for the pooling warning that comes
// with every temporary object. A table naming a schema other than temp fails
// Build with ErrTemporaryTable, as the engine does.
func (x *CreateTableAsStatement) Temporary() *CreateTableAsStatement {
	c := *x
	c.temporary = true
	return &c
}

// Build renders the statement.
func (x *CreateTableAsStatement) Build() (ddl.Script, error) {
	if x.body == nil {
		return nil, ddl.ErrZeroValue
	}
	if x.temporary && x.t.Schema != "" && x.t.Schema != schemaTemp {
		return nil, ErrTemporaryTable
	}
	return buildOne(x.t.Name, func(a *sqlb.Args) string {
		var b strings.Builder
		x.body.WriteSQL(&b, a)
		head := kwCreate
		if x.temporary {
			head += " " + kwTemporary
		}
		head += " " + kwTable + ifNotExists(x.ifNotExists)
		return head + " " + x.t.Qualified(a.Dialect()) + " " + kwAs + " " + b.String()
	})
}
