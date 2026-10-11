package drivermysql_test

import (
	"context"
	"database/sql"
	"strings"
	"testing"
	"time"

	"github.com/ubgo/sqlb"
	"github.com/ubgo/sqlb/ddl"
	"github.com/ubgo/sqlb/mysql"
)

// The MySQL DDL the detector called partial or left out until 2026-09-24,
// executed. A rendering test proves the text and only execution proves the
// SQL, and this file is where the table options, the remaining ALTER TABLE
// actions, the partition maintenance actions, the procedural trigger steps,
// ALTER VIEW, the EVENT and the stored routines each meet a real MySQL 8.4.
//
// Several of these constructs report by returning rows rather than by
// failing, or fail in a way that is the proof: the maintenance actions return
// one row per partition, and IMPORT TABLESPACE fails for a missing file,
// which is what shows the server parsed it. Each case says which it is.

var (
	gOpts   = sqlb.Table{Name: "g_opts", Alias: "o"}
	gOptsID = sqlb.Col[int64](gOpts, "id")
	gOptsN  = sqlb.Col[string](gOpts, "name")
)

// queryRows runs a statement that reports by returning rows, as ANALYZE,
// CHECK, OPTIMIZE and REPAIR PARTITION do, and returns the Msg_type of every
// row. Executing one instead would report success whatever it found.
func queryRows(t *testing.T, db *sql.DB, b interface{ Build() (ddl.Script, error) }) []string {
	t.Helper()
	s, err := b.Build()
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, st := range s {
		rows, err := db.QueryContext(context.Background(), st.SQL)
		if err != nil {
			t.Fatalf("%s: %v", st.SQL, err)
		}
		cols, err := rows.Columns()
		if err != nil {
			t.Fatal(err)
		}
		for rows.Next() {
			cells := make([]sql.NullString, len(cols))
			ptrs := make([]any, len(cols))
			for i := range cells {
				ptrs[i] = &cells[i]
			}
			if err := rows.Scan(ptrs...); err != nil {
				t.Fatal(err)
			}
			for i, c := range cols {
				if strings.EqualFold(c, "Msg_type") {
					out = append(out, cells[i].String)
				}
			}
		}
		if err := rows.Err(); err != nil {
			t.Fatal(err)
		}
		if err := rows.Close(); err != nil {
			t.Fatal(err)
		}
	}
	return out
}

// buildErr is the error executing b produced, for a case whose proof is the
// server's refusal rather than its acceptance.
func buildErr(t *testing.T, db *sql.DB, b interface{ Build() (ddl.Script, error) }) error {
	t.Helper()
	s, err := b.Build()
	if err != nil {
		t.Fatal(err)
	}
	for _, st := range s {
		if _, err := db.ExecContext(context.Background(), st.SQL); err != nil {
			return err
		}
	}
	return nil
}

// TestDDL_MySQLTableOptions alters every table option CREATE TABLE can write
// and reads each back from information_schema, which is the only place the
// difference between "the server took it" and "the server ignored it" shows:
// an ENGINE it does not know is a warning and the default engine, not an
// error.
func TestDDL_MySQLTableOptions(t *testing.T) {
	db := openOrSkip(t)
	cleanup(t, db, "DROP TABLE IF EXISTS g_opts")
	runDDL(t, db, mysql.CreateTable(gOpts).Column(ddl.Column(gOptsID), ddl.Column(gOptsN)).
		Clause(mysql.AutoIncrement(gOptsID), mysql.PrimaryKey(gOptsID), mysql.TableEngine(mysql.EngineInnoDB),
			mysql.RowFormat(mysql.RowFormatCompact), mysql.TableComment("before")))
	if got := myScalar(t, db, `SELECT concat(engine, ' ', row_format, ' ', table_comment) FROM information_schema.tables WHERE table_schema = DATABASE() AND table_name = 'g_opts'`); got != "InnoDB Compact before" {
		t.Errorf("CREATE TABLE options: %q", got)
	}
	runDDL(t, db, mysql.AlterTable(gOpts).Clause(mysql.SetEngine(mysql.EngineInnoDB), mysql.SetRowFormat(mysql.RowFormatDynamic),
		mysql.SetTableComment("after"), mysql.SetAutoIncrement(500)))
	runDDL(t, db, mysql.AlterTable(gOpts).Clause(mysql.SetDefaultCharset("utf8mb4"), mysql.SetTableCollate("utf8mb4_bin")))
	if got := myScalar(t, db, `SELECT concat(row_format, ' ', table_comment, ' ', auto_increment, ' ', table_collation) FROM information_schema.tables WHERE table_schema = DATABASE() AND table_name = 'g_opts'`); got != "Dynamic after 500 utf8mb4_bin" {
		t.Errorf("ALTER TABLE options: %q", got)
	}
	// SetAutoIncrement reserves a range: the next row takes the value set.
	if _, err := db.Exec(`INSERT INTO g_opts (name) VALUES ('a')`); err != nil {
		t.Fatal(err)
	}
	if got := myScalar(t, db, `SELECT id FROM g_opts`); got != "500" {
		t.Errorf("AUTO_INCREMENT = 500 gave id %q", got)
	}
	// ConvertToCharset rewrites the columns, so the column's own character
	// set changes where SetDefaultCharset left it alone.
	runDDL(t, db, mysql.AlterTable(gOpts).Clause(mysql.ConvertToCharset("latin1")))
	if got := myScalar(t, db, `SELECT character_set_name FROM information_schema.columns WHERE table_schema = DATABASE() AND table_name = 'g_opts' AND column_name = 'name'`); got != "latin1" {
		t.Errorf("CONVERT TO CHARACTER SET left the column on %q", got)
	}
	// The escape hatch reaches an option nobody typed, and the server takes
	// it: STATS_PERSISTENT is one of the forty this package does not name.
	runDDL(t, db, mysql.AlterTable(gOpts).Clause(mysql.RawAlter("STATS_PERSISTENT = 1")))
	if got := myScalar(t, db, `SELECT create_options FROM information_schema.tables WHERE table_schema = DATABASE() AND table_name = 'g_opts'`); !strings.Contains(strings.ToLower(got), "stats_persistent=1") {
		t.Errorf("RawAlter option: %q", got)
	}
}

// TestDDL_MySQLAlterHow runs the ALGORITHM and LOCK clauses both ways, which
// is the point of naming them: an ALTER that cannot be done the way asked
// fails instead of quietly copying the table.
func TestDDL_MySQLAlterHow(t *testing.T) {
	db := openOrSkip(t)
	cleanup(t, db, "DROP TABLE IF EXISTS g_how")
	tbl := sqlb.Table{Name: "g_how", Alias: "h"}
	id := sqlb.Col[int64](tbl, "id")
	n := sqlb.Col[int64](tbl, "n")
	extra := sqlb.Col[int64](tbl, "extra")
	runDDL(t, db, mysql.CreateTable(tbl).Column(ddl.Column(id), ddl.Column(n)).Clause(mysql.PrimaryKey(id)))
	// Adding a column at the end is instant on 8.0 or later. MySQL refuses a
	// LOCK clause beside ALGORITHM = INSTANT, so it stands alone.
	runDDL(t, db, mysql.AlterTable(tbl).Action(ddl.AddColumn(ddl.Column(extra).Nullable())).Clause(mysql.Algorithm(mysql.AlgorithmInstant)))
	// Changing a column's type is not, so the same clause must fail rather
	// than fall back.
	err := buildErr(t, db, mysql.AlterTable(tbl).Clause(mysql.ModifyColumn(ddl.Column(n).As(ddl.RawType[int64]("VARCHAR(40)"))), mysql.Algorithm(mysql.AlgorithmInstant)))
	if err == nil {
		t.Error("ALGORITHM = INSTANT was accepted for a type change, so it is not being enforced")
	}
	// The copying algorithm takes the same change, with a lock it can honour.
	runDDL(t, db, mysql.AlterTable(tbl).Clause(mysql.ModifyColumn(ddl.Column(n).As(ddl.RawType[int64]("VARCHAR(40)"))),
		mysql.Algorithm(mysql.AlgorithmCopy), mysql.Lock(mysql.LockShared)))
	if got := myScalar(t, db, `SELECT column_type FROM information_schema.columns WHERE table_schema = DATABASE() AND table_name = 'g_how' AND column_name = 'n'`); got != "varchar(40)" {
		t.Errorf("the copying ALTER left n as %q", got)
	}
	// A COPY algorithm cannot allow concurrent writes, so LOCK = NONE beside
	// it is refused; without the clause MySQL would have taken the lock and
	// said nothing.
	if err := buildErr(t, db, mysql.AlterTable(tbl).Clause(mysql.ForceRebuild(), mysql.Algorithm(mysql.AlgorithmCopy), mysql.Lock(mysql.LockNone))); err == nil {
		t.Error("LOCK = NONE was accepted beside ALGORITHM = COPY")
	}
	// FORCE rebuilds with no other change, and DISABLE KEYS is accepted and
	// ignored by InnoDB, which is why it is documented as MyISAM's.
	runDDL(t, db, mysql.AlterTable(tbl).Clause(mysql.ForceRebuild()))
	runDDL(t, db, mysql.AlterTable(tbl).Clause(mysql.DisableKeys()))
	runDDL(t, db, mysql.AlterTable(tbl).Clause(mysql.EnableKeys()))
}

// TestDDL_MySQLCheckEnforcementAndIndexVisibility drives the two per-object
// toggles through both states and reads each back, because each one's whole
// value is that it is reversible: an unenforced CHECK still exists, and an
// invisible index is still maintained.
func TestDDL_MySQLCheckEnforcementAndIndexVisibility(t *testing.T) {
	db := openOrSkip(t)
	cleanup(t, db, "DROP TABLE IF EXISTS g_chk")
	tbl := sqlb.Table{Name: "g_chk", Alias: "c"}
	id := sqlb.Col[int64](tbl, "id")
	n := sqlb.Col[int64](tbl, "n")
	con := mysql.TableConstraint{Table: tbl, Name: "g_chk_positive"}
	ix := mysql.Index{Name: "g_chk_n"}
	runDDL(t, db, mysql.CreateTable(tbl).Column(ddl.Column(id), ddl.Column(n)).
		Clause(mysql.PrimaryKey(id), mysql.TableIndex(ix, n)).
		Constraint(ddl.Check(con, sqlb.Greater(n, int64(0)))))
	if !fails(db, `INSERT INTO g_chk VALUES (1, -1)`) {
		t.Error("the CHECK was not enforced to begin with")
	}
	runDDL(t, db, mysql.AlterTable(tbl).Clause(mysql.SetCheckNotEnforced(con)))
	if got := myScalar(t, db, `SELECT enforced FROM information_schema.table_constraints WHERE table_schema = DATABASE() AND constraint_name = 'g_chk_positive'`); got != "NO" {
		t.Errorf("enforced %q after NOT ENFORCED", got)
	}
	if _, err := db.Exec(`INSERT INTO g_chk VALUES (1, -1)`); err != nil {
		t.Fatalf("the unenforced CHECK still refused the row: %v", err)
	}
	// Re-enforcing validates the rows already there, so it must fail while
	// the violating row is present and succeed once it is gone.
	if err := buildErr(t, db, mysql.AlterTable(tbl).Clause(mysql.SetCheckEnforced(con))); err == nil {
		t.Error("ENFORCED was accepted with a violating row present")
	}
	if _, err := db.Exec(`DELETE FROM g_chk`); err != nil {
		t.Fatal(err)
	}
	runDDL(t, db, mysql.AlterTable(tbl).Clause(mysql.SetCheckEnforced(con)))
	if got := myScalar(t, db, `SELECT enforced FROM information_schema.table_constraints WHERE table_schema = DATABASE() AND constraint_name = 'g_chk_positive'`); got != "YES" {
		t.Errorf("enforced %q after ENFORCED", got)
	}
	runDDL(t, db, mysql.AlterTable(tbl).Clause(mysql.SetIndexInvisible(ix)))
	if got := myScalar(t, db, `SELECT is_visible FROM information_schema.statistics WHERE table_schema = DATABASE() AND table_name = 'g_chk' AND index_name = 'g_chk_n'`); got != "NO" {
		t.Errorf("is_visible %q after INVISIBLE", got)
	}
	runDDL(t, db, mysql.AlterTable(tbl).Clause(mysql.SetIndexVisible(ix)))
	if got := myScalar(t, db, `SELECT is_visible FROM information_schema.statistics WHERE table_schema = DATABASE() AND table_name = 'g_chk' AND index_name = 'g_chk_n'`); got != "YES" {
		t.Errorf("is_visible %q after VISIBLE", got)
	}
}

// TestDDL_MySQLValidationAndTablespace covers the two pairs whose proof is
// not a catalogue row. WITH and WITHOUT VALIDATION apply only to a change to
// a generated column an index covers, and MySQL accepts WITHOUT only for a
// VIRTUAL one; DISCARD TABLESPACE detaches the data file, and IMPORT
// TABLESPACE fails for the missing file, which is what shows the server
// parsed the statement rather than rejecting its syntax.
func TestDDL_MySQLValidationAndTablespace(t *testing.T) {
	db := openOrSkip(t)
	cleanup(t, db, "DROP TABLE IF EXISTS g_val", "DROP TABLE IF EXISTS g_ts")
	tbl := sqlb.Table{Name: "g_val", Alias: "v"}
	id := sqlb.Col[int64](tbl, "id")
	n := sqlb.Col[int64](tbl, "n")
	doubled := sqlb.Col[int64](tbl, "doubled")
	runDDL(t, db, mysql.CreateTable(tbl).Column(ddl.Column(id), ddl.Column(n), ddl.Column(doubled)).
		Clause(mysql.PrimaryKey(id), mysql.GeneratedVirtual(doubled, sqlb.Mul(n, sqlb.Param(int64(2)))),
			mysql.TableIndex(mysql.Index{Name: "g_val_doubled"}, doubled)))
	if _, err := db.Exec(`INSERT INTO g_val (id, n) VALUES (1, 5)`); err != nil {
		t.Fatal(err)
	}
	// Changing the expression of a virtual generated column an index covers
	// is exactly where the clause applies.
	runDDL(t, db, mysql.AlterTable(tbl).Clause(mysql.ModifyColumn(ddl.Column(doubled),
		mysql.GeneratedVirtual(doubled, sqlb.Mul(n, sqlb.Param(int64(3))))), mysql.WithoutValidation()))
	if got := myScalar(t, db, `SELECT doubled FROM g_val`); got != "15" {
		t.Errorf("the changed expression gives %q", got)
	}
	runDDL(t, db, mysql.AlterTable(tbl).Clause(mysql.ModifyColumn(ddl.Column(doubled),
		mysql.GeneratedVirtual(doubled, sqlb.Mul(n, sqlb.Param(int64(4))))), mysql.WithValidation()))
	if got := myScalar(t, db, `SELECT doubled FROM g_val`); got != "20" {
		t.Errorf("the validated change gives %q", got)
	}
	ts := sqlb.Table{Name: "g_ts", Alias: "t"}
	tsID := sqlb.Col[int64](ts, "id")
	runDDL(t, db, mysql.CreateTable(ts).Column(ddl.Column(tsID)).Clause(mysql.PrimaryKey(tsID)))
	runDDL(t, db, mysql.AlterTable(ts).Clause(mysql.DiscardTablespace()))
	if !fails(db, `SELECT * FROM g_ts`) {
		t.Error("the table was still readable after DISCARD TABLESPACE")
	}
	// The import has no .ibd file to find, so it fails; a syntax error would
	// say so instead, which is how this case distinguishes the two.
	err := buildErr(t, db, mysql.AlterTable(ts).Clause(mysql.ImportTablespace()))
	if err == nil {
		t.Error("IMPORT TABLESPACE succeeded with no file to import")
	} else if strings.Contains(err.Error(), "syntax") {
		t.Errorf("IMPORT TABLESPACE was a syntax error, not a missing file: %v", err)
	}
}

// TestDDL_MySQLPartitionMaintenance runs every maintenance action against a
// partitioned table. The four reporting ones are queried rather than
// executed, because each reports what it found in a result set and does not
// fail, so a runner that only executes them says success whatever they found.
func TestDDL_MySQLPartitionMaintenance(t *testing.T) {
	db := openOrSkip(t)
	cleanup(t, db, "DROP TABLE IF EXISTS g_part", "DROP TABLE IF EXISTS g_arch")
	tbl := sqlb.Table{Name: "g_part", Alias: "p"}
	id := sqlb.Col[int64](tbl, "id")
	year := sqlb.Col[int64](tbl, "year")
	arch := sqlb.Table{Name: "g_arch", Alias: "a"}
	aID := sqlb.Col[int64](arch, "id")
	aYear := sqlb.Col[int64](arch, "year")
	runDDL(t, db, mysql.CreateTable(tbl).Column(ddl.Column(id), ddl.Column(year)).
		Clause(mysql.PrimaryKey(id, year), mysql.PartitionByRange(mysql.PartitionOn(year)).
			With(mysql.LessThan("p2025", 2026), mysql.LessThanMax("pmax"))))
	if _, err := db.Exec(`INSERT INTO g_part VALUES (1, 2025), (2, 2030)`); err != nil {
		t.Fatal(err)
	}
	runDDL(t, db, mysql.AlterTable(tbl).Clause(mysql.RebuildPartitions("p2025")))
	runDDL(t, db, mysql.AlterTable(tbl).Clause(mysql.RebuildPartitions(mysql.AllPartitions)))
	for name, b := range map[string]interface{ Build() (ddl.Script, error) }{
		"analyze":  mysql.AlterTable(tbl).Clause(mysql.AnalyzePartitions(mysql.AllPartitions)),
		"check":    mysql.AlterTable(tbl).Clause(mysql.CheckPartitions("p2025", "pmax")),
		"optimize": mysql.AlterTable(tbl).Clause(mysql.OptimizePartitions(mysql.AllPartitions)),
		"repair":   mysql.AlterTable(tbl).Clause(mysql.RepairPartitions(mysql.AllPartitions)),
	} {
		msgs := queryRows(t, db, b)
		if len(msgs) == 0 {
			t.Errorf("%s returned no row, so nothing reported what it found", name)
		}
		for _, m := range msgs {
			if m == "Error" {
				t.Errorf("%s reported an error row on a healthy table", name)
			}
		}
	}
	// The constraint that shapes every partitioned schema on InnoDB, and the
	// reason the exchange table below carries no key of its own: a
	// partitioned table takes no foreign key in either direction.
	if !fails(db, `CREATE TABLE g_lines (order_id bigint, order_year bigint, FOREIGN KEY (order_id, order_year) REFERENCES g_part (id, year))`) {
		_, _ = db.Exec(`DROP TABLE g_lines`)
		t.Error("MySQL accepted a foreign key to a partitioned table")
	}

	// EXCHANGE PARTITION needs a table of the identical structure, without
	// the partitioning.
	runDDL(t, db, mysql.CreateTable(arch).Column(ddl.Column(aID), ddl.Column(aYear)).Clause(mysql.PrimaryKey(aID, aYear)))
	if _, err := db.Exec(`INSERT INTO g_arch VALUES (3, 2025)`); err != nil {
		t.Fatal(err)
	}
	runDDL(t, db, mysql.AlterTable(tbl).Clause(mysql.ExchangePartition("p2025", arch)))
	if got := myScalar(t, db, `SELECT id FROM g_part PARTITION (p2025)`); got != "3" {
		t.Errorf("the partition holds %q after the exchange", got)
	}
	if got := myScalar(t, db, `SELECT id FROM g_arch`); got != "1" {
		t.Errorf("the table holds %q after the exchange", got)
	}
	// Validation is what refuses a row that does not belong in the partition,
	// and skipping it is what lets the same exchange through.
	if _, err := db.Exec(`UPDATE g_arch SET year = 2030 WHERE id = 1`); err != nil {
		t.Fatal(err)
	}
	if err := buildErr(t, db, mysql.AlterTable(tbl).Clause(mysql.ExchangePartition("p2025", arch))); err == nil {
		t.Error("a validated exchange accepted a row outside the partition's bounds")
	}
	runDDL(t, db, mysql.AlterTable(tbl).Clause(mysql.ExchangePartition("p2025", arch).WithoutValidation()))
	// The row is now stored in a partition whose bounds exclude it, which is
	// exactly the cost the doc comment names: pruning never finds it again.
	if got := myScalar(t, db, `SELECT count(*) FROM g_part PARTITION (pmax) WHERE year = 2030`); got != "1" {
		t.Errorf("pmax holds %q rows for 2030; the unvalidated row is invisible to pruning, which is the point", got)
	}
}

// TestDDL_MySQLTriggerControlFlow fires a trigger whose body mixes the typed
// steps with the raw one: StepSetNew writes the row being inserted, StepRaw
// carries the IF, and a second trigger rejects a row with StepSignal.
func TestDDL_MySQLTriggerControlFlow(t *testing.T) {
	db := openOrSkip(t)
	cleanup(t, db, "DROP TABLE IF EXISTS g_trig")
	tbl := sqlb.Table{Name: "g_trig", Alias: "t"}
	n := sqlb.Col[int64](tbl, "n")
	doubled := sqlb.Col[int64](tbl, "doubled")
	note := sqlb.Col[string](tbl, "note")
	runDDL(t, db, mysql.CreateTable(tbl).Column(ddl.Column(n), ddl.Column(doubled).Default(0), ddl.Column(note).Default("")))
	runDDL(t, db, mysql.CreateTrigger(mysql.Trigger{Name: "g_trig_bi"}).Before(mysql.OnInsert, tbl).Do(
		mysql.StepSetNew(doubled, sqlb.Mul(mysql.NewRow(n), sqlb.Param(int64(2)))),
		mysql.StepRaw("IF NEW.n > 10 THEN SET NEW.note = 'big'; END IF"),
		mysql.StepRaw("IF NEW.n < 0 THEN SIGNAL SQLSTATE '45000' SET MESSAGE_TEXT = 'negative'; END IF")))
	if _, err := db.Exec(`INSERT INTO g_trig (n) VALUES (11)`); err != nil {
		t.Fatal(err)
	}
	if got := myScalar(t, db, `SELECT concat(doubled, ':', note) FROM g_trig`); got != "22:big" {
		t.Errorf("the trigger wrote %q", got)
	}
	if !fails(db, `INSERT INTO g_trig (n) VALUES (-1)`) {
		t.Error("the raw SIGNAL did not reject the row")
	}
	// A body of one typed StepSignal is the declarative form of the same
	// rejection, with no raw text at all.
	cleanup(t, db, "DROP TABLE IF EXISTS g_guard")
	guard := sqlb.Table{Name: "g_guard", Alias: "g"}
	gn := sqlb.Col[int64](guard, "n")
	runDDL(t, db, mysql.CreateTable(guard).Column(ddl.Column(gn)))
	runDDL(t, db, mysql.CreateTrigger(mysql.Trigger{Name: "g_guard_bi"}).IfNotExists().Before(mysql.OnInsert, guard).
		Do(mysql.StepSignal(mysql.UserSQLState, "g_guard is append-only through the API")))
	_, err := db.Exec(`INSERT INTO g_guard VALUES (1)`)
	if err == nil {
		t.Fatal("the StepSignal trigger accepted the row")
	}
	if !strings.Contains(err.Error(), "append-only") {
		t.Errorf("the message did not reach the client: %v", err)
	}
}

// TestDDL_MySQLAlterView redefines a view and shows the difference from
// CREATE OR REPLACE: ALTER VIEW fails on a view that is not there, where
// CREATE OR REPLACE would silently create one.
func TestDDL_MySQLAlterView(t *testing.T) {
	db := openOrSkip(t)
	cleanup(t, db, "DROP VIEW IF EXISTS g_view", "DROP VIEW IF EXISTS g_absent", "DROP TABLE IF EXISTS g_vt")
	tbl := sqlb.Table{Name: "g_vt", Alias: "v"}
	n := sqlb.Col[int64](tbl, "n")
	view := mysql.View{Name: "g_view"}
	runDDL(t, db, mysql.CreateTable(tbl).Column(ddl.Column(n)))
	if _, err := db.Exec(`INSERT INTO g_vt VALUES (7)`); err != nil {
		t.Fatal(err)
	}
	runDDL(t, db, mysql.CreateView(view).As(mysql.Select(n).From(tbl)))
	if got := myScalar(t, db, `SELECT n FROM g_view`); got != "7" {
		t.Errorf("the view reads %q", got)
	}
	runDDL(t, db, mysql.AlterView(view).As(mysql.Select(sqlb.Mul(n, sqlb.Param(int64(3)))).From(tbl)))
	if got := myScalar(t, db, `SELECT * FROM g_view`); got != "21" {
		t.Errorf("the altered view reads %q", got)
	}
	// The whole reason ALTER VIEW is worth having beside CREATE OR REPLACE.
	if err := buildErr(t, db, mysql.AlterView(mysql.View{Name: "g_absent"}).As(mysql.Select(n).From(tbl))); err == nil {
		_, _ = db.Exec(`DROP VIEW g_absent`)
		t.Error("ALTER VIEW created a view that did not exist")
	}
	// A check option on an updatable view refuses a write it would not show.
	cleanup(t, db, "DROP VIEW IF EXISTS g_small")
	small := mysql.View{Name: "g_small"}
	runDDL(t, db, mysql.CreateView(small).As(mysql.Select(n).From(tbl).Where(sqlb.Less(n, int64(10)))))
	runDDL(t, db, mysql.AlterView(small).As(mysql.Select(n).From(tbl).Where(sqlb.Less(n, int64(10)))).WithCheckOption(mysql.Cascaded))
	if !fails(db, `INSERT INTO g_small VALUES (99)`) {
		t.Error("the altered view's check option did not refuse a row it cannot show")
	}
}

// TestDDL_MySQLEvents creates, alters and drops an event, reads each part
// back from information_schema, and then has the scheduler actually run one,
// which is the only thing that proves the schedule rather than the text.
func TestDDL_MySQLEvents(t *testing.T) {
	db := openOrSkip(t)
	ctx := context.Background()
	cleanup(t, db, "DROP EVENT IF EXISTS g_tick", "DROP EVENT IF EXISTS g_tock", "DROP TABLE IF EXISTS g_ticks")
	ticks := sqlb.Table{Name: "g_ticks", Alias: "k"}
	tickN := sqlb.Col[int64](ticks, "n")
	runDDL(t, db, mysql.CreateTable(ticks).Column(ddl.Column(tickN)))
	ev := mysql.Event{Name: "g_tick"}
	runDDL(t, db, mysql.CreateEvent(ev).IfNotExists().On(mysql.EventEvery(1, mysql.UnitDay)).
		OnCompletion(mysql.Preserve).Status(mysql.EventDisable).Comment("nightly").
		Do(mysql.StepDelete(mysql.Delete(ticks).All())))
	if got := myScalar(t, db, `SELECT concat(interval_value, ' ', interval_field, ' ', status, ' ', on_completion, ' ', event_comment) FROM information_schema.events WHERE event_schema = DATABASE() AND event_name = 'g_tick'`); got != "1 DAY DISABLED PRESERVE nightly" {
		t.Errorf("CREATE EVENT recorded %q", got)
	}
	runDDL(t, db, mysql.AlterEvent(ev).On(mysql.EventEvery(2, mysql.UnitHour)).Comment("hourly").Rename(mysql.Event{Name: "g_tock"}))
	if got := myScalar(t, db, `SELECT concat(interval_value, ' ', interval_field, ' ', event_comment) FROM information_schema.events WHERE event_schema = DATABASE() AND event_name = 'g_tock'`); got != "2 HOUR hourly" {
		t.Errorf("ALTER EVENT recorded %q", got)
	}
	runDDL(t, db, mysql.DropEvent(mysql.Event{Name: "g_tock"}).IfExists())
	if got := myScalar(t, db, `SELECT count(*) FROM information_schema.events WHERE event_schema = DATABASE() AND event_name = 'g_tock'`); got != "0" {
		t.Errorf("the event survived the drop: %q", got)
	}

	// The scheduler is off by default, so an event created while it is off
	// never runs and a test that only reads the catalogue would not notice.
	if _, err := db.ExecContext(ctx, `SET GLOBAL event_scheduler = ON`); err != nil {
		t.Skipf("the account cannot turn the event scheduler on, so the run half did not execute: %v", err)
	}
	t.Cleanup(func() { _, _ = db.ExecContext(ctx, `SET GLOBAL event_scheduler = OFF`) })
	// A one-shot event a second from now, on a connection whose time zone is
	// UTC, because MySQL reads the schedule's timestamp in the session's zone
	// and the dialect writes the time as UTC text.
	conn, err := db.Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = conn.Close() }()
	if _, err := conn.ExecContext(ctx, `SET time_zone = '+00:00'`); err != nil {
		t.Fatal(err)
	}
	at := time.Now().UTC().Add(time.Second)
	s, err := mysql.CreateEvent(ev).On(mysql.EventAt(at)).OnCompletion(mysql.Preserve).
		Do(mysql.StepInsert(mysql.Insert(ticks).Row(sqlb.Set(tickN, int64(1))))).Build()
	if err != nil {
		t.Fatal(err)
	}
	for _, st := range s {
		if _, err := conn.ExecContext(ctx, st.SQL); err != nil {
			t.Fatalf("%s: %v", st.SQL, err)
		}
	}
	deadline := time.Now().Add(30 * time.Second)
	for myScalar(t, db, `SELECT count(*) FROM g_ticks`) != "1" {
		if time.Now().After(deadline) {
			t.Fatal("the scheduled event never ran, so the schedule this built is not one MySQL acts on")
		}
		time.Sleep(200 * time.Millisecond)
	}
	// ON COMPLETION PRESERVE is why the event is still there to disable.
	runDDL(t, db, mysql.AlterEvent(ev).Status(mysql.EventDisable))
	if got := myScalar(t, db, `SELECT status FROM information_schema.events WHERE event_schema = DATABASE() AND event_name = 'g_tick'`); got != "DISABLED" {
		t.Errorf("the preserved event is %q", got)
	}
}

// TestDDL_MySQLRoutines creates a function and a procedure, calls both, alters
// their characteristics and drops them. Calling them is the half that matters:
// a routine that renders and does not run is a string.
func TestDDL_MySQLRoutines(t *testing.T) {
	db := openOrSkip(t)
	ctx := context.Background()
	cleanup(t, db, "DROP FUNCTION IF EXISTS g_add", "DROP PROCEDURE IF EXISTS g_bump")
	fn := mysql.Function{Name: "g_add"}
	pr := mysql.Procedure{Name: "g_bump"}
	intType := ddl.RawType[int64]("BIGINT")
	// Deterministic is what lets MySQL create the function at all with binary
	// logging on and log_bin_trust_function_creators off.
	runDDL(t, db, mysql.CreateFunction(fn, intType).IfNotExists().
		Param(mysql.RoutineParam("a", intType), mysql.RoutineParam("b", intType)).
		Characteristic(mysql.Deterministic, mysql.NoSQL, mysql.SecurityInvoker).
		Body("RETURN a + b"))
	if got := myScalar(t, db, `SELECT g_add(2, 3)`); got != "5" {
		t.Errorf("g_add(2, 3) = %q", got)
	}
	runDDL(t, db, mysql.CreateProcedure(pr).
		Param(mysql.RoutineParam("n", intType).Mode(mysql.ModeInOut)).
		Characteristic(mysql.NoSQL).
		Body("BEGIN SET n = n * 2; END"))
	if _, err := db.ExecContext(ctx, `SET @v = 21`); err != nil {
		t.Fatal(err)
	}
	// A procedure's INOUT parameter needs one connection for the CALL and the
	// read, since a user variable is per session and the pool may not give
	// the same connection twice.
	conn, err := db.Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = conn.Close() }()
	if _, err := conn.ExecContext(ctx, `SET @v = 21`); err != nil {
		t.Fatal(err)
	}
	if _, err := conn.ExecContext(ctx, `CALL g_bump(@v)`); err != nil {
		t.Fatal(err)
	}
	var v int64
	if err := conn.QueryRowContext(ctx, `SELECT @v`).Scan(&v); err != nil || v != 42 {
		t.Errorf("the procedure left @v = %d, %v", v, err)
	}
	// ALTER changes what MySQL lets it change, and the catalogue shows it.
	runDDL(t, db, mysql.AlterFunction(fn).Characteristic(mysql.SecurityDefiner))
	runDDL(t, db, mysql.AlterProcedure(pr).Characteristic(mysql.ReadsSQLData, mysql.SecurityInvoker))
	if got := myScalar(t, db, `SELECT concat(routine_name, ' ', security_type) FROM information_schema.routines WHERE routine_schema = DATABASE() AND routine_name = 'g_add'`); got != "g_add DEFINER" {
		t.Errorf("the altered function is %q", got)
	}
	if got := myScalar(t, db, `SELECT concat(sql_data_access, ' ', security_type) FROM information_schema.routines WHERE routine_schema = DATABASE() AND routine_name = 'g_bump'`); got != "READS SQL DATA INVOKER" {
		t.Errorf("the altered procedure is %q", got)
	}
	// A mode on a function's parameter is a parse error on the server, so the
	// builder must refuse it here; this is the half that proves the refusal
	// is the server's rule and not an invention.
	if _, err := mysql.CreateFunction(fn, intType).Param(mysql.RoutineParam("a", intType).Mode(mysql.ModeIn)).
		Characteristic(mysql.NoSQL).Body("RETURN a").Build(); err == nil {
		t.Error("a function's parameter took a mode, which MySQL refuses to parse")
	}
	if fails(db, "CREATE FUNCTION g_modecheck (IN a BIGINT) RETURNS BIGINT NO SQL RETURN a") {
		t.Log("confirmed: MySQL refuses IN on a stored function's parameter")
	} else {
		_, _ = db.ExecContext(ctx, "DROP FUNCTION IF EXISTS g_modecheck")
		t.Error("MySQL accepted IN on a function parameter, so the builder's refusal is wrong")
	}
	runDDL(t, db, mysql.DropFunction(fn).IfExists())
	runDDL(t, db, mysql.DropProcedure(pr).IfExists())
	if got := myScalar(t, db, `SELECT count(*) FROM information_schema.routines WHERE routine_schema = DATABASE() AND routine_name IN ('g_add', 'g_bump')`); got != "0" {
		t.Errorf("%q routines survived the drops", got)
	}
}
