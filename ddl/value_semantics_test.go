package ddl_test

import (
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"

	"github.com/ubgo/sqlb/ddl"
	"github.com/ubgo/sqlb/dialect/postgres"
	"github.com/ubgo/sqlb/render"
)

// builder is anything a DDL constructor or method returns that can be built.
type builder interface {
	Build() (ddl.Script, error)
}

type pgCreate = *ddl.CreateTable[postgres.Dialect]
type pgCreateReady = *ddl.CreateTableReady[postgres.Dialect]
type pgAlter = *ddl.AlterTable[postgres.Dialect]
type pgAlterReady = *ddl.AlterTableReady[postgres.Dialect]
type pgDrop = *ddl.DropTable[postgres.Dialect]

var (
	joinable = ddl.NewAlterClause[postgres.Dialect](func(*render.Args) string { return "X" })
	element  = ddl.NewTableClause[postgres.Dialect](ddl.PositionElement, func(*render.Args) string { return "Y" })
	cascade  = ddl.NewDropClause[postgres.Dialect](func(*render.Args) string { return "CASCADE" })
)

func baseCreate() pgCreate { return ddl.NewCreateTable(postgres.Dialect{}, users) }
func baseReady() pgCreateReady {
	return ddl.NewCreateTable(postgres.Dialect{}, users).Column(ddl.Column(id))
}
func baseAlter() pgAlter { return ddl.NewAlterTable(postgres.Dialect{}, users) }
func baseAlterReady() pgAlterReady {
	return ddl.NewAlterTable(postgres.Dialect{}, users).Action(ddl.AddColumn(ddl.Column(phone)))
}
func baseDrop() pgDrop { return ddl.NewDropTable(postgres.Dialect{}, users) }

// ddlMethods is every method on every DDL statement type that returns a
// statement, keyed Type.Method, which TestDDLBuilders_AreAllCovered checks
// against the source. Incomplete forms have no Build, so a derivation is
// compared through the complete form it leads to.
var ddlMethods = map[string]struct {
	before func() builder // the receiver's state, built
	derive func() (receiverAfter, derived builder)
}{
	"CreateTable.IfNotExists": {
		func() builder { return baseCreate().Column(ddl.Column(id)) },
		func() (builder, builder) {
			b := baseCreate()
			d := b.IfNotExists()
			return b.Column(ddl.Column(id)), d.Column(ddl.Column(id))
		},
	},
	"CreateTable.Constraint": {
		func() builder { return baseCreate().Column(ddl.Column(id)) },
		func() (builder, builder) {
			b := baseCreate()
			d := b.Constraint(ddl.OneOf(ddl.TableConstraint{Table: stat.Table(), Name: "k"}, stat, "a"))
			return b.Column(ddl.Column(id)), d.Column(ddl.Column(id), ddl.Column(stat))
		},
	},
	"CreateTable.Clause": {
		func() builder { return baseCreate().Column(ddl.Column(id)) },
		func() (builder, builder) {
			b := baseCreate()
			d := b.Clause(element)
			return b.Column(ddl.Column(id)), d.Column(ddl.Column(id))
		},
	},
	"CreateTable.Column": {
		func() builder { return baseCreate().Column(ddl.Column(id)) },
		func() (builder, builder) {
			b := baseCreate()
			_ = b.Column(ddl.Column(email))
			return b.Column(ddl.Column(id)), b.Column(ddl.Column(id), ddl.Column(email))
		},
	},
	"CreateTableReady.IfNotExists": {
		func() builder { return baseReady() },
		func() (builder, builder) { b := baseReady(); return b, b.IfNotExists() },
	},
	"CreateTableReady.Column": {
		func() builder { return baseReady() },
		func() (builder, builder) { b := baseReady(); return b, b.Column(ddl.Column(email)) },
	},
	"CreateTableReady.Constraint": {
		func() builder { return baseReady().Column(ddl.Column(stat)) },
		func() (builder, builder) {
			b := baseReady().Column(ddl.Column(stat))
			return b, b.Constraint(ddl.OneOf(ddl.TableConstraint{Table: stat.Table(), Name: "k"}, stat, "a"))
		},
	},
	"CreateTableReady.Clause": {
		func() builder { return baseReady() },
		func() (builder, builder) { b := baseReady(); return b, b.Clause(element) },
	},
	"AlterTable.Action": {
		func() builder { return baseAlter().Action(ddl.AddColumn(ddl.Column(phone))) },
		func() (builder, builder) {
			b := baseAlter()
			d := b.Action(ddl.DropColumn(email))
			return b.Action(ddl.AddColumn(ddl.Column(phone))), d
		},
	},
	"AlterTable.Clause": {
		func() builder { return baseAlter().Action(ddl.AddColumn(ddl.Column(phone))) },
		func() (builder, builder) {
			b := baseAlter()
			d := b.Clause(joinable)
			return b.Action(ddl.AddColumn(ddl.Column(phone))), d
		},
	},
	"AlterTableReady.Action": {
		func() builder { return baseAlterReady() },
		func() (builder, builder) { b := baseAlterReady(); return b, b.Action(ddl.DropColumn(email)) },
	},
	"AlterTableReady.Clause": {
		func() builder { return baseAlterReady() },
		func() (builder, builder) { b := baseAlterReady(); return b, b.Clause(joinable) },
	},
	"DropTable.IfExists": {
		func() builder { return baseDrop() },
		func() (builder, builder) { b := baseDrop(); return b, b.IfExists() },
	},
	"DropTable.Clause": {
		func() builder { return baseDrop() },
		func() (builder, builder) { b := baseDrop(); return b, b.Clause(cascade) },
	},
}

func sqlOf(t *testing.T, b builder) string {
	t.Helper()
	s, err := b.Build()
	if err != nil {
		t.Fatal(err)
	}
	return strings.Join(s.SQL(), ";")
}

// TestDDLBuilders_MethodsDoNotWriteToTheirReceiver is the builder invariant
// for DDL: every method returns a derived statement and leaves its receiver
// alone, so a table definition shared by two migrations cannot be changed
// through either.
func TestDDLBuilders_MethodsDoNotWriteToTheirReceiver(t *testing.T) {
	for name, m := range ddlMethods {
		t.Run(name, func(t *testing.T) {
			want := sqlOf(t, m.before())
			after, derived := m.derive()
			if got := sqlOf(t, after); got != want {
				t.Errorf("the method wrote to its receiver:\nbefore %s\nafter  %s", want, got)
			}
			if sqlOf(t, derived) == want {
				t.Errorf("the derived statement did not change: %s", want)
			}
		})
	}
}

// ddlMethod finds a method on a DDL statement type that returns a statement.
var ddlMethod = regexp.MustCompile(`(?m)^func \(\w+ \*(CreateTable|CreateTableReady|AlterTable|AlterTableReady|DropTable)\[D\]\) ([A-Z]\w*)\([^)]*\) \*\w+\[D\]`)

// TestDDLBuilders_AreAllCovered fails when a DDL statement type gains a method
// the test above does not walk, which is how three engine builders went
// unchecked for as long as they did. It fails closed on an empty scan.
func TestDDLBuilders_AreAllCovered(t *testing.T) {
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	found := map[string]bool{}
	for _, f := range files {
		if strings.HasSuffix(f, "_test.go") {
			continue
		}
		src, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		for _, m := range ddlMethod.FindAllStringSubmatch(string(src), -1) {
			found[m[1]+"."+m[2]] = true
		}
	}
	if len(found) == 0 {
		t.Fatal("found no DDL builder method; the matcher is broken")
	}
	var missing []string
	for k := range found {
		if _, ok := ddlMethods[k]; !ok {
			missing = append(missing, k)
		}
	}
	sort.Strings(missing)
	if len(missing) > 0 {
		t.Errorf("DDL builder methods with no receiver-write coverage: %v", missing)
	}
}
