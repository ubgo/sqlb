package driversqlite_test

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/ubgo/sqlb"
	driversqlite "github.com/ubgo/sqlb/contrib/driver-sqlite"
	"github.com/ubgo/sqlb/ddl"
	"github.com/ubgo/sqlb/scan"
	"github.com/ubgo/sqlb/schema"
	"github.com/ubgo/sqlb/sqlite"
)

// The schema layer's end-to-end tasks: an application whose database is
// assembled from libraries that have never heard of each other.
//
// schema describes and never does, so its tasks are the ones a host program
// has: work out what order these tables can be created in, find out at startup
// that two libraries want the same name, and query a table whose declaration
// arrived from somewhere else without writing any of its names down a second
// time. Each is executed here because the ordering is only worth anything if
// the database accepts it, and a foreign key that never reached the engine
// orders just as convincingly as one that did.

// Two libraries, written out as two declaration blocks that import nothing
// from each other. "billing" owns the accounts; "ledger" owns the entries and
// references an account by a column it was handed.
var (
	schAccounts = sqlb.Table{Name: "bill_account", Alias: "acc"}
	schAccID    = sqlb.Col[int64](schAccounts, "id")
	schAccName  = sqlb.Col[string](schAccounts, "name")
	schAccLimit = sqlb.Col[int64](schAccounts, "credit_limit")

	schEntries   = sqlb.Table{Name: "ledger_entry", Alias: "led"}
	schEntryID   = sqlb.Col[int64](schEntries, "id")
	schEntryAcc  = sqlb.Col[int64](schEntries, "account_id")
	schEntryCent = sqlb.Col[int64](schEntries, "cents")
)

// billingTable is what the billing library would export: its own CREATE, built
// from its own column declarations.
func billingTable() *sqlite.CreateTableReady {
	return sqlite.CreateTable(schAccounts).
		Column(ddl.Column(schAccID), ddl.Column(schAccName), ddl.Column(schAccLimit)).
		Clause(sqlite.PrimaryKey(schAccID))
}

// ledgerTable is the second library's. Its foreign key names billing's column,
// which is the only thing the two share, and it is a value rather than a
// string so a rename in billing fails to compile here.
func ledgerTable() *sqlite.CreateTableReady {
	return sqlite.CreateTable(schEntries).
		Column(ddl.Column(schEntryID), ddl.Column(schEntryAcc), ddl.Column(schEntryCent)).
		Clause(
			sqlite.PrimaryKey(schEntryID),
			sqlite.ForeignKey(
				sqlite.TableConstraint{Table: schEntries, Name: "ledger_entry_account"},
				sqlite.RefPair(schEntryAcc, schAccID),
			),
		)
}

// schDescribe is the description each library contributes to the application's
// Set: the schema.Table its own CREATE statement produces, derived from the
// statement rather than written out beside it, so the two cannot disagree.
func schDescribe(t *testing.T, c *sqlite.CreateTableReady) schema.Table {
	t.Helper()
	desc, err := c.Describe()
	if err != nil {
		t.Fatal(err)
	}
	return desc
}

// TestTask_InstallASchemaTwoPackagesContributed is the host application's
// startup: collect every library's tables, check they fit together, and create
// them in an order that respects the references between them.
//
// The ordering is the job. Neither library can work it out — billing has never
// heard of the ledger, and the ledger knows only the one column it references
// — so it exists only in the Set the application assembles, and it has to be
// derived rather than configured, because a list of tables in creation order
// is a third place for the truth to live and it goes stale the first time a
// library adds a table.
//
// Executed rather than rendered because an ordering nothing runs is a claim.
// The last two writes here are what prove the foreign key in the description
// is the same foreign key the engine is enforcing: an entry pointing at a real
// account is accepted and an orphan is refused.
func TestTask_InstallASchemaTwoPackagesContributed(t *testing.T) {
	ctx := context.Background()
	db := schEmptyDB(t)

	// The ledger contributes first, so the declaration order is the wrong
	// creation order and Sorted has something to do.
	set, err := schema.New("ledger", schDescribe(t, ledgerTable()))
	if err != nil {
		t.Fatal(err)
	}
	if err := set.Add("billing", schDescribe(t, billingTable())); err != nil {
		t.Fatal(err)
	}
	// Validate is the startup check: a reference to a table nobody
	// contributed is a missing dependency, and it is worth finding here
	// rather than as a create that fails halfway through.
	if err := set.Validate(); err != nil {
		t.Fatalf("the two libraries do not fit together: %v", err)
	}

	sorted, err := set.Sorted()
	if err != nil {
		t.Fatal(err)
	}
	if len(sorted) != 2 || sorted[0].Name != schAccounts.Name || sorted[1].Name != schEntries.Name {
		t.Fatalf("creation order is %v, want the referenced table first", schNames(sorted))
	}

	// The Set orders descriptions; the statements that make the tables are the
	// libraries' own, so the application keeps the two side by side and runs
	// them in the order the Set gave.
	statements := map[string]*sqlite.CreateTableReady{
		schAccounts.Name: billingTable(),
		schEntries.Name:  ledgerTable(),
	}
	for _, table := range sorted {
		script, err := statements[table.Name].Build()
		if err != nil {
			t.Fatalf("%s: %v", table.Name, err)
		}
		for _, st := range script {
			if _, err := db.ExecContext(ctx, st.SQL); err != nil {
				t.Fatalf("%s: %v", st.SQL, err)
			}
		}
	}

	ins, err := sqlite.Insert(schAccounts).
		Row(sqlb.Set(schAccID, int64(1)), sqlb.Set(schAccName, "ada"), sqlb.Set(schAccLimit, int64(5000))).
		Build()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, ins.SQL, ins.Args...); err != nil {
		t.Fatal(err)
	}

	entry, err := sqlite.Insert(schEntries).
		Row(sqlb.Set(schEntryID, int64(1)), sqlb.Set(schEntryAcc, int64(1)), sqlb.Set(schEntryCent, int64(250))).
		Build()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, entry.SQL, entry.Args...); err != nil {
		t.Fatalf("an entry against a real account was refused: %v", err)
	}

	orphan, err := sqlite.Insert(schEntries).
		Row(sqlb.Set(schEntryID, int64(2)), sqlb.Set(schEntryAcc, int64(99)), sqlb.Set(schEntryCent, int64(1))).
		Build()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, orphan.SQL, orphan.Args...); err == nil {
		t.Error("an entry against an account that does not exist was stored, so " +
			"the declared foreign key never reached the table")
	}
}

// TestTask_QueryATableThroughItsDeclaration is what a host does with a
// library's declaration once the table exists: read from it without writing
// any of its names a second time.
//
// A column named by a string is a runtime failure waiting for the release that
// renames it, and it is the failure every hand-written query has. MustCol
// takes the name once, at package load, and checks the Go type against the
// declaration while it is there — so the join below writes no column name of
// its own, and the mistake it protects against is asserted too: asking for a text
// column where the declaration says bigint is an error at startup rather than
// a scan that fails in production on the first row.
func TestTask_QueryATableThroughItsDeclaration(t *testing.T) {
	ctx := context.Background()
	db := schInstalled(t)

	// What the library handed over: a description, not a package of columns.
	accounts := schDescribe(t, billingTable())
	entries := schDescribe(t, ledgerTable())

	// The host derives exactly the columns it needs, typed.
	accID := schema.MustCol[int64](accounts, "id")
	accName := schema.MustCol[string](accounts, "name")
	entAcc := schema.MustCol[int64](entries, "account_id")
	entCents := schema.MustCol[int64](entries, "cents")

	rows, err := scan.All(ctx, db,
		sqlite.Select().
			From(accounts.Ref()).
			Join(sqlb.Join{
				Type: sqlb.JoinInner, Table: entries.Ref(), Source: entries.Ref(),
				On: []sqlb.Predicate{sqlb.EqExpr(accID, entAcc)},
			}).
			GroupBy(accName).
			Order(sqlb.OrderBy(accName, sqlb.SortAsc)),
		scan.Map2(scan.Field(accName), scan.Field(sqlb.Sum(entCents)),
			func(name string, total int64) string { return fmt.Sprintf("%s=%d", name, total) }))
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"ada=350", "bob=40"}; fmt.Sprint(rows) != fmt.Sprint(want) {
		t.Fatalf("the per-account totals are %v, want %v", rows, want)
	}

	// The two mistakes the declaration can catch, both at the moment the
	// column is derived rather than at the moment a row is scanned.
	if _, err := schema.Col[string](accounts, "credit_limit"); !errors.Is(err, schema.ErrTypeMismatch) {
		t.Errorf("asking for a string column on a bigint gave %v, want ErrTypeMismatch", err)
	}
	if _, err := schema.Col[int64](accounts, "creditlimit"); err == nil {
		t.Error("a misspelled column name was accepted")
	}
}

// TestTask_TwoLibrariesClaimTheSameTable is the collision an application finds
// out about the moment it adds a dependency, and the reason a Set records who
// contributed what.
//
// Last write wins is the alternative and it is the worst one available: the
// two declarations are never identical, so one library's table silently has
// the other's columns and the failure appears later, as a missing column in a
// query nobody changed. The error here has to name both owners, because the
// fix is a prefix or a schema and neither can be chosen without knowing who
// collided.
func TestTask_TwoLibrariesClaimTheSameTable(t *testing.T) {
	set, err := schema.New("billing", schDescribe(t, billingTable()))
	if err != nil {
		t.Fatal(err)
	}

	// A second library that also calls its table bill_account, with different
	// columns, which is the realistic version of this accident.
	other := sqlb.Table{Name: schAccounts.Name, Alias: "b2"}
	clash := sqlite.CreateTable(other).Column(ddl.Column(sqlb.Col[string](other, "uuid")))

	err = set.Add("invoicing", schDescribe(t, clash))
	if !errors.Is(err, schema.ErrDuplicateTable) {
		t.Fatalf("the second claim gave %v, want ErrDuplicateTable", err)
	}
	for _, owner := range []string{"billing", "invoicing"} {
		if !strings.Contains(err.Error(), owner) {
			t.Errorf("the collision does not name %q: %v", owner, err)
		}
	}

	// And the Set is unchanged: the first contribution stands, so a host that
	// logs the error and carries on is not left with half a schema.
	if got := set.Tables(); len(got) != 1 || len(got[0].Columns) != 3 {
		t.Errorf("after the refused claim the set holds %v", schNames(got))
	}
}

// schEmptyDB is a database with nothing in it, for the task that installs the
// schema itself.
func schEmptyDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := driversqlite.Memory()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

// schInstalled is the same database with both libraries installed and seeded,
// which is where a host program starts every day after the first.
func schInstalled(t *testing.T) *sql.DB {
	t.Helper()
	ctx := context.Background()
	db := schEmptyDB(t)
	for _, c := range []*sqlite.CreateTableReady{billingTable(), ledgerTable()} {
		script, err := c.Build()
		if err != nil {
			t.Fatal(err)
		}
		for _, st := range script {
			if _, err := db.ExecContext(ctx, st.SQL); err != nil {
				t.Fatalf("%s: %v", st.SQL, err)
			}
		}
	}
	for _, seed := range []struct {
		accID       int64
		name        string
		entries     []int64
		creditLimit int64
	}{
		{1, "ada", []int64{250, 100}, 5000},
		{2, "bob", []int64{40}, 100},
	} {
		ins, err := sqlite.Insert(schAccounts).Row(
			sqlb.Set(schAccID, seed.accID),
			sqlb.Set(schAccName, seed.name),
			sqlb.Set(schAccLimit, seed.creditLimit),
		).Build()
		if err != nil {
			t.Fatal(err)
		}
		if _, err := db.ExecContext(ctx, ins.SQL, ins.Args...); err != nil {
			t.Fatal(err)
		}
		for i, cents := range seed.entries {
			entry, err := sqlite.Insert(schEntries).Row(
				sqlb.Set(schEntryID, seed.accID*10+int64(i)),
				sqlb.Set(schEntryAcc, seed.accID),
				sqlb.Set(schEntryCent, cents),
			).Build()
			if err != nil {
				t.Fatal(err)
			}
			if _, err := db.ExecContext(ctx, entry.SQL, entry.Args...); err != nil {
				t.Fatal(err)
			}
		}
	}
	return db
}

// schNames is what a failure message about an ordering should print: the
// names, in the order the Set gave them.
func schNames(tables []schema.Table) []string {
	out := make([]string, len(tables))
	for i, t := range tables {
		out[i] = t.Name
	}
	return out
}
