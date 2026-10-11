package render_test

import (
	"errors"
	"go/build"
	"strings"
	"testing"

	"github.com/ubgo/sqlb/render"
)

// gremlinish is a dialect for a target that is not SQL. It exists to hold the
// claim that render says nothing about SQL: if a construct, an operator or a
// keyword ever leaks into this package, writing this type will stop compiling
// or stop making sense.
//
// Identifiers are unquoted, because a traversal has no quoted identifiers, and
// parameters are named bindings rather than positional markers.
type gremlinish struct{}

func (gremlinish) Placeholder(n int) string       { return "_" + string(rune('0'+n)) }
func (gremlinish) QuoteIdent(ident string) string { return ident }
func (gremlinish) Name() string                   { return "gremlinish" }

// TestRender_ServesANonSQLTarget is the whole reason the layer was split out.
// A target with no quoting and named bindings drives Args unchanged, which is
// what "any target whose wire form is text" has to mean in practice.
func TestRender_ServesANonSQLTarget(t *testing.T) {
	a := render.NewArgs(gremlinish{})
	got := render.Raw(func(a *render.Args) string {
		return "has(" + a.Dialect().QuoteIdent("email") + "," + a.Next("a@example.com") + ")"
	}).SQL(a)

	if want := "has(email,_1)"; got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
	if len(a.Values()) != 1 || a.Values()[0] != "a@example.com" {
		t.Fatalf("value not bound: %v", a.Values())
	}
	if a.Err() != nil {
		t.Fatalf("unexpected error: %v", a.Err())
	}
}

// TestRender_ImportsOnlyStdlib pins the floor. render is the bottom of the
// module because the next level down is a driver, which is third-party by
// definition, so a non-stdlib import here would mean the floor had moved.
func TestRender_ImportsOnlyStdlib(t *testing.T) {
	pkg, err := build.ImportDir(".", 0)
	if err != nil {
		t.Fatal(err)
	}
	for _, imp := range pkg.Imports {
		if strings.Contains(imp, ".") {
			t.Errorf("render imports %q; it must import nothing outside the standard library", imp)
		}
	}
}

// TestArgs_FirstErrorWins pins the sticky-error rule that lets every render
// function keep a single string return value while Build still fails closed.
func TestArgs_FirstErrorWins(t *testing.T) {
	a := render.NewArgs(gremlinish{})
	first := errors.New("first")
	a.Fail(first)
	a.Fail(errors.New("second"))
	a.Unsupported("some.Construct")

	if !errors.Is(a.Err(), first) {
		t.Fatalf("later errors overwrote the first: %v", a.Err())
	}
}

// TestArgs_UnsupportedNamesConstructAndDialect keeps the error message useful:
// it is the only thing a caller sees when a construct meets the wrong engine.
func TestArgs_UnsupportedNamesConstructAndDialect(t *testing.T) {
	a := render.NewArgs(gremlinish{})
	a.Unsupported("pg.Merge")

	if !errors.Is(a.Err(), render.ErrUnsupportedDialect) {
		t.Fatalf("want ErrUnsupportedDialect, got %v", a.Err())
	}
	msg := a.Err().Error()
	for _, want := range []string{"pg.Merge", "gremlinish"} {
		if !strings.Contains(msg, want) {
			t.Errorf("error %q does not name %q", msg, want)
		}
	}
}
