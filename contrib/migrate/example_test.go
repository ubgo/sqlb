package migrate_test

import (
	"fmt"
	"strings"

	"github.com/ubgo/sqlb/contrib/migrate"
	"github.com/ubgo/sqlb/dialect/postgres"
	"github.com/ubgo/sqlb/dialect/sqlite"
	"github.com/ubgo/sqlb/schema"
)

func users() schema.Table {
	return schema.Table{
		Name:       "app_user",
		PrimaryKey: []string{"id"},
		Columns: []schema.Column{
			{Name: "id", Type: schema.TypeUUID},
			{Name: "email", Type: schema.TypeText},
			{Name: "age", Type: schema.TypeInt, Nullable: true},
			{Name: "created_at", Type: schema.TypeTimestamp, Default: "CURRENT_TIMESTAMP"},
		},
		Indexes: []schema.Index{{Name: "app_user_email_key", Columns: []string{"email"}, Unique: true}},
	}
}

// ExampleCreateTable shows DDL generated from a declaration.
func ExampleCreateTable() {
	ddl, err := migrate.CreateTable(postgres.Dialect{}, users())
	if err != nil {
		panic(err)
	}
	fmt.Println(ddl)

	// Output:
	// CREATE TABLE "app_user" (
	//	"id" UUID NOT NULL,
	//	"email" TEXT NOT NULL,
	//	"age" INTEGER,
	//	"created_at" TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
	//	PRIMARY KEY ("id")
	// )
}

// ExampleCreateTable_perEngine shows one declaration serving two engines. The
// difference is the type mapping and the quoting, and nothing in the schema
// itself changes.
func ExampleCreateTable_perEngine() {
	ddl, err := migrate.CreateTable(sqlite.Dialect{}, users())
	if err != nil {
		panic(err)
	}
	fmt.Println(ddl)

	// Output:
	// CREATE TABLE "app_user" (
	//	"id" TEXT NOT NULL,
	//	"email" TEXT NOT NULL,
	//	"age" INTEGER,
	//	"created_at" TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP,
	//	PRIMARY KEY ("id")
	// )
}

// ExampleCreateIndexes shows indexes rendered separately, because every engine
// here accepts them only after the table exists.
func ExampleCreateIndexes() {
	for _, s := range migrate.CreateIndexes(postgres.Dialect{}, users()) {
		fmt.Println(s)
	}

	// Output:
	// CREATE UNIQUE INDEX "app_user_email_key" ON "app_user" ("email")
}

// ExamplePlan shows the whole thing: two packages that never imported each
// other, ordered and rendered by a third that imported neither.
func ExamplePlan() {
	set, err := schema.New("ubgo/auth", users())
	if err != nil {
		panic(err)
	}
	err = set.Add("the application", schema.Table{
		Name:       "invoice",
		PrimaryKey: []string{"id"},
		Columns: []schema.Column{
			{Name: "id", Type: schema.TypeUUID},
			{Name: "user_id", Type: schema.TypeUUID},
			{Name: "total", Type: schema.TypeDecimal},
		},
		ForeignKeys: []schema.ForeignKey{{
			Name: "invoice_user_fk", Columns: []string{"user_id"},
			RefTable: "app_user", RefColumns: []string{"id"}, OnDelete: schema.Cascade,
		}},
	})
	if err != nil {
		panic(err)
	}

	plan, err := migrate.Plan(postgres.Dialect{}, *set)
	if err != nil {
		panic(err)
	}
	for i, s := range plan {
		first, _, _ := strings.Cut(s, "\n")
		fmt.Printf("%d. %s\n", i+1, first)
	}

	// Output:
	// 1. CREATE TABLE "app_user" (
	// 2. CREATE TABLE "invoice" (
	// 3. CREATE UNIQUE INDEX "app_user_email_key" ON "app_user" ("email")
}

// ExampleSQLType shows the one genuinely engine-specific part of DDL.
func ExampleSQLType() {
	c := schema.Column{Name: "at", Type: schema.TypeTimestamp}
	pgType, _ := migrate.SQLType(postgres.Dialect{}, c)
	liteType, _ := migrate.SQLType(sqlite.Dialect{}, c)
	fmt.Println("postgres:", pgType)
	fmt.Println("sqlite:  ", liteType)
	fmt.Println("engines: ", migrate.Engines())

	// Output:
	// postgres: TIMESTAMPTZ
	// sqlite:   TEXT
	// engines:  [clickhouse mysql postgres sqlite]
}
