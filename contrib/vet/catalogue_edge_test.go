package vet_test

import (
	"context"
	"database/sql"
	"os"
	"strings"
	"testing"

	"github.com/ubgo/sqlb/contrib/vet"
	"github.com/ubgo/sqlb/ddl"
	"github.com/ubgo/sqlb/expr"
	"github.com/ubgo/sqlb/pg"
	"github.com/ubgo/sqlb/render"
	"github.com/ubgo/sqlb/schema"
)

func memorySQLite(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	db.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = db.Close() })
	return db
}

// TestReadCatalogue_SQLiteKeyNullabilityIsTheEngines pins SQLite's rule that
// a primary-key column holds NULL unless it is the rowid, NOT NULL, or in a
// STRICT or WITHOUT ROWID table, by asking the engine rather than a table:
// each shape tries to insert a NULL key, and the catalogue must say nullable
// exactly when the insert succeeds. INTEGER PRIMARY KEY DESC is the documented
// trap, an integer key that is not the rowid.
func TestReadCatalogue_SQLiteKeyNullabilityIsTheEngines(t *testing.T) {
	ctx := context.Background()
	db := memorySQLite(t)
	shapes := map[string]string{
		"k_text":      `CREATE TABLE k_text (k TEXT PRIMARY KEY)`,
		"k_rowid":     `CREATE TABLE k_rowid (k INTEGER PRIMARY KEY)`,
		"k_desc":      `CREATE TABLE k_desc (k INTEGER PRIMARY KEY DESC)`,
		"k_strict":    `CREATE TABLE k_strict (k TEXT PRIMARY KEY) STRICT`,
		"k_norowid":   `CREATE TABLE k_norowid (k TEXT PRIMARY KEY) WITHOUT ROWID`,
		"k_notnull":   `CREATE TABLE k_notnull (k TEXT NOT NULL PRIMARY KEY)`,
		"k_composite": `CREATE TABLE k_composite (k INTEGER, j INTEGER, PRIMARY KEY (k, j))`,
	}
	for _, q := range shapes {
		if _, err := db.ExecContext(ctx, q); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}
	cat, err := vet.ReadCatalogue(ctx, db, render.DialectSQLite)
	if err != nil {
		t.Fatal(err)
	}
	for table := range shapes {
		ins := `INSERT INTO ` + table + ` (k) VALUES (NULL)`
		if table == "k_composite" {
			ins = `INSERT INTO k_composite (k, j) VALUES (NULL, 1)`
		}
		_, insErr := db.ExecContext(ctx, ins)
		engineAllows := insErr == nil
		// The rowid alias turns a NULL into a fresh rowid rather than storing
		// it, so a successful insert there is not a stored NULL.
		if table == "k_rowid" {
			var nulls int
			if err := db.QueryRowContext(ctx, `SELECT count(*) FROM k_rowid WHERE k IS NULL`).Scan(&nulls); err != nil {
				t.Fatal(err)
			}
			engineAllows = nulls > 0
		}
		if got := cat[table]["k"].Nullable; got != engineAllows {
			t.Errorf("%s: catalogue says nullable=%v, the engine stores a NULL key: %v (%v)", table, got, engineAllows, insErr)
		}
	}
}

// TestReadCatalogue_KeysTheDefaultSchemaBothWays pins the Catalogue key
// convention on SQLite, whose one schema is main: a table is found by its
// bare name and by main.name.
func TestReadCatalogue_KeysTheDefaultSchemaBothWays(t *testing.T) {
	ctx := context.Background()
	db := memorySQLite(t)
	if _, err := db.ExecContext(ctx, `CREATE TABLE s_t (a TEXT)`); err != nil {
		t.Fatal(err)
	}
	cat, err := vet.ReadCatalogue(ctx, db, render.DialectSQLite)
	if err != nil {
		t.Fatal(err)
	}
	if cat["s_t"] == nil || cat[vet.CatalogueKey("main", "s_t")] == nil {
		t.Fatalf("s_t is not under both keys: %v", cat)
	}
	if got := vet.CatalogueKey("", "s_t"); got != "s_t" {
		t.Fatalf("CatalogueKey with no schema = %q", got)
	}
}

// TestCompareTables_EdgeCases pins the rules a catalogue literal can show:
// a schema-qualified table is looked up qualified; a column wrong twice gets
// two findings; a key column the DDL leaves nullable is the engine's NOT NULL,
// not drift, while a key column the database leaves nullable is drift; and no
// tables means no findings.
func TestCompareTables_EdgeCases(t *testing.T) {
	cat := vet.Catalogue{
		"auth.users": {"id": {Type: "bigint"}, "name": {Type: "boolean", Nullable: true}},
		"users":      {"id": {Type: "text", Nullable: true}},
	}
	auth := schema.Table{Schema: "auth", Name: "users", PrimaryKey: []string{"id"}, Columns: []schema.Column{
		{Name: "id", Type: schema.TypeBigInt, Nullable: true},
		{Name: "name", Type: schema.TypeText},
	}}
	public := schema.Table{Name: "users", PrimaryKey: []string{"id"}, Columns: []schema.Column{{Name: "id", Type: schema.TypeText}}}
	var lines []string
	for _, f := range vet.CompareTables(render.DialectPostgres, []schema.Table{auth, public}, cat) {
		lines = append(lines, f.Pos+": "+f.Message)
	}
	want := []string{
		"auth.users.name: the DDL says NOT NULL, the database allows NULL",
		"auth.users.name: the DDL says text, the database has boolean",
		"users.id: the DDL says NOT NULL, the database allows NULL",
	}
	if strings.Join(lines, "\n") != strings.Join(want, "\n") {
		t.Fatalf("got\n%s\nwant\n%s", strings.Join(lines, "\n"), strings.Join(want, "\n"))
	}
	if got := vet.CompareTables(render.DialectPostgres, []schema.Table{{Schema: "billing", Name: "users"}}, cat); len(got) != 1 || got[0].Pos != "billing.users" {
		t.Fatalf("a table in another schema matched: %v", got)
	}
	exprCat := vet.Catalogue{"e": {"at": {Type: "timestamp with time zone", Default: ptr("clock_timestamp()")}}}
	exprDesc := schema.Table{Name: "e", Columns: []schema.Column{{Name: "at", Type: schema.TypeTimestamp, Default: "(now())"}}}
	if got := vet.CompareTables(render.DialectPostgres, []schema.Table{exprDesc}, exprCat); len(got) != 1 || !got[0].Warning || !strings.Contains(got[0].Message, "check it by eye") {
		t.Fatalf("two different expressions must be one warning: %v", got)
	}
	if got := vet.CompareTables(render.DialectPostgres, nil, cat); len(got) != 0 {
		t.Fatalf("no tables gave findings: %v", got)
	}
}

// TestCompare_LooksUpTheDeclaredSchema pins the same key for Col[T]
// declarations: a column declared on auth.users is compared with auth.users,
// not with a same-named table in the default schema.
func TestCompare_LooksUpTheDeclaredSchema(t *testing.T) {
	cat := vet.Catalogue{
		"auth.users": {"id": {Type: "bigint"}},
		"users":      {"id": {Type: "text"}},
	}
	decls := vet.Declared{
		Columns: []vet.Declaration{{Pos: "a.go:1", Schema: "auth", Table: "users", Column: "id", GoType: "int64"}},
		Tables:  []vet.TableDeclaration{{Pos: "a.go:0", Schema: "auth", Name: "users"}},
	}
	if got := vet.Compare(render.DialectPostgres, decls, cat); len(got) != 0 {
		t.Fatalf("auth.users was compared with the wrong table: %v", got)
	}
	decls.Columns[0].Schema, decls.Tables[0].Schema = "", ""
	if got := vet.Compare(render.DialectPostgres, decls, cat); len(got) != 1 || !strings.Contains(got[0].Message, "declared Col[int64], database has text") {
		t.Fatalf("the unqualified declaration did not read public.users: %v", got)
	}
}

// TestCompareTables_PostgresSchemas runs the schema rule on a real Postgres:
// a table in a schema of its own is read, and a same-named table in the
// default schema is a different table.
func TestCompareTables_PostgresSchemas(t *testing.T) {
	dsn := os.Getenv("SQLB_PG_DSN")
	if dsn == "" {
		t.Skip("SKIPPED, NOT PASSED: set SQLB_PG_DSN to run the Postgres schema check")
	}
	ctx := context.Background()
	db, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = db.Exec(`DROP SCHEMA IF EXISTS v_auth CASCADE`)
		_, _ = db.Exec(`DROP TABLE IF EXISTS v_accounts`)
		_ = db.Close()
	})
	accounts := expr.Table{Schema: "v_auth", Name: "v_accounts", Alias: "a"}
	aID := expr.Col[int64](accounts, "id")
	for _, q := range []string{`CREATE SCHEMA v_auth`, `CREATE TABLE v_accounts (id text)`} {
		if _, err := db.ExecContext(ctx, q); err != nil {
			t.Fatal(err)
		}
	}
	stmt := pg.CreateTable(accounts).Column(ddl.Column(aID)).Clause(pg.PrimaryKey(aID))
	s, err := stmt.Build()
	if err != nil {
		t.Fatal(err)
	}
	for _, st := range s {
		if _, err := db.ExecContext(ctx, st.SQL); err != nil {
			t.Fatal(err)
		}
	}
	desc, err := stmt.Describe()
	if err != nil {
		t.Fatal(err)
	}
	cat, err := vet.ReadCatalogue(ctx, db, render.DialectPostgres)
	if err != nil {
		t.Fatal(err)
	}
	if got := vet.CompareTables(render.DialectPostgres, []schema.Table{desc}, cat); len(got) != 0 {
		t.Fatalf("v_auth.v_accounts differs from its own DDL: %v", got)
	}
	desc.Schema = ""
	if got := vet.CompareTables(render.DialectPostgres, []schema.Table{desc}, cat); len(got) == 0 {
		t.Fatal("the public v_accounts, a text column, matched the DDL for v_auth.v_accounts")
	}
}

func ptr(s string) *string { return &s }
