package driversqlite_test

import (
	"context"
	"database/sql"
	"fmt"
	"testing"

	driversqlite "github.com/ubgo/sqlb/contrib/driver-sqlite"
	"github.com/ubgo/sqlb/dialect/sqlite"
	"github.com/ubgo/sqlb/expr"
	"github.com/ubgo/sqlb/scan"
	"github.com/ubgo/sqlb/stmt"
)

// The stmt layer's end-to-end tasks: jobs whose difficulty is the shape of the
// statement rather than the expressions in it.
//
// A CTE feeding a set operation, a window function filtered from outside the
// window, and a cursor over a composite key are three statements that a
// rendering test can only confirm spell the way their author expected. What
// they are for is an answer — the tenants in one list and not the other, one
// row per group, every row exactly once — and an answer needs an engine. Two
// of these have already caught something here that rendered cleanly: a set
// operation's branch scoping and a builder that wrote to its receiver.

// evtKey is one event, named by the pair that identifies it. The pair is the
// point: a single-column cursor cannot page this table.
type evtKey struct {
	Tenant string
	Seq    int64
}

func (k evtKey) String() string { return fmt.Sprintf("%s/%d", k.Tenant, k.Seq) }

var (
	evtTbl    = expr.Table{Name: "events", Alias: "e"}
	evtTenant = expr.Col[string](evtTbl, "tenant")
	evtSeq    = expr.Col[int64](evtTbl, "seq")
	evtKind   = expr.Col[string](evtTbl, "kind")
	evtAmount = expr.Col[int64](evtTbl, "amount")
)

var evtKeyReader = scan.Map2(scan.Field(evtTenant), scan.Field(evtSeq),
	func(tenant string, seq int64) evtKey { return evtKey{tenant, seq} })

// evtDB is a per-tenant event log: the seq restarts for every tenant, so
// nothing but the pair is unique and every task below has to say so.
func evtDB(t *testing.T) *sql.DB {
	t.Helper()
	ctx := context.Background()
	db, err := driversqlite.Memory()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if _, err := db.ExecContext(ctx, `CREATE TABLE events (
		tenant TEXT NOT NULL, seq INTEGER NOT NULL, kind TEXT NOT NULL,
		amount INTEGER NOT NULL, PRIMARY KEY (tenant, seq))`); err != nil {
		t.Fatal(err)
	}
	for _, row := range []struct {
		tenant string
		seq    int64
		kind   string
		amount int64
	}{
		{"t1", 1, "open", 100}, {"t1", 2, "paid", 50}, {"t1", 3, "failed", 20},
		{"t2", 1, "open", 70}, {"t2", 2, "paid", 10},
		{"t3", 1, "open", 5}, {"t3", 2, "paid", 7}, {"t3", 3, "paid", 9},
		{"t4", 1, "open", 3}, {"t4", 2, "failed", 4},
	} {
		if _, err := db.ExecContext(ctx, `INSERT INTO events VALUES (?, ?, ?, ?)`,
			row.tenant, row.seq, row.kind, row.amount); err != nil {
			t.Fatal(err)
		}
	}
	return db
}

// TestTask_TenantsThatPaidAndNeverFailed answers a question about two row sets
// rather than about rows: which tenants are in one list and not the other.
//
// The set operation is what makes it one statement. Written as two queries the
// difference is taken in Go, which means every tenant crosses the wire so that
// most of them can be discarded, and the two halves can disagree if a write
// lands between them. Written as a join it is an anti-join with a NOT EXISTS,
// which is the same answer and a different statement; EXCEPT is the spelling
// that says what the question was.
//
// Worth executing rather than rendering: a branch of a set operation is the
// construct SQLite refuses when it is parenthesised, so this is a shape whose
// text an engine has an opinion about, and the CTE feeding both branches has
// to be visible from the second one.
func TestTask_TenantsThatPaidAndNeverFailed(t *testing.T) {
	ctx := context.Background()
	db := evtDB(t)
	d := sqlite.Dialect{}

	// The CTE both branches read: the events that moved money. A real report
	// narrows here once — by date, by product — rather than in each branch,
	// which is the reason it is a CTE and not two copies of a predicate.
	settled := expr.Table{Name: "settled"}
	sTenant := expr.Col[string](settled, "tenant")
	sKind := expr.Col[string](settled, "kind")
	body := stmt.NewSelect(d).
		Exprs(evtTenant, evtKind).
		From(evtTbl).
		Where(expr.Greater(evtAmount, 0))

	// The left branch deliberately declares no select list: scan.All appends
	// the Reader's columns to the statement it is handed, which is the left
	// branch, so a branch that also declared them would select two columns
	// against the right branch's one. The asymmetry is real and nothing but
	// the engine reports it — "SELECTs to the left and right of EXCEPT do not
	// have the same number of result columns" is what building this the
	// obvious way says.
	paid := stmt.NewSelect(d).
		With("settled", body).
		From(settled).
		Where(expr.Eq(sKind, "paid"))
	failed := stmt.NewSelect(d).
		Exprs(sTenant).
		From(settled).
		Where(expr.Eq(sKind, "failed"))

	rows, err := scan.All(ctx, db, paid.Except(failed).Order(expr.OrderBy(sTenant, expr.SortAsc)),
		scan.Field(sTenant))
	if err != nil {
		t.Fatal(err)
	}

	// t1 paid and then failed, t4 never paid, t2 and t3 paid cleanly.
	if want := []string{"t2", "t3"}; fmt.Sprint(rows) != fmt.Sprint(want) {
		t.Fatalf("paid-and-never-failed tenants are %v, want %v", rows, want)
	}
}

// TestTask_LatestEventPerTenant is the top-row-per-group question, which is
// the one report that has no answer in plain GROUP BY.
//
// max(seq) per tenant gives the number and loses the row it came from, so the
// usual workaround is a second query per group — the N+1 that turns a report
// into a load test. A ranking window computed in a CTE and filtered outside it
// is the one-statement answer, and the filter is the part that needs the CTE:
// a window function cannot appear in the WHERE of the statement that computes
// it, because the window is evaluated after the WHERE.
func TestTask_LatestEventPerTenant(t *testing.T) {
	ctx := context.Background()
	db := evtDB(t)
	d := sqlite.Dialect{}

	// The CTE's own table and its output columns, including the rank, which
	// exists only in the CTE and is referenced from outside it by name.
	ranked := expr.Table{Name: "ranked"}
	rTenant := expr.Col[string](ranked, "tenant")
	rKind := expr.Col[string](ranked, "kind")
	rRank := expr.Col[int64](ranked, "rn")

	newestFirst := expr.NewWindow().
		PartitionBy(evtTenant).
		OrderBy(expr.OrderBy(evtSeq, expr.SortDesc))

	body := stmt.NewSelect(d).
		Exprs(evtTenant, evtKind, expr.As(expr.RowNumber().Over(newestFirst), "rn")).
		From(evtTbl)

	rows, err := scan.All(ctx, db,
		stmt.NewSelect(d).
			With("ranked", body).
			From(ranked).
			Where(expr.Eq(rRank, 1)).
			Order(expr.OrderBy(rTenant, expr.SortAsc)),
		scan.Map2(scan.Field(rTenant), scan.Field(rKind),
			func(tenant, kind string) string { return tenant + ":" + kind }))
	if err != nil {
		t.Fatal(err)
	}

	// One row per tenant, and it is the last event rather than the first: t1
	// ends failed although it opened, t3 ends paid although it has two paids.
	want := []string{"t1:failed", "t2:paid", "t3:paid", "t4:failed"}
	if fmt.Sprint(rows) != fmt.Sprint(want) {
		t.Fatalf("latest event per tenant is %v, want %v", rows, want)
	}
}

// TestTask_ExportEveryEventThroughACompositeCursor pages a whole table for an
// export: every row exactly once, no gap and no repeat, with a cursor rather
// than an OFFSET.
//
// Two things make it a task and not a construct test. The key is composite, so
// the cursor is a row comparison and a single-column one silently skips rows
// that share a tenant — the failure is missing data in an export, which nobody
// notices until a customer does. And every page is derived from one base
// builder held across the whole loop, which is the pattern a paginated
// endpoint is written in; it is only correct because a builder method returns
// a new statement, so the base after nine pages must still be the first page.
func TestTask_ExportEveryEventThroughACompositeCursor(t *testing.T) {
	ctx := context.Background()
	db := evtDB(t)

	const pageSize = 3
	// Declared once and never reassigned: every page below is derived from it.
	base := stmt.NewSelect(sqlite.Dialect{}).
		From(evtTbl).
		Order(expr.OrderBy(evtTenant, expr.SortAsc), expr.OrderBy(evtSeq, expr.SortAsc)).
		Limit(pageSize)

	var seen []evtKey
	cursor := evtKey{}
	for page := 0; page < 10; page++ {
		sel := base
		if page > 0 {
			// The cursor is the whole key compared as one row. Written as
			// tenant > ? OR (tenant = ? AND seq > ?) it means the same thing
			// and is the version people get wrong.
			sel = base.Where(expr.RowGreater(
				expr.Row(evtTenant, evtSeq),
				expr.Row(expr.Param(cursor.Tenant), expr.Param(cursor.Seq)),
			))
		}
		rows, err := scan.All(ctx, db, sel, evtKeyReader)
		if err != nil {
			t.Fatalf("page %d: %v", page, err)
		}
		if len(rows) == 0 {
			break
		}
		seen = append(seen, rows...)
		cursor = rows[len(rows)-1]
	}

	want := []evtKey{
		{"t1", 1}, {"t1", 2}, {"t1", 3},
		{"t2", 1}, {"t2", 2},
		{"t3", 1}, {"t3", 2}, {"t3", 3},
		{"t4", 1}, {"t4", 2},
	}
	if fmt.Sprint(seen) != fmt.Sprint(want) {
		t.Fatalf("the export walked\n%v\nwant\n%v", seen, want)
	}

	// The base is the thing the endpoint keeps. If any page had written its
	// cursor into it, this would come back short or empty rather than as the
	// first page, and the export above would still have passed.
	first, err := scan.All(ctx, db, base, evtKeyReader)
	if err != nil {
		t.Fatal(err)
	}
	if want := fmt.Sprint(seen[:pageSize]); fmt.Sprint(first) != want {
		t.Fatalf("the base builder now returns %v, want the first page %s", first, want)
	}
}
