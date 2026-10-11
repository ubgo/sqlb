package migrate_test

import (
	"context"
	"database/sql"
	"testing"

	_ "modernc.org/sqlite"

	"github.com/ubgo/sqlb/bind"
	"github.com/ubgo/sqlb/contrib/migrate"
	sqlitedialect "github.com/ubgo/sqlb/dialect/sqlite"
	"github.com/ubgo/sqlb/exec"
	"github.com/ubgo/sqlb/expr"
	"github.com/ubgo/sqlb/schema"
	"github.com/ubgo/sqlb/stmt"
)

// TestPlan_RunsAgainstARealDatabase is the end of the chain, and the reason the
// schema layer is not just a data structure with opinions.
//
// Two packages declare tables without knowing about each other. A third orders
// and renders them. A real engine accepts the DDL. Then the query builder, which
// took no part in any of that, reads a row back through columns derived from the
// same declaration. Every one of those steps was asserted separately elsewhere;
// this is the one that would catch a plan that is well-formed and wrong.
func TestPlan_RunsAgainstARealDatabase(t *testing.T) {
	ctx := context.Background()
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	// Foreign keys are off by default in SQLite, and a migration test that did
	// not turn them on would pass while proving nothing about the constraints
	// it just rendered.
	if _, err := db.ExecContext(ctx, "PRAGMA foreign_keys = ON"); err != nil {
		t.Fatal(err)
	}

	set := assembled(t)
	plan, err := migrate.Plan(sqlitedialect.Dialect{}, set)
	if err != nil {
		t.Fatal(err)
	}
	for i, ddl := range plan {
		if _, err := db.ExecContext(ctx, ddl); err != nil {
			t.Fatalf("statement %d failed: %v\n%s", i, err, ddl)
		}
	}

	seed(ctx, t, db)

	// Read a row back through the declaration the tables were created from.
	users := set.Tables()[0]
	email, err := schema.Col[string](users, "email")
	if err != nil {
		t.Fatal(err)
	}
	id, err := schema.Col[string](users, "id")
	if err != nil {
		t.Fatal(err)
	}

	var gotEmail string
	q, err := stmt.NewSelect(sqlitedialect.Dialect{}).
		Columns(bind.Bind(email, &gotEmail)).
		From(users.Ref()).
		Where(expr.Eq(id, "u1")).
		Build()
	if err != nil {
		t.Fatal(err)
	}
	if err := exec.One(ctx, db, q); err != nil {
		t.Fatal(err)
	}
	if gotEmail != "ada@example.com" {
		t.Fatalf("got %q, want %q", gotEmail, "ada@example.com")
	}
}

// seed inserts one user and one invoice, then proves the foreign key the plan
// rendered is actually enforced.
func seed(ctx context.Context, t *testing.T, db *sql.DB) {
	t.Helper()
	if _, err := db.ExecContext(ctx,
		`INSERT INTO "auth_user" ("id","email","created_at") VALUES ('u1','ada@example.com','now')`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx,
		`INSERT INTO "invoice" ("id","user_id","total","note") VALUES ('i1','u1','10.00',NULL)`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx,
		`INSERT INTO "invoice" ("id","user_id","total","note") VALUES ('i2','ghost','1.00',NULL)`); err == nil {
		t.Fatal("an invoice referencing a user that does not exist was accepted; the foreign key in the plan is not doing anything")
	}
}

// TestPlan_UniqueIndexIsEnforced pins the other half of the DDL: an index the
// plan declared unique has to reject a duplicate, or the statement was decorative.
func TestPlan_UniqueIndexIsEnforced(t *testing.T) {
	ctx := context.Background()
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()

	plan, err := migrate.Plan(sqlitedialect.Dialect{}, assembled(t))
	if err != nil {
		t.Fatal(err)
	}
	for _, ddl := range plan {
		if _, err := db.ExecContext(ctx, ddl); err != nil {
			t.Fatalf("%v\n%s", err, ddl)
		}
	}

	ins := `INSERT INTO "auth_user" ("id","email","created_at") VALUES (?,'same@example.com','now')`
	if _, err := db.ExecContext(ctx, ins, "u1"); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, ins, "u2"); err == nil {
		t.Fatal("a duplicate email was accepted; the unique index in the plan is not doing anything")
	}
}
