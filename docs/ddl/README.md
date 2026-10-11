# `ddl` — creating, changing and removing tables

```go
import "github.com/ubgo/sqlb/ddl"
```

Builds `CREATE TABLE`, `ALTER TABLE` and `DROP TABLE` over the same `Col[T]` values a library's queries already use, so a column cannot be created under one name or type and read under another.

It is **authored** DDL, never derived. A builder renders the statement its author wrote; nothing here looks at Go declarations and decides what the table should be, and which statements run, in what order and when, stays the application's decision. An engine package spells the constructors `pg.CreateTable(t)` and `sqlite.CreateTable(t)`; this page uses the layer directly, which is what those call.

## Contents

- [Setup](#setup)
- [Creating a table](#creating-a-table)
- [Column types](#column-types)
- [Defaults](#defaults)
- [Constraints](#constraints)
- [Postgres: keys, identity and generated columns](#postgres-keys-identity-and-generated-columns)
- [Changing a table](#changing-a-table)
- [Postgres: changing a table in place](#postgres-changing-a-table-in-place)
- [Postgres: schemas, extensions, enums, indexes and comments](#postgres-schemas-extensions-enums-indexes-and-comments)
- [Postgres: sequences and views](#postgres-sequences-and-views)
- [Postgres: triggers and partitions](#postgres-triggers-and-partitions)
- [Postgres: policies, domains, collations and statistics](#postgres-policies-domains-collations-and-statistics)
- [SQLite: keys, STRICT and what ADD COLUMN refuses](#sqlite-keys-strict-and-what-add-column-refuses)
- [SQLite: indexes, views, triggers and PRAGMA](#sqlite-indexes-views-triggers-and-pragma)
- [SQLite: FTS5 and the table rebuild](#sqlite-fts5-and-the-table-rebuild)
- [Removing tables](#removing-tables)
- [What Build refuses](#what-build-refuses)
- [A Script, not a string](#a-script-not-a-string)
- [Engine clauses](#engine-clauses)
- [Clause order is the engine's, not the caller's](#clause-order-is-the-engines-not-the-callers)
- [Describing a table](#describing-a-table)
- [Naming constraints](#naming-constraints)
- [Postgres 18: virtual columns, unenforced constraints and temporal keys](#postgres-18-virtual-columns-unenforced-constraints-and-temporal-keys)
- [What will not compile](#what-will-not-compile)
- [What is not built](#what-is-not-built)

## Setup

The snippets below use one table and the columns a library would already export for its queries.

```go
import (
	"time"

	"github.com/ubgo/sqlb/ddl"
	"github.com/ubgo/sqlb/dialect/postgres"
	"github.com/ubgo/sqlb/dialect/sqlite"
	"github.com/ubgo/sqlb/expr"
	"github.com/ubgo/sqlb/render"
	"github.com/ubgo/sqlb/schema"
)

var (
	accounts   = expr.Table{Name: "account", Alias: "a"}
	accID      = expr.Col[int64](accounts, "id")
	accEmail   = expr.Col[string](accounts, "email")
	accNick    = expr.Col[*string](accounts, "nick")
	accHandle  = expr.Col[*string](accounts, "handle")
	accStatus  = expr.Col[string](accounts, "status")
	accPhone   = expr.Col[*string](accounts, "phone")
	accBalance = expr.Col[float64](accounts, "balance")
	accCreated = expr.Col[time.Time](accounts, "created_at")
)

// Keep the imports used in a block that only declares.
var (
	_ = postgres.Dialect{}
	_ = sqlite.Dialect{}
	_ render.Dialect
	_ = schema.TypeDecimal
	_ ddl.Script
)
```

## Creating a table

`NewCreateTable` has no `Build` until `Column` has been called, because a table with no column is rejected by SQLite and useless on Postgres. That is a type, not a check: `CreateTable` has no `Build` to call, and `Column` returns a `CreateTableReady`, which has one.

```go
cols := []ddl.ColumnSpec{
	ddl.Column(accID), ddl.Column(accEmail), ddl.Column(accNick),
	ddl.Column(accStatus).Default("active"),
}
ddl.NewCreateTable(postgres.Dialect{}, accounts).IfNotExists().Column(cols[0], cols[1:]...).Build()
ddl.NewCreateTable(sqlite.Dialect{}, accounts).IfNotExists().Column(cols[0], cols[1:]...).Build()
```

```sql
CREATE TABLE IF NOT EXISTS "account" ("id" BIGINT NOT NULL, "email" TEXT NOT NULL, "nick" TEXT, "status" TEXT NOT NULL DEFAULT 'active')
CREATE TABLE IF NOT EXISTS "account" ("id" INTEGER NOT NULL, "email" TEXT NOT NULL, "nick" TEXT, "status" TEXT NOT NULL DEFAULT 'active')
```

`ddl.Column` makes a `ColumnDef` from a column. Nullability comes from the Go type: a `*string` column is nullable and a `string` column is `NOT NULL`, which is the same thing the query side already believes. `Nullable` and `NotNull` override it when the table and the Go type disagree on purpose.

`IfNotExists` checks the name only, not the shape. A table that exists with other columns is left as it is, which is what a library running its install on every boot wants and exactly why it is not a migration tool.

A column declared on another table is refused with `ErrForeignColumn`, and one defined twice with `ErrDuplicateColumn`. The table is compared by schema and name, not alias, so a plugin borrowing a table under its own alias is still the same table.

## Column types

The type is inferred from Go through the spelling table in `schema`, the same one `contrib/migrate` and `contrib/vet` read: `int64` is `BIGINT` on Postgres and `INTEGER` on SQLite. A Go type with no obvious SQL type, such as `json.RawMessage`, fails with `ErrNoDefaultType` rather than guessing. `As` overrides the inference, with a `Portable` type the table spells per engine or a `RawType` written out for one engine.

```go
ddl.NewCreateTable(postgres.Dialect{}, accounts).Column(
	ddl.Column(accBalance).As(ddl.Portable[float64](schema.TypeDecimal)),
	ddl.Column(accEmail).As(ddl.RawType[string]("citext")),
).Build()
```

```sql
CREATE TABLE "account" ("balance" NUMERIC NOT NULL, "email" citext NOT NULL)
```

Both are an `SQLType[T]`, typed to the column, so a `Portable[int64]` cannot be given to a `string` column. A portable type the dialect has no spelling for fails with `ErrNoTypeTable`, and the answer is `RawType`.

## Defaults

`Default` takes a value of the column's type and writes it as a literal, because no engine accepts a placeholder in DDL. `DefaultExpr` takes an expression and parenthesises it, which SQLite requires of any default that is not a literal and Postgres accepts.

A column that stores JSON, declared `As(ddl.Portable[json.RawMessage](schema.TypeJSON))` or `As(ddl.RawJSONType[T]("jsonb"))` for a Go type such as `pg.JSON[T]`, takes its default as JSON: a `json.RawMessage` or string as written, a `driver.Valuer` through `Value`, anything else through `json.Marshal`. It is checked with `json.Valid`, so invalid JSON fails `Build` with `ErrInvalidJSON` on every engine, and written as `('<json>')`, the one spelling all three accept: MySQL refuses a JSON column's plain literal default, measured. A type of another package that stores JSON implements `ddl.JSONStorer` to join the two. MySQL still refuses a backslash in any literal, so a JSON default with an escaped character cannot be written for MySQL; bind it at insert instead.

```go
ddl.Column(profile).As(ddl.Portable[json.RawMessage](schema.TypeJSON)).Default(json.RawMessage(`{"theme":"dark"}`))
```

```sql
"profile" JSONB NOT NULL DEFAULT ('{"theme":"dark"}')
```

```go
ddl.NewCreateTable(sqlite.Dialect{}, accounts).Column(
	ddl.Column(accCreated).DefaultExpr(expr.Literal[time.Time]("CURRENT_TIMESTAMP")),
).Build()
```

```sql
CREATE TABLE "account" ("created_at" TEXT NOT NULL DEFAULT (CURRENT_TIMESTAMP))
```

## Constraints

`OneOf` writes a `CHECK` from a column and the values it may hold, and `Check` from any predicate. Each is a `Constraint`, named by a `ddl.TableConstraint` (see [Naming constraints](#naming-constraints)). Inside a `CHECK` the column is written bare, since a table's own constraint cannot name it through an alias; an empty `OneOf` is refused with `ErrEmptyOneOf` rather than rendering `IN ()`.

```go
ddl.NewCreateTable(postgres.Dialect{}, accounts).
	Column(ddl.Column(accStatus)).
	Constraint(ddl.OneOf(ddl.TableConstraint{Table: accounts, Name: "account_status_known"}, accStatus, "active", "banned")).Build()
```

```sql
CREATE TABLE "account" ("status" TEXT NOT NULL, CONSTRAINT "account_status_known" CHECK ("status" IN ('active', 'banned')))
```

Keys, foreign keys and identity columns are not universal, since ClickHouse has no foreign key and its `PRIMARY KEY` means sort order, so they live in the engine packages and attach through `Clause`. The next section is Postgres's.

## Postgres: keys, identity and generated columns

`pg` supplies the clauses a real table needs. Each names its columns by value, never by string, and the statement checks at `Build` that the table defines every one: a key on a column it does not define fails with `ErrUnknownColumn`, and one on another table's column with `ErrForeignColumn`.

```go
users := expr.Table{Name: "users"}
userID := expr.Col[int64](users, "id")
email := expr.Col[string](users, "email")
emailLower := expr.Col[string](users, "email_lower")
sessions := expr.Table{Name: "sessions"}
sessionUser := expr.Col[int64](sessions, "user_id")

printScript(pg.CreateTable(users).IfNotExists().
	Column(ddl.Column(userID), ddl.Column(email), ddl.Column(emailLower)).
	Clause(
		pg.Identity(userID, pg.Always),
		pg.GeneratedStored(emailLower, expr.Lower(email)),
		pg.PrimaryKey(userID),
		pg.Unique(pg.TableConstraint{Table: users, Name: "users_email_lower_key"}, emailLower),
	).Build())
printScript(pg.CreateTable(sessions).IfNotExists().
	Column(ddl.Column(sessionUser)).
	Clause(pg.References(sessionUser, userID).OnDelete(pg.RefCascade)).Build())
```

```sql
CREATE TABLE IF NOT EXISTS "users" ("id" BIGINT NOT NULL GENERATED ALWAYS AS IDENTITY, "email" TEXT NOT NULL, "email_lower" TEXT GENERATED ALWAYS AS (lower("email")) STORED, PRIMARY KEY ("id"), CONSTRAINT "users_email_lower_key" UNIQUE ("email_lower"))
CREATE TABLE IF NOT EXISTS "sessions" ("user_id" BIGINT NOT NULL, FOREIGN KEY ("user_id") REFERENCES "users" ("id") ON DELETE CASCADE)
```

What each one checks at compile time:

- `pg.Identity` takes only `int16`, `int32` and `int64` columns, the types Postgres accepts. A column with an identity cannot also have a default, which `Build` refuses with `ErrDefaultOnGenerated`.
- `pg.GeneratedStored(col, e)` takes an expression of the column's own Go type. `NOT NULL` is not written on a generated column, because the application never writes it and the expression decides.
- `pg.References(local, ref)` and `pg.RefPair(local, ref)` take two columns of one Go type, so a `bigint` referencing a `text` column does not compile. A composite key is `pg.ForeignKey(name, pg.RefPair(a, x), pg.RefPair(b, y))`, a list of pairs, because Go cannot express two variadic lists of matching types. Its options are `OnDelete` and `OnUpdate` with `pg.RefCascade`, `RefRestrict`, `RefSetNull`, `RefSetDefault` or `RefNoAction`, and `Deferrable(pg.InitiallyDeferred)`.
- `pg.Unique(name, cols...)` takes a name, because a unique constraint is what a later migration drops. `NullsNotDistinct()` makes two NULLs collide.
- `pg.Exclude(name, pg.GiST, pg.ExcludeOn(room, pg.OpEquals), pg.ExcludeOn(during, pg.OpOverlaps))` is the constraint `UNIQUE` cannot express: no two rows where every element's operator is true, a room booked for two overlapping ranges. `Where` limits it to live rows. `ExcludeOp` is open, as index methods are, with `OpOverlaps`, `OpEquals` and `OpAdjacent` named; an equality element on a scalar needs the `btree_gist` extension.
- `pg.CreateTableLike(t, src, opts...)` copies a table's columns, and each `LikeOption` names one more thing to copy: `IncludingDefaults`, `IncludingConstraints`, `IncludingGenerated`, `IncludingIdentity`, `IncludingIndexes`, `IncludingComments`, `IncludingStatistics`, `IncludingStorage` and `IncludingCompression`. There is no "everything" option on purpose: without `IncludingGenerated`, Postgres silently makes a generated column a plain one, which the execution test shows.
- `pg.ColumnCollate(col, "C")` goes straight after the type and only on a string column. `pg.Unlogged()` makes an unlogged table and `pg.Cascade()` a `DROP TABLE ... CASCADE`.

All of it runs against Postgres 17 in `contrib/driver-pgx`, where the server enforces each clause: the identity refuses an explicit id, the deferred composite key raises `foreign_key_violation` when checked, and a second NULL collides under `NULLS NOT DISTINCT`.

## Changing a table

`NewAlterTable` takes an `Action`: `AddColumn`, `DropColumn` and `RenameColumn`. Postgres applies several actions in one statement and the builder groups them, except a rename, which Postgres's grammar keeps as a statement of its own. SQLite takes one action per statement. The code is the same for both, and so is the order.

```go
actions := []ddl.Action{
	ddl.AddColumn(ddl.Column(accPhone)),
	ddl.AddColumn(ddl.Column(accBalance).Default(0)),
	ddl.RenameColumn(accNick, accHandle),
	ddl.DropColumn(accEmail),
}
ddl.NewAlterTable(postgres.Dialect{}, accounts).Action(actions[0], actions[1:]...).Build()
ddl.NewAlterTable(sqlite.Dialect{}, accounts).Action(actions[0], actions[1:]...).Build()
```

```sql
-- Postgres
ALTER TABLE "account" ADD COLUMN "phone" TEXT, ADD COLUMN "balance" DOUBLE PRECISION NOT NULL DEFAULT 0;
ALTER TABLE "account" RENAME COLUMN "nick" TO "handle";
ALTER TABLE "account" DROP COLUMN "email"
-- SQLite
ALTER TABLE "account" ADD COLUMN "phone" TEXT;
ALTER TABLE "account" ADD COLUMN "balance" REAL NOT NULL DEFAULT 0;
ALTER TABLE "account" RENAME COLUMN "nick" TO "handle";
ALTER TABLE "account" DROP COLUMN "email"
```

`RenameColumn` takes two columns of one Go type, so renaming a `*string` column to an `int64` one does not compile. The form with no action, `AlterTable`, has no `Build`; `Action` or `Clause` returns an `AlterTableReady`, which does.

## Postgres: changing a table in place

The Postgres `ALTER TABLE` actions go on `pg.AlterTable(t)` through `Clause`, in the order written, and each one that names a column is checked against the table. Adding a `NOT NULL` column to a table that has rows is three steps, because Postgres checks every row when `NOT NULL` is set; and a foreign key on a large table is added `NOT VALID` and validated in a second transaction, so the lock that blocks writes is brief:

```go
users := expr.Table{Name: "users"}
userID := expr.Col[int64](users, "id")
tzNullable := expr.Col[*string](users, "tz")
tz := expr.Col[string](users, "tz")
sessions := expr.Table{Name: "sessions"}
sessionUser := expr.Col[int64](sessions, "user_id")

printScript(pg.AlterTable(users).Clause(pg.AddColumnIfNotExists(ddl.Column(tzNullable))).Build())
// ... backfill with pg.Update(users).Set(sqlb.Set(tz, "UTC")) ...
printScript(pg.AlterTable(users).Clause(pg.SetNotNull(tz), pg.SetDefault(tz, "UTC")).Build())
userFK := pg.TableConstraint{Table: sessions, Name: "sessions_user_fk"}
ownerFK := pg.TableConstraint{Table: sessions, Name: "sessions_owner_fk"}
printScript(pg.AlterTable(sessions).Clause(
	pg.AddConstraintNotValid(pg.ForeignKey(userFK, pg.RefPair(sessionUser, userID))),
).Build())
printScript(pg.AlterTable(sessions).Clause(
	pg.ValidateConstraint(userFK),
	pg.RenameConstraint(userFK, ownerFK),
).Build())
```

```sql
ALTER TABLE "users" ADD COLUMN IF NOT EXISTS "tz" TEXT
ALTER TABLE "users" ALTER COLUMN "tz" SET NOT NULL, ALTER COLUMN "tz" SET DEFAULT 'UTC'
ALTER TABLE "sessions" ADD CONSTRAINT "sessions_user_fk" FOREIGN KEY ("user_id") REFERENCES "users" ("id") NOT VALID
ALTER TABLE "sessions" VALIDATE CONSTRAINT "sessions_user_fk";
ALTER TABLE "sessions" RENAME CONSTRAINT "sessions_user_fk" TO "sessions_owner_fk"
```

The column actions are `AddColumnIfNotExists` (which takes column clauses such as `pg.ColumnCollate` for the added column, rendered through `ddl.WriteColumnWith`), `DropColumnIfExists` with `Cascade`, `SetDefault`, `SetDefaultExpr`, `DropDefault`, `SetNotNull`, `DropNotNull`, `AlterType(col, type).Using(e)` and `AddIdentity`. The generated column and identity a table was created with are changed by `SetExpression(col, e)` (Postgres 17 and later), `DropExpression(col).IfExists()`, `SetIdentityKind(col, kind)`, `RestartIdentity(col).With(n)` and `DropIdentity(col).IfExists()`, which are the inverses and adjustments of `pg.GeneratedStored` and `pg.Identity`. Each is typed to the column: `SetDefault` takes a value of the column's Go type, `AlterType` a type for it, `SetExpression` and `Using` an expression of it, and `RestartIdentity(col).With(n)` a value of it, since `col` is the column's new declaration.

The constraint actions are `AddConstraint`, which takes any `pg.Constraint` (a primary key, a unique key, a foreign key or a check), `AddConstraintNotValid`, which takes only a `pg.Validatable` (a foreign key or a check, the two Postgres can add unchecked), so `NOT VALID` on a unique key does not compile; `ValidateConstraint(c)`, `DropConstraint(c).IfExists().Cascade()` and `RenameConstraint(from, to)`, each over a declared `TableConstraint`. `AddUniqueUsingIndex(c, ix)` and `AddPrimaryKeyUsingIndex(c, ix)` promote an index already built to a named constraint, which is the cheap second half of a zero-downtime key migration: build the index with `CreateIndex(...).Concurrently()` outside a transaction, then attach it here. Postgres writes the index name bare there, and an index cannot be created outside its table's schema at all, so an `Index` whose `Schema` disagrees with the table's fails `Build` with `ErrIndexSchema` rather than having the field dropped in silence; declare the schema on both values or on neither. The table actions are `RenameTo`, `SetSchema` and `EnableRowLevelSecurity`.

`RENAME CONSTRAINT`, `RENAME TO` and `SET SCHEMA` are standalone forms in Postgres's grammar and each gets its own statement; the rest share one. An engine action that belongs to one table says so by implementing `ddl.TableNamer`, and one that names columns by implementing `ddl.ColumnNamer`, and the statement refuses it on another table with `ErrForeignColumn`. Every action runs against Postgres 17 in `contrib/driver-pgx`, read back from the catalog.

## Postgres: schemas, extensions, enums, indexes and comments

A named object is a value declared once, like a table: `pg.Schema`, `pg.Extension`, `pg.Enum[T]` and `pg.Index`. Every statement that creates, renames, drops or comments on one takes the value, so no name is typed twice, and each has a `Build` that returns a `Script`.

```go
auth := pg.Schema{Name: "auth"}
statusType := pg.Enum[status]{Schema: "auth", Name: "user_status"}
users := expr.Table{Schema: "auth", Name: "users"}
email := expr.Col[string](users, "email")
stat := expr.Col[status](users, "status")
deletedAt := expr.Col[*time.Time](users, "deleted_at")
docs := expr.Table{Schema: "auth", Name: "documents"}
embedding := expr.Col[vector.Vector](docs, "embedding")

for _, b := range []interface{ Build() (ddl.Script, error) }{
	pg.CreateSchema(auth).IfNotExists(),
	pg.CreateExtension(vector.Extension).IfNotExists(),
	pg.Guarded(pg.CreateEnum(statusType, statusActive, statusBanned)),
	pg.CreateTable(users).IfNotExists().Column(ddl.Column(email), ddl.Column(stat).As(statusType).Default(statusActive), ddl.Column(deletedAt)),
	pg.CreateIndex(pg.Index{Schema: "auth", Name: "users_live_email"}, users).Unique().IfNotExists().
		On(pg.IndexExpr(expr.Lower(email))).Where(expr.IsNull(deletedAt)),
	pg.CreateIndex(pg.Index{Schema: "auth", Name: "documents_embedding"}, docs).IfNotExists().
		Using(vector.HNSW).On(pg.IndexKey(embedding).Opclass(vector.CosineOps)).With(vector.M(16)),
	pg.CommentOn(statusType).Is("Account lifecycle; add a value with AlterEnum."),
} {
	printScript(b.Build())
}
```

```sql
CREATE SCHEMA IF NOT EXISTS "auth"
CREATE EXTENSION IF NOT EXISTS "vector"
DO $sqlb$ BEGIN CREATE TYPE "auth"."user_status" AS ENUM ('active', 'banned'); EXCEPTION WHEN duplicate_object THEN NULL; END $sqlb$
CREATE TABLE IF NOT EXISTS "auth"."users" ("email" TEXT NOT NULL, "status" "auth"."user_status" NOT NULL DEFAULT 'active', "deleted_at" TIMESTAMPTZ)
CREATE UNIQUE INDEX IF NOT EXISTS "users_live_email" ON "auth"."users" ((lower("email"))) WHERE "deleted_at" IS NULL
CREATE INDEX IF NOT EXISTS "documents_embedding" ON "auth"."documents" USING "hnsw" ("embedding" "vector_cosine_ops") WITH ("m" = 16)
COMMENT ON TYPE "auth"."user_status" IS 'Account lifecycle; add a value with AlterEnum.'
```

**Schemas and extensions.** `CreateSchema`, `DropSchema` with `Cascade`, `RenameSchema`, `CreateExtension` and `DropExtension`, each with the `IfNotExists` or `IfExists` Postgres accepts. `CreateExtension` needs a role allowed to install one, which a library's own role often is not; that is why it is a statement of its own.

**Enums.** `pg.Enum[T]` binds the type to a Go named type, so every label is a `T` and a typo is a compile error, and it is a `ddl.SQLType[T]`, so `ddl.Column(c).As(statusType)` compiles only on a `Col[T]`. `CreateEnum(e, labels...)` has no `IfNotExists` because Postgres has none. `AlterEnum(e)` starts an `AlterEnumQuery`, completed by `AddValue(v)`, an `AddValueStatement` with `Before`, `After` and `IfNotExists`, or by `RenameValue` or `RenameTo`, each an `AlterEnumStatement`. A label added inside a transaction cannot be used until that transaction commits, which the execution test confirms. `DropType(e)` drops it, with `IfExists` and `Cascade`.

**Composite types.** `pg.Composite[T]` is a named row type, declared once and a `ddl.SQLType[T]` like an enum, so `ddl.Column(c).As(money)` compiles only on a `Col[T]`. `CreateComposite(c, attrs...)` takes `pg.Attr(name, type)` attributes, each typed to its own Go type, and is a `Guardable`, since Postgres has no `IF NOT EXISTS` for it. `AlterComposite(c)` is completed by `AddAttribute`, `DropAttribute`, `AlterAttributeType` or `RenameAttribute`, each an `AlterCompositeStatement`; `DropComposite(c)` drops it, and `CommentOn` takes it.

**Guarded.** `pg.Guarded(s)` makes a create Postgres cannot write `IF NOT EXISTS` for safe to repeat, by catching the one condition that statement raises on a second run. It accepts only a `pg.Guardable`: `CreateEnum`, and `AddConstraintStatement(c)`, which is `ADD CONSTRAINT` as a statement of its own on the table the constraint's `TableConstraint` names and refuses an unnamed constraint with `ErrUnnamedConstraint`, because an unnamed foreign key raises nothing on a second run and is added twice. The condition differs by kind, measured: a type, a foreign key or a check raises `duplicate_object`; a unique or primary key raises `duplicate_table`, because the index behind it is a relation. Like `IF NOT EXISTS`, it checks the name only. The dollar-quote tag changes if the statement contains it.

**Indexes.** `CreateIndex(ix, t)` has no `Build` until `On` gives at least one key: `IndexKey(col)` for a column, `IndexExpr(e)` for an expression, each with `Desc`, `Asc`, `NullsFirst`, `NullsLast` and `Opclass`. `Unique`, `IfNotExists`, `Concurrently` and `Using` can come before or after `On`; `Include`, `With`, `Where` and `NullsNotDistinct` after. The methods are `pg.BTree`, `Hash`, `GiST`, `SPGiST`, `GIN` and `BRIN`, an open `IndexMethod` set an extension adds to, and the jsonb classes are `pg.JSONBPathOps` and `pg.JSONBOps`. `NewOpClass[T]`, `NewIndexParam` and `IndexElement` are how an extension package adds its own. A key or included column from another table fails with `ErrForeignColumn`.

`Concurrently` builds without blocking writes, and Postgres refuses it inside a transaction, so its `Statement` has `Transactional` false and a runner must execute it alone. `RenameIndex` and `DropIndex` (one statement per index, with `IfExists`, `Concurrently` and `Cascade`) complete the set.

**Comments.** `CommentOn(o)` takes any declared object, an `Index`, `Schema`, `Extension` or `Enum`, so the object kind comes from the Go type; `CommentOnTable` and `CommentOnColumn` take a table and a column. `Is(text)` writes the text as a literal, so quotes and backslashes are stored exactly, and `IsNull()` removes the comment. A name missing from any object fails with `ErrEmptyName`.

Everything in this section runs on Postgres 17 in `contrib/driver-pgx`, twice where it claims to be idempotent, and is read back from `pg_indexes`, `pg_enum` and the comment functions.

## Postgres: sequences and views

**Sequences.** `pg.Sequence[T]` is typed to the integer it produces, `int16`, `int32` or `int64` or a named type over one, and `CreateSequence` writes `AS smallint`, `integer` or `bigint` from that type. `IfNotExists`, `IncrementBy`, `MinValue`, `MaxValue` and `Start` take values of `T`, and `Cache` a count. `pg.NextVal(s)` is an `Expr[T]`, so `ddl.Column(c).DefaultExpr(pg.NextVal(s))` compiles only on a column of the sequence's type. `AlterSequence(s)` is completed by `Restart(n)`, `OwnedBy(col)`, which ties the sequence to a column of its type, `OwnedByNone()`, `IncrementBy`, `MinValue`, `MaxValue`, `Cache`, `Cycle` or `NoCycle`, each an `AlterSequenceStatement` with `IfExists` on which the same options chain into one statement, as Postgres allows. `SetType()` writes `AS` from the `Sequence[T]`'s own Go type, so widening a sequence is a change to its declaration rather than a string; `StartWith(n)` changes the value a bare `RESTART` returns to, and `SetLogged()` and `SetUnlogged()` its durability. `RenameTo(to)` takes the new `Sequence[T]` and must stand alone: chaining an option onto it fails with `ErrRenameAlone`, and a cache below 1 with `ErrBadCache`. `DropSequence(s)`, like `DropView` and `DropMaterializedView`, is a `DropObjectStatement` with `IfExists` and `Cascade`. Prefer an identity column for a new table.

**Views.** A view's body is a `*pg.SelectQuery`, the same builder as any query, rendered in inline mode, so the values it bound become literals and a SQLite select does not compile as a Postgres view's body:

```go
body := pg.Select(num, status).From(invoices).Where(sqlb.Eq(status, "open"))
pg.CreateView(open).OrReplace().As(body).WithCheckOption(pg.Local)
// CREATE OR REPLACE VIEW "open_invoices" AS SELECT "i"."number", "i"."status" FROM "invoices" AS "i" WHERE ("i"."status" = 'open') WITH LOCAL CHECK OPTION
```

`CreateView(v)` has no `Build` until `As` gives the body. `OrReplace` is its idempotent form, since Postgres has no `CREATE VIEW IF NOT EXISTS`, and it succeeds only when the new body keeps every existing column. `WithCheckOption(pg.Local)` or `pg.Cascaded` refuses a write through the view that the view would not show. `RenameView` renames it. `SetViewColumnDefault(v, col, value)` and `DropViewColumnDefault(v, col)` set a view column's default, a literal of the column's type, which an `INSERT` through an updatable view uses; each is an `AlterViewStatement`. `AlterViewOptions(v)` sets and resets the view's own options, `CheckOption(o)`, `SecurityBarrier(on)` and `SecurityInvoker(on)`, the last of which runs the view's query with the caller's privileges so their row-level policies apply; `Reset(pg.ViewCheckOption, …)` puts them back, and because Postgres keeps `SET` and `RESET` apart in its grammar a builder that does both renders a `Script` of two.

**Materialized views.** `CreateMaterializedView(v)` with `IfNotExists`, `As(body)` and `WithNoData()`, which creates it empty and unreadable until the first refresh. `RefreshMaterializedView(v)` recomputes it, and `Concurrently()` does so without blocking reads, which needs a unique index on the view; index one with `pg.CreateIndex` over a `sqlb.Table` naming the view. `CommentOn` takes a `View`, `MaterializedView` or `Sequence` too, and `pg.SetObjectSchema(o, schema)` moves any `pg.Movable` — a view, a materialized view, a sequence, an enum, a composite type or a domain — into another schema, writing the `ALTER` keyword its Go type names.

All of it runs on Postgres 17 in `contrib/driver-pgx`: the sequence's type and step are read back, the check option refuses a row, and the materialized view refreshes concurrently.

## Postgres: triggers and partitions

**Trigger functions.** `pg.Function` is declared once, and `CreateTriggerFunction(f, body)` writes a PL/pgSQL function returning `trigger`. The body is the one place in this package a whole program is a string: the library author writes it, it is never built from input, and it is dollar-quoted with a tag it does not contain. `OrReplace` is the idempotent form and `DropFunction(f)` drops it. Functions other than trigger functions are `pg.CreateFunction(f, lang, body)`: the signature is typed, because a parameter is a name and a `ddl.SQLType[T]` like a column, and the body stays a string, because it is a program in another language. `Params` lives on the `pg.Function` value since a function's argument types are part of the name every other statement identifies it by, and `CreateTriggerFunction` refuses a `Function` that declares any.

**Triggers.** `pg.Trigger` names a trigger and its table, since the name is unique per table. `CreateTrigger(tr)` has no `Build` until a timing, `Before(events...)` or `After(events...)`, and a function, `Execute(f)`, are given; between them come `ForEachRow` (without it the trigger fires once per statement) and `When(p)`. The events are `OnInsert`, `OnUpdate`, `OnUpdateOf(cols...)` and `OnDelete`, and `pg.Old(col)` and `pg.New(col)` are the column's values before and after, typed to it:

```go
pg.CreateTrigger(tr).OrReplace().Before(pg.OnUpdateOf(email, nick)).ForEachRow().
	When(pg.IsDistinctFrom(pg.Old(email), pg.New(email))).Execute(fn)
// CREATE OR REPLACE TRIGGER "users_touch" BEFORE UPDATE OF "email", "nick" ON "users" FOR EACH ROW WHEN ((OLD."email" IS DISTINCT FROM NEW."email")) EXECUTE FUNCTION "touch"()
```

The stages are `CreateTriggerQuery`, `TimedTriggerQuery` and `CreateTriggerStatement`. `RenameTrigger` and `DropTrigger` complete the set, and `CommentOn` takes a `Function` or a `Trigger`.

**Partitions.** `pg.PartitionByRange(cols...)`, `PartitionByList(col)` and `PartitionByHash(cols...)` are `CREATE TABLE` clauses whose key is checked against the table's columns. `CreatePartition(child, parent)` has no `Build` until `For(bound)` gives a `PartitionBound`: `FromTo(key, from, to)` and `ListValues(key, values...)`, typed to the key so a timestamp partition cannot be bounded by numbers, `HashOf(modulus, remainder)`, or `DefaultPartition()`. `pg.AttachPartition(child, bound)` is an `ALTER TABLE` action of its own. `DetachPartition(parent, child)` is a statement, because `Concurrently` makes it non-transactional, which an action cannot say; `Finalize` completes an interrupted concurrent detach.

Executed on Postgres 17: the trigger fires once over four updates, as its `WHEN` and `UPDATE OF` say, rows land in the partition their bound names, and a concurrent detach is refused inside a transaction.

## Postgres: policies, domains, collations and statistics

**Row-level security.** A policy is a pair of predicates, which is where typed DDL pays off most: `USING` and `WITH CHECK` are ordinary predicates, so comparing a `bigint` owner with a text setting does not compile. `EnableRowLevelSecurity` turns it on and `DisableRowLevelSecurity` off; `ForceRowLevelSecurity` applies the policies to the table's owner too, which Postgres otherwise exempts, and is what a library needs when its own role owns its tables, and `NoForceRowLevelSecurity` exempts the owner again.

```go
me := sqlb.Cast[int64](pg.CurrentSetting("app.user_id"), "bigint")
pg.AlterTable(sessions).Clause(pg.EnableRowLevelSecurity())
pg.Guarded(pg.CreatePolicy(pg.Policy{Name: "sessions_owner", Table: sessions}).For(pg.AllCommands).To(pg.RolePublic).
	Using(sqlb.EqExpr(owner, me)).WithCheck(sqlb.EqExpr(owner, me)))
```

`pg.Policy` names a policy and its table. `CreatePolicy` takes `Permissive` (the default) or `Restrictive`, `For` a `PolicyCommand` (`AllCommands`, `SelectCommand`, `InsertCommand`, `UpdateCommand`, `DeleteCommand`), `To` any `RoleRef` — a `pg.Role` the deployment declared or one of the keyword roles `RolePublic`, `RoleCurrentUser`, `RoleSessionUser`, `Using` and `WithCheck`. It has no `IF NOT EXISTS`, so it is a `Guardable`. `AlterPolicy(p)` is completed by `Using`, `WithCheck` or `RenameTo`, which changes a policy's rule without a window in which it is dropped, and `DropPolicy` removes it. A policy does nothing until row security is enabled, and nothing to the table's owner; the execution test switches to a role that does not own the table to see it apply. `AlterPolicy(p).To(role)` replaces the whole set of roles, and `SetOwner(o, role)` hands a declared object to one; this package still creates no role and grants no privilege.

**Domains.** `pg.Domain[T]` is a `ddl.SQLType[T]` like an enum, so `ddl.Column(email).As(emailDomain)` compiles only on a `Col[T]`. `CreateDomain(d, base)` takes a base type for the same Go type, with `NotNull` and named `Check(c, pred)` constraints, `c` a `pg.DomainConstraint[T]`, written over `pg.DomainValue[T]()`, the `VALUE` keyword typed to the domain. It is a `Guardable`. `AlterDomain(d)` is completed by `AddConstraint(c, pred)` (with `NotValid`), `ValidateConstraint` or `DropConstraint`; `DropDomain` removes it.

**Collations.** `CreateCollation(c, locale)` with `ICU`, `Nondeterministic` and `IfNotExists`, and `DropCollation`. A nondeterministic ICU collation is why collations are here at all: `pg.ColumnCollation(email, ci)` on a column with a unique constraint makes the email unique case-insensitively, with no `lower()` column, which the execution test shows by refusing `ada@X.IO` after `Ada@x.io`.

**Statistics.** `CreateStatistics(s, t, a, b, more...)` takes at least two columns, the fewest Postgres accepts, with `Kinds` (`NDistinct`, `Dependencies`, `MCV`) and `IfNotExists`; `DropStatistics` removes it. It tells the planner two columns are correlated.

**The rest.** `UpdateExtension(e)`, with `To(version)`, is `ALTER EXTENSION ... UPDATE`; `RenameMaterializedView` renames a materialized view. `CommentOn` takes a `Policy`, `Domain` or `Collation` too.

## SQLite: keys, STRICT and what ADD COLUMN refuses

`sqlite` supplies SQLite's own clauses, typed to its dialect so none of them compiles on a Postgres statement:

- `sqlite.PrimaryKey(cols...)` and `sqlite.Unique(cols...)`, each with `Named` and `OnConflict`, which takes the same `OrAction` constants as `InsertOr`: a key that resolves its own conflicts is SQLite's alone. A single `INTEGER` primary key is the rowid alias.
- `sqlite.AutoincrementKey(col)` is `INTEGER PRIMARY KEY AUTOINCREMENT`, written in the column's definition because that is the only place SQLite accepts it, and only on an integer column. Use it only when rowids must never be reused.
- `sqlite.References(local, ref)`, `ForeignKey(name, pairs...)` and `RefPair`, with `OnDelete`, `OnUpdate` (`sqlite.RefCascade`, `RefRestrict`, `RefSetNull`, `RefSetDefault`, `RefNoAction`) and `Deferred`. SQLite enforces them only with `PRAGMA foreign_keys` on, which `contrib/driver-sqlite` sets.
- `sqlite.ColumnCollate(col, sqlite.NoCase)`, with `Binary` and `RTrim` the other `Collation` values.
- `sqlite.GeneratedVirtual(col, e)` and `GeneratedStored(col, e)`.
- `sqlite.Strict()` makes SQLite enforce column types, which a library scanning into typed Go values wants, and `sqlite.WithoutRowID()` stores the table as its primary key's index. Both are written in SQLite's comma list, `) STRICT, WITHOUT ROWID`, through `ddl.ListedOption`.

The universal constraints still go through `Constraint`, so `ddl.OneOf` makes the same closed set a `CHECK ... IN` here that is a Postgres enum there. `sqlite.Length`, `Substr`, `SubstrN`, `IsNot` and `IsEqual`, and the core `sqlb.Upper`, exist for the checks and generated columns a table needs.

**What ADD COLUMN refuses.** SQLite's `ADD COLUMN` cannot add every column `CREATE TABLE` can, and says so at runtime with an error about syntax. `Build` refuses the same things instead, naming the rule: `ddl.ErrAddNotNull` for a `NOT NULL` column with no literal default, `ErrAddNonConstantDefault` for an expression default, `ErrAddConstraint` for a key on the added column, and `sqlite.ErrAddStored` for a stored generated column. The dialect declines `render.UnrestrictedAddColumn`, which Postgres implements, and a column says its `ColumnFacts` (`NotNull` and a `DefaultKind` of `DefaultNone`, `DefaultLiteral` or `DefaultExpression`) through `ddl.FactsReporter`.

Three of those four SQLite refuses only when the table has rows; on an empty table it accepts them. `Build` refuses them unconditionally on purpose: the empty table is the test database and the table with rows is production, so accepting them is how a migration passes CI and fails on deploy. `contrib/driver-sqlite` measures both halves.

`sqlite.AddColumn(spec, with...)` is `ADD COLUMN` with column clauses, a collation or a virtual generated expression, through the same checks as `ddl.AddColumn`; an engine's own add action uses `ddl.WriteAddColumn` for that. `sqlite.RenameTo(t)` renames the table.

## SQLite: indexes, views, triggers and PRAGMA

**Indexes.** `sqlite.Index` is declared once. `sqlite.CreateIndex(ix, t)` takes `Unique` and `IfNotExists`, has no `Build` until `On` gives a key (`IndexKey(col)` or `IndexExpr(e)`, each an `IndexElement` with `Desc`, `Asc` and `Collate`), and takes `Where` for a partial index. `DropIndex(ix)` is a `DropStatement` with `IfExists`, like `DropView` and `DropTrigger`.

**Views.** `sqlite.CreateView(v).IfNotExists().As(body)` over a `*sqlite.SelectQuery`, rendered inline. There is no `OrReplace`, because `CREATE OR REPLACE VIEW` is a syntax error in SQLite; replacing a view is `DropView` then `CreateView`.

**Triggers.** A SQLite trigger's body is statements, so the query builder composes inside it:

```go
sqlite.CreateTrigger(sqlite.Trigger{Name: "users_audit"}).IfNotExists().After(sqlite.OnUpdateOf(status), users).
	When(sqlite.IsNot(sqlite.Old(status), sqlite.New(status))).
	Do(sqlite.StepInsert(sqlite.Insert(audit).Row(sqlb.SetExpr(auditUser, sqlite.New(id)), sqlb.Set(auditWhat, "status"))))
// CREATE TRIGGER IF NOT EXISTS "users_audit" AFTER UPDATE OF "status" ON "users" FOR EACH ROW WHEN (OLD."status" IS NOT NEW."status") BEGIN INSERT INTO "audit" ("user_id", "what") VALUES (NEW."id", 'status'); END
```

`Before`, `After` and `InsteadOf` (on a view) each take one `TriggerEvent`, since SQLite takes one per trigger: `OnInsert`, `OnUpdate`, `OnUpdateOf(cols...)` or `OnDelete`. `Do` takes `TriggerStep`s, and only four functions make one: `StepInsert`, `StepUpdate`, `StepDelete` and `StepSelect`, each typed to a complete SQLite statement. So an unfiltered `UPDATE` does not compile inside a trigger any more than outside one, and a Postgres statement does not fit. The stages are `CreateTriggerQuery`, `TimedTriggerQuery` and `CreateTriggerStatement`.

**PRAGMA.** `sqlite.SetUserVersion(n)` sets the integer SQLite keeps in the database header and never reads, a migration ledger with no table for a database one library owns. `sqlite.SetApplicationID(n)` sets the header's other integer, which says which application a file belongs to, the way a file format's magic number does. `sqlite.ForeignKeys(on)` is a `PragmaStatement` marked `OutsideTransaction`, because inside a transaction it silently does nothing, which the execution test shows.

Executed on SQLite in `contrib/driver-sqlite`: the NOCASE unique index refuses a case variant, the query planner uses the partial index for a query with its filter, and the trigger writes one audit row over three updates.

## SQLite: FTS5 and the table rebuild

**FTS5.** `sqlite.CreateFTS5(t)` has no `Build` until `Columns` names the indexed columns, declared on the FTS5 table as for any table; `ExternalContent(content, rowid)` makes it read its text from a content table whose integer key `rowid` is, `Tokenize` takes a `Tokenizer` (`Unicode61(removeDiacritics)`, `ASCII()`, `Trigram()` or `Porter(inner)`), and `Prefix` adds prefix indexes. It is queried with `Match`, `Rank` and `BM25`, which the package already had.

**R*Tree.** `sqlite.CreateRTree(t)` is SQLite's spatial index, with no `Build` until `Columns(id, dims...)` names an integer id and one to five `Dimension(min, max)` column pairs, each declared on the R*Tree table; more than five, or none, is `ErrRTreeDims`. `IfNotExists` makes it repeatable and `Integer` stores the coordinates as 32-bit integers, `rtree_i32`, for grid data that float rounding would misplace. It is queried with ordinary comparisons on the coordinate columns, which the engine answers from the tree.

**The rebuild.** Everything SQLite's `ALTER TABLE` cannot do (a column's type, a constraint, a key) is the rebuild SQLite's documentation gives: create the new table, copy the rows, drop the old one, rename the new one into place.

```go
sqlite.Rebuild(sessions).
	Into(sqlite.CreateTable(next).Column(ddl.Column(nid), ddl.Column(nuser), ddl.Column(nexp)).Clause(sqlite.PrimaryKey(nid))).
	Copy(sqlite.CopyAs(nid, sid), sqlite.CopyAs(nuser, suser),
		sqlite.CopyAs(nexp, sqlb.Cast[int64](sqlite.Strftime("%s", sexp), "INTEGER"))).
	Recreate(byUser)
```

`CopyAs(to, e)` fills a new column with an expression of its own Go type over the old table, so a type change is writable only with a conversion that produces the new type. `Recreate` lists the indexes, triggers and views on the old table, which the `DROP` removes; the rebuild cannot discover them without reading the database, so a rebuild that forgets one loses it, and the execution test compares `sqlite_schema` before and after. `ErrRebuildIncomplete` and `ErrRebuildSameName` refuse a rebuild with nothing to copy or no new name. The stages are `RebuildQuery` and `RebuildStatement`, and a `CopyPair` is one copied column.

The `Script` it builds holds no `BEGIN` or `COMMIT`. Its first and last statements turn foreign-key enforcement off and on and are `OutsideTransaction`; the runner executes everything between in one transaction. The foreign-key check before the end is `MustReturnNoRows`, because `PRAGMA foreign_key_check` reports a broken reference by returning a row rather than by failing: a runner must query it and fail on any row. `contrib/driver-sqlite` runs it with a runner that honours all three flags, and shows the check failing a rebuild that carries a dangling reference.

## Removing tables

`NewDropTable` returns a `DropTable` and writes one statement per table, because SQLite's `DROP TABLE` takes one name. `IfExists` makes an uninstall safe to repeat.

```go
sessions := expr.Table{Name: "session"}
ddl.NewDropTable(sqlite.Dialect{}, sessions, accounts).IfExists().Build()
```

```sql
DROP TABLE IF EXISTS "session";
DROP TABLE IF EXISTS "account"
```

## What Build refuses

Every value in DDL is a literal, written by the dialect's `render.LiteralQuoting`, and a value it cannot write faithfully fails `Build` rather than rendering something that looks right. SQLite has no time type, and the literal a `time.Time` would become matches no time the driver stores, so a default of one would never compare equal to a row the application wrote:

```go
_, err := ddl.NewCreateTable(sqlite.Dialect{}, accounts).
	Column(ddl.Column(accCreated).Default(time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC))).Build()
errors.Is(err, render.ErrNotInlinable) // true
```

A dialect with no `LiteralQuoting` at all, ClickHouse today, cannot render DDL and fails with `render.ErrNoLiteralQuoting` before any statement text exists. A column made some other way than `Col` is refused with `ErrNotAColumn`, and an engine clause aimed at a position the statement does not render with `ErrUnknownPosition`.

A column clause that only means what it says when the column has a default implements `DefaultRequirer`, and on a column that declares none `Build` fails with `ErrNoDefault`. SQLite's `NOT NULL ON CONFLICT REPLACE` is the case: `REPLACE` writes the column's default in place of a `NULL`, so with no default there is nothing to write and SQLite falls back to `ABORT`.

```go
_, err := sqlite.CreateTable(notes).Column(ddl.Column(noteID), ddl.Column(noteBody)).
	Clause(sqlite.NotNullOnConflict(noteBody, sqlite.OrReplace)).Build()
errors.Is(err, ddl.ErrNoDefault) // true: add .Default("untitled") to noteBody
```

It is refused at `Build` rather than documented because of when the failure would otherwise arrive. SQLite accepts the definition, so the schema installs and every test that never writes a `NULL` to that column passes; the first `NULL` production writes fails with `NOT NULL constraint failed`, from a table whose declaration promised the opposite. A warning in a comment is read by the person who already knows, and `Build` is the one place the mistake shows before it ships. The clause declares the need and `ddl` makes the check, because only the column definition knows whether a default was declared — the same reason `ErrNoNotNull` lives in `ddl`.

Every clause and statement type is a struct, so `pg.PrimaryKeyClause{}` compiles as well as `pg.PrimaryKey(id)` does, and a struct literal skips what the constructor guarantees. A zero value is refused at `Build` with `ddl.ErrZeroValue` rather than rendering `PRIMARY KEY ()` or panicking: a key with no columns, a foreign key with no column pairs, a statement wrapping nothing, a `CreateTableReady{}` with no table. `sqlbtest.ZeroValues` builds the zero value of every exported type in `ddl`, `pg`, `sqlite`, `mysql` and `ch` and fails on one that panics or builds, and a new exported type fails it until someone decides how its zero value is checked.

## A Script, not a string

`Build` returns a `Script`, which is a list of `Statement` values, because one builder call can need several statements and a runner must know how to run each. A `Statement` carries its `SQL` and no arguments, since there are none.

`Transactional` is false for a statement Postgres refuses inside a transaction block, such as `CREATE INDEX CONCURRENTLY`; a runner must execute it on its own. `OutsideTransaction` is true for a statement that is a silent no-op inside one, which SQLite's `PRAGMA foreign_keys` is. `MustReturnNoRows` is true for a check that reports a problem by returning rows, SQLite's `PRAGMA foreign_key_check`; a runner must query it and fail on any row. `SkipIf` is a query a runner checks first, skipping the statement when it returns a row: MySQL's repeatable `CREATE INDEX` is written that way, since MySQL has no `IF NOT EXISTS` for it, and a runner that ignores the field fails loudly on a repeat. `Script.SQL()` returns the text in order and `Script.Transactional()` says whether the whole script may run in one transaction.

## Engine clauses

A clause that only one engine has lives in that engine's package, and reaches a statement through a typed position rather than a string. `NewTableClause` places one at a `Position` in `CREATE TABLE`: `PositionModifier` between `CREATE` and `TABLE`, `PositionElement` after the columns inside the parentheses, and `PositionOption` after them. `PositionValues` lists all three.

```go
unlogged := ddl.NewTableClause[postgres.Dialect](ddl.PositionModifier, func(*render.Args) string { return "UNLOGGED" })
ddl.NewCreateTable(postgres.Dialect{}, accounts).Clause(unlogged).Column(ddl.Column(accID)).Build()
```

```sql
CREATE UNLOGGED TABLE "account" ("id" BIGINT NOT NULL)
```

A clause whose validity depends on the rest of the table implements `ddl.TableChecker`: `CREATE TABLE` describes itself, as `Describe` does, and passes the description to `CheckTable`, and an error fails `Build`. MySQL's `Partitioning` uses it to require every primary and unique key to include the partition columns and to refuse any foreign key.

The clause is a `TableClause[postgres.Dialect]`, so on a SQLite statement it does not compile. `PositionColumn` is the fourth position, inside one column's definition, and only a `ColumnClause` may use it.

A clause that belongs to one column, an identity or a collation, is made with `NewColumnClause`, which names the column as a `ColumnRef` (any column made by `Col` is one) and says which `ColumnPart` of the definition it fills: `PartAfterType` straight after the type, `PartConstraint` after the default, `PartValueSource` for a constraint that supplies the value and so refuses a default, `PartComputed` for a generated expression, which also writes no `NOT NULL`, and `PartAfterNotNull` immediately after the `NOT NULL` and before the default. `ColumnPartValues` lists all five, and a test renders every one of them in a single column definition, so a part cannot be added without being written.

`PartAfterNotNull` exists because SQLite's conflict clause on a `NOT NULL` has exactly one legal position, measured on 3.50.3: `"body" TEXT NOT NULL ON CONFLICT REPLACE DEFAULT 'untitled'` is accepted, while the same clause after the type or after the default is a syntax error. A clause there modifies the `NOT NULL` rather than the type, so a definition that writes none — a nullable column, or a generated one, where the expression decides nullability — fails `Build` with `ErrNoNotNull` instead of rendering a bare `ON CONFLICT` the engine would read as a constraint of its own. `ColumnExtras.AfterNotNull` carries it for a `ColumnSpec` written outside this package. `sqlite.NotNullOnConflict` is the consumer.

```go
collate := ddl.NewColumnClause[postgres.Dialect](accEmail, ddl.PartAfterType, func(*render.Args) string { return `COLLATE "C"` })
ddl.NewCreateTable(postgres.Dialect{}, accounts).Column(ddl.Column(accEmail)).Clause(collate).Build()
// CREATE TABLE "account" ("email" TEXT COLLATE "C" NOT NULL)
_, err := ddl.NewCreateTable(postgres.Dialect{}, accounts).Column(ddl.Column(accID)).Clause(collate).Build()
errors.Is(err, ddl.ErrUnknownColumn) // true
```

A clause at `PositionColumn` that names no column fails with `ErrNoColumnTarget`. The statement hands each column its rendered clauses as a `ColumnExtras`, whose `AfterType`, `Constraints`, `ValueSource`, `Computed`, `AfterNotNull` and `RequiresDefault` fields say what to write and what to refuse, so a `ColumnSpec` written outside this package can honour them. A table-level clause that names columns of its own table, a key say, implements `ColumnNamer`, or is made with `NewTableClauseOn`, and the statement checks each column exists. `NewAlterClause` makes an `AlterClause` that joins other actions, `NewStandaloneAlterClause` one that must be its own statement the way a rename is, and `NewDropClause` a `DropClause` such as `CASCADE`.

## Clause order is the engine's, not the caller's

Two engines fix the order of clauses that share a position, and since 2026-09-24 the statement writes them in that order whatever order they were passed in. Measured on the servers:

| Where | Engine | The rule | Out of order |
|---|---|---|---|
| Options after `)` | Postgres 17 | `INHERITS`, `PARTITION BY`, `USING`, `WITH`, `ON COMMIT`, `TABLESPACE`, in exactly that order | `syntax error at or near "USING"` |
| Options after `)` | MySQL 8.4 | table options in any order, `PARTITION BY` only last | `ER_PARSE_ERROR` |
| Options after `)` | SQLite 3.50 | `STRICT` and `WITHOUT ROWID` either way round | accepted |
| One column's clauses | Postgres 17 | `STORAGE`, `COMPRESSION`, `COLLATE`, in that order | `syntax error at or near "STORAGE"` |
| One column's clauses | MySQL 8.4 | `COMMENT`, `VISIBLE`, `COLLATE` and the rest in any order | accepted |

Before this, a caller who passed two correct clauses in the wrong order got a clean `Build` and then a server error about SQL they never wrote. So these two calls now produce the same statement:

```go
pg.CreateTable(users).Column(ddl.Column(id)).Clause(pg.TableAccessMethod(pg.Heap), pg.TableStorage(fill))
pg.CreateTable(users).Column(ddl.Column(id)).Clause(pg.TableStorage(fill), pg.TableAccessMethod(pg.Heap))
```

```sql
CREATE TABLE "users" ("id" BIGINT NOT NULL) USING "heap" WITH ("fillfactor" = 70)
```

### How it works

An engine clause carries an `OptionRank`, and the statement renders ranked clauses in rank order. An option is made with `NewOptionClause`, or `NewOptionClauseOn` when it names columns the way `PARTITION BY` does, and a column clause with `NewRankedColumnClause`. A type of an engine's own, such as `mysql.Partitioning`, implements `OptionRanker` directly. The ranks are the engine's: `pg` keeps one table for its table options and one for its column clauses, and `mysql` gives every table option one rank and partitioning a higher one. `ddl` knows only that a rank means an order.

MySQL's table options share a rank because MySQL takes them in any order, and the sort is stable, so the order the caller chose among them is kept and only partitioning moves. Zero means unranked.

### Why sort, rather than refuse or leave it

Three answers were considered.

- **Leave it.** Document the order and pin it with a test. That is honest, but the wrong order stays writable, and a test that passes the clauses in the right order proves nothing about a caller who did not. This was the state until 2026-09-24, and the test that pinned it passed the clauses in exactly the order it wanted.
- **Refuse.** Fail `Build` when the order is wrong. That names the mistake, but it makes the caller reproduce an order the library already knows. There is one legal order, so there is no choice to get wrong, only busywork to demand. Refusing is right when the caller holds information the library does not; here the library holds all of it.
- **Sort.** Write the order the engine requires. This is what the statement does.

### Why an unranked clause does not move

A clause from outside the module, made with `NewTableClause`, may be anything, and no default place for it is right in every case. First breaks a raw `TABLESPACE` on Postgres, which belongs last; last breaks a raw table option before MySQL's partitioning. So an unranked clause stays exactly where the caller put it, and only the ranked clauses move, each into a position a ranked clause already held. The statement reorders only what it understands, which is the one rule that never turns a correct statement into a wrong one.

That only ever affects a caller's own clause, because every order-sensitive clause `pg` and `mysql` ship is ranked. A test reads their source and fails on one that is not, so the next option somebody adds cannot quietly reintroduce the bug.

### How it is proven

`ExampleOptionRank`, `ExampleOptionRank_mysql`, `ExampleOptionRank_unranked` and `ExampleErrNoDefault` in `examples/packages` are the compiled versions of the calls above: `go test` checks each one's printed SQL against its `// Output:` block, so the text shown here cannot drift from what the builder produces. Writing them caught one thing already — the MySQL example's expected output first spelled the charset bare, and the renderer writes it as a quoted identifier.

`contrib/driver-pgx` builds a table with every Postgres option and column clause passed in reverse, and `contrib/driver-mysql` one with partitioning passed first. Both run on a real server, and the effects are read back from the catalogue: the storage parameter, the parent, and the column's storage, compression and collation on Postgres; the engine, the comment and the partition count on MySQL. With the sort disabled, Postgres answers `syntax error at or near "COMPRESSION"` and MySQL `Error 1064`, so the tests depend on the fix rather than passing beside it.

## Describing a table

`CreateTableReady.Describe()` returns the `schema.Table` the statement creates: its columns, keys and foreign keys, derived from the statement rather than declared beside it. A migration planner and a drift check then read the same description as the DDL, and it cannot disagree with it.

A column describes itself through `ColumnDescriber`, which `ColumnDef` implements. Its type is a portable `schema.Type` when it has one, and otherwise the dialect's spelling in `Raw`: a `RawType`, or an engine type such as `pg.Enum`. Nullability comes from the definition, and the default is the literal or expression the dialect writes. An engine clause adds what it knows through `Describer`: a primary key, a unique or plain index, a foreign key whose `RefTable` is written by `RefTableName` so a `schema.Set` can resolve it. A clause with nothing to describe, `UNLOGGED` or `STRICT`, is left out, and so is a `CHECK`, which `schema.Table` has no place for.

A column `Build` cannot spell fails `Describe` too, so a description never exists for DDL that does not. `contrib/migrate`'s `Install` orders tables by their descriptions, and `contrib/vet`'s `CompareTables` compares a database against them.

## Naming constraints

A named constraint is a `ddl.TableConstraint`, its table and its name, declared once beside the table and used by every statement that names it. `pg.TableConstraint`, `mysql.TableConstraint` and `sqlite.TableConstraint` are the same type.

```go
var UsersEmailKey = pg.TableConstraint{Table: Users, Name: "users_email_key"}

pg.CreateTable(Users).Column(ddl.Column(UserEmail)).Clause(pg.Unique(UsersEmailKey, UserEmail))
pg.CommentOnConstraint(UsersEmailKey).Is("one address per user")
pg.Insert(Users).Row(sqlb.Set(UserEmail, "a@example.com")).Clause(pg.OnConstraint(UsersEmailKey).DoNothing())
pg.AlterTable(Users).Clause(pg.DropConstraint(UsersEmailKey))
```

The constructors take it: `ddl.Check`, `ddl.OneOf`, `pg.Unique`, `pg.ForeignKey`, `pg.Check`, `pg.Exclude` and `pg.PrimaryKey(...).Named`, `mysql.Unique`, `mysql.ForeignKey` and `mysql.Check`, `sqlite.ForeignKey` and `sqlite.Unique(...).Named`. So do the statements about one: `pg.ValidateConstraint`, `pg.RenameConstraint`, `pg.DropConstraint`, `pg.OnConstraint`, `pg.CommentOnConstraint` and `pg.AddConstraintStatement`, and MySQL's `DropForeignKey`, `DropCheck`, `DropKey` and `RenameKey`. Each checks the constraint is on its own table: a constraint of one table in another's `CREATE` or `ALTER TABLE` fails `Build` with `ErrForeignColumn`, and a rename to a constraint of another table with `pg.ErrRenameAcrossTables`. A `TableConstraint` missing its name or table fails with `ErrZeroValue`. A constraint that stays unnamed has its own form: an inline `References`, a primary key without `Named`, an unnamed SQLite `Unique`.

A domain's constraint is a `pg.DomainConstraint[T]`, the domain and a name, which `CreateDomain(...).Check` and `AlterDomain`'s `AddConstraint`, `ValidateConstraint` and `DropConstraint` take, refusing one of another domain with `pg.ErrForeignDomain`. A MySQL plain index, which shares the key namespace, is an `Index` value: `TableIndex(ix, cols...)`, `TableFulltext(ix, cols...)` and `RenameIndex(from, to)`.

## Postgres 18: virtual columns, unenforced constraints and temporal keys

Four groups of construct need Postgres 18 or later. Postgres 17 refuses the statement, so the execution tests run them against a scratch 18 (`task engines:pg18:up` then `task test:pg18`) and skip loudly elsewhere.

`pg.GeneratedVirtual(col, e)` is a generated column evaluated when it is read rather than stored, which is 18's default for `GENERATED ALWAYS AS`. It takes no disk space and cannot be indexed; `pg.GeneratedStored` stays the one to index.

`NotEnforced` on `pg.Check` and `pg.ForeignKey` declares a constraint Postgres records but does not apply, which is how one is introduced over data that is not yet clean. Enforcing it afterwards is `pg.AlterConstraint(c).Enforced()`, and that is for a foreign key: Postgres 18 refuses to change a check's enforceability, measured, so a check is turned on by dropping it and adding it enforced. `AlterConstraint` also carries `Deferrable`, `NotDeferrable`, `NotEnforced`, and `Inherit`/`NoInherit` for a `NOT NULL` constraint, which the grammar has as a form of its own: combining them with the others fails `Build` with `pg.ErrConstraintAlterMix`.

A temporal key is a key over a range column: two rows may share the other key columns as long as their periods do not overlap, which is what a booking or a price valid over an interval needs. `pg.Period(col)` marks the range column, and the key takes it last.

```go
var (
	During  = sqlb.Col[pg.Range[time.Time]](Bookings, "during")
	TsRange = ddl.RawType[pg.Range[time.Time]]("tstzrange")
)

pg.CreateTable(Bookings).
	Column(ddl.Column(BookingRoom), ddl.Column(During).As(TsRange)).
	Clause(
		pg.PrimaryKey(BookingRoom).Named(BookingsPK).WithoutOverlaps(pg.Period(During)),
		pg.ForeignKey(BookingsRoomFK, pg.RefPair(BookingRoom, RoomID)).
			Period(pg.Period(During), pg.Period(RoomValid)),
	)
```

`WithoutOverlaps` is on `pg.PrimaryKey` and `pg.Unique`; a GiST index backs the key, so scalar columns in it need the `btree_gist` extension. A foreign key's `Period` makes the reference temporal: each row's period must be covered by the union of the referenced rows' periods, over a temporal key of the referenced table. `pg.Period` takes a `Col[pg.Range[T]]`, so a column of any other type does not compile there.

## What will not compile

These are pinned as negative builds in `examples/typecheck`, which the gate fails if they ever compile:

- building a `CREATE TABLE` with no column, or an `ALTER TABLE` with no action, and calling `Column` with no column;
- a SQLite table clause or `ALTER` clause on a Postgres statement;
- a default of the wrong Go type, `ddl.Column(accEmail).Default(42)`;
- a rename between columns of two Go types;
- an `SQLType` for another Go type than the column's.

Every builder method returns a derived statement and leaves its receiver alone, as the query builders do, so a table definition shared by two releases cannot be changed through either.

## What is not built

Every statement Postgres, SQLite and MySQL have that these builders cover only in part, or not at all, is listed in [not-built.md](./not-built.md) with the SQL to write it through an escape hatch, each recipe executed on its engine. The same page lists the limits inside built statements, the work that may come later, and a generated appendix of every entry the three DDL detectors classify.
