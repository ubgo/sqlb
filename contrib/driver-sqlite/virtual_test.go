package driversqlite_test

import (
	"context"
	"strings"
	"testing"

	"github.com/ubgo/sqlb"
	driversqlite "github.com/ubgo/sqlb/contrib/driver-sqlite"
	"github.com/ubgo/sqlb/exec"
	"github.com/ubgo/sqlb/sqlite"
)

// CREATE VIRTUAL TABLE executed: geopoly, and the general form for a module
// this package has no builder for.
//
// A virtual table is the one DDL statement whose text SQLite barely checks
// itself — everything inside the parentheses goes to the module — so rendering
// proves very little here. What proves it is creating the table, putting rows
// in it and asking the module a question only it can answer.

var (
	geoShapes = sqlb.Table{Name: "geo_shapes", Alias: "g"}
	geoLabel  = sqlb.Col[string](geoShapes, "label")
	geoWeight = sqlb.Col[int64](geoShapes, "weight")
)

// TestTask_IndexPolygonsWithGeopoly is the task geopoly exists for: store
// polygons and ask which one contains a point, without scanning every row.
//
// The columns are declared once, on the table, and used to create the virtual
// table and to read it back — the one-declaration promise the DDL package
// exists for. The shape itself goes through geopoly's own functions, because
// _shape is the module's hidden column rather than one this table declares.
func TestTask_IndexPolygonsWithGeopoly(t *testing.T) {
	ctx := context.Background()
	db, err := driversqlite.Memory()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })

	s, err := sqlite.CreateGeopoly(geoShapes, geoLabel, geoWeight).Build()
	run(t, db, s, err)
	if _, err := db.ExecContext(ctx, `INSERT INTO "geo_shapes"("_shape", "label", "weight") VALUES
		(geopoly_regular(0.0, 0.0, 2.0, 30), 'origin', 1),
		(geopoly_regular(50.0, 50.0, 2.0, 30), 'far', 2)`); err != nil {
		t.Fatal(err)
	}

	// The question the module answers: which polygon contains this point. A
	// scan would have to test every row; geopoly uses its index.
	var label string
	var weight int64
	if err := db.QueryRowContext(ctx,
		`SELECT "label", "weight" FROM "geo_shapes" WHERE geopoly_contains_point("_shape", 0.5, 0.5)`).
		Scan(&label, &weight); err != nil {
		t.Fatal(err)
	}
	if label != "origin" || weight != 1 {
		t.Errorf("the containing polygon is %q/%d, want origin/1", label, weight)
	}

	// The same columns, read through the query builder, which is the half a
	// raw statement would not prove: the names the DDL wrote are the names the
	// queries use.
	var got string
	q, err := sqlite.Select().Columns(sqlb.Bind(geoLabel, &got)).From(geoShapes).
		Where(sqlb.Eq(geoWeight, int64(2))).Build()
	if err != nil {
		t.Fatal(err)
	}
	if err := exec.One(ctx, db, q); err != nil {
		t.Fatal(err)
	}
	if got != "far" {
		t.Errorf("read back %q, want far", got)
	}
}

// TestVirtualTable_ExecutesOnSQLite covers the shapes the task test does not:
// a geopoly table with no columns of its own, IF NOT EXISTS, and the general
// form reaching a real module.
func TestVirtualTable_ExecutesOnSQLite(t *testing.T) {
	ctx := context.Background()
	db, err := driversqlite.Memory()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })

	bare := sqlb.Table{Name: "geo_bare"}
	s, err := sqlite.CreateGeopoly(bare).Build()
	run(t, db, s, err)
	if _, err := db.ExecContext(ctx, `INSERT INTO "geo_bare"("_shape") VALUES (geopoly_regular(1.0, 1.0, 1.0, 4))`); err != nil {
		t.Fatalf("a geopoly table with no columns of its own is still usable: %v", err)
	}

	// IF NOT EXISTS on a table that is already there is a no-op rather than an
	// error, which is what makes a create repeatable.
	again, err := sqlite.CreateGeopoly(bare).IfNotExists().Build()
	run(t, db, again, err)

	// The general form, against a module this package does have a builder for,
	// so the result can be checked: an FTS5 table created through
	// CreateVirtualTable answers a MATCH query.
	notes := sqlb.Table{Name: "vt_notes"}
	s, err = sqlite.CreateVirtualTable(notes, sqlite.ModuleFTS5, "body", "tokenize = 'porter unicode61'").Build()
	run(t, db, s, err)
	if _, err := db.ExecContext(ctx, `INSERT INTO "vt_notes"("body") VALUES ('the cat was running')`); err != nil {
		t.Fatal(err)
	}
	var body string
	// "run" matches "running" only because the porter tokenizer given as a
	// module argument reached the module, which is the thing this proves.
	if err := db.QueryRowContext(ctx, `SELECT "body" FROM "vt_notes" WHERE "vt_notes" MATCH 'run'`).Scan(&body); err != nil {
		t.Fatalf("the module arguments did not reach the module: %v", err)
	}
	if body != "the cat was running" {
		t.Errorf("matched %q", body)
	}
}

// TestVirtualTable_UnknownModuleFailsAtTheEngine pins where the boundary is.
// Build checks the module name is a bare identifier and nothing more, because
// a module can be compiled in or loaded at run time and no list here could be
// right; whether the module exists is the engine's answer, and it is a clear
// one.
func TestVirtualTable_UnknownModuleFailsAtTheEngine(t *testing.T) {
	ctx := context.Background()
	db, err := driversqlite.Memory()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })

	s, err := sqlite.CreateVirtualTable(sqlb.Table{Name: "nope"}, "no_such_module", "x").Build()
	if err != nil {
		t.Fatalf("Build refused a legal module name: %v", err)
	}
	_, err = db.ExecContext(ctx, s[0].SQL)
	if err == nil {
		t.Fatal("creating a table with a module that does not exist succeeded")
	}
	if !strings.Contains(err.Error(), "no such module") {
		t.Errorf("the engine's refusal changed: %v", err)
	}
}
