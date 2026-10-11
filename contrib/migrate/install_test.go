package migrate_test

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"testing"

	_ "modernc.org/sqlite"

	"github.com/ubgo/sqlb/contrib/migrate"
	"github.com/ubgo/sqlb/ddl"
	"github.com/ubgo/sqlb/expr"
	"github.com/ubgo/sqlb/schema"
	"github.com/ubgo/sqlb/sqlite"
)

// A library and an application, declaring their tables as the DDL each
// already writes and knowing nothing about each other.
var (
	iUsers   = expr.Table{Name: "i_users", Alias: "u"}
	iUserID  = expr.Col[int64](iUsers, "id")
	iEmail   = expr.Col[string](iUsers, "email")
	iOrders  = expr.Table{Name: "i_orders", Alias: "o"}
	iOrderID = expr.Col[int64](iOrders, "id")
	iOwner   = expr.Col[int64](iOrders, "user_id")
)

func library() migrate.Contribution {
	return migrate.Contribution{Owner: "ubgo/auth", Tables: []migrate.Table{
		sqlite.CreateTable(iUsers).IfNotExists().Column(ddl.Column(iUserID), ddl.Column(iEmail)).Clause(sqlite.PrimaryKey(iUserID)),
	}}
}

func application() migrate.Contribution {
	return migrate.Contribution{Owner: "the application", Tables: []migrate.Table{
		sqlite.CreateTable(iOrders).IfNotExists().Column(ddl.Column(iOrderID), ddl.Column(iOwner)).
			Clause(sqlite.PrimaryKey(iOrderID), sqlite.References(iOwner, iUserID).OnDelete(sqlite.RefCascade)),
	}}
}

// fileDB opens a SQLite file with a pool of several connections, which is what
// makes the connection-pinning of ExecuteDB observable: an in-memory database
// is a separate database on each connection.
func fileDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", filepath.Join(t.TempDir(), "i.db")+"?_pragma=foreign_keys(1)")
	if err != nil {
		t.Fatal(err)
	}
	db.SetMaxOpenConns(4)
	t.Cleanup(func() { _ = db.Close() })
	return db
}

// TestInstall_OrdersAcrossContributorsAndRuns is the converged path end to
// end: two packages' CREATE TABLE statements, contributed in the wrong order,
// are ordered by the foreign key Install reads from their own descriptions,
// executed twice, and the reference holds on a real database.
func TestInstall_OrdersAcrossContributorsAndRuns(t *testing.T) {
	ctx := context.Background()
	s, err := migrate.Install(application(), library())
	if err != nil {
		t.Fatal(err)
	}
	if len(s) != 2 || s[0].SQL[:40] != `CREATE TABLE IF NOT EXISTS "i_users" ("i` {
		t.Fatalf("users must come first: %v", s.SQL())
	}
	db := fileDB(t)
	for range 2 {
		if err := migrate.ExecuteDB(ctx, db, s); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO i_users VALUES (1, 'a'); INSERT INTO i_orders VALUES (1, 1)`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO i_orders VALUES (2, 99)`); err == nil {
		t.Error("the reference Install ordered for does not hold")
	}
}

// TestInstall_RefusesWhatAWholeDatabaseCannotHave pins the two refusals.
func TestInstall_RefusesWhatAWholeDatabaseCannotHave(t *testing.T) {
	if _, err := migrate.Install(library(), library()); !errors.Is(err, schema.ErrDuplicateTable) {
		t.Errorf("a table two contributors create: %v", err)
	}
	if _, err := migrate.Install(application()); !errors.Is(err, schema.ErrMissingTable) {
		t.Errorf("a reference to a table nobody creates: %v", err)
	}
}

// TestExecute_RunsANonTransactionalStatementAlone uses VACUUM, which SQLite
// refuses inside a transaction: the script succeeds only if Execute commits
// the statements before it, runs it on its own, and opens a new transaction
// for the ones after.
func TestExecute_RunsANonTransactionalStatementAlone(t *testing.T) {
	ctx := context.Background()
	db := fileDB(t)
	s := ddl.Script{
		{SQL: `CREATE TABLE v (n INTEGER)`, Transactional: true},
		{SQL: `INSERT INTO v VALUES (1)`, Transactional: true},
		{SQL: `VACUUM`, Transactional: false},
		{SQL: `INSERT INTO v VALUES (2)`, Transactional: true},
	}
	if err := migrate.ExecuteDB(ctx, db, s); err != nil {
		t.Fatal(err)
	}
	var n int
	if err := db.QueryRowContext(ctx, `SELECT count(*) FROM v`).Scan(&n); err != nil || n != 2 {
		t.Errorf("rows %d, %v", n, err)
	}
}

// TestExecute_RollsBackATransactionThatFails pins that a failure leaves none
// of the transaction's statements behind.
func TestExecute_RollsBackATransactionThatFails(t *testing.T) {
	ctx := context.Background()
	db := fileDB(t)
	err := migrate.ExecuteDB(ctx, db, ddl.Script{
		{SQL: `CREATE TABLE r (n INTEGER)`, Transactional: true},
		{SQL: `INSERT INTO nowhere VALUES (1)`, Transactional: true},
	})
	if err == nil {
		t.Fatal("a failing statement passed")
	}
	if _, err := db.ExecContext(ctx, `SELECT 1 FROM r`); err == nil {
		t.Error("the table from the failed transaction survived")
	}
}

// TestExecute_RunsTheSQLiteRebuildOnOneConnection runs a table rebuild on a
// pool of four connections. The rebuild turns foreign keys off, copies a table
// that another references, and turns them on; on a pool the PRAGMA and the
// copy could land on different connections, and the check at the end is
// queried rather than executed. A rebuild that leaves a dangling reference
// fails with ErrCheckFailed.
func TestExecute_RunsTheSQLiteRebuildOnOneConnection(t *testing.T) {
	ctx := context.Background()
	db := fileDB(t)
	install, err := migrate.Install(library(), application())
	if err != nil {
		t.Fatal(err)
	}
	if err := migrate.ExecuteDB(ctx, db, install); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO i_users VALUES (1, 'a'); INSERT INTO i_orders VALUES (1, 1)`); err != nil {
		t.Fatal(err)
	}
	next := expr.Table{Name: "i_users_new"}
	nid := expr.Col[int64](next, "id")
	nemail := expr.Col[string](next, "email")
	rebuild := func(copyID expr.Expr[int64]) ddl.Script {
		s, err := sqlite.Rebuild(iUsers).Into(sqlite.CreateTable(next).Column(ddl.Column(nid), ddl.Column(nemail)).Clause(sqlite.PrimaryKey(nid))).
			Copy(sqlite.CopyAs(nid, copyID), sqlite.CopyAs(nemail, iEmail)).Build()
		if err != nil {
			t.Fatal(err)
		}
		return s
	}
	if err := migrate.ExecuteDB(ctx, db, rebuild(iUserID)); err != nil {
		t.Fatalf("a clean rebuild failed: %v", err)
	}
	var n int
	if err := db.QueryRowContext(ctx, `SELECT count(*) FROM i_orders JOIN i_users ON i_users.id = i_orders.user_id`).Scan(&n); err != nil || n != 1 {
		t.Errorf("the reference did not survive the rebuild: %d, %v", n, err)
	}
	// Renumbering the ids leaves the order pointing at a user who is gone.
	if err := migrate.ExecuteDB(ctx, db, rebuild(expr.Add(iUserID, expr.Literal[int64]("100")))); !errors.Is(err, migrate.ErrCheckFailed) {
		t.Errorf("a rebuild with a dangling reference: %v, want ErrCheckFailed", err)
	}
}
