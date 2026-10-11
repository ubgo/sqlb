package exec_test

import (
	"go/build"
	"strings"
	"testing"
)

// allowed is what the execution layer may import. It needs the statement layer
// for Query and nothing else from this module: it must not know about an
// engine, and it must not reach back to the root.
var allowed = map[string]bool{
	"github.com/ubgo/sqlb/stmt": true,
}

// TestExec_ImportsOnlyStmt pins that execution stays independent of how results
// are delivered. The moment this package imports a result strategy, the split
// that made a second strategy cheap has been undone.
func TestExec_ImportsOnlyStmt(t *testing.T) {
	pkg, err := build.ImportDir(".", 0)
	if err != nil {
		t.Fatal(err)
	}
	for _, imp := range pkg.Imports {
		if strings.Contains(imp, ".") && !allowed[imp] {
			t.Errorf("exec imports %q; it may import only the standard library and stmt", imp)
		}
	}
}
