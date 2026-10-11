package stmt_test

import (
	"go/build"
	"strings"
	"testing"

	"github.com/ubgo/sqlb/dialect/postgres"
	"github.com/ubgo/sqlb/expr"
	"github.com/ubgo/sqlb/stmt"
)

// allowed is what the statement layer may import: the two layers below it and
// the standard library. Not an engine package, not the execution layer, and
// never the root, which would make the whole hierarchy circular.
var allowed = map[string]bool{
	"github.com/ubgo/sqlb/render": true,
	"github.com/ubgo/sqlb/expr":   true,
}

// TestStmt_ImportsOnlyTheLayersBelow pins the direction of the dependency.
// Every layer compiles and is useful without anything above it, and an import
// pointing the wrong way is the cheapest way for that to quietly stop being
// true.
func TestStmt_ImportsOnlyTheLayersBelow(t *testing.T) {
	pkg, err := build.ImportDir(".", 0)
	if err != nil {
		t.Fatal(err)
	}
	for _, imp := range pkg.Imports {
		if strings.Contains(imp, ".") && !allowed[imp] {
			t.Errorf("stmt imports %q; it may import only the standard library, render and expr", imp)
		}
	}
}

// TestStmt_BuildsWithoutAResultStrategy is the separation the layering is for.
// A statement renders to a string and a slice of values with no scan target
// anywhere, which is what makes a Query safe to share and a second result
// strategy possible without forking this package.
func TestStmt_BuildsWithoutAResultStrategy(t *testing.T) {
	users := expr.Table{Name: "app_user", Alias: "u"}
	email := expr.Col[string](users, "email")

	q, err := stmt.NewSelect(postgres.Dialect{}).
		Exprs(email).
		From(users).
		Where(expr.Eq(email, "a@example.com")).
		Build()
	if err != nil {
		t.Fatal(err)
	}

	want := `SELECT "u"."email" FROM "app_user" AS "u" WHERE ("u"."email" = $1)`
	if q.SQL != want {
		t.Fatalf("got  %q\nwant %q", q.SQL, want)
	}
	if len(q.Args) != 1 || q.Args[0] != "a@example.com" {
		t.Fatalf("args not bound: %v", q.Args)
	}
	if len(q.Targets) != 0 {
		t.Fatalf("a statement built from Exprs must carry no scan targets, got %d", len(q.Targets))
	}
}

// TestQuery_GuardIsReachableFromAbove pins the one piece of Query state the
// execution layer needs. Acquire and Release are exported precisely because
// running a statement lives in a different package from building one; if they
// were unexported, exec would have to live here.
func TestQuery_GuardIsReachableFromAbove(t *testing.T) {
	q := stmt.NewQuery("SELECT 1", nil, nil)

	if !q.Acquire() {
		t.Fatal("first Acquire was refused")
	}
	if q.Acquire() {
		t.Fatal("second Acquire succeeded; an overlapping execution must be refused")
	}
	q.Release()
	if !q.Acquire() {
		t.Fatal("Acquire refused after Release")
	}
}

// TestQuery_LiteralHasNoGuard pins the documented hole rather than pretending
// it is closed. A struct-literal Query carries no guard, so it is always
// allowed to proceed. The guard detects the overlap it can see; it does not
// prevent the hazard, and a doc that claimed otherwise would be worse than none.
func TestQuery_LiteralHasNoGuard(t *testing.T) {
	q := stmt.Query{SQL: "SELECT 1"}
	for i := range 3 {
		if !q.Acquire() {
			t.Fatalf("Acquire refused on call %d; a struct-literal Query has no guard and must never refuse", i+1)
		}
	}
}
