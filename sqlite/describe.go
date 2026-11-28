package sqlite

import (
	"github.com/ubgo/sqlb/ddl"
	"github.com/ubgo/sqlb/schema"
)

// The description each SQLite key and reference contributes to the
// schema.Table a CREATE TABLE describes, through ddl.Describer.

func names(cols []ddl.ColumnRef) []string {
	out := make([]string, len(cols))
	for i, c := range cols {
		out[i] = c.Name()
	}
	return out
}

func schemaAction(r RefAction) schema.Action {
	if kw, ok := r.keyword(); ok {
		return schema.Action(kw)
	}
	return ""
}

// DescribeInto sets the primary key or adds the unique index, with each
// key's direction when UniqueOn was given one. A primary key's direction is
// not described, because schema.Table.PrimaryKey holds names only.
func (k KeyClause) DescribeInto(t *schema.Table) {
	if k.kw == kwPrimaryKey {
		t.PrimaryKey = names(k.cols)
		return
	}
	ix := schema.Index{Name: k.con.Name, Columns: names(k.cols), Unique: true}
	// A key made by UniqueOn states its order only when a part was given a
	// direction; otherwise the description stays as it always was, with the
	// order not stated.
	for _, p := range k.parts {
		if p.dir != "" {
			ix.Orders = make([]schema.KeyOrder, len(k.parts))
			for i, q := range k.parts {
				ix.Orders[i] = schema.KeyOrder{Direction: schema.Direction(q.dir)}
			}
			break
		}
	}
	t.Indexes = append(t.Indexes, ix)
}

// DescribeInto adds the foreign key. SQLite's references name a table in the
// same database, so the reference has no schema.
func (f ForeignKeyClause) DescribeInto(t *schema.Table) {
	if len(f.pairs) == 0 || f.pairs[0].ref == nil {
		return
	}
	refs := make([]ddl.ColumnRef, len(f.pairs))
	for i, p := range f.pairs {
		refs[i] = p.ref
	}
	t.ForeignKeys = append(t.ForeignKeys, schema.ForeignKey{
		Name: f.con.Name, Columns: names(f.NamedColumns()),
		RefTable: refs[0].Table().Name, RefColumns: names(refs),
		OnDelete: schemaAction(f.onDelete), OnUpdate: schemaAction(f.onUpdate),
	})
}

// autoincrementKey is AutoincrementKey's clause, which is the table's primary
// key as well as a column property.
type autoincrementKey struct {
	ddl.ColumnClause[Dialect]
}

// DescribeInto makes the column the primary key.
func (k autoincrementKey) DescribeInto(t *schema.Table) { t.PrimaryKey = []string{k.Target().Name()} }
