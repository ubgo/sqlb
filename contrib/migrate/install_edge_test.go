package migrate_test

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/ubgo/sqlb/contrib/migrate"
	"github.com/ubgo/sqlb/ddl"
	"github.com/ubgo/sqlb/expr"
	"github.com/ubgo/sqlb/pg"
	"github.com/ubgo/sqlb/schema"
	"github.com/ubgo/sqlb/sqlite"
)

// TestInstall_EdgeCases pins what Install does with the inputs a caller gets
// wrong: a nil table is an error naming its owner, no contributions are an
// empty script, a cycle is refused, and the same contributions in any order
// give byte-identical scripts, run after run.
func TestInstall_EdgeCases(t *testing.T) {
	if _, err := migrate.Install(migrate.Contribution{Owner: "lib", Tables: []migrate.Table{nil}}); !errors.Is(err, migrate.ErrNilTable) {
		t.Errorf("a nil table: %v", err)
	}
	var typedNil *sqlite.CreateTableReady
	if _, err := migrate.Install(migrate.Contribution{Owner: "lib", Tables: []migrate.Table{typedNil}}); !errors.Is(err, migrate.ErrNilTable) {
		t.Errorf("a typed nil table: %v", err)
	}
	if s, err := migrate.Install(); err != nil || len(s) != 0 {
		t.Errorf("no contributions: %v, %v", s, err)
	}

	a := expr.Table{Name: "c_a"}
	aID, aB := expr.Col[int64](a, "id"), expr.Col[int64](a, "b_id")
	b := expr.Table{Name: "c_b"}
	bID, bA := expr.Col[int64](b, "id"), expr.Col[int64](b, "a_id")
	cycle := migrate.Contribution{Owner: "lib", Tables: []migrate.Table{
		sqlite.CreateTable(a).Column(ddl.Column(aID), ddl.Column(aB)).Clause(sqlite.PrimaryKey(aID), sqlite.References(aB, bID)),
		sqlite.CreateTable(b).Column(ddl.Column(bID), ddl.Column(bA)).Clause(sqlite.PrimaryKey(bID), sqlite.References(bA, aID)),
	}}
	if _, err := migrate.Install(cycle); !errors.Is(err, schema.ErrCycle) {
		t.Errorf("a cycle: %v", err)
	}

	first, err := migrate.Install(application(), library())
	if err != nil {
		t.Fatal(err)
	}
	for range 3 {
		again, err := migrate.Install(library(), application())
		if err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(first, again) {
			t.Fatalf("the order depends on the input order or the run:\n%v\n%v", first.SQL(), again.SQL())
		}
	}
}

// TestInstall_OrdersAcrossSchemas pins that a reference to a table in a
// schema resolves to that table: a library's auth.users and an application's
// public orders, where the reference is the only thing linking them.
func TestInstall_OrdersAcrossSchemas(t *testing.T) {
	users := expr.Table{Schema: "auth", Name: "users"}
	uID := expr.Col[int64](users, "id")
	orders := expr.Table{Name: "orders"}
	oID, oUser := expr.Col[int64](orders, "id"), expr.Col[int64](orders, "user_id")
	s, err := migrate.Install(
		migrate.Contribution{Owner: "app", Tables: []migrate.Table{
			pg.CreateTable(orders).Column(ddl.Column(oID), ddl.Column(oUser)).Clause(pg.PrimaryKey(oID), pg.References(oUser, uID)),
		}},
		migrate.Contribution{Owner: "ubgo/auth", Tables: []migrate.Table{
			pg.CreateTable(users).Column(ddl.Column(uID)).Clause(pg.PrimaryKey(uID)),
		}},
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(s) != 2 || s[0].SQL[:28] != `CREATE TABLE "auth"."users" ` {
		t.Fatalf("auth.users must come first: %v", s.SQL())
	}
}

// TestExecute_EdgeCases pins the runner on the inputs around its main path:
// an empty script does nothing, a cancelled context fails before anything
// runs, and a check that finds a row rolls back the transaction it is in, so
// the statements before it in that transaction are not left behind.
func TestExecute_EdgeCases(t *testing.T) {
	ctx := context.Background()
	db := fileDB(t)
	if err := migrate.ExecuteDB(ctx, db, nil); err != nil {
		t.Errorf("an empty script: %v", err)
	}

	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	if err := migrate.ExecuteDB(cancelled, db, ddl.Script{{SQL: `CREATE TABLE x (n INTEGER)`, Transactional: true}}); !errors.Is(err, context.Canceled) {
		t.Errorf("a cancelled context: %v", err)
	}
	if _, err := db.ExecContext(ctx, `SELECT 1 FROM x`); err == nil {
		t.Error("a cancelled script created its table")
	}

	err := migrate.ExecuteDB(ctx, db, ddl.Script{
		{SQL: `CREATE TABLE y (n INTEGER)`, Transactional: true},
		{SQL: `SELECT 1`, Transactional: true, MustReturnNoRows: true},
	})
	if !errors.Is(err, migrate.ErrCheckFailed) {
		t.Fatalf("a check that found a row: %v", err)
	}
	if _, err := db.ExecContext(ctx, `SELECT 1 FROM y`); err == nil {
		t.Error("the failed check's transaction was committed")
	}
	if err := migrate.ExecuteDB(ctx, db, ddl.Script{{SQL: `SELECT 1 WHERE 0`, Transactional: true, MustReturnNoRows: true}}); err != nil {
		t.Errorf("a check that found nothing: %v", err)
	}
}

// valueTable is a Table that is a struct value, not a pointer, which isNil
// must not mistake for nil.
type valueTable struct{ inner *sqlite.CreateTableReady }

func (v valueTable) Build() (ddl.Script, error)      { return v.inner.Build() }
func (v valueTable) Describe() (schema.Table, error) { return v.inner.Describe() }

// TestInstall_AcceptsAValueTable pins that the nil check only catches nil:
// a Table implemented by a struct value installs.
func TestInstall_AcceptsAValueTable(t *testing.T) {
	tb := expr.Table{Name: "v_t"}
	id := expr.Col[int64](tb, "id")
	s, err := migrate.Install(migrate.Contribution{Owner: "lib", Tables: []migrate.Table{
		valueTable{sqlite.CreateTable(tb).Column(ddl.Column(id)).Clause(sqlite.PrimaryKey(id))},
	}})
	if err != nil || len(s) != 1 {
		t.Fatalf("%v, %v", s, err)
	}
}
