package vet

import (
	"testing"

	"github.com/ubgo/sqlb/render"
	"github.com/ubgo/sqlb/schema"
)

// TestTypeAllowed_EveryEngine pins the lenient rule Compare uses, "can this
// Go type hold this column", on each engine: a fitting type, a wrong one, a
// []byte, a pointer, a caller's own type (no opinion), and a catalogue type
// with no portable reading (no opinion).
func TestTypeAllowed_EveryEngine(t *testing.T) {
	cases := []struct {
		dialect, cat, goType string
		want                 bool
	}{
		{render.DialectPostgres, "bigint", "int64", true},
		{render.DialectPostgres, "bigint", "string", false},
		{render.DialectPostgres, "bytea", "[]byte", true},
		{render.DialectPostgres, "text", "*string", true},
		{render.DialectPostgres, "text", "pg.JSON[T]", true},
		{render.DialectPostgres, "int4range", "int64", true},
		{render.DialectMySQL, "varchar", "string", true},
		{render.DialectMySQL, "bigint", "string", false},
		{render.DialectMySQL, "tinyint", "bool", true},
		{render.DialectMySQL, "geometry", "int64", true},
		{render.DialectSQLite, "integer", "int64", true},
		{render.DialectSQLite, "integer", "string", false},
		{render.DialectSQLite, "", "[]byte", true},
		{render.DialectSQLite, "blob", "[]byte", true},
		{render.DialectSQLite, "datetime", "string", true},
		{render.DialectSQLite, "double", "float64", true},
		{render.DialectSQLite, "varchar(10)", "string", true},
	}
	for _, c := range cases {
		if got := typeAllowed(c.dialect, c.cat, c.goType); got != c.want {
			t.Errorf("%s: typeAllowed(%q, %q) = %v, want %v", c.dialect, c.cat, c.goType, got, c.want)
		}
	}
}

// TestTypeIs_IsStrict pins the rule CompareTables uses, "is this the type the
// DDL created": exactly the schema table's spelling, so each lenient
// equivalence Compare accepts is drift here.
func TestTypeIs_IsStrict(t *testing.T) {
	col := func(typ, colType string) CatalogueColumn { return CatalogueColumn{Type: typ, ColumnType: colType} }
	cases := []struct {
		name    string
		dialect string
		got     CatalogueColumn
		want    schema.Type
		same    bool
	}{
		{"pg timestamptz", render.DialectPostgres, col("timestamp with time zone", ""), schema.TypeTimestamp, true},
		{"pg timestamp without zone", render.DialectPostgres, col("timestamp without time zone", ""), schema.TypeTimestamp, false},
		{"pg real for double", render.DialectPostgres, col("real", ""), schema.TypeFloat, false},
		{"pg smallint for integer", render.DialectPostgres, col("smallint", ""), schema.TypeInt, false},
		{"pg json for jsonb", render.DialectPostgres, col("json", ""), schema.TypeJSON, false},
		{"pg varchar for text", render.DialectPostgres, col("character varying", ""), schema.TypeText, false},
		{"pg numeric", render.DialectPostgres, col("numeric", ""), schema.TypeDecimal, true},
		{"mysql varchar(255)", render.DialectMySQL, col("varchar", "varchar(255)"), schema.TypeText, true},
		{"mysql varchar(100)", render.DialectMySQL, col("varchar", "varchar(100)"), schema.TypeText, false},
		{"mysql text for varchar", render.DialectMySQL, col("text", "text"), schema.TypeText, false},
		{"mysql boolean is tinyint(1)", render.DialectMySQL, col("tinyint", "tinyint(1)"), schema.TypeBool, true},
		{"mysql decimal spacing", render.DialectMySQL, col("decimal", "decimal(38,10)"), schema.TypeDecimal, true},
		{"mysql datetime precision", render.DialectMySQL, col("datetime", "datetime"), schema.TypeTimestamp, false},
		{"sqlite exact", render.DialectSQLite, col("integer", ""), schema.TypeBigInt, true},
		{"sqlite bigint is not the INTEGER written", render.DialectSQLite, col("bigint", ""), schema.TypeBigInt, false},
		{"unknown engine has no opinion", "oracle", col("x", ""), schema.TypeText, true},
	}
	for _, c := range cases {
		if got := typeIs(c.dialect, c.got, c.want); got != c.same {
			t.Errorf("%s: typeIs = %v, want %v", c.name, got, c.same)
		}
	}
}
