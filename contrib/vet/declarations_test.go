package vet_test

import (
	"strings"
	"testing"

	"github.com/ubgo/sqlb/contrib/vet"
	"github.com/ubgo/sqlb/render"
)

// TestDeclarations_ResolveConstantNames pins that a table or column named by
// a constant, a concatenation or a typed constant is read as the name it
// folds to, and that a table built at run time is reported unresolved, which
// Compare turns into a finding rather than a skip.
func TestDeclarations_ResolveConstantNames(t *testing.T) {
	decls, err := vet.Declarations(".", "./internal/probe/constnames")
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]vet.Declaration{}
	for _, d := range decls.Columns {
		got[d.Schema+"|"+d.Table+"|"+d.Column] = d
	}
	for _, want := range []string{"auth|auth_users|email", "|sessions|id", "||id", "auth|auth_users|"} {
		if _, ok := got[want]; !ok {
			t.Errorf("no declaration %q in %v", want, got)
		}
	}
	var tables []string
	for _, tl := range decls.Tables {
		tables = append(tables, tl.Schema+"."+tl.Name)
	}
	if len(decls.Columns) != 4 {
		t.Errorf("want exactly the four sqlb Col calls, got %d: %v", len(decls.Columns), decls.Columns)
	}
	if strings.Join(tables, ",") != "auth.auth_users,.sessions,.partly" {
		t.Errorf("tables %v", tables)
	}
	findings := vet.Compare(render.DialectSQLite, vet.Declared{Columns: []vet.Declaration{got["||id"]}}, vet.Catalogue{})
	if len(findings) != 1 || !strings.Contains(findings[0].Message, "table is not a package-level literal") {
		t.Errorf("an unresolved table must be a finding: %v", findings)
	}
	findings = vet.Compare(render.DialectSQLite, vet.Declared{Columns: []vet.Declaration{got["auth|auth_users|"]}}, vet.Catalogue{"auth.auth_users": {"email": {Type: "text"}}})
	if len(findings) != 1 || !strings.Contains(findings[0].Message, "auth.auth_users: a column whose name is not a constant cannot be verified") {
		t.Errorf("a column named by a variable must say so: %v", findings)
	}
}

// TestDeclarations_FailsOnAPackageThatDoesNotLoad pins the two load errors:
// a pattern that matches nothing loadable and a package that does not
// compile, each an error rather than an empty result that reads as clean.
func TestDeclarations_FailsOnAPackageThatDoesNotLoad(t *testing.T) {
	if _, err := vet.Declarations(".", "./internal/probe/nosuchpackage"); err == nil {
		t.Error("a missing package loaded")
	}
	if _, err := vet.Declarations(t.TempDir()+"/does-not-exist", "./..."); err == nil {
		t.Error("a directory that does not exist loaded")
	}
	if _, err := vet.Declarations(t.TempDir(), "./..."); err == nil {
		t.Error("a directory with no module loaded")
	}
}
