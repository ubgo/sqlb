package pg_test

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/ubgo/sqlb"
	"github.com/ubgo/sqlb/dialect/postgres"
)

// The shape rules for an operator and a function name, walked against every
// constant this repository actually declares rather than a sample.
//
// The rules live in expr, because an operator is SQL vocabulary and render
// must stay free of it. They are applied at expr.Binary, expr.Compare,
// expr.Fn, expr.Aggregate and the three engine doors in stmt. What they must
// never do is refuse a constant the module ships, and a typed-out sample would
// not notice when somebody adds one — so this reads the source of every layer
// and every engine package, the way TestEngineBuilders_AreAllCovered does, and
// fails on the first constant the rule would reject.
//
// It lives here rather than in expr or stmt because those are core layers and
// may not import an engine package; a test in pg already reads the sibling
// engines' source as text, which is a file read and not an import.

// opConstant matches an operator constant, capturing its name and its value:
// opFoo or OpFoo, possibly with a named type between the two.
var opConstant = regexp.MustCompile(`(?m)^\s*((?:op|Op)[A-Za-z0-9]*)\s*(?:[A-Za-z]+\s*)?=\s*"([^"]*)"`)

// fnConstant matches a function-name constant the same way.
var fnConstant = regexp.MustCompile(`(?m)^\s*(fn[A-Za-z0-9]*)\s*(?:[A-Za-z]+\s*)?=\s*"([^"]*)"`)

// isOptionName reports whether a constant named like an operator is in fact a
// DDL option name. optStorage, optCheckOption and their family are written in
// a "NAME = value" list and never reach an operator door; an earlier run of
// this scan reported ten of them as false refusals, which is how the filter
// came to be here rather than in the regex.
func isOptionName(name string) bool { return strings.HasPrefix(name, "opt") }

// shapeDirs is every package that declares an operator or a function name.
var shapeDirs = []string{
	"../expr", "../stmt", ".", "../sqlite", "../mysql", "../ch",
	"vector", "trgm", "fuzzy", "ltree", "crypto", "earth", "unaccent", "postgis",
}

// TestOperatorShape_AdmitsEveryConstantTheModuleShips fails when the shape
// rule would refuse an operator or a function name this module declares. It is
// the half of the audit that protects the library from its own check.
func TestOperatorShape_AdmitsEveryConstantTheModuleShips(t *testing.T) {
	ops, fns := scanConstants(t)
	if len(ops) < 50 || len(fns) < 100 {
		t.Fatalf("the scan found %d operators and %d function names, which is too few to be a real scan", len(ops), len(fns))
	}
	for _, op := range ops {
		if !operatorBuilds(t, op) {
			t.Errorf("the operator %q is declared by this module and the shape rule refuses it", op)
		}
	}
	for _, name := range fns {
		// A handful of "fn" constants are operators rather than names:
		// pg's fnTSVectorConcatOp is "||" and goes to binop. Either shape is
		// acceptable for them, which is what this allows.
		if !functionNameBuilds(t, name) && !operatorBuilds(t, name) {
			t.Errorf("the function name %q is declared by this module and the shape rule refuses it", name)
		}
	}
	t.Logf("shape rule admits %d operators and %d function names", len(ops), len(fns))
}

// TestOperatorShape_RefusesAPayload is the other half at the same door: the
// rule is only worth having if it refuses what it exists for.
func TestOperatorShape_RefusesAPayload(t *testing.T) {
	for _, payload := range []string{
		`= ANY (SELECT 1); DROP TABLE users; --`,
		`"); DROP TABLE users; --`,
		`f(1); DROP TABLE users; --`,
		`&& ) OR true --`,
		"",
	} {
		if operatorBuilds(t, payload) {
			t.Errorf("the operator shape accepted %q", payload)
		}
		if functionNameBuilds(t, payload) {
			t.Errorf("the function-name shape accepted %q", payload)
		}
	}
}

// operatorBuilds reports whether a statement using op as an operator builds.
// It goes through the real door rather than calling a predicate, so the test
// measures what a caller would get.
func operatorBuilds(t *testing.T, op string) bool {
	t.Helper()
	_, err := sqlb.NewSelect(postgres.Dialect{}).
		Exprs(zID).From(zt).
		Where(sqlb.EngineBinPred(sqlb.DialectPostgres, "shape.probe", op, zID, zID)).
		Build()
	return err == nil
}

// functionNameBuilds reports whether a statement calling name builds.
func functionNameBuilds(t *testing.T, name string) bool {
	t.Helper()
	_, err := sqlb.NewSelect(postgres.Dialect{}).
		Exprs(sqlb.EngineFn[int64](sqlb.DialectPostgres, "shape.probe", name, zID)).
		From(zt).Build()
	return err == nil
}

// scanConstants reads every package that declares operators or function names
// and returns both sets. It fails closed: a scan that matches nothing is a
// broken scan, not a clean repository.
func scanConstants(t *testing.T) (ops, fns []string) {
	t.Helper()
	seenOp, seenFn := map[string]bool{}, map[string]bool{}
	files := 0
	for _, dir := range shapeDirs {
		paths, err := filepath.Glob(filepath.Join(dir, "*.go"))
		if err != nil {
			t.Fatal(err)
		}
		for _, p := range paths {
			if strings.HasSuffix(p, "_test.go") || strings.HasSuffix(p, "shape.go") {
				continue
			}
			src, err := os.ReadFile(p)
			if err != nil {
				t.Fatal(err)
			}
			files++
			for _, m := range opConstant.FindAllStringSubmatch(string(src), -1) {
				if isOptionName(m[1]) || seenOp[m[2]] {
					continue
				}
				seenOp[m[2]] = true
				ops = append(ops, m[2])
			}
			for _, m := range fnConstant.FindAllStringSubmatch(string(src), -1) {
				if seenFn[m[2]] {
					continue
				}
				seenFn[m[2]] = true
				fns = append(fns, m[2])
			}
		}
	}
	if files == 0 {
		t.Fatal("the scan read no files at all; the directory list is wrong")
	}
	return ops, fns
}
