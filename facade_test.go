package sqlb_test

import (
	"testing"

	"github.com/ubgo/sqlb"
	"github.com/ubgo/sqlb/expr"
	"github.com/ubgo/sqlb/render"
)

// outsideDialect is written against the render layer alone, the way a community
// package adding an engine would write it. It never mentions sqlb.
type outsideDialect struct{}

func (outsideDialect) Placeholder(int) string         { return "?" }
func (outsideDialect) QuoteIdent(ident string) string { return render.Quote(ident, '`') }
func (outsideDialect) Name() string                   { return "outside" }

// These two declarations are the compile-time half of the split, and they are
// the reason the root uses type aliases rather than wrapper types.
//
// Each function returns its argument unchanged under the other spelling. That
// only compiles when the two names are the same type: a defined type would
// require an explicit conversion here and the build would fail. So if anyone
// turns one of these aliases into a wrapper, this file stops compiling, which
// is the warning worth having. A wrapper would fork the type and force every
// engine package to pick a spelling.
var (
	_ = func(d render.Dialect) sqlb.Dialect { return d }
	_ = func(a *sqlb.Args) *render.Args { return a }

	// The same, one layer up. Expr is generic, so these also pin that Go 1.24
	// generic type aliases preserve identity: sqlb.Expr[T] and expr.Expr[T]
	// must be one type, or a typed column declared by a library would not be
	// usable by an application that spells it the other way, which is the
	// entire reason this package exists.
	_ = func(e expr.Expr[string]) sqlb.Expr[string] { return e }
	_ = func(e sqlb.Expr[int]) expr.Expr[int] { return e }
	_ = func(p expr.Predicate) sqlb.Predicate { return p }
	_ = func(t sqlb.Table) expr.Table { return t }
)

// TestFacade_RenderReachesThroughTheRoot is the runtime half: a dialect and an
// Args built through the render layer drive the root API with no conversion
// anywhere along the way.
func TestFacade_RenderReachesThroughTheRoot(t *testing.T) {
	d := outsideDialect{}
	a := render.NewArgs(d)

	got := sqlb.Raw(func(a *sqlb.Args) string {
		return a.Dialect().QuoteIdent("email") + " = " + a.Next("x")
	}).SQL(a)

	if want := "`email` = ?"; got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
	if name := sqlb.DialectName(d); name != "outside" {
		t.Fatalf("DialectName did not reach the render layer: %q", name)
	}
}

// TestFacade_OutsideDialectDrivesTheBuilder proves the point of the facade: a
// dialect that knows only the render layer still drives the whole statement
// builder above it, with no edit to the core.
func TestFacade_OutsideDialectDrivesTheBuilder(t *testing.T) {
	users := sqlb.Table{Name: "app_user", Alias: "u"}
	email := sqlb.Col[string](users, "email")

	var dst string
	q, err := sqlb.NewSelect(outsideDialect{}).
		Columns(sqlb.Bind(email, &dst)).
		From(users).
		Where(sqlb.Eq(email, "a@example.com")).
		Build()
	if err != nil {
		t.Fatal(err)
	}

	want := "SELECT `u`.`email` FROM `app_user` AS `u` WHERE (`u`.`email` = ?)"
	if q.SQL != want {
		t.Fatalf("got  %q\nwant %q", q.SQL, want)
	}
	if len(q.Args) != 1 || q.Args[0] != "a@example.com" {
		t.Fatalf("args not bound: %v", q.Args)
	}
}

// TestFacade_SentinelsSurviveTheSplit names every error and canonical-order
// slice the root exports.
//
// These are the entries a mechanical facade most easily drops, because unlike a
// type or a function they live inside grouped var declarations that a generator
// scanning for declarations at line start will walk straight past. Each one is
// also load-bearing for a caller: an errors.Is check against a name that
// silently vanished is a compile error for them and a release note for us. So
// they are listed by hand, on purpose, and this test is the list.
func TestFacade_SentinelsSurviveTheSplit(t *testing.T) {
	for name, v := range map[string]any{
		"ErrUnsupportedDialect": sqlb.ErrUnsupportedDialect,
		"ErrMultipleRows":       sqlb.ErrMultipleRows,
		"ErrNoReturning":        sqlb.ErrNoReturning,
		"ErrNoRows":             sqlb.ErrNoRows,
		"ErrNoTargets":          sqlb.ErrNoTargets,
		"ErrQueryInUse":         sqlb.ErrQueryInUse,
		"ErrUnknownSlot":        sqlb.ErrUnknownSlot,
	} {
		if v == nil {
			t.Errorf("%s is nil", name)
		}
	}

	for name, n := range map[string]int{
		"OpValues":            len(sqlb.OpValues),
		"JoinTypeValues":      len(sqlb.JoinTypeValues),
		"SortDirectionValues": len(sqlb.SortDirectionValues),
		"SlotValues":          len(sqlb.SlotValues),
	} {
		if n == 0 {
			t.Errorf("%s is empty; a canonical iteration order that lost its entries is worse than none", name)
		}
	}
}
