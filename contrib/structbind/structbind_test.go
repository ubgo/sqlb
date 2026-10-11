package structbind_test

import (
	"errors"
	"testing"

	"github.com/ubgo/sqlb/contrib/structbind"
	"github.com/ubgo/sqlb/dialect/postgres"
	"github.com/ubgo/sqlb/expr"
	"github.com/ubgo/sqlb/stmt"
)

type base struct {
	ID        string `db:"id"`
	CreatedAt string `db:"created_at"`
}

type user struct {
	base          // embedded, so its columns are contributed
	Name   string `db:"name"`
	Email  string // no tag: the column is the field name lowercased
	Secret string `db:"-"` // excluded
	cache  int    // unexported: ignored
}

var users = expr.Table{Name: "app_user", Alias: "u"}

var m = structbind.MustNew[user](users)

// TestTagKey_IsTheConventionalOne pins the tag this package reads.
//
// It is exported and it matters: a struct already annotated for another Go
// database package works here unchanged because the key is the
// same. Changing it would silently stop reading every existing struct, since a
// field with no recognised tag falls back to its lowercased name rather than
// failing.
func TestTagKey_IsTheConventionalOne(t *testing.T) {
	if structbind.TagKey != "db" {
		t.Fatalf("TagKey = %q; changing it silently stops reading structs annotated for other packages", structbind.TagKey)
	}
}

// TestMapping_ReadsTags pins every tag rule at once, because they are the
// package's entire contract and a reader needs them in one place.
func TestMapping_ReadsTags(t *testing.T) {
	got := m.Columns()
	want := []string{"id", "created_at", "name", "email"}

	if len(got) != len(want) {
		t.Fatalf("columns = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("columns = %v, want %v", got, want)
		}
	}

	// The three exclusions, stated rather than implied by absence above.
	var u user
	u.cache = 1 // the unexported field exists and must contribute no column
	for _, c := range got {
		if c == "secret" || c == "cache" {
			t.Errorf("column %q should have been excluded", c)
		}
	}
	_ = u
}

// TestMapping_Bindings is the point of the package: a select list and its scan
// destinations from one declaration.
func TestMapping_Bindings(t *testing.T) {
	var dst user

	q, err := stmt.NewSelect(postgres.Dialect{}).
		Columns(m.Bindings(&dst)...).
		From(users).
		Where(expr.Eq(expr.Col[string](users, "id"), "u1")).
		Build()
	if err != nil {
		t.Fatal(err)
	}

	want := `SELECT "u"."id", "u"."created_at", "u"."name", "u"."email" ` +
		`FROM "app_user" AS "u" WHERE ("u"."id" = $1)`
	if q.SQL != want {
		t.Fatalf("got  %s\nwant %s", q.SQL, want)
	}

	// The targets must point into the caller's struct, in select-list order.
	if len(q.Targets) != 4 {
		t.Fatalf("targets = %d, want 4", len(q.Targets))
	}
	if q.Targets[0] != any(&dst.ID) {
		t.Error("the first target is not the embedded ID field")
	}
	if q.Targets[2] != any(&dst.Name) {
		t.Error("the third target is not Name")
	}
}

// TestMapping_ImplementsStmtBinding is why this package can exist at all.
//
// bind.Bind takes an Expr[T] and a *T, and reflection has no static T to give
// it, so a reflection-driven binder can only work if stmt.Binding is an
// interface a package outside the module can implement. It is, and this asserts
// it: if Binding is ever made a struct again, this stops compiling.
func TestMapping_ImplementsStmtBinding(t *testing.T) {
	var dst user
	b := m.Bindings(&dst)[2]
	if b.ColumnName() != "name" {
		t.Errorf("ColumnName = %q, want name", b.ColumnName())
	}
	if b.Target() != any(&dst.Name) {
		t.Error("Target does not point at the field")
	}

	// The real assertion: a binding built here goes straight into Columns,
	// which takes stmt.Binding. If Binding ever stops being an interface this
	// call is what fails.
	if _, err := stmt.NewSelect(postgres.Dialect{}).
		Columns(m.Bindings(&dst)...).From(users).Build(); err != nil {
		t.Fatal(err)
	}
}

// TestMapping_Assignments covers the write half: an INSERT row built from a
// struct, with every value bound.
func TestMapping_Assignments(t *testing.T) {
	u := user{base: base{ID: "u1", CreatedAt: "now"}, Name: "ada", Email: "a@x.com"}

	q, err := stmt.NewInsert(postgres.Dialect{}).
		Into(users).
		Row(m.Assignments(&u)...).
		Build()
	if err != nil {
		t.Fatal(err)
	}

	want := `INSERT INTO "app_user" AS "u" ("id", "created_at", "name", "email") VALUES ($1, $2, $3, $4)`
	if q.SQL != want {
		t.Fatalf("got  %s\nwant %s", q.SQL, want)
	}
	if len(q.Args) != 4 || q.Args[0] != "u1" || q.Args[2] != "ada" {
		t.Fatalf("args = %v", q.Args)
	}
}

// TestMapping_Exprs covers the no-scan case, for a subquery or a caller that
// scans on its own.
func TestMapping_Exprs(t *testing.T) {
	q, err := stmt.NewSelect(postgres.Dialect{}).Exprs(m.Exprs()...).From(users).Build()
	if err != nil {
		t.Fatal(err)
	}
	if len(q.Targets) != 0 {
		t.Fatalf("Exprs must carry no scan targets, got %d", len(q.Targets))
	}
}

// TestNew_RejectsBadTypes pins the two failures that are always a programming
// mistake, so they are caught where the Mapping is declared rather than where
// a statement is built.
func TestNew_RejectsBadTypes(t *testing.T) {
	if _, err := structbind.New[int](users); !errors.Is(err, structbind.ErrNotAStruct) {
		t.Errorf("want ErrNotAStruct, got %v", err)
	}
	// A struct whose only field is excluded by tag: exported, so the linter
	// sees it used, and still contributing no column.
	type empty struct {
		Ignored int `db:"-"`
	}
	if _, err := structbind.New[empty](users); !errors.Is(err, structbind.ErrNoColumns) {
		t.Errorf("want ErrNoColumns, got %v", err)
	}
}

// TestMapping_IsReusable pins that the reflection happens once and a Mapping is
// safe as a package-level value, which is how it is meant to be used.
func TestMapping_IsReusable(t *testing.T) {
	var a, b user
	ba, bb := m.Bindings(&a), m.Bindings(&b)

	if ba[0].Target() == bb[0].Target() {
		t.Fatal("two calls returned bindings pointing at the same memory")
	}
	if len(m.Columns()) != 4 {
		t.Fatal("the Mapping was modified by being used")
	}
}
