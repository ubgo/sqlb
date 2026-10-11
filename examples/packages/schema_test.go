package packages_test

import (
	"fmt"
	"strings"

	"github.com/ubgo/sqlb/schema"
)

// libraryTables is what a library like an auth package would export. It knows
// nothing about any application.
func libraryTables() []schema.Table {
	return []schema.Table{{
		Name:       "auth_user",
		Alias:      "u",
		PrimaryKey: []string{"id"},
		Columns: []schema.Column{
			{Name: "id", Type: schema.TypeUUID},
			{Name: "email", Type: schema.TypeText},
			{Name: "created_at", Type: schema.TypeTimestamp, Default: "CURRENT_TIMESTAMP"},
		},
		Indexes: []schema.Index{{Name: "auth_user_email_key", Columns: []string{"email"}, Unique: true}},
	}}
}

// applicationTables reference the library's table by NAME, which is the only
// way they could: importing the library's schema value is exactly the coupling
// this package exists to avoid.
func applicationTables() []schema.Table {
	return []schema.Table{{
		Name:       "invoice",
		PrimaryKey: []string{"id"},
		Columns: []schema.Column{
			{Name: "id", Type: schema.TypeUUID},
			{Name: "user_id", Type: schema.TypeUUID},
			{Name: "total", Type: schema.TypeDecimal},
		},
		ForeignKeys: []schema.ForeignKey{{
			Name: "invoice_user_fk", Columns: []string{"user_id"},
			RefTable: "auth_user", RefColumns: []string{"id"}, OnDelete: schema.Cascade,
		}},
	}}
}

// ExampleSet shows the reason this package exists. Neither contributor can
// produce this ordering alone: the library does not know invoices exist, and
// the application knows only a table name.
func ExampleSet() {
	set, err := schema.New("ubgo/auth", libraryTables()...)
	if err != nil {
		panic(err)
	}
	if err := set.Add("the application", applicationTables()...); err != nil {
		panic(err)
	}

	sorted, err := set.Sorted()
	if err != nil {
		panic(err)
	}
	for _, t := range sorted {
		owner, _ := set.Owner(t.Name)
		fmt.Printf("%-12s declared by %s\n", t.Name, owner)
	}

	// Output:
	// auth_user    declared by ubgo/auth
	// invoice      declared by the application
}

// ExampleSet_duplicate shows the collision two independent packages will
// actually hit. The fix is a prefix or a schema, and nobody can choose one
// without being told who collided.
func ExampleSet_duplicate() {
	set, _ := schema.New("ubgo/auth", schema.Table{
		Name: "users", Columns: []schema.Column{{Name: "id", Type: schema.TypeUUID}},
	})
	err := set.Add("ubgo/billing", schema.Table{
		Name: "users", Columns: []schema.Column{{Name: "id", Type: schema.TypeInt}},
	})
	fmt.Println(err)

	// Output:
	// schema: two packages declare the same table: "users" declared by both "ubgo/auth" and "ubgo/billing"
}

// ExampleCol shows the bridge to the query builder: a table is declared once,
// and the columns come from the declaration rather than a second copy that can
// drift.
func ExampleCol_fromADeclaration() {
	users := libraryTables()[0]

	email, err := schema.Col[string](users, "email")
	if err != nil {
		panic(err)
	}
	fmt.Println(email.Name(), "on", users.Ref().Name, "as", users.Ref().Alias)

	// The Go type is checked against the declaration:
	_, err = schema.Col[int](users, "email")
	fmt.Println(strings.SplitN(err.Error(), ":", 3)[2])

	// Output:
	// email on auth_user as u
	//  schema: Go type does not match the declared column type: column is text, which is string, but was asked for int
}

// ExampleTable_Validate shows that a schema written in one sitting reports all
// its mistakes at once, rather than one per run.
func ExampleTable_Validate() {
	err := schema.Table{
		Name: "broken",
		Columns: []schema.Column{
			{Name: "id", Type: schema.TypeUUID},
			{Name: "id", Type: schema.TypeText},
		},
		PrimaryKey: []string{"absent"},
	}.Validate()

	for _, line := range strings.Split(err.Error(), "\n") {
		fmt.Println(line)
	}

	// Output:
	// table "broken" declares column "id" twice
	// table "broken" has primary key column "absent" that it does not declare
}
