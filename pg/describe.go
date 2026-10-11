package pg

import (
	"github.com/ubgo/sqlb/ddl"
	"github.com/ubgo/sqlb/schema"
)

// The description each Postgres key and reference contributes to the
// schema.Table a CREATE TABLE describes, through ddl.Describer.

func names(cols []ddl.ColumnRef) []string {
	out := make([]string, len(cols))
	for i, c := range cols {
		out[i] = c.Name()
	}
	return out
}

// schemaAction maps a referential action to schema's, which spells the same
// five with the same meaning.
func schemaAction(r RefAction) schema.Action {
	if kw, ok := r.keyword(); ok {
		return schema.Action(kw)
	}
	return ""
}

// DescribeInto sets the table's primary key.
func (k PrimaryKeyClause) DescribeInto(t *schema.Table) { t.PrimaryKey = names(k.cols) }

// DescribeInto adds the constraint as a unique index.
func (u UniqueClause) DescribeInto(t *schema.Table) {
	t.Indexes = append(t.Indexes, schema.Index{Name: u.con.Name, Columns: names(u.cols), Unique: true})
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
	if f.period != nil {
		refs = append(refs, f.period[1])
	}
	target := refs[0].Table()
	t.ForeignKeys = append(t.ForeignKeys, schema.ForeignKey{
		Name: f.con.Name, Columns: names(f.NamedColumns()),
		RefTable: ddl.RefTableName(target.Schema, target.Name), RefColumns: names(refs),
		OnDelete: schemaAction(f.onDelete), OnUpdate: schemaAction(f.onUpdate),
	})
}
