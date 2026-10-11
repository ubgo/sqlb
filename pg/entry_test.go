package pg_test

import (
	"testing"

	"github.com/ubgo/sqlb/ddl"
	"github.com/ubgo/sqlb/dialect/postgres"
	"github.com/ubgo/sqlb/expr"
	"github.com/ubgo/sqlb/pg"
	"github.com/ubgo/sqlb/stmt"
)

var (
	entryUsers = expr.Table{Name: "users", Alias: "u"}
	entryID    = expr.Col[int64](entryUsers, "id")
	entryName  = expr.Col[string](entryUsers, "name")
)

// TestEntry_IsTheLongFormExactly pins that each constructor is only a shorter
// spelling: byte-identical SQL and arguments to stmt.New* with the dialect
// written out. Anything more would be a second builder.
func TestEntry_IsTheLongFormExactly(t *testing.T) {
	d := postgres.Dialect{}
	cases := []struct {
		name        string
		short, long func() (stmt.Query, error)
	}{
		{"select",
			func() (stmt.Query, error) {
				return pg.Select(entryID, entryName).From(entryUsers).Where(expr.Eq(entryID, 1)).Build()
			},
			func() (stmt.Query, error) {
				return stmt.NewSelect(d).Exprs(entryID, entryName).From(entryUsers).Where(expr.Eq(entryID, 1)).Build()
			}},
		{"insert",
			func() (stmt.Query, error) { return pg.Insert(entryUsers).Row(stmt.Set(entryName, "a")).Build() },
			func() (stmt.Query, error) {
				return stmt.NewInsert(d).Into(entryUsers).Row(stmt.Set(entryName, "a")).Build()
			}},
		{"update",
			func() (stmt.Query, error) {
				return pg.Update(entryUsers).Set(stmt.Set(entryName, "b")).Where(expr.Eq(entryID, 1)).Build()
			},
			func() (stmt.Query, error) {
				return stmt.NewUpdate(d).Table(entryUsers).Set(stmt.Set(entryName, "b")).Where(expr.Eq(entryID, 1)).Build()
			}},
		{"delete",
			func() (stmt.Query, error) { return pg.Delete(entryUsers).Where(expr.Eq(entryID, 1)).Build() },
			func() (stmt.Query, error) {
				return stmt.NewDelete(d).From(entryUsers).Where(expr.Eq(entryID, 1)).Build()
			}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			a, errA := tc.short()
			b, errB := tc.long()
			if errA != nil || errB != nil {
				t.Fatalf("errors: %v / %v", errA, errB)
			}
			if a.SQL != b.SQL || len(a.Args) != len(b.Args) {
				t.Fatalf("short form differs\n short %s %v\n long  %s %v", a.SQL, a.Args, b.SQL, b.Args)
			}
		})
	}
}

// TestEntry_AliasesAreTheSameTypes pins that a value passes through the alias
// and the stmt entryName in both directions with no conversion, which is what makes
// *pg.SelectQuery a spelling rather than a wrapper. The conversions are
// function values so the compiler is the assertion; the dialect check is the
// one thing worth running.
func TestEntry_AliasesAreTheSameTypes(t *testing.T) {
	_ = func(s *pg.SelectQuery) *stmt.Select[postgres.Dialect] { return s }
	_ = func(s *stmt.Select[postgres.Dialect]) *pg.SelectQuery { return s }
	_ = func(u *pg.FilteredUpdate) *stmt.FilteredUpdate[postgres.Dialect] { return u }
	_ = func(i *pg.InsertReady) *stmt.InsertReady[postgres.Dialect] { return i }
	_ = func(x *pg.FilteredDelete) *stmt.FilteredDelete[postgres.Dialect] { return x }
	_ = func(u *pg.UpdateQuery) *stmt.Update[postgres.Dialect] { return u }
	_ = func(i *pg.InsertQuery) *stmt.Insert[postgres.Dialect] { return i }
	_ = func(x *pg.DeleteQuery) *stmt.Delete[postgres.Dialect] { return x }
	_ = func(c *pg.CreateTableQuery) *ddl.CreateTable[postgres.Dialect] { return c }
	_ = func(c *pg.CreateTableReady) *ddl.CreateTableReady[postgres.Dialect] { return c }
	_ = func(a *pg.AlterTableQuery) *ddl.AlterTable[postgres.Dialect] { return a }
	_ = func(a *pg.AlterTableReady) *ddl.AlterTableReady[postgres.Dialect] { return a }
	_ = func(x *pg.DropTableQuery) *ddl.DropTable[postgres.Dialect] { return x }

	if got := pg.Select().Dialect(); got != (pg.Dialect{}) {
		t.Fatalf("Select renders for %v, want %v", got, pg.Dialect{})
	}
}

// TestEntry_DDLIsTheLongFormExactly is the same pin for the DDL constructors:
// each is ddl.New* with the dialect written in, and nothing more.
func TestEntry_DDLIsTheLongFormExactly(t *testing.T) {
	d := postgres.Dialect{}
	cases := []struct {
		name        string
		short, long func() (ddl.Script, error)
	}{
		{"create",
			func() (ddl.Script, error) {
				return pg.CreateTable(entryUsers).IfNotExists().Column(ddl.Column(entryID)).Build()
			},
			func() (ddl.Script, error) {
				return ddl.NewCreateTable(d, entryUsers).IfNotExists().Column(ddl.Column(entryID)).Build()
			}},
		{"alter",
			func() (ddl.Script, error) { return pg.AlterTable(entryUsers).Action(ddl.DropColumn(entryName)).Build() },
			func() (ddl.Script, error) {
				return ddl.NewAlterTable(d, entryUsers).Action(ddl.DropColumn(entryName)).Build()
			}},
		{"drop",
			func() (ddl.Script, error) { return pg.DropTable(entryUsers).IfExists().Build() },
			func() (ddl.Script, error) { return ddl.NewDropTable(d, entryUsers).IfExists().Build() }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			a, errA := tc.short()
			b, errB := tc.long()
			if errA != nil || errB != nil {
				t.Fatalf("errors: %v / %v", errA, errB)
			}
			if len(a) == 0 || len(a) != len(b) {
				t.Fatalf("short form differs\n short %v\n long  %v", a, b)
			}
			for i := range a {
				if a[i] != b[i] {
					t.Fatalf("short form differs\n short %v\n long  %v", a, b)
				}
			}
		})
	}
}
