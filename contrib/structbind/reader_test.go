package structbind_test

import (
	"context"
	"database/sql"
	"strings"
	"sync"
	"testing"

	"github.com/ubgo/sqlb/dialect/sqlite"
	"github.com/ubgo/sqlb/expr"
	"github.com/ubgo/sqlb/render"
	"github.com/ubgo/sqlb/scan"
	"github.com/ubgo/sqlb/stmt"
	_ "modernc.org/sqlite"
)

// readerDB builds the fixture both Reader tests read from.
func readerDB(t *testing.T) *sql.DB {
	t.Helper()
	ctx := context.Background()
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	// An in-memory SQLite database belongs to the connection that created it,
	// so a pool of two serves the next query from a second, empty database and
	// the schema appears to vanish. That is a property of the fixture, not of
	// anything under test, and it is what contrib/driver-sqlite's Memory pins
	// for the same reason. The concurrency test below still exercises one
	// shared Reader from many goroutines; only the connection is serialised.
	db.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = db.Close() })
	if _, err := db.ExecContext(ctx,
		`CREATE TABLE accounts (id TEXT NOT NULL, email TEXT NOT NULL, age INTEGER NOT NULL)`); err != nil {
		t.Fatal(err)
	}
	for _, row := range []account{
		{ID: "a1", Email: "ada@x.com", Age: 36},
		{ID: "a2", Email: "bob@x.com", Age: 41},
		{ID: "a3", Email: "cyd@x.com", Age: 29},
	} {
		if _, err := db.ExecContext(ctx,
			`INSERT INTO accounts VALUES (?, ?, ?)`, row.ID, row.Email, row.Age); err != nil {
			t.Fatal(err)
		}
	}
	return db
}

// TestReader_ReadsRowsAsValues is the shape a struct mapper's Select has and
// this package
// did not: a query into a slice of structs, in one call.
//
// Bindings could not do it. They point into a struct the caller owns, so
// reading three rows meant a loop over exec.Each that copied the struct each
// time, and the query was unsafe to share for exactly as long as it held those
// pointers. The scan layer had the right shape already and no door: Reader's
// fields are unexported and its constructors were all in-package, so a
// reflective mapping in another module could produce bindings and could not
// produce a Reader. scan.New is that door.
func TestReader_ReadsRowsAsValues(t *testing.T) {
	ctx := context.Background()
	db := readerDB(t)
	id := expr.Col[string](accounts, "id")

	got, err := scan.All(ctx, db,
		stmt.NewSelect(sqlite.Dialect{}).
			From(accounts).
			Order(expr.OrderBy(id, expr.SortAsc)),
		accountMap.Reader())
	if err != nil {
		t.Fatal(err)
	}

	want := []account{
		{ID: "a1", Email: "ada@x.com", Age: 36},
		{ID: "a2", Email: "bob@x.com", Age: 41},
		{ID: "a3", Email: "cyd@x.com", Age: 29},
	}
	if len(got) != len(want) {
		t.Fatalf("got %d rows, want %d: %v", len(got), len(want), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("row %d: got %+v, want %+v", i, got[i], want[i])
		}
	}
}

// TestReader_SafeAcrossGoroutines is the property that distinguishes a Reader
// from a Binding, proven rather than claimed.
//
// One Mapping, one Select and one Reader are shared by sixty-four goroutines.
// If the per-row allocator ever handed back a pointer into memory outside the
// row — the one rule scan.New states — this is where it would show, as rows
// interleaving into the same struct under -race. The same run with Bindings
// would be a genuine race, which is why the binding API documents that a query
// holding them is not safe to share.
func TestReader_SafeAcrossGoroutines(t *testing.T) {
	ctx := context.Background()
	db := readerDB(t)
	id := expr.Col[string](accounts, "id")

	sel := stmt.NewSelect(sqlite.Dialect{}).
		From(accounts).
		Order(expr.OrderBy(id, expr.SortAsc))
	reader := accountMap.Reader()

	const workers = 64
	var wg sync.WaitGroup
	bad := make(chan string, workers)
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			rows, err := scan.All(ctx, db, sel, reader)
			if err != nil {
				bad <- err.Error()
				return
			}
			if len(rows) != 3 || rows[0].ID != "a1" || rows[2].Email != "cyd@x.com" {
				bad <- "rows interleaved"
			}
		}()
	}
	wg.Wait()
	close(bad)
	for msg := range bad {
		t.Fatal(msg)
	}
}

// TestReader_SelectsTheSameColumnsAsBindings pins the two halves against each
// other, because they are derived from one field list and a divergence would
// mean one of them silently reads a different query than the other.
func TestReader_SelectsTheSameColumnsAsBindings(t *testing.T) {
	var dst account
	bindings := accountMap.Bindings(&dst)
	cols := accountMap.Reader().Columns()
	if len(bindings) != len(cols) {
		t.Fatalf("%d bindings, %d reader columns", len(bindings), len(cols))
	}

	a1 := renderAll(t, toSQLers(bindings))
	a2 := renderAll(t, cols)
	if a1 != a2 {
		t.Errorf("bindings select %s, reader selects %s", a1, a2)
	}
}

func toSQLers(bs []stmt.Binding) []render.SQLer {
	out := make([]render.SQLer, len(bs))
	for i, b := range bs {
		out[i] = b
	}
	return out
}

func renderAll(t *testing.T, items []render.SQLer) string {
	t.Helper()
	a := render.NewArgs(sqlite.Dialect{})
	var out []string
	for _, it := range items {
		out = append(out, it.SQL(a))
	}
	return strings.Join(out, ", ")
}
