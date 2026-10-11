package driversqlite_test

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"sort"
	"sync"
	"testing"

	driversqlite "github.com/ubgo/sqlb/contrib/driver-sqlite"
	"github.com/ubgo/sqlb/dialect/sqlite"
	"github.com/ubgo/sqlb/expr"
	"github.com/ubgo/sqlb/render"
	"github.com/ubgo/sqlb/scan"
	"github.com/ubgo/sqlb/stmt"
)

// The scan layer's end-to-end tasks: getting rows into the shape the rest of
// the program is written in.
//
// The shapes are the jobs. A report is a slice of the caller's own struct, an
// invoice is a parent holding its lines, an export runs several pages at once,
// and an admin screen selects columns the caller only learns at runtime. All
// four were once a hand-written loop over Each with a copy per row, which is
// exactly the finding this file exists to keep from coming back; each task
// below is written with no loop over rows except the one that folds a join,
// and that one is a library function.

// invLine is the domain type the rows are read into. It is deliberately not a
// tuple and not a struct whose fields happen to match the select list: cents
// is money, and the program that displays it wants it as its own type.
type cents int64

func (c cents) String() string { return fmt.Sprintf("%d.%02d", int64(c)/100, int64(c)%100) }

type invLine struct {
	SKU    string
	Amount cents
}

type invoice struct {
	ID       int64
	Customer string
	Lines    []invLine
}

func (i invoice) total() cents {
	var sum cents
	for _, l := range i.Lines {
		sum += l.Amount
	}
	return sum
}

var (
	invTbl      = expr.Table{Name: "invoices", Alias: "i"}
	invID       = expr.Col[int64](invTbl, "id")
	invCustomer = expr.Col[string](invTbl, "customer")

	invLineTbl   = expr.Table{Name: "invoice_lines", Alias: "l"}
	invLineID    = expr.Col[int64](invLineTbl, "id")
	invLineOwner = expr.Col[int64](invLineTbl, "invoice_id")
	// Nullable because the join below is LEFT: an invoice with no lines is a
	// row whose line columns are all NULL, and the Go type says so.
	invLineSKU   = expr.Col[*string](invLineTbl, "sku")
	invLineCents = expr.Col[*int64](invLineTbl, "cents")
)

// invDB seeds eight invoices and their lines. One invoice deliberately has no
// lines at all, which is the row that separates a correct fold from a
// plausible one, and both tables have a column called id, which is what the
// runtime-column task runs into.
func invDB(t *testing.T, dsn string) *sql.DB {
	t.Helper()
	ctx := context.Background()
	var db *sql.DB
	var err error
	if dsn == "" {
		db, err = driversqlite.Memory()
	} else {
		db, err = driversqlite.Open(dsn)
	}
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })

	for _, ddl := range []string{
		`CREATE TABLE invoices (id INTEGER PRIMARY KEY, customer TEXT NOT NULL)`,
		`CREATE TABLE invoice_lines (id INTEGER PRIMARY KEY, invoice_id INTEGER NOT NULL,
			sku TEXT NOT NULL, cents INTEGER NOT NULL)`,
	} {
		if _, err := db.ExecContext(ctx, ddl); err != nil {
			t.Fatal(err)
		}
	}
	for id := int64(1); id <= 8; id++ {
		if _, err := db.ExecContext(ctx, `INSERT INTO invoices VALUES (?, ?)`,
			id, fmt.Sprintf("customer-%d", id)); err != nil {
			t.Fatal(err)
		}
	}
	for _, l := range []struct {
		id, owner, cents int64
		sku              string
	}{
		{1, 1, 1250, "widget"}, {2, 1, 399, "shipping"},
		{3, 2, 10000, "licence"},
		// Invoice 3 has no lines on purpose.
		{4, 4, 5, "rounding"},
	} {
		if _, err := db.ExecContext(ctx, `INSERT INTO invoice_lines VALUES (?, ?, ?, ?)`,
			l.id, l.owner, l.sku, l.cents); err != nil {
			t.Fatal(err)
		}
	}
	return db
}

// TestTask_BuildInvoicesFromOneJoin is the job behind every detail screen:
// one query, one pass, a parent holding its children, in the caller's own
// types.
//
// Two things are worth executing. The Reader is built with scan.New rather
// than a chain of Map2, which is the door a caller needs once a row has more
// columns than a nested pair reads legibly — it is also the door
// contrib/structbind goes through, so a test of it is a test of that
// contract. And the fold has to tell an invoice with no lines from an invoice
// with one blank line, which is a distinction the SQL cannot carry: both come
// back as one row, and only the NULL key says which it was.
func TestTask_BuildInvoicesFromOneJoin(t *testing.T) {
	ctx := context.Background()
	db := invDB(t, "")

	// One flat row of the join, allocated fresh per row. The contract scan.New
	// states is the whole safety argument: these destinations are new every
	// call and nothing outside keeps a pointer to them, which is what lets the
	// same Reader run in several goroutines at once.
	type flat struct {
		invoice  int64
		customer string
		lineID   *int64
		sku      *string
		cents    *int64
	}
	reader := scan.New([]render.SQLer{invID, invCustomer, invLineID, invLineSKU, invLineCents},
		func() ([]any, func() flat) {
			var r flat
			return []any{&r.invoice, &r.customer, &r.lineID, &r.sku, &r.cents},
				func() flat { return r }
		})

	rows, err := scan.All(ctx, db,
		stmt.NewSelect(sqlite.Dialect{}).
			From(invTbl).
			Join(stmt.Join{
				Type: stmt.JoinLeft, Table: invLineTbl, Source: invLineTbl,
				On: []expr.Predicate{expr.EqExpr(invID, invLineOwner)},
			}).
			Where(expr.In(invID, 1, 2, 3)).
			Order(expr.OrderBy(invID, expr.SortAsc), expr.OrderBy(invLineID, expr.SortAsc)),
		reader)
	if err != nil {
		t.Fatal(err)
	}

	// The invoice key is never NULL, so the grouping is GroupBy; the lines are
	// collected with CollectPtr, which drops the row whose line columns came
	// back NULL. Without that, the invoice with no lines would hold one line
	// with no sku costing nothing, which is a plausible invoice and a wrong
	// one.
	got := scan.GroupBy(rows,
		func(r flat) int64 { return r.invoice },
		func(first flat, group []flat) invoice {
			return invoice{
				ID:       first.invoice,
				Customer: first.customer,
				Lines: scan.CollectPtr(group, func(r flat) *invLine {
					if r.sku == nil || r.cents == nil {
						return nil
					}
					return &invLine{SKU: *r.sku, Amount: cents(*r.cents)}
				}),
			}
		})

	if len(got) != 3 {
		t.Fatalf("got %d invoices from 4 flat rows, want 3: %v", len(got), got)
	}
	if n := len(got[0].Lines); n != 2 || got[0].total().String() != "16.49" {
		t.Errorf("invoice 1 has %d lines totalling %s, want 2 totalling 16.49", n, got[0].total())
	}
	if got[2].ID != 3 || got[2].Customer != "customer-3" {
		t.Errorf("the third invoice is %+v, want invoice 3 with its own columns", got[2])
	}
	if len(got[2].Lines) != 0 {
		t.Errorf("invoice 3 has no lines but came back with %v; a LEFT-join miss "+
			"was folded into a line rather than dropped", got[2].Lines)
	}
}

// TestTask_ExportEveryPageConcurrently runs the pages of an export at the same
// time, which is the reason this package exists beside the binding API.
//
// A Query built from bindings holds pointers into caller variables, so running
// one twice at once is refused with ErrQueryInUse — correctly, because the two
// executions would write into the same memory. A Reader holds nothing, so the
// same statement and the same Reader can be shared by as many goroutines as
// there are pages, and this asserts that every row arrives exactly once across
// all of them. Under -race it is also the test that would catch a Reader that
// quietly kept a destination between rows.
func TestTask_ExportEveryPageConcurrently(t *testing.T) {
	ctx := context.Background()
	// A shared-cache DSN rather than a plain memory database: the pages run on
	// several pooled connections at once and each connection to :memory: is
	// otherwise its own empty database.
	db := invDB(t, "file:taskexport?mode=memory&cache=shared")

	const pageSize = 2
	base := stmt.NewSelect(sqlite.Dialect{}).
		From(invTbl).
		Order(expr.OrderBy(invID, expr.SortAsc)).
		Limit(pageSize)
	reader := scan.Map2(scan.Field(invID), scan.Field(invCustomer),
		func(id int64, customer string) invoice { return invoice{ID: id, Customer: customer} })

	var mu sync.Mutex
	var seen []int64
	var wg sync.WaitGroup
	errs := make(chan error, 4)
	for page := range 4 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			rows, err := scan.All(ctx, db, base.Offset(page*pageSize), reader)
			if err != nil {
				errs <- fmt.Errorf("page %d: %w", page, err)
				return
			}
			mu.Lock()
			defer mu.Unlock()
			for _, r := range rows {
				seen = append(seen, r.ID)
			}
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Fatal(err)
	}

	sort.Slice(seen, func(i, j int) bool { return seen[i] < seen[j] })
	if want := []int64{1, 2, 3, 4, 5, 6, 7, 8}; fmt.Sprint(seen) != fmt.Sprint(want) {
		t.Fatalf("the concurrent export saw %v, want %v", seen, want)
	}
}

// TestTask_ShowWhateverColumnsTheOperatorPicked is the admin screen whose
// select list is not known until somebody clicks something.
//
// Neither Field nor Bind can express it, and correctly so: both need the
// column's Go type at compile time, and there is none when the column is a
// string chosen at runtime. Maps is the escape hatch, and the two things this
// asserts about it are the two that bite. A column name from outside is
// rendered into the statement text rather than bound, because no engine takes
// a placeholder where an identifier goes, so it has to survive a hostile name
// as one identifier. And a join of two tables that both have an id cannot be a
// map at all: the duplicate is reported by name instead of one value silently
// winning, and Slices, which is positional, is the answer.
func TestTask_ShowWhateverColumnsTheOperatorPicked(t *testing.T) {
	ctx := context.Background()
	db := invDB(t, "")
	d := sqlite.Dialect{}

	// What the operator picked, as it arrives: strings. The allow-list is the
	// application's business; what the library owes is that a name it accepts
	// stays one identifier.
	picked := []string{"customer", "id"}
	cols := make([]render.SQLer, len(picked))
	for i, name := range picked {
		cols[i] = expr.Col[any](invTbl, name)
	}

	q, err := stmt.NewSelect(d).Exprs(cols...).From(invTbl).
		Where(expr.Eq(invID, 2)).Build()
	if err != nil {
		t.Fatal(err)
	}
	maps, err := scan.Maps(ctx, db, q)
	if err != nil {
		t.Fatal(err)
	}
	if len(maps) != 1 {
		t.Fatalf("got %d rows, want 1: %v", len(maps), maps)
	}
	if maps[0]["customer"] != "customer-2" || maps[0]["id"] != int64(2) {
		t.Errorf("row is %v, want customer-2 and 2; nothing is converted, so an "+
			"integer arrives as int64 and that is the contract", maps[0])
	}

	// The same screen over a join. Both tables have an id, the result has two
	// columns called id, and a map cannot hold both.
	joined, err := stmt.NewSelect(d).
		Exprs(invID, invLineID).
		From(invTbl).
		Join(stmt.Join{
			Type: stmt.JoinInner, Table: invLineTbl, Source: invLineTbl,
			On: []expr.Predicate{expr.EqExpr(invID, invLineOwner)},
		}).
		Order(expr.OrderBy(invLineID, expr.SortAsc)).
		Build()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := scan.Maps(ctx, db, joined); !errors.Is(err, scan.ErrDuplicateColumn) {
		t.Fatalf("a join of two id columns gave %v, want ErrDuplicateColumn; one "+
			"of the two would otherwise have won silently", err)
	}

	names, values, err := scan.Slices(ctx, db, joined)
	if err != nil {
		t.Fatal(err)
	}
	if fmt.Sprint(names) != "[id id]" {
		t.Errorf("column names are %v, want both spellings of id", names)
	}
	if len(values) != 4 || fmt.Sprint(values[0]) != "[1 1]" {
		t.Errorf("positional rows are %v, want four rows starting [1 1]", values)
	}
}
