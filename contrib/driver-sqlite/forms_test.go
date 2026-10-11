package driversqlite_test

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"testing"

	"github.com/ubgo/sqlb"
	driversqlite "github.com/ubgo/sqlb/contrib/driver-sqlite"
	"github.com/ubgo/sqlb/ddl"
	"github.com/ubgo/sqlb/exec"
	"github.com/ubgo/sqlb/sqlite"
)

// The within-statement forms executed: TEMP, schema qualification, a view's
// column list, a key's collation and sort order, a column-level primary key,
// deferrability and RAISE.
//
// Rendering proves the text; only this proves the SQL. Each case here runs the
// statement and then reads back the thing the form was for — the collation
// from a duplicate insert that must be refused, the schema from
// sqlite_schema, the trigger from the error it raises — rather than taking a
// clean Exec as evidence.

var (
	fxPeople = sqlb.Table{Name: "fx_people", Alias: "p"}
	fxID     = sqlb.Col[int64](fxPeople, "id")
	fxEmail  = sqlb.Col[string](fxPeople, "email")
	fxName   = sqlb.Col[string](fxPeople, "name")
)

func formsDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := driversqlite.Memory()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	// One connection, because every temporary object in this file lives on the
	// connection that created it and the pool would otherwise hand the next
	// statement a connection that has never seen it.
	db.SetMaxOpenConns(1)
	return db
}

// TestForms_CaseInsensitiveUniqueOnSQLite is the form the decorated key exists
// for, and the only thing that distinguishes it from a plain UNIQUE is a row
// the engine refuses.
func TestForms_CaseInsensitiveUniqueOnSQLite(t *testing.T) {
	ctx := context.Background()
	db := formsDB(t)
	s, err := sqlite.CreateTable(fxPeople).
		Column(ddl.Column(fxID), ddl.Column(fxEmail)).
		Clause(sqlite.PrimaryKey(fxID), sqlite.UniqueOn(sqlite.Key(fxEmail).Collate(sqlite.NoCase))).Build()
	run(t, db, s, err)

	if _, err := db.ExecContext(ctx, `INSERT INTO fx_people VALUES (1, 'Ada@example.com')`); err != nil {
		t.Fatal(err)
	}
	// The same address in another case. Under a plain UNIQUE this succeeds;
	// under COLLATE NOCASE it is a duplicate, which is the whole point.
	_, err = db.ExecContext(ctx, `INSERT INTO fx_people VALUES (2, 'ada@EXAMPLE.com')`)
	if err == nil {
		t.Fatal("the same address in another case was accepted; the collation did not reach the constraint")
	}
	if !strings.Contains(err.Error(), "UNIQUE constraint failed") {
		t.Errorf("the refusal changed: %v", err)
	}
	// A genuinely different address still fits.
	if _, err := db.ExecContext(ctx, `INSERT INTO fx_people VALUES (3, 'grace@example.com')`); err != nil {
		t.Errorf("a different address was refused: %v", err)
	}
}

// TestForms_ColumnKeyDescExecutes pins the consequence Desc has beyond order:
// an INTEGER PRIMARY KEY DESC is not an alias for the rowid, so the column can
// hold NULL. That is a change of storage, and it is exactly the sort of thing
// a rendering test cannot see.
func TestForms_ColumnKeyDescExecutes(t *testing.T) {
	ctx := context.Background()
	db := formsDB(t)
	desc := sqlb.Table{Name: "fx_desc"}
	descID := sqlb.Col[int64](desc, "id")
	s, err := sqlite.CreateTable(desc).Column(ddl.Column(descID).Nullable()).
		Clause(sqlite.ColumnKey(descID).Desc()).Build()
	run(t, db, s, err)
	if _, err := db.ExecContext(ctx, `INSERT INTO fx_desc VALUES (NULL)`); err != nil {
		t.Errorf("INTEGER PRIMARY KEY DESC refused a NULL key, so it is a rowid alias after all: %v", err)
	}

	// The ascending form is the rowid alias, and refuses nothing: SQLite fills
	// a NULL in it with the next rowid.
	asc := sqlb.Table{Name: "fx_asc"}
	ascID := sqlb.Col[int64](asc, "id")
	s, err = sqlite.CreateTable(asc).Column(ddl.Column(ascID).Nullable()).
		Clause(sqlite.ColumnKey(ascID)).Build()
	run(t, db, s, err)
	if _, err := db.ExecContext(ctx, `INSERT INTO fx_asc VALUES (NULL)`); err != nil {
		t.Fatal(err)
	}
	var id sql.NullInt64
	if err := db.QueryRowContext(ctx, `SELECT id FROM fx_asc`).Scan(&id); err != nil {
		t.Fatal(err)
	}
	if !id.Valid {
		t.Error("a plain INTEGER PRIMARY KEY kept the NULL, so it is not the rowid alias")
	}
}

// TestForms_ColumnKeyOnConflictExecutes pins that a key's conflict resolution
// changes what an ordinary INSERT does, with no OR clause written anywhere.
func TestForms_ColumnKeyOnConflictExecutes(t *testing.T) {
	ctx := context.Background()
	db := formsDB(t)
	tbl := sqlb.Table{Name: "fx_replace"}
	key := sqlb.Col[string](tbl, "k")
	val := sqlb.Col[string](tbl, "v")
	s, err := sqlite.CreateTable(tbl).Column(ddl.Column(key), ddl.Column(val)).
		Clause(sqlite.ColumnKey(key).OnConflict(sqlite.OrReplace)).Build()
	run(t, db, s, err)
	for _, v := range []string{"first", "second"} {
		if _, err := db.ExecContext(ctx, `INSERT INTO fx_replace VALUES ('k', ?)`, v); err != nil {
			t.Fatalf("a plain INSERT was refused, so ON CONFLICT REPLACE did not reach the key: %v", err)
		}
	}
	var got string
	if err := db.QueryRowContext(ctx, `SELECT v FROM fx_replace`).Scan(&got); err != nil {
		t.Fatal(err)
	}
	if got != "second" {
		t.Errorf("the row holds %q, want second", got)
	}
}

// TestForms_DeferrabilityExecutes pins the one setting that changes behaviour,
// against the one that does not. A deferred key lets a transaction leave a
// reference dangling in the middle; an immediate one does not.
func TestForms_DeferrabilityExecutes(t *testing.T) {
	ctx := context.Background()
	db := formsDB(t)
	parent := sqlb.Table{Name: "fx_parent"}
	pid := sqlb.Col[int64](parent, "id")
	for _, spec := range []struct {
		table    sqlb.Table
		deferral sqlite.Deferrability
	}{
		{sqlb.Table{Name: "fx_deferred"}, sqlite.DeferrableInitiallyDeferred},
		{sqlb.Table{Name: "fx_immediate"}, sqlite.DeferrableInitiallyImmediate},
		{sqlb.Table{Name: "fx_not"}, sqlite.NotDeferrable},
	} {
		child := sqlb.Col[int64](spec.table, "parent_id")
		if spec.table.Name == "fx_deferred" {
			s, err := sqlite.CreateTable(parent).Column(ddl.Column(pid)).Clause(sqlite.PrimaryKey(pid)).Build()
			run(t, db, s, err)
		}
		s, err := sqlite.CreateTable(spec.table).Column(ddl.Column(child)).
			Clause(sqlite.References(child, pid).Deferrable(spec.deferral)).Build()
		run(t, db, s, err)
	}

	// Deferred: the row may point at a parent that does not exist yet, as long
	// as it exists by COMMIT.
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO fx_deferred VALUES (7)`); err != nil {
		t.Fatalf("a deferred key was checked immediately: %v", err)
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO fx_parent VALUES (7)`); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatalf("the deferred key failed at COMMIT although the parent was there: %v", err)
	}

	// Immediate, and NOT DEFERRABLE, are the same at run time: both refuse the
	// row where it is written. That is the measurement behind the doc comment
	// saying only the deferred setting changes anything.
	for _, table := range []string{"fx_immediate", "fx_not"} {
		tx, err := db.BeginTx(ctx, nil)
		if err != nil {
			t.Fatal(err)
		}
		_, err = tx.ExecContext(ctx, `INSERT INTO `+table+` VALUES (404)`)
		_ = tx.Rollback()
		if err == nil {
			t.Errorf("%s accepted a dangling reference, so it behaved as deferred", table)
		}
	}
}

// TestForms_RaiseRefusesTheRow is the trigger form: a constraint a CHECK
// cannot express, enforced by RAISE, with the message arriving as the error.
func TestForms_RaiseRefusesTheRow(t *testing.T) {
	ctx := context.Background()
	db := formsDB(t)
	s, err := sqlite.CreateTable(fxPeople).Column(ddl.Column(fxID), ddl.Column(fxName)).
		Clause(sqlite.PrimaryKey(fxID)).Build()
	run(t, db, s, err)
	s, err = sqlite.CreateTrigger(sqlite.Trigger{Name: "fx_people_name_required"}).
		Before(sqlite.OnInsert(), fxPeople).
		When(sqlb.Eq(sqlite.New(fxName), "")).
		Do(sqlite.StepSelect(sqlite.Select(sqlite.Raise(sqlite.RaiseAbort, "name required")))).Build()
	run(t, db, s, err)

	_, err = db.ExecContext(ctx, `INSERT INTO fx_people VALUES (1, '')`)
	if err == nil {
		t.Fatal("the empty name was accepted; the trigger did not fire")
	}
	if !strings.Contains(err.Error(), "name required") {
		t.Errorf("the message did not reach the caller: %v", err)
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO fx_people VALUES (2, 'Ada')`); err != nil {
		t.Errorf("a good row was refused: %v", err)
	}

	// IGNORE is the form with no message: the row is skipped and the caller is
	// told nothing, which is why it is documented as dangerous for validation.
	s, err = sqlite.CreateTrigger(sqlite.Trigger{Name: "fx_people_skip_x"}).
		Before(sqlite.OnInsert(), fxPeople).
		When(sqlb.Eq(sqlite.New(fxName), "x")).
		Do(sqlite.StepSelect(sqlite.Select(sqlite.RaiseIgnore()))).Build()
	run(t, db, s, err)
	if _, err := db.ExecContext(ctx, `INSERT INTO fx_people VALUES (3, 'x')`); err != nil {
		t.Fatalf("RAISE(IGNORE) reported an error: %v", err)
	}
	var n int
	if err := db.QueryRowContext(ctx, `SELECT count(*) FROM fx_people WHERE name = 'x'`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Errorf("RAISE(IGNORE) wrote the row anyway (%d rows)", n)
	}
}

// TestForms_TemporaryObjectsExecute runs every temporary form and reads each
// back from the temp schema, which is the only thing that shows TEMP was
// honoured rather than merely accepted.
func TestForms_TemporaryObjectsExecute(t *testing.T) {
	ctx := context.Background()
	db := formsDB(t)
	scratch := sqlb.Table{Name: "fx_scratch"}
	sid := sqlb.Col[int64](scratch, "id")
	s, err := sqlite.CreateTable(scratch).Column(ddl.Column(sid)).Clause(sqlite.Temporary()).Build()
	run(t, db, s, err)

	view := sqlite.View{Name: "fx_scratch_view"}
	s, err = sqlite.CreateView(view).Temporary().As(sqlite.Select(sid).From(scratch)).Build()
	run(t, db, s, err)

	s, err = sqlite.CreateTrigger(sqlite.Trigger{Name: "fx_scratch_trigger"}).Temporary().
		After(sqlite.OnInsert(), scratch).
		Do(sqlite.StepSelect(sqlite.Select(sid).From(scratch))).Build()
	run(t, db, s, err)

	// A temporary index has no TEMP spelling — CREATE TEMP INDEX is a syntax
	// error, measured — so it is written by putting the index in temp.
	s, err = sqlite.CreateIndex(sqlite.Index{Name: "fx_scratch_ix", Schema: "temp"}, scratch).
		On(sqlite.IndexKey(sid)).Build()
	run(t, db, s, err)

	for _, name := range []string{"fx_scratch", "fx_scratch_view", "fx_scratch_trigger", "fx_scratch_ix"} {
		var got string
		if err := db.QueryRowContext(ctx,
			`SELECT name FROM temp.sqlite_schema WHERE name = ?`, name).Scan(&got); err != nil {
			t.Errorf("%s is not in the temp schema: %v", name, err)
		}
	}
	// And none of them is in main.
	var n int
	if err := db.QueryRowContext(ctx,
		`SELECT count(*) FROM main.sqlite_schema WHERE name LIKE 'fx_scratch%'`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Errorf("%d temporary objects landed in main", n)
	}
}

// TestForms_TemporaryQualifiedIsTheEnginesRule pins that Build's refusal
// matches SQLite's rather than being stricter or laxer than it.
func TestForms_TemporaryQualifiedIsTheEnginesRule(t *testing.T) {
	ctx := context.Background()
	db := formsDB(t)
	_, err := db.ExecContext(ctx, `CREATE TEMP TABLE "main"."fx_q"(i INTEGER)`)
	if err == nil {
		t.Fatal("SQLite accepted a temporary table qualified by main; Build's refusal is now stricter than the engine")
	}
	if !strings.Contains(err.Error(), "must be unqualified") {
		t.Errorf("the engine's refusal changed: %v", err)
	}
	// And temp itself is accepted by both.
	if _, err := db.ExecContext(ctx, `CREATE TEMP TABLE "temp"."fx_q"(i INTEGER)`); err != nil {
		t.Errorf("SQLite refused a temporary table qualified by temp: %v", err)
	}
}

// TestForms_ViewColumnsExecute pins that the names in the column list are the
// names the view has, read back through the same Col values that wrote them.
func TestForms_ViewColumnsExecute(t *testing.T) {
	ctx := context.Background()
	db := formsDB(t)
	s, err := sqlite.CreateTable(fxPeople).Column(ddl.Column(fxID), ddl.Column(fxName)).
		Clause(sqlite.PrimaryKey(fxID)).Build()
	run(t, db, s, err)
	if _, err := db.ExecContext(ctx, `INSERT INTO fx_people VALUES (1, 'Ada')`); err != nil {
		t.Fatal(err)
	}

	view := sqlite.View{Name: "fx_named"}
	viewT := sqlb.Table{Name: "fx_named"}
	viewKey := sqlb.Col[int64](viewT, "person_id")
	viewLabel := sqlb.Col[string](viewT, "label")
	s, err = sqlite.CreateView(view).Columns(viewKey, viewLabel).
		As(sqlite.Select(fxID, fxName).From(fxPeople)).Build()
	run(t, db, s, err)

	// The names the view was created with are the names it is read by: one
	// declaration, both directions.
	var label string
	q, err := sqlite.Select().Columns(sqlb.Bind(viewLabel, &label)).From(viewT).
		Where(sqlb.Eq(viewKey, int64(1))).Build()
	if err != nil {
		t.Fatal(err)
	}
	if err := exec.One(ctx, db, q); err != nil {
		t.Fatalf("the view's columns are not the names it was created with: %v", err)
	}
	if label != "Ada" {
		t.Errorf("read back %q", label)
	}
}

// TestForms_SchemaQualifiedObjectsExecute runs each object with a schema and
// reads it back from that schema's catalogue. The virtual-table cases are the
// ones that were silently wrong before: the table's schema was dropped, so a
// statement meant for temp created its table in main.
func TestForms_SchemaQualifiedObjectsExecute(t *testing.T) {
	ctx := context.Background()
	db := formsDB(t)
	tempNotes := sqlb.Table{Name: "fx_temp_fts", Schema: "temp"}
	tempBody := sqlb.Col[string](tempNotes, "body")
	s, err := sqlite.CreateFTS5(tempNotes).Columns(tempBody).Build()
	run(t, db, s, err)

	geo := sqlb.Table{Name: "fx_temp_geo", Schema: "temp"}
	geoLabel := sqlb.Col[string](geo, "label")
	s, err = sqlite.CreateGeopoly(geo, geoLabel).Build()
	run(t, db, s, err)

	for _, name := range []string{"fx_temp_fts", "fx_temp_geo"} {
		var got string
		if err := db.QueryRowContext(ctx,
			`SELECT name FROM temp.sqlite_schema WHERE name = ?`, name).Scan(&got); err != nil {
			t.Errorf("%s was not created in temp: %v", name, err)
		}
	}

	// An index and a view explicitly in main, dropped by the same values.
	s, err = sqlite.CreateTable(fxPeople).Column(ddl.Column(fxID), ddl.Column(fxName)).
		Clause(sqlite.PrimaryKey(fxID)).Build()
	run(t, db, s, err)
	ix := sqlite.Index{Name: "fx_people_name", Schema: "main"}
	s, err = sqlite.CreateIndex(ix, fxPeople).On(sqlite.IndexKey(fxName)).Build()
	run(t, db, s, err)
	var n int
	if err := db.QueryRowContext(ctx,
		`SELECT count(*) FROM main.sqlite_schema WHERE name = 'fx_people_name'`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Errorf("the qualified index is not in main")
	}
	s, err = sqlite.DropIndex(ix).Build()
	run(t, db, s, err)
}

// TestForms_ChecksNarrowedToOneTableExecute runs the per-table checks and
// requires each to fail on a broken table and pass on a sound one, because a
// check that cannot fail is indistinguishable from one that works.
func TestForms_ChecksNarrowedToOneTableExecute(t *testing.T) {
	ctx := context.Background()
	db := formsDB(t)
	parent := sqlb.Table{Name: "fx_ck_parent"}
	pid := sqlb.Col[int64](parent, "id")
	child := sqlb.Table{Name: "fx_ck_child"}
	cid := sqlb.Col[int64](child, "id")
	cparent := sqlb.Col[int64](child, "parent_id")
	other := sqlb.Table{Name: "fx_ck_other"}
	oid := sqlb.Col[int64](other, "id")
	for _, b := range []interface{ Build() (ddl.Script, error) }{
		sqlite.CreateTable(parent).Column(ddl.Column(pid)).Clause(sqlite.PrimaryKey(pid)),
		sqlite.CreateTable(child).Column(ddl.Column(cid), ddl.Column(cparent)).
			Clause(sqlite.PrimaryKey(cid), sqlite.References(cparent, pid)),
		sqlite.CreateTable(other).Column(ddl.Column(oid)).Clause(sqlite.PrimaryKey(oid)),
	} {
		s, err := b.Build()
		run(t, db, s, err)
	}
	for _, name := range []string{"integrity", "quick", "foreign keys"} {
		var built func() (ddl.Script, error)
		switch name {
		case "integrity":
			built = sqlite.IntegrityCheckOf(child).Build
		case "quick":
			built = sqlite.QuickCheckOf(child).Build
		default:
			built = sqlite.ForeignKeyCheckOf(child).Build
		}
		s, err := built()
		if err != nil {
			t.Fatal(err)
		}
		if err := runScript(ctx, db, s); err != nil {
			t.Errorf("%s of a sound table failed: %v", name, err)
		}
	}

	if _, err := db.ExecContext(ctx, `PRAGMA foreign_keys = OFF`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO fx_ck_child VALUES (1, 404)`); err != nil {
		t.Fatal(err)
	}
	s, err := sqlite.ForeignKeyCheckOf(child).Build()
	if err != nil {
		t.Fatal(err)
	}
	if err := runScript(ctx, db, s); err == nil {
		t.Fatal("the narrowed check passed with a dangling reference in that very table")
	}
	// And the narrowing is real: the same check on another table is clean,
	// where the whole-database check is not.
	s, err = sqlite.ForeignKeyCheckOf(other).Build()
	if err != nil {
		t.Fatal(err)
	}
	if err := runScript(ctx, db, s); err != nil {
		t.Errorf("a table with no foreign key at all reported a violation: %v", err)
	}
	s, err = sqlite.ForeignKeyCheck().Build()
	if err != nil {
		t.Fatal(err)
	}
	if err := runScript(ctx, db, s); err == nil {
		t.Error("the whole-database check missed the violation the narrowed one found")
	}
}

// TestTask_StageACopyOfATableWithCreateTableAs is the task the statement
// exists for: take a snapshot of a table's rows in one pass, then read it back
// through declarations of its own.
//
// It also pins the two measurements the doc comment rests on, because both are
// the kind of thing a rendering test cannot see and a reader would otherwise
// have to trust: a second run under IfNotExists appends nothing, and the copy
// keeps none of the source's constraints.
func TestTask_StageACopyOfATableWithCreateTableAs(t *testing.T) {
	ctx := context.Background()
	db := formsDB(t)
	s, err := sqlite.CreateTable(fxPeople).
		Column(ddl.Column(fxID), ddl.Column(fxName)).
		Clause(sqlite.PrimaryKey(fxID)).Build()
	run(t, db, s, err)
	if _, err := db.ExecContext(ctx, `INSERT INTO fx_people VALUES (1, 'Ada'), (2, 'Grace')`); err != nil {
		t.Fatal(err)
	}

	staging := sqlb.Table{Name: "fx_people_staging"}
	stageID := sqlb.Col[int64](staging, "id")
	stageName := sqlb.Col[string](staging, "name")
	stmt := sqlite.CreateTableAs(staging, sqlite.Select(fxID, fxName).From(fxPeople)).IfNotExists()
	s, err = stmt.Build()
	run(t, db, s, err)

	// The rows are there, read through the staging table's own declarations.
	var name string
	q, err := sqlite.Select().Columns(sqlb.Bind(stageName, &name)).From(staging).
		Where(sqlb.Eq(stageID, int64(2))).Build()
	if err != nil {
		t.Fatal(err)
	}
	if err := exec.One(ctx, db, q); err != nil {
		t.Fatal(err)
	}
	if name != "Grace" {
		t.Errorf("read back %q", name)
	}

	// A second run appends nothing, which is what makes the statement
	// repeatable in an install.
	s, err = stmt.Build()
	run(t, db, s, err)
	var n int
	if err := db.QueryRowContext(ctx, `SELECT count(*) FROM fx_people_staging`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 2 {
		t.Errorf("the second run left %d rows, so IF NOT EXISTS appended instead of skipping", n)
	}

	// And the copy kept none of the source's constraints: the primary key is
	// gone, so the duplicate the source would refuse is accepted here. This is
	// the sentence in the doc comment, executed.
	if _, err := db.ExecContext(ctx, `INSERT INTO fx_people VALUES (1, 'again')`); err == nil {
		t.Error("the source accepted a duplicate key, so this test proves nothing")
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO fx_people_staging VALUES (1, 'again')`); err != nil {
		t.Errorf("the copy refused a duplicate key, so it kept the source's primary key: %v", err)
	}
}

// TestForms_CreateTableAsTemporaryExecutes runs the temporary form and reads
// the table back out of the temp schema.
func TestForms_CreateTableAsTemporaryExecutes(t *testing.T) {
	ctx := context.Background()
	db := formsDB(t)
	s, err := sqlite.CreateTable(fxPeople).Column(ddl.Column(fxID), ddl.Column(fxName)).
		Clause(sqlite.PrimaryKey(fxID)).Build()
	run(t, db, s, err)
	if _, err := db.ExecContext(ctx, `INSERT INTO fx_people VALUES (1, 'Ada')`); err != nil {
		t.Fatal(err)
	}
	scratch := sqlb.Table{Name: "fx_people_scratch"}
	s, err = sqlite.CreateTableAs(scratch, sqlite.Select(fxID, fxName).From(fxPeople)).Temporary().Build()
	run(t, db, s, err)

	var got string
	if err := db.QueryRowContext(ctx,
		`SELECT name FROM temp.sqlite_schema WHERE name = 'fx_people_scratch'`).Scan(&got); err != nil {
		t.Errorf("the temporary copy is not in the temp schema: %v", err)
	}
	var n int
	if err := db.QueryRowContext(ctx,
		`SELECT count(*) FROM main.sqlite_schema WHERE name = 'fx_people_scratch'`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Error("the temporary copy landed in main")
	}
}

// TestForms_NotNullOnConflictChangesWhatAnInsertDoes is the half a rendering
// test cannot reach. Each action is run against a row that leaves the column
// null, and the table is read back, so what is pinned is what SQLite DID
// rather than what the statement said.
//
// The plain NOT NULL is the control: without the clause the same insert
// fails, so a case that passes because the row was always legal is not
// possible.
func TestForms_NotNullOnConflictChangesWhatAnInsertDoes(t *testing.T) {
	ctx := context.Background()
	db := formsDB(t)

	type outcome struct {
		refused bool
		rows    int
		value   string
	}
	// The measurement, from 3.50.3. REPLACE is the only action that keeps the
	// row, and it keeps it by writing the column's DEFAULT.
	want := map[sqlite.OrAction]outcome{
		sqlite.OrReplace:  {refused: false, rows: 1, value: "untitled"},
		sqlite.OrIgnore:   {refused: false, rows: 0},
		sqlite.OrAbort:    {refused: true, rows: 0},
		sqlite.OrFail:     {refused: true, rows: 0},
		sqlite.OrRollback: {refused: true, rows: 0},
	}
	for action, exp := range want {
		t.Run(string(action), func(t *testing.T) {
			tbl := sqlb.Table{Name: "fx_nn_" + strings.ToLower(string(action))}
			id := sqlb.Col[int64](tbl, "id")
			body := sqlb.Col[string](tbl, "body")
			s, err := sqlite.CreateTable(tbl).
				Column(ddl.Column(id), ddl.Column(body).Default("untitled")).
				Clause(sqlite.PrimaryKey(id), sqlite.NotNullOnConflict(body, action)).Build()
			run(t, db, s, err)

			_, err = db.ExecContext(ctx, `INSERT INTO "`+tbl.Name+`"("id", "body") VALUES (1, NULL)`)
			if exp.refused && err == nil {
				t.Errorf("ON CONFLICT %s accepted the null row", action)
			}
			if !exp.refused && err != nil {
				t.Errorf("ON CONFLICT %s refused the null row: %v", action, err)
			}
			var n int
			if err := db.QueryRowContext(ctx, `SELECT count(*) FROM "`+tbl.Name+`"`).Scan(&n); err != nil {
				t.Fatal(err)
			}
			if n != exp.rows {
				t.Errorf("ON CONFLICT %s left %d rows, want %d", action, n, exp.rows)
			}
			if exp.value != "" {
				var got string
				if err := db.QueryRowContext(ctx, `SELECT "body" FROM "`+tbl.Name+`"`).Scan(&got); err != nil {
					t.Fatal(err)
				}
				if got != exp.value {
					t.Errorf("ON CONFLICT %s wrote %q, want %q", action, got, exp.value)
				}
			}
		})
	}

	// The control: the same column with a plain NOT NULL refuses the same row,
	// so every case above measures the clause rather than a legal insert.
	plain := sqlb.Table{Name: "fx_nn_plain"}
	pid := sqlb.Col[int64](plain, "id")
	pbody := sqlb.Col[string](plain, "body")
	s, err := sqlite.CreateTable(plain).
		Column(ddl.Column(pid), ddl.Column(pbody).Default("untitled")).
		Clause(sqlite.PrimaryKey(pid)).Build()
	run(t, db, s, err)
	if _, err := db.ExecContext(ctx, `INSERT INTO fx_nn_plain("id", "body") VALUES (1, NULL)`); err == nil {
		t.Fatal("a plain NOT NULL accepted the null row, so the cases above prove nothing")
	}
}

// TestForms_NotNullReplaceNeedsADefault pins the refusal that replaced a doc
// comment. Until 2026-09-24 this test executed the statement and asserted the
// insert failed, because the clause could not see the column's default and the
// caveat was only written down; ddl.DefaultRequirer now lets Build refuse it.
//
// Both halves are kept, because each proves something the other cannot. Build
// refusing it is the behaviour. The raw SQL running on a real SQLite is the
// evidence that the refusal is SQLite's rule and not an invention of this
// package: CREATE TABLE accepts the definition, and only the first NULL written
// to the column fails — which is exactly why it had to move to Build, since a
// schema that installs cleanly and fails months later in production is the
// failure this module exists to prevent.
func TestForms_NotNullReplaceNeedsADefault(t *testing.T) {
	ctx := context.Background()
	db := formsDB(t)
	tbl := sqlb.Table{Name: "fx_nn_nodefault"}
	id := sqlb.Col[int64](tbl, "id")
	body := sqlb.Col[string](tbl, "body")

	// The behaviour: Build names the missing default.
	_, err := sqlite.CreateTable(tbl).Column(ddl.Column(id), ddl.Column(body)).
		Clause(sqlite.PrimaryKey(id), sqlite.NotNullOnConflict(body, sqlite.OrReplace)).Build()
	if !errors.Is(err, ddl.ErrNoDefault) {
		t.Fatalf("OrReplace on a column with no default must fail Build with ErrNoDefault, got %v", err)
	}

	// The control: the same clause on a column WITH a default builds, so the
	// refusal is about the default and not about the clause.
	if _, err := sqlite.CreateTable(tbl).Column(ddl.Column(id), ddl.Column(body).Default("untitled")).
		Clause(sqlite.PrimaryKey(id), sqlite.NotNullOnConflict(body, sqlite.OrReplace)).Build(); err != nil {
		t.Fatalf("OrReplace with a default must build: %v", err)
	}

	// The other four actions never write the default, so none of them needs
	// one; refusing them would be a rule this package made up.
	for _, action := range []sqlite.OrAction{sqlite.OrIgnore, sqlite.OrAbort, sqlite.OrFail, sqlite.OrRollback} {
		if _, err := sqlite.CreateTable(tbl).Column(ddl.Column(id), ddl.Column(body)).
			Clause(sqlite.PrimaryKey(id), sqlite.NotNullOnConflict(body, action)).Build(); err != nil {
			t.Errorf("%s does not write a default and must build without one: %v", string(action), err)
		}
	}

	// The evidence: SQLite itself accepts the definition and refuses only the
	// write. Run as raw SQL because Build no longer produces it.
	if _, err := db.ExecContext(ctx, `CREATE TABLE fx_nn_nodefault("id" INTEGER PRIMARY KEY, "body" TEXT NOT NULL ON CONFLICT REPLACE)`); err != nil {
		t.Fatalf("SQLite refused the definition itself, so the case for a Build refusal has changed: %v", err)
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO fx_nn_nodefault("id", "body") VALUES (1, NULL)`); err == nil {
		t.Error("SQLite kept the row with no default to write; ErrNoDefault now refuses a definition SQLite accepts")
	}
}

// TestForms_CheckConflictClauseIsIgnoredBySQLite is why no CHECK conflict
// clause is built, kept as a test rather than as a sentence because the reason
// is a measurement that could change in a later SQLite.
//
// A column-level CHECK refuses the clause outright — a syntax error, which the
// railroad diagram does not say — and a table-level one accepts it and does
// nothing: every action behaves as ABORT. UNIQUE ON CONFLICT IGNORE is the
// control, and it demonstrably works, so the CHECK result is the engine
// ignoring the clause rather than this test failing to observe it.
func TestForms_CheckConflictClauseIsIgnoredBySQLite(t *testing.T) {
	ctx := context.Background()
	db := formsDB(t)

	if _, err := db.ExecContext(ctx, `CREATE TABLE fx_ck_col(a INTEGER CHECK (a > 0) ON CONFLICT IGNORE)`); err == nil {
		t.Error("SQLite now accepts a conflict clause on a column CHECK; the reason for leaving it out has changed")
	}

	// The control: on a UNIQUE the same clause is honoured.
	if _, err := db.ExecContext(ctx, `CREATE TABLE fx_u_ig(a INTEGER UNIQUE ON CONFLICT IGNORE)`); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		if _, err := db.ExecContext(ctx, `INSERT INTO fx_u_ig VALUES (1)`); err != nil {
			t.Fatalf("UNIQUE ON CONFLICT IGNORE refused the duplicate: %v", err)
		}
	}
	var n int
	if err := db.QueryRowContext(ctx, `SELECT count(*) FROM fx_u_ig`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatalf("the control left %d rows; UNIQUE ON CONFLICT IGNORE is not honoured either, so this test proves nothing", n)
	}

	// And on a table-level CHECK the same clause changes nothing.
	for _, action := range []string{"IGNORE", "REPLACE", "FAIL", "ROLLBACK"} {
		table := "fx_ck_" + strings.ToLower(action)
		if _, err := db.ExecContext(ctx,
			`CREATE TABLE `+table+`(a INTEGER, CHECK (a > 0) ON CONFLICT `+action+`)`); err != nil {
			t.Fatalf("%s: %v", action, err)
		}
		if _, err := db.ExecContext(ctx, `INSERT INTO `+table+` VALUES (-1)`); err == nil {
			t.Errorf("CHECK ON CONFLICT %s was honoured; it can now be built", action)
		}
	}
}

// TestForms_AnUnknownTimeModifierIsSilentlyNull is the measurement behind
// Offset taking a closed Unit rather than a string.
//
// SQLite does not refuse a modifier it does not recognise. The whole function
// returns NULL, with no error, so a misspelled unit is a null date somebody
// reads months later rather than a failure anybody sees. That cannot be fixed
// by quoting — the modifier IS correctly quoted, and reaches SQLite as one
// argument — so the only defence is to close the part of the vocabulary that
// can be closed, which is the unit.
func TestForms_AnUnknownTimeModifierIsSilentlyNull(t *testing.T) {
	ctx := context.Background()
	db := formsDB(t)
	ask := func(modifier string) (string, bool) {
		t.Helper()
		var v sql.NullString
		if err := db.QueryRowContext(ctx, `SELECT date('2020-03-15', ?)`, modifier).Scan(&v); err != nil {
			t.Fatalf("%q: %v", modifier, err)
		}
		return v.String, v.Valid
	}
	for _, modifier := range []string{"+1 banana", "start of week", "", "'); DROP TABLE x; --"} {
		if v, ok := ask(modifier); ok {
			t.Errorf("SQLite now answers %q with %q rather than NULL; the reason Unit is closed has changed", modifier, v)
		}
	}
	// The control: a real modifier answers, so the NULLs above are the engine
	// rejecting the modifier rather than the query being wrong.
	if v, ok := ask("+1 day"); !ok || v != "2020-03-16" {
		t.Fatalf("the control answered %q/%v; this test proves nothing", v, ok)
	}
	// And every unit this package offers is one SQLite accepts.
	for _, u := range []sqlite.Unit{sqlite.Day, sqlite.Hour, sqlite.Minute, sqlite.Second, sqlite.Month, sqlite.Year} {
		if _, ok := ask(string(sqlite.Offset(1, u))); !ok {
			t.Errorf("SQLite does not recognise the unit %q this package offers", u)
		}
	}
	// The modifiers with fixed spellings, likewise — including the three added
	// after measuring the vocabulary, which older SQLite did not have.
	for _, m := range []sqlite.Modifier{
		sqlite.StartOfDay, sqlite.StartOfMonth, sqlite.StartOfYear,
		sqlite.UTC, sqlite.LocalTime, sqlite.Subsec, sqlite.Ceiling, sqlite.Floor,
		sqlite.Weekday(1),
	} {
		if _, ok := ask(string(m)); !ok {
			t.Errorf("SQLite does not recognise the modifier %q this package offers", m)
		}
	}
}
