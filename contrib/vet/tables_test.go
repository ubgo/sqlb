package vet_test

import (
	"context"
	"database/sql"
	"os"
	"strings"
	"testing"

	_ "github.com/go-sql-driver/mysql"

	"github.com/ubgo/sqlb/contrib/vet"
	"github.com/ubgo/sqlb/ddl"
	"github.com/ubgo/sqlb/expr"
	"github.com/ubgo/sqlb/mysql"
	"github.com/ubgo/sqlb/pg"
	"github.com/ubgo/sqlb/render"
	"github.com/ubgo/sqlb/schema"
	"github.com/ubgo/sqlb/sqlite"
)

// The table a library declares as DDL, and the drift a database can have
// from it: the checks read the statement's own description.
var (
	vUsers = expr.Table{Name: "v_users", Alias: "u"}
	vID    = expr.Col[int64](vUsers, "id")
	vEmail = expr.Col[string](vUsers, "email")
	vNick  = expr.Col[*string](vUsers, "nick")
	vAge   = expr.Col[int64](vUsers, "age")
)

type describer interface {
	Build() (ddl.Script, error)
	Describe() (schema.Table, error)
}

// driftCheck installs what the DDL creates, confirms it is clean, then makes
// the database drift four ways with the engine's own SQL and checks each is
// named once.
func driftCheck(t *testing.T, db *sql.DB, dialect string, stmt describer, drift []string) {
	t.Helper()
	ctx := context.Background()
	s, err := stmt.Build()
	if err != nil {
		t.Fatal(err)
	}
	for _, st := range s {
		if _, err := db.ExecContext(ctx, st.SQL); err != nil {
			t.Fatalf("%s: %v", st.SQL, err)
		}
	}
	desc, err := stmt.Describe()
	if err != nil {
		t.Fatal(err)
	}
	cat, err := vet.ReadCatalogue(ctx, db, dialect)
	if err != nil {
		t.Fatal(err)
	}
	if got := vet.CompareTables(dialect, []schema.Table{desc}, cat); len(got) != 0 {
		t.Fatalf("the database the DDL just created differs from it: %v", got)
	}
	for _, q := range drift {
		if _, err := db.ExecContext(ctx, q); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}
	cat, err = vet.ReadCatalogue(ctx, db, dialect)
	if err != nil {
		t.Fatal(err)
	}
	got := vet.CompareTables(dialect, []schema.Table{desc, {Name: "v_ghosts"}}, cat)
	var lines []string
	for _, f := range got {
		prefix := "error"
		if f.Warning {
			prefix = "warning"
		}
		lines = append(lines, prefix+" "+f.Pos+": "+f.Message)
	}
	want := []string{
		"error v_ghosts: table \"v_ghosts\" is not in the database",
		"warning v_users: database has columns the DDL does not create: extra",
		"error v_users.age: column is not in the database",
		"error v_users.email: the DDL says NOT NULL, the database allows NULL",
		"error v_users.nick: the DDL allows NULL, the database says NOT NULL",
	}
	if strings.Join(lines, "\n") != strings.Join(want, "\n") {
		t.Fatalf("got\n%s\nwant\n%s", strings.Join(lines, "\n"), strings.Join(want, "\n"))
	}
}

// TestCompareTables_SQLite runs the drift check with no server.
func TestCompareTables_SQLite(t *testing.T) {
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	db.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = db.Close() })
	stmt := sqlite.CreateTable(vUsers).Column(ddl.Column(vID), ddl.Column(vEmail), ddl.Column(vNick), ddl.Column(vAge)).Clause(sqlite.PrimaryKey(vID))
	// SQLite cannot alter nullability, so the drift is a rebuilt table.
	driftCheck(t, db, render.DialectSQLite, stmt, []string{
		`DROP TABLE v_users`,
		`CREATE TABLE v_users (id INTEGER NOT NULL PRIMARY KEY, email TEXT, nick TEXT NOT NULL, extra TEXT)`,
	})
}

// TestCompareTables_TypeDrift pins the type check: a column the DDL declares
// as one portable type and the database holds as another.
func TestCompareTables_TypeDrift(t *testing.T) {
	desc := schema.Table{Name: "t", Columns: []schema.Column{{Name: "n", Type: schema.TypeBigInt}, {Name: "r", Raw: "citext"}}}
	cat := vet.Catalogue{"t": {"n": {Type: "boolean"}, "r": {Type: "user-defined"}}}
	got := vet.CompareTables(render.DialectPostgres, []schema.Table{desc}, cat)
	if len(got) != 1 || got[0].Message != "the DDL says bigint, the database has boolean" {
		t.Fatalf("got %v", got)
	}
}

// TestCompareTables_Postgres runs the drift check on a real Postgres.
func TestCompareTables_Postgres(t *testing.T) {
	dsn := os.Getenv("SQLB_PG_DSN")
	if dsn == "" {
		t.Skip("SKIPPED, NOT PASSED: set SQLB_PG_DSN to run the Postgres drift check")
	}
	db, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = db.Exec(`DROP TABLE IF EXISTS v_users`); _ = db.Close() })
	stmt := pg.CreateTable(vUsers).Column(ddl.Column(vID), ddl.Column(vEmail), ddl.Column(vNick), ddl.Column(vAge)).Clause(pg.PrimaryKey(vID))
	driftCheck(t, db, render.DialectPostgres, stmt, []string{
		`ALTER TABLE v_users ALTER COLUMN email DROP NOT NULL, ALTER COLUMN nick SET NOT NULL, DROP COLUMN age, ADD COLUMN extra text`,
	})
}

// TestCompareTables_MySQL runs the drift check on a real MySQL.
func TestCompareTables_MySQL(t *testing.T) {
	dsn := os.Getenv("SQLB_MYSQL_DSN")
	if dsn == "" {
		t.Skip("SKIPPED, NOT PASSED: set SQLB_MYSQL_DSN to run the MySQL drift check")
	}
	db, err := sql.Open("mysql", dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = db.Exec("DROP TABLE IF EXISTS v_users"); _ = db.Close() })
	stmt := mysql.CreateTable(vUsers).Column(ddl.Column(vID), ddl.Column(vEmail), ddl.Column(vNick), ddl.Column(vAge)).Clause(mysql.PrimaryKey(vID))
	driftCheck(t, db, render.DialectMySQL, stmt, []string{
		"ALTER TABLE v_users MODIFY COLUMN email VARCHAR(255) NULL, MODIFY COLUMN nick VARCHAR(255) NOT NULL, DROP COLUMN age, ADD COLUMN extra TEXT",
	})
}
