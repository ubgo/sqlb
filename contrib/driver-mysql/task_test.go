package drivermysql_test

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"

	"github.com/ubgo/sqlb"
	"github.com/ubgo/sqlb/ddl"
	"github.com/ubgo/sqlb/exec"
	"github.com/ubgo/sqlb/mysql"
	"github.com/ubgo/sqlb/scan"
)

// Tests in this file answer a different question than the rest of the suite.
//
// Everywhere else the question is "does this construct render, and does it
// execute". That question can only confirm what somebody already thought of;
// it cannot see a workflow that is missing, because a gap in the library is
// invisible until a person tries to do a job with it. `contrib/driver-mysql`
// had none of these until 2026-09-24, which made MySQL the one engine whose
// proofs all answered the first question and none the second.
//
// So each test here is one job a person actually has, done end to end against
// a real MySQL 8.4, using only what a consumer would import. Each asserts the
// DIFFERENCE between the answer somebody would reach for first and the
// correct one, or reads back an effect no rendering could show; a task that
// renders identically either way and is only right by luck is not a task
// test. A job that cannot be done without a hand-rolled loop or a raw string
// is the finding.

var (
	tkPages   = sqlb.Table{Name: "tk_pages", Alias: "p"}
	tkSKU     = sqlb.Col[string](tkPages, "sku")
	tkHits    = sqlb.Col[int64](tkPages, "hits")
	tkSKUCons = mysql.TableConstraint{Table: tkPages, Name: "tk_pages_sku"}
)

// tkExec builds and runs a statement, failing the test on either.
func tkExec(t *testing.T, db *sql.DB, b interface{ Build() (sqlb.Query, error) }) {
	t.Helper()
	q, err := b.Build()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(context.Background(), q.SQL, q.Args...); err != nil {
		t.Fatalf("%s: %v", q.SQL, err)
	}
}

// TestTask_CountHitsWithoutLosingAWrite is the counter every application has:
// a page view arrives, the row may or may not exist, and the count must end
// up right when several arrive at once.
//
// Read-modify-write is what people reach for and it loses writes under
// concurrency. MySQL's answer is an upsert that adds the incoming value to
// the stored one, and it has a rule no other engine has: the INSERT takes no
// table alias, so the row alias `new` makes a bare column name ambiguous and
// the EXISTING row's columns must be qualified by the table's name while the
// SET target stays bare. Getting that wrong does not fail — it compiles, it
// runs, and it stores the wrong number.
//
// The arithmetic is the assertion. Five stored plus three incoming must be
// eight: `hits = new.hits` would give three and `hits = hits + hits` ten, and
// all three render as valid SQL.
func TestTask_CountHitsWithoutLosingAWrite(t *testing.T) {
	db := openOrSkip(t)
	ctx := context.Background()
	cleanup(t, db, "DROP TABLE IF EXISTS tk_pages")
	runDDL(t, db, mysql.CreateTable(tkPages).
		Column(ddl.Column(tkSKU), ddl.Column(tkHits).Default(0)).
		Clause(mysql.Unique(tkSKUCons, tkSKU)))

	// One statement that both creates the row and increments it, so the
	// caller never has to know which case it is in.
	bump := func(sku string, by int64) {
		t.Helper()
		tkExec(t, db, mysql.Insert(tkPages).
			Row(sqlb.Set(tkSKU, sku), sqlb.Set(tkHits, by)).
			Clause(mysql.OnDuplicateKeyUpdate(sqlb.SetExpr(tkHits, sqlb.Add(tkHits, mysql.New(tkHits))))))
	}

	bump("a-1", 5)
	bump("a-1", 3)
	var hits int64
	if err := exec.One(ctx, db, mustQuery(t, mysql.Select().Columns(sqlb.Bind(tkHits, &hits)).
		From(tkPages).Where(sqlb.Eq(tkSKU, "a-1")))); err != nil {
		t.Fatal(err)
	}
	if hits != 8 {
		t.Fatalf("five stored plus three incoming gave %d; 3 means the upsert overwrote instead of adding, 10 means it doubled the stored value", hits)
	}

	// The property read-modify-write cannot give: sixteen concurrent bumps of
	// one each must total sixteen. The same loop written as SELECT then
	// UPDATE loses writes here, which is the whole reason for the upsert.
	const workers, each = 8, 2
	var wg sync.WaitGroup
	for range workers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for range each {
				q, err := mysql.Insert(tkPages).
					Row(sqlb.Set(tkSKU, "b-1"), sqlb.Set(tkHits, int64(1))).
					Clause(mysql.OnDuplicateKeyUpdate(sqlb.SetExpr(tkHits, sqlb.Add(tkHits, mysql.New(tkHits))))).Build()
				if err != nil {
					t.Error(err)
					return
				}
				if _, err := db.ExecContext(ctx, q.SQL, q.Args...); err != nil {
					t.Error(err)
					return
				}
			}
		}()
	}
	wg.Wait()
	var concurrent int64
	if err := exec.One(ctx, db, mustQuery(t, mysql.Select().Columns(sqlb.Bind(tkHits, &concurrent)).
		From(tkPages).Where(sqlb.Eq(tkSKU, "b-1")))); err != nil {
		t.Fatal(err)
	}
	if concurrent != workers*each {
		t.Errorf("%d concurrent increments landed as %d; an increment was lost", workers*each, concurrent)
	}
}

var (
	tkOrders = sqlb.Table{Name: "tk_orders", Alias: "o"}
	tkOrdID  = sqlb.Col[int64](tkOrders, "id")
	tkOrdYr  = sqlb.Col[int64](tkOrders, "yr")
	tkOrdAmt = sqlb.Col[int64](tkOrders, "amount")

	tkArchive = sqlb.Table{Name: "tk_orders_2025", Alias: "a"}
	tkArcID   = sqlb.Col[int64](tkArchive, "id")
	tkArcYr   = sqlb.Col[int64](tkArchive, "yr")
	tkArcAmt  = sqlb.Col[int64](tkArchive, "amount")
)

// TestTask_ArchiveLastYearWithoutTouchingThisYear is the annual job on a table
// that grew: last year's rows must leave the live table and stay readable
// somewhere, and this year's must not be disturbed.
//
// DELETE ... WHERE yr = 2025 is the obvious answer and it reads and writes
// every row it removes, holding locks the whole time. A partitioned table
// exchanges the partition's tablespace with an empty table of the identical
// structure, which moves the rows in constant time. The difference this
// asserts is the one that matters to the person doing it: after the exchange
// the archive holds exactly last year's rows, the live table still answers
// for this year's, and the partition that was swapped is empty — and the
// archive's own rows come back through the declarations, not through a count.
func TestTask_ArchiveLastYearWithoutTouchingThisYear(t *testing.T) {
	db := openOrSkip(t)
	ctx := context.Background()
	cleanup(t, db, "DROP TABLE IF EXISTS tk_orders", "DROP TABLE IF EXISTS tk_orders_2025")

	// The live table is partitioned by year, which is what makes the exchange
	// possible at all; the partition columns must be in every key, which
	// Build enforces rather than leaving to the server.
	runDDL(t, db, mysql.CreateTable(tkOrders).
		Column(ddl.Column(tkOrdID), ddl.Column(tkOrdYr), ddl.Column(tkOrdAmt)).
		Clause(mysql.PrimaryKey(tkOrdID, tkOrdYr),
			mysql.PartitionByRange(mysql.PartitionOn(tkOrdYr)).
				With(mysql.LessThan("p2025", 2026), mysql.LessThanMax("pmax"))))
	// The archive is the same shape without the partitioning, which MySQL
	// requires for an exchange; CreateTableLike is how it is kept identical
	// without restating the definition and letting the two drift.
	runDDL(t, db, mysql.CreateTableLike(tkArchive, tkOrders))
	runDDL(t, db, mysql.AlterTable(tkArchive).Clause(mysql.RemovePartitioning()))

	for _, row := range []struct{ id, yr, amount int64 }{
		{1, 2025, 100}, {2, 2025, 250}, {3, 2026, 700},
	} {
		tkExec(t, db, mysql.Insert(tkOrders).Row(
			sqlb.Set(tkOrdID, row.id), sqlb.Set(tkOrdYr, row.yr), sqlb.Set(tkOrdAmt, row.amount)))
	}

	runDDL(t, db, mysql.AlterTable(tkOrders).Clause(mysql.ExchangePartition("p2025", tkArchive)))

	// Last year's rows are in the archive, read back through its own columns.
	type order struct {
		id, yr, amount int64
	}
	archived, err := scan.All(ctx, db,
		mysql.Select().From(tkArchive).Order(sqlb.OrderBy(tkArcID, sqlb.SortAsc)),
		scan.Map2(scan.Map2(scan.Field(tkArcID), scan.Field(tkArcYr),
			func(id, yr int64) [2]int64 { return [2]int64{id, yr} }), scan.Field(tkArcAmt),
			func(p [2]int64, amt int64) order { return order{p[0], p[1], amt} }))
	if err != nil {
		t.Fatal(err)
	}
	want := []order{{1, 2025, 100}, {2, 2025, 250}}
	if len(archived) != len(want) {
		t.Fatalf("the archive holds %+v, want %+v", archived, want)
	}
	for i := range want {
		if archived[i] != want[i] {
			t.Errorf("archived row %d is %+v, want %+v", i, archived[i], want[i])
		}
	}
	// This year's rows are untouched and the live table still answers.
	var thisYear int64
	if err := exec.One(ctx, db, mustQuery(t, mysql.Select().Columns(sqlb.Bind(tkOrdAmt, &thisYear)).
		From(tkOrders).Where(sqlb.Eq(tkOrdYr, int64(2026))))); err != nil {
		t.Fatal(err)
	}
	if thisYear != 700 {
		t.Errorf("this year's row reads %d after the archive, want 700", thisYear)
	}
	// And the partition that was swapped is now empty, which is what says the
	// rows moved rather than being copied.
	if got := myScalar(t, db, "SELECT count(*) FROM tk_orders PARTITION (p2025)"); got != "0" {
		t.Errorf("the exchanged partition still holds %q rows", got)
	}
}

// TestTask_AddAColumnToATableYouCannotRebuild is the release-day job: a new
// column on a table too large to copy, during business hours.
//
// Without a clause MySQL picks for itself, and when it cannot do the change
// instantly it silently falls back to rebuilding the table — the migration
// finishes, an hour late, having blocked writes throughout. Naming
// AlgorithmInstant turns that silence into an error before anything starts.
//
// The assertion is the effect, read from InnoDB's own catalogue:
// total_row_versions counts the instant changes a table carries, so it rises
// when a column is added instantly and returns to zero when the table is
// rebuilt. No rendering can show that difference; both statements are valid
// SQL and only the server knows which one it did.
func TestTask_AddAColumnToATableYouCannotRebuild(t *testing.T) {
	db := openOrSkip(t)
	cleanup(t, db, "DROP TABLE IF EXISTS tk_big")
	big := sqlb.Table{Name: "tk_big", Alias: "b"}
	bigID := sqlb.Col[int64](big, "id")
	bigName := sqlb.Col[string](big, "name")
	bigNote := sqlb.Col[*string](big, "note")
	runDDL(t, db, mysql.CreateTable(big).Column(ddl.Column(bigID), ddl.Column(bigName)).
		Clause(mysql.PrimaryKey(bigID)))
	tkExec(t, db, mysql.Insert(big).Row(sqlb.Set(bigID, int64(1)), sqlb.Set(bigName, "ada")))

	versions := func() string {
		return myScalar(t, db, "SELECT total_row_versions FROM information_schema.innodb_tables WHERE name = CONCAT(DATABASE(), '/tk_big')")
	}
	if got := versions(); got != "0" {
		t.Fatalf("a fresh table carries %q row versions, want 0", got)
	}
	runDDL(t, db, mysql.AlterTable(big).
		Action(ddl.AddColumn(ddl.Column(bigNote).Nullable())).
		Clause(mysql.Algorithm(mysql.AlgorithmInstant)))
	if got := versions(); got != "1" {
		t.Errorf("after an instant ADD COLUMN the table carries %q row versions, want 1; 0 means MySQL rebuilt it", got)
	}

	// The half that makes naming the algorithm worth anything: a change that
	// cannot be instant is refused rather than quietly copying the table.
	s, err := mysql.AlterTable(big).
		Clause(mysql.ModifyColumn(ddl.Column(bigName).As(ddl.RawType[string]("VARCHAR(500)"))),
			mysql.Algorithm(mysql.AlgorithmInstant)).Build()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(context.Background(), s[0].SQL); err == nil {
		t.Error("ALGORITHM = INSTANT was accepted for a type change; without the refusal a release would have rebuilt the table instead")
	}
	// The same change goes through when the cost is accepted knowingly, and
	// the rebuild resets the instant versions, which is how the catalogue
	// says a copy happened.
	runDDL(t, db, mysql.AlterTable(big).
		Clause(mysql.ModifyColumn(ddl.Column(bigName).As(ddl.RawType[string]("VARCHAR(500)"))),
			mysql.Algorithm(mysql.AlgorithmCopy)))
	if got := versions(); got != "0" {
		t.Errorf("after a copying ALTER the table carries %q row versions, want 0", got)
	}
}

// TestTask_InstallALibrarySchemaTwice is what every library that owns tables
// has to survive: its install runs on a fresh database and again on one it
// already ran on, and the second run must change nothing and raise nothing.
//
// MySQL makes this harder than the other engines because CREATE INDEX has no
// IF NOT EXISTS and DROP INDEX has no IF EXISTS. The statements carry a
// SkipIf query instead — a catalogue lookup the runner performs first — and a
// runner that does not know about the field runs the statement anyway and
// fails on the repeat. This asserts both halves: the honouring runner is
// idempotent, and the ignoring one fails, which is what makes the field worth
// honouring rather than a decoration.
func TestTask_InstallALibrarySchemaTwice(t *testing.T) {
	db := openOrSkip(t)
	ctx := context.Background()
	cleanup(t, db, "DROP TABLE IF EXISTS tk_notes")
	notes := sqlb.Table{Name: "tk_notes", Alias: "n"}
	noteID := sqlb.Col[int64](notes, "id")
	noteBody := sqlb.Col[string](notes, "body")
	noteTag := sqlb.Col[string](notes, "tag")
	tagIndex := mysql.Index{Name: "tk_notes_tag"}

	// What the library would export as its install: the table and its index,
	// each written so a second run is a no-op.
	install := func() ddl.Script {
		t.Helper()
		var out ddl.Script
		for _, b := range []interface{ Build() (ddl.Script, error) }{
			mysql.CreateTable(notes).IfNotExists().
				Column(ddl.Column(noteID), ddl.Column(noteBody), ddl.Column(noteTag)).
				Clause(mysql.PrimaryKey(noteID)),
			mysql.CreateIndex(tagIndex, notes).On(mysql.IndexKey(noteTag)).IfNotExists(),
		} {
			s, err := b.Build()
			if err != nil {
				t.Fatal(err)
			}
			out = append(out, s...)
		}
		return out
	}

	// A runner that honours SkipIf, which is all contrib/migrate's Execute
	// does for this field and all a runner of your own has to do.
	run := func(s ddl.Script, honourSkipIf bool) error {
		for _, st := range s {
			if honourSkipIf && st.SkipIf != "" {
				var one int
				switch err := db.QueryRowContext(ctx, st.SkipIf).Scan(&one); {
				case err == nil:
					continue // the object is there; the statement is skipped
				case errors.Is(err, sql.ErrNoRows):
				default:
					return err
				}
			}
			if _, err := db.ExecContext(ctx, st.SQL); err != nil {
				return fmt.Errorf("%s: %w", st.SQL, err)
			}
		}
		return nil
	}

	for run2 := range 2 {
		if err := run(install(), true); err != nil {
			t.Fatalf("install run %d failed, so the schema is not repeatable: %v", run2+1, err)
		}
	}
	// One table, one index beside the primary key: the second run added
	// nothing.
	if got := myScalar(t, db, "SELECT count(DISTINCT index_name) FROM information_schema.statistics WHERE table_schema = DATABASE() AND table_name = 'tk_notes'"); got != "2" {
		t.Errorf("after two installs the table has %q distinct indexes, want 2", got)
	}
	// The table still works, which is worth checking because "idempotent" is
	// easy to achieve by doing nothing at all.
	tkExec(t, db, mysql.Insert(notes).Row(
		sqlb.Set(noteID, int64(1)), sqlb.Set(noteBody, "first"), sqlb.Set(noteTag, "x")))
	var body string
	if err := exec.One(ctx, db, mustQuery(t, mysql.Select().Columns(sqlb.Bind(noteBody, &body)).
		From(notes).Where(sqlb.Eq(noteTag, "x")))); err != nil {
		t.Fatal(err)
	}
	if body != "first" {
		t.Errorf("the installed table reads %q", body)
	}

	// And the half that says the guard is load-bearing: a runner that ignores
	// SkipIf fails on the repeat, because MySQL has no CREATE INDEX IF NOT
	// EXISTS to fall back on.
	if err := run(install(), false); err == nil {
		t.Error("a runner ignoring SkipIf installed twice without failing, so nothing here proves the guard is needed")
	}
}

var (
	tkAcc     = sqlb.Table{Name: "tk_accounts", Alias: "ac"}
	tkAccID   = sqlb.Col[int64](tkAcc, "id")
	tkAccOpen = sqlb.Col[bool](tkAcc, "is_open")

	tkEntry     = sqlb.Table{Name: "tk_entries", Alias: "en"}
	tkEntryID   = sqlb.Col[int64](tkEntry, "id")
	tkEntryAcc  = sqlb.Col[int64](tkEntry, "account_id")
	tkEntryCent = sqlb.Col[int64](tkEntry, "cents")
)

// TestTask_RefuseAWriteNoCheckConstraintCanExpress is the rule that always
// arrives late: an entry may not be posted to a closed account. It is a rule
// about two tables, and that is what makes it a job rather than a demo.
//
// A CHECK constraint cannot express it — MySQL rejects a subquery in one — so
// the naive answer is to check in the application, which every other writer
// then has to remember. A BEFORE INSERT trigger that raises SIGNAL puts the
// rule in the database, where a writer cannot forget it.
//
// Three things are asserted: MySQL really does refuse the CHECK, so the
// trigger is not being preferred on taste; a posting to a closed account is
// refused with the message reaching the client, where a rule enforced by a
// silent rollback would be indistinguishable from a bug; and a posting to an
// open account still succeeds, because a guard that refuses everything passes
// the first two.
//
// It also produced a finding about this package, recorded at the body below.
func TestTask_RefuseAWriteNoCheckConstraintCanExpress(t *testing.T) {
	db := openOrSkip(t)
	ctx := context.Background()
	cleanup(t, db, "DROP TABLE IF EXISTS tk_entries", "DROP TABLE IF EXISTS tk_accounts")
	runDDL(t, db, mysql.CreateTable(tkAcc).Column(ddl.Column(tkAccID), ddl.Column(tkAccOpen)).
		Clause(mysql.PrimaryKey(tkAccID)))
	runDDL(t, db, mysql.CreateTable(tkEntry).
		Column(ddl.Column(tkEntryID), ddl.Column(tkEntryAcc), ddl.Column(tkEntryCent)).
		Clause(mysql.PrimaryKey(tkEntryID), mysql.References(tkEntryAcc, tkAccID)))
	tkExec(t, db, mysql.Insert(tkAcc).Row(sqlb.Set(tkAccID, int64(1)), sqlb.Set(tkAccOpen, true)))
	tkExec(t, db, mysql.Insert(tkAcc).Row(sqlb.Set(tkAccID, int64(2)), sqlb.Set(tkAccOpen, false)))

	// First, that the rule genuinely cannot be a CHECK: MySQL refuses a
	// subquery in one, which is why the trigger is the answer and not a
	// preference.
	if !fails(db, "ALTER TABLE tk_entries ADD CONSTRAINT tk_entries_open CHECK ((SELECT is_open FROM tk_accounts WHERE id = account_id))") {
		t.Error("MySQL accepted a CHECK reading another table, so this rule did not need a trigger after all")
	}

	// The rule, as a trigger. The condition reads the other table, which is
	// what a CHECK cannot do, and StepBlock keeps the statement inside the
	// guard a typed step rather than more string.
	//
	// This task is what produced StepBlock. Written as three StepRaw values
	// the guard rendered "IF ... THEN; SIGNAL ...; END IF" — a body of
	// several steps separates every step, and a raw step that opens a block
	// is not a statement — and MySQL answered ER_PARSE_ERROR. Until
	// 2026-09-24 the only way to write this job was one hand-rolled string
	// with the SIGNAL spelled out inside it, which is exactly the finding a
	// task test exists to produce.
	runDDL(t, db, mysql.CreateTrigger(mysql.Trigger{Name: "tk_entries_bi"}).
		IfNotExists().Before(mysql.OnInsert, tkEntry).
		Do(mysql.StepBlock("IF NOT (SELECT is_open FROM tk_accounts WHERE id = NEW.account_id) THEN", "END IF",
			mysql.StepSignal(mysql.UserSQLState, "the account is closed"))))

	// A posting to the closed account is refused, and the reason arrives.
	q, err := mysql.Insert(tkEntry).Row(sqlb.Set(tkEntryID, int64(10)),
		sqlb.Set(tkEntryAcc, int64(2)), sqlb.Set(tkEntryCent, int64(500))).Build()
	if err != nil {
		t.Fatal(err)
	}
	_, err = db.ExecContext(ctx, q.SQL, q.Args...)
	if err == nil {
		t.Fatal("the entry was posted to a closed account")
	}
	if !strings.Contains(err.Error(), "the account is closed") {
		t.Errorf("the rule fired but its reason did not reach the client: %v", err)
	}

	// And the open account still takes an entry, so the guard is a rule and
	// not a wall.
	tkExec(t, db, mysql.Insert(tkEntry).Row(sqlb.Set(tkEntryID, int64(11)),
		sqlb.Set(tkEntryAcc, int64(1)), sqlb.Set(tkEntryCent, int64(700))))
	var cents int64
	if err := exec.One(ctx, db, mustQuery(t, mysql.Select().Columns(sqlb.Bind(tkEntryCent, &cents)).
		From(tkEntry).Where(sqlb.Eq(tkEntryAcc, int64(1))))); err != nil {
		t.Fatal(err)
	}
	if cents != 700 {
		t.Errorf("the legal entry reads %d, want 700", cents)
	}
}

// mustQuery builds a statement for the execution helpers, failing the test on
// a build error so each task reads as the job rather than as error handling.
func mustQuery(t *testing.T, b interface{ Build() (sqlb.Query, error) }) sqlb.Query {
	t.Helper()
	q, err := b.Build()
	if err != nil {
		t.Fatal(err)
	}
	return q
}
