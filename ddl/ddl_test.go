package ddl_test

import (
	"go/build"
	"strings"
	"testing"

	"github.com/ubgo/sqlb/ddl"
)

// allowed is every module package ddl may import: the layers below it.
var allowed = map[string]bool{
	"github.com/ubgo/sqlb/render": true,
	"github.com/ubgo/sqlb/expr":   true,
	"github.com/ubgo/sqlb/stmt":   true,
	"github.com/ubgo/sqlb/schema": true,
}

// TestDDL_ImportsOnlyTheLayersBelow is the layering rule for this package,
// stated as a test the way every other layer states it. ddl sits above stmt, so
// it may import render, expr, stmt and schema, and nothing that imports it:
// never an engine package, never exec.
func TestDDL_ImportsOnlyTheLayersBelow(t *testing.T) {
	pkg, err := build.ImportDir(".", 0)
	if err != nil {
		t.Fatal(err)
	}
	for _, imp := range pkg.Imports {
		if strings.Contains(imp, ".") && !allowed[imp] {
			t.Errorf("ddl imports %q; it may import only the standard library, render, expr, stmt and schema", imp)
		}
	}
}

// TestScript_SQLKeepsOrder pins that a script's text comes back in the order
// the statements must run.
func TestScript_SQLKeepsOrder(t *testing.T) {
	s := ddl.Script{{SQL: "a", Transactional: true}, {SQL: "b", Transactional: true}}
	if got := strings.Join(s.SQL(), ";"); got != "a;b" {
		t.Fatalf("got %q", got)
	}
}

// TestScript_Transactional pins the question a runner asks: a script may be
// wrapped in one transaction only when no statement refuses one and none is a
// no-op inside one.
func TestScript_Transactional(t *testing.T) {
	for _, tc := range []struct {
		name string
		s    ddl.Script
		want bool
	}{
		{"all transactional", ddl.Script{{SQL: "a", Transactional: true}, {SQL: "b", Transactional: true}}, true},
		{"one CONCURRENTLY", ddl.Script{{SQL: "a", Transactional: true}, {SQL: "b"}}, false},
		{"one PRAGMA foreign_keys", ddl.Script{{SQL: "a", Transactional: true, OutsideTransaction: true}}, false},
		{"empty", ddl.Script{}, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.s.Transactional(); got != tc.want {
				t.Fatalf("got %v, want %v", got, tc.want)
			}
		})
	}
}
