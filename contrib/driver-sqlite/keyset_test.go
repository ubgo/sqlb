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

// TestKeysetPagination executes the row-constructor comparison that ent issue
// 215 has been open about since 2019.
//
// Rendering it is not the claim worth making. A row constructor is standard
// SQL, but "standard" is where universality claims go wrong, so this walks a
// real table page by page and asserts every row is seen exactly once with no
// gap and no repeat. That is the property keyset pagination exists for and the
// one an OFFSET does not have.
func TestKeysetPagination(t *testing.T) {
	ctx := context.Background()
	db, err := driversqlite.Memory()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()

	if _, err := db.ExecContext(ctx,
		`CREATE TABLE users (id TEXT NOT NULL, first_name TEXT NOT NULL)`); err != nil {
		t.Fatal(err)
	}
	// Deliberately duplicated names, which is the case a single-column cursor
	// gets wrong and a composite one does not.
	for i, row := range [][2]string{
		{"u1", "ada"}, {"u2", "ada"}, {"u3", "bob"},
		{"u4", "bob"}, {"u5", "bob"}, {"u6", "cyd"},
	} {
		if _, err := db.ExecContext(ctx,
			`INSERT INTO users VALUES (?, ?)`, row[0], row[1]); err != nil {
			t.Fatalf("row %d: %v", i, err)
		}
	}

	users := expr.Table{Name: "users", Alias: "u"}
	id := expr.Col[string](users, "id")
	name := expr.Col[string](users, "first_name")

	const pageSize = 2
	var seen []string
	cursorName, cursorID := "", ""

	for page := 0; page < 10; page++ {
		sel := stmt.NewSelect(sqlite.Dialect{}).
			From(users).
			Order(expr.OrderBy(name, expr.SortAsc), expr.OrderBy(id, expr.SortAsc)).
			Limit(pageSize)

		if page > 0 {
			sel = sel.Where(expr.RowGreater(
				expr.Row(name, id),
				expr.Row(expr.Param(cursorName), expr.Param(cursorID)),
			))
		}

		var gotID, gotName string
		q, err := sel.Columns(bind.Bind(id, &gotID), bind.Bind(name, &gotName)).Build()
		if err != nil {
			t.Fatal(err)
		}

		n := 0
		err = exec.Each(ctx, db, q, func() error {
			seen = append(seen, gotName+"/"+gotID)
			cursorName, cursorID = gotName, gotID
			n++
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
		if n < pageSize {
			break
		}
	}

	want := "[ada/u1 ada/u2 bob/u3 bob/u4 bob/u5 cyd/u6]"
	if got := fmt.Sprint(seen); got != want {
		t.Fatalf("paged through\n got %s\nwant %s", got, want)
	}
}
