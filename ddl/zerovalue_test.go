package ddl_test

import (
	"testing"

	"github.com/ubgo/sqlb/ddl"
	"github.com/ubgo/sqlb/dialect/postgres"
	"github.com/ubgo/sqlb/expr"
	"github.com/ubgo/sqlb/sqlbtest"
)

// TestZeroValues_AreRefused builds the zero value of every exported ddl type
// and fails on a panic or a clean build; see sqlbtest.ZeroValues.
func TestZeroValues_AreRefused(t *testing.T) {
	d := postgres.Dialect{}
	sqlbtest.ZeroValues(t, sqlbtest.ZeroConfig{
		Dir:     ".",
		Dialect: d,
		Values: []any{
			ddl.CreateTableReady[postgres.Dialect]{},
			ddl.AlterTableReady[postgres.Dialect]{},
			ddl.DropTable[postgres.Dialect]{},
		},
		Probes: map[string]func() error{
			"CreateTable": func() error { return failsDDL((&ddl.CreateTable[postgres.Dialect]{}).Column(ddl.Column(id)).Build()) },
			"AlterTable": func() error {
				return failsDDL((&ddl.AlterTable[postgres.Dialect]{}).Action(ddl.DropColumn(email)).Build())
			},
			"ColumnDef": func() error { return failsDDL(ddl.NewCreateTable(d, users).Column(ddl.ColumnDef[int64]{}).Build()) },
			"TableConstraint": func() error {
				return failsDDL(ddl.NewCreateTable(d, users).Column(ddl.Column(id)).Constraint(ddl.Check(ddl.TableConstraint{}, expr.Greater(id, int64(0)))).Build())
			},
		},
		Controls: map[string]func() error{
			"CreateTable": func() error { return failsDDL(ddl.NewCreateTable(d, users).Column(ddl.Column(id)).Build()) },
			"AlterTable":  func() error { return failsDDL(ddl.NewAlterTable(d, users).Action(ddl.DropColumn(email)).Build()) },
			"ColumnDef":   func() error { return failsDDL(ddl.NewCreateTable(d, users).Column(ddl.Column(id)).Build()) },
			"TableConstraint": func() error {
				return failsDDL(ddl.NewCreateTable(d, users).Column(ddl.Column(id)).Constraint(ddl.Check(ddl.TableConstraint{Table: users, Name: "c"}, expr.Greater(id, int64(0)))).Build())
			},
		},
		Exempt: map[string]string{
			"ColumnFacts":  "a report ColumnDef returns; nothing takes one back",
			"ColumnExtras": "what a statement passes to WriteColumn; a caller never builds one",
			"Statement":    "a rendered statement, text plus flags; a zero one is an empty statement a runner would send, and every builder here produces its own",
		},
	})
}

func failsDDL(_ ddl.Script, err error) error { return err }
