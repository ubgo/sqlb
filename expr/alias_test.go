package expr_test

import (
	"testing"

	"github.com/ubgo/sqlb/dialect/postgres"
	"github.com/ubgo/sqlb/expr"
	"github.com/ubgo/sqlb/render"
)

// TestTable_UnaliasedQualifiesByName is the fallback that was missing.
//
// A table with no alias rendered "FROM "t" AS """ and qualified its columns as
// """."c"" — a quoted empty identifier. Postgres's scanner rejects that
// outright with "zero-length delimited identifier"; SQLite accepts it, which
// is exactly why nothing here caught it, since SQLite is the only engine this
// repository can execute.
//
// The shape that produces it is ordinary rather than exotic. A CTE is
// referenced by its own name and has no alias to give, so Table{Name: "x"} is
// the natural thing to write, and it is what rebuilding another library's
// headline query produced on the first attempt.
//
// contrib/structbind already had the fallback, which is the tell: the core was
// the outlier, not the convention.
func TestTable_UnaliasedQualifiesByName(t *testing.T) {
	for _, tc := range []struct {
		name       string
		table      expr.Table
		wantSource string
		wantCol    string
	}{
		{
			name:       "aliased",
			table:      expr.Table{Name: "users", Alias: "u"},
			wantSource: `"users" AS "u"`,
			wantCol:    `"u"."id"`,
		},
		{
			name:       "unaliased",
			table:      expr.Table{Name: "regional_sales"},
			wantSource: `"regional_sales"`,
			wantCol:    `"regional_sales"."id"`,
		},
		{
			// The column qualifies by the table's own name, never by the
			// schema: a column of "public"."users" is still "users"."id".
			name:       "schema, no alias",
			table:      expr.Table{Schema: "public", Name: "users"},
			wantSource: `"public"."users"`,
			wantCol:    `"users"."id"`,
		},
		{
			name:       "schema and alias",
			table:      expr.Table{Schema: "public", Name: "users", Alias: "u"},
			wantSource: `"public"."users" AS "u"`,
			wantCol:    `"u"."id"`,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			d := postgres.Dialect{}
			if got := tc.table.SQL(d); got != tc.wantSource {
				t.Errorf("source\n got %s\nwant %s", got, tc.wantSource)
			}
			a := render.NewArgs(d)
			if got := expr.Col[int64](tc.table, "id").SQL(a); got != tc.wantCol {
				t.Errorf("column\n got %s\nwant %s", got, tc.wantCol)
			}
			if got := tc.table.Qualifier(d); got != quoteOf(tc.wantCol) {
				t.Errorf("Qualifier got %s, want %s", got, quoteOf(tc.wantCol))
			}
		})
	}
}

// quoteOf returns the qualifier part of a rendered column, "u" from "u"."id".
func quoteOf(col string) string {
	for i := 1; i < len(col); i++ {
		if col[i] == '.' {
			return col[:i]
		}
	}
	return col
}

// TestTable_SourceAliasFallsBackToName pins what the duplicate-alias check
// keys on.
//
// Returning "" for every unaliased table put them all under one key, so two
// different tables collided and a genuine self-join with no aliases — which
// really is ambiguous — was indistinguishable from it.
func TestTable_SourceAliasFallsBackToName(t *testing.T) {
	if got := (expr.Table{Name: "users", Alias: "u"}).SourceAlias(); got != "u" {
		t.Errorf("aliased: got %q, want %q", got, "u")
	}
	if got := (expr.Table{Name: "users"}).SourceAlias(); got != "users" {
		t.Errorf("unaliased: got %q, want %q", got, "users")
	}
}
