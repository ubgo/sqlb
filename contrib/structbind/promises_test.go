package structbind_test

import (
	"context"
	"database/sql"
	"strings"
	"testing"

	"github.com/ubgo/sqlb/contrib/structbind"
	"github.com/ubgo/sqlb/dialect/sqlite"
	sqlexec "github.com/ubgo/sqlb/exec"
	"github.com/ubgo/sqlb/expr"
	"github.com/ubgo/sqlb/stmt"
)

// Build cannot inspect a live column type. database/sql can convert an integer
// into a string; incompatible text into an integer fails only during Scan.
// promise:structbind-unchecked-type
func TestPromise_StructbindDefersColumnTypeCheck(t *testing.T) {
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	db.SetMaxOpenConns(1)
	if _, err = db.Exec(`CREATE TABLE type_probe (value INTEGER); INSERT INTO type_probe VALUES (42)`); err != nil {
		t.Fatal(err)
	}
	table := expr.Table{Name: "type_probe"}
	type stringRow struct {
		Value string `db:"value"`
	}
	mapping, err := structbind.New[stringRow](table)
	if err != nil {
		t.Fatal(err)
	}
	var out stringRow
	q, err := stmt.NewSelect(sqlite.Dialect{}).Columns(mapping.Bindings(&out)...).From(table).Build()
	if err != nil {
		t.Fatalf("Build tried to check the database type: %v", err)
	}
	if err = sqlexec.One(context.Background(), db, q); err != nil || out.Value != "42" {
		t.Fatalf("compatible integer-to-string conversion: %+v, %v", out, err)
	}
	if _, err = db.Exec(`UPDATE type_probe SET value = 'not-an-integer'`); err != nil {
		t.Fatal(err)
	}
	type intRow struct {
		Value int64 `db:"value"`
	}
	ints, err := structbind.New[intRow](table)
	if err != nil {
		t.Fatal(err)
	}
	var bad intRow
	q, err = stmt.NewSelect(sqlite.Dialect{}).Columns(ints.Bindings(&bad)...).From(table).Build()
	if err != nil {
		t.Fatalf("Build rejected an unknown database value: %v", err)
	}
	if err = sqlexec.One(context.Background(), db, q); err == nil || !strings.Contains(err.Error(), "converting") {
		t.Fatalf("incompatible value must fail at scan: %v", err)
	}
}
