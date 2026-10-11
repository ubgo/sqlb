package scan_test

import (
	"strings"
	"testing"

	"github.com/ubgo/sqlb/dialect/postgres"
	"github.com/ubgo/sqlb/expr"
	"github.com/ubgo/sqlb/render"
	"github.com/ubgo/sqlb/scan"
)

// TestJSON_SelectsOneColumn pins the shape: a JSON reader asks for exactly the
// expression it was given, and composes with Map2 like any other.
var jsonCol = expr.Col[string](people, "doc")

func TestJSON_SelectsOneColumn(t *testing.T) {
	r := scan.JSON[[]string](jsonCol)

	a := render.NewArgs(postgres.Dialect{})
	cols := r.Columns()
	if len(cols) != 1 {
		t.Fatalf("selects %d columns, want 1", len(cols))
	}
	if got, want := cols[0].SQL(a), `"p"."doc"`; got != want {
		t.Fatalf("got %s, want %s", got, want)
	}
}

// TestJSON_ComposesWithMap2 pins that it is an ordinary Reader, which is the
// reason it is a Reader rather than a separate execution path: a parent's own
// columns and its aggregated children come back in one pass.
func TestJSON_ComposesWithMap2(t *testing.T) {
	type out struct {
		Name string
		Tags []string
	}
	r := scan.Map2(
		scan.Field(email),
		scan.JSON[[]string](jsonCol),
		func(n string, tags []string) out { return out{n, tags} },
	)

	a := render.NewArgs(postgres.Dialect{})
	var got []string
	for _, c := range r.Columns() {
		got = append(got, c.SQL(a))
	}
	if want := `"p"."email", "p"."doc"`; strings.Join(got, ", ") != want {
		t.Fatalf("got %q, want %q", strings.Join(got, ", "), want)
	}
}

// TestJSON_ZeroReaderStillGuarded pins that the empty-reader check applies to
// a JSON reader too, since All and One reject a reader selecting nothing.
func TestJSON_ZeroReaderStillGuarded(t *testing.T) {
	if n := len(scan.Reader[[]string]{}.Columns()); n != 0 {
		t.Fatalf("the zero Reader selects %d columns, want 0", n)
	}
}
