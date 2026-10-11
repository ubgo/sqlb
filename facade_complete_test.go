package sqlb_test

import (
	"go/ast"
	"io/fs"

	"go/parser"
	"go/token"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// facadeLayers are the layers the root promises to re-export in full.
//
// scan and schema are absent on purpose. schema.Table collides with expr.Table
// and scan.One with the binding API's One, so re-exporting them would mean
// renaming somebody's type to fit through a facade. Both are imported directly.
var facadeLayers = []string{"render", "expr", "stmt", "exec", "bind"}

// exportsOf returns every exported package-level name declared in a directory.
func exportsOf(t *testing.T, dir string) map[string]string {
	t.Helper()
	out := map[string]string{}
	fset := token.NewFileSet()
	pkgs, err := parser.ParseDir(fset, dir, func(fi fs.FileInfo) bool {
		return !strings.HasSuffix(fi.Name(), "_test.go")
	}, 0)
	if err != nil {
		t.Fatalf("parsing %s: %v", dir, err)
	}
	for _, pkg := range pkgs {
		for _, f := range pkg.Files {
			for _, d := range f.Decls {
				switch d := d.(type) {
				case *ast.FuncDecl:
					if d.Recv == nil && d.Name.IsExported() {
						out[d.Name.Name] = dir
					}
				case *ast.GenDecl:
					for _, sp := range d.Specs {
						switch sp := sp.(type) {
						case *ast.TypeSpec:
							if sp.Name.IsExported() {
								out[sp.Name.Name] = dir
							}
						case *ast.ValueSpec:
							for _, n := range sp.Names {
								if n.IsExported() {
									out[n.Name] = dir
								}
							}
						}
					}
				}
			}
		}
	}
	return out
}

// TestFacade_ReExportsEveryLayerName is the check that keeps the facade honest
// between edits.
//
// The facade was generated mechanically from the layer sources, which is the
// only sane way to write a hundred and seventy-five aliases without typos. The
// hazard is what happens afterwards: adding one export to expr or stmt leaves
// the root silently missing it, and nobody finds out until a caller upgrades
// and cannot compile. A generator that must be remembered is a contract
// enforced by convention, and those fail quietly.
//
// So the completeness is asserted here rather than trusted. Adding an export to
// a layer fails this test until the root re-exports it, and the failure names
// exactly what is missing.
func TestFacade_ReExportsEveryLayerName(t *testing.T) {
	root := exportsOf(t, ".")

	var missing []string
	for _, layer := range facadeLayers {
		for name, dir := range exportsOf(t, layer) {
			if _, ok := root[name]; !ok {
				missing = append(missing, dir+"."+name)
			}
		}
	}
	sort.Strings(missing)

	if len(missing) > 0 {
		t.Fatalf("the root facade is missing %d name(s) from the layers below:\n\t%s\n\n"+
			"Add an alias for a type, a forwarder for a function, or a new constant for a const.",
			len(missing), strings.Join(missing, "\n\t"))
	}
}

// TestFacade_LayerDirsExist guards the test above from passing vacuously. A
// renamed or moved layer would make exportsOf return an empty map, and a
// completeness check over nothing always succeeds.
func TestFacade_LayerDirsExist(t *testing.T) {
	for _, layer := range facadeLayers {
		if n := len(exportsOf(t, layer)); n == 0 {
			t.Errorf("layer %q exports nothing; the completeness check above would pass vacuously", filepath.Clean(layer))
		}
	}
}
