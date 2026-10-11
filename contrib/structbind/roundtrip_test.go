package structbind_test

import (
	"context"
	"database/sql"
	"testing"

	"github.com/ubgo/sqlb/contrib/structbind"
	"github.com/ubgo/sqlb/dialect/sqlite"
	"github.com/ubgo/sqlb/exec"
	"github.com/ubgo/sqlb/expr"
	"github.com/ubgo/sqlb/stmt"
	_ "modernc.org/sqlite"
)

type account struct {
	ID    string `db:"id"`
	Email string `db:"email"`
	Age   int    `db:"age"`
}

var accounts = expr.Table{Name: "accounts", Alias: "a"}
var accountMap = structbind.MustNew[account](accounts)

// TestStructBind_RoundTrips executes the whole loop against a real database:
// an INSERT whose values come from a struct, and a SELECT whose scan targets
// come from the same declaration.
//
// Rendering proves the SQL is shaped right; only executing proves the columns
// and the destinations line up, which is the one thing a reflection-derived
// binding can get wrong in a way that renders perfectly.
func TestStructBind_RoundTrips(t *testing.T) {
	ctx := context.Background()
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()

	if _, err := db.ExecContext(ctx,
		`CREATE TABLE accounts (id TEXT NOT NULL, email TEXT NOT NULL, age INTEGER NOT NULL)`); err != nil {
		t.Fatal(err)
	}

	// Write from a struct.
	in := account{ID: "a1", Email: "ada@x.com", Age: 36}
	ins, err := stmt.NewInsert(sqlite.Dialect{}).Into(accounts).
		Row(accountMap.Assignments(&in)...).
		Build()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, ins.SQL, ins.Args...); err != nil {
		t.Fatalf("%v\n%s", err, ins.SQL)
	}

	// Read back into a struct.
	var out account
	sel, err := stmt.NewSelect(sqlite.Dialect{}).
		Columns(accountMap.Bindings(&out)...).
		From(accounts).
		Where(expr.Eq(expr.Col[string](accounts, "id"), "a1")).
		Build()
	if err != nil {
		t.Fatal(err)
	}
	if err := exec.One(ctx, db, sel); err != nil {
		t.Fatal(err)
	}

	if out != in {
		t.Fatalf("round trip changed the value\n got %+v\nwant %+v", out, in)
	}
}

// snakeSchema is an untagged struct. Under the old lowercasing fallback its
// columns were createdat and isactive and every statement failed; this is the
// case the Namer exists for.
type snakeSchema struct {
	ID         string
	UserEmail  string
	CreatedAt  string
	IsActive   bool
	HTTPStatus int
}

// camelSchema is the same struct against a database whose columns are
// camelCase, which is what JavaScript-side tooling generates and what makes a
// fixed convention wrong rather than merely inconvenient.
type camelSchema struct {
	ID         string
	UserEmail  string
	CreatedAt  string
	IsActive   bool
	HTTPStatus int
}

// TestNamer_AgainstRealSchemas executes both conventions against real tables.
//
// The rendered SQL was already asserted in mapping_test.go. This is here
// because a column name is the one thing a builder cannot check: it renders
// whatever it is given, so only a database can say whether the name was right.
// Both tables are created with the exact spellings the namers must produce, so
// a wrong derivation fails as "no such column" rather than as a diff.
func TestNamer_AgainstRealSchemas(t *testing.T) {
	ctx := context.Background()
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()

	for _, ddl := range []string{
		`CREATE TABLE snake_rows (
			id TEXT NOT NULL, user_email TEXT NOT NULL, created_at TEXT NOT NULL,
			is_active BOOLEAN NOT NULL, http_status INTEGER NOT NULL)`,
		`INSERT INTO snake_rows VALUES ('s1','ada@x.com','2026-01-01',1,200)`,
		`CREATE TABLE camel_rows (
			"id" TEXT NOT NULL, "userEmail" TEXT NOT NULL, "createdAt" TEXT NOT NULL,
			"isActive" BOOLEAN NOT NULL, "httpStatus" INTEGER NOT NULL)`,
		`INSERT INTO camel_rows VALUES ('c1','bob@x.com','2026-02-02',0,404)`,
	} {
		if _, err := db.ExecContext(ctx, ddl); err != nil {
			t.Fatal(err)
		}
	}

	t.Run("snake_case schema, the default", func(t *testing.T) {
		tb := expr.Table{Name: "snake_rows", Alias: "s"}
		m := structbind.MustNew[snakeSchema](tb) // no namer: Snake
		var got snakeSchema
		q, err := stmt.NewSelect(sqlite.Dialect{}).
			Columns(m.Bindings(&got)...).From(tb).Build()
		if err != nil {
			t.Fatal(err)
		}
		if err := exec.One(ctx, db, q); err != nil {
			t.Fatalf("%v\nSQL: %s", err, q.SQL)
		}
		want := snakeSchema{"s1", "ada@x.com", "2026-01-01", true, 200}
		if got != want {
			t.Fatalf("got %+v, want %+v", got, want)
		}
	})

	t.Run("camelCase schema", func(t *testing.T) {
		tb := expr.Table{Name: "camel_rows", Alias: "c"}
		m := structbind.MustNew[camelSchema](tb, structbind.Camel)
		var got camelSchema
		q, err := stmt.NewSelect(sqlite.Dialect{}).
			Columns(m.Bindings(&got)...).From(tb).Build()
		if err != nil {
			t.Fatal(err)
		}
		if err := exec.One(ctx, db, q); err != nil {
			t.Fatalf("%v\nSQL: %s", err, q.SQL)
		}
		want := camelSchema{"c1", "bob@x.com", "2026-02-02", false, 404}
		if got != want {
			t.Fatalf("got %+v, want %+v", got, want)
		}
	})

	// The wrong namer must fail loudly against a real schema, which is what
	// makes choosing one a real decision rather than a formality.
	t.Run("the wrong convention fails at the database", func(t *testing.T) {
		tb := expr.Table{Name: "snake_rows", Alias: "s"}
		m := structbind.MustNew[snakeSchema](tb, structbind.Camel)
		var got snakeSchema
		q, err := stmt.NewSelect(sqlite.Dialect{}).
			Columns(m.Bindings(&got)...).From(tb).Build()
		if err != nil {
			t.Fatal(err)
		}
		if err := exec.One(ctx, db, q); err == nil {
			t.Fatal("camelCase names matched a snake_case table; the test proves nothing")
		}
	})
}
