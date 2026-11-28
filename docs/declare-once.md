# Declare a table once

A library that owns tables usually ends up with two descriptions of each one: a `CREATE TABLE` string in a migration, and typed columns beside it for the queries. Nothing checks that they agree, so a column renamed in one and not the other is found in production. This page shows the path that writes each table once, with a table prefix the application chooses at run time, and converts a module that has the two descriptions today.

The whole path is in [`examples/prefixed/widgets`](../examples/prefixed/widgets/widgets.go), a library with two tables, and it is executed on SQLite and Postgres 17 by [`contrib/vet/enginetest/prefixed_test.go`](../contrib/vet/enginetest/prefixed_test.go).

## Contents

- [The path](#the-path)
- [Before](#before)
- [Step 1: the columns, once, under a prefix](#step-1-the-columns-once-under-a-prefix)
- [Step 2: each column's definition, once](#step-2-each-columns-definition-once)
- [Step 3: the declaration and its description](#step-3-the-declaration-and-its-description)
- [Step 4: numbered migrations](#step-4-numbered-migrations)
- [Step 5: databases the old strings already created](#step-5-databases-the-old-strings-already-created)
- [Moving an existing migration history onto the ledger](#moving-an-existing-migration-history-onto-the-ledger)
- [Step 6: the queries](#step-6-the-queries)
- [Step 7: the live-schema check](#step-7-the-live-schema-check)
- [Step 8: the tests](#step-8-the-tests)
- [A package that has only the description](#a-package-that-has-only-the-description)
- [What catches what](#what-catches-what)

## The path

| Piece | Where it comes from |
|---|---|
| Table and typed columns, `sqlb.Table` and `sqlb.Expr[T]` | written once, built at run time from the prefix |
| `CREATE TABLE`, `CREATE INDEX`, `ALTER TABLE ... ADD COLUMN` | the `ddl` builders, over those same column values |
| the `schema.Table` description | `Describe` on the `CREATE TABLE`, never written by hand |
| numbered migrations | written with the `ddl` builders, applied once each by the `contrib/migrate` ledger |
| the check that a database matches | `vet.CheckTables` against the description |

No code is generated and no DDL is derived from Go declarations: the library still writes every statement, and the builders only stop the statements being strings.

## Before

A typical module before conversion. The column list is written twice, the types are written twice in two languages, and the prefix is spliced into a string:

```go
const createUsers = `CREATE TABLE IF NOT EXISTS %susers (
	id BIGINT PRIMARY KEY,
	email TEXT NOT NULL,
	created_at TIMESTAMPTZ NOT NULL
)`

var (
	Users        = sqlb.Table{Name: "users", Alias: "au"}
	UserID       = sqlb.Col[int64](Users, "id")
	UserEmail    = sqlb.Col[string](Users, "email")
	UserCreated  = sqlb.Col[time.Time](Users, "created_at")
)

func migrateSQL(prefix string) string { return fmt.Sprintf(createUsers, prefix) }
```

The package-level columns also ignore the prefix: they always name `users`.

## Step 1: the columns, once, under a prefix

A prefix decides the real table names, so the tables cannot be package-level values. Put them in a struct built from the prefix, and keep the struct where the library keeps its database handle. Check the prefix, because it comes from configuration:

```go
type Tables struct {
	Accounts         sqlb.Table
	AccountID        sqlb.Expr[int64]
	AccountEmail     sqlb.Expr[string]
	AccountCreatedAt sqlb.Expr[time.Time]
}

func NewTables(prefix string) (Tables, error) {
	if !prefixPattern.MatchString(prefix) {
		return Tables{}, fmt.Errorf("%w: %q", ErrBadPrefix, prefix)
	}
	var t Tables
	t.Accounts = sqlb.Table{Name: prefix + "accounts", Alias: "wa"}
	t.AccountID = sqlb.Col[int64](t.Accounts, "id")
	t.AccountEmail = sqlb.Col[string](t.Accounts, "email")
	t.AccountCreatedAt = sqlb.Col[time.Time](t.Accounts, "created_at")
	return t, nil
}
```

The alias stays fixed and distinctive (`wa`, not `a`), so two libraries joined in one statement do not collide. A library with no prefix can make one package-level `Tables` value instead.

## Step 2: each column's definition, once

The SQL type, default and nullability of a column are said once, as a `ddl.ColumnDef`, and kept beside the columns, because both the declaration and the migrations use them:

```go
t.defActive = ddl.Column(t.AccountActive).Default(true)
t.defCreatedAt = ddl.Column(t.AccountCreatedAt)                                        // TIMESTAMPTZ on Postgres
t.defBirthday = ddl.Column(t.AccountBirthday).As(ddl.Portable[*time.Time](schema.TypeDate)) // DATE
t.defProfile = ddl.Column(t.AccountProfile).As(ddl.Portable[json.RawMessage](schema.TypeJSON)).Default(json.RawMessage(`{}`))
t.defAvatar = ddl.Column(t.AccountAvatar).Nullable()                                   // BYTEA, NULL
```

What the Go type decides on its own: `string` is text, `int64` bigint, `bool` boolean, `time.Time` a timestamp (`TIMESTAMPTZ` on Postgres, `TIMESTAMP` on SQLite), `[]byte` bytes, and a pointer makes the column NULL. A `time.Time` that is a calendar date, a `json.RawMessage` that is JSON, and a `string` that is a uuid need `As`, because the Go type cannot say which they are. A composite primary key is an engine clause, `pg.PrimaryKey(a, b)` or `sqlite.PrimaryKey(a, b)`.

## Step 3: the declaration and its description

The declaration is the tables as they are now: one `CREATE TABLE` per table, with every column. The engines differ only in their clauses, so the engine is a switch and the columns are not repeated:

```go
switch e {
case Postgres:
	create = pg.CreateTable(t.Accounts).
		Column(t.defID, t.defEmail, t.defActive, t.defCreatedAt, t.defBirthday, t.defProfile, t.defAvatar).
		Clause(pg.PrimaryKey(t.AccountID))
case SQLite:
	create = sqlite.CreateTable(t.Accounts).
		Column(t.defID, t.defEmail, t.defActive, t.defCreatedAt, t.defBirthday, t.defProfile, t.defAvatar).
		Clause(sqlite.PrimaryKey(t.AccountID))
}
desc, err := create.Describe()
```

`Describe` returns the `schema.Table` the statement creates: columns, types, nullability, defaults, and the keys and foreign keys its clauses declare. An index created by a separate `CREATE INDEX` is not part of `CREATE TABLE`, so add it to the description's `Indexes`. Write each index once, as the builder value the migration also uses, and take its description from it: `Describe` on a `pg.CreateIndex` or `sqlite.CreateIndex` returns the `schema.Index` with its columns, every key's sort order and a partial index's predicate, so the live check verifies all three. The example's partial owner index:

```go
func (t Tables) ownerIndexStmt(e Engine) indexStmt {
	if e == SQLite {
		return sqlite.CreateIndex(sqlite.Index{Name: t.ownerIndex()}, t.Members).IfNotExists().
			On(sqlite.IndexKey(t.MemberTeam)).Where(sqlb.Eq(t.MemberRole, "owner"))
	}
	return pg.CreateIndex(pg.Index{Name: t.ownerIndex()}, t.Members).IfNotExists().
		On(pg.IndexKey(t.MemberTeam)).Where(sqlb.Eq(t.MemberRole, "owner"))
}
```

Migration 0003 builds it, and the example's `indexed` type appends its `Describe()` to the members table's description; a descending key, `pg.IndexKey(t.MemberTeam).Desc()`, is described the same way. A hand-written `schema.Index` with no `Orders` and no `Where` still works and is checked as before: by columns and uniqueness, its order not judged. The declaration is never run against a database; it is what the migrations must arrive at, and the tests in step 8 prove they do.

Name derived objects from the real table name, so the prefix reaches them: `t.Accounts.Name + "_email_key"`.

## Step 4: numbered migrations

Migrations are written with the same builders over the same definitions, numbered, and never edited once released. Each is a `migrate.Migration` with the library as owner:

```go
m1 := pg.CreateTable(t.Accounts).IfNotExists().
	Column(t.defID, t.defEmail, t.defActive, t.defCreatedAt).
	Clause(pg.PrimaryKey(t.AccountID))
m2 := pg.AlterTable(t.Accounts).Action(ddl.AddColumn(t.defBirthday), ddl.AddColumn(t.defProfile), ddl.AddColumn(t.defAvatar))
```

Release 2 added three columns, so migration 0002 adds them with `ddl.AddColumn` of the definition the declaration uses: the column is not written a second time. A column added to a table that has rows is nullable or has a default; on SQLite `Build` refuses one that is not, and refuses an expression default, before anything reaches a database.

Apply them at start-up, with the ledger table and the lock key under the same prefix, so two applications sharing a database with different prefixes keep separate ledgers:

```go
led, err := migrate.NewLedger(pg.Dialect{}, prefix+"sqlb_migrations", migrate.EngineLock(pg.Dialect{}, prefix+"acme/widgets"))
if err != nil {
	return err
}
led = led.RefuseUnknown()
applied, err := led.ApplyDB(ctx, db, ms...)
```

Each migration runs once, in one transaction on Postgres and SQLite, under a lock so two processes starting together do not both run it. `RefuseUnknown` makes an older release refuse a database a newer one migrated. A released migration edited afterwards, a column's type changed in 0001 say, fails the next deploy with `migrate.ErrChecksumMismatch` instead of being skipped: the edit belongs in a new number. A change that is not add-only, a dropped or retyped column, is a new migration written by hand with the `ddl` builders (`ddl.DropColumn`, a SQLite table rebuild), and the declaration changes in the same commit.

## Step 5: databases the old strings already created

Databases already exist that the old module created with `CREATE TABLE IF NOT EXISTS` strings and no ledger. They must move to the ledger without losing data:

1. Make migration **0001** the old schema exactly, written with the builders and `IfNotExists` on every table and index: the same table names under the prefix, the same column names, types, nullability and defaults, the same index names. On a database the old strings created, 0001 then changes nothing and is only recorded; on a fresh database it creates the tables.
2. Put everything the new release adds in **0002** and later, add-only.
3. Do not try to change an existing table in 0001. `IF NOT EXISTS` checks a table's name, not its shape, so 0001 cannot fix a table the old strings made differently. The live check in step 7 is what reports a difference, and the fix is a later migration.
4. On SQLite, the declared type is what the driver reads back by: a `time.Time` column must be declared `TIMESTAMP` or `DATE` to scan into a `time.Time`, which is what `ddl.Column` writes. A column the old strings made `TEXT` is reported by the live check as a type difference; either read it as a string with `ddl.RawType[string]("TEXT")`, or move it with a table rebuild in a later migration.

`TestTask_AdoptTablesAnEarlierReleaseCreated` runs exactly this on both engines: the old strings, a row, then the ledger; 0001, 0002 and 0003 are recorded, the row is intact, and the live check passes.

## Moving an existing migration history onto the ledger

Step 5 fits a schema the old module created in one step. A module that already has a ledger of its own is different: its users' databases hold a history of numbered migrations, some from the module and some contributed by plugins under their own owners, and most of them `ALTER TABLE ... ADD COLUMN`, which has no `IF NOT EXISTS` on SQLite or MySQL. Folding that history into one idempotent 0001 is not possible, and running it again fails. The ledger adopts it instead:

1. Turn every migration the old ledger could have recorded into a `migrate.Migration` with the same owner and name, its script written with the `ddl` builders. Released migrations keep their numbers; nothing is renumbered.
2. At start-up, if the old ledger table exists, read its rows. Where the old ledger kept checksums, check each against the old script by the old algorithm first, and refuse to start on a mismatch, as the old code would have.
3. Call `Adopt` with every migration the code knows and a `migrate.Ran` that answers from those rows: which migrations ran, and when. Then call `Apply` with the same list, which runs only what the old ledger did not list.

```go
led, err := migrate.NewLedger(d, prefix+"sqlb_migrations", migrate.EngineLock(d, prefix+"acme/widgets"))
if err != nil {
	return err
}
ran := func(m migrate.Migration) (time.Time, bool) {
	at, ok := oldRows[m.Owner+"/"+m.Name] // read from the old ledger table
	return at, ok
}
if _, err := led.AdoptDB(ctx, db, ran, all...); err != nil {
	return err
}
applied, err := led.ApplyDB(ctx, db, all...)
```

`Adopt` writes the rows `Apply` would have written, with `migrate.Checksum` of the scripts the code holds now, in one transaction and under the same lock as `Apply`. Two processes starting together, or an old release and a new one deploying together, serialize. If the old code took a lock of its own, give `NewLedger` that same lock, so old and new releases wait for each other: `EngineLock` on Postgres keys its advisory lock by the first eight bytes of the name's SHA-256, and any other scheme is a `migrate.Locker` of your own. Adopting again on the next start records nothing, so the step can stay in the start-up path until every database has moved, and then it and the old table can go. A migration already in the new ledger under a different checksum is `migrate.ErrChecksumMismatch`, exactly as from `Apply`.

A module that also offered its users an offline script, for teams that apply schema changes by hand, writes it with `led.Script(pending...)`: the ledger's `CREATE TABLE`, each migration's statements, and the row that records it, built by the code `Apply` runs, so a later `Apply` skips them. `led.WithClock` fixes the recorded time, so the script's text is the same on every run.

`TestAdopt_ExistingHistoryThenApply` and the adoption suite in `contrib/migrate/enginetest` run this flow on SQLite and Postgres, with the MySQL suite there for the scratch container.

## Step 6: the queries

Queries take the columns from `Tables`, so a column's name and type exist in one place. A construct one engine has and another lacks is typed to its engine, so it sits in the same switch as the DDL:

```go
switch s.Engine {
case Postgres:
	ins := pg.Insert(t.Accounts).Row(row...).Clause(pg.OnConflict(t.AccountEmail).
		DoUpdate(sqlb.SetExpr(t.AccountActive, pg.Excluded(t.AccountActive))).Clause())
	q, err = pg.InsertReturning(ins, t.bind(out)...).Build()
case SQLite:
	ins := sqlite.Insert(t.Accounts).Row(row...).Clause(sqlite.OnConflict(t.AccountEmail).
		DoUpdate(sqlb.SetExpr(t.AccountActive, sqlite.Excluded(t.AccountActive))).Clause())
	q, err = sqlite.InsertReturning(ins, t.bind(out)...).Build()
}
```

`pg.ForUpdate()` locks a row on Postgres; SQLite has no row locks, because its write transaction locks the whole database. `ON CONFLICT` names the columns of a unique index the migrations create.

## Step 7: the live-schema check

`vet.CheckTables` is the check `sqlb vet` runs, as a library function. It reads the database's catalogue and compares it with the descriptions, columns by name, type, nullability and default, keys, indexes and foreign keys by their columns, and an index's key order and partial predicate where its description states them:

```go
desc, err := tables.Describe(widgets.Postgres)
if err != nil {
	return err
}
findings, err := vet.CheckTables(ctx, db, render.DialectPostgres, desc)
if err != nil {
	return err
}
if vet.Failing(findings) {
	return fmt.Errorf("the database does not match the declaration: %v", findings)
}
```

Each `vet.Finding` names its position as `table.column` and says what differs; a warning, a column the database has and the declaration does not, does not fail. An index finding also carries a `Kind`: `vet.FindingIndexOrder` and `vet.FindingIndexPredicate` are errors. Postgres re-prints a predicate (`"role" = 'owner'` comes back as `(role = 'owner'::text)`), so `CheckTables` asks Postgres for its canonical text of both sides, in a transaction it rolls back; when it cannot ask, the pair is `vet.FindingIndexPredicateNotCompared`, a warning, never a silent pass. The rule and its limits are in the [`contrib/vet` README](../contrib/vet/README.md#keys-indexes-and-foreign-keys). Only the tables passed are judged, so other tables in the database, another prefix's included, are left alone. `contrib/vet` imports `golang.org/x/tools`, so a library usually runs this in its tests and documents it for the application, which can run it at start-up.

## Step 8: the tests

Three tests hold the path, each on SQLite and on Postgres when `SQLB_PG_DSN` is set:

- **Fresh database:** apply the migrations, then `vet.CheckTables` against the declaration, and require no finding at all, warnings included: a column the migrations create and the declaration lacks is only a warning, which is right at start-up and wrong in this test. This is the test that the migrations and the declaration end in the same schema; a column added to one and not the other fails it. Apply twice and assert the second run applies nothing. Run it under two prefixes in one database.
- **Old database:** run the old module's `CREATE TABLE` strings, insert a row, apply the migrations, run the check, and read the row back.
- **Edited migration:** apply, change a released migration, apply again, and require `migrate.ErrChecksumMismatch`.

## A package that has only the description

A package that borrows a table, and has the library's `schema.Table` but not its Go values, gets typed columns from the description with `schema.MustCol`, which checks the Go type against the declared type when the package loads:

```go
email := schema.MustCol[string](accounts, "email")
```

`schema.Col` is the same with an error, for a table or column named at run time; asking for `schema.Col[int64]` on a text column returns `schema.ErrTypeMismatch`.

## What catches what

| Mistake | Caught by |
|---|---|
| a column of one type compared or scanned as another | the compiler |
| a column created in the migrations and missing from the declaration, or the reverse | the fresh-database test's live check |
| a released migration edited | `migrate.ErrChecksumMismatch` at the next deploy |
| a NOT NULL column added to a table with rows without a default | `Build`, on SQLite; the server, on Postgres |
| a database that drifted from the declaration, or an old table made differently | `vet.CheckTables` |
| a partial index created without its predicate, or an index created ascending where the declaration says `DESC` | `vet.CheckTables`, with the index described by its `CREATE INDEX` |
| a wrong Go type asked of a description | `schema.MustCol` at package load |
