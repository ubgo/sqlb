package mysql

import (
	"strings"

	"github.com/ubgo/sqlb"
	"github.com/ubgo/sqlb/ddl"
)

// The forms of CREATE TABLE and DROP TABLE that are not the ordinary one.
//
// These were missing while the detector said `built CreateTable`, which is
// the defect that audit found: a `built` entry names a constructor and says
// nothing about the forms inside the statement, so `CREATE TABLE ... LIKE`,
// `CREATE TABLE ... AS SELECT` and `TEMPORARY` were absent with nothing to
// report them. Each is here now and each is executed.
//
// Two shapes, for the reason `pg.CreateTableLike` already has one: the
// ordinary `CREATE TABLE`'s typestate requires a column before it will build,
// and a table defined by `LIKE` or by a query has none, so neither can be a
// method on it. `TEMPORARY` is different and is a clause, because `ddl`'s
// `PositionModifier` is exactly the position between `CREATE` and `TABLE`, so
// the full body stays available.

const (
	kwCreateTable     = "CREATE TABLE"
	kwTemporary       = "TEMPORARY"
	kwLike            = "LIKE"
	kwDropTempTable   = "DROP TEMPORARY TABLE"
	kwIgnoreRows      = "IGNORE"
	kwReplaceRows     = "REPLACE"
	kwRenameTableSep  = ", "
	kwCreateTempTable = "CREATE TEMPORARY TABLE"
)

// Temporary makes the table a temporary one: visible to this connection only,
// dropped when it closes, and free to shadow a permanent table of the same
// name — which is the hazard as well as the point, since a query naming that
// table then reads the temporary one with nothing to say so.
//
// It is a clause at ddl's modifier position, between CREATE and TABLE, so a
// temporary table takes the same columns, keys and options as any other.
func Temporary() TableClause {
	return ddl.NewTableClause[Dialect](ddl.PositionModifier, func(*sqlb.Args) string { return kwTemporary })
}

// RawTableOption is a CREATE TABLE option written out, the counterpart of
// RawAlter and the same escape hatch for the same set: MySQL's storage-tuning
// table options, which are an operator's decision rather than a schema a
// library ships. Nothing validates the string.
//
// It takes the table options' rank, so a raw option is written before
// Partitioning wherever the caller places it: every option it can hold is a
// table option, and MySQL takes those in any order but only before
// PARTITION BY.
func RawTableOption(sql string) TableClause {
	return ddl.NewOptionClause[Dialect](rankTableOption, func(a *sqlb.Args) string {
		if strings.TrimSpace(sql) == "" {
			a.Fail(ddl.ErrZeroValue)
			return ""
		}
		return sql
	})
}

// createHead writes CREATE [TEMPORARY] TABLE [IF NOT EXISTS] name, which the
// two alternative forms share with the ordinary statement.
func createHead(a *sqlb.Args, t sqlb.Table, temporary, absent bool) string {
	kw := kwCreateTable
	if temporary {
		kw = kwCreateTempTable
	}
	return kw + ifNotExists(absent) + " " + t.Qualified(a.Dialect())
}

// CreateTableLikeStatement is CREATE TABLE ... LIKE.
type CreateTableLikeStatement struct {
	t, src      sqlb.Table
	temporary   bool
	ifNotExists bool
}

// CreateTableLike creates t with the structure of src: every column, every
// index and the table's attributes, and no rows. MySQL copies no foreign key
// and no table comment, which is the difference from a dump; it is how a
// staging table that must match a real one exactly is made without restating
// the definition and letting the two drift.
func CreateTableLike(t, src sqlb.Table) *CreateTableLikeStatement {
	return &CreateTableLikeStatement{t: t, src: src}
}

// IfNotExists makes creating an existing table a no-op.
func (x *CreateTableLikeStatement) IfNotExists() *CreateTableLikeStatement {
	c := *x
	c.ifNotExists = true
	return &c
}

// Temporary makes the copy a temporary table; see Temporary.
func (x *CreateTableLikeStatement) Temporary() *CreateTableLikeStatement {
	c := *x
	c.temporary = true
	return &c
}

// Build renders the statement.
func (x *CreateTableLikeStatement) Build() (ddl.Script, error) {
	if x.src.Name == "" {
		return nil, ErrEmptyName
	}
	return buildOne(x.t.Name, func(a *sqlb.Args) string {
		return createHead(a, x.t, x.temporary, x.ifNotExists) + " " + kwLike + " " + x.src.Qualified(a.Dialect())
	})
}

// RowConflict is what CREATE TABLE ... AS SELECT does with a row the new
// table's own unique key refuses. It matters only when the table is created
// with keys, which happens when the query's source columns carry them.
type RowConflict int

// The conflict behaviours.
const (
	// ConflictIgnore skips the duplicate row and continues.
	ConflictIgnore RowConflict = iota + 1
	// ConflictReplace deletes the row already there and inserts the new one.
	ConflictReplace
)

func (c RowConflict) keyword() (string, bool) {
	switch c {
	case ConflictIgnore:
		return kwIgnoreRows, true
	case ConflictReplace:
		return kwReplaceRows, true
	}
	return "", false
}

// CreateTableAsStatement is CREATE TABLE ... AS SELECT.
type CreateTableAsStatement struct {
	t           sqlb.Table
	body        *SelectQuery
	temporary   bool
	ifNotExists bool
	conflict    RowConflict
}

// CreateTableAs creates t from the result of a query, deriving every column's
// name and type from the select list and filling the table in the same
// statement. It is how a backfill or a staging copy is made in one pass.
//
// The derived types are MySQL's reading of the expressions, not this module's
// declarations, so a column built from an expression may come out wider or
// narrower than the one it was computed from; a table whose shape matters
// should be created by CreateTable or CreateTableLike and filled by an
// INSERT ... SELECT instead.
func CreateTableAs(t sqlb.Table, body *SelectQuery) *CreateTableAsStatement {
	return &CreateTableAsStatement{t: t, body: body}
}

// IfNotExists makes creating an existing table a no-op, and on this form that
// means the query does not run either: the table keeps the rows it had and
// nothing is appended. Measured on 8.4, because older MySQL did append here
// and the behaviour is the difference between a repeatable install and one
// that doubles its rows on the second run.
func (x *CreateTableAsStatement) IfNotExists() *CreateTableAsStatement {
	c := *x
	c.ifNotExists = true
	return &c
}

// Temporary makes the new table a temporary one; see Temporary.
func (x *CreateTableAsStatement) Temporary() *CreateTableAsStatement {
	c := *x
	c.temporary = true
	return &c
}

// OnConflict says what to do with a row the new table's own unique key
// refuses.
func (x *CreateTableAsStatement) OnConflict(c RowConflict) *CreateTableAsStatement {
	n := *x
	n.conflict = c
	return &n
}

// Build renders the statement.
func (x *CreateTableAsStatement) Build() (ddl.Script, error) {
	if x.body == nil {
		return nil, ddl.ErrZeroValue
	}
	return buildOne(x.t.Name, func(a *sqlb.Args) string {
		s := createHead(a, x.t, x.temporary, x.ifNotExists)
		if x.conflict != 0 {
			kw, ok := x.conflict.keyword()
			if !ok {
				a.Fail(ErrUnknownKeyword)
				return ""
			}
			s += " " + kw
		}
		var b strings.Builder
		x.body.WriteSQL(&b, a)
		return s + " " + kwAs + " " + b.String()
	})
}

// DropTemporaryTableStatement is DROP TEMPORARY TABLE.
type DropTemporaryTableStatement struct {
	tables   []sqlb.Table
	ifExists bool
}

// DropTemporaryTable drops temporary tables, one statement each.
//
// The word is not decoration: DROP TABLE on a name that is both a temporary
// table and a permanent one drops the temporary, so the permanent table
// survives a drop that looked like it removed it, while DROP TEMPORARY TABLE
// refuses a permanent table outright. It is a statement of its own rather
// than a clause because ddl's DROP TABLE writes its clauses after the table
// name and MySQL's keyword goes before it.
func DropTemporaryTable(first sqlb.Table, more ...sqlb.Table) *DropTemporaryTableStatement {
	return &DropTemporaryTableStatement{tables: append([]sqlb.Table{first}, more...)}
}

// IfExists makes dropping a missing table a no-op.
func (x *DropTemporaryTableStatement) IfExists() *DropTemporaryTableStatement {
	c := *x
	c.tables = append([]sqlb.Table(nil), x.tables...)
	c.ifExists = true
	return &c
}

// Build renders one statement per table.
func (x *DropTemporaryTableStatement) Build() (ddl.Script, error) {
	if len(x.tables) == 0 {
		return nil, ddl.ErrZeroValue
	}
	out := make(ddl.Script, 0, len(x.tables))
	for _, t := range x.tables {
		s, err := buildOne(t.Name, func(a *sqlb.Args) string {
			return kwDropTempTable + ifExists(x.ifExists) + " " + t.Qualified(a.Dialect())
		})
		if err != nil {
			return nil, err
		}
		out = append(out, s...)
	}
	return out, nil
}
