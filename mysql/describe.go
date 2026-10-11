package mysql

import (
	"github.com/ubgo/sqlb/ddl"
	"github.com/ubgo/sqlb/schema"
)

// The description each MySQL key and reference contributes to the
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

// DescribeInto sets the primary key, or adds a unique or plain index. A
// FULLTEXT index is not a schema.Index, which has no kind for it, so it is
// left out of the description.
func (k KeyClause) DescribeInto(t *schema.Table) {
	switch k.kw {
	case kwPrimaryKey:
		t.PrimaryKey = names(k.cols)
	case kwUnique:
		t.Indexes = append(t.Indexes, schema.Index{Name: k.name, Columns: names(k.cols), Unique: true})
	case kwIndex:
		t.Indexes = append(t.Indexes, schema.Index{Name: k.name, Columns: names(k.cols)})
	}
}

// DescribeInto adds the foreign key.
func (f ForeignKeyClause) DescribeInto(t *schema.Table) {
	if len(f.pairs) == 0 || f.pairs[0].ref == nil {
		return
	}
	refs := make([]ddl.ColumnRef, len(f.pairs))
	for i, p := range f.pairs {
		refs[i] = p.ref
	}
	target := refs[0].Table()
	t.ForeignKeys = append(t.ForeignKeys, schema.ForeignKey{
		Name: f.con.Name, Columns: names(f.NamedColumns()),
		RefTable: ddl.RefTableName(target.Schema, target.Name), RefColumns: names(refs),
		OnDelete: schemaAction(f.onDelete), OnUpdate: schemaAction(f.onUpdate),
	})
}
