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

// runDDL builds b and executes each statement. MySQL commits DDL implicitly,
// so these tests clean up by dropping rather than by rolling back.
func runDDL(t *testing.T, db *sql.DB, b interface{ Build() (ddl.Script, error) }) {
	t.Helper()
	s, err := b.Build()
	if err != nil {
		t.Fatal(err)
	}
	for _, st := range s {
		if _, err := db.ExecContext(context.Background(), st.SQL); err != nil {
			t.Fatalf("%s: %v", st.SQL, err)
		}
	}
}

func fails(db *sql.DB, q string) bool {
	_, err := db.ExecContext(context.Background(), q)
	return err != nil
}

var (
	dUsers   = sqlb.Table{Name: "d_users", Alias: "u"}
	dID      = sqlb.Col[int64](dUsers, "id")
	dEmail   = sqlb.Col[string](dUsers, "email")
	dLower   = sqlb.Col[string](dUsers, "email_lower")
	dStatus  = sqlb.Col[string](dUsers, "status")
	dBio     = sqlb.Col[*string](dUsers, "bio")
	dCreated = sqlb.Col[time.Time](dUsers, "created_at")

	dSess    = sqlb.Table{Name: "d_sessions", Alias: "s"}
	dSessID  = sqlb.Col[int64](dSess, "id")
	dSessUID = sqlb.Col[int64](dSess, "user_id")
)

func cleanup(t *testing.T, db *sql.DB, qs ...string) {
	t.Cleanup(func() {
		for _, q := range qs {
			_, _ = db.ExecContext(context.Background(), q)
		}
	})
}

// TestDDL_MySQLTablesAndKeysHold installs two tables twice from their Col
// declarations, and has MySQL enforce each clause: AUTO_INCREMENT fills the
// id, the case-insensitive default collation and a binary one differ on the
// unique email, the generated column computes, the CHECK and OneOf refuse,
// the foreign key cascades, and the table options are recorded.
func TestDDL_MySQLTablesAndKeysHold(t *testing.T) {
	db := openOrSkip(t)
	ctx := context.Background()
	cleanup(t, db, "DROP TABLE IF EXISTS d_sessions", "DROP TABLE IF EXISTS d_users")
	for range 2 {
		runDDL(t, db, mysql.CreateTable(dUsers).IfNotExists().
			Column(ddl.Column(dID), ddl.Column(dEmail), ddl.Column(dLower), ddl.Column(dStatus).Default("active"),
				ddl.Column(dBio).As(ddl.RawType[*string]("TEXT")), ddl.Column(dCreated).DefaultExpr(mysql.UTCNow())).
			Clause(mysql.AutoIncrement(dID), mysql.PrimaryKey(dID), mysql.ColumnCollate(dEmail, "utf8mb4_bin"),
				mysql.GeneratedStored(dLower, sqlb.Lower(dEmail)), mysql.Unique(mysql.TableConstraint{Table: dEmail.Table(), Name: "d_users_email"}, dEmail),
				mysql.TableFulltext(mysql.Index{Name: "d_users_bio"}, dBio), mysql.ColumnComment(dStatus, "lifecycle"),
				mysql.InnoDB(), mysql.DefaultCharset("utf8mb4"), mysql.TableComment("users")).
			Constraint(ddl.OneOf(ddl.TableConstraint{Table: dStatus.Table(), Name: "d_users_status"}, dStatus, "active", "banned"),
				ddl.Check(ddl.TableConstraint{Table: dEmail.Table(), Name: "d_users_email_at"}, mysql.RegexpLike(dEmail, "@"))))
		runDDL(t, db, mysql.CreateTable(dSess).IfNotExists().Column(ddl.Column(dSessID), ddl.Column(dSessUID)).
			Clause(mysql.PrimaryKey(dSessID), mysql.References(dSessUID, dID).OnDelete(mysql.RefCascade)))
	}
	if _, err := db.ExecContext(ctx, "INSERT INTO d_users (email) VALUES ('Ada@x.io')"); err != nil {
		t.Fatal(err)
	}
	var id int64
	var lower string
	var created time.Time
	if err := db.QueryRowContext(ctx, "SELECT id, email_lower, created_at FROM d_users").Scan(&id, &lower, &created); err != nil ||
		id != 1 || lower != "ada@x.io" || time.Since(created) > time.Minute {
		t.Errorf("id %d lower %q created %v, %v", id, lower, created, err)
	}
	if fails(db, "INSERT INTO d_users (email) VALUES ('ada@x.io')") {
		t.Error("utf8mb4_bin made the email case-sensitive, so a case variant must be accepted")
	}
	if !fails(db, "INSERT INTO d_users (email) VALUES ('Ada@x.io')") {
		t.Error("the unique email accepted an exact duplicate")
	}
	if !fails(db, "INSERT INTO d_users (email, status) VALUES ('c@x.io', 'gone')") {
		t.Error("OneOf did not reach the table")
	}
	if !fails(db, "INSERT INTO d_users (email) VALUES ('no-at-sign')") {
		t.Error("the CHECK did not reach the table")
	}
	if _, err := db.ExecContext(ctx, "INSERT INTO d_sessions VALUES (1, 1)"); err != nil {
		t.Fatal(err)
	}
	if !fails(db, "INSERT INTO d_sessions VALUES (2, 99)") {
		t.Error("the foreign key accepted a missing user")
	}
	if _, err := db.ExecContext(ctx, "DELETE FROM d_users WHERE id = 1"); err != nil {
		t.Fatal(err)
	}
	var n int
	if err := db.QueryRowContext(ctx, "SELECT count(*) FROM d_sessions").Scan(&n); err != nil || n != 0 {
		t.Errorf("ON DELETE CASCADE left %d, %v", n, err)
	}
	var engine, comment, colComment string
	if err := db.QueryRowContext(ctx, "SELECT engine, table_comment FROM information_schema.tables WHERE table_schema = DATABASE() AND table_name = 'd_users'").Scan(&engine, &comment); err != nil || engine != "InnoDB" || comment != "users" {
		t.Errorf("engine %q comment %q, %v", engine, comment, err)
	}
	if err := db.QueryRowContext(ctx, "SELECT column_comment FROM information_schema.columns WHERE table_schema = DATABASE() AND table_name = 'd_users' AND column_name = 'status'").Scan(&colComment); err != nil || colComment != "lifecycle" {
		t.Errorf("column comment %q, %v", colComment, err)
	}
}

// TestDDL_MySQLAltersIndexesViewsTriggers runs the ALTER actions, the index
// statements, a view and a trigger on MySQL and reads each back.
func TestDDL_MySQLAltersIndexesViewsTriggers(t *testing.T) {
	db := openOrSkip(t)
	ctx := context.Background()
	items := sqlb.Table{Name: "d_items", Alias: "i"}
	iid := sqlb.Col[int64](items, "id")
	name := sqlb.Col[string](items, "name")
	code := sqlb.Col[int64](items, "code")
	price := sqlb.Col[int64](items, "price")
	audit := sqlb.Table{Name: "d_audit", Alias: "a"}
	aItem := sqlb.Col[int64](audit, "item_id")
	aWhat := sqlb.Col[string](audit, "what")
	cleanup(t, db, "DROP VIEW IF EXISTS d_cheap", "DROP TABLE IF EXISTS d_audit", "DROP TABLE IF EXISTS d_products", "DROP TABLE IF EXISTS d_items")
	runDDL(t, db, mysql.CreateTable(items).Column(ddl.Column(iid), ddl.Column(name), ddl.Column(code)).Clause(mysql.PrimaryKey(iid)))
	runDDL(t, db, mysql.CreateTable(audit).Column(ddl.Column(aItem), ddl.Column(aWhat)))
	if _, err := db.ExecContext(ctx, "INSERT INTO d_items VALUES (1, 'Pen', 7), (2, 'Ink', 9)"); err != nil {
		t.Fatal(err)
	}
	s, err := mysql.AlterTable(items).Action(ddl.AddColumn(ddl.Column(price).Default(0))).
		Clause(mysql.SetDefault(price, int64(5)), mysql.ModifyColumn(ddl.Column(name).As(ddl.RawType[string]("VARCHAR(80)"))),
			mysql.AddConstraint(mysql.Unique(mysql.TableConstraint{Table: code.Table(), Name: "d_items_code"}, code)), mysql.AddConstraint(mysql.Check(mysql.TableConstraint{Table: items, Name: "d_items_price"}, sqlb.GreaterEq(price, int64(0))))).Build()
	if err != nil {
		t.Fatal(err)
	}
	if len(s) != 1 {
		t.Errorf("MySQL combines ALTER actions; got %d statements: %v", len(s), s.SQL())
	}
	// MySQL cannot refer, in one ALTER TABLE, to a column the same statement
	// adds ("Unknown column 'price'"), so adding it is a statement of its own.
	runDDL(t, db, mysql.AlterTable(items).Action(ddl.AddColumn(ddl.Column(price).Default(0))))
	runDDL(t, db, mysql.AlterTable(items).
		Clause(mysql.SetDefault(price, int64(5)), mysql.ModifyColumn(ddl.Column(name).As(ddl.RawType[string]("VARCHAR(80)"))),
			mysql.AddConstraint(mysql.Unique(mysql.TableConstraint{Table: code.Table(), Name: "d_items_code"}, code)), mysql.AddConstraint(mysql.Check(mysql.TableConstraint{Table: items, Name: "d_items_price"}, sqlb.GreaterEq(price, int64(0))))))
	var typ string
	var def sql.NullString
	if err := db.QueryRowContext(ctx, "SELECT column_type FROM information_schema.columns WHERE table_schema = DATABASE() AND table_name = 'd_items' AND column_name = 'name'").Scan(&typ); err != nil || typ != "varchar(80)" {
		t.Errorf("MODIFY COLUMN type %q, %v", typ, err)
	}
	if err := db.QueryRowContext(ctx, "SELECT column_default FROM information_schema.columns WHERE table_schema = DATABASE() AND table_name = 'd_items' AND column_name = 'price'").Scan(&def); err != nil || def.String != "5" {
		t.Errorf("SET DEFAULT %q, %v", def.String, err)
	}
	if !fails(db, "INSERT INTO d_items (id, name, code) VALUES (3, 'x', 7)") {
		t.Error("the added unique key accepted a duplicate code")
	}
	if !fails(db, "INSERT INTO d_items (id, name, code, price) VALUES (3, 'x', 1, -1)") {
		t.Error("the added CHECK accepted a negative price")
	}
	runDDL(t, db, mysql.AlterTable(items).Clause(mysql.DropCheck(mysql.TableConstraint{Table: items, Name: "d_items_price"}), mysql.RenameKey(mysql.TableConstraint{Table: items, Name: "d_items_code"}, mysql.TableConstraint{Table: items, Name: "d_items_by_code"}), mysql.DropDefault(price)))
	runDDL(t, db, mysql.AlterTable(items).Clause(mysql.DropKey(mysql.TableConstraint{Table: items, Name: "d_items_by_code"})))

	runDDL(t, db, mysql.CreateIndex(mysql.Index{Name: "d_items_name"}, items).Using(mysql.BTree).On(mysql.IndexPrefix(name, 10), mysql.IndexKey(code).Desc()))
	runDDL(t, db, mysql.CreateIndex(mysql.Index{Name: "d_items_lower"}, items).Unique().On(mysql.IndexExpr(sqlb.Lower(name))))
	if !fails(db, "INSERT INTO d_items (id, name, code) VALUES (3, 'PEN', 1)") {
		t.Error("the functional unique index on lower(name) accepted PEN beside Pen")
	}
	runDDL(t, db, mysql.CreateIndex(mysql.Index{Name: "d_items_ft"}, items).Fulltext().On(mysql.IndexKey(name)))
	var idx string
	if err := db.QueryRowContext(ctx, "SELECT group_concat(DISTINCT index_name ORDER BY index_name) FROM information_schema.statistics WHERE table_schema = DATABASE() AND table_name = 'd_items'").Scan(&idx); err != nil ||
		idx != "d_items_ft,d_items_lower,d_items_name,PRIMARY" {
		t.Errorf("indexes %q, %v", idx, err)
	}
	runDDL(t, db, mysql.DropIndex(mysql.Index{Name: "d_items_name"}, items))

	cheap := mysql.View{Name: "d_cheap"}
	for range 2 {
		runDDL(t, db, mysql.CreateView(cheap).OrReplace().As(mysql.Select(iid, name, code, price).From(items).Where(sqlb.Less(price, int64(10)))).WithCheckOption(mysql.Cascaded))
	}
	// The view carries every NOT NULL column, so the inserts can fail only on
	// the check option: one the view shows is accepted, one it hides refused.
	if fails(db, "INSERT INTO d_cheap (id, name, code, price) VALUES (8, 'a', 80, 5)") {
		t.Fatal("an insert the view shows was refused; the assertion below would prove nothing")
	}
	if !fails(db, "INSERT INTO d_cheap (id, name, code, price) VALUES (9, 'b', 90, 50)") {
		t.Error("WITH CHECK OPTION accepted a row the view does not show")
	}

	tr := mysql.Trigger{Name: "d_items_audit"}
	for range 2 {
		runDDL(t, db, mysql.CreateTrigger(tr).IfNotExists().After(mysql.OnUpdate, items).
			Do(mysql.StepInsert(mysql.Insert(audit).Row(sqlb.SetExpr(aItem, mysql.NewRow(iid)), sqlb.SetExpr(aWhat, mysql.OldRow(name))))))
	}
	if _, err := db.ExecContext(ctx, "UPDATE d_items SET name = 'Pencil' WHERE id = 1"); err != nil {
		t.Fatal(err)
	}
	var what string
	if err := db.QueryRowContext(ctx, "SELECT what FROM d_audit WHERE item_id = 1").Scan(&what); err != nil || what != "Pen" {
		t.Errorf("the trigger wrote %q, %v", what, err)
	}
	for range 2 {
		runDDL(t, db, mysql.DropTrigger(tr).IfExists())
		runDDL(t, db, mysql.DropView(cheap).IfExists())
	}
	products := sqlb.Table{Name: "d_products"}
	runDDL(t, db, mysql.RenameTable(items, products))
	runDDL(t, db, mysql.Truncate(products))
	if err := db.QueryRowContext(ctx, "SELECT count(*) FROM d_products").Scan(&n); err != nil || n != 0 {
		t.Errorf("TRUNCATE left %d, %v", n, err)
	}
	runDDL(t, db, mysql.AlterTable(products).Clause(mysql.RenameTo(items)))
	if !strings.Contains(fmtTables(t, db), "d_items") {
		t.Error("RENAME TO did not bring the name back")
	}
}

var n int

func fmtTables(t *testing.T, db *sql.DB) string {
	var s string
	if err := db.QueryRowContext(context.Background(), "SELECT group_concat(table_name) FROM information_schema.tables WHERE table_schema = DATABASE()").Scan(&s); err != nil {
		t.Fatal(err)
	}
	return s
}

// TestDDL_MySQLCommitsDDLImplicitly pins the engine fact migrate.Execute's
// documentation rests on: a DDL statement inside a transaction commits it
// (https://dev.mysql.com/doc/refman/8.4/en/implicit-commit.html), so a
// rollback after a failed CREATE TABLE leaves the earlier one behind. A
// script that fails halfway on MySQL is half applied, which is why every
// MySQL CREATE is written to run again.
func TestDDL_MySQLCommitsDDLImplicitly(t *testing.T) {
	db := openOrSkip(t)
	ctx := context.Background()
	cleanup(t, db, "DROP TABLE IF EXISTS d_implicit")
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := tx.ExecContext(ctx, "CREATE TABLE d_implicit (n INT)"); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.ExecContext(ctx, "INSERT INTO d_nowhere VALUES (1)"); err == nil {
		t.Fatal("the failing statement succeeded")
	}
	_ = tx.Rollback()
	if fails(db, "SELECT 1 FROM d_implicit") {
		t.Error("the rollback removed the table: MySQL no longer commits DDL implicitly, and migrate's documentation is wrong")
	}
}
