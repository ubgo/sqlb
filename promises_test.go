package sqlb_test

import (
	"errors"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"

	"github.com/ubgo/sqlb"
	"github.com/ubgo/sqlb/dialect/clickhouse"
	"github.com/ubgo/sqlb/dialect/mysql"
	"github.com/ubgo/sqlb/dialect/postgres"
	"github.com/ubgo/sqlb/dialect/sqlite"
	"github.com/ubgo/sqlb/pg"
)

// TestPromise_BoundValuesStayOutOfSQL drives the public entrypoints, including
// empty and hostile input, through every shipped query dialect. Exact argument
// readback prevents a renderer that simply drops the input from passing.
// ds:def id=bound-values-3t7cnq5d
// promise:parameter-binding
func TestPromise_BoundValuesStayOutOfSQL(t *testing.T) {
	table := sqlb.Table{Name: "promise_values", Alias: "v"}
	col := sqlb.Col[string](table, "value")
	for _, d := range []sqlb.Dialect{postgres.Dialect{}, sqlite.Dialect{}, mysql.Dialect{}, clickhouse.Dialect{}} {
		t.Run(sqlb.DialectName(d), func(t *testing.T) {
			for _, value := range []string{"", "'; DROP TABLE promise_values; --", "a\x00b", "日本語\\'\""} {
				for name, build := range map[string]func() (sqlb.Query, error){
					"Param": func() (sqlb.Query, error) { return sqlb.NewSelect(d).Exprs(sqlb.Param(value)).From(table).Build() },
					"Eq": func() (sqlb.Query, error) {
						return sqlb.NewSelect(d).Exprs(col).From(table).Where(sqlb.Eq(col, value)).Build()
					},
					"In": func() (sqlb.Query, error) {
						return sqlb.NewSelect(d).Exprs(col).From(table).Where(sqlb.In(col, value)).Build()
					},
					"Set": func() (sqlb.Query, error) { return sqlb.NewInsert(d).Into(table).Row(sqlb.Set(col, value)).Build() },
				} {
					t.Run(name+"/"+strconv.Quote(value), func(t *testing.T) {
						q, err := build()
						if err != nil {
							t.Fatal(err)
						}
						if !reflect.DeepEqual(q.Args, []any{value}) {
							t.Fatalf("bound argument changed: %#v", q.Args)
						}
						if value != "" && strings.Contains(q.SQL, value) {
							t.Fatalf("request value reached SQL: %s", q.SQL)
						}
						again, err := build()
						if err != nil || !reflect.DeepEqual(q, again) {
							t.Fatalf("repeat build changed: %#v, %v", again, err)
						}
					})
				}
			}
		})
	}
}

// TestPromise_ValidationReturnsNoPartialQuery checks error identity and the
// absence of SQL, arguments and targets after an empty SELECT is rejected.
// ds:def id=build-failure-pwec46e6
// promise:render-errors
func TestPromise_ValidationReturnsNoPartialQuery(t *testing.T) {
	q, err := sqlb.NewSelect(sqlite.Dialect{}).From(sqlb.Table{Name: "promise_values"}).Build()
	if !errors.Is(err, sqlb.ErrNoColumns) {
		t.Fatalf("got %v, want ErrNoColumns", err)
	}
	if q.SQL != "" || len(q.Args) != 0 || len(q.Targets) != 0 {
		t.Fatalf("partial query escaped failure: %#v", q)
	}
}

// productionFiles parses imports and selectors, so multiline or aliased imports
// cannot bypass the architectural checks.
func productionFiles(t *testing.T, dir string, visit func(string, *ast.File)) {
	t.Helper()
	err := filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if d.Name() == ".git" || d.Name() == ".lore" || d.Name() == "contrib" || d.Name() == "testdata" {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		f, err := parser.ParseFile(token.NewFileSet(), path, nil, 0)
		if err != nil {
			return err
		}
		visit(path, f)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

// promise:root-zero-dependencies
// ds:def id=core-boundaries-y4kpz7kr
func TestPromise_CoreHasNoThirdPartyImports(t *testing.T) {
	productionFiles(t, ".", func(path string, f *ast.File) {
		for _, imp := range f.Imports {
			p, _ := strconv.Unquote(imp.Path.Value)
			first, _, _ := strings.Cut(p, "/")
			if strings.Contains(first, ".") && (!strings.HasPrefix(p, "github.com/ubgo/sqlb/") && p != "github.com/ubgo/sqlb" || strings.HasPrefix(p, "github.com/ubgo/sqlb/contrib/")) {
				t.Errorf("core imports third party %s in %s", p, path)
			}
		}
	})
}

// promise:connection-ownership
func TestPromise_CoreDoesNotOpenConnections(t *testing.T) {
	productionFiles(t, ".", func(path string, f *ast.File) {
		aliases := map[string]bool{}
		for _, imp := range f.Imports {
			p, _ := strconv.Unquote(imp.Path.Value)
			if p == "database/sql" {
				alias := "sql"
				if imp.Name != nil {
					alias = imp.Name.Name
				}
				if alias == "." {
					t.Errorf("dot sql import defeats connection ownership check: %s", path)
				}
				aliases[alias] = true
			}
		}
		ast.Inspect(f, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}
			sel, ok := call.Fun.(*ast.SelectorExpr)
			if !ok {
				return true
			}
			if sel.Sel.Name == "Begin" || sel.Sel.Name == "BeginTx" {
				t.Errorf("core starts a transaction in %s", path)
			}
			id, ok := sel.X.(*ast.Ident)
			if ok && aliases[id.Name] && (sel.Sel.Name == "Open" || sel.Sel.Name == "OpenDB") {
				t.Errorf("core opens a database in %s", path)
			}
			return true
		})
	})
}

// promise:cross-package
// ds:def id=project-purpose-7mzq92jv
func TestPromise_CookbookPackageBoundaries(t *testing.T) {
	for _, pkg := range []string{"authlite", "workspace"} {
		productionFiles(t, filepath.Join("examples", "cookbook", pkg), func(path string, f *ast.File) {
			for _, imp := range f.Imports {
				p, _ := strconv.Unquote(imp.Path.Value)
				if strings.Contains(p, "/examples/cookbook/") {
					t.Errorf("independent cookbook package imports sibling %s in %s", p, path)
				}
			}
		})
	}
}

// promise:pg-conflict-target
func TestPromise_ConflictUpdateNeedsTarget(t *testing.T) {
	table := sqlb.Table{Name: "promise_values"}
	col := sqlb.Col[string](table, "value")
	_, err := pg.Insert(table).Row(sqlb.Set(col, "x")).Clause(pg.OnConflict().DoUpdate(sqlb.Set(col, "y")).Clause()).Build()
	if !errors.Is(err, pg.ErrConflictTarget) {
		t.Fatalf("missing conflict target: %v", err)
	}
	if _, err := pg.Insert(table).Row(sqlb.Set(col, "x")).Clause(pg.OnConflict().DoNothing()).Build(); err != nil {
		t.Fatalf("DO NOTHING needs no target: %v", err)
	}
}
