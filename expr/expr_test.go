package expr_test

import (
	"go/build"
	"strings"
	"testing"

	"github.com/ubgo/sqlb/dialect/postgres"
	"github.com/ubgo/sqlb/dialect/sqlite"
	"github.com/ubgo/sqlb/expr"
	"github.com/ubgo/sqlb/render"
)

// allowedImport is the only non-stdlib import the expression layer may have.
// It may reach down to render and nowhere else: not to the statement layer, not
// to an engine package, and never back to the root.
const allowedImport = "github.com/ubgo/sqlb/render"

// TestExpr_ImportsOnlyRender is the layering rule as a test rather than as a
// convention. Every layer compiles and is useful without anything above it, and
// the cheapest way for that to stop being true is an innocent-looking import.
func TestExpr_ImportsOnlyRender(t *testing.T) {
	pkg, err := build.ImportDir(".", 0)
	if err != nil {
		t.Fatal(err)
	}
	for _, imp := range pkg.Imports {
		if strings.Contains(imp, ".") && imp != allowedImport {
			t.Errorf("expr imports %q; it may import only the standard library and %s", imp, allowedImport)
		}
	}
}

// TestExpr_BuildsWithoutAStatement is what "useful without anything above it"
// means concretely. A typed column, a bound parameter and a predicate all
// render on their own, with no Select in sight.
func TestExpr_BuildsWithoutAStatement(t *testing.T) {
	users := expr.Table{Name: "app_user", Alias: "u"}
	email := expr.Col[string](users, "email")

	a := render.NewArgs(postgres.Dialect{})

	var b strings.Builder
	expr.Eq(email, "a@example.com").WriteSQL(&b, a)

	if want := `"u"."email" = $1`; b.String() != want {
		t.Fatalf("got %q, want %q", b.String(), want)
	}
	if len(a.Values()) != 1 || a.Values()[0] != "a@example.com" {
		t.Fatalf("value not bound: %v", a.Values())
	}
}

// TestExpr_TypeSafetySurvivesTheSplit pins that the type parameter still means
// something after the move. These are the comparisons that must keep working;
// the ones that must NOT compile live in examples/typecheck, behind a build tag
// the gate tries to build and fails if it ever succeeds.
func TestExpr_TypeSafetySurvivesTheSplit(t *testing.T) {
	users := expr.Table{Name: "app_user", Alias: "u"}
	name := expr.Col[string](users, "name")
	age := expr.Col[int](users, "age")

	a := render.NewArgs(postgres.Dialect{})
	var b strings.Builder
	expr.And(
		expr.Eq(name, "ada"),
		expr.Greater(age, 30),
		expr.EqExpr(name, expr.Param("ada")),
	).WriteSQL(&b, a)

	if got := len(a.Values()); got != 3 {
		t.Fatalf("bound %d values, want 3: %v", got, a.Values())
	}
	if !strings.Contains(b.String(), `"u"."age" > $2`) {
		t.Fatalf("unexpected SQL: %s", b.String())
	}
}

// TestUpper_Renders pins the core upper() beside lower().
func TestUpper_Renders(t *testing.T) {
	a := render.NewArgs(postgres.Dialect{})
	got := expr.Upper(expr.Col[string](expr.Table{Name: "u"}, "email")).SQL(a)
	if got != `upper("u"."email")` {
		t.Fatalf("got %s", got)
	}
}

// TestConcat_TypesABareParameterWhereTheDialectAsks pins that only a bare
// placeholder argument is cast, and only for a dialect implementing
// render.UntypedParameters: Postgres casts, SQLite does not, and a column is
// never cast on either.
func TestConcat_TypesABareParameterWhereTheDialectAsks(t *testing.T) {
	email := expr.Col[string](expr.Table{Name: "u"}, "email")
	c := expr.Concat(email, expr.Param("!"), expr.Lower(expr.Param("X")))
	if got := c.SQL(render.NewArgs(postgres.Dialect{})); got != `concat("u"."email", CAST($1 AS text), lower($2))` {
		t.Errorf("postgres: %s", got)
	}
	if got := c.SQL(render.NewArgs(sqlite.Dialect{})); got != `concat("u"."email", ?, lower(?))` {
		t.Errorf("sqlite: %s", got)
	}
}

// TestCol_ByTableName pins the third way a column renders: qualified by its
// table's name rather than its alias, which MySQL's upsert needs.
func TestCol_ByTableName(t *testing.T) {
	c := expr.Col[int64](expr.Table{Name: "items", Alias: "i", Schema: "s"}, "hits")
	a := render.NewArgs(postgres.Dialect{})
	if got := a.TableNamed(func() string { return c.SQL(a) }); got != `"s"."items"."hits"` {
		t.Fatalf("got %s", got)
	}
	if got := c.SQL(a); got != `"i"."hits"` {
		t.Fatalf("the mode leaked: %s", got)
	}
}
