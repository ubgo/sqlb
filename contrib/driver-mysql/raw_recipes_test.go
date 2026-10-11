package drivermysql_test

import (
	"context"
	"database/sql"
	"encoding/json"
	"testing"

	"github.com/ubgo/sqlb"
	"github.com/ubgo/sqlb/ddl"
	"github.com/ubgo/sqlb/mysql"
	"github.com/ubgo/sqlb/schema"
)

// The raw recipes in docs/ddl/not-built.md, executed against a real MySQL
// 8.4 and read back. MySQL commits DDL implicitly, so each recipe drops what
// it made rather than rolling back.
//
// The MySQL detector has had no partial or left-out entry since 2026-09-24,
// so the recipes that stood in for an unbuilt statement are gone and their
// constructs are executed as built statements in ddlgaps_test.go. What
// remains here is the two caveats the page still records, which are
// properties of the engine rather than gaps in this package: MySQL refuses a
// backslash in a literal, and it has no CREATE INDEX IF NOT EXISTS, so the
// guard is a runner contract.

// rawScript is the statement-level escape hatch on MySQL.
func rawScript(sql string) ddl.Script { return ddl.Script{{SQL: sql}} }

func execScript(t *testing.T, db *sql.DB, s ddl.Script) {
	t.Helper()
	for _, st := range s {
		if _, err := db.ExecContext(context.Background(), st.SQL); err != nil {
			t.Fatalf("%s: %v", st.SQL, err)
		}
	}
}

func myScalar(t *testing.T, db *sql.DB, q string) string {
	t.Helper()
	var s sql.NullString
	if err := db.QueryRowContext(context.Background(), q).Scan(&s); err != nil {
		t.Fatalf("%s: %v", q, err)
	}
	return s.String
}

var (
	rrOrders = sqlb.Table{Name: "rr_orders", Alias: "o"}
	rrOID    = sqlb.Col[int64](rrOrders, "id")
	rrOYear  = sqlb.Col[int64](rrOrders, "year")
	rrOTotal = sqlb.Col[int64](rrOrders, "total")
)

func TestRawRecipes_MySQL(t *testing.T) {
	db := openOrSkip(t)
	d := mysql.Dialect{}

	t.Run("CREATE_INDEX_if_absent", func(t *testing.T) {
		cleanup(t, db, "DROP TABLE IF EXISTS rr_orders")
		runDDL(t, db, mysql.CreateTable(rrOrders).IfNotExists().
			Column(ddl.Column(rrOID), ddl.Column(rrOYear), ddl.Column(rrOTotal)).Clause(mysql.PrimaryKey(rrOID)))
		// MySQL has no CREATE INDEX IF NOT EXISTS, so a repeatable install
		// asks the catalogue first and runs the built statement only on a miss.
		ix := mysql.Index{Name: "rr_orders_year"}
		for range 2 {
			var n int
			if err := db.QueryRow(`SELECT count(*) FROM information_schema.statistics WHERE table_schema = DATABASE() AND table_name = ? AND index_name = ?`, rrOrders.Name, ix.Name).Scan(&n); err != nil {
				t.Fatal(err)
			}
			if n == 0 {
				runDDL(t, db, mysql.CreateIndex(ix, rrOrders).On(mysql.IndexKey(rrOYear)))
			}
		}
		if got := myScalar(t, db, `SELECT count(DISTINCT index_name) FROM information_schema.statistics WHERE table_schema = DATABASE() AND table_name = 'rr_orders'`); got != "2" {
			t.Errorf("indexes %q", got)
		}
	})

	t.Run("literal", func(t *testing.T) {
		// The dialect refuses a backslash, because MySQL reads it as an
		// escape unless NO_BACKSLASH_ESCAPES is set; a value with one is bound
		// in a query, and in DDL it is written with the hex form by hand.
		if _, err := d.QuoteLiteral(`a\b`); err == nil {
			t.Fatal("MySQL literal quoting accepted a backslash")
		}
		cleanup(t, db, "DROP TABLE IF EXISTS rr_lit")
		execScript(t, db, rawScript("CREATE TABLE rr_lit (p VARCHAR(10) NOT NULL DEFAULT (CONVERT(0x615C62 USING utf8mb4)))"))
		if _, err := db.Exec(`INSERT INTO rr_lit () VALUES ()`); err != nil {
			t.Fatal(err)
		}
		if got := myScalar(t, db, `SELECT p FROM rr_lit`); got != `a\b` {
			t.Errorf("default %q", got)
		}
	})
}

// TestDDL_MySQLRenameIndexAndKey renames a plain index through RenameIndex
// and a unique key through RenameKey, and reads both names back.
func TestDDL_MySQLRenameIndexAndKey(t *testing.T) {
	db := openOrSkip(t)
	cleanup(t, db, "DROP TABLE IF EXISTS rr_ren")
	tb := sqlb.Table{Name: "rr_ren", Alias: "r"}
	a := sqlb.Col[int64](tb, "a")
	b := sqlb.Col[int64](tb, "b")
	key := mysql.TableConstraint{Table: tb, Name: "rr_ren_b"}
	runDDL(t, db, mysql.CreateTable(tb).Column(ddl.Column(a), ddl.Column(b)).
		Clause(mysql.TableIndex(mysql.Index{Name: "rr_ren_a"}, a), mysql.Unique(key, b)))
	runDDL(t, db, mysql.AlterTable(tb).Clause(
		mysql.RenameIndex(mysql.Index{Name: "rr_ren_a"}, mysql.Index{Name: "rr_ren_a2"}),
		mysql.RenameKey(key, mysql.TableConstraint{Table: tb, Name: "rr_ren_b2"})))
	if got := myScalar(t, db, `SELECT group_concat(DISTINCT index_name ORDER BY index_name) FROM information_schema.statistics WHERE table_schema = DATABASE() AND table_name = 'rr_ren'`); got != "rr_ren_a2,rr_ren_b2" {
		t.Errorf("indexes after rename %q", got)
	}
}

// TestDDL_MySQLJSONDefaultExecutes inserts a row that takes a JSON column's
// default, which MySQL accepts only in the expression form the builder
// writes, and reads it back as the same JSON value.
func TestDDL_MySQLJSONDefaultExecutes(t *testing.T) {
	db := openOrSkip(t)
	cleanup(t, db, "DROP TABLE IF EXISTS rr_jd")
	tb := sqlb.Table{Name: "rr_jd", Alias: "t"}
	id := sqlb.Col[int64](tb, "id")
	doc := sqlb.Col[json.RawMessage](tb, "doc")
	runDDL(t, db, mysql.CreateTable(tb).Column(ddl.Column(id),
		ddl.Column(doc).As(ddl.Portable[json.RawMessage](schema.TypeJSON)).Default(json.RawMessage(`{"a":[1,"it's"]}`))))
	if _, err := db.Exec(`INSERT INTO rr_jd (id) VALUES (1)`); err != nil {
		t.Fatal(err)
	}
	if got := myScalar(t, db, `SELECT doc = CAST('{"a":[1,"it''s"]}' AS JSON) FROM rr_jd`); got != "1" {
		t.Errorf("the default is not the JSON written: %q", got)
	}
}

// TestDDL_MySQLMultiStepTrigger fires a trigger whose body is two typed
// statements, written BEGIN ... END by the builder and sent whole with no
// DELIMITER, and reads back what both wrote.
func TestDDL_MySQLMultiStepTrigger(t *testing.T) {
	db := openOrSkip(t)
	cleanup(t, db, "DROP TABLE IF EXISTS mt_items", "DROP TABLE IF EXISTS mt_audit")
	items := sqlb.Table{Name: "mt_items", Alias: "i"}
	id := sqlb.Col[int64](items, "id")
	audit := sqlb.Table{Name: "mt_audit", Alias: "a"}
	aid := sqlb.Col[int64](audit, "item_id")
	kind := sqlb.Col[string](audit, "kind")
	runDDL(t, db, mysql.CreateTable(items).Column(ddl.Column(id)))
	runDDL(t, db, mysql.CreateTable(audit).Column(ddl.Column(aid), ddl.Column(kind)))
	gone := mysql.StepInsert(mysql.Insert(audit).Row(sqlb.SetExpr(aid, mysql.OldRow(id)), sqlb.Set(kind, "deleted")))
	again := mysql.StepInsert(mysql.Insert(audit).Row(sqlb.SetExpr(aid, mysql.OldRow(id)), sqlb.Set(kind, "noted")))
	runDDL(t, db, mysql.CreateTrigger(mysql.Trigger{Name: "mt_items_ad"}).After(mysql.OnDelete, items).Do(gone, again))
	if _, err := db.Exec(`INSERT INTO mt_items VALUES (7)`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`DELETE FROM mt_items WHERE id = 7`); err != nil {
		t.Fatal(err)
	}
	if got := myScalar(t, db, `SELECT group_concat(concat(item_id, ':', kind) ORDER BY kind) FROM mt_audit`); got != "7:deleted,7:noted" {
		t.Errorf("the trigger's two statements wrote %q", got)
	}
}
