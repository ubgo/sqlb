package sqlite_test

import (
	"errors"
	"testing"

	"github.com/ubgo/sqlb/ddl"
	"github.com/ubgo/sqlb/expr"
	"github.com/ubgo/sqlb/pg"
	"github.com/ubgo/sqlb/sqlite"
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
	d := sqlite.Dialect{}
	cases := []struct {
		name        string
		short, long func() (stmt.Query, error)
	}{
		{"select",
			func() (stmt.Query, error) {
				return sqlite.Select(entryID, entryName).From(entryUsers).Where(expr.Eq(entryID, 1)).Build()
			},
			func() (stmt.Query, error) {
				return stmt.NewSelect(d).Exprs(entryID, entryName).From(entryUsers).Where(expr.Eq(entryID, 1)).Build()
			}},
		{"insert",
			func() (stmt.Query, error) { return sqlite.Insert(entryUsers).Row(stmt.Set(entryName, "a")).Build() },
			func() (stmt.Query, error) {
				return stmt.NewInsert(d).Into(entryUsers).Row(stmt.Set(entryName, "a")).Build()
			}},
		{"update",
			func() (stmt.Query, error) {
				return sqlite.Update(entryUsers).Set(stmt.Set(entryName, "b")).Where(expr.Eq(entryID, 1)).Build()
			},
			func() (stmt.Query, error) {
				return stmt.NewUpdate(d).Table(entryUsers).Set(stmt.Set(entryName, "b")).Where(expr.Eq(entryID, 1)).Build()
			}},
		{"delete",
			func() (stmt.Query, error) { return sqlite.Delete(entryUsers).Where(expr.Eq(entryID, 1)).Build() },
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
// *sqlite.SelectQuery a spelling rather than a wrapper. The conversions are
// function values so the compiler is the assertion; the dialect check is the
// one thing worth running.
func TestEntry_AliasesAreTheSameTypes(t *testing.T) {
	_ = func(s *sqlite.SelectQuery) *stmt.Select[sqlite.Dialect] { return s }
	_ = func(s *stmt.Select[sqlite.Dialect]) *sqlite.SelectQuery { return s }
	_ = func(u *sqlite.FilteredUpdate) *stmt.FilteredUpdate[sqlite.Dialect] { return u }
	_ = func(i *sqlite.InsertReady) *stmt.InsertReady[sqlite.Dialect] { return i }
	_ = func(x *sqlite.FilteredDelete) *stmt.FilteredDelete[sqlite.Dialect] { return x }
	_ = func(u *sqlite.UpdateQuery) *stmt.Update[sqlite.Dialect] { return u }
	_ = func(i *sqlite.InsertQuery) *stmt.Insert[sqlite.Dialect] { return i }
	_ = func(x *sqlite.DeleteQuery) *stmt.Delete[sqlite.Dialect] { return x }
	_ = func(c *sqlite.CreateTableQuery) *ddl.CreateTable[sqlite.Dialect] { return c }
	_ = func(c *sqlite.CreateTableReady) *ddl.CreateTableReady[sqlite.Dialect] { return c }
	_ = func(a *sqlite.AlterTableQuery) *ddl.AlterTable[sqlite.Dialect] { return a }
	_ = func(a *sqlite.AlterTableReady) *ddl.AlterTableReady[sqlite.Dialect] { return a }
	_ = func(x *sqlite.DropTableQuery) *ddl.DropTable[sqlite.Dialect] { return x }

	if got := sqlite.Select().Dialect(); got != (sqlite.Dialect{}) {
		t.Fatalf("Select renders for %v, want %v", got, sqlite.Dialect{})
	}
}

// TestResidue_EngineExpressionFailsAtBuild pins the one dialect mistake the
// compiler does not see, so it cannot quietly become "caught nowhere": an
// engine expression in another engine's statement builds, and Build refuses
// it with ErrUnsupportedDialect naming the construct.
func TestResidue_EngineExpressionFailsAtBuild(t *testing.T) {
	q, err := sqlite.Select(pg.ArrayAgg(entryID)).From(entryUsers).Build()
	if !errors.Is(err, stmt.ErrUnsupportedDialect) {
		t.Fatalf("got %v, want ErrUnsupportedDialect", err)
	}
	if q.SQL != "" {
		t.Fatalf("a refused statement must carry no SQL, got %q", q.SQL)
	}
}

// TestEntry_DDLIsTheLongFormExactly is the same pin for the DDL constructors:
// each is ddl.New* with the dialect written in, and nothing more.
func TestEntry_DDLIsTheLongFormExactly(t *testing.T) {
	d := sqlite.Dialect{}
	cases := []struct {
		name        string
		short, long func() (ddl.Script, error)
	}{
		{"create",
			func() (ddl.Script, error) {
				return sqlite.CreateTable(entryUsers).IfNotExists().Column(ddl.Column(entryID)).Build()
			},
			func() (ddl.Script, error) {
				return ddl.NewCreateTable(d, entryUsers).IfNotExists().Column(ddl.Column(entryID)).Build()
			}},
		{"alter",
			func() (ddl.Script, error) {
				return sqlite.AlterTable(entryUsers).Action(ddl.DropColumn(entryName)).Build()
			},
			func() (ddl.Script, error) {
				return ddl.NewAlterTable(d, entryUsers).Action(ddl.DropColumn(entryName)).Build()
			}},
		{"drop",
			func() (ddl.Script, error) { return sqlite.DropTable(entryUsers).IfExists().Build() },
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
