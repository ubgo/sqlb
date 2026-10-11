package sqlite_test

import (
	"errors"
	"testing"

	"github.com/ubgo/sqlb"
	"github.com/ubgo/sqlb/ddl"
	"github.com/ubgo/sqlb/sqlite"
)

var (
	shapes    = sqlb.Table{Name: "shapes", Alias: "s"}
	shapeName = sqlb.Col[string](shapes, "name")
	shapeArea = sqlb.Col[float64](shapes, "area")
)

// TestVirtualTable_Renders pins geopoly and the general form, including the
// two shapes a module with no arguments takes.
func TestVirtualTable_Renders(t *testing.T) {
	one := func(s ddl.Script, err error) string {
		t.Helper()
		if err != nil || len(s) != 1 {
			t.Fatalf("%v, %v", s, err)
		}
		return s[0].SQL
	}
	cases := []struct {
		name string
		got  string
		want string
	}{
		{"geopoly with columns",
			one(sqlite.CreateGeopoly(shapes, shapeName, shapeArea).Build()),
			`CREATE VIRTUAL TABLE "shapes" USING geopoly("name", "area")`},
		{"geopoly with none",
			one(sqlite.CreateGeopoly(shapes).Build()),
			`CREATE VIRTUAL TABLE "shapes" USING geopoly`},
		{"geopoly if not exists",
			one(sqlite.CreateGeopoly(shapes, shapeName).IfNotExists().Build()),
			`CREATE VIRTUAL TABLE IF NOT EXISTS "shapes" USING geopoly("name")`},
		{"a module with arguments",
			one(sqlite.CreateVirtualTable(shapes, "csv", "filename='x.csv'", "header=true").Build()),
			`CREATE VIRTUAL TABLE "shapes" USING csv(filename='x.csv', header=true)`},
		{"a module with none",
			one(sqlite.CreateVirtualTable(shapes, sqlite.ModuleDBStat).Build()),
			`CREATE VIRTUAL TABLE "shapes" USING dbstat`},
		{"a module named by constant",
			one(sqlite.CreateVirtualTable(shapes, sqlite.ModuleFTS5, "body").Build()),
			`CREATE VIRTUAL TABLE "shapes" USING fts5(body)`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if tc.got != tc.want {
				t.Errorf("got %s, want %s", tc.got, tc.want)
			}
		})
	}
}

// TestVirtualTable_Refuses covers what Build will not render: a module name
// that is not a bare identifier, which is the one place text from outside
// could leave its position, and a geopoly column belonging to another table.
func TestVirtualTable_Refuses(t *testing.T) {
	other := sqlb.Col[string](sqlb.Table{Name: "elsewhere"}, "name")
	cases := []struct {
		name  string
		built func() (ddl.Script, error)
		want  error
	}{
		{"no module", sqlite.CreateVirtualTable(shapes, "").Build, sqlite.ErrNoModule},
		{"a module name with a space", sqlite.CreateVirtualTable(shapes, "fts5 x").Build, sqlite.ErrModuleName},
		{"a module name closing the call", sqlite.CreateVirtualTable(shapes, "fts5(a); DROP TABLE t; --").Build, sqlite.ErrModuleName},
		{"a module name starting with a digit", sqlite.CreateVirtualTable(shapes, "5ts").Build, sqlite.ErrModuleName},
		{"no table name", sqlite.CreateVirtualTable(sqlb.Table{}, sqlite.ModuleGeopoly).Build, sqlite.ErrEmptyName},
		{"a column of another table", sqlite.CreateGeopoly(shapes, other).Build, ddl.ErrForeignColumn},
		{"the zero value", (&sqlite.CreateVirtualTableStatement{}).Build, sqlite.ErrNoModule},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := tc.built(); !errors.Is(err, tc.want) {
				t.Errorf("got %v, want %v", err, tc.want)
			}
		})
	}
}

// TestVirtualTable_IfNotExistsDerives pins the builder invariant: the method
// returns a derived statement and leaves its receiver alone, so a base held
// for two tables cannot pick up the second one's flag.
func TestVirtualTable_IfNotExistsDerives(t *testing.T) {
	base := sqlite.CreateGeopoly(shapes, shapeName)
	before, err := base.Build()
	if err != nil {
		t.Fatal(err)
	}
	derived := base.IfNotExists()
	if derived == base {
		t.Fatal("IfNotExists returned its receiver, so the statement is shared")
	}
	after, err := base.Build()
	if err != nil {
		t.Fatal(err)
	}
	if after[0].SQL != before[0].SQL {
		t.Errorf("the method wrote to its receiver:\nbefore %s\nafter  %s", before[0].SQL, after[0].SQL)
	}
	got, err := derived.Build()
	if err != nil {
		t.Fatal(err)
	}
	if got[0].SQL == before[0].SQL {
		t.Errorf("the derived statement did not change: %s", got[0].SQL)
	}
}
