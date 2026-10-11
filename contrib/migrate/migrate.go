// Package migrate turns a schema declaration into CREATE TABLE statements.
//
// It is the first consumer of github.com/ubgo/sqlb/schema, and it exists as
// much to prove that layer carries its weight as to be used. A schema is only
// worth describing if something can act on the description without knowing who
// wrote it, and this package acts on a Set assembled from packages that never
// imported each other.
//
// Deliberately narrow. It renders the DDL to create a schema that does not yet
// exist, in an order that satisfies foreign keys, and it stops there. It does
// not diff against a live database, because that needs introspection and
// introspection is engine-specific in a way DDL generation is not; it does not
// apply anything, because deciding when to run DDL is an operational question
// and not a library's. Both are reasonable things to build on top, and both
// would be worse hidden inside this.
//
// It lives under contrib because it targets specific engines. The core stays
// free of DDL for the same reason it stays free of drivers.
package migrate

import (
	"errors"
	"fmt"
	"strings"

	"github.com/ubgo/sqlb/render"
	"github.com/ubgo/sqlb/schema"
)

// ErrNoTypeMapping reports that an engine has no spelling for a portable type.
//
// It is an error rather than a silent fallback to TEXT. A column that was meant
// to be a timestamp and silently became text is the kind of defect that is
// found months later in the data, not at migration time.
var ErrNoTypeMapping = errors.New("migrate: engine has no mapping for this column type")

// types maps a portable schema type to each engine's spelling.
//
// The mapping lives here rather than in the schema package because it is the
// one part of DDL that is genuinely per engine, and the schema layer's whole
// value is that one declaration serves every engine. Keeping the table here
// means adding an engine is a change to this file and to nothing else.
// The per-engine spelling table lives in schema, so this renderer and sqlb
// vet, which checks declarations against a live catalogue, read one table
// and cannot disagree about what a bigint is.

// Engines reports which engines this package can render DDL for, in a stable
// order, so a caller can check before it starts rather than after.
func Engines() []string { return schema.EngineNames() }

// SQLType returns the engine's spelling for a column.
//
// A column with Raw set is returned verbatim, which is the documented escape
// hatch: the schema layer cannot express every engine's type, and pretending
// otherwise would be worse than a visible per-column override.
func SQLType(d render.Dialect, c schema.Column) (string, error) {
	if c.Raw != "" {
		return c.Raw, nil
	}
	engine := render.DialectName(d)
	t, ok := schema.EngineType(engine, c.Type)
	if !ok {
		if len(schema.GoTypes(c.Type)) == 0 {
			return "", fmt.Errorf("%w: %q has no %s type", ErrNoTypeMapping, engine, c.Type)
		}
		return "", fmt.Errorf("%w: engine %q is not one of %v", ErrNoTypeMapping, engine, Engines())
	}
	return t, nil
}

// CreateTable renders one CREATE TABLE statement.
//
// Foreign keys are rendered inline rather than as separate ALTER statements,
// which is why the creation order from Set.Sorted matters: an inline reference
// to a table that does not exist yet is rejected by every engine here.
func CreateTable(d render.Dialect, t schema.Table) (string, error) {
	if err := t.Validate(); err != nil {
		return "", err
	}
	var b strings.Builder
	b.WriteString("CREATE TABLE ")
	b.WriteString(qualified(d, t))
	b.WriteString(" (")

	parts, err := columnParts(d, t)
	if err != nil {
		return "", err
	}
	parts = append(parts, constraintParts(d, t)...)

	b.WriteString("\n\t")
	b.WriteString(strings.Join(parts, ",\n\t"))
	b.WriteString("\n)")
	return b.String(), nil
}

// columnParts renders one line per column.
func columnParts(d render.Dialect, t schema.Table) ([]string, error) {
	out := make([]string, 0, len(t.Columns))
	for _, c := range t.Columns {
		typ, err := SQLType(d, c)
		if err != nil {
			return nil, fmt.Errorf("%s.%s: %w", t.Name, c.Name, err)
		}
		line := d.QuoteIdent(c.Name) + " " + typ
		if !c.Nullable {
			line += " NOT NULL"
		}
		if c.Default != "" {
			line += " DEFAULT " + c.Default
		}
		out = append(out, line)
	}
	return out, nil
}

// constraintParts renders the primary key and the foreign keys.
func constraintParts(d render.Dialect, t schema.Table) []string {
	var out []string
	if len(t.PrimaryKey) > 0 {
		out = append(out, "PRIMARY KEY ("+quoteList(d, t.PrimaryKey)+")")
	}
	for _, fk := range t.ForeignKeys {
		line := "CONSTRAINT " + d.QuoteIdent(fk.Name) +
			" FOREIGN KEY (" + quoteList(d, fk.Columns) + ")" +
			" REFERENCES " + d.QuoteIdent(fk.RefTable) + " (" + quoteList(d, fk.RefColumns) + ")"
		if fk.OnDelete != "" {
			line += " ON DELETE " + string(fk.OnDelete)
		}
		if fk.OnUpdate != "" {
			line += " ON UPDATE " + string(fk.OnUpdate)
		}
		out = append(out, line)
	}
	return out
}

// CreateIndexes renders one CREATE INDEX statement per declared index.
//
// They are separate from CreateTable because every engine here accepts indexes
// only after the table exists, and because an index is the part of a schema a
// caller most often wants to create later or not at all.
func CreateIndexes(d render.Dialect, t schema.Table) []string {
	out := make([]string, 0, len(t.Indexes))
	for _, ix := range t.Indexes {
		kw := "CREATE INDEX "
		if ix.Unique {
			kw = "CREATE UNIQUE INDEX "
		}
		out = append(out, kw+d.QuoteIdent(ix.Name)+" ON "+qualified(d, t)+" ("+quoteList(d, ix.Columns)+")")
	}
	return out
}

// Plan renders every statement needed to create a whole schema, in an order
// that can be executed top to bottom.
//
// This is the function the schema layer was built for. The ordering comes from
// Set.Sorted, which resolves foreign keys across contributors, so a library and
// an application are migrated together by code that imported neither.
func Plan(d render.Dialect, s schema.Set) ([]string, error) {
	tables, err := s.Sorted()
	if err != nil {
		return nil, err
	}
	var out []string
	for _, t := range tables {
		ddl, err := CreateTable(d, t)
		if err != nil {
			return nil, err
		}
		out = append(out, ddl)
	}
	// Indexes come after every table, not after each one, so that a failure
	// partway through leaves a set of tables rather than a half-indexed one.
	for _, t := range tables {
		out = append(out, CreateIndexes(d, t)...)
	}
	return out, nil
}

func qualified(d render.Dialect, t schema.Table) string {
	if t.Schema == "" {
		return d.QuoteIdent(t.Name)
	}
	return d.QuoteIdent(t.Schema) + "." + d.QuoteIdent(t.Name)
}

func quoteList(d render.Dialect, names []string) string {
	out := make([]string, len(names))
	for i, n := range names {
		out[i] = d.QuoteIdent(n)
	}
	return strings.Join(out, ", ")
}
