package scan_test

import (
	"go/build"
	"strings"
	"testing"

	"github.com/ubgo/sqlb/dialect/postgres"
	"github.com/ubgo/sqlb/expr"
	"github.com/ubgo/sqlb/render"
	"github.com/ubgo/sqlb/scan"
)

var (
	people = expr.Table{Name: "people", Alias: "p"}
	email  = expr.Col[string](people, "email")
	age    = expr.Col[int](people, "age")
	city   = expr.Col[string](people, "city")
)

// render renders a Reader's columns the way a statement would, so the test
// asserts on what the database will actually be asked for.
func columns[T any](r scan.Reader[T]) string {
	a := render.NewArgs(postgres.Dialect{})
	var out []string
	for _, c := range r.Columns() {
		out = append(out, c.SQL(a))
	}
	return strings.Join(out, ", ")
}

// TestMap2_PreservesColumnOrder is the invariant the whole design rests on.
// Composition selects a's columns then b's, and the scan destinations are
// allocated in the same order, so a Reader built from six Map2 calls still
// lines its values up with its columns.
func TestMap2_PreservesColumnOrder(t *testing.T) {
	type person struct {
		Email string
		Age   int
		City  string
	}
	r := scan.Map2(
		scan.Map2(scan.Field(email), scan.Field(age),
			func(e string, a int) person { return person{Email: e, Age: a} }),
		scan.Field(city),
		func(p person, c string) person { p.City = c; return p },
	)

	if got, want := columns(r), `"p"."email", "p"."age", "p"."city"`; got != want {
		t.Fatalf("got  %s\nwant %s", got, want)
	}
}

// TestMap_KeepsColumnsUnchanged pins that wrapping a value in a domain type
// changes what comes back and not what is asked for.
func TestMap_KeepsColumnsUnchanged(t *testing.T) {
	type userID string
	r := scan.Map(scan.Field(email), func(s string) userID { return userID(s) })
	if got, want := columns(r), `"p"."email"`; got != want {
		t.Fatalf("got %s, want %s", got, want)
	}
}

// TestReader_ZeroValueSelectsNothing pins that the only unusable Reader is the
// one nobody constructed, which is what All and One check before building a
// statement that could not be valid.
func TestReader_ZeroValueSelectsNothing(t *testing.T) {
	if n := len(scan.Reader[int]{}.Columns()); n != 0 {
		t.Fatalf("the zero Reader selects %d columns, want 0", n)
	}
}

// allowed is what this layer may import. It needs exec to run a statement and
// stmt to describe one; it must not reach for an engine, and never for the root.
var allowed = map[string]bool{
	"github.com/ubgo/sqlb/exec":   true,
	"github.com/ubgo/sqlb/expr":   true,
	"github.com/ubgo/sqlb/render": true,
	"github.com/ubgo/sqlb/stmt":   true,
}

// TestScan_ImportsOnlyTheLayersBelow keeps this a peer of the binding API
// rather than something layered on top of it: it must not import a result
// strategy, because it is one.
func TestScan_ImportsOnlyTheLayersBelow(t *testing.T) {
	pkg, err := build.ImportDir(".", 0)
	if err != nil {
		t.Fatal(err)
	}
	for _, imp := range pkg.Imports {
		if strings.Contains(imp, ".") && !allowed[imp] {
			t.Errorf("scan imports %q, which is not one of the layers below it", imp)
		}
	}
}
