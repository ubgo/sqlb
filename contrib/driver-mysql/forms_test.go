package drivermysql_test

import (
	"context"
	"database/sql"
	"strings"
	"testing"

	"github.com/ubgo/sqlb"
	"github.com/ubgo/sqlb/ddl"
	"github.com/ubgo/sqlb/mysql"
)

// The forms inside statements the detector already called built, executed.
// The audit that found them read MySQL 8.4's own HELP output statement by
// statement; this file is what turns each from a reading into a proof, and it
// has already earned its keep: CREATE INDEX takes no comma before ALGORITHM,
// which every rendering test agreed with and the server did not.

// TestDDL_MySQLTemporaryTables covers TEMPORARY on all three creation forms
// and the drop that names it, including the shadowing that makes the keyword
// worth having: a temporary table hides a permanent one of the same name, and
// DROP TABLE would remove the temporary while looking like it removed the
// permanent one.
func TestDDL_MySQLTemporaryTables(t *testing.T) {
	db := openOrSkip(t)
	ctx := context.Background()
	// A temporary table lives on one connection, so the whole test runs on a
	// single pinned one; on the pool it would vanish between statements.
	conn, err := db.Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = conn.Close() }()
	cleanup(t, db, "DROP TABLE IF EXISTS f_perm")

	perm := sqlb.Table{Name: "f_perm", Alias: "p"}
	permID := sqlb.Col[int64](perm, "id")
	permN := sqlb.Col[int64](perm, "n")
	runDDL(t, db, mysql.CreateTable(perm).Column(ddl.Column(permID), ddl.Column(permN)).Clause(mysql.PrimaryKey(permID)))
	if _, err := conn.ExecContext(ctx, `INSERT INTO f_perm VALUES (1, 100), (2, 200)`); err != nil {
		t.Fatal(err)
	}

	exec := func(b interface{ Build() (ddl.Script, error) }) {
		t.Helper()
		s, err := b.Build()
		if err != nil {
			t.Fatal(err)
		}
		for _, st := range s {
			if _, err := conn.ExecContext(ctx, st.SQL); err != nil {
				t.Fatalf("%s: %v", st.SQL, err)
			}
		}
	}
	scalar := func(q string) string {
		t.Helper()
		var v string
		if err := conn.QueryRowContext(ctx, q).Scan(&v); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
		return v
	}

	// A temporary table shadowing the permanent one of the same name: the
	// hazard the doc comment names, shown rather than asserted in prose.
	exec(mysql.CreateTable(perm).Column(ddl.Column(permID), ddl.Column(permN)).Clause(mysql.Temporary(), mysql.PrimaryKey(permID)))
	if got := scalar(`SELECT count(*) FROM f_perm`); got != "0" {
		t.Errorf("the temporary table did not shadow the permanent one: count %q", got)
	}
	exec(mysql.DropTemporaryTable(perm))
	if got := scalar(`SELECT count(*) FROM f_perm`); got != "2" {
		t.Errorf("the permanent table's rows did not come back: count %q", got)
	}

	// LIKE copies the structure and no rows.
	like := sqlb.Table{Name: "f_like", Alias: "l"}
	exec(mysql.CreateTableLike(like, perm).Temporary().IfNotExists())
	if got := scalar(`SELECT count(*) FROM f_like`); got != "0" {
		t.Errorf("LIKE copied rows: %q", got)
	}
	// A temporary table is absent from information_schema, so the structure
	// is checked by using it: the copy must take a row and then refuse a
	// second one with the same key, which proves LIKE copied the key.
	if _, err := conn.ExecContext(ctx, `INSERT INTO f_like VALUES (1, 1)`); err != nil {
		t.Fatalf("the LIKE copy would not take a row: %v", err)
	}
	if _, err := conn.ExecContext(ctx, `INSERT INTO f_like VALUES (1, 2)`); err == nil {
		t.Error("LIKE did not copy the primary key, so the duplicate was accepted")
	}

	// AS SELECT derives the columns and fills the table in one statement.
	asT := sqlb.Table{Name: "f_as", Alias: "s"}
	exec(mysql.CreateTableAs(asT, mysql.Select(permID, permN).From(perm)).Temporary())
	if got := scalar(`SELECT sum(n) FROM f_as`); got != "300" {
		t.Errorf("AS SELECT filled the table with %q", got)
	}
	// IF NOT EXISTS on an existing table skips the query too, so the rows are
	// not doubled. Older MySQL appended here, which is why this is measured
	// rather than assumed.
	exec(mysql.CreateTableAs(asT, mysql.Select(permID, permN).From(perm)).Temporary().IfNotExists())
	if got := scalar(`SELECT count(*) FROM f_as`); got != "2" {
		t.Errorf("IF NOT EXISTS on an existing table left %q rows; on 8.4 it skips the query", got)
	}
	exec(mysql.DropTemporaryTable(like, asT).IfExists())
	// DROP TEMPORARY TABLE refuses a permanent table outright.
	s, err := mysql.DropTemporaryTable(perm).Build()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := conn.ExecContext(ctx, s[0].SQL); err == nil {
		t.Error("DROP TEMPORARY TABLE removed a permanent table")
	}
	if got := scalar(`SELECT count(*) FROM f_perm`); got != "2" {
		t.Errorf("the permanent table lost rows: %q", got)
	}
}

// TestDDL_MySQLIndexOptions executes every index option and the ALGORITHM and
// LOCK clauses, and reads the options back from information_schema. The
// separator between the options and ALGORITHM is what this case exists for:
// it is a space, and the comma the first version wrote was ER_PARSE_ERROR.
func TestDDL_MySQLIndexOptions(t *testing.T) {
	db := openOrSkip(t)
	cleanup(t, db, "DROP TABLE IF EXISTS f_ix")
	tbl := sqlb.Table{Name: "f_ix", Alias: "x"}
	id := sqlb.Col[int64](tbl, "id")
	sku := sqlb.Col[string](tbl, "sku")
	body := sqlb.Col[string](tbl, "body")
	runDDL(t, db, mysql.CreateTable(tbl).
		Column(ddl.Column(id), ddl.Column(sku), ddl.Column(body).As(ddl.RawType[string]("TEXT"))).
		Clause(mysql.PrimaryKey(id).Using(mysql.BTree),
			mysql.TableIndex(mysql.Index{Name: "f_ix_sku"}, sku).With(mysql.IndexComment("lookup"))))
	if got := myScalar(t, db, `SELECT index_comment FROM information_schema.statistics WHERE table_schema = DATABASE() AND table_name = 'f_ix' AND index_name = 'f_ix_sku'`); got != "lookup" {
		t.Errorf("the inline key's comment is %q", got)
	}
	// Every option at once, with the online-DDL clauses after them.
	runDDL(t, db, mysql.CreateIndex(mysql.Index{Name: "f_ix_body"}, tbl).On(mysql.IndexPrefix(body, 32)).
		With(mysql.IndexComment("prefix"), mysql.IndexInvisible()).
		Algorithm(mysql.AlgorithmInplace).Lock(mysql.LockNone))
	if got := myScalar(t, db, `SELECT concat(index_comment, ' ', is_visible) FROM information_schema.statistics WHERE table_schema = DATABASE() AND table_name = 'f_ix' AND index_name = 'f_ix_body'`); got != "prefix NO" {
		t.Errorf("the index's options are %q", got)
	}
	runDDL(t, db, mysql.CreateIndex(mysql.Index{Name: "f_ix_vis"}, tbl).On(mysql.IndexKey(sku)).With(mysql.IndexVisible()))
	if got := myScalar(t, db, `SELECT is_visible FROM information_schema.statistics WHERE table_schema = DATABASE() AND table_name = 'f_ix' AND index_name = 'f_ix_vis'`); got != "YES" {
		t.Errorf("the explicitly visible index is %q", got)
	}
	// A FULLTEXT parser: ngram ships with MySQL, so this needs no plugin.
	runDDL(t, db, mysql.CreateIndex(mysql.Index{Name: "f_ix_ft"}, tbl).Fulltext().On(mysql.IndexKey(body)).
		With(mysql.IndexWithParser("ngram")))
	if got := myScalar(t, db, `SELECT count(*) FROM information_schema.statistics WHERE table_schema = DATABASE() AND table_name = 'f_ix' AND index_name = 'f_ix_ft'`); got != "1" {
		t.Errorf("the parsed FULLTEXT index is %q", got)
	}
	runDDL(t, db, mysql.DropIndex(mysql.Index{Name: "f_ix_body"}, tbl).Algorithm(mysql.AlgorithmInplace).Lock(mysql.LockNone))
	if got := myScalar(t, db, `SELECT count(*) FROM information_schema.statistics WHERE table_schema = DATABASE() AND table_name = 'f_ix' AND index_name = 'f_ix_body'`); got != "0" {
		t.Errorf("the index survived the drop: %q", got)
	}
	// A SPATIAL index, over the NOT NULL geometry column MySQL requires.
	cleanup(t, db, "DROP TABLE IF EXISTS f_geo")
	geo := sqlb.Table{Name: "f_geo", Alias: "g"}
	geoID := sqlb.Col[int64](geo, "id")
	geoAt := sqlb.Col[[]byte](geo, "at")
	runDDL(t, db, mysql.CreateTable(geo).Column(ddl.Column(geoID), ddl.Column(geoAt).As(ddl.RawType[[]byte]("POINT"))).
		Clause(mysql.PrimaryKey(geoID), mysql.TableSpatial(mysql.Index{Name: "f_geo_at"}, geoAt)))
	runDDL(t, db, mysql.CreateIndex(mysql.Index{Name: "f_geo_at2"}, geo).Spatial().On(mysql.IndexKey(geoAt)))
	if got := myScalar(t, db, `SELECT count(*) FROM information_schema.statistics WHERE table_schema = DATABASE() AND table_name = 'f_geo' AND index_type = 'SPATIAL'`); got != "2" {
		t.Errorf("spatial indexes: %q", got)
	}
}

// TestDDL_MySQLColumnFormsAndSwap covers the column forms and the atomic
// rename. The swap is the case that cannot be done any other way: two
// statements leave an instant in which the live name does not exist.
func TestDDL_MySQLColumnFormsAndSwap(t *testing.T) {
	db := openOrSkip(t)
	cleanup(t, db, "DROP TABLE IF EXISTS f_col", "DROP TABLE IF EXISTS f_old", "DROP TABLE IF EXISTS f_next")
	tbl := sqlb.Table{Name: "f_col", Alias: "c"}
	id := sqlb.Col[int64](tbl, "id")
	n := sqlb.Col[int64](tbl, "n")
	secret := sqlb.Col[string](tbl, "secret")
	renamed := sqlb.Col[int64](tbl, "renamed")
	added := sqlb.Col[int64](tbl, "added")
	runDDL(t, db, mysql.CreateTable(tbl).Column(ddl.Column(id), ddl.Column(n), ddl.Column(secret)).
		Clause(mysql.PrimaryKey(id), mysql.ColumnInvisible(secret)))
	if _, err := db.Exec(`INSERT INTO f_col (id, n, secret) VALUES (1, 5, 'x')`); err != nil {
		t.Fatal(err)
	}
	// An invisible column is absent from SELECT * and present when named.
	var cols int
	if err := db.QueryRow(`SELECT count(*) FROM (SELECT * FROM f_col) q`).Scan(&cols); err != nil {
		t.Fatal(err)
	}
	if got := myScalar(t, db, `SELECT group_concat(column_name ORDER BY ordinal_position) FROM information_schema.columns WHERE table_schema = DATABASE() AND table_name = 'f_col' AND extra = 'INVISIBLE'`); got != "secret" {
		t.Errorf("the invisible column is %q", got)
	}
	if got := myScalar(t, db, `SELECT secret FROM f_col`); got != "x" {
		t.Errorf("naming the invisible column gave %q", got)
	}
	runDDL(t, db, mysql.AlterTable(tbl).Clause(mysql.SetColumnVisible(secret)))
	if got := myScalar(t, db, `SELECT count(*) FROM information_schema.columns WHERE table_schema = DATABASE() AND table_name = 'f_col' AND extra = 'INVISIBLE'`); got != "0" {
		t.Errorf("SET VISIBLE left %q invisible columns", got)
	}
	runDDL(t, db, mysql.AlterTable(tbl).Clause(mysql.SetColumnInvisible(secret)))
	if got := myScalar(t, db, `SELECT count(*) FROM information_schema.columns WHERE table_schema = DATABASE() AND table_name = 'f_col' AND extra = 'INVISIBLE'`); got != "1" {
		t.Errorf("SET INVISIBLE left %q invisible columns", got)
	}
	// Placement: MySQL keeps the column order and reports it.
	runDDL(t, db, mysql.AlterTable(tbl).Clause(mysql.AddColumn(ddl.Column(added).Default(0)).At(mysql.First())))
	if got := myScalar(t, db, `SELECT column_name FROM information_schema.columns WHERE table_schema = DATABASE() AND table_name = 'f_col' AND ordinal_position = 1`); got != "added" {
		t.Errorf("FIRST put %q in position 1", got)
	}
	runDDL(t, db, mysql.AlterTable(tbl).Clause(mysql.ModifyColumnAt(ddl.Column(added).Default(0), mysql.After(id))))
	if got := myScalar(t, db, `SELECT column_name FROM information_schema.columns WHERE table_schema = DATABASE() AND table_name = 'f_col' AND ordinal_position = 2`); got != "added" {
		t.Errorf("AFTER id put %q in position 2", got)
	}
	// CHANGE COLUMN renames and redefines at once, which nothing else does.
	runDDL(t, db, mysql.AlterTable(tbl).Clause(mysql.ChangeColumn(n, ddl.Column(renamed).As(ddl.RawType[int64]("INT")))))
	if got := myScalar(t, db, `SELECT concat(column_name, ' ', column_type) FROM information_schema.columns WHERE table_schema = DATABASE() AND table_name = 'f_col' AND column_name = 'renamed'`); got != "renamed int" {
		t.Errorf("CHANGE COLUMN gave %q", got)
	}
	// DROP PRIMARY KEY, which has no name to give DropKey.
	runDDL(t, db, mysql.AlterTable(tbl).Clause(mysql.DropPrimaryKey()))
	if got := myScalar(t, db, `SELECT count(*) FROM information_schema.table_constraints WHERE table_schema = DATABASE() AND table_name = 'f_col' AND constraint_type = 'PRIMARY KEY'`); got != "0" {
		t.Errorf("the primary key survived: %q", got)
	}
	// A CHECK added unenforced accepts the rows already there, and the
	// generic DROP CONSTRAINT removes it without naming its kind.
	con := mysql.TableConstraint{Table: tbl, Name: "f_col_positive"}
	runDDL(t, db, mysql.AlterTable(tbl).Clause(mysql.AddConstraint(mysql.Check(con, sqlb.Greater(renamed, int64(1000))).NotEnforced())))
	if got := myScalar(t, db, `SELECT enforced FROM information_schema.table_constraints WHERE table_schema = DATABASE() AND constraint_name = 'f_col_positive'`); got != "NO" {
		t.Errorf("the check was added %q", got)
	}
	runDDL(t, db, mysql.AlterTable(tbl).Clause(mysql.DropConstraint(con)))
	if got := myScalar(t, db, `SELECT count(*) FROM information_schema.table_constraints WHERE table_schema = DATABASE() AND constraint_name = 'f_col_positive'`); got != "0" {
		t.Errorf("DROP CONSTRAINT left %q", got)
	}

	// The atomic swap. Both names exist throughout, which is the property two
	// statements cannot give.
	live := sqlb.Table{Name: "f_col"}
	old := sqlb.Table{Name: "f_old"}
	next := sqlb.Table{Name: "f_next"}
	nextID := sqlb.Col[int64](next, "id")
	runDDL(t, db, mysql.CreateTable(next).Column(ddl.Column(nextID)).Clause(mysql.PrimaryKey(nextID)))
	if _, err := db.Exec(`INSERT INTO f_next VALUES (99)`); err != nil {
		t.Fatal(err)
	}
	runDDL(t, db, mysql.RenameTable(live, old).And(next, live))
	if got := myScalar(t, db, `SELECT id FROM f_col`); got != "99" {
		t.Errorf("after the swap f_col holds %q", got)
	}
	if got := myScalar(t, db, `SELECT count(*) FROM information_schema.tables WHERE table_schema = DATABASE() AND table_name IN ('f_col', 'f_old')`); got != "2" {
		t.Errorf("both names must exist after the swap: %q", got)
	}
}

// TestDDL_MySQLViewHeadAndTriggerOrder covers the view's algorithm, security
// and column list, and the trigger order. The order is the half that cannot
// be read from a catalogue alone: two triggers of one timing and event are
// fired in an order, and only firing them shows it.
func TestDDL_MySQLViewHeadAndTriggerOrder(t *testing.T) {
	db := openOrSkip(t)
	cleanup(t, db, "DROP VIEW IF EXISTS f_view", "DROP TABLE IF EXISTS f_vt", "DROP TABLE IF EXISTS f_log")
	tbl := sqlb.Table{Name: "f_vt", Alias: "v"}
	n := sqlb.Col[int64](tbl, "n")
	note := sqlb.Col[string](tbl, "note")
	runDDL(t, db, mysql.CreateTable(tbl).Column(ddl.Column(n), ddl.Column(note).Default("")))
	if _, err := db.Exec(`INSERT INTO f_vt (n) VALUES (4)`); err != nil {
		t.Fatal(err)
	}
	view := mysql.View{Name: "f_view"}
	runDDL(t, db, mysql.CreateView(view).Algorithm(mysql.ViewMerge).Security(mysql.ViewInvoker).
		Columns("value").As(mysql.Select(n).From(tbl)))
	if got := myScalar(t, db, `SELECT security_type FROM information_schema.views WHERE table_schema = DATABASE() AND table_name = 'f_view'`); got != "INVOKER" {
		t.Errorf("the view's security is %q", got)
	}
	if got := showCreateView(t, db, "f_view"); !strings.Contains(got, "ALGORITHM=MERGE") {
		t.Errorf("the view's algorithm is not MERGE: %s", got)
	}
	if got := myScalar(t, db, `SELECT column_name FROM information_schema.columns WHERE table_schema = DATABASE() AND table_name = 'f_view'`); got != "value" {
		t.Errorf("the view's column is %q", got)
	}
	if got := myScalar(t, db, `SELECT value FROM f_view`); got != "4" {
		t.Errorf("the renamed column reads %q", got)
	}
	runDDL(t, db, mysql.AlterView(view).Algorithm(mysql.ViewTemptable).Security(mysql.ViewDefiner).
		Columns("amount").As(mysql.Select(n).From(tbl)))
	if got := myScalar(t, db, `SELECT security_type FROM information_schema.views WHERE table_schema = DATABASE() AND table_name = 'f_view'`); got != "DEFINER" {
		t.Errorf("the altered view's security is %q", got)
	}
	if got := showCreateView(t, db, "f_view"); !strings.Contains(got, "ALGORITHM=TEMPTABLE") {
		t.Errorf("the altered view's algorithm is not TEMPTABLE: %s", got)
	}
	if got := myScalar(t, db, `SELECT amount FROM f_view`); got != "4" {
		t.Errorf("the renamed column reads %q", got)
	}

	// Two BEFORE INSERT triggers, the second created later and made to run
	// first. Without PRECEDES the order would be the order of creation and
	// the note would read the other way round.
	runDDL(t, db, mysql.CreateTrigger(mysql.Trigger{Name: "f_vt_b1"}).Before(mysql.OnInsert, tbl).
		Do(mysql.StepSetNew(note, sqlb.Concat(mysql.NewRow(note), sqlb.Param("one")))))
	runDDL(t, db, mysql.CreateTrigger(mysql.Trigger{Name: "f_vt_b0"}).Before(mysql.OnInsert, tbl).
		Precedes(mysql.Trigger{Name: "f_vt_b1"}).
		Do(mysql.StepSetNew(note, sqlb.Concat(mysql.NewRow(note), sqlb.Param("zero")))))
	if _, err := db.Exec(`INSERT INTO f_vt (n) VALUES (7)`); err != nil {
		t.Fatal(err)
	}
	if got := myScalar(t, db, `SELECT note FROM f_vt WHERE n = 7`); got != "zerone" && got != "zeroone" {
		t.Errorf("the trigger order gave %q, wanted the PRECEDES trigger to run first", got)
	}
	runDDL(t, db, mysql.CreateTrigger(mysql.Trigger{Name: "f_vt_b2"}).Before(mysql.OnInsert, tbl).
		Follows(mysql.Trigger{Name: "f_vt_b1"}).Do(mysql.StepRaw("SET NEW.note = concat(NEW.note, 'two')")))
	if _, err := db.Exec(`INSERT INTO f_vt (n) VALUES (8)`); err != nil {
		t.Fatal(err)
	}
	if got := myScalar(t, db, `SELECT note FROM f_vt WHERE n = 8`); !strings.HasSuffix(got, "two") {
		t.Errorf("FOLLOWS did not put the third trigger last: %q", got)
	}
}

// TestDDL_MySQLRoutineCommentAndRelativeEvent covers the two forms the first
// pass missed inside statements it had built: a routine's COMMENT, which is a
// characteristic carrying a value, and an event scheduled relative to the
// server's clock, which needs no time zone.
func TestDDL_MySQLRoutineCommentAndRelativeEvent(t *testing.T) {
	db := openOrSkip(t)
	cleanup(t, db, "DROP PROCEDURE IF EXISTS f_proc", "DROP EVENT IF EXISTS f_ev", "DROP TABLE IF EXISTS f_evt")
	pr := mysql.Procedure{Name: "f_proc"}
	runDDL(t, db, mysql.CreateProcedure(pr).Characteristic(mysql.NoSQL).Comment("the original").Body("BEGIN END"))
	if got := myScalar(t, db, `SELECT routine_comment FROM information_schema.routines WHERE routine_schema = DATABASE() AND routine_name = 'f_proc'`); got != "the original" {
		t.Errorf("the routine's comment is %q", got)
	}
	runDDL(t, db, mysql.AlterProcedure(pr).Comment("the replacement"))
	if got := myScalar(t, db, `SELECT routine_comment FROM information_schema.routines WHERE routine_schema = DATABASE() AND routine_name = 'f_proc'`); got != "the replacement" {
		t.Errorf("the altered comment is %q", got)
	}
	runDDL(t, db, mysql.AlterProcedure(pr).Characteristic(mysql.ReadsSQLData).Comment("both at once"))
	if got := myScalar(t, db, `SELECT concat(sql_data_access, ' ', routine_comment) FROM information_schema.routines WHERE routine_schema = DATABASE() AND routine_name = 'f_proc'`); got != "READS SQL DATA both at once" {
		t.Errorf("the combined ALTER gave %q", got)
	}
	runDDL(t, db, mysql.DropProcedure(pr).IfExists())

	// A relative schedule: MySQL resolves CURRENT_TIMESTAMP itself, so the
	// event's recorded execute_at is in the future whatever the session's
	// time zone is, which is the property EventAt cannot offer.
	evt := sqlb.Table{Name: "f_evt", Alias: "e"}
	evtN := sqlb.Col[int64](evt, "n")
	runDDL(t, db, mysql.CreateTable(evt).Column(ddl.Column(evtN)))
	runDDL(t, db, mysql.CreateEvent(mysql.Event{Name: "f_ev"}).On(mysql.EventAfter(1, mysql.UnitHour)).
		OnCompletion(mysql.Preserve).Status(mysql.EventDisable).
		Do(mysql.StepInsert(mysql.Insert(evt).Row(sqlb.Set(evtN, int64(1))))))
	if got := myScalar(t, db, `SELECT concat(event_type, ' ', CASE WHEN execute_at > now() THEN 'future' ELSE 'past' END) FROM information_schema.events WHERE event_schema = DATABASE() AND event_name = 'f_ev'`); got != "ONE TIME future" {
		t.Errorf("the relative schedule recorded %q", got)
	}
	runDDL(t, db, mysql.DropEvent(mysql.Event{Name: "f_ev"}).IfExists())
}

// showCreateView returns a view's definition text. information_schema.views
// has no algorithm column — it records the security type and not the
// algorithm — so SHOW CREATE VIEW is the only place the algorithm this module
// wrote can be read back.
func showCreateView(t *testing.T, db *sql.DB, name string) string {
	t.Helper()
	var view, create, client, collation string
	if err := db.QueryRowContext(context.Background(), "SHOW CREATE VIEW `"+name+"`").
		Scan(&view, &create, &client, &collation); err != nil {
		t.Fatalf("SHOW CREATE VIEW %s: %v", name, err)
	}
	return create
}

// TestDDL_MySQLStepBlockExecutes runs every shape StepBlock renders, because
// the separator rule is the construct and only the server can say it is
// right: MySQL 8.4 refuses a separator after the opener and requires one
// after every inner statement including the last, and a rendering test agrees
// with whichever rule it was written against.
//
// Each case fires the trigger and reads the effect back, and each asserts
// both halves — the guarded path taken and not taken — because a block whose
// condition never matches renders and executes exactly as well as one that
// works.
func TestDDL_MySQLStepBlockExecutes(t *testing.T) {
	db := openOrSkip(t)
	cleanup(t, db, "DROP TABLE IF EXISTS f_blk")
	tbl := sqlb.Table{Name: "f_blk", Alias: "k"}
	n := sqlb.Col[int64](tbl, "n")
	note := sqlb.Col[string](tbl, "note")
	tag := sqlb.Col[string](tbl, "tag")
	runDDL(t, db, mysql.CreateTable(tbl).
		Column(ddl.Column(n), ddl.Column(note).Default(""), ddl.Column(tag).Default("")))

	// One block with two typed inner statements, beside a step outside it.
	// The "n > 10" branch is taken for one row and not for another, so the
	// same trigger proves both directions.
	runDDL(t, db, mysql.CreateTrigger(mysql.Trigger{Name: "f_blk_bi"}).Before(mysql.OnInsert, tbl).Do(
		mysql.StepBlock("IF NEW.n > 10 THEN", "END IF",
			mysql.StepSetNew(note, sqlb.Param("big")),
			mysql.StepSetNew(tag, sqlb.Param("hot"))),
		mysql.StepBlock("IF NEW.n <= 10 THEN", "END IF",
			mysql.StepSetNew(note, sqlb.Param("small"))),
		mysql.StepRaw("SET NEW.n = NEW.n + 1")))
	for _, in := range []int64{11, 2} {
		if _, err := db.Exec(`INSERT INTO f_blk (n) VALUES (?)`, in); err != nil {
			t.Fatalf("insert %d: %v", in, err)
		}
	}
	if got := myScalar(t, db, `SELECT concat(n, ':', note, ':', tag) FROM f_blk WHERE n = 12`); got != "12:big:hot" {
		t.Errorf("the taken branch wrote %q; both inner statements and the step after the block must have run", got)
	}
	if got := myScalar(t, db, `SELECT concat(n, ':', note, ':', tag) FROM f_blk WHERE n = 3`); got != "3:small:" {
		t.Errorf("the untaken branch wrote %q; the first block must not have run and the step after it must have", got)
	}

	// A nested block, which works only because an inner step may itself be a
	// block and each level terminates its own statements.
	cleanup(t, db, "DROP TABLE IF EXISTS f_blk2")
	tbl2 := sqlb.Table{Name: "f_blk2", Alias: "k2"}
	n2 := sqlb.Col[int64](tbl2, "n")
	note2 := sqlb.Col[string](tbl2, "note")
	runDDL(t, db, mysql.CreateTable(tbl2).Column(ddl.Column(n2), ddl.Column(note2).Default("")))
	runDDL(t, db, mysql.CreateTrigger(mysql.Trigger{Name: "f_blk2_bi"}).Before(mysql.OnInsert, tbl2).Do(
		mysql.StepBlock("IF NEW.n > 0 THEN", "END IF",
			mysql.StepBlock("IF NEW.n > 100 THEN", "END IF",
				mysql.StepSetNew(note2, sqlb.Param("huge"))))))
	for _, in := range []int64{500, 5, -1} {
		if _, err := db.Exec(`INSERT INTO f_blk2 (n) VALUES (?)`, in); err != nil {
			t.Fatalf("insert %d: %v", in, err)
		}
	}
	for _, tc := range []struct {
		n    int64
		want string
	}{{500, "huge"}, {5, ""}, {-1, ""}} {
		var got string
		if err := db.QueryRow(`SELECT note FROM f_blk2 WHERE n = ?`, tc.n).Scan(&got); err != nil {
			t.Fatal(err)
		}
		if got != tc.want {
			t.Errorf("n = %d gave note %q, want %q", tc.n, got, tc.want)
		}
	}

	// And the rejection path: a SIGNAL inside a block aborts the write and
	// its message reaches the client, while a row the guard lets through is
	// still written.
	cleanup(t, db, "DROP TABLE IF EXISTS f_blk3")
	tbl3 := sqlb.Table{Name: "f_blk3", Alias: "k3"}
	n3 := sqlb.Col[int64](tbl3, "n")
	runDDL(t, db, mysql.CreateTable(tbl3).Column(ddl.Column(n3)))
	runDDL(t, db, mysql.CreateTrigger(mysql.Trigger{Name: "f_blk3_bi"}).Before(mysql.OnInsert, tbl3).Do(
		mysql.StepBlock("IF NEW.n < 0 THEN", "END IF",
			mysql.StepSignal(mysql.UserSQLState, "n must not be negative"))))
	_, err := db.Exec(`INSERT INTO f_blk3 (n) VALUES (-1)`)
	if err == nil {
		t.Error("the guarded SIGNAL did not reject the row")
	} else if !strings.Contains(err.Error(), "n must not be negative") {
		t.Errorf("the message did not reach the client: %v", err)
	}
	if _, err := db.Exec(`INSERT INTO f_blk3 (n) VALUES (7)`); err != nil {
		t.Errorf("the guard refused a legal row: %v", err)
	}
	if got := myScalar(t, db, `SELECT count(*) FROM f_blk3`); got != "1" {
		t.Errorf("f_blk3 holds %q rows, want 1", got)
	}
}
