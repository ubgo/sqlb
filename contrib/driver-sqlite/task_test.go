package driversqlite_test

import (
	"context"
	"database/sql"
	"testing"

	"github.com/ubgo/sqlb/bind"
	driversqlite "github.com/ubgo/sqlb/contrib/driver-sqlite"
	"github.com/ubgo/sqlb/dialect/sqlite"
	"github.com/ubgo/sqlb/exec"
	"github.com/ubgo/sqlb/expr"
	"github.com/ubgo/sqlb/scan"
	"github.com/ubgo/sqlb/stmt"
)

// Tests in this file answer a different question than the rest of the suite.
//
// Everywhere else the question is "does this construct render, and does it
// execute". That question cannot see a missing workflow: every gap the library
// comparisons found — folding a join into a tree, reading into a slice of
// structs, a select list chosen at runtime — was buildable the whole time, and
// invisible because nobody had written the paragraph that needed it.
//
// So each test here is one task a person actually has, done end to end against
// a real database, using only what a consumer would import. A task that cannot
// be written without a hand-rolled loop or a raw string is the finding.

var (
	tasksTbl    = expr.Table{Name: "orders", Alias: "o"}
	taskID      = expr.Col[int64](tasksTbl, "id")
	taskCustom  = expr.Col[string](tasksTbl, "customer")
	taskRegion  = expr.Col[string](tasksTbl, "region")
	taskTotal   = expr.Col[int64](tasksTbl, "total")
	taskShipped = expr.Col[bool](tasksTbl, "shipped")
)

func taskDB(t *testing.T) *sql.DB {
	t.Helper()
	ctx := context.Background()
	db, err := driversqlite.Memory()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if _, err := db.ExecContext(ctx, `CREATE TABLE orders (
		id INTEGER PRIMARY KEY, customer TEXT NOT NULL, region TEXT NOT NULL,
		total INTEGER NOT NULL, shipped INTEGER NOT NULL)`); err != nil {
		t.Fatal(err)
	}
	for _, row := range []struct {
		id      int64
		cust    string
		region  string
		total   int64
		shipped bool
	}{
		{1, "ada", "eu", 120, true},
		{2, "bob", "eu", 80, false},
		{3, "cyd", "us", 300, true},
		{4, "dee", "us", 50, false},
		{5, "eve", "us", 220, true},
	} {
		if _, err := db.ExecContext(ctx,
			`INSERT INTO orders VALUES (?, ?, ?, ?, ?)`,
			row.id, row.cust, row.region, row.total, row.shipped); err != nil {
			t.Fatal(err)
		}
	}
	return db
}

// TestTask_PageAListAndCountTheTotal is the most common screen there is: a
// filtered, sorted page of rows plus the number of rows the filter matched.
//
// The naive version runs two statements and they can disagree, because a write
// between them changes the count under the page. count(*) OVER () answers both
// in one pass, and this asserts the page and the total come back together and
// agree with the filter.
func TestTask_PageAListAndCountTheTotal(t *testing.T) {
	ctx := context.Background()
	db := taskDB(t)

	total := expr.Over(expr.Count(), expr.Window{})
	rows, err := scan.All(ctx, db,
		stmt.NewSelect(sqlite.Dialect{}).
			From(tasksTbl).
			Where(expr.Eq(taskShipped, true)).
			Order(expr.OrderBy(taskTotal, expr.SortDesc)).
			Limit(2),
		scan.Map2(scan.Field(taskCustom), scan.Field(total),
			func(name string, matched int64) struct {
				Name    string
				Matched int64
			} {
				return struct {
					Name    string
					Matched int64
				}{name, matched}
			}))
	if err != nil {
		t.Fatal(err)
	}

	if len(rows) != 2 {
		t.Fatalf("got %d rows, want a page of 2: %v", len(rows), rows)
	}
	if rows[0].Name != "cyd" || rows[1].Name != "eve" {
		t.Errorf("page is %v, want cyd then eve", rows)
	}
	// Three orders are shipped, and the page shows two of them.
	for _, r := range rows {
		if r.Matched != 3 {
			t.Errorf("row %v reports %d matching rows, want 3", r.Name, r.Matched)
		}
	}
}

// TestTask_ReportGroupedWithTotals is the other half of the same screen: a
// summary per group, filtered after aggregation and ordered by the aggregate.
//
// Ordering by an aggregate is the part worth executing rather than rendering.
// It is legal to order by an expression that only exists after grouping, and
// it is the sort of thing that renders fine and then fails at the database.
func TestTask_ReportGroupedWithTotals(t *testing.T) {
	ctx := context.Background()
	db := taskDB(t)

	revenue := expr.Sum(taskTotal)
	rows, err := scan.All(ctx, db,
		stmt.NewSelect(sqlite.Dialect{}).
			From(tasksTbl).
			GroupBy(taskRegion).
			Having(expr.Greater(expr.CountOf(taskID), 1)).
			Order(expr.OrderBy(revenue, expr.SortDesc)),
		scan.Map2(scan.Field(taskRegion), scan.Field(revenue),
			func(region string, rev int64) string {
				return region + "=" + itoa(rev)
			}))
	if err != nil {
		t.Fatal(err)
	}

	want := []string{"us=570", "eu=200"}
	if len(rows) != len(want) {
		t.Fatalf("got %v, want %v", rows, want)
	}
	for i := range want {
		if rows[i] != want[i] {
			t.Fatalf("got %v, want %v", rows, want)
		}
	}
}

// TestTask_WriteThenReadItBack walks one row through its whole life: inserted,
// updated, read, deleted, and confirmed gone.
//
// Each builder has its own tests. What this adds is the sequence, executed in
// order against one table, which is the only thing that catches a write that
// renders correctly and touches the wrong row — a WHERE bound to the wrong
// table, say, which every one of these statements would otherwise accept.
func TestTask_WriteThenReadItBack(t *testing.T) {
	ctx := context.Background()
	db := taskDB(t)
	d := sqlite.Dialect{}

	ins, err := stmt.NewInsert(d).Into(tasksTbl).Row(
		stmt.Set(taskID, int64(6)),
		stmt.Set(taskCustom, "fay"),
		stmt.Set(taskRegion, "eu"),
		stmt.Set(taskTotal, int64(10)),
		stmt.Set(taskShipped, false),
	).Build()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, ins.SQL, ins.Args...); err != nil {
		t.Fatal(err)
	}

	upd, err := stmt.NewUpdate(d).Table(tasksTbl).
		Set(stmt.Set(taskTotal, int64(999))).
		Where(expr.Eq(taskID, int64(6))).
		Build()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, upd.SQL, upd.Args...); err != nil {
		t.Fatal(err)
	}

	var got int64
	read, err := stmt.NewSelect(d).
		Columns(bind.Bind(taskTotal, &got)).
		From(tasksTbl).
		Where(expr.Eq(taskID, int64(6))).
		Build()
	if err != nil {
		t.Fatal(err)
	}
	if err := exec.One(ctx, db, read); err != nil {
		t.Fatal(err)
	}
	if got != 999 {
		t.Errorf("read back %d, want 999", got)
	}

	// The update must not have touched its neighbours.
	others, err := scan.All(ctx, db,
		stmt.NewSelect(d).From(tasksTbl).Where(expr.Eq(taskID, int64(5))),
		scan.Field(taskTotal))
	if err != nil {
		t.Fatal(err)
	}
	if len(others) != 1 || others[0] != 220 {
		t.Errorf("neighbouring row is %v, want [220]", others)
	}

	del, err := stmt.NewDelete(d).From(tasksTbl).Where(expr.Eq(taskID, int64(6))).Build()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, del.SQL, del.Args...); err != nil {
		t.Fatal(err)
	}
	if err := exec.One(ctx, db, read); err == nil {
		t.Error("row still readable after DELETE")
	}
}

// TestTask_ReportFromACTEChain is the query another builder's README leads
// with, which is
// the Postgres manual's regional-sales example: one CTE aggregating, a second
// selecting from the first, and a final statement filtered against it.
//
// It is here because building it is what found the empty-alias bug. A CTE is
// referenced by its own name and has no alias to give, so the natural
// declaration is Table{Name: "regional_sales"} — which used to qualify its
// columns as ""."region" and render FROM "regional_sales" AS "". SQLite
// accepts that, which is why no test in this repository ever complained;
// Postgres rejects it as a zero-length delimited identifier.
//
// So this asserts the whole chain returns the right rows, and every table in
// it is deliberately declared without an alias.
func TestTask_ReportFromACTEChain(t *testing.T) {
	ctx := context.Background()
	db := taskDB(t)
	d := sqlite.Dialect{}

	orders := expr.Table{Name: "orders"}
	oRegion := expr.Col[string](orders, "region")
	oTotal := expr.Col[int64](orders, "total")

	// The CTE and its columns are their own unaliased table.
	regional := expr.Table{Name: "regional"}
	rRegion := expr.Col[string](regional, "region")
	rTotal := expr.Col[int64](regional, "revenue")

	revenue := stmt.NewSelect(d).
		Exprs(oRegion, expr.As(expr.Sum(oTotal), "revenue")).
		From(orders).
		GroupBy(oRegion)

	rows, err := scan.All(ctx, db,
		stmt.NewSelect(d).
			With("regional", revenue).
			From(regional).
			Where(expr.Greater(rTotal, 250)).
			Order(expr.OrderBy(rRegion, expr.SortAsc)),
		scan.Map2(scan.Field(rRegion), scan.Field(rTotal),
			func(region string, rev int64) string { return region + "=" + itoa(rev) }))
	if err != nil {
		t.Fatal(err)
	}

	// eu totals 200 and is filtered out; us totals 570.
	if len(rows) != 1 || rows[0] != "us=570" {
		t.Fatalf("got %v, want [us=570]", rows)
	}
}

func itoa(n int64) string {
	if n == 0 {
		return "0"
	}
	var b []byte
	for n > 0 {
		b = append([]byte{byte('0' + n%10)}, b...)
		n /= 10
	}
	return string(b)
}
