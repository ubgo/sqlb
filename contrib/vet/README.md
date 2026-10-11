# vet

The analyzers behind `sqlb vet`: the checks the compiler cannot make, as standard `golang.org/x/tools/go/analysis` values, runnable from a CLI or from an application's own test, plus the one check that needs a database.

## Contents

- [Setup](#setup)
- [What it checks](#what-it-checks)
- [Against the database](#against-the-database)
- [The escape-hatch listing](#the-escape-hatch-listing)
- [Running it](#running-it)
- [Why a contrib module](#why-a-contrib-module)

## Setup

```go
import (
	"github.com/ubgo/sqlb/contrib/vet"
)
```

## What it checks

Six mistakes survive the type system, and `Analyzers` returns the analyzer for each. Every one compiles, renders, and is wrong.

**`Dialect`** — an engine expression used in a statement fixed to another engine. `pg.ArrayAgg` inside a `sqlite.Select` is universal by type, so it compiles and fails only at `Build`; the compiler cannot see it because an expression carries no dialect. The analyzer walks each method chain to its root call — an entry package's constructor, or `NewSelect` and its siblings given a dialect literal — to learn the statement's engine, then reports any selector into `pg`, `sqlite`, `ch` or an extension sub-package that names a different one. Packages are resolved through type information by import path (`Module` and below), so an import alias cannot hide one. A statement whose dialect is a runtime value has no engine here and is not checked; `Build` catches it.

**`Discarded`** — a builder method whose result is dropped. Every builder method returns a new statement and leaves its receiver alone, so `q.Where(p)` on its own line built the unfiltered query and the compiler cannot flag the ignored return. Any expression statement whose value is a pointer to a `stmt` statement type is reported. The engine packages' own statement kinds count too: `pg.Merge`, `pg.Truncate` and `pg.LockTable` return derived statements since 2026-09-21, so `m.Into(t)` on its own line is the same mistake, and the analyzer recognises any engine type with a `Build` method rather than a list of names, so a statement kind added later is covered without anyone remembering to add it.

**`Literal`** — `Literal` called with anything but a constant. A literal is SQL text the author wrote and is rendered verbatim, so `Literal[string](s)` with `s` from a variable is interpolation; the value belongs in `Param`, which binds it. The definition in `expr` and the facade's forwarder are skipped, since each passes its own parameter through.

**`StructLit`** — a statement or `Query` built as a struct literal outside `stmt`. A literal skips the constructor, so it has no exec guard and no typestate: `&FilteredUpdate{}` renders without a `WHERE` and `Query{SQL: …}` lets two goroutines interleave rows. The zero value `Query{}` returned beside an error is exempt; nothing executes it.

**`Keyword`** — a `JoinType` or `SetOp` keyword the statement's engine rejects, when the engine is statically known, and `NATURAL` in any join under any engine. The two take a string on purpose, as the escape hatch for a keyword the constants omit; this is the check that the hatch was used for a keyword the engine has. The tables are small and verified: `STRAIGHT_JOIN`, `CROSS APPLY` and `OUTER APPLY` are other engines' spellings on all three; `INTERSECT ALL` and `EXCEPT ALL` do not exist on SQLite; ClickHouse's set operations are not in the table, and an absent engine means no opinion. A `Join` literal is checked when written inline in the `Join(...)` call, which is where nearly every one is.

**`Rows`** — a `RowIn`, `RowNotIn`, `RowEq`, `RowLess` or `RowGreater` whose literal `Row(...)` values disagree in arity or element type. `Row` returns an untyped `SQLer`, so `RowIn(Row(id, name), Row(Param(1), Param("a"), Param("b")))` compiles; the database rejects it. Rows are compared to the first, positionally, using the `Expr[T]` type argument of each element.

Run on this repository, the analyzers report exactly seven sites, all deliberate: three tests that pin the dialect residue by building an engine expression under the wrong dialect and asserting `ErrUnsupportedDialect`, one each for `pg`, `sqlite` and `mysql`; two that pin the same residue for an engine *statement*, a `pg.Truncate` or `pg.LockTable` embedded as a CTE body in a SQLite statement, which is the one path to another engine's dialect since their constructors became typed; and two that pin that a `Query` literal has no guard.

## Against the database

`Schema` is the one check that needs a connection, because it checks the one claim no compiler can see: that a `Col[T]` declaration matches the real table.

```go
findings, err := vet.Schema(ctx, db, "sqlite", ".", "./...")
```

`Declarations` reads every `expr.Col[T]` and `sqlb.Col[T]` call in the packages named and returns a `Declared` value: one `Declaration` per column, carrying its `Pos`, `Table`, `Schema`, `Column`, the `GoType` it claims and whether that type is a pointer (`Nullable`), and one `TableDeclaration` per package-level `Table` literal the columns were resolved against. It reads source rather than a runtime registry, so a declaration in a code path nothing executes is still checked. A table's `Name` and `Schema` and a column's name are read as the constant the compiler folds them to, so `Name: usersTable` with `const usersTable = prefix + "users"` is `auth_users`, and a typed constant works as a literal does; only `sqlb` and `expr` `Col` calls count, so a function of another package that happens to be called `Col` is not read as one. `ReadCatalogue` reads every user table's columns, in every user schema, from `information_schema.columns` on Postgres and MySQL or `pragma_table_info` on SQLite into a `Catalogue`, a map of table to column to `CatalogueColumn` — the engine's own type spelling, lower-cased, and whether NULL is allowed. Tables are keyed by `CatalogueKey(schema, name)`: a table in the connection's default schema (`current_schema()`, `DATABASE()`, SQLite's `main`) is under its bare name and under `schema.name`, any other only qualified, so `auth.users` is compared with `auth.users` and never with a `users` in the default schema. Postgres shows only the columns the connecting role can access, so a table the role has no privilege on reads as missing. On SQLite a primary-key column is nullable unless it is declared `NOT NULL`, is the rowid itself, or its table is `STRICT` or `WITHOUT ROWID`, which is [SQLite's own rule](https://www.sqlite.org/lang_createtable.html); `INTEGER PRIMARY KEY DESC` is not the rowid and is nullable, and a test inserts a NULL key into each shape to prove the catalogue agrees with the engine. `Compare` diffs the two and returns a `Finding` per mismatch, by file and line:

| Finding | Kind |
|---|---|
| the table is not in the database | error |
| the column is not in the database | error |
| the Go type cannot hold the column's type — `Col[string]` on an `integer` | error |
| the column is nullable and the declaration has no pointer — `Col[string]` where `Col[*string]` is needed | error |
| the table argument is not a package-level literal, so the declaration cannot be verified | error |
| the column's name is a variable, not a constant, so the declaration cannot be verified | error |
| the database has columns no declaration names | warning |

A warning is reported and does not fail the check; a library may deliberately declare only the columns it reads, and the warning is there so a migration that adds a column is not invisible.

`ReadCatalogue` reads MySQL's `information_schema.columns` too, and the `--dsn` flag takes `mysql://user:pw@tcp(host:3306)/db` for it.

### Against the DDL

`CompareTables(dialect, tables, catalogue)` compares the database with what the library's DDL creates, rather than with its `Col[T]` declarations. The tables are `schema.Table` values from `ddl.CreateTableReady.Describe`, so the check sees what a `Col[T]` cannot: nullability as the DDL declares it, in both directions, and the declared SQL type. The type check is strict where `Compare`'s is lenient: `Compare` asks whether a Go type can hold a column, so `timestamp` and `timestamptz` both hold a `time.Time`; `CompareTables` asks whether the column is the one the DDL created, so it compares the catalogue's spelling with the one `schema.EngineType` writes, reading MySQL's `CatalogueColumn.ColumnType` (`varchar(255)`, where `Type` is `varchar`) for the length.

```go
desc, err := pg.CreateTable(Users).Column(/* … */).Describe()
cat, err := vet.ReadCatalogue(ctx, db, "postgres")
findings := vet.CompareTables("postgres", []schema.Table{desc}, cat)
```

| Finding | Kind |
|---|---|
| the table is not in the database | error |
| the column is not in the database | error |
| the DDL says `NOT NULL` and the database allows NULL, or the other way round | error |
| the column is not the type the DDL writes: a `timestamp without time zone` where the DDL says `timestamptz`, a `real` for `double precision`, a MySQL `varchar(100)` for `VARCHAR(255)` | error |
| the database has columns the DDL does not create | warning |

A primary-key column the DDL leaves nullable is not a finding, because Postgres and MySQL make every key column NOT NULL themselves; a key column the DDL says is NOT NULL and the database allows NULL in is, and SQLite can have one. A column wrong in both nullability and type gets both findings.

A default is compared by what it means, because no two engines print one alike: Postgres reads `DEFAULT -5` back as `'-5'::integer`, MySQL returns `TRUE` as `1` and `2.5` on a `DECIMAL(10,2)` as `2.50`, and SQLite returns the text as written. `CatalogueColumn.Default` holds each as SQL text, MySQL's unquoted value quoted by the reader, and two literals are equal when their values are: numbers numerically, booleans as 1 and 0, times as instants, strings exactly. A default on one side only, or two literals that differ, is an error; two expressions that still differ once lower-cased and stripped of whitespace, parentheses, casts and charset introducers are a warning, since the engine reprints an expression in a form nobody documents.

### Keys, indexes and foreign keys

`ReadKeys(ctx, db, dialect)` reads every table's primary key, its indexes on plain columns and its foreign keys into a `KeyCatalogue`: a `TableKeys` per table keyed like a `Catalogue`, and `DefaultSchema`, the connection's default schema, which lets a reference the DDL writes unqualified match the qualified name the database reports. `CompareKeys(tables, keys)` compares them with the DDL's descriptions:

| Finding | Kind |
|---|---|
| the primary key is other columns, or the same columns in another order | error |
| an index the DDL declares is not over the same columns | error |
| a unique index the DDL declares is not unique in the database | error |
| a foreign key the DDL declares is not in the database, references another table or columns, or has another ON DELETE or ON UPDATE | error |
| the database has a unique index or a foreign key the DDL does not create | warning |

Keys are matched by their columns, never by name, because SQLite reports no foreign-key names and an unnamed key's name is the engine's; an unstated action is `NO ACTION`, which every engine reports for it. An extra plain index is not reported, since MySQL makes one for every foreign key and `CREATE INDEX` is not part of a `CREATE TABLE`'s description, and an expression or partial index is skipped because `schema.Index` has no place for one. A catalogue action this reader has no name for is `ErrUnknownAction` rather than a guess. `CheckTables(ctx, db, dialect, tables)` is the boot check in one call: both catalogues read, both comparisons made.

```go
findings, err := vet.CheckTables(ctx, db, "postgres", []schema.Table{desc})
```

The same parent and child tables, with every default kind the DDL writes and a unique key and a foreign key with an action, are created by each engine's builders, checked clean, and drifted five ways with that engine's own SQL; `TestCheckTables_KeysAndDefaults_*` require exactly the five findings on SQLite, Postgres and MySQL. The readers' parsing and every error return also run on every `go test` against canned catalogue rows from a fault-injecting driver. A column declared with a raw spelling (an enum, a domain, `citext`) is checked for existence and nullability only, since matching the catalogue's own spelling would be a guess. Each finding is positioned at `table.column`, or `schema.table.column` for a table with a schema, since a description has no source line. The same drift, made with each engine's own SQL, is reported identically on SQLite, on Postgres with `SQLB_PG_DSN` set, and on MySQL with `SQLB_MYSQL_DSN` set.

The diff is tested against an in-memory SQLite database on every `go test`, and against a real Postgres when `SQLB_PG_DSN` is set — the same variable `contrib/driver-pgx` uses — with a skip that says `SKIPPED, NOT PASSED` rather than passing quietly without it. and a real MySQL when `SQLB_MYSQL_DSN` is set. Every engine reports the same findings for the same probe.

The type table is `schema`'s, the same one `contrib/migrate` renders `CREATE TABLE` from, so the vet and the DDL renderer cannot disagree about what a `bigint` is: a catalogue type is mapped back to the portable `schema.Type` values it spells, and the Go type must be one `schema.GoTypes` allows for any of them. On Postgres the catalogue's spelling is normalised (`timestamp with time zone`, `character varying`); on SQLite a declared type is free text resolved by the engine's own affinity rule, so a `TEXT` column may be a text, a timestamp, a uuid or a json column and the declaration is right if it fits any of them, and a column of `NUMERIC` affinity — which is what `DATETIME` and `DECIMAL` resolve to — holds every storage class and gets no opinion. Only a basic type, `[]byte` and `time.Time` are checked; a caller's own type such as `pg.JSON[T]` or anything implementing `sql.Scanner` carries its own contract with the driver and is left alone. A catalogue type the schema layer has no portable name for, such as an array or an enum, is accepted rather than guessed at.

## The escape-hatch listing

`Escapes` is not in `Analyzers`; it reports nothing wrong. It lists every place the compiler has been asked to step aside — `Raw`, `Literal`, `New`, `PredicateOf`, a `JoinType` or `SetOp` keyword, and a `Col` whose name is not a constant — as warnings, for review. It runs when asked for, with `--escapes` from the CLI or `RunWith` from code.

## Running it

Four ways, one implementation.

From a test in the application, on every `go test`, with no binary installed — `Check` takes a `TB`, fails the test once per error `Finding` and logs each warning:

```go
func TestSQL(t *testing.T) { vet.Check(t, "./...") }
```

From the command line, via `cmd/sqlb`, which prints one finding per line as `file:line:col: analyzer: message` and exits 1 when any is an error, 0 when there are only warnings, 2 on a load error:

```
go run github.com/ubgo/sqlb/contrib/vet/cmd/sqlb vet ./...
go run github.com/ubgo/sqlb/contrib/vet/cmd/sqlb vet --escapes ./...
go run github.com/ubgo/sqlb/contrib/vet/cmd/sqlb vet --dsn postgres://localhost/app ./...
go run github.com/ubgo/sqlb/contrib/vet/cmd/sqlb vet --dsn ./app.db ./...
```

`--dsn` picks the driver from the connection string: a `postgres://` or `postgresql://` URL is Postgres through `pgx`, anything else is a SQLite path through `modernc.org/sqlite`. The source checks always run; `--dsn` adds the schema diff.

To keep a binary instead, `go install github.com/ubgo/sqlb/contrib/vet/cmd/sqlb@latest`, or install the prebuilt `sqlb` and `sqlbvet` together from the [`sqlb` releases](https://github.com/ubgo/sqlb/releases?q=sqlb%2F&expanded=true) with the install script, which takes `VERSION=v0.1.1` to pin a release and `INSTALL_DIR=$HOME/.local/bin` to install without sudo; on Windows, `install.ps1` beside it:

```
curl -fsSL https://raw.githubusercontent.com/ubgo/sqlb/main/install.sh | sh
```

Under `go vet`, through `cmd/sqlbvet`, a `unitchecker` main over the same analyzers, for the places that already invoke `go vet` such as a golangci-lint configuration or an editor. The schema diff and the escape-hatch listing stay in `cmd/sqlb`, because the vet protocol carries neither a flag nor a connection. `TestVettool_RunsUnderGoVet` builds it and drives it through `go vet -vettool` over packages it generates, since a unitchecker main does nothing outside that protocol. One caveat, found by that test and reproduced by hand on Go 1.27.1: `go vet` caches a package's vet result in a way that does not carry a custom tool's findings, so a second `go vet -vettool` over an unchanged package — or one a plain `go vet` has already seen — reports nothing. Changed sources or `go clean -cache` make it run again. For a gate, use `vet.Check` or `cmd/sqlb vet`, which load and analyse every time; the vettool suits an editor, where the package at hand is the one that just changed:

```
go build -o sqlbvet github.com/ubgo/sqlb/contrib/vet/cmd/sqlbvet
go vet -vettool=./sqlbvet ./...
```

The install script above puts a prebuilt `sqlbvet` beside `sqlb`, so it can be used without building it first; it is also published on its own as `sqlbvet_<version>_<os>_<arch>` archives on the [`sqlbvet` releases](https://github.com/ubgo/sqlb/releases?q=sqlbvet%2F&expanded=true).

Programmatically, `Run(dir, patterns...)` returns the `Finding` values — each carrying `Pos`, `Analyzer`, `Message` and `Warning` — and `RunWith` takes the analyzer set; `Failing` says whether any finding is an error, which is what decides an exit code.

## Why a contrib module

The analyzers depend on `golang.org/x/tools`, the schema check on two database drivers, and the core depends on nothing but the standard library. The CLI lives here for the same reason, as `contrib/vet/cmd/sqlb`. The drivers are imported directly rather than through `contrib/driver-sqlite` and `contrib/driver-pgx`, because those add pragmas and pool settings a checker reading a catalogue does not need, and no contrib module depends on a sibling.
