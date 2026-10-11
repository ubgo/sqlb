package vet

import (
	"testing"

	"github.com/ubgo/sqlb/schema"
)

// TestCompareDefaults_MeasuredSpellings pins the comparison against what each
// engine printed for the same DDL, measured on Postgres 17, MySQL 8.4 and
// SQLite 3.50; the DDL column is what the dialects write.
func TestCompareDefaults_MeasuredSpellings(t *testing.T) {
	s := func(v string) *string { return &v }
	mysql := func(v string) *string { return mysqlCatalogueDefault(&v, "") }
	mysqlExpr := func(v string) *string { return mysqlCatalogueDefault(&v, "DEFAULT_GENERATED") }
	cases := []struct {
		name string
		ddl  string
		cat  *string
		want defaultVerdict
	}{
		{"pg string with a quote", `'act''ive'`, s(`'act''ive'::text`), defaultsEqual},
		{"pg negative number, quoted and cast", `-5`, s(`'-5'::integer`), defaultsEqual},
		{"pg boolean", `true`, s(`true`), defaultsEqual},
		{"pg float", `1.5`, s(`1.5`), defaultsEqual},
		{"pg escape string", `E'a\\b'`, s(`'a\b'::text`), defaultsEqual},
		{"pg empty string", `''`, s(`''::text`), defaultsEqual},
		{"pg expression", `(now())`, s(`now()`), defaultsEqual},
		{"pg timestamp literal", `'2026-01-02 03:04:05+00:00'`, s(`'2026-01-02 03:04:05+00'::timestamp with time zone`), defaultsEqual},
		{"pg DEFAULT NULL is no default", ``, s(`NULL::text`), defaultsEqual},
		{"pg changed string", `'active'`, s(`'banned'::text`), defaultsDiffer},
		{"pg changed number", `5`, s(`6`), defaultsDiffer},
		{"mysql string", `'act''ive'`, mysql(`act'ive`), defaultsEqual},
		{"mysql TRUE reads back as 1", `TRUE`, mysql(`1`), defaultsEqual},
		{"mysql FALSE is not 1", `FALSE`, mysql(`1`), defaultsDiffer},
		{"mysql DECIMAL pads to its scale", `2.5`, mysql(`2.50`), defaultsEqual},
		{"mysql negative", `-5`, mysql(`-5`), defaultsEqual},
		{"mysql empty string", `''`, mysql(``), defaultsEqual},
		{"mysql timestamp", `'2026-01-02 03:04:05'`, mysql(`2026-01-02 03:04:05`), defaultsEqual},
		{"mysql expression", `(UTC_TIMESTAMP())`, mysqlExpr(`utc_timestamp()`), defaultsEqual},
		{"mysql string expression with an introducer", `('j')`, mysqlExpr(`_latin1\'j\'`), defaultsEqual},
		{"mysql string that looks like a number stays a string", `'05'`, mysql(`5`), defaultsDiffer},
		{"sqlite as written", `'active'`, s(`'active'`), defaultsEqual},
		{"a boolean against the word t", `true`, s(`'t'`), defaultsEqual},
		{"a boolean against the word false", `false`, s(`'false'`), defaultsEqual},
		{"sqlite boolean as integer", `1`, s(`1`), defaultsEqual},
		{"sqlite expression", `(datetime('now'))`, s(`(datetime('now'))`), defaultsEqual},
		{"a default the database lacks", `'x'`, nil, defaultsDiffer},
		{"a default the DDL lacks", ``, s(`'x'`), defaultsDiffer},
		{"neither has one", ``, nil, defaultsEqual},
		{"two different expressions", `(now())`, s(`clock_timestamp()`), defaultsMayDiffer},
		{"a literal against an expression", `'x'`, s(`upper('x')`), defaultsMayDiffer},
		{"an expression with a cast inside", `(now() AT TIME ZONE 'utc')`, s(`(now() AT TIME ZONE 'utc'::text)`), defaultsEqual},
		{"parentheses that do not wrap the whole", `(1) + (2)`, s(`(1) + (2)`), defaultsEqual},
		{"a quote inside does not end the literal", `'a''b'`, s(`'a''b'`), defaultsEqual},
	}
	for _, c := range cases {
		if got := compareDefaults(c.ddl, c.cat); got != c.want {
			t.Errorf("%s: compareDefaults(%q, %v) = %d, want %d", c.name, c.ddl, deref(c.cat), got, c.want)
		}
	}
}

// TestParseDefault_Kinds pins the classification the comparison rests on.
func TestParseDefault_Kinds(t *testing.T) {
	cases := map[string]parsedDefault{
		``:                       {},
		`NULL`:                   {},
		`(NULL)`:                 {},
		`'x'`:                    {kind: kindString, value: "x"},
		`'it''s'`:                {kind: kindString, value: "it's"},
		`E'a\\b'`:                {kind: kindString, value: `a\b`},
		`'a'::text`:              {kind: kindString, value: "a"},
		`'a'::character varying`: {kind: kindString, value: "a"},
		`42`:                     {kind: kindNumber, value: "42"},
		`-1.5e3`:                 {kind: kindNumber, value: "-1.5e3"},
		`TRUE`:                   {kind: kindBool, value: "1"},
		`false`:                  {kind: kindBool, value: "0"},
		`now()`:                  {kind: kindExpression, value: "now"},
		`'a' || 'b'`:             {kind: kindExpression, value: "'a'||'b'"},
		`'unterminated`:          {kind: kindExpression, value: "'unterminated"},
	}
	for in, want := range cases {
		if got := parseDefault(in); got != want {
			t.Errorf("parseDefault(%q) = %+v, want %+v", in, got, want)
		}
	}
	if describeDefault("") != "none" || describeDefault("NULL") != "none" || describeDefault("'x'") != "'x'" {
		t.Error("describeDefault")
	}
	if mysqlCatalogueDefault(nil, "") != nil {
		t.Error("a NULL COLUMN_DEFAULT is no default")
	}
}

// TestCompareDefaultsAs_JSON pins that a JSON column's default is compared as
// JSON, the spacing Postgres normalises away included, and that a text column
// is still compared exactly.
func TestCompareDefaultsAs_JSON(t *testing.T) {
	s := func(v string) *string { return &v }
	if got := compareDefaultsAs(`('{"a":1}')`, s(`'{"a": 1}'::jsonb`), true); got != defaultsEqual {
		t.Errorf("jsonb spacing: %d", got)
	}
	if got := compareDefaultsAs(`('{"a":1}')`, s(`_utf8mb4\'{"a": 1}\'`), true); got != defaultsEqual {
		t.Errorf("mysql json expression: %d", got)
	}
	if got := compareDefaultsAs(`('{"a":1}')`, s(`'{"a": 2}'::jsonb`), true); got != defaultsDiffer {
		t.Errorf("a different JSON value: %d", got)
	}
	if got := compareDefaultsAs(`'{"a":1}'`, s(`'{"a": 1}'`), false); got != defaultsDiffer {
		t.Errorf("a text column is compared exactly: %d", got)
	}
	if !isJSONColumn(schema.Column{Raw: "JSONB"}) || isJSONColumn(schema.Column{Raw: "text"}) || !isJSONColumn(schema.Column{Type: schema.TypeJSON}) {
		t.Error("isJSONColumn")
	}
	if jsonEqual("{", "{}") {
		t.Error("invalid JSON compared equal")
	}
}
