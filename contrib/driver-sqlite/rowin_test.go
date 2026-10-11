package driversqlite_test

import (
	"context"
	"testing"

	"github.com/ubgo/sqlb/bind"
	driversqlite "github.com/ubgo/sqlb/contrib/driver-sqlite"
	"github.com/ubgo/sqlb/dialect/sqlite"
	"github.com/ubgo/sqlb/exec"
	"github.com/ubgo/sqlb/expr"
	"github.com/ubgo/sqlb/render"
	"github.com/ubgo/sqlb/stmt"
)

// TestRowIn_ExecutesAgainstARealDatabase is the half of the claim that reading
// the SQL cannot make.
//
// Row values are standard and were still a real portability question: SQLite
// only grew them in 3.15, and "the standard says so" is exactly where a
// universality claim goes wrong here. So this runs the rendered predicate,
// asserts the matching pairs come back and the near misses do not, and runs
// the same question through the subquery form, which needs nothing new because
// SubqueryCompare already takes a SQLer.
//
// The near misses are the point of the fixture: (1,'y') and (2,'x') both share
// an element with a listed pair, so a predicate that compared the elements
// independently rather than as a tuple would return them.
func TestRowIn_ExecutesAgainstARealDatabase(t *testing.T) {
	ctx := context.Background()
	db, err := driversqlite.Memory()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()

	if _, err := db.ExecContext(ctx,
		`CREATE TABLE members (org_id INTEGER NOT NULL, user_id TEXT NOT NULL)`); err != nil {
		t.Fatal(err)
	}
	for _, row := range []struct {
		org  int64
		user string
	}{
		{1, "x"}, {1, "y"}, {2, "x"}, {2, "y"}, {3, "z"},
	} {
		if _, err := db.ExecContext(ctx,
			`INSERT INTO members VALUES (?, ?)`, row.org, row.user); err != nil {
			t.Fatal(err)
		}
	}

	members := expr.Table{Name: "members", Alias: "m"}
	orgID := expr.Col[int64](members, "org_id")
	userID := expr.Col[string](members, "user_id")

	count := func(t *testing.T, p expr.Predicate) []string {
		t.Helper()
		var org int64
		var user string
		q, err := stmt.NewSelect(sqlite.Dialect{}).
			From(members).
			Columns(bind.Bind(orgID, &org), bind.Bind(userID, &user)).
			Where(p).
			Order(expr.OrderBy(orgID, expr.SortAsc), expr.OrderBy(userID, expr.SortAsc)).
			Build()
		if err != nil {
			t.Fatal(err)
		}
		var got []string
		if err := exec.Each(ctx, db, q, func() error {
			got = append(got, string(rune('0'+org))+user)
			return nil
		}); err != nil {
			t.Fatal(err)
		}
		return got
	}

	pair := func(org int64, user string) render.SQLer {
		return expr.Row(expr.Param(org), expr.Param(user))
	}

	t.Run("in a list of pairs", func(t *testing.T) {
		got := count(t, expr.RowIn(expr.Row(orgID, userID), pair(1, "x"), pair(2, "y")))
		want := []string{"1x", "2y"}
		assertRows(t, got, want)
	})

	t.Run("not in a list of pairs", func(t *testing.T) {
		got := count(t, expr.RowNotIn(expr.Row(orgID, userID), pair(1, "x"), pair(2, "y")))
		want := []string{"1y", "2x", "3z"}
		assertRows(t, got, want)
	})

	t.Run("empty list matches nothing", func(t *testing.T) {
		assertRows(t, count(t, expr.RowIn(expr.Row(orgID, userID))), nil)
	})

	t.Run("in a subquery needs nothing new", func(t *testing.T) {
		inner := stmt.NewSelect(sqlite.Dialect{}).
			From(members).
			Exprs(orgID, userID).
			Where(expr.Eq(userID, "z"))
		got := count(t, stmt.SubqueryCompare(
			expr.Row(orgID, userID), expr.OpIn, "", inner))
		assertRows(t, got, []string{"3z"})
	})
}

func assertRows(t *testing.T, got, want []string) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	for i := range got {
		if got[i] != want[i] {
			t.Fatalf("got %v, want %v", got, want)
		}
	}
}
