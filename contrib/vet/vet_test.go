package vet_test

import (
	"strings"
	"testing"

	"github.com/ubgo/sqlb/contrib/vet"
)

// The probe packages under internal/probe are real packages that import sqlb,
// so the analyzers are exercised the way a consumer's code exercises them —
// through go/packages with type information — rather than on a fixture the
// analyzer cannot resolve.

func findings(t *testing.T, pattern string) []vet.Finding {
	t.Helper()
	out, err := vet.Run(".", pattern)
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func TestClean_ReportsNothing(t *testing.T) {
	if got := findings(t, "./internal/probe/clean"); len(got) != 0 {
		t.Fatalf("clean package reported: %v", got)
	}
}

func TestDialect_ReportsAnEngineExpressionInAForeignStatement(t *testing.T) {
	got := findings(t, "./internal/probe/mixed")
	// Three: one statement started by sqlite.Select, one by stmt.NewSelect
	// given sqlite.Dialect{}, whose type is an alias and so must be resolved,
	// and a MySQL expression in a Postgres statement.
	if len(got) != 3 {
		t.Fatalf("want three findings, got %v", got)
	}
	var mysqlSeen int
	for _, f := range got {
		switch {
		case f.Analyzer == "sqlbdialect" && strings.Contains(f.Message, "postgres construct ArrayAgg in a statement fixed to sqlite"):
		case f.Analyzer == "sqlbdialect" && strings.Contains(f.Message, "mysql construct UUID in a statement fixed to postgres"):
			mysqlSeen++
		default:
			t.Fatalf("unexpected finding: %+v", f)
		}
	}
	if mysqlSeen != 1 {
		t.Fatalf("the MySQL construct was not reported: %v", got)
	}
}

func TestDiscarded_ReportsADroppedBuilderResult(t *testing.T) {
	got := findings(t, "./internal/probe/discard")
	// Three: a core Select, a DDL statement, and a Merge, which an engine
	// package defines and which the analyzer recognises by its Build method
	// rather than by name.
	if len(got) != 3 {
		t.Fatalf("want three findings, got %v", got)
	}
	want := []string{"Select builder call is discarded", "CreateTableReady builder call is discarded", "Merge builder call is discarded"}
	for i, f := range got {
		if f.Analyzer != "sqlbdiscarded" || !strings.Contains(f.Message, want[i]) {
			t.Fatalf("unexpected finding %d: %+v", i, f)
		}
	}
}

// recorder is the TB Check needs, capturing what it would have failed with.
type recorder struct {
	errors []string
	logs   []string
	fatal  string
}

func (r *recorder) Helper() {}
func (r *recorder) Logf(f string, a ...any) {
	r.logs = append(r.logs, strings.TrimSpace(sprintf(f, a...)))
}
func (r *recorder) Errorf(f string, a ...any) {
	r.errors = append(r.errors, strings.TrimSpace(sprintf(f, a...)))
}
func (r *recorder) Fatalf(f string, a ...any) { r.fatal = sprintf(f, a...) }

func TestCheck_FailsTheTestPerFinding(t *testing.T) {
	var r recorder
	vet.Check(&r, "./internal/probe/mixed", "./internal/probe/discard")
	if r.fatal != "" {
		t.Fatal(r.fatal)
	}
	// One error per finding: three in mixed (the direct form, the alias form
	// and the MySQL one) and three in discard (a core, a DDL and an engine
	// statement).
	if len(r.errors) != 6 {
		t.Fatalf("want six errors, got %v", r.errors)
	}
}
