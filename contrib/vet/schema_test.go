package vet_test

import (
	"context"
	"database/sql"
	"os"
	"strings"
	"testing"

	"github.com/ubgo/sqlb/contrib/vet"
	"github.com/ubgo/sqlb/render"

	_ "github.com/jackc/pgx/v5/stdlib"
	_ "modernc.org/sqlite"
)

// TestSchema_Postgres is the same diff against a real Postgres, which is the
// only way the information_schema query and the standard's type spellings
// are verified. It needs SQLB_PG_DSN and says so loudly rather than passing
// quietly without it; the table is created and dropped by the test.
func TestSchema_Postgres(t *testing.T) {
	dsn := os.Getenv("SQLB_PG_DSN")
	if dsn == "" {
		t.Skip("SKIPPED, NOT PASSED: set SQLB_PG_DSN to run the Postgres schema diff")
	}
	ctx := context.Background()
	db, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	if _, err := db.ExecContext(ctx, `CREATE TABLE accounts (
		id bigint PRIMARY KEY, email text NOT NULL, age integer NOT NULL,
		nick text, made timestamptz NOT NULL, extra bytea NOT NULL)`); err != nil {
		t.Fatal(err)
	}
	defer func() { _, _ = db.ExecContext(ctx, `DROP TABLE accounts`) }()

	got, err := vet.Schema(ctx, db, render.DialectPostgres, ".", "./internal/probe/schema")
	if err != nil {
		t.Fatal(err)
	}
	assertSchemaFindings(t, got, "integer")
}

// TestSchema_MySQL is the same diff against a real MySQL 8, reading its
// information_schema, gated on SQLB_MYSQL_DSN.
func TestSchema_MySQL(t *testing.T) {
	dsn := os.Getenv("SQLB_MYSQL_DSN")
	if dsn == "" {
		t.Skip("SKIPPED, NOT PASSED: set SQLB_MYSQL_DSN to run the MySQL schema diff")
	}
	ctx := context.Background()
	db, err := sql.Open("mysql", dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	if _, err := db.ExecContext(ctx, `CREATE TABLE accounts (
		id BIGINT PRIMARY KEY, email VARCHAR(255) NOT NULL, age INT NOT NULL,
		nick VARCHAR(255), made DATETIME(6) NOT NULL, extra LONGBLOB NOT NULL)`); err != nil {
		t.Fatal(err)
	}
	defer func() { _, _ = db.ExecContext(ctx, `DROP TABLE accounts`) }()

	got, err := vet.Schema(ctx, db, render.DialectMySQL, ".", "./internal/probe/schema")
	if err != nil {
		t.Fatal(err)
	}
	assertSchemaFindings(t, got, "int")
}

// TestSchema_DiffsDeclarationsAgainstTheDatabase is the --dsn mode on
// SQLite, which needs no server: five declarations are wrong in five
// different ways and each is named once, by file and line, and the right ones
// are not.
func TestSchema_DiffsDeclarationsAgainstTheDatabase(t *testing.T) {
	ctx := context.Background()
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	if _, err := db.ExecContext(ctx, `CREATE TABLE accounts (
		id INTEGER PRIMARY KEY, email TEXT NOT NULL, age INTEGER NOT NULL,
		nick TEXT, made DATETIME NOT NULL, extra BLOB NOT NULL)`); err != nil {
		t.Fatal(err)
	}

	got, err := vet.Schema(ctx, db, render.DialectSQLite, ".", "./internal/probe/schema")
	if err != nil {
		t.Fatal(err)
	}
	assertSchemaFindings(t, got, "integer")
}

// assertSchemaFindings is what every engine must report for the probe, with
// intType the engine's catalogue spelling of the integer column: four
// errors, one warning, and nothing about the two right declarations.
func assertSchemaFindings(t *testing.T, got []vet.Finding, intType string) {
	t.Helper()
	want := []string{
		`accounts.age: declared Col[string], database has ` + intType,
		`accounts.nick: column is NULLABLE, declared Col[string]; use Col[*string]`,
		`accounts.deleted: column is not in the database`,
		`ghosts.id: table "ghosts" is not in the database`,
		`accounts: database has columns no declaration names: extra`,
	}
	if len(got) != len(want) {
		t.Fatalf("want %d findings, got %d: %v", len(want), len(got), got)
	}
	for _, g := range got {
		if g.Warning != strings.Contains(g.Message, "no declaration names") {
			t.Errorf("warning flag wrong on %v", g)
		}
	}
	for _, w := range want {
		found := false
		for _, g := range got {
			if strings.Contains(g.Message, w) {
				found = true
			}
		}
		if !found {
			t.Errorf("missing finding %q in %v", w, got)
		}
	}
}
