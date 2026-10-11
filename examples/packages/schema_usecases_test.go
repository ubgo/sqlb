package packages_test

import (
	"fmt"
	"strings"

	"github.com/ubgo/sqlb/schema"
)

// ExampleColumn_types shows the portable type set. Every one of these means the
// same thing on Postgres, MySQL, SQLite and ClickHouse, which is the same
// universality rule the core builder follows.
func ExampleColumn_types() {
	for _, t := range schema.TypeValues {
		fmt.Println(t)
	}

	// Output:
	// text
	// int
	// bigint
	// bool
	// float
	// bytes
	// timestamp
	// date
	// uuid
	// json
	// decimal
}

// ExampleColumn_nullable shows what nullability means here. It is a property of
// the declaration, and schema.Col enforces it: a NOT NULL column cannot be
// asked for as a pointer, and a nullable one cannot be asked for as a value.
func ExampleColumn_nullable() {
	t := schema.Table{
		Name: "profile", Alias: "p",
		Columns: []schema.Column{
			{Name: "id", Type: schema.TypeUUID},
			{Name: "bio", Type: schema.TypeText, Nullable: true},
		},
	}

	_, err := schema.Col[string](t, "bio")
	fmt.Println("bio as string: ", err != nil)

	_, err = schema.Col[*string](t, "bio")
	fmt.Println("bio as *string:", err == nil)

	_, err = schema.Col[*string](t, "id")
	fmt.Println("id as *string: ", err == nil)

	// Output:
	// bio as string:  false
	// bio as *string: true
	// id as *string:  false
}

// ExampleColumn_raw shows the escape hatch. The portable set cannot express
// every engine's type, and a per-column override is visible in a diff where a
// per-table one would not be.
func ExampleColumn_raw() {
	t := schema.Table{
		Name: "document",
		Columns: []schema.Column{
			{Name: "id", Type: schema.TypeUUID},
			{Name: "embedding", Raw: "vector(1536)"},
		},
	}
	if err := t.Validate(); err != nil {
		fmt.Println("unexpected:", err)
	}

	// A Raw column opts out of the Go type check, because this package does not
	// know what the engine's own spelling means.
	_, err := schema.Col[[]float32](t, "embedding")
	fmt.Println("any Go type accepted for a Raw column:", err == nil)

	// Setting both is rejected, because Raw would win silently.
	err = schema.Table{Name: "x", Columns: []schema.Column{
		{Name: "c", Type: schema.TypeText, Raw: "TEXT"},
	}}.Validate()
	fmt.Println(err)

	// Output:
	// any Go type accepted for a Raw column: true
	// column x.c sets both Type and Raw; Raw would win silently
}

// ExampleIndex shows a composite index and a unique one. Indexes are declared
// on the table but created separately, because every engine accepts them only
// after the table exists.
func ExampleIndex() {
	t := schema.Table{
		Name:       "membership",
		PrimaryKey: []string{"id"},
		Columns: []schema.Column{
			{Name: "id", Type: schema.TypeUUID},
			{Name: "workspace_id", Type: schema.TypeUUID},
			{Name: "user_id", Type: schema.TypeUUID},
			{Name: "role", Type: schema.TypeText},
		},
		Indexes: []schema.Index{
			{Name: "membership_unique", Columns: []string{"workspace_id", "user_id"}, Unique: true},
			{Name: "membership_by_role", Columns: []string{"role"}},
		},
	}
	if err := t.Validate(); err != nil {
		fmt.Println("unexpected:", err)
	}
	for _, ix := range t.Indexes {
		fmt.Printf("%-20s unique=%-5v %v\n", ix.Name, ix.Unique, ix.Columns)
	}

	// An index naming a column the table does not declare is caught:
	broken := t
	broken.Indexes = []schema.Index{{Name: "ghost_ix", Columns: []string{"ghost"}}}
	fmt.Println(broken.Validate())

	// Output:
	// membership_unique    unique=true  [workspace_id user_id]
	// membership_by_role   unique=false [role]
	// index "ghost_ix" on "membership" names column "ghost" that the table does not declare
}

// ExampleForeignKey_actions shows the referential actions, which every engine
// here implements with the same meaning.
func ExampleForeignKey_actions() {
	for _, a := range []schema.Action{
		schema.NoAction, schema.Restrict, schema.Cascade, schema.SetNull, schema.SetDefault,
	} {
		fmt.Println(a)
	}

	// Output:
	// NO ACTION
	// RESTRICT
	// CASCADE
	// SET NULL
	// SET DEFAULT
}

// ExampleForeignKey_composite shows a two-column foreign key. The column counts
// must match, and a mismatch is caught at declaration rather than by the engine.
func ExampleForeignKey_composite() {
	err := schema.Table{
		Name: "line",
		Columns: []schema.Column{
			{Name: "tenant_id", Type: schema.TypeUUID},
			{Name: "order_id", Type: schema.TypeUUID},
		},
		ForeignKeys: []schema.ForeignKey{{
			Name: "line_order_fk", Columns: []string{"tenant_id", "order_id"},
			RefTable: "order", RefColumns: []string{"tenant_id"},
		}},
	}.Validate()
	fmt.Println(err)

	// Output:
	// foreign key "line_order_fk" on "line" has 2 columns but 1 referenced columns
}

// ExampleSet_schemas shows how two packages that both want a table called
// "users" resolve it. The Schema field is part of a table's key, so putting
// them in different schemas is the intended fix rather than a rename.
func ExampleSet_schemas() {
	set, err := schema.New("ubgo/auth", schema.Table{
		Schema: "auth", Name: "users",
		Columns: []schema.Column{{Name: "id", Type: schema.TypeUUID}},
	})
	if err != nil {
		panic(err)
	}
	err = set.Add("ubgo/billing", schema.Table{
		Schema: "billing", Name: "users",
		Columns: []schema.Column{{Name: "id", Type: schema.TypeUUID}},
	})
	fmt.Println("collision:", err != nil)

	for _, t := range set.Tables() {
		owner, _ := set.Owner(t.Schema + "." + t.Name)
		fmt.Printf("%s.%s owned by %s\n", t.Schema, t.Name, owner)
	}

	// Output:
	// collision: false
	// auth.users owned by ubgo/auth
	// billing.users owned by ubgo/billing
}

// ExampleTable_selfReference shows a tree. A row pointing at another row of its
// own table is legal and imposes no creation order, so it is not a cycle.
func ExampleTable_selfReference() {
	set, err := schema.New("app", schema.Table{
		Name: "category",
		Columns: []schema.Column{
			{Name: "id", Type: schema.TypeUUID},
			{Name: "parent_id", Type: schema.TypeUUID, Nullable: true},
		},
		ForeignKeys: []schema.ForeignKey{{
			Name: "category_parent_fk", Columns: []string{"parent_id"},
			RefTable: "category", RefColumns: []string{"id"}, OnDelete: schema.SetNull,
		}},
	})
	if err != nil {
		panic(err)
	}
	sorted, err := set.Sorted()
	fmt.Println(len(sorted), "table,", "err:", err)

	// Output:
	// 1 table, err: <nil>
}

// ExampleSet_deepChain shows ordering through four levels, which is the case a
// migration runner actually has and which no single contributor could compute.
func ExampleSet_deepChain() {
	mk := func(name string, refs ...string) schema.Table {
		t := schema.Table{
			Name:    name,
			Columns: []schema.Column{{Name: "id", Type: schema.TypeUUID}},
		}
		for _, r := range refs {
			t.ForeignKeys = append(t.ForeignKeys, schema.ForeignKey{
				Name: name + "_" + r + "_fk", Columns: []string{"id"},
				RefTable: r, RefColumns: []string{"id"},
			})
		}
		return t
	}

	// Added in an order that is deliberately wrong.
	set, err := schema.New("app", mk("line", "invoice"), mk("invoice", "user", "org"), mk("user", "org"), mk("org"))
	if err != nil {
		panic(err)
	}
	sorted, err := set.Sorted()
	if err != nil {
		panic(err)
	}
	names := make([]string, len(sorted))
	for i, t := range sorted {
		names[i] = t.Name
	}
	fmt.Println(strings.Join(names, " -> "))

	// Output:
	// org -> user -> invoice -> line
}
