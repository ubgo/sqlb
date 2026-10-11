package driversqlite_test

import (
	"context"
	"database/sql"
	"sync"
	"testing"

	"github.com/ubgo/sqlb/bind"
	driversqlite "github.com/ubgo/sqlb/contrib/driver-sqlite"
	"github.com/ubgo/sqlb/dialect/sqlite"
	"github.com/ubgo/sqlb/exec"
	"github.com/ubgo/sqlb/expr"
	"github.com/ubgo/sqlb/scan"
	"github.com/ubgo/sqlb/stmt"
)

// This file is the class test for one bug, and it exists because the bug was
// found by accident.
//
// scan.All finished the statement it was given by calling Exprs on it. Every
// builder method mutates its receiver, so the Reader's columns stayed there: a
// second call selected every column twice, which is legal SQL and therefore
// failed somewhere else entirely, and two goroutines calling All on one
// statement appended to the same slice, which is a genuine race. build now
// clones.
//
// The rule that generalises it: a builder METHOD may accumulate, because that
// is what Where and Join are for and calling Where twice meaning two
// predicates is the whole design. A function that merely TAKES a statement may
// not write to it, because a caller holds both and cannot be asked to remember
// which half is the mutable one. Every entry point below takes a statement, so
// every one of them is checked, and scripts/check-reuse-coverage.py fails if a
// new one is added without appearing here.
//
// ENTRY POINTS: scan.All scan.One scan.Maps scan.EachMap scan.Slices exec.One exec.Each

const reuseWorkers = 32

func reuseFixture(t *testing.T) (*sql.DB, expr.Table, expr.Expr[string]) {
	t.Helper()
	ctx := context.Background()
	db, err := driversqlite.Memory()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if _, err := db.ExecContext(ctx, `CREATE TABLE crew (name TEXT NOT NULL)`); err != nil {
		t.Fatal(err)
	}
	for _, n := range []string{"ada", "bob", "cyd"} {
		if _, err := db.ExecContext(ctx, `INSERT INTO crew VALUES (?)`, n); err != nil {
			t.Fatal(err)
		}
	}
	tbl := expr.Table{Name: "crew", Alias: "c"}
	return db, tbl, expr.Col[string](tbl, "name")
}

// TestReuse_EveryEntryPointLeavesItsArgumentAlone calls each entry point twice
// on one value and requires the second call to behave exactly like the first.
//
// The ones taking a built Query cannot fail this today, and they are here
// anyway: the point of a class test is that it covers the shape rather than
// the instance, so an entry point that later starts finishing a statement is
// caught by a test that already exists.
func TestReuse_EveryEntryPointLeavesItsArgumentAlone(t *testing.T) {
	ctx := context.Background()
	db, tbl, name := reuseFixture(t)

	sel := func() *stmt.Select[sqlite.Dialect] {
		return stmt.NewSelect(sqlite.Dialect{}).
			From(tbl).
			Order(expr.OrderBy(name, expr.SortAsc))
	}
	built := func(t *testing.T) stmt.Query {
		t.Helper()
		q, err := sel().Exprs(name).Build()
		if err != nil {
			t.Fatal(err)
		}
		return q
	}

	t.Run("scan.All", func(t *testing.T) {
		s := sel()
		r := scan.Field(name)
		first, err := scan.All(ctx, db, s, r)
		if err != nil {
			t.Fatal(err)
		}
		second, err := scan.All(ctx, db, s, r)
		if err != nil {
			t.Fatalf("second call: %v", err)
		}
		sameStrings(t, first, second)
	})

	t.Run("scan.One", func(t *testing.T) {
		s := sel().Limit(1)
		r := scan.Field(name)
		first, err := scan.One(ctx, db, s, r)
		if err != nil {
			t.Fatal(err)
		}
		second, err := scan.One(ctx, db, s, r)
		if err != nil {
			t.Fatalf("second call: %v", err)
		}
		if first != second {
			t.Errorf("got %q then %q", first, second)
		}
	})

	t.Run("scan.Maps", func(t *testing.T) {
		q := built(t)
		first, err := scan.Maps(ctx, db, q)
		if err != nil {
			t.Fatal(err)
		}
		second, err := scan.Maps(ctx, db, q)
		if err != nil {
			t.Fatalf("second call: %v", err)
		}
		if len(first) != len(second) {
			t.Errorf("%d rows then %d", len(first), len(second))
		}
	})

	t.Run("scan.EachMap", func(t *testing.T) {
		q := built(t)
		count := func() int {
			n := 0
			if err := scan.EachMap(ctx, db, q, func(scan.Row) error { n++; return nil }); err != nil {
				t.Fatal(err)
			}
			return n
		}
		if a, b := count(), count(); a != b {
			t.Errorf("%d rows then %d", a, b)
		}
	})

	t.Run("scan.Slices", func(t *testing.T) {
		q := built(t)
		cols1, rows1, err := scan.Slices(ctx, db, q)
		if err != nil {
			t.Fatal(err)
		}
		cols2, rows2, err := scan.Slices(ctx, db, q)
		if err != nil {
			t.Fatalf("second call: %v", err)
		}
		if len(cols1) != len(cols2) || len(rows1) != len(rows2) {
			t.Errorf("%d cols/%d rows then %d cols/%d rows", len(cols1), len(rows1), len(cols2), len(rows2))
		}
	})

	t.Run("exec.One", func(t *testing.T) {
		var got string
		q, err := sel().Limit(1).Columns(bind.Bind(name, &got)).Build()
		if err != nil {
			t.Fatal(err)
		}
		if err := exec.One(ctx, db, q); err != nil {
			t.Fatal(err)
		}
		first := got
		got = ""
		if err := exec.One(ctx, db, q); err != nil {
			t.Fatalf("second call: %v", err)
		}
		if first != got {
			t.Errorf("got %q then %q", first, got)
		}
	})

	t.Run("exec.Each", func(t *testing.T) {
		var row string
		q, err := sel().Columns(bind.Bind(name, &row)).Build()
		if err != nil {
			t.Fatal(err)
		}
		collect := func() []string {
			var out []string
			if err := exec.Each(ctx, db, q, func() error { out = append(out, row); return nil }); err != nil {
				t.Fatal(err)
			}
			return out
		}
		sameStrings(t, collect(), collect())
	})
}

// TestReuse_SharedStatementAcrossGoroutines is the same property under -race,
// which is the only thing that was ever going to catch the append.
//
// Nothing in scan takes the query guard, deliberately: a Reader holds no
// caller memory, so refusing a concurrent execution would deny something that
// is safe. That is only true while nothing writes to the statement during a
// read. exec.One and exec.Each are absent here on purpose — they hold caller
// pointers, so a shared execution is genuinely unsafe and is refused with
// ErrQueryInUse, which TestSharedQuery_IsRefusedNotRaced covers.
func TestReuse_SharedStatementAcrossGoroutines(t *testing.T) {
	ctx := context.Background()
	db, tbl, name := reuseFixture(t)

	sel := stmt.NewSelect(sqlite.Dialect{}).
		From(tbl).
		Order(expr.OrderBy(name, expr.SortAsc))
	reader := scan.Field(name)

	var wg sync.WaitGroup
	bad := make(chan string, reuseWorkers)
	for i := 0; i < reuseWorkers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			got, err := scan.All(ctx, db, sel, reader)
			if err != nil {
				bad <- err.Error()
				return
			}
			if len(got) != 3 || got[0] != "ada" || got[2] != "cyd" {
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

func sameStrings(t *testing.T, a, b []string) {
	t.Helper()
	if len(a) != len(b) {
		t.Fatalf("got %v then %v", a, b)
	}
	for i := range a {
		if a[i] != b[i] {
			t.Fatalf("got %v then %v", a, b)
		}
	}
}
