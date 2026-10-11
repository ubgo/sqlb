package sqlbtest

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"

	"github.com/ubgo/sqlb"
)

// ZeroConfig describes one package's zero-value check: the package whose
// exported struct types are checked, the dialect its clauses render under,
// and what to do with each type.
type ZeroConfig struct {
	// Dir is the package's source directory, read to find every exported
	// struct type, so a type added later is checked or fails the gate.
	Dir string
	// Dialect is what a clause's WriteSQL renders under.
	Dialect sqlb.Dialect
	// Values holds the zero value of every type with a Build or WriteSQL
	// method, as T{} or &T{}; a generic type is listed instantiated, since
	// Go cannot instantiate one from its name. The element is any because
	// the list holds values of unrelated types, which is what the check
	// inspects by reflection; no generic type can name that set.
	Values []any
	// Probes holds, by type name, a check for a type whose zero value is not
	// rendered by itself: an object value passed to its constructor, a column
	// definition placed in a table. A probe returns the error Build returned.
	Probes map[string]func() error
	// Controls holds, for every probe, the same construct built with a real
	// value, which must succeed. Without it a probe that fails for a reason of
	// its own, a wrong argument in the test, passes and proves nothing.
	Controls map[string]func() error
	// Exempt names a type that needs no probe and says why: a value that
	// renders nothing and reaches no Build with its zero value unguarded.
	Exempt map[string]string
}

// ZeroValues checks that no zero value of an exported type in a package can
// make Build panic or succeed.
//
// Every exported builder, statement and clause here is a struct, so a caller
// can write T{} as well as use its constructor, and a struct literal skips
// whatever the constructor guaranteed. pg.ForeignKeyClause{} indexed its first
// column pair and panicked inside Build until 2026-09-22, and so did
// pg.DropColumnClause{}; nothing had constructed either one without its
// constructor. So each Value is built, a clause by calling WriteSQL on inline
// Args, a statement by calling Build, each under recover, and each must fail
// with an error rather than panic or render. A type that has neither method
// must be in Probes or Exempt, and the source is scanned for every exported
// struct type, so a new one fails the check until someone decides which of
// the three it is. The scan fails closed: finding no types is an error.
func ZeroValues(t testing.TB, cfg ZeroConfig) {
	t.Helper()
	declared, err := exportedStructs(cfg.Dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(declared) == 0 {
		t.Fatalf("sqlbtest: no exported struct types in %s; is Dir right?", cfg.Dir)
	}
	seen := map[string]string{}
	claim := func(name, how string) {
		if prev, ok := seen[name]; ok {
			t.Errorf("sqlbtest: %s is listed twice, in %s and %s", name, prev, how)
		}
		seen[name] = how
	}
	for _, v := range cfg.Values {
		name := zeroTypeName(v)
		claim(name, "Values")
		if err := zeroRender(v, cfg.Dialect); err != nil {
			t.Error(err)
		}
	}
	for name, probe := range cfg.Probes {
		claim(name, "Probes")
		if err := runProbe(name, probe); err != nil {
			t.Error(err)
		}
		control, ok := cfg.Controls[name]
		if !ok {
			t.Errorf("sqlbtest: the probe for %s has no control, so its failure proves nothing", name)
			continue
		}
		if err := runControl(name, control); err != nil {
			t.Error(err)
		}
	}
	for name := range cfg.Controls {
		if _, ok := cfg.Probes[name]; !ok {
			t.Errorf("sqlbtest: a control for %s has no probe", name)
		}
	}
	for name, why := range cfg.Exempt {
		claim(name, "Exempt")
		if strings.TrimSpace(why) == "" {
			t.Errorf("sqlbtest: %s is exempt with no reason", name)
		}
	}
	for _, name := range declared {
		if _, ok := seen[name]; !ok {
			t.Errorf("sqlbtest: %s is an exported struct with no zero-value check: add it to Values, Probes or Exempt", name)
		}
	}
	decl := map[string]bool{}
	for _, n := range declared {
		decl[n] = true
	}
	for name, how := range seen {
		if !decl[name] {
			t.Errorf("sqlbtest: %s in %s is not an exported struct in %s", name, how, cfg.Dir)
		}
	}
}

// exportedStructs lists the exported struct types a package's non-test files
// declare, sorted.
func exportedStructs(dir string) ([]string, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	fset := token.NewFileSet()
	var out []string
	for _, e := range entries {
		n := e.Name()
		if e.IsDir() || !strings.HasSuffix(n, ".go") || strings.HasSuffix(n, "_test.go") {
			continue
		}
		f, err := parser.ParseFile(fset, filepath.Join(dir, n), nil, parser.SkipObjectResolution)
		if err != nil {
			return nil, err
		}
		for _, d := range f.Decls {
			g, ok := d.(*ast.GenDecl)
			if !ok || g.Tok != token.TYPE {
				continue
			}
			for _, s := range g.Specs {
				ts, ok := s.(*ast.TypeSpec)
				if !ok || !ts.Name.IsExported() || ts.Assign.IsValid() {
					continue
				}
				if _, ok := ts.Type.(*ast.StructType); ok {
					out = append(out, ts.Name.Name)
				}
			}
		}
	}
	sort.Strings(out)
	return out, nil
}

// zeroTypeName is v's type name without its package, pointer or type
// arguments, the spelling the source scan finds.
func zeroTypeName(v any) string {
	rt := reflect.TypeOf(v)
	for rt.Kind() == reflect.Pointer {
		rt = rt.Elem()
	}
	name, _, _ := strings.Cut(rt.Name(), "[")
	return name
}

// renderMethods are the methods a clause renders through, each taking a
// builder and the statement's Args.
var renderMethods = []string{"WriteSQL", "WriteAction", "WriteConstraint", "WriteIndexElement"}

// zeroRender builds or renders one zero value and reports anything but a
// clean refusal.
func zeroRender(v any, d sqlb.Dialect) (failure error) {
	name := zeroTypeName(v)
	defer func() {
		if r := recover(); r != nil {
			failure = fmt.Errorf("sqlbtest: the zero %s panicked: %v", name, r)
		}
	}()
	rv := reflect.ValueOf(v)
	// Call through a pointer to a copy, so pointer and value receivers are
	// both found and the listed value is never written.
	ptr := reflect.New(reflect.TypeOf(v))
	ptr.Elem().Set(rv)
	target := ptr.Elem()
	if target.Kind() != reflect.Pointer {
		target = ptr
	}
	if m := target.MethodByName("Build"); m.IsValid() && m.Type().NumIn() == 0 && m.Type().NumOut() == 2 {
		out := m.Call(nil)
		if err, _ := out[1].Interface().(error); err == nil {
			return fmt.Errorf("sqlbtest: the zero %s built without an error: %v", name, out[0].Interface())
		}
		return nil
	}
	for _, method := range renderMethods {
		m := target.MethodByName(method)
		if !m.IsValid() || m.Type().NumIn() != 2 {
			continue
		}
		var b strings.Builder
		a := sqlb.NewInlineArgs(d)
		m.Call([]reflect.Value{reflect.ValueOf(&b), reflect.ValueOf(a)})
		if a.Err() == nil {
			return fmt.Errorf("sqlbtest: the zero %s rendered %q without an error", name, b.String())
		}
		return nil
	}
	return fmt.Errorf("sqlbtest: %s has no Build or render method; move it to Probes or Exempt", name)
}

func runProbe(name string, probe func() error) (failure error) {
	defer func() {
		if r := recover(); r != nil {
			failure = fmt.Errorf("sqlbtest: the probe for %s panicked: %v", name, r)
		}
	}()
	if err := probe(); err == nil {
		return fmt.Errorf("sqlbtest: the probe for the zero %s succeeded; a zero value must be refused", name)
	}
	return nil
}

func runControl(name string, control func() error) (failure error) {
	defer func() {
		if r := recover(); r != nil {
			failure = fmt.Errorf("sqlbtest: the control for %s panicked: %v", name, r)
		}
	}()
	if err := control(); err != nil {
		return fmt.Errorf("sqlbtest: the control for %s failed, so its probe tests nothing: %v", name, err)
	}
	return nil
}
