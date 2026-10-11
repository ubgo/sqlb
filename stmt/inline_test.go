package stmt_test

import (
	"strings"
	"testing"

	"github.com/ubgo/sqlb/dialect/postgres"
	"github.com/ubgo/sqlb/dialect/sqlite"
	"github.com/ubgo/sqlb/expr"
	"github.com/ubgo/sqlb/render"
	"github.com/ubgo/sqlb/stmt"
)

// TestInline_ASelectRendersWithLiterals is the view body a DDL builder will
// write: an ordinary stmt.Select, rendered through inline Args, so every value
// its constructs bind lands in the text as a literal the dialect wrote. No
// construct changed to allow it; the Args decided.
func TestInline_ASelectRendersWithLiterals(t *testing.T) {
	users := expr.Table{Name: "users", Alias: "u"}
	id := expr.Col[int64](users, "id")
	status := expr.Col[string](users, "status")
	body := stmt.NewSelect(postgres.Dialect{}).Exprs(id).From(users).
		Where(expr.Eq(status, "active"), expr.Greater(id, int64(10))).Limit(5)

	for _, tc := range []struct {
		name string
		d    render.Dialect
		want string
	}{
		{"postgres", postgres.Dialect{}, `SELECT "u"."id" FROM "users" AS "u" WHERE ("u"."status" = 'active' AND "u"."id" > 10) LIMIT 5`},
		{"sqlite", sqlite.Dialect{}, `SELECT "u"."id" FROM "users" AS "u" WHERE ("u"."status" = 'active' AND "u"."id" > 10) LIMIT 5`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			a := render.NewInlineArgs(tc.d)
			var b strings.Builder
			body.WriteSQL(&b, a)
			if err := a.Err(); err != nil {
				t.Fatal(err)
			}
			if b.String() != tc.want {
				t.Fatalf("got  %s\nwant %s", b.String(), tc.want)
			}
			if len(a.Values()) != 0 {
				t.Fatalf("an inline body bound %v", a.Values())
			}
		})
	}
}

// TestInline_ABackslashValueTakesTheEForm pins that the Postgres rule reaches
// a value bound by an ordinary construct, not only an engine's constants: the
// same Where that binds in a query writes E'...' in a view body.
func TestInline_ABackslashValueTakesTheEForm(t *testing.T) {
	users := expr.Table{Name: "users", Alias: "u"}
	path := expr.Col[string](users, "path")
	a := render.NewInlineArgs(postgres.Dialect{})
	var b strings.Builder
	stmt.NewSelect(postgres.Dialect{}).Exprs(path).From(users).Where(expr.Eq(path, `C:\tmp`)).WriteSQL(&b, a)
	if err := a.Err(); err != nil {
		t.Fatal(err)
	}
	if want := `SELECT "u"."path" FROM "users" AS "u" WHERE ("u"."path" = E'C:\\tmp')`; b.String() != want {
		t.Fatalf("got  %s\nwant %s", b.String(), want)
	}
}
