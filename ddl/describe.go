package ddl

import (
	"github.com/ubgo/sqlb/render"
	"github.com/ubgo/sqlb/schema"
)

// Describer is implemented by an engine clause that adds to a table's
// description: a primary key sets schema.Table.PrimaryKey, a unique key adds a
// unique schema.Index, a foreign key a schema.ForeignKey, an auto-increment key
// its column as the key. A clause that describes nothing, UNLOGGED or STRICT,
// does not implement it, and Describe leaves it out.
type Describer interface {
	DescribeInto(t *schema.Table)
}

// ColumnDescriber is implemented by a ColumnSpec that can describe itself as a
// schema.Column for a dialect. ColumnDef does; a ColumnSpec written outside
// this package that does not is described by name alone.
type ColumnDescriber interface {
	DescribeColumn(d render.Dialect) (schema.Column, error)
}

// DescribeColumn describes the definition: its name, its type as a portable
// schema.Type when it has one and as the dialect's spelling in Raw when it
// does not, its nullability and its default as the dialect writes it.
func (c ColumnDef[T]) DescribeColumn(d render.Dialect) (schema.Column, error) {
	col := schema.Column{Name: c.col.Name(), Nullable: c.nullable()}
	switch t := c.typ.(type) {
	case nil:
		pt, err := inferType[T]()
		if err != nil {
			return schema.Column{}, err
		}
		col.Type = pt
	case portable[T]:
		col.Type = t.t
	default:
		s, err := c.typ.Spelling(d)
		if err != nil {
			return schema.Column{}, err
		}
		col.Raw = s
	}
	if c.def != nil {
		a, err := inlineArgs(d)
		if err != nil {
			return schema.Column{}, err
		}
		col.Default = a.Bare(func() string { return c.def(a, c.typ) })
		if err := a.Err(); err != nil {
			return schema.Column{}, err
		}
	}
	return col, nil
}

// Describe is the schema.Table this statement creates, derived from the
// statement rather than declared beside it, so the description a migration
// planner orders and a drift check compares cannot disagree with the DDL that
// made the table. Columns come from their definitions, and the keys and
// references from each engine clause that implements Describer; a CHECK and an
// engine clause that describes nothing are not in the description, because
// schema.Table has no place for them.
//
// A statement Build refuses is not described: the error is Build's. Without
// that, a key on a column the table does not define, or a column declared on
// another table, described cleanly and was ordered by migrate.Install and
// compared by the vet as if the DDL existed.
func (c *CreateTableReady[D]) Describe() (schema.Table, error) {
	if _, err := c.Build(); err != nil {
		return schema.Table{}, err
	}
	return c.core.describe()
}

// TableChecker is a table clause whose validity depends on the rest of the
// table: MySQL's PARTITION BY needs every primary and unique key to include
// the partition columns, and no foreign key at all. CREATE TABLE describes
// itself, as Describe does, and hands the description to each such clause; an
// error fails Build. A clause that checks only itself does not need it.
type TableChecker interface {
	CheckTable(t schema.Table) error
}

// describe builds the description Describe returns, without building the
// statement first, so the render can call it for a TableChecker.
func (c createCore[D]) describe() (schema.Table, error) {
	t := schema.Table{Name: c.table.Name, Schema: c.table.Schema, Alias: c.table.Alias}
	for _, spec := range c.columns {
		if cd, ok := spec.(ColumnDescriber); ok {
			col, err := cd.DescribeColumn(c.dialect)
			if err != nil {
				return schema.Table{}, err
			}
			t.Columns = append(t.Columns, col)
			continue
		}
		t.Columns = append(t.Columns, schema.Column{Name: spec.ColumnName()})
	}
	for _, cl := range c.clauses {
		if d, ok := cl.(Describer); ok {
			d.DescribeInto(&t)
		}
	}
	return t, nil
}

// RefTableName is how a schema.ForeignKey names the table it references: the
// key schema.Set stores a table under, schema-qualified when it has a schema.
// An engine clause describing a foreign key uses it so the Set can resolve it.
func RefTableName(schemaName, name string) string {
	if schemaName == "" {
		return name
	}
	return schemaName + "." + name
}
