package bind_test

import (
	"go/build"
	"strings"
	"testing"

	"github.com/ubgo/sqlb/bind"
	"github.com/ubgo/sqlb/dialect/postgres"
	"github.com/ubgo/sqlb/expr"
	"github.com/ubgo/sqlb/stmt"
)

// allowed is what this layer may import. It is a result strategy, so it needs
// the statement layer's Binding interface and the expression layer's typed
// columns. It must not import the other strategy, scan, because the two are
// peers.
var allowed = map[string]bool{
	"github.com/ubgo/sqlb/expr":   true,
	"github.com/ubgo/sqlb/render": true,
	"github.com/ubgo/sqlb/stmt":   true,
}

// TestBind_ImportsOnlyTheLayersBelow pins that bind stays a peer of scan.
func TestBind_ImportsOnlyTheLayersBelow(t *testing.T) {
	pkg, err := build.ImportDir(".", 0)
	if err != nil {
		t.Fatal(err)
	}
	for _, imp := range pkg.Imports {
		if strings.Contains(imp, ".") && !allowed[imp] {
			t.Errorf("bind imports %q, which is not one of the layers below it", imp)
		}
	}
}

var (
	users = expr.Table{Name: "app_user", Alias: "u"}
	id    = expr.Col[string](users, "id")
	email = expr.Col[string](users, "email")
)

// TestStmt_DoesNotKnowTheTargetIsAPointer is what moving this layer out bought.
//
// The statement layer sees only stmt.Binding, an interface whose Target is an
// opaque any. Nothing in stmt can dereference it, retain it, or reason about
// aliasing, which is why a second result strategy does not have to fight a
// statement that already took an opinion about Go memory.
func TestStmt_DoesNotKnowTheTargetIsAPointer(t *testing.T) {
	var dst string
	// Bind's declared result is the interface, so the statement layer can only
	// ever see these three methods. Target is an opaque any: nothing in stmt
	// can dereference it, retain it, or reason about aliasing.
	b := bind.Bind(email, &dst)

	if got := b.Target(); got != any(&dst) {
		t.Fatalf("Target did not round-trip the destination")
	}
	if got := b.ColumnName(); got != "email" {
		t.Fatalf("ColumnName = %q, want %q", got, "email")
	}
}

// TestBindings_ComposeAcrossPackages pins the property the library exists for:
// one query scanning into structs owned by several packages that do not import
// each other. The slice is []stmt.Binding, so a package contributing its own
// group never names a concrete type from here.
func TestBindings_ComposeAcrossPackages(t *testing.T) {
	var gotID, gotEmail string
	libraryGroup := []stmt.Binding{bind.Bind(id, &gotID)}
	appGroup := []stmt.Binding{bind.Bind(email, &gotEmail)}

	all := bind.Bindings(libraryGroup, appGroup)
	if len(all) != 2 {
		t.Fatalf("got %d bindings, want 2", len(all))
	}

	q, err := stmt.NewSelect(postgres.Dialect{}).Columns(all...).From(users).Build()
	if err != nil {
		t.Fatal(err)
	}
	if want := `SELECT "u"."id", "u"."email" FROM "app_user" AS "u"`; q.SQL != want {
		t.Fatalf("got  %q\nwant %q", q.SQL, want)
	}

	// The column list and the scan targets come from one slice, in one order.
	// That they cannot drift is the whole idea, so it is asserted rather than
	// assumed.
	if len(q.Targets) != 2 || q.Targets[0] != any(&gotID) || q.Targets[1] != any(&gotEmail) {
		t.Fatal("targets are not in select-list order")
	}
}

// TestTargets_EmptyIsNil pins a detail One and Each depend on: a nil Targets is
// what makes ErrNoTargets reachable, and an empty non-nil slice would pass that
// check while scanning nothing.
func TestTargets_EmptyIsNil(t *testing.T) {
	if bind.Targets(nil) != nil {
		t.Fatal("Targets of an empty list must be nil")
	}
}
