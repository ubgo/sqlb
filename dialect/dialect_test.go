// Package dialect_test holds the invariants that make the dialects this module
// ships indistinguishable from one written anywhere else.
package dialect_test

import (
	"go/build"
	"os"
	"path/filepath"
	"testing"

	"github.com/ubgo/sqlb/dialect/clickhouse"
	"github.com/ubgo/sqlb/dialect/postgres"
	"github.com/ubgo/sqlb/dialect/sqlite"
	"github.com/ubgo/sqlb/render"
)

// allowedImport is the only non-stdlib import a dialect package may have. A
// dialect is the render layer's two methods plus optional capabilities; if one
// ever needs a statement, an expression or an engine's constructs, the layering
// has been inverted rather than extended.
const allowedImport = "github.com/ubgo/sqlb/render"

// TestDialects_ImportOnlyRender is the point of the split. These three are not
// privileged: each imports what an outside dialect can import, and nothing a
// third-party package could not reach. The day one of them reaches up into the
// core for a shortcut, "an outside package can add an engine" stops being true
// and this test is what says so.
func TestDialects_ImportOnlyRender(t *testing.T) {
	// The directories are discovered rather than listed: a list is how a new
	// dialect would escape this test.
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatal(err)
	}
	var dirs []string
	for _, e := range entries {
		if e.IsDir() {
			dirs = append(dirs, e.Name())
		}
	}
	if len(dirs) < 4 {
		t.Fatalf("found %v; the discovery is broken, not the dialects gone", dirs)
	}
	for _, dir := range dirs {
		t.Run(dir, func(t *testing.T) {
			pkg, err := build.ImportDir(filepath.Join(".", dir), 0)
			if err != nil {
				t.Fatal(err)
			}
			for _, imp := range pkg.Imports {
				if !isStdlib(imp) && imp != allowedImport {
					t.Errorf("imports %q; a dialect may import only the standard library and %s", imp, allowedImport)
				}
			}
		})
	}
}

// isStdlib reports whether an import path names a standard library package.
// Standard library paths never contain a dot, because they carry no domain.
func isStdlib(path string) bool {
	for i := 0; i < len(path); i++ {
		if path[i] == '.' {
			return false
		}
		if path[i] == '/' {
			break
		}
	}
	return true
}

// TestDialects_SatisfyRenderDialect pins that each is usable as the interface
// without help from the core, which is what lets one be swapped for a
// community package's dialect at a call site.
func TestDialects_SatisfyRenderDialect(t *testing.T) {
	cases := []struct {
		d            render.Dialect
		name         string
		placeholder1 string
		quoted       string
	}{
		{postgres.Dialect{}, "postgres", "$1", `"id"`},
		{sqlite.Dialect{}, "sqlite", "?", `"id"`},
		{clickhouse.Dialect{}, "clickhouse", "?", "`id`"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := render.DialectName(c.d); got != c.name {
				t.Errorf("DialectName = %q, want %q", got, c.name)
			}
			if got := c.d.Placeholder(1); got != c.placeholder1 {
				t.Errorf("Placeholder(1) = %q, want %q", got, c.placeholder1)
			}
			if got := c.d.QuoteIdent("id"); got != c.quoted {
				t.Errorf("QuoteIdent = %q, want %q", got, c.quoted)
			}
		})
	}
}

// TestClickHouse_DeclinesReturning pins the capability model: a dialect says it
// cannot do something by not implementing the interface, never by implementing
// it and returning false. ClickHouse has no RETURNING, so a write builder must
// be able to discover that by type assertion alone.
func TestClickHouse_DeclinesReturning(t *testing.T) {
	if _, ok := any(clickhouse.Dialect{}).(render.Returning); ok {
		t.Fatal("clickhouse.Dialect satisfies render.Returning; ClickHouse has no RETURNING")
	}
	for _, d := range []render.Dialect{postgres.Dialect{}, sqlite.Dialect{}} {
		if _, ok := d.(render.Returning); !ok {
			t.Errorf("%s does not satisfy render.Returning but supports it", render.DialectName(d))
		}
	}
}

// TestInsertAliasing_IsPostgresOnly pins the other capability the builder asks
// about, for the same reason.
func TestInsertAliasing_IsPostgresOnly(t *testing.T) {
	if _, ok := any(postgres.Dialect{}).(render.InsertAliasing); !ok {
		t.Error("postgres.Dialect must satisfy render.InsertAliasing")
	}
	for _, d := range []render.Dialect{sqlite.Dialect{}, clickhouse.Dialect{}} {
		if _, ok := d.(render.InsertAliasing); ok {
			t.Errorf("%s satisfies render.InsertAliasing; only Postgres accepts INSERT INTO t AS alias", render.DialectName(d))
		}
	}
}
