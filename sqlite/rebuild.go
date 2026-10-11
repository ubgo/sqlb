package sqlite

import (
	"errors"
	"strings"

	"github.com/ubgo/sqlb"
	"github.com/ubgo/sqlb/ddl"
)

// ErrRebuildIncomplete is returned for a rebuild with no new definition or no
// column copied.
var ErrRebuildIncomplete = errors.New("sqlb/sqlite: a rebuild needs the old table, Into and at least one Copy")

// ErrRebuildSameName is returned for a rebuild whose new table has the old
// one's name; the new one is created alongside and renamed into place.
var ErrRebuildSameName = errors.New("sqlb/sqlite: the rebuilt table needs a name of its own until the rename")

// CopyPair is one column of a rebuild: the new table's column and the
// expression over the old table that fills it.
type CopyPair struct {
	to   ddl.ColumnRef
	from sqlb.SQLer
}

// CopyAs fills a column of the new table with e, an expression of the new
// column's own Go type over the old table, so a type change is only writable
// with a conversion that produces the new type: text to integer needs a cast
// returning int64.
func CopyAs[T any](to, e sqlb.Expr[T]) CopyPair { return CopyPair{to: to, from: e} }

// RebuildQuery is a table rebuild with no new definition yet.
type RebuildQuery struct{ old sqlb.Table }

// Rebuild starts the table rebuild SQLite's documentation gives for every
// change ALTER TABLE cannot make: a column's type, a constraint, a key. It
// creates the new table, copies the rows, drops the old one and renames the
// new one into its place, with foreign-key enforcement off for the duration.
func Rebuild(old sqlb.Table) *RebuildQuery { return &RebuildQuery{old: old} }

// Into gives the new table's definition. Its table must have another name,
// which the rebuild renames to the old one at the end.
func (x *RebuildQuery) Into(def *CreateTableReady) *RebuildStatement {
	return &RebuildStatement{old: x.old, def: def}
}

// RebuildStatement is a rebuild with its new definition.
type RebuildStatement struct {
	old      sqlb.Table
	def      *CreateTableReady
	copies   []CopyPair
	recreate []interface{ Build() (ddl.Script, error) }
}

// Copy says how each column of the new table is filled from the old one.
func (x *RebuildStatement) Copy(first CopyPair, more ...CopyPair) *RebuildStatement {
	c := *x
	c.copies = append(append(append([]CopyPair(nil), x.copies...), first), more...)
	return &c
}

// Recreate names the indexes, triggers and views on the old table, which the
// DROP removes and nothing brings back. The rebuild cannot find them without
// reading the database, which ADR-0003 keeps out, so the author lists them;
// a rebuild that forgets one silently loses it, which is why the execution
// test compares sqlite_schema before and after.
func (x *RebuildStatement) Recreate(bs ...interface{ Build() (ddl.Script, error) }) *RebuildStatement {
	c := *x
	c.recreate = append(append([]interface{ Build() (ddl.Script, error) }(nil), x.recreate...), bs...)
	return &c
}

// Build renders the rebuild. It holds no BEGIN or COMMIT: its first and last
// statements, which turn foreign-key enforcement off and on, are marked
// OutsideTransaction, and a runner executes everything between them in one
// transaction. The foreign-key check before the end is MustReturnNoRows.
func (x *RebuildStatement) Build() (ddl.Script, error) {
	if x.def == nil || len(x.copies) == 0 || x.old.Name == "" {
		return nil, ErrRebuildIncomplete
	}
	created, err := x.def.Build()
	if err != nil {
		return nil, err
	}
	newT := x.def.Table()
	if newT.Name == x.old.Name {
		return nil, ErrRebuildSameName
	}
	a := sqlb.NewInlineArgs(Dialect{})
	d := a.Dialect()
	names := make([]string, len(x.copies))
	exprs := make([]sqlb.SQLer, len(x.copies))
	for i, c := range x.copies {
		if c.to == nil || c.from == nil || c.to.Name() == "" {
			return nil, ddl.ErrNotAColumn
		}
		if !onTable(c.to, newT) {
			return nil, ddl.ErrForeignColumn
		}
		names[i] = d.QuoteIdent(c.to.Name())
		exprs[i] = c.from
	}
	var sel strings.Builder
	Select(exprs...).From(x.old).WriteSQL(&sel, a)
	if err := a.Err(); err != nil {
		return nil, err
	}
	off, _ := ForeignKeys(false).Build()
	on, _ := ForeignKeys(true).Build()
	out := ddl.Script{off[0]}
	out = append(out, created...)
	out = append(out,
		ddl.Statement{SQL: "INSERT INTO " + d.QuoteIdent(newT.Name) + " (" + strings.Join(names, ", ") + ") " + sel.String(), Transactional: true},
		ddl.Statement{SQL: "DROP TABLE " + d.QuoteIdent(x.old.Name), Transactional: true},
		ddl.Statement{SQL: "ALTER TABLE " + d.QuoteIdent(newT.Name) + " " + kwRenameTo + " " + d.QuoteIdent(x.old.Name), Transactional: true},
	)
	for _, b := range x.recreate {
		s, err := b.Build()
		if err != nil {
			return nil, err
		}
		out = append(out, s...)
	}
	check, err := ForeignKeyCheck().Build()
	if err != nil {
		return nil, err
	}
	out = append(out, check[0], on[0])
	return out, nil
}
