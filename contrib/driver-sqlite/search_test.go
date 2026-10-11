package driversqlite_test

import (
	"context"
	"fmt"
	"testing"

	"github.com/ubgo/sqlb/bind"
	driversqlite "github.com/ubgo/sqlb/contrib/driver-sqlite"
	"github.com/ubgo/sqlb/dialect/sqlite"
	"github.com/ubgo/sqlb/exec"
	"github.com/ubgo/sqlb/expr"
	"github.com/ubgo/sqlb/stmt"
)

// TestSearchPredicates_MatchWhatWasTyped is the reason these predicates exist,
// and it is only visible against real rows.
//
// A search box takes text, not a pattern. Passing that text to LIKE makes the
// % and _ in it mean "anything", so searching for "50%" also returns "50" and
// "500". Nothing fails; the answer is simply wrong, and the query looks
// correct in a log. The table below contains exactly the rows that separate
// the two behaviours.
func TestSearchPredicates_MatchWhatWasTyped(t *testing.T) {
	ctx := context.Background()
	db, err := driversqlite.Memory()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()

	if _, err := db.ExecContext(ctx, `CREATE TABLE items (name TEXT NOT NULL)`); err != nil {
		t.Fatal(err)
	}
	for _, n := range []string{"50% off", "50 off", "500 off", "a_b", "axb", `back\slash`} {
		if _, err := db.ExecContext(ctx, `INSERT INTO items VALUES (?)`, n); err != nil {
			t.Fatal(err)
		}
	}

	items := expr.Table{Name: "items", Alias: "i"}
	name := expr.Col[string](items, "name")

	find := func(t *testing.T, p expr.Predicate) []string {
		t.Helper()
		var got string
		q, err := stmt.NewSelect(sqlite.Dialect{}).
			Columns(bind.Bind(name, &got)).
			From(items).
			Where(p).
			Order(expr.OrderBy(name, expr.SortAsc)).
			Build()
		if err != nil {
			t.Fatal(err)
		}
		var out []string
		if err := exec.Each(ctx, db, q, func() error {
			out = append(out, got)
			return nil
		}); err != nil {
			t.Fatal(err)
		}
		return out
	}

	t.Run("the wrong way", func(t *testing.T) {
		// What a caller writes when Like is the only tool: wrap the typed text
		// in wildcards and hope.
		got := find(t, expr.Like(name, "%"+"50%"+"%"))
		want := "[50 off 50% off 500 off]"
		if fmt.Sprint(got) != want {
			t.Fatalf("got %v, want %s", got, want)
		}
		// Three rows for a search that should find one.
	})

	t.Run("Contains", func(t *testing.T) {
		if got := find(t, expr.Contains(name, "50%")); fmt.Sprint(got) != "[50% off]" {
			t.Fatalf("got %v, want only the literal match", got)
		}
	})

	t.Run("underscore is literal too", func(t *testing.T) {
		if got := find(t, expr.Contains(name, "a_b")); fmt.Sprint(got) != "[a_b]" {
			t.Fatalf("got %v; the underscore matched any character", got)
		}
	})

	t.Run("backslash is literal", func(t *testing.T) {
		if got := find(t, expr.Contains(name, `back\slash`)); fmt.Sprint(got) != `[back\slash]` {
			t.Fatalf("got %v", got)
		}
	})

	t.Run("StartsWith and EndsWith", func(t *testing.T) {
		if got := find(t, expr.StartsWith(name, "50%")); fmt.Sprint(got) != "[50% off]" {
			t.Fatalf("StartsWith got %v", got)
		}
		if got := find(t, expr.EndsWith(name, "off")); len(got) != 3 {
			t.Fatalf("EndsWith got %v, want the three rows ending in off", got)
		}
	})

	t.Run("NotContains", func(t *testing.T) {
		got := find(t, expr.NotContains(name, "50%"))
		if fmt.Sprint(got) == "[50% off]" || len(got) != 5 {
			t.Fatalf("got %v, want every row but the literal match", got)
		}
	})
}
