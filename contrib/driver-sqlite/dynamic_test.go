package driversqlite_test

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"

	driversqlite "github.com/ubgo/sqlb/contrib/driver-sqlite"
	"github.com/ubgo/sqlb/dialect/sqlite"
	"github.com/ubgo/sqlb/expr"
	"github.com/ubgo/sqlb/scan"
	"github.com/ubgo/sqlb/stmt"
)

// TestDynamic_ColumnsChosenAtRuntime is the case the feature exists for.
//
// The column list arrives with the request, so no set of variables declared in
// advance can match it. The query was already buildable; this is the reading
// side that was missing.
func TestDynamic_ColumnsChosenAtRuntime(t *testing.T) {
	ctx := context.Background()
	db, err := driversqlite.Memory()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()

	for _, q := range []string{
		`CREATE TABLE users (id TEXT NOT NULL, email TEXT NOT NULL, age INTEGER, note TEXT)`,
		`INSERT INTO users VALUES ('u1','ada@x.com',36,NULL), ('u2','bob@x.com',41,'hi')`,
	} {
		if _, err := db.ExecContext(ctx, q); err != nil {
			t.Fatal(err)
		}
	}
	users := expr.Table{Name: "users", Alias: "u"}

	// Whatever the user ticked. Only known now.
	for _, picked := range [][]string{
		{"email", "age"},
		{"id", "email", "age", "note"},
		{"id"},
	} {
		t.Run(strings.Join(picked, "+"), func(t *testing.T) {
			sel := stmt.NewSelect(sqlite.Dialect{}).From(users).
				Order(expr.OrderBy(expr.Col[string](users, "id"), expr.SortAsc))
			for _, c := range picked {
				sel = sel.Exprs(expr.Col[any](users, c))
			}

			q, err := sel.Build()
			if err != nil {
				t.Fatal(err)
			}
			rows, err := scan.Maps(ctx, db, q)
			if err != nil {
				t.Fatal(err)
			}
			if len(rows) != 2 {
				t.Fatalf("got %d rows, want 2", len(rows))
			}
			if len(rows[0]) != len(picked) {
				t.Fatalf("row has %d keys, want %d: %v", len(rows[0]), len(picked), rows[0])
			}
			for _, c := range picked {
				if _, ok := rows[0][c]; !ok {
					t.Errorf("column %q missing from %v", c, rows[0])
				}
			}
		})
	}
}

// TestDynamic_DriverTypesAndNull pins what actually lands in the map.
//
// The values are not converted, which is deliberate: normalising them would
// have to guess, and a guess here is invisible. A caller needs to know that an
// integer column is int64 and a NULL is nil, so it is asserted rather than
// described.
func TestDynamic_DriverTypesAndNull(t *testing.T) {
	ctx := context.Background()
	db, err := driversqlite.Memory()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()

	if _, err := db.ExecContext(ctx,
		`CREATE TABLE t (i INTEGER, s TEXT, f REAL, b BLOB, n TEXT)`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO t VALUES (1,'ada',1.5,x'DEAD',NULL)`); err != nil {
		t.Fatal(err)
	}

	tbl := expr.Table{Name: "t", Alias: "t"}
	sel := stmt.NewSelect(sqlite.Dialect{}).From(tbl)
	for _, c := range []string{"i", "s", "f", "b", "n"} {
		sel = sel.Exprs(expr.Col[any](tbl, c))
	}
	q, err := sel.Build()
	if err != nil {
		t.Fatal(err)
	}

	rows, err := scan.Maps(ctx, db, q)
	if err != nil {
		t.Fatal(err)
	}
	r := rows[0]

	for _, c := range []struct {
		col  string
		want string
	}{
		{"i", "int64"}, {"s", "string"}, {"f", "float64"}, {"b", "[]uint8"}, {"n", "<nil>"},
	} {
		if got := fmt.Sprintf("%T", r[c.col]); got != c.want {
			t.Errorf("%s is %s, want %s", c.col, got, c.want)
		}
	}
}

// TestDynamic_DuplicateColumnIsAnError pins the failure a map cannot avoid.
//
// "SELECT a.id, b.id" is an ordinary query and a map can hold one of the two.
// Keeping one and dropping the other silently is the kind of wrong answer this
// package refuses, so Maps fails and names the column, and Slices is the form
// that loses nothing.
func TestDynamic_DuplicateColumnIsAnError(t *testing.T) {
	ctx := context.Background()
	db, err := driversqlite.Memory()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()

	for _, q := range []string{
		`CREATE TABLE a (id INTEGER)`, `CREATE TABLE b (id INTEGER)`,
		`INSERT INTO a VALUES (1)`, `INSERT INTO b VALUES (99)`,
	} {
		if _, err := db.ExecContext(ctx, q); err != nil {
			t.Fatal(err)
		}
	}
	ta, tb := expr.Table{Name: "a", Alias: "a"}, expr.Table{Name: "b", Alias: "b"}
	q, err := stmt.NewSelect(sqlite.Dialect{}).
		Exprs(expr.Col[any](ta, "id"), expr.Col[any](tb, "id")).
		From(ta).
		Join(stmt.Join{Type: stmt.JoinCross, Table: tb, Source: tb}).
		Build()
	if err != nil {
		t.Fatal(err)
	}

	if _, err := scan.Maps(ctx, db, q); !errors.Is(err, scan.ErrDuplicateColumn) {
		t.Fatalf("want ErrDuplicateColumn, got %v", err)
	}

	// Slices keeps both, positionally.
	names, vals, err := scan.Slices(ctx, db, q)
	if err != nil {
		t.Fatal(err)
	}
	if len(names) != 2 || len(vals) != 1 || len(vals[0]) != 2 {
		t.Fatalf("names=%v vals=%v", names, vals)
	}
	if vals[0][0] == vals[0][1] {
		t.Fatalf("both values are %v; one row lost the other column", vals[0][0])
	}
}

// TestDynamic_BlobsAreCopied pins the driver-buffer hazard.
//
// A driver may hand back a slice into its own read buffer and overwrite it on
// the next row, so a reader that kept the slice would corrupt every row but
// the last, and only for blob columns. The copy is what makes multi-row blob
// results correct.
func TestDynamic_BlobsAreCopied(t *testing.T) {
	ctx := context.Background()
	db, err := driversqlite.Memory()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()

	if _, err := db.ExecContext(ctx, `CREATE TABLE t (i INTEGER, b BLOB)`); err != nil {
		t.Fatal(err)
	}
	for i, hex := range []string{"x'AAAA'", "x'BBBB'", "x'CCCC'"} {
		if _, err := db.ExecContext(ctx,
			fmt.Sprintf(`INSERT INTO t VALUES (%d, %s)`, i, hex)); err != nil {
			t.Fatal(err)
		}
	}

	tbl := expr.Table{Name: "t", Alias: "t"}
	q, err := stmt.NewSelect(sqlite.Dialect{}).
		Exprs(expr.Col[any](tbl, "b")).From(tbl).
		Order(expr.OrderBy(expr.Col[int](tbl, "i"), expr.SortAsc)).
		Build()
	if err != nil {
		t.Fatal(err)
	}

	rows, err := scan.Maps(ctx, db, q)
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, r := range rows {
		got = append(got, fmt.Sprintf("%X", r["b"]))
	}
	if want := "[AAAA BBBB CCCC]"; fmt.Sprint(got) != want {
		t.Fatalf("got %v, want %s; the rows share a buffer", got, want)
	}
}

// TestDynamic_EachMapGivesAFreshRow pins that a callback may keep the map.
//
// The binding API's Each reuses its destinations and documents that a callback
// must copy. This does not, and the difference should be asserted rather than
// assumed, because a caller who has met the other rule will expect this one to
// behave the same way.
func TestDynamic_EachMapGivesAFreshRow(t *testing.T) {
	ctx := context.Background()
	db, err := driversqlite.Memory()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()

	if _, err := db.ExecContext(ctx, `CREATE TABLE t (i INTEGER)`); err != nil {
		t.Fatal(err)
	}
	for i := 1; i <= 3; i++ {
		if _, err := db.ExecContext(ctx, `INSERT INTO t VALUES (?)`, i); err != nil {
			t.Fatal(err)
		}
	}
	tbl := expr.Table{Name: "t", Alias: "t"}
	q, err := stmt.NewSelect(sqlite.Dialect{}).
		Exprs(expr.Col[any](tbl, "i")).From(tbl).
		Order(expr.OrderBy(expr.Col[int](tbl, "i"), expr.SortAsc)).Build()
	if err != nil {
		t.Fatal(err)
	}

	var kept []scan.Row
	if err := scan.EachMap(ctx, db, q, func(r scan.Row) error {
		kept = append(kept, r) // keeping it without copying must be safe
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if fmt.Sprint(kept) != "[map[i:1] map[i:2] map[i:3]]" {
		t.Fatalf("got %v; the rows share one map", kept)
	}
}

// TestDynamic_CallbackErrorStopsIteration pins that fn can halt the scan, and
// that its error reaches the caller unwrapped.
func TestDynamic_CallbackErrorStopsIteration(t *testing.T) {
	ctx := context.Background()
	db, err := driversqlite.Memory()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()

	if _, err := db.ExecContext(ctx, `CREATE TABLE t (i INTEGER)`); err != nil {
		t.Fatal(err)
	}
	for i := 1; i <= 5; i++ {
		if _, err := db.ExecContext(ctx, `INSERT INTO t VALUES (?)`, i); err != nil {
			t.Fatal(err)
		}
	}
	tbl := expr.Table{Name: "t", Alias: "t"}
	q, err := stmt.NewSelect(sqlite.Dialect{}).Exprs(expr.Col[any](tbl, "i")).From(tbl).Build()
	if err != nil {
		t.Fatal(err)
	}

	stop := errors.New("enough")
	seen := 0
	err = scan.EachMap(ctx, db, q, func(scan.Row) error {
		seen++
		if seen == 2 {
			return stop
		}
		return nil
	})
	if !errors.Is(err, stop) {
		t.Fatalf("got %v, want the callback's own error", err)
	}
	if seen != 2 {
		t.Fatalf("read %d rows after the callback stopped", seen)
	}
}

// TestDynamic_NoRows pins the empty case, which a caller hits constantly and
// which must not be a special path with its own bugs.
func TestDynamic_NoRows(t *testing.T) {
	ctx := context.Background()
	db, err := driversqlite.Memory()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()

	if _, err := db.ExecContext(ctx, `CREATE TABLE t (i INTEGER)`); err != nil {
		t.Fatal(err)
	}
	tbl := expr.Table{Name: "t", Alias: "t"}
	q, err := stmt.NewSelect(sqlite.Dialect{}).Exprs(expr.Col[any](tbl, "i")).From(tbl).Build()
	if err != nil {
		t.Fatal(err)
	}

	rows, err := scan.Maps(ctx, db, q)
	if err != nil || rows != nil {
		t.Fatalf("got %v, %v; want no rows and no error", rows, err)
	}
	names, vals, err := scan.Slices(ctx, db, q)
	if err != nil {
		t.Fatal(err)
	}
	if vals != nil {
		t.Fatalf("got %v values, want none", vals)
	}
	// Columns are only learned from a row here, which is worth knowing.
	_ = names
}

// TestDynamic_SafeAcrossGoroutines proves the claim the code makes about
// itself, rather than leaving it in a comment.
//
// exec takes the query guard because a Binding holds a pointer into a caller's
// variable. Nothing in the dynamic path does, so one query may be executed
// concurrently, and this asserts that under -race with every goroutine sharing
// a single Query value. If a future change reintroduced shared state, this is
// what would catch it.
func TestDynamic_SafeAcrossGoroutines(t *testing.T) {
	ctx := context.Background()
	db, err := driversqlite.Memory()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()

	if _, err := db.ExecContext(ctx, `CREATE TABLE t (i INTEGER, s TEXT)`); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 50; i++ {
		if _, err := db.ExecContext(ctx, `INSERT INTO t VALUES (?, ?)`, i, fmt.Sprint("n", i)); err != nil {
			t.Fatal(err)
		}
	}
	tbl := expr.Table{Name: "t", Alias: "t"}
	sel := stmt.NewSelect(sqlite.Dialect{}).From(tbl).
		Order(expr.OrderBy(expr.Col[int](tbl, "i"), expr.SortAsc))
	for _, c := range []string{"i", "s"} {
		sel = sel.Exprs(expr.Col[any](tbl, c))
	}
	q, err := sel.Build() // one Query, shared by every goroutine
	if err != nil {
		t.Fatal(err)
	}

	const workers = 64
	errs := make(chan error, workers)
	var wg sync.WaitGroup
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			rows, err := scan.Maps(ctx, db, q)
			if err != nil {
				errs <- err
				return
			}
			if len(rows) != 50 {
				errs <- fmt.Errorf("got %d rows, want 50", len(rows))
				return
			}
			// Every goroutine must see its own correct values, not another's.
			for i, r := range rows {
				if r["i"] != int64(i) || r["s"] != fmt.Sprint("n", i) {
					errs <- fmt.Errorf("row %d is %v", i, r)
					return
				}
			}
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Error(err)
	}
}
