# `schema` — what tables exist

```go
import "github.com/ubgo/sqlb/schema"
```

Describes tables so that two packages which do not know about each other can be migrated together.

It **describes only**. No DDL, no database connection, no migration. A migration engine, a code generator and an introspector all live above it — [`contrib/migrate`](../../contrib/migrate) is the one that ships.

## Contents

- [Setup](#setup)
- [Why a shared vocabulary](#why-a-shared-vocabulary)
- [Declaring a table](#declaring-a-table)
- [Column types](#column-types)
- [Nullability](#nullability)
- [The Raw escape hatch](#the-raw-escape-hatch)
- [Indexes](#indexes)
- [Foreign keys](#foreign-keys)
- [Assembling a Set](#assembling-a-set)
- [What assembly catches](#what-assembly-catches)
- [Two packages, one table name](#two-packages-one-table-name)
- [Trees and deep chains](#trees-and-deep-chains)
- [Generating DDL](#generating-ddl)
- [From a Set to statements](#from-a-set-to-statements)
- [Bridging to the query builder](#bridging-to-the-query-builder)
- [Which direction declares which](#which-direction-declares-which)
- [Validation](#validation)

## Why a shared vocabulary

The query layers already solve half of this. A library exports typed columns and an application joins across the boundary, because both speak one vocabulary: `expr.Table` and `expr.Col`.

Without a shared vocabulary for the *schema* itself, every generator and migration tool built on `sqlb` invents its own description. The moment a library declares its tables in one format and the application uses another, a migration runner holds two incompatible accounts of the same database and can order neither.

So the argument is not that an ORM-like package cannot be built without this. One was, against the exported API, inventing its own table description on the spot. The argument is **what happens when there are two of them.**

## Setup

The snippets below render for one engine.

```go
import (
	"github.com/ubgo/sqlb/dialect/postgres"
	"github.com/ubgo/sqlb/schema"
)

// Every snippet below renders for one engine.
var d = postgres.Dialect{}

// The table used by the DDL snippets near the end.
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

// mk builds a table with foreign keys, for the ordering snippets.
func mk(name string, refs ...string) schema.Table {
	t := schema.Table{Name: name, Columns: []schema.Column{{Name: "id", Type: schema.TypeUUID}}}
	for _, r := range refs {
		t.ForeignKeys = append(t.ForeignKeys, schema.ForeignKey{
			Name: name + "_" + r + "_fk", Columns: []string{"id"},
			RefTable: r, RefColumns: []string{"id"},
		})
	}
	return t
}
```

## Declaring a table

```go
schema.Table{
	Name:       "auth_user",
	Alias:      "u",
	PrimaryKey: []string{"id"},
	Columns: []schema.Column{
		{Name: "id", Type: schema.TypeUUID},
		{Name: "email", Type: schema.TypeText},
		{Name: "created_at", Type: schema.TypeTimestamp, Default: "CURRENT_TIMESTAMP"},
	},
	Indexes: []schema.Index{{Name: "auth_user_email_key", Columns: []string{"email"}, Unique: true}},
}
```

Both `Table` and `Column` carry a `Comment`, which a DDL renderer that supports comments carries through and one that does not ignores. It is on the declaration rather than in a sidecar file for the usual reason: a comment that lives beside the column is a comment that gets updated with it.

Types are **logical, not SQL**: `TypeText`, `TypeInt`, `TypeBigInt`, `TypeBool`, `TypeFloat`, `TypeBytes`, `TypeTimestamp`, `TypeDate`, `TypeUUID`, `TypeJSON`, `TypeDecimal`. "The smallest integer that holds a user id" is portable; `INTEGER`, `INT4` and `Int32` are three engines disagreeing about how to spell it. Mapping a type to an engine is the job of whatever renders DDL, which is why one declaration serves Postgres and SQLite without a conditional.

`Column.Raw` is the escape hatch, per column so that reaching for an engine-specific type costs one field and is visible in a diff:

```go
{Name: "embedding", Raw: "vector(1536)"}
```

A foreign key names its target **by string**:

```go
ForeignKeys: []schema.ForeignKey{{
	Name: "invoice_user_fk", Columns: []string{"user_id"},
	RefTable: "auth_user", RefColumns: []string{"id"}, OnDelete: schema.Cascade,
}}
```

That is deliberate. The referenced table is frequently owned by a different package, and taking a value here would force that package to be imported — exactly the coupling this package exists to avoid.

## Column types

The portable set, in `TypeValues` order:

```
text   int   bigint   bool   float   bytes   timestamp   date   uuid   json   decimal
```

Eleven, and the set is closed on purpose. Every one has to mean the same thing on Postgres, MySQL, SQLite and ClickHouse, which is the same universality rule the core builder follows. Anything an engine spells its own way belongs in `Raw`, where it is visibly engine-specific.

They are logical types, not SQL ones. "The smallest integer that holds a user id" is portable; `INTEGER`, `INT4` and `Int32` are three engines disagreeing about how to spell it. What each becomes is decided by whatever renders DDL — see [generating DDL](#generating-ddl).

The spelling table itself lives here, as description rather than action, so that everything reading it agrees: `contrib/migrate` renders `CREATE TABLE` from it and `contrib/vet` checks a `Col[T]` declaration against a live catalogue with it, and a column the vet accepts is one `CreateTable` would have produced.

```go
schema.EngineNames()                          // ["clickhouse", "postgres", "sqlite"]
schema.EngineType("postgres", schema.TypeBigInt)  // "BIGINT", true
schema.EngineType("postgres", schema.Type("money")) // "", false: not portable
schema.GoTypes(schema.TypeTimestamp)          // ["time.Time", "string"]
```

`GoTypes` is the same table `Column.Get` checks a Go type against, spelled as reflect prints it, so `[]byte` is `[]uint8`.

## Nullability

`Nullable` is a property of the declaration, and `schema.Col` enforces it in both directions:

```go
t := schema.Table{
	Name: "profile", Alias: "p",
	Columns: []schema.Column{
		{Name: "id", Type: schema.TypeUUID},
		{Name: "bio", Type: schema.TypeText, Nullable: true},
	},
}
```

```
bio as string:  false     a nullable column cannot be a value
bio as *string: true
id as *string:  false     a NOT NULL column cannot be a pointer
```

This is the mistake the package can actually catch. A nullable column scanned into a non-pointer fails at runtime on the first `NULL` row, which is usually in production and usually months later.

## The Raw escape hatch

The portable set cannot express every engine's type, so `Column.Raw` overrides it. It is per column rather than per table, so reaching for an engine-specific type costs one field and is visible in a diff:

```go
{Name: "embedding", Raw: "vector(1536)"}
```

A `Raw` column opts out of the Go type check, because this package does not know what the engine's own spelling means. That is the documented cost of the hatch.

Setting both is rejected rather than resolved, because `Raw` would win silently:

```
column x.c sets both Type and Raw; Raw would win silently
```

## Indexes

Declared on the table, created separately, because every engine here accepts an index only after the table exists.

```go
Indexes: []schema.Index{
	{Name: "membership_unique", Columns: []string{"workspace_id", "user_id"}, Unique: true},
	{Name: "membership_by_role", Columns: []string{"role"}},
}
```

```
membership_unique    unique=true  [workspace_id user_id]
membership_by_role   unique=false [role]
```

An index naming a column the table does not declare is caught at declaration:

```
index "ghost_ix" on "membership" names column "ghost" that the table does not declare
```

## Foreign keys

`Action` is what a foreign key does when the referenced row goes away, and the five constants each mean the same thing on every engine here:

| Constant | Renders |
|---|---|
| `NoAction` | `NO ACTION` |
| `Restrict` | `RESTRICT` |
| `Cascade` | `CASCADE` |
| `SetNull` | `SET NULL` |
| `SetDefault` | `SET DEFAULT` |

`NoAction` and `Restrict` are the pair worth reading twice, because the difference is invisible in the name: both refuse the delete, and they differ only in whether the check can be deferred. Nothing in this repository measures that difference on an engine, so treat it as the engine's rule to check rather than as something this page verified. The zero `Action` is the empty string and renders no clause at all, which leaves the engine's own default in place.

Composite keys are two lists, and a mismatch is caught where it is written rather than by the engine at migration time:

```go
ForeignKeys: []schema.ForeignKey{{
	Name: "line_order_fk", Columns: []string{"tenant_id", "order_id"},
	RefTable: "order", RefColumns: []string{"tenant_id"},
}}
```

```
foreign key "line_order_fk" on "line" has 2 columns but 1 referenced columns
```

## Assembling a Set

A `Set` is where contributors meet:

```go
set, err := schema.New("ubgo/auth", libraryTables()...)
err = set.Add("the application", applicationTables()...)

sorted, err := set.Sorted()
```

```
auth_user    declared by ubgo/auth
invoice      declared by the application
```

**Neither contributor could produce that ordering alone.** The library does not know invoices exist; the application knows only a table name. The order emerges from putting them in one `Set`, which is the whole argument for a shared schema vocabulary.

The owner string is used only in errors, and it earns its place — see below.

Three properties are load-bearing, and each has a test:

- **The sort is deterministic.** Kahn's algorithm leaves genuine freedom among independent tables, and resolving it by map iteration would make a generated migration change shape between runs for no reason.
- **A self-reference is not a cycle.** A row pointing at another row of its own table imposes no creation order.
- **A cycle names its members**, rather than announcing that one exists somewhere. `Sorted` returns `ErrCycle` wrapped with the tables still unplaced, so the error says which foreign keys to break rather than that some do.

## What assembly catches

A foreign key naming a table by string is unchecked until the pieces are put together. Assembly is where it gets checked:

```
schema: foreign key references a table no package contributed: "invoice_user_fk" on "invoice" references "auth_user"
```

And the collision two independent packages will actually hit:

```
schema: two packages declare the same table: "users" declared by both "ubgo/auth" and "ubgo/billing"
```

Naming both owners is the value of that error. The fix is a prefix or a schema, and nobody can choose one without knowing who collided. Putting the two tables in different schemas resolves it, because the `Schema` field is part of a table's key.

## Two packages, one table name

Two independent packages will eventually both want `users`. The `Schema` field is part of a table's key, so putting them in different schemas is the intended fix rather than renaming somebody's table:

```go
set, _ := schema.New("ubgo/auth", schema.Table{
	Schema: "auth", Name: "users", Columns: []schema.Column{{Name: "id", Type: schema.TypeUUID}},
})
err := set.Add("ubgo/billing", schema.Table{
	Schema: "billing", Name: "users", Columns: []schema.Column{{Name: "id", Type: schema.TypeUUID}},
})
```

```
collision: false
auth.users owned by ubgo/auth
billing.users owned by ubgo/billing
```

Without the schemas the same two declarations collide, and the error names both owners so the fix can be chosen — see [what assembly catches](#what-assembly-catches).

## Trees and deep chains

A table referencing itself is a tree, and it is not a cycle: a row pointing at another row of its own table imposes no creation order.

```go
ForeignKeys: []schema.ForeignKey{{
	Name: "category_parent_fk", Columns: []string{"parent_id"},
	RefTable: "category", RefColumns: []string{"id"}, OnDelete: schema.SetNull,
}}
```

```
1 table, err: <nil>
```

Depth is where ordering earns its place. Four tables, added deliberately in the wrong order, one of them referencing two others:

```go
set, _ := schema.New("app",
	mk("line", "invoice"),
	mk("invoice", "user", "org"),
	mk("user", "org"),
	mk("org"),
)
sorted, err := set.Sorted()
```

```
org -> user -> invoice -> line
```

No contributor could compute that alone, and neither could a human reliably, which is the argument for declaring dependencies rather than ordering migrations by hand.

## Generating DDL

This package renders nothing. [`contrib/migrate`](../../contrib/migrate) is the consumer that does, and it is a separate module because it targets engines by name.

```go
ddl, err := migrate.CreateTable(postgres.Dialect{}, users())
```

```sql
CREATE TABLE "app_user" (
	"id" UUID NOT NULL,
	"email" TEXT NOT NULL,
	"age" INTEGER,
	"created_at" TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
	PRIMARY KEY ("id")
)
```

The same declaration, rendered for SQLite. Nothing in the schema changed; the type mapping did:

```sql
CREATE TABLE "app_user" (
	"id" TEXT NOT NULL,
	"email" TEXT NOT NULL,
	"age" INTEGER,
	"created_at" TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP,
	PRIMARY KEY ("id")
)
```

```go
migrate.SQLType(postgres.Dialect{}, schema.Column{Name: "at", Type: schema.TypeTimestamp})
```

```
postgres: TIMESTAMPTZ
sqlite:   TEXT
engines:  [clickhouse mysql postgres sqlite]
```

`Plan` does the whole set, in `Sorted` order, with indexes after every table so a failure partway through leaves a set of tables rather than a half-indexed one:

```go
plan, err := migrate.Plan(postgres.Dialect{}, *set)
```

```
1. CREATE TABLE "app_user" (
2. CREATE TABLE "invoice" (
3. CREATE UNIQUE INDEX "app_user_email_key" ON "app_user" ("email")
```

`Plan` deliberately stops there: it returns statements and runs nothing. `migrate` does not diff against a live database, because that needs engine-specific introspection, which `contrib/vet`'s `CompareTables` does and reports rather than repairs. Running is separate and opt-in: `Execute` runs a script the way its flags say, and a `Ledger` applies the migrations it is handed once each under a lock and reads back with `Applied` what a database has had. Which migrations exist and when a deploy runs them stay the application's, because that is an operational question rather than a library's.

## From a Set to statements

The arrow between this package and [`ddl`](../ddl/README.md) points one way. `ddl.CreateTableReady.Describe` turns a statement a library authored into a `schema.Table`, so authored DDL can join a `Set` and be ordered alongside declarations its author never saw. Nothing goes back: a `schema.Table` does not become a `CREATE TABLE`, and `Sorted` answers with tables in dependency order rather than with statements to run.

A host that assembles a `Set` from several libraries therefore holds the creation **order** and no DDL. The piece it is missing is one function:

```go
plan, err := migrate.Plan(postgres.Dialect{}, *set)
```

`Plan` is `Sorted` plus one `CreateTable` per table, with the indexes after all of them; `migrate.CreateTable` is the single-table form, for a host that interleaves statements of its own.

That function lives in `contrib/migrate` rather than here, and the boundary is a decision rather than an omission. Rendering DDL means knowing how one engine spells a portable type, so `migrate` has to know engine names, and a separate module is how a caller opts into that instead of paying for it by importing the builder. The cost of crossing is small and worth stating, because it is easy to assume otherwise: `contrib/migrate` requires only the root module and no third-party package at all, so opting in is a line in `go.mod`.

Standing in the root module alone, what you write instead is a side map from table name to the statement that creates it, walked against `Sorted`:

```go
byName := map[string]string{"app_user": userDDL, "invoice": invoiceDDL}

tables, err := set.Sorted()
for _, t := range tables {
	fmt.Println(byName[t.Name])
}
```

That is the whole of the glue, and it is worth knowing it is that small. It is also a second place where every table name is written down, one that no gate compares against the declarations — which is the thing `Plan` exists to remove.

## Bridging to the query builder

A table is declared once, and the query builder gets its columns from the declaration rather than a second copy that can drift:

```go
users := libraryTables()[0]

email, err := schema.Col[string](users, "email")
q, err := stmt.NewSelect(d).Exprs(email).From(users.Ref()).Build()
```

`Ref()` returns the `expr.Table`. `Col` is a function rather than a method because Go methods cannot take type parameters, and the type argument is checked against the declaration:

```go
schema.Col[int](users, "email")
// schema: Go type does not match the declared column type: column is text, which is string, but was asked for int
```

Asking for an `int` from a text column is a scan failure in production and an error where it is written here. A `NOT NULL` column asked for as a pointer is rejected the same way.

### Declaring columns at package level

A library declares its columns in a `var` block, and package initialisation has no error path, so the two-value `Col` does not fit there:

```go
var UserEmail, err = schema.Col[string](UserTable, "email")   // and now what?
```

`MustCol` is that same call for that one situation, panicking instead of returning an error:

```go
var (
	UserID    = schema.MustCol[string](UserTable, "id")
	UserEmail = schema.MustCol[string](UserTable, "email")
)
```

It refuses exactly what `Col` refuses — a wrong Go type, a `NOT NULL` column asked for as a pointer, a column the table does not declare — and panics with `Col`'s own error, so the message names the table and the column.

Panicking is right here and nowhere else in this module, for the same reason `regexp.MustCompile` panics: the arguments are literals the author wrote, so a failure means the program is wrong rather than the input is bad, and it happens at package load, where the first run of any test hits it. Use `Col` wherever the table or the column name comes from outside the source, because there a failure is data and an error is the honest answer.

## Which direction declares which

There are two ways a table's declaration and its typed columns stay in step, and they run in opposite directions. Pick by one question: **does this package create its own tables?**

| | You write | You derive | Use it when |
|---|---|---|---|
| **DDL-first** | the columns, and the `CREATE TABLE` from them | the `schema.Table`, through `Describe()` | the package owns its DDL |
| **Schema-first** | the `schema.Table` | the typed columns, through `MustCol` | the tables already exist, made by migrations this package does not own |

DDL-first is the one to reach for, because the description is computed rather than maintained:

```go
var UserEmail = sqlb.Col[string](TableUser, "email")

create := pg.CreateTable(TableUser).Column(ddl.Column(UserID), ddl.Column(UserEmail).NotNull())
desc, err := create.Describe()   // the schema.Table, derived from the statement
```

Schema-first is the answer when there is no `CREATE TABLE` to derive from — a library reading tables somebody else's migration tool created. Then the `schema.Table` is the single source and `MustCol` hands out the query columns.

What both avoid is the third option, which is writing a column's name and type in two places that nothing compares. That drifts the first time somebody renames a column, and nothing fails until a query reaches the database.

## Validation

`Validate` reports **every** problem, not the first:

```
table "broken" declares column "id" twice
table "broken" has primary key column "absent" that it does not declare
```

A schema is usually written in one sitting, and being told about one typo only to discover the next is the slowest possible way to find four mistakes. `Set.Validate` runs it for every table and adds the cross-table checks.

`Col` and `MustCol` run a second check, per column and at the moment a typed column is declared: they return `ErrTypeMismatch` when the Go type asked for cannot hold what the column declares, which includes asking for a pointer type on a `NOT NULL` column and asking for a `string` where the declaration says `TypeInt`. A `Raw` column is not checked, because this package does not know what an engine's own spelling means; that is the stated cost of the escape hatch.
