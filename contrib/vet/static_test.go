package vet_test

import (
	"fmt"
	"os"
	"strings"
	"testing"

	"golang.org/x/tools/go/analysis"

	"github.com/ubgo/sqlb/contrib/vet"
)

// TestStatic_ReportsEachMistakeOnceAndTheRightUsesNever runs the static
// analyzers over a probe holding every mistake once, beside the correct form
// of the same construct, and checks the findings against the "want" marks in
// the probe's source: one finding per mark, none elsewhere. The marks are
// read from the file, so adding a line to the probe does not move the test.
func TestStatic_ReportsEachMistakeOnceAndTheRightUsesNever(t *testing.T) {
	src, err := os.ReadFile("internal/probe/static/static.go")
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]int{}
	for i, line := range strings.Split(string(src), "\n") {
		if _, mark, ok := strings.Cut(line, "// want "); ok {
			want[fmt.Sprintf("static.go:%d:%s", i+1, strings.TrimSpace(mark))]++
		}
	}
	if len(want) != 8 {
		t.Fatalf("the probe should carry 8 marks, found %d", len(want))
	}
	got, err := vet.Run(".", "./internal/probe/static")
	if err != nil {
		t.Fatal(err)
	}
	seen := map[string]int{}
	for _, f := range got {
		if f.Warning {
			t.Errorf("static analyzers report errors, got a warning: %v", f)
		}
		parts := strings.Split(f.Pos, ":")
		key := fmt.Sprintf("%s:%s:%s", parts[0][strings.LastIndex(parts[0], "/")+1:], parts[1], f.Analyzer)
		seen[key]++
	}
	for k, n := range want {
		if seen[k] != n {
			t.Errorf("%s: want %d finding(s), got %d", k, n, seen[k])
		}
	}
	for k, n := range seen {
		if want[k] == 0 {
			t.Errorf("unexpected finding %s ×%d", k, n)
		}
	}
}

// TestStatic_CleanProbeIsSilent pins that the probe with only correct code
// yields nothing from any analyzer, including the four static ones.
func TestStatic_CleanProbeIsSilent(t *testing.T) {
	got, err := vet.Run(".", "./internal/probe/clean")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 {
		t.Fatalf("clean probe produced findings: %v", got)
	}
}

// TestEscapes_ListsEveryHatchAsAWarning runs the opt-in listing over the
// static probe and checks it names each hatch there and fails nothing.
func TestEscapes_ListsEveryHatchAsAWarning(t *testing.T) {
	got, err := vet.RunWith(".", []*analysis.Analyzer{vet.Escapes}, "./internal/probe/static")
	if err != nil {
		t.Fatal(err)
	}
	if vet.Failing(got) {
		t.Fatalf("the listing must be warnings only: %v", got)
	}
	counts := map[string]int{}
	for _, f := range got {
		counts[f.Message]++
	}
	want := map[string]int{
		"escape hatch: Literal renders text the compiler does not check": 2,
		"escape hatch: SetOp keyword":                                    2,
		"escape hatch: JoinType keyword":                                 3,
		"escape hatch: Col with a non-constant column name":              1,
	}
	for m, n := range want {
		if counts[m] != n {
			t.Errorf("%q: want %d, got %d", m, n, counts[m])
		}
	}
	if len(got) != 8 {
		t.Errorf("want 8 listings, got %d: %v", len(got), got)
	}
}
