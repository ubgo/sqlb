package vet

import (
	"fmt"

	"golang.org/x/tools/go/analysis"
	"golang.org/x/tools/go/analysis/checker"
	"golang.org/x/tools/go/packages"
)

// TB is the part of testing.TB Check needs, so it is callable from a test
// without importing testing here.
type TB interface {
	Helper()
	Logf(format string, args ...any)
	Errorf(format string, args ...any)
	Fatalf(format string, args ...any)
}

// Finding is one diagnostic, positioned. A Warning is reported but does not
// fail a check: it names something worth a look that the code may be right
// to do, such as a column the database has that nothing declares.
type Finding struct {
	Pos      string
	Analyzer string
	Message  string
	Warning  bool
}

// Failing reports whether any finding is an error rather than a warning,
// which is what decides a process exit code and a test's outcome.
func Failing(fs []Finding) bool {
	for _, f := range fs {
		if !f.Warning {
			return true
		}
	}
	return false
}

// Run loads the packages the patterns name and runs every analyzer over
// them, returning the findings. It is what Check and cmd/sqlb share.
func Run(dir string, patterns ...string) ([]Finding, error) {
	return RunWith(dir, Analyzers(), patterns...)
}

// RunWith is Run over a chosen set of analyzers, which is how cmd/sqlb adds
// the escape-hatch listing on request without making it a default.
func RunWith(dir string, analyzers []*analysis.Analyzer, patterns ...string) ([]Finding, error) {
	cfg := &packages.Config{
		// The checker needs syntax and type information for the dependencies
		// too, not only the roots, which is what LoadAllSyntax spells out.
		Mode:  packages.LoadAllSyntax | packages.NeedModule,
		Dir:   dir,
		Tests: true,
	}
	pkgs, err := packages.Load(cfg, patterns...)
	if err != nil {
		return nil, fmt.Errorf("sqlb vet: load: %w", err)
	}
	if n := packages.PrintErrors(pkgs); n > 0 {
		return nil, fmt.Errorf("sqlb vet: %d package(s) had errors", n)
	}
	graph, err := checker.Analyze(analyzers, pkgs, nil)
	if err != nil {
		return nil, fmt.Errorf("sqlb vet: analyze: %w", err)
	}
	// A package and its test variant are two actions over the same files,
	// so a finding in a non-test file arrives twice; keep one.
	seen := map[Finding]bool{}
	var out []Finding
	for act := range graph.All() {
		if act.Err != nil {
			return nil, fmt.Errorf("sqlb vet: %s on %s: %w", act.Analyzer.Name, act.Package.PkgPath, act.Err)
		}
		for _, d := range act.Diagnostics {
			f := Finding{
				Pos:      act.Package.Fset.Position(d.Pos).String(),
				Analyzer: act.Analyzer.Name,
				Message:  d.Message,
				Warning:  act.Analyzer == Escapes,
			}
			if seen[f] {
				continue
			}
			seen[f] = true
			out = append(out, f)
		}
	}
	return out, nil
}

// Check runs the analyzers over the patterns from inside a test and fails it
// on any finding. An application includes the check at build time with one
// test function and no binary installed:
//
//	func TestSQL(t *testing.T) { vet.Check(t, "./...") }
func Check(t TB, patterns ...string) {
	t.Helper()
	findings, err := Run(".", patterns...)
	if err != nil {
		t.Fatalf("%v", err)
	}
	for _, f := range findings {
		if f.Warning {
			t.Logf("%s: %s: warning: %s", f.Pos, f.Analyzer, f.Message)
			continue
		}
		t.Errorf("%s: %s: %s", f.Pos, f.Analyzer, f.Message)
	}
}
