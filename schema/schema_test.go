package schema_test

import (
	"errors"
	"go/build"
	"strings"
	"testing"

	"github.com/ubgo/sqlb/dialect/postgres"
	"github.com/ubgo/sqlb/expr"
	"github.com/ubgo/sqlb/render"
	"github.com/ubgo/sqlb/schema"
	"github.com/ubgo/sqlb/stmt"
)

// authTables is what a library like ubgo/auth would export. It knows nothing
// about any application, and nothing below imports it.
func authTables() []schema.Table {
	return []schema.Table{{
		Name:       "auth_user",
		Alias:      "u",
		PrimaryKey: []string{"id"},
		Columns: []schema.Column{
			{Name: "id", Type: schema.TypeUUID},
			{Name: "email", Type: schema.TypeText},
			{Name: "created_at", Type: schema.TypeTimestamp},
		},
		Indexes: []schema.Index{{Name: "auth_user_email_key", Columns: []string{"email"}, Unique: true}},
	}}
}

// appTables is what the application declares. It references the library's table
// by name, which is the only way it could: importing the library's schema value
// is exactly the coupling this package exists to avoid.
func appTables() []schema.Table {
	return []schema.Table{
		{
			Name:       "invoice",
			Alias:      "i",
			PrimaryKey: []string{"id"},
			Columns: []schema.Column{
				{Name: "id", Type: schema.TypeUUID},
				{Name: "user_id", Type: schema.TypeUUID},
				{Name: "total", Type: schema.TypeDecimal},
				{Name: "note", Type: schema.TypeText, Nullable: true},
			},
			ForeignKeys: []schema.ForeignKey{{
				Name: "invoice_user_fk", Columns: []string{"user_id"},
				RefTable: "auth_user", RefColumns: []string{"id"}, OnDelete: schema.Cascade,
			}},
		},
		{
			Name:       "invoice_line",
			PrimaryKey: []string{"id"},
			Columns: []schema.Column{
				{Name: "id", Type: schema.TypeUUID},
				{Name: "invoice_id", Type: schema.TypeUUID},
				{Name: "amount", Type: schema.TypeDecimal},
			},
			ForeignKeys: []schema.ForeignKey{{
				Name: "line_invoice_fk", Columns: []string{"invoice_id"},
				RefTable: "invoice", RefColumns: []string{"id"}, OnDelete: schema.Cascade,
			}},
		},
	}
}

// TestSet_OrdersTablesFromPackagesThatDoNotKnowEachOther is the question this
// package was built to answer, quoted from the architecture doc: can a library
// declare its tables, an application declare its own, and a migration runner
// order both correctly without either importing the other?
//
// Neither contributor here can produce this ordering alone. The library does not
// know invoices exist; the application knows only a table name. The order
// emerges from putting them in one Set, which is the argument for having a
// shared schema vocabulary at all.
func TestSet_OrdersTablesFromPackagesThatDoNotKnowEachOther(t *testing.T) {
	set, err := schema.New("ubgo/auth", authTables()...)
	if err != nil {
		t.Fatal(err)
	}
	if err := set.Add("the application", appTables()...); err != nil {
		t.Fatal(err)
	}

	sorted, err := set.Sorted()
	if err != nil {
		t.Fatal(err)
	}

	var got []string
	for _, tbl := range sorted {
		got = append(got, tbl.Name)
	}
	want := "auth_user invoice invoice_line"
	if strings.Join(got, " ") != want {
		t.Fatalf("got  %v\nwant %s", got, want)
	}
}

// TestSet_SortIsDeterministic pins that independent tables come back in a
// stable order. Kahn's algorithm leaves that freedom, and resolving it by map
// iteration would make a generated migration change shape between runs for no
// reason.
func TestSet_SortIsDeterministic(t *testing.T) {
	build := func() []string {
		set, _ := schema.New("a", authTables()...)
		_ = set.Add("b", appTables()...)
		sorted, err := set.Sorted()
		if err != nil {
			t.Fatal(err)
		}
		var names []string
		for _, tbl := range sorted {
			names = append(names, tbl.Name)
		}
		return names
	}
	first := strings.Join(build(), " ")
	for range 20 {
		if got := strings.Join(build(), " "); got != first {
			t.Fatalf("ordering changed between runs: %q then %q", first, got)
		}
	}
}

// TestSet_DuplicateTableNamesBothOwners is the collision two independent
// packages will actually hit. The fix is a prefix or a schema, and you cannot
// choose one without being told who collided.
func TestSet_DuplicateTableNamesBothOwners(t *testing.T) {
	set, err := schema.New("ubgo/auth", schema.Table{
		Name: "users", Columns: []schema.Column{{Name: "id", Type: schema.TypeUUID}},
	})
	if err != nil {
		t.Fatal(err)
	}
	err = set.Add("ubgo/billing", schema.Table{
		Name: "users", Columns: []schema.Column{{Name: "id", Type: schema.TypeInt}},
	})
	if !errors.Is(err, schema.ErrDuplicateTable) {
		t.Fatalf("want ErrDuplicateTable, got %v", err)
	}
	for _, who := range []string{"ubgo/auth", "ubgo/billing"} {
		if !strings.Contains(err.Error(), who) {
			t.Errorf("error does not name %q: %v", who, err)
		}
	}
}

// TestSet_MissingReferenceIsCaughtOnAssembly pins where an unresolved reference
// is found. A library names its target by string so it need not import the
// owner; the cost is that the name is unchecked until assembly, and this is
// assembly.
func TestSet_MissingReferenceIsCaughtOnAssembly(t *testing.T) {
	set, _ := schema.New("the application", appTables()...)
	_, err := set.Sorted()
	if !errors.Is(err, schema.ErrMissingTable) {
		t.Fatalf("want ErrMissingTable, got %v", err)
	}
	if !strings.Contains(err.Error(), "auth_user") {
		t.Errorf("error does not name the missing table: %v", err)
	}
}

// TestSet_CycleIsReportedWithItsMembers pins that an impossible schema names the
// tables involved rather than announcing that a cycle exists somewhere.
func TestSet_CycleIsReportedWithItsMembers(t *testing.T) {
	mk := func(name, ref string) schema.Table {
		return schema.Table{
			Name: name, Columns: []schema.Column{{Name: "id", Type: schema.TypeUUID}},
			ForeignKeys: []schema.ForeignKey{{Name: name + "_fk", Columns: []string{"id"}, RefTable: ref, RefColumns: []string{"id"}}},
		}
	}
	set, _ := schema.New("app", mk("a", "b"), mk("b", "a"))
	_, err := set.Sorted()
	if !errors.Is(err, schema.ErrCycle) {
		t.Fatalf("want ErrCycle, got %v", err)
	}
	for _, name := range []string{"a", "b"} {
		if !strings.Contains(err.Error(), name) {
			t.Errorf("cycle error does not name %q: %v", name, err)
		}
	}
}

// TestTable_SelfReferenceIsNotACycle pins the case that naively looks like one.
// A row pointing at another row of its own table imposes no creation order.
func TestTable_SelfReferenceIsNotACycle(t *testing.T) {
	set, _ := schema.New("app", schema.Table{
		Name: "node",
		Columns: []schema.Column{
			{Name: "id", Type: schema.TypeUUID},
			{Name: "parent_id", Type: schema.TypeUUID, Nullable: true},
		},
		ForeignKeys: []schema.ForeignKey{{
			Name: "node_parent_fk", Columns: []string{"parent_id"},
			RefTable: "node", RefColumns: []string{"id"},
		}},
	})
	sorted, err := set.Sorted()
	if err != nil {
		t.Fatalf("a self-reference must not be a cycle: %v", err)
	}
	if len(sorted) != 1 {
		t.Fatalf("got %d tables, want 1", len(sorted))
	}
}

// TestSchema_BridgesToTheQueryBuilder is the reason this package imports expr:
// a table is declared once, and the query builder gets its columns from the
// declaration instead of from a second copy that can drift.
func TestSchema_BridgesToTheQueryBuilder(t *testing.T) {
	users := authTables()[0]
	email, err := schema.Col[string](users, "email")
	if err != nil {
		t.Fatal(err)
	}

	q, err := stmt.NewSelect(postgres.Dialect{}).
		Exprs(email).
		From(users.Ref()).
		Where(expr.Eq(email, "a@example.com")).
		Build()
	if err != nil {
		t.Fatal(err)
	}
	want := `SELECT "u"."email" FROM "auth_user" AS "u" WHERE ("u"."email" = $1)`
	if q.SQL != want {
		t.Fatalf("got  %q\nwant %q", q.SQL, want)
	}
}

// TestSchema_ColRejectsTheWrongGoType pins the mistake this package can
// actually catch. Asking for an int from a text column is a scan failure in
// production and an error at declaration time here.
func TestSchema_ColRejectsTheWrongGoType(t *testing.T) {
	users := authTables()[0]
	if _, err := schema.Col[int](users, "email"); !errors.Is(err, schema.ErrTypeMismatch) {
		t.Fatalf("want ErrTypeMismatch, got %v", err)
	}
	if _, err := schema.Col[string](users, "nope"); err == nil {
		t.Fatal("an undeclared column must be an error")
	}
	if _, err := schema.Col[*string](users, "email"); !errors.Is(err, schema.ErrTypeMismatch) {
		t.Fatalf("a NOT NULL column asked for as a pointer must be rejected, got %v", err)
	}
}

// TestSchema_MustColPanicsOnEveryMistakeColReports pins that the package-level
// spelling refuses exactly what Col refuses, so using it in a var block trades
// the error for a panic and gives up no checking. The panic carries Col's own
// error, which is what makes the failure readable at package load.
func TestSchema_MustColPanicsOnEveryMistakeColReports(t *testing.T) {
	users := authTables()[0]

	// The one case that must not panic: the declared type, asked for correctly.
	email := schema.MustCol[string](users, "email")
	q, err := stmt.NewSelect(postgres.Dialect{}).Exprs(email).From(users.Ref()).Build()
	if err != nil {
		t.Fatal(err)
	}
	if want := `SELECT "u"."email" FROM "auth_user" AS "u"`; q.SQL != want {
		t.Fatalf("got  %q\nwant %q", q.SQL, want)
	}

	cases := map[string]struct {
		call func()
		want error
	}{
		"the wrong Go type":                 {func() { schema.MustCol[int](users, "email") }, schema.ErrTypeMismatch},
		"a NOT NULL column as a pointer":    {func() { schema.MustCol[*string](users, "email") }, schema.ErrTypeMismatch},
		"a column the table never declared": {func() { schema.MustCol[string](users, "nope") }, nil},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			defer func() {
				r := recover()
				if r == nil {
					t.Fatal("MustCol returned where Col reports an error")
				}
				err, ok := r.(error)
				if !ok {
					t.Fatalf("panicked with %T, want the error Col returned", r)
				}
				if c.want != nil && !errors.Is(err, c.want) {
					t.Fatalf("panicked with %v, want %v", err, c.want)
				}
			}()
			c.call()
		})
	}
}

// TestTable_ValidateReportsEveryProblem pins that a schema written in one
// sitting reports all its mistakes at once, rather than one per run.
func TestTable_ValidateReportsEveryProblem(t *testing.T) {
	err := schema.Table{
		Name: "broken",
		Columns: []schema.Column{
			{Name: "id", Type: schema.TypeUUID},
			{Name: "id", Type: schema.TypeText},
			{Name: "both", Type: schema.TypeText, Raw: "TEXT"},
			{Name: "neither"},
		},
		PrimaryKey: []string{"absent"},
		Indexes:    []schema.Index{{Name: "ix", Columns: []string{"ghost"}}},
	}.Validate()
	if err == nil {
		t.Fatal("expected errors")
	}
	for _, want := range []string{"twice", "both Type and Raw", "neither Type nor Raw", "absent", "ghost"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error does not mention %q:\n%v", want, err)
		}
	}
}

// allowed is what the schema layer may import: the expression layer, so a
// declaration can hand back typed columns, and nothing else from this module.
// Not stmt, not exec, not an engine, and never the root.
var allowed = map[string]bool{
	"github.com/ubgo/sqlb/expr":   true,
	"github.com/ubgo/sqlb/render": true,
}

// TestSchema_ImportsOnlyExpr keeps this layer describing rather than doing. A
// schema that imported stmt would be a migration engine wearing the wrong name.
func TestSchema_ImportsOnlyExpr(t *testing.T) {
	pkg, err := build.ImportDir(".", 0)
	if err != nil {
		t.Fatal(err)
	}
	for _, imp := range pkg.Imports {
		if strings.Contains(imp, ".") && !allowed[imp] {
			t.Errorf("schema imports %q; it may import only the standard library and expr", imp)
		}
	}
}

var _ render.Dialect = postgres.Dialect{}

// TestSet_TablesAndOwners pins the two accessors a migration runner needs
// beyond the ordering: what is in the set, and who put it there. Ownership is
// what turns "table users already exists" into an actionable message.
func TestSet_TablesAndOwners(t *testing.T) {
	set, err := schema.New("ubgo/auth", authTables()...)
	if err != nil {
		t.Fatal(err)
	}
	if err := set.Add("the application", appTables()...); err != nil {
		t.Fatal(err)
	}

	var names []string
	for _, tbl := range set.Tables() {
		names = append(names, tbl.Name)
	}
	if got, want := strings.Join(names, " "), "auth_user invoice invoice_line"; got != want {
		t.Fatalf("Tables() = %q, want %q (name order, not dependency order)", got, want)
	}

	for table, want := range map[string]string{
		"auth_user": "ubgo/auth",
		"invoice":   "the application",
	} {
		got, ok := set.Owner(table)
		if !ok || got != want {
			t.Errorf("Owner(%q) = %q, %v; want %q, true", table, got, ok, want)
		}
	}
	if _, ok := set.Owner("nobody"); ok {
		t.Error("Owner reported an owner for a table nobody contributed")
	}
}

// TestTable_ColumnNames pins that the declared order is preserved, which is
// what a DDL renderer and an INSERT column list both depend on.
func TestTable_ColumnNames(t *testing.T) {
	got := strings.Join(authTables()[0].ColumnNames(), " ")
	if want := "id email created_at"; got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}
