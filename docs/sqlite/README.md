# `sqlite` — SQLite statements and constructs

```go
import "github.com/ubgo/sqlb/sqlite"
```

Everything SQLite in one import: the statements start here (`sqlite.Select`, `sqlite.Insert`, `sqlite.Update`, `sqlite.Delete`), and the functions and clauses SQLite spells differently from everyone else are here too, built on the core's extension points.

It is the one engine package whose SQL is **verified rather than asserted**. `contrib/driver-sqlite` executes every helper here against a real database, because SQLite's driver is pure Go and needs no server. `pg` is executed too, against a Postgres server by `contrib/driver-pgx` when one is configured; `ch` is checked as rendered text only.

## Contents

- [Clause types](#clause-types)
- [Setup](#setup)
- [Starting a statement](#starting-a-statement)
- [JSON without a JSON type](#json-without-a-json-type)
- [Upsert, and the four OR actions](#upsert-and-the-four-or-actions)
- [Full-text search with FTS5](#full-text-search-with-fts5)
- [Dates, which are not a type either](#dates-which-are-not-a-type-either)
- [rowid, and the connection-scoped functions](#rowid-and-the-connection-scoped-functions)
- [Index hints](#index-hints)
- [Temporary objects, and schemas](#temporary-objects-and-schemas)
- [Copying a table with CREATE TABLE AS SELECT](#copying-a-table-with-create-table-as-select)
- [Keys: collation, order and conflict](#keys-collation-order-and-conflict)
- [Refusing a row from a trigger](#refusing-a-row-from-a-trigger)
- [Virtual tables beyond FTS5](#virtual-tables-beyond-fts5)
- [PRAGMA: settings and checks](#pragma-settings-and-checks)
- [Time modifiers, and the silent NULL](#time-modifiers-and-the-silent-null)
- [Building your own helper](#building-your-own-helper)
- [Reference](#reference)

## Clause types

Every clause constructor in this package returns one of four aliases for the core's typed clause, so a statement of another engine or of the wrong statement kind refuses it at compile time: `sqlite.SelectClause`, `sqlite.InsertClause`, `sqlite.UpdateClause` and `sqlite.DeleteClause`, each an alias for `sqlb.Clause[sqlite.Dialect, sqlb.<Kind>Kind]`. `ClauseIn[K]` builds one for an extension package, with the statement kind named by the caller.

## Setup

Every snippet on this page uses these declarations.

```go
import (
	"github.com/ubgo/sqlb/expr"
)

// A library declares its table and its typed columns, and exports them.
var (
	Docs      = expr.Table{Name: "docs", Alias: "d"}
	DocID     = expr.Col[string](Docs, "id")
	DocTitle  = expr.Col[string](Docs, "title")
	DocBody   = expr.Col[string](Docs, "body")
	DocMeta   = expr.Col[string](Docs, "meta")
	DocViews  = expr.Col[int](Docs, "views")
	DocMadeAt = expr.Col[string](Docs, "made_at")
)
```

## Starting a statement

A SQLite user imports this one package. Statements start here, and every SQLite construct that goes inside them is here too, so there is no second package to learn. `Select` takes the select items directly, in the order the SQL reads them; `Insert`, `Update` and `Delete` take the table. Everything after the first call is `stmt`.

```go
sqlite.Select(DocID, DocTitle).From(Docs).Where(expr.Eq(DocID, "p1")).Build()
sqlite.Insert(Docs).Row(stmt.Set(DocTitle, "Ada")).Build()
sqlite.Update(Docs).Set(stmt.Set(DocTitle, "x")).Where(expr.Eq(DocID, "p1")).Build()
sqlite.Delete(Docs).Where(expr.Eq(DocID, "p1")).Build()
```

`sqlite.Select()` with no items is `sqlb.NewSelect(sqlite.Dialect{})`, for a statement whose items are bindings added with `Columns`, which is what a query you intend to scan wants. Each constructor is exactly that long form and nothing more, which `TestEntry_IsTheLongFormExactly` pins byte for byte. What it adds is the dialect in the type: a statement started here is a SQLite statement to the compiler, so a Postgres clause on it does not compile.

A type named in user code is the alias, never the spelled-out dialect:

```go
func Lookup(id string) *sqlite.SelectQuery {
	return sqlite.Select(DocID).From(Docs).Where(expr.Eq(DocID, id))
}

var f *sqlite.FilteredUpdate
```

`SelectQuery`, `InsertQuery`, `InsertReady`, `UpdateQuery`, `FilteredUpdate`, `DeleteQuery` and `FilteredDelete` are aliases of the `stmt` types, so a value passes through either name with no conversion, pinned by `TestEntry_AliasesAreTheSameTypes`. The typestate on writes is unchanged: `InsertQuery`, `UpdateQuery` and `DeleteQuery` have no `Build`; `InsertReady`, `FilteredUpdate` and `FilteredDelete` do. The table's DDL starts here as well. `sqlite.CreateTable`, `sqlite.AlterTable` and `sqlite.DropTable` are `ddl.NewCreateTable`, `ddl.NewAlterTable` and `ddl.NewDropTable` with the dialect written in, pinned the same way by `TestEntry_DDLIsTheLongFormExactly`, and they take the columns the queries above already use:

```go
sqlite.CreateTable(Docs).IfNotExists().Column(ddl.Column(DocTitle)).Build()
```

The builders, the column types, the SQLite rules they follow and what they refuse are on the [`ddl` page](../ddl/README.md).

`InsertReturning`, `UpdateReturning` and `DeleteReturning` are here because `RETURNING` is a capability of the dialect, checked by a type constraint, and this is the package where the engine is already known.

These lived in a separate package, `liteq`, until 2026-09-21. They moved here because two packages that both read as "SQLite" left a reader guessing which one held a name.

## JSON without a JSON type

SQLite has no JSON column type. A JSON document is text, and the JSON1 extension is a set of functions over it — compiled in by default since 3.38, which is also when the `->` and `->>` operators arrived.

Every function here carries a `JSON` prefix, as `pg`'s JSON functions do: `sqlite.JSONSet`, `sqlite.JSONInsert`, `sqlite.JSONEach`. The package also starts statements, and `sqlite.Insert` is the `INSERT`, so `json_insert` cannot share the name.

The distinction that catches people is between the two arrows, and it is the reason both exist here:

```go
sqlite.JSONArrow(DocMeta, "$.owner")     // ("d"."meta" -> '$.owner')   always JSON text: a string keeps its quotes
sqlite.JSONArrowText(DocMeta, "$.owner") // ("d"."meta" ->> '$.owner')  the value as SQL text, unquoted
```

`->>` is what you want when scanning into a Go `string`; `->` is what you want when the result is going back into another JSON function. Getting it the wrong way round gives you `"ada"` including the quote characters, which scans fine and compares wrong.

Reading, writing and walking:

```go
sqlite.JSONArrayLength(DocMeta)                            // json_array_length("d"."meta")
sqlite.JSONSet(DocMeta, "$.seen", sqlb.Param(true))        // json_set: replace, or create if missing
sqlite.JSONInsert(DocMeta, "$.seen", sqlb.Param(true))     // json_insert: only if missing
sqlite.JSONReplace(DocMeta, "$.seen", sqlb.Param(true))    // json_replace: only if present
sqlite.JSONRemove(DocMeta, "$.seen")                       // json_remove
```

The value is a `SQLer` rather than a Go value, so it is `sqlb.Param(true)` and not `true`. That is deliberate: it is what lets another column or an expression go in the same position, and it renders as a bound `?` either way.

The three-way split between `JSONSet`, `JSONInsert` and `JSONReplace` is SQLite's, not ours, and it is genuinely useful: `JSONInsert` is a create-if-absent that will not clobber, which is otherwise a read-modify-write.

`sqlite.JSONEach` and `sqlite.JSONTree` are FROM sources rather than expressions — `json_each` walks one level, `json_tree` walks the whole document.

## Upsert, and the four OR actions

SQLite has two entirely different conflict mechanisms and they are not interchangeable.

`ON CONFLICT … DO UPDATE` is the targeted one. It names the conflicting columns and updates the existing row:

```go
q, err := stmt.NewInsert(sqlite.Dialect{}).
	Into(Docs).
	Row(stmt.Set(DocID, "d1"), stmt.Set(DocTitle, "Ada")).
	Clause(sqlite.OnConflict(DocID).
		DoUpdate(stmt.Set(DocTitle, "Ada")).
		Clause()).
	Build()
```

```sql
INSERT INTO "docs" ("id", "title") VALUES (?, ?) ON CONFLICT ("id") DO UPDATE SET "title" = ?
```

`OnConflict` is a small builder rather than one call, because the clause has four independent parts: the conflicting columns, an optional `WHERE` on the index (`TargetWhere`), the action, and an optional `WHERE` on the update (`UpdateWhere`). `DoNothing` ends it directly; `DoUpdate` returns the builder so `UpdateWhere` can follow, and `Clause()` closes it.

`OR REPLACE` is the blunt one: it **deletes the conflicting row and inserts a new one**, which means the row gets a new rowid, and anything referencing the old rowid is now wrong. That difference is executed and pinned in `contrib/driver-sqlite`, not assumed.

```go
stmt.NewInsert(sqlite.Dialect{}).
	Clause(sqlite.InsertOr(sqlite.OrReplace)).
	Into(Docs).
	Row(stmt.Set(DocID, "d1"))
// INSERT OR REPLACE INTO "docs" ("id") VALUES (?)
```

`OrAction` is a named type with five values, not a string, because they are a closed set whose differences are exactly the kind that do not announce themselves:

| Action | What it does on conflict |
|---|---|
| `sqlite.OrReplace` | deletes the conflicting row and inserts, so **the row gets a new rowid** |
| `sqlite.OrIgnore` | skips the offending row and continues |
| `sqlite.OrAbort` | fails the statement, undoing that statement only |
| `sqlite.OrFail` | fails the statement but **keeps the rows already changed by it** |
| `sqlite.OrRollback` | fails and rolls back the whole transaction |

`OrAbort` and `OrFail` are the pair worth reading twice: both report an error, and one of them leaves half your batch written.

## Full-text search with FTS5

FTS5 is a virtual table, so searching it means matching against the table rather than a column:

```go
fts := expr.Table{Name: "docs_fts", Alias: "f"}

q, err := stmt.NewSelect(sqlite.Dialect{}).
	Exprs(DocID, sqlite.BM25(fts)).
	From(fts).
	Where(sqlite.Match(expr.Col[string](fts, "docs_fts"), "ada NEAR lovelace")).
	Order(expr.OrderBy(sqlite.Rank(fts), expr.SortAsc)).
	Build()
```

```sql
SELECT "d"."id", bm25("docs_fts") FROM "docs_fts" AS "f" WHERE (("f"."docs_fts" MATCH ?)) ORDER BY "f"."rank" ASC
```

`sqlite.Rank(fts)` is the `rank` column FTS5 exposes, and it sorts **ascending** because a better match is a more negative number. `sqlite.BM25` gives the score directly and takes optional per-column weights. `sqlite.Highlight` and `sqlite.Snippet` return the matched text with markers around it, which is what a search results page needs and what you would otherwise write by hand from offsets.

## Dates, which are not a type either

As with JSON, SQLite stores a date as text, a number or a Julian day, and the functions do the interpreting:

```go
sqlite.Date(DocMadeAt, sqlite.StartOfMonth)   // date("d"."made_at", 'start of month')
sqlite.Strftime("%Y-%W", DocMadeAt)           // strftime('%Y-%W', "d"."made_at")
sqlite.UnixEpoch(DocMadeAt)                   // unixepoch("d"."made_at")
sqlite.DateTime(DocMadeAt, sqlite.LocalTime)  // datetime("d"."made_at", 'localtime')
```

The modifiers are constants of a named `Modifier` type, not function calls and not strings: `StartOfDay`, `StartOfMonth`, `StartOfYear`, `LocalTime`, `UTC`, `Auto`. They are a closed set, and a typo in one is a silently different query rather than an error. Every one of these functions takes them variadically, so they compose: `sqlite.Date(DocMadeAt, sqlite.StartOfMonth, sqlite.LocalTime)`.

## rowid, and the connection-scoped functions

Every SQLite table has an implicit `rowid` unless it was declared `WITHOUT ROWID`:

```go
sqlite.Rowid(Docs)  // "d"."rowid", typed Expr[int64]
```

Three functions answer questions about the **connection**, not the database, and that is the whole warning:

```go
sqlite.LastInsertRowid() // the rowid of the last insert on THIS connection
sqlite.Changes()         // rows changed by the last statement on THIS connection
sqlite.TotalChanges()    // rows changed since THIS connection was opened
```

With `database/sql` you do not choose the connection. Running one of these in a separate `Query` call can land on a different pooled connection and answer about somebody else's work. Use them inside a transaction, or use `RETURNING` instead.

## Index hints

`INDEXED BY` binds to a table reference rather than to the statement, so it goes on the driving table or on the join that needs it, never in a statement slot:

```go
stmt.NewSelect(sqlite.Dialect{}).Exprs(DocID).From(Docs).
	Clause(sqlite.IndexedBy("docs_by_made_at"))
// SELECT "d"."id" FROM "docs" AS "d" INDEXED BY "docs_by_made_at"

stmt.NewSelect(sqlite.Dialect{}).Exprs(DocID).From(Docs).
	Clause(sqlite.NotIndexed())
// SELECT "d"."id" FROM "docs" AS "d" NOT INDEXED
```

Neither takes the table: the clause is placed on a table reference, so which table it binds to is decided by where you put it. On the driving table it goes in the statement as above; on a joined table it goes in that join's `Clauses`.

`NOT INDEXED` forbids every index on that source, which is a debugging tool: it is how you find out whether an index is the reason a query is slow.

## Temporary objects, and schemas

Every SQLite object can be created in a schema, and most can be created as temporary. Both are written on the object value, so the schema an object was created in is the schema it is dropped from and the two cannot drift apart:

```go
sqlite.Index{Name: "docs_by_made_at", Schema: "main"}
sqlite.View{Name: "recent", Schema: "main"}
sqlite.Trigger{Name: "docs_guard", Schema: "main"}
```

`Temporary` puts the object in the `temp` schema, where it vanishes when the connection closes:

```go
sqlite.CreateTable(Scratch).Column(ddl.Column(ScratchID)).Clause(sqlite.Temporary())
// CREATE TEMPORARY TABLE "scratch" ("id" INTEGER NOT NULL)

sqlite.CreateView(sqlite.View{Name: "recent"}).Temporary().As(body)
sqlite.CreateTrigger(sqlite.Trigger{Name: "guard"}).Temporary().After(sqlite.OnInsert(), Docs).Do(step)
```

**There is no temporary index.** `CREATE TEMP INDEX` is a syntax error, measured on 3.50.3; a temporary index is written by putting the index in `temp` instead — `sqlite.Index{Name: "ix", Schema: "temp"}` — which is the form SQLite actually accepts.

A temporary object named in any schema but `temp` fails `Build` with `sqlite.ErrTemporaryQualified`, or `sqlite.ErrTemporaryTable` for a table, because SQLite refuses it: *temporary table name must be unqualified*. `Temporary()` on a table is a clause that checks the whole table rather than itself, since the name it must not qualify is the table's.

**With `database/sql` a temporary object is sharper than it looks.** The pool hands out connections, so an object created by one statement is invisible to the next unless both ran on the same connection. Create it inside a transaction, or on a connection taken with `Conn`, and never assume a later query will find it.

`CREATE VIEW` also takes its own column names, which SQLite's documentation recommends always giving because the rules by which it derives one from a result expression are undefined:

```go
sqlite.CreateView(Recent).Columns(RecentID, RecentText).As(sqlite.Select(DocID, DocBody).From(Docs))
// CREATE VIEW "recent" ("note_id", "text") AS SELECT ...
```

The columns are declared on a table of the view's own name — the value every later query against the view uses — so a view cannot be created under one column name and read under another. A column of any other table is `ddl.ErrForeignColumn`.

## Copying a table with CREATE TABLE AS SELECT

`sqlite.CreateTableAs` creates a table from a query's result and fills it in the same statement, which is how a staging copy or a backfill is made in one pass:

```go
sqlite.CreateTableAs(Staging, sqlite.Select(DocID, DocBody).From(Docs)).IfNotExists()
// CREATE TABLE IF NOT EXISTS "staging" AS SELECT "d"."id", "d"."body" FROM "docs" AS "d"
```

It is a statement of its own rather than a form of `sqlite.CreateTable`, because `ddl`'s typestate requires at least one column and a table whose body is a `SELECT` has no column list at all — SQLite answers *near "AS": syntax error* to one. `pg.CreateTableLike` exists for the same reason.

**The new table keeps none of the source's constraints.** Measured on 3.50.3 against a source declared `id INTEGER PRIMARY KEY, name TEXT NOT NULL`, the copy comes out `id INT, name TEXT`: no primary key, no `NOT NULL`, and the type is SQLite's reading of the expression rather than the declaration. A column computed by an expression gets no declared type at all, so it has blob affinity and will hold whatever it is given. A table whose shape matters is created by `CreateTable`, which declares it, and filled by an `INSERT` from a `SELECT`.

`IfNotExists` on this form means the query does not run either, so a second run appends nothing and the statement is repeatable in an install — measured, because "create it and fill it" being one statement makes the alternative a table that doubles its rows. `Temporary` and a schema on the table work as everywhere else. There are no table options and no column list, because SQLite has neither here; note that `STRICT` after the select is **not** refused — it parses as an alias for the last table in the query and silently does nothing, which is why it is not offered.

## Keys: collation, order and conflict

`PrimaryKey` and `Unique` take plain columns. `PrimaryKeyOn` and `UniqueOn` take key columns, which carry the two decorations SQLite indexes a key under:

```go
sqlite.UniqueOn(sqlite.Key(DocEmail).Collate(sqlite.NoCase))
// UNIQUE ("email" COLLATE NOCASE)

sqlite.PrimaryKeyOn(sqlite.Key(DocEmail).Collate(sqlite.NoCase).Asc(), sqlite.Key(DocID).Desc())
// PRIMARY KEY ("email" COLLATE NOCASE ASC, "id" DESC)
```

This is not decoration. `UNIQUE(email COLLATE NOCASE)` is a **different constraint** from `UNIQUE(email)`: without it the uniqueness is byte-exact, and `Ada@example.com` and `ada@EXAMPLE.com` both fit. That is almost never what a table storing email addresses means.

`ColumnKey` is the column-level primary key, the one place SQLite accepts a sort order and a conflict resolution on the key itself:

```go
sqlite.ColumnKey(DocEmail).OnConflict(sqlite.OrReplace)  // PRIMARY KEY ON CONFLICT REPLACE
sqlite.ColumnKey(DocID).Desc()                           // PRIMARY KEY DESC
```

`Desc` is the sharp one: **`INTEGER PRIMARY KEY DESC` is explicitly not an alias for the rowid**, so the column becomes an ordinary indexed column that may hold `NULL`. That is a change of storage, not of order, and `contrib/driver-sqlite` pins it by inserting a `NULL` key into both forms.

A `NOT NULL` violation can resolve by an action instead of failing:

```go
sqlite.CreateTable(Docs).
	Column(ddl.Column(DocID), ddl.Column(DocBody).Default("untitled")).
	Clause(sqlite.NotNullOnConflict(DocBody, sqlite.OrReplace))
// "body" TEXT NOT NULL ON CONFLICT REPLACE DEFAULT 'untitled'
```

Measured on 3.50.3, this is what each action does to a row that leaves the column null: `OrReplace` writes the column's **`DEFAULT`** and keeps the row — the reason to reach for the clause at all — `OrIgnore` skips the whole row and reports success, and `OrAbort`, `OrFail` and `OrRollback` all refuse it as the plain `NOT NULL` does, differing only in how much they undo. **`OrReplace` needs a default**, and since 2026-09-24 `Build` refuses one without it with `ddl.ErrNoDefault`. Without a default there is nothing to write in place of the null, and SQLite silently falls back to `ABORT`. It is refused rather than documented because of when the failure would otherwise arrive: SQLite accepts the definition, so the table installs and every test that never writes a `NULL` there passes, and the first `NULL` production writes fails with `NOT NULL constraint failed`. The other four actions never write the default, so they build without one.

The clause renders immediately after `NOT NULL`, which is the only position SQLite accepts — written after the `DEFAULT` it is a syntax error. On a column whose definition writes no `NOT NULL`, `Build` fails with `ddl.ErrNoNotNull` rather than rendering a bare `ON CONFLICT` that the engine would read as a constraint of its own.

**There is no conflict clause on `CHECK`, because SQLite does not honour one.** A column-level `CHECK (…) ON CONFLICT …` is a syntax error, which the railroad diagram does not say, and a table-level one is accepted and ignored: every action behaves as `ABORT`. Measured against a `UNIQUE` control where the same clause demonstrably works, so the result is the engine ignoring it rather than the measurement missing it.

A foreign key's deferrability is a closed set:

```go
sqlite.References(DocAuthor, AuthorID).Deferrable(sqlite.DeferrableInitiallyDeferred)
```

Only `DeferrableInitiallyDeferred` changes anything — it moves the check to `COMMIT`, so a batch may leave a reference dangling in the middle and repair it before the end. `NotDeferrable` is the default and `DeferrableInitiallyImmediate` means the same thing at run time; they exist because the spelling is recorded in the schema and read back by anything reading `sqlite_schema`. `Deferred()` is the shorthand for the one that matters.

## Refusing a row from a trigger

`RAISE` is how a trigger enforces a constraint a `CHECK` cannot express — one that looks at another table, or at the old and new row together:

```go
sqlite.CreateTrigger(sqlite.Trigger{Name: "docs_body_required"}).
	Before(sqlite.OnInsert(), Docs).
	When(sqlb.Eq(sqlite.New(DocBody), "")).
	Do(sqlite.StepSelect(sqlite.Select(sqlite.Raise(sqlite.RaiseAbort, "body required"))))
```

The insert then fails with `SQLITE_CONSTRAINT` and the message reaches the caller as the error text. `RaiseRollback` undoes the whole transaction, `RaiseAbort` undoes only the statement (SQLite's own default for a constraint violation), and `RaiseFail` stops the statement where it is and **keeps the changes it has already made**, which is almost never what a validation wants.

`sqlite.RaiseIgnore()` is the form with no message: the row is skipped and the caller is told the statement succeeded. Useful for a filter, dangerous for a validation, because a caller cannot tell it apart from a row that was written.

`RAISE` is legal only inside a trigger body — anywhere else SQLite answers *RAISE() may only be used within a trigger-program* — so it is the one construct here whose position is checked by the engine rather than by the type.

## Virtual tables beyond FTS5

Four constructors write `CREATE VIRTUAL TABLE`, and which one to reach for depends on how much of the module's grammar is worth typing.

`sqlite.CreateFTS5` and `sqlite.CreateRTree` type the two modules whose arguments are a grammar: a tokenizer, a prefix index, one to five dimensions. `sqlite.CreateGeopoly` types geopoly, whose arguments are ordinary column names, so each is checked to belong to the table being created exactly as in a `CREATE TABLE`:

```go
sqlite.CreateGeopoly(Docs, DocTitle)
// CREATE VIRTUAL TABLE "docs" USING geopoly("title")
```

geopoly stores the polygon itself in a hidden `_shape` column that the table does not declare and the `geopoly_*` functions read and write; the columns named here are the ordinary ones stored beside it. A geopoly table with no columns of its own is legal, and renders `USING geopoly` with no parentheses.

`sqlite.CreateVirtualTable` is the general form, for every other module — compiled in, or loaded at run time:

```go
sqlite.CreateVirtualTable(Docs, sqlite.ModuleDBStat)
// CREATE VIRTUAL TABLE "docs" USING dbstat

sqlite.CreateVirtualTable(Docs, "csv", "filename='rows.csv'", "header=true")
// CREATE VIRTUAL TABLE "docs" USING csv(filename='rows.csv', header=true)
```

**The arguments are the module's own language, and nothing here checks them.** SQLite hands the text between the parentheses to the module, which reads it as column declarations, as coordinates, as a filename, or as whatever it chose; they are written out exactly as given. They are authored text like every other value in `ddl` — never a value that arrived in a request, which is the one thing that cannot be made safe in that position. The module name itself is checked, because it is the only part that could carry text out of its position: anything but letters, digits and underscores is `sqlite.ErrModuleName`. Whether the module exists is the engine's answer, and a clear one: `no such module: …`.

`sqlite.ModuleFTS5`, `sqlite.ModuleRTree`, `sqlite.ModuleRTreeI32`, `sqlite.ModuleGeopoly` and `sqlite.ModuleDBStat` name the modules a stock SQLite ships. The type is `sqlite.Module`, a named string rather than a closed set, because a module can be loaded at run time and an exhaustive list would be a claim about somebody else's build.

## PRAGMA: settings and checks

Every pragma is a typed constructor over a closed set rather than a string, and the reason is the failure mode: SQLite does not refuse a pragma name it does not know, or a value it will not use — it ignores them, and the setting stays at its default with nothing to point at. Measured on 3.50.3, `PRAGMA page_size = 1000` returns no error and leaves the page size alone. `sqlite.SetPageSize` refuses that value instead.

```go
sqlite.SetJournalMode(sqlite.JournalWAL)                 // PRAGMA journal_mode = WAL
sqlite.SetSynchronous(sqlite.SynchronousNormal)          // PRAGMA synchronous = NORMAL
sqlite.SetBusyTimeout(5 * time.Second)                   // PRAGMA busy_timeout = 5000
sqlite.SetCacheSizeKibibytes(4000)                       // PRAGMA cache_size = -4000
sqlite.SetPageSize(8192)                                 // PRAGMA page_size = 8192
sqlite.SetAutoVacuum(sqlite.AutoVacuumIncremental)       // PRAGMA auto_vacuum = INCREMENTAL
sqlite.SetTempStore(sqlite.TempStoreMemory)              // PRAGMA temp_store = MEMORY
sqlite.SetSecureDelete(sqlite.SecureDeleteFast)          // PRAGMA secure_delete = FAST
sqlite.SetMmapSize(268435456)                            // PRAGMA mmap_size = 268435456
sqlite.RecursiveTriggers(true)                           // PRAGMA recursive_triggers = ON
sqlite.LegacyAlterTable(false)                           // PRAGMA legacy_alter_table = OFF
sqlite.DeferForeignKeys(true)                            // PRAGMA defer_foreign_keys = ON
sqlite.SetUserVersion(7)                                 // PRAGMA user_version = 7
sqlite.SetApplicationID(1397836866)                      // PRAGMA application_id = 1397836866
```

`sqlite.SetCacheSizePages` and `sqlite.SetCacheSizeKibibytes` are two constructors for one pragma because SQLite spells a memory budget as a *negative* number of kibibytes and a page count as a positive one. A caller who writes the number without the sign asks for that many pages instead, silently, which on a 4 KiB page is four times the memory they meant.

Five of these build with `OutsideTransaction` set — `journal_mode`, `synchronous`, `page_size`, `auto_vacuum` and `foreign_keys` — so a runner must execute them before opening a transaction. Each is measured rather than read: `synchronous` raises *Safety level may not be changed inside a transaction*; `journal_mode` raises *cannot change out of wal mode from within a transaction* in a transaction that has not written, and inside one that **has** written it returns no error and changes nothing, which is the silent half a flag guards and a comment does not. `defer_foreign_keys` is deliberately not marked, because SQLite clears it at every commit and inside a transaction is the only place it means anything.

Three pragmas are checks rather than settings, and they report a problem by **returning rows**, so they build with `MustReturnNoRows`:

```go
sqlite.ForeignKeyCheck()  // PRAGMA foreign_key_check
sqlite.IntegrityCheck()   // SELECT 1 FROM pragma_integrity_check WHERE "integrity_check" <> 'ok'
sqlite.QuickCheck()       // SELECT 1 FROM pragma_quick_check WHERE "quick_check" <> 'ok'
```

`ForeignKeyCheck` returns one row per dangling reference and nothing when the references are sound, which is the shape `MustReturnNoRows` was invented for; it is the check a table rebuild ends with. `IntegrityCheck` and `QuickCheck` are the opposite: both **always** return rows — the single row `ok` when they found nothing — so the bare pragma under `MustReturnNoRows` would fail every healthy database. They are therefore built as a `SELECT` over SQLite's table-valued form of the pragma with the `ok` row filtered away, which makes "no rows" mean "nothing wrong" again. `IntegrityCheck` reads every page and compares every index against its table; `QuickCheck` skips the comparison, which is most of the cost. The `N` argument SQLite also accepts, a cap on how many errors are listed, is not offered: the table-valued form these are built on reads any argument as a table name and answers *no such table: 5*, measured, and a cap is meaningless under `MustReturnNoRows`, which asks only whether there is one error rather than how many.

Each check also takes one table — `sqlite.IntegrityCheckOf(Docs)`, `sqlite.QuickCheckOf(Docs)`, `sqlite.ForeignKeyCheckOf(Docs)` — which is the form a library owning a few tables in somebody else's database wants, since the whole-database check reads every page of every table including ones it did not write and cannot repair. The table's schema qualifies the pragma where it declares one.

A runner must honour all three flags, or a check reports success whatever it found. `runScript` in `contrib/driver-sqlite` is the reference implementation.

**What is not here, and why.** The introspection pragmas (`table_info`, `index_list`, `foreign_key_list`, `database_list`) are reads rather than statements, and belong in a `SELECT` through SQLite's `pragma_*` table-valued functions. The maintenance pragmas (`optimize`, `incremental_vacuum`, `wal_checkpoint`, `shrink_memory`, `analysis_limit`) and the resource limits (`max_page_count`, `soft_heap_limit`, `hard_heap_limit`, `threads`, `locking_mode`) are decisions about a running database rather than about a schema a library owns, and `locking_mode = EXCLUSIVE` additionally locks the file against every other process on the machine. `writable_schema` and `ignore_check_constraints` are left out because their purpose is to break the guarantees the rest of this package makes: one lets a caller corrupt a database that still opens, the other silently stops enforcing every `CHECK` the DDL declares. `case_sensitive_like`, `count_changes`, `full_column_names`, `short_column_names`, `temp_store_directory` and `data_store_directory` are deprecated, and several are already no-ops.

## Time modifiers, and the silent NULL

`sqlite.Date`, `Time`, `DateTime`, `JulianDay`, `UnixEpoch` and `Strftime` all take modifiers, applied left to right:

```go
sqlite.Date(DocMadeAt, sqlite.StartOfMonth, sqlite.Offset(1, sqlite.Month), sqlite.Offset(-1, sqlite.Day))
// the last day of the month, as SQLite spells it
```

`sqlite.Modifier` is deliberately an open string type, because it cannot be closed: an offset carries its number inside the string, so `+1 day`, `-7 days` and `+1.5 seconds` are all modifiers and no enumeration covers them. What makes an open type safe is not validation but **quoting** — every modifier is written through the dialect as a literal, so a value from anywhere reaches SQLite as one argument and cannot leave its position.

**What quoting cannot protect you from is a modifier SQLite does not recognise, because SQLite does not report one.** Measured on 3.50.3, `date('2020-03-15', '+1 banana')` returns **`NULL`** — not an error, `NULL`, from the whole function. So does `start of week`, which does not exist, and so does the empty string.

That is why `Offset` takes a closed `sqlite.Unit` rather than a string: `Day`, `Hour`, `Minute`, `Second`, `Month`, `Year`. A misspelled unit was a null date somebody reads months later; now it does not compile. An empty modifier is refused at `Build` with `sqlite.ErrEmptyModifier` for the same reason. Anything you spell yourself with `sqlite.Modifier("…")` is on you, and a `NULL` result is the symptom to look for.

Beside the fixed spellings, note `sqlite.Weekday(n)` — how you ask for the start of a week, since SQLite has no `start of week` — and `sqlite.Ceiling` / `sqlite.Floor`, which decide how a month or year offset lands on a day the target month does not have: 31 January plus one month is 2 March under `Ceiling` and 28 February under `Floor`.

## Building your own helper

Everything above is built on six exported constructors, and they are exported precisely so a construct this package lacks does not require a fork:

```go
// A SQLite function this package does not wrap.
func Soundex(e sqlb.Expr[string]) sqlb.Expr[string] {
	return sqlite.Func[string]("soundex", e)
}
```

`Expr`, `Pred`, `BinOp`, `BinPred`, `Func` and `ClauseIn` all carry the guard, so a helper you write fails closed under Postgres exactly as `sqlite.Glob` does. That is the same set `pg` exports, and it is what makes an extension package possible without editing the engine package.

## Reference

<!-- BEGIN GENERATED REFERENCE -->

Generated from the source by `task docs:reference`. Every exported name in `sqlite` appears here.

### entry

| Name | What it is |
|---|---|
| `sqlite.SelectQuery` | The statement constructors |
| `sqlite.InsertQuery` | the statement Insert returns |
| `sqlite.InsertReady` | a complete INSERT, the only form with Build |
| `sqlite.UpdateQuery` | the statement Update returns |
| `sqlite.FilteredUpdate` | an UPDATE that has said which rows, the only form with Build |
| `sqlite.DeleteQuery` | the statement Delete returns, with no Build until Where or All |
| `sqlite.FilteredDelete` | a DELETE that has said which rows, the only form with Build |
| `sqlite.Select` | Select starts a SELECT for SQLite |
| `sqlite.Insert` | Insert starts an INSERT into t for SQLite |
| `sqlite.Update` | Update starts an UPDATE of t for SQLite |
| `sqlite.Delete` | Delete starts a DELETE from t for SQLite |
| `sqlite.InsertReturning` | — |
| `sqlite.UpdateReturning` | — |
| `sqlite.DeleteReturning` | — |
| `sqlite.CreateTableQuery` | The DDL statement constructors, spellings of the ddl package's with the dialect written in, exactly as Select is of sqlb.NewSelect |
| `sqlite.CreateTableReady` | a CREATE TABLE with at least one column, the only form with Build |
| `sqlite.AlterTableQuery` | an ALTER TABLE with no action yet, which has no Build |
| `sqlite.AlterTableReady` | an ALTER TABLE with at least one action |
| `sqlite.DropTableQuery` | a DROP TABLE, complete from its constructor |
| `sqlite.CreateTable` | CreateTable starts a CREATE TABLE of t for SQLite |
| `sqlite.AlterTable` | AlterTable starts an ALTER TABLE of t for SQLite |
| `sqlite.DropTable` | DropTable drops one or more tables for SQLite, one statement per table |

### forms

| Name | What it is |
|---|---|
| `sqlite.CreateTableAsStatement` | CREATE TABLE .. |
| `sqlite.CreateTableAs` | CreateTableAs creates t from the result of a query, deriving each column's name and type from the select list and filling the table in the same statement |

### Full-text search

| Name | What it is |
|---|---|
| `sqlite.Tokenizer` | how an FTS5 table splits text into terms |
| `sqlite.Unicode61` | FTS5's default tokenizer, folding case for every script |
| `sqlite.ASCII` | ASCII folds only ASCII case, and is faster |
| `sqlite.Trigram` | Trigram indexes every three-character sequence, which serves substring search and LIKE rather than word search |
| `sqlite.Porter` | Porter stems English words over another tokenizer, so "running" matches "run" |
| `sqlite.CreateFTS5Query` | CREATE VIRTUAL TABLE .. |
| `sqlite.CreateFTS5` | CreateFTS5 starts an FTS5 table t, whose columns are declared on t as for any table and queried with Match, Rank and BM25 |
| `sqlite.CreateFTS5Statement` | a complete CREATE VIRTUAL TABLE .. |

### JSON and JSONB

| Name | What it is |
|---|---|
| `sqlite.JSONExtract` | The JSON1 extension, compiled into SQLite by default since 3.38 |
| `sqlite.JSONArrow` | e -> '$.path' (3.38), which always yields JSON text |
| `sqlite.JSONArrowText` | e ->> '$.path' (3.38), the extracted value as SQL text |
| `sqlite.JSONValid` | json_valid(e) |
| `sqlite.JSONType` | json_type(e): "object", "array", "text", "integer", "real", "true", "false" or "null" |
| `sqlite.JSONTypeAt` | json_type(e, '$.path'), the type at a path rather than of the whole document |
| `sqlite.JSONArrayLength` | json_array_length(e) |
| `sqlite.JSONArrayLengthAt` | json_array_length(e, '$.path') |
| `sqlite.JSONQuote` | json_quote(v), a Go value as a JSON literal |
| `sqlite.JSONArray` | json_array(items...) |
| `sqlite.ErrIncompleteSpec` | returned for a caller-written Pair with no Value: the type has exported fields and is written as a literal, so Pair{} is the easy mistake, and it rendered through a nil SQLer inside Build until 2026-09-22 |
| `sqlite.JSONObject` | json_object(k1, v1, k2, v2, ...) from alternating pairs |
| `sqlite.Pair` | one key and value for Object |
| `sqlite.Key` | — |
| `sqlite.Value` | — |
| `sqlite.KV` | a Pair |
| `sqlite.JSONSet` | json_set(e, '$.path', value): replaces the value at the path, and creates it when it is missing |
| `sqlite.JSONInsert` | json_insert(e, '$.path', value): creates the value only when the path is missing, and leaves an existing one alone |
| `sqlite.JSONReplace` | json_replace(e, '$.path', value): the mirror of Insert, replacing only what is already there |
| `sqlite.JSONRemove` | json_remove(e, '$.path', ...), dropping one or more paths |
| `sqlite.JSONPatch` | json_patch(target, patch), an RFC 7386 merge patch: a null in the patch deletes the key rather than setting it to null |
| `sqlite.JSONGroupArray` | json_group_array(e), the group as a JSON array |
| `sqlite.JSONGroupObject` | json_group_object(key, value) |
| `sqlite.JSONEach` | json_each(e) as a FROM source, one row per element of an array or member of an object |
| `sqlite.JSONTree` | json_tree(e) as a FROM source, which walks the whole document rather than one level |
| `sqlite.RawJSON` | json.RawMessage, re-exported so a caller declaring a JSON column does not need a second import to say what the column holds |

### Miscellaneous

| Name | What it is |
|---|---|
| `sqlite.Rowid` | Rowid references a table's implicit rowid column |
| `sqlite.LastInsertRowid` | last_insert_rowid(), the rowid of the most recent insert on this connection |
| `sqlite.Changes` | changes(), the rows modified by the most recent statement on this connection |
| `sqlite.TotalChanges` | total_changes(), rows modified since the connection was opened |
| `sqlite.Random` | random(), a signed 64-bit integer |
| `sqlite.RandomBlob` | randomblob(n), n cryptographically random bytes |
| `sqlite.Abs` | abs(e) |
| `sqlite.Max` | max(a, b, ...), the scalar many-argument form, which is a different function from the aggregate of the same name |
| `sqlite.Min` | min(a, b, ...), the scalar form |
| `sqlite.Version` | sqlite_version(), the library version as text |
| `sqlite.IndexedBy` | IndexedBy forces a query to use one index on a table reference, and NotIndexed forbids every index on one |
| `sqlite.NotIndexed` | NOT INDEXED, forbidding every index on the source |

### objects

| Name | What it is |
|---|---|
| `sqlite.ErrEmptyName` | returned for an object value with no name |
| `sqlite.ErrTemporaryQualified` | a temporary object whose name is qualified by a schema other than temp |
| `sqlite.Index` | a SQLite index, declared once as a value |
| `sqlite.IndexElement` | one key of an index: a column or an expression, with its sort order and collation |
| `sqlite.IndexKeyElem` | a column or expression key |
| `sqlite.IndexKey` | IndexKey indexes a column |
| `sqlite.IndexExpr` | IndexExpr indexes an expression; a query uses it only when it filters on the same expression |
| `sqlite.CreateIndexQuery` | a CREATE INDEX with no key yet, which has no Build |
| `sqlite.CreateIndexReady` | a CREATE INDEX with at least one key |
| `sqlite.CreateIndex` | CreateIndex starts an index ix on table t |
| `sqlite.DropStatement` | DROP INDEX, DROP VIEW or DROP TRIGGER, with IF EXISTS |
| `sqlite.DropIndex` | DropIndex drops an index |
| `sqlite.View` | a SQLite view |
| `sqlite.CreateViewQuery` | CREATE VIEW with no body yet, which has no Build |
| `sqlite.CreateView` | CreateView starts a view |
| `sqlite.CreateViewStatement` | a complete CREATE VIEW |
| `sqlite.DropView` | DropView drops a view |
| `sqlite.Trigger` | a SQLite trigger; its name is unique per database |
| `sqlite.TriggerEvent` | the one event that fires a SQLite trigger; SQLite takes one per trigger, so there is no list |
| `sqlite.OnInsert` | OnInsert fires on INSERT |
| `sqlite.OnUpdate` | OnUpdate fires on any UPDATE |
| `sqlite.OnUpdateOf` | OnUpdateOf fires on an UPDATE whose SET names one of cols |
| `sqlite.OnDelete` | OnDelete fires on DELETE |
| `sqlite.Old` | a column's value before the change, in a trigger's WHEN or body |
| `sqlite.New` | a column's value after the change |
| `sqlite.TriggerStep` | one statement in a trigger's body |
| `sqlite.StepInsert` | an INSERT in a trigger body |
| `sqlite.StepUpdate` | an UPDATE in a trigger body |
| `sqlite.StepDelete` | a DELETE in a trigger body |
| `sqlite.StepSelect` | a SELECT in a trigger body, which is how a trigger raises an error through a function such as RAISE |
| `sqlite.CreateTriggerQuery` | CREATE TRIGGER with no timing yet, which has no Build |
| `sqlite.CreateTrigger` | CreateTrigger starts a trigger |
| `sqlite.TimedTriggerQuery` | CREATE TRIGGER with its timing and no body yet |
| `sqlite.CreateTriggerStatement` | a complete CREATE TRIGGER |
| `sqlite.RaiseAction` | what RAISE does to the statement that fired the trigger |
| `sqlite.RaiseRollback` | — |
| `sqlite.RaiseAbort` | — |
| `sqlite.RaiseFail` | — |
| `sqlite.Raise` | Raise refuses the row from inside a trigger, with a message the caller receives as the error text |
| `sqlite.RaiseIgnore` | RaiseIgnore abandons the rest of the trigger and the statement that fired it, without an error and without undoing anything |
| `sqlite.DropTrigger` | DropTrigger drops a trigger |

### pragma

| Name | What it is |
|---|---|
| `sqlite.ErrPageSize` | a page size SQLite would silently ignore: it takes a power of two from 512 to 65536 and drops anything else without an error |
| `sqlite.ErrNegativeTimeout` | a busy timeout below zero, which SQLite reads as no wait at all rather than as the wait the caller meant |
| `sqlite.PragmaStatement` | a PRAGMA: one that sets a value, or one of the three checks that report a damaged database by returning rows |
| `sqlite.SetUserVersion` | SetUserVersion sets PRAGMA user_version, an integer in the database header that SQLite stores and never reads: a migration ledger with no table, for a database one library owns |
| `sqlite.SetApplicationID` | SetApplicationID sets PRAGMA application_id, an integer in the database header that says which application a file belongs to, the way a file format's magic number does |
| `sqlite.ForeignKeys` | ForeignKeys turns foreign-key enforcement on or off for the connection |
| `sqlite.DeferForeignKeys` | foreign-key enforcement until the transaction commits, so a batch of statements may leave a reference dangling in the middle and repair it before the end |
| `sqlite.RecursiveTriggers` | RecursiveTriggers turns recursive trigger firing on or off |
| `sqlite.LegacyAlterTable` | LegacyAlterTable makes ALTER TABLE .. |
| `sqlite.JournalMode` | how SQLite journals a transaction, which decides what happens to a database when the process dies mid-write |
| `sqlite.JournalDelete` | — |
| `sqlite.JournalTruncate` | — |
| `sqlite.JournalPersist` | — |
| `sqlite.JournalMemory` | — |
| `sqlite.JournalWAL` | — |
| `sqlite.JournalOff` | — |
| `sqlite.SetJournalMode` | SetJournalMode sets PRAGMA journal_mode |
| `sqlite.Synchronous` | how hard SQLite makes the filesystem promise a commit reached the disk |
| `sqlite.SynchronousOff` | — |
| `sqlite.SynchronousNormal` | — |
| `sqlite.SynchronousFull` | — |
| `sqlite.SynchronousExtra` | — |
| `sqlite.SetSynchronous` | SetSynchronous sets PRAGMA synchronous, per connection |
| `sqlite.TempStore` | where SQLite puts a temporary table or index, and the sort files a large ORDER BY or a VACUUM needs |
| `sqlite.TempStoreDefault` | — |
| `sqlite.TempStoreFile` | — |
| `sqlite.TempStoreMemory` | — |
| `sqlite.SetTempStore` | SetTempStore sets PRAGMA temp_store, per connection |
| `sqlite.AutoVacuum` | what SQLite does with the pages a DELETE or a DROP freed |
| `sqlite.AutoVacuumNone` | — |
| `sqlite.AutoVacuumFull` | — |
| `sqlite.AutoVacuumIncremental` | — |
| `sqlite.SetAutoVacuum` | SetAutoVacuum sets PRAGMA auto_vacuum |
| `sqlite.SecureDelete` | what SQLite writes over a deleted row's bytes |
| `sqlite.SecureDeleteOff` | — |
| `sqlite.SecureDeleteOn` | — |
| `sqlite.SecureDeleteFast` | — |
| `sqlite.SetSecureDelete` | SetSecureDelete sets PRAGMA secure_delete |
| `sqlite.SetBusyTimeout` | SetBusyTimeout sets PRAGMA busy_timeout: how long a statement waits for a lock another connection holds before it gives up with SQLITE_BUSY |
| `sqlite.SetCacheSizePages` | SetCacheSizePages sets PRAGMA cache_size as a number of database pages, which is what a positive value means to SQLite |
| `sqlite.SetCacheSizeKibibytes` | SetCacheSizeKibibytes sets PRAGMA cache_size as an amount of memory |
| `sqlite.SetMmapSize` | SetMmapSize sets PRAGMA mmap_size, the most of the database file SQLite may read through a memory mapping instead of through read calls |
| `sqlite.SetPageSize` | SetPageSize sets PRAGMA page_size, the size of every page in the database file |
| `sqlite.IntegrityCheck` | IntegrityCheck verifies the whole database: every page, every index entry against its table, every NOT NULL and every UNIQUE |
| `sqlite.QuickCheck` | IntegrityCheck without the index-against-table comparison, which is most of the cost |
| `sqlite.ForeignKeyCheck` | every row whose foreign key points at a row that is not there, one row per violation and nothing at all when the references are sound |
| `sqlite.IntegrityCheckOf` | IntegrityCheck over one table rather than the whole database: its rows, its indexes and the constraints between them |
| `sqlite.QuickCheckOf` | QuickCheck over one table; see IntegrityCheckOf |
| `sqlite.ForeignKeyCheckOf` | every row of one table whose foreign key points at a row that is not there |

### rebuild

| Name | What it is |
|---|---|
| `sqlite.ErrRebuildIncomplete` | returned for a rebuild with no new definition or no column copied |
| `sqlite.ErrRebuildSameName` | returned for a rebuild whose new table has the old one's name; the new one is created alongside and renamed into place |
| `sqlite.CopyPair` | one column of a rebuild: the new table's column and the expression over the old table that fills it |
| `sqlite.CopyAs` | CopyAs fills a column of the new table with e, an expression of the new column's own Go type over the old table, so a type change is only writable with a conversion that produces the new type: text to integer needs a cast returning int64 |
| `sqlite.RebuildQuery` | a table rebuild with no new definition yet |
| `sqlite.Rebuild` | Rebuild starts the table rebuild SQLite's documentation gives for every change ALTER TABLE cannot make: a column's type, a constraint, a key |
| `sqlite.RebuildStatement` | a rebuild with its new definition |

### rtree

| Name | What it is |
|---|---|
| `sqlite.ErrRTreeDims` | an R*Tree with no dimension or more than five, the range SQLite accepts |
| `sqlite.Dim` | one R*Tree dimension: the columns holding its minimum and maximum |
| `sqlite.Dimension` | Dimension pairs a dimension's minimum and maximum columns |
| `sqlite.CreateRTreeQuery` | CREATE VIRTUAL TABLE .. |
| `sqlite.CreateRTree` | CreateRTree starts an R*Tree table t, whose columns are declared on t as for any table |
| `sqlite.CreateRTreeStatement` | a complete CREATE VIRTUAL TABLE .. |

### Dialect and core

| Name | What it is |
|---|---|
| `sqlite.Dialect` | the SQLite dialect, re-exported so a caller that already imports this package for its constructs does not need a second import to name the engine |
| `sqlite.Is` | whether d is the SQLite dialect |
| `sqlite.SelectClause` | a clause for a SELECT on this engine |
| `sqlite.InsertClause` | a clause for an INSERT on this engine |
| `sqlite.UpdateClause` | a clause for an UPDATE on this engine |
| `sqlite.DeleteClause` | a clause for a DELETE on this engine |
| `sqlite.Guard` | The exported constructors an application builds its own helpers on, the same set pg exports |
| `sqlite.Expr` | Expr makes a guarded typed expression from a render function |
| `sqlite.Pred` | Pred makes a guarded predicate from a render function |
| `sqlite.BinOp` | "left op right" as a guarded expression typed R |
| `sqlite.BinPred` | "left op right" as a guarded predicate |
| `sqlite.Func` | a guarded function call typed R |
| `sqlite.ClauseIn` | ClauseIn makes a guarded clause for a statement slot, typed to this engine and to the statement kind K, which the caller names: an extension package writing a SELECT clause instantiates ClauseIn[sqlb.SelectKind] |
| `sqlite.QuoteLit` | an author-chosen constant as a single-quoted literal |
| `sqlite.List` | SQLers comma-separated |

### table

| Name | What it is |
|---|---|
| `sqlite.TableClause` | a SQLite clause for CREATE TABLE |
| `sqlite.AlterClause` | a SQLite ALTER TABLE action |
| `sqlite.ErrUnknownKeyword` | returned for a value outside its named constants, such as a zero referential action or collation |
| `sqlite.ErrMixedReferences` | returned for a foreign key whose referenced columns are on more than one table |
| `sqlite.ErrTemporaryTable` | a CREATE TEMP TABLE whose table names a schema other than temp, which SQLite refuses with "temporary table name must be unqualified", measured on 3.50.3 |
| `sqlite.ErrAddStored` | returned for a stored generated column in ADD COLUMN, which SQLite refuses because every existing row would need computing; use GeneratedVirtual, or rebuild the table |
| `sqlite.KeyColumn` | one column of a table-level key, with the collation and sort order SQLite indexes it under |
| `sqlite.KeyClause` | a PRIMARY KEY or UNIQUE table constraint |
| `sqlite.TableConstraint` | a named constraint of one table, declared once |
| `sqlite.PrimaryKey` | PrimaryKey makes the table's primary key |
| `sqlite.Unique` | Unique makes a unique constraint over one or more columns |
| `sqlite.PrimaryKeyOn` | PrimaryKeyOn makes the table's primary key over decorated key columns, for a key that needs a collation or a sort order; PrimaryKey is the plain form |
| `sqlite.UniqueOn` | UniqueOn makes a unique constraint over decorated key columns |
| `sqlite.RefAction` | what a foreign key does when the row it references is deleted or updated |
| `sqlite.RefNoAction` | — |
| `sqlite.RefRestrict` | — |
| `sqlite.RefCascade` | — |
| `sqlite.RefSetNull` | — |
| `sqlite.RefSetDefault` | — |
| `sqlite.ColumnPair` | one column of a foreign key and the column it references |
| `sqlite.RefPair` | RefPair pairs a column with the one it references, of one Go type |
| `sqlite.ForeignKeyClause` | a FOREIGN KEY table constraint |
| `sqlite.ForeignKey` | ForeignKey makes a named foreign key from one or more pairs on one table |
| `sqlite.References` | the one-column foreign key |
| `sqlite.Deferrability` | when SQLite checks a foreign key |
| `sqlite.NotDeferrable` | — |
| `sqlite.DeferrableInitiallyDeferred` | — |
| `sqlite.DeferrableInitiallyImmediate` | — |
| `sqlite.AutoincrementKey` | AutoincrementKey makes col the INTEGER PRIMARY KEY with AUTOINCREMENT, so a rowid is never reused, even after the row with the largest one is deleted |
| `sqlite.ColumnKeyClause` | a column-level PRIMARY KEY, with the sort order and the conflict resolution SQLite lets it carry |
| `sqlite.ColumnKey` | ColumnKey makes col the table's primary key, written in the column's own definition rather than as a table constraint |
| `sqlite.NotNullConflictClause` | NotNullConflictClause resolves a NOT NULL violation by an action instead of failing |
| `sqlite.NotNullOnConflict` | NotNullOnConflict says what SQLite does with a row that leaves col null, instead of refusing it |
| `sqlite.Collation` | one of SQLite's built-in collations |
| `sqlite.Binary` | — |
| `sqlite.NoCase` | — |
| `sqlite.RTrim` | — |
| `sqlite.ColumnCollate` | ColumnCollate gives a text column a collation, which every comparison, index and UNIQUE on it then uses |
| `sqlite.GeneratedVirtual` | GeneratedVirtual makes col computed by e on every read and stored nowhere |
| `sqlite.GeneratedStored` | GeneratedStored makes col computed by e on every write and stored, which an index on it needs to be cheap |
| `sqlite.Strict` | Strict makes SQLite enforce column types, which a library that scans into typed Go values wants: without it a TEXT column accepts an integer and the scan fails later, far from the write |
| `sqlite.Temporary` | Temporary makes the table live in the temp schema and vanish when the connection closes |
| `sqlite.WithoutRowID` | WithoutRowID stores the table as its primary key's index, which a table keyed by something other than an integer is smaller and faster as |
| `sqlite.AddColumn` | AddColumn adds a column with column clauses, a collation or a virtual generated expression, under the same refusals ddl.AddColumn has on SQLite: a NOT NULL column needs a literal default, a default cannot be an expression, and a key constraint needs a unique index instead |
| `sqlite.RenameTo` | RenameTo renames the table to to's name |

### Text

| Name | What it is |
|---|---|
| `sqlite.Glob` | e GLOB $n, Unix shell wildcard matching |
| `sqlite.NotGlob` | e NOT GLOB $n |
| `sqlite.Match` | e MATCH $n, the full-text search operator |
| `sqlite.Rank` | Rank references the FTS5 rank column, which is the relevance of the current match |
| `sqlite.BM25` | bm25(table), the FTS5 relevance score, optionally weighted per column |
| `sqlite.Highlight` | highlight(table, column, openTag, closeTag), the matched text with markers around the matching terms |
| `sqlite.Snippet` | snippet(table, column, open, close, ellipsis, tokens), a short extract around the match rather than the whole column |
| `sqlite.Instr` | instr(haystack, needle): the 1-based position, or 0 when absent |
| `sqlite.Hex` | hex(e), the uppercase hexadecimal of a blob or text |
| `sqlite.QuoteSQL` | quote(e), the value as a SQL literal |
| `sqlite.Typeof` | typeof(e): "null", "integer", "real", "text" or "blob" |
| `sqlite.IfNull` | ifnull(a, b), COALESCE with exactly two arguments |
| `sqlite.IIF` | iif(cond, then, else) (3.32), the expression form of CASE |
| `sqlite.Format` | format(fmt, args...) (3.38), printf-style formatting |
| `sqlite.GroupConcat` | group_concat(e, sep) |
| `sqlite.Length` | length(e), the number of characters in a text value, which is what a CHECK on a column's length means |
| `sqlite.Substr` | substr(e, start), the text from the 1-based position start to the end |
| `sqlite.SubstrN` | substr(e, start, n), at most n characters from start |
| `sqlite.IsNot` | l IS NOT r, SQLite's null-safe inequality: true when exactly one side is NULL, where <> would be NULL |
| `sqlite.IsEqual` | l IS r, SQLite's null-safe equality: two NULLs are equal |

### Dates and times

| Name | What it is |
|---|---|
| `sqlite.Modifier` | Time in SQLite |
| `sqlite.StartOfDay` | — |
| `sqlite.StartOfMonth` | — |
| `sqlite.StartOfYear` | — |
| `sqlite.UTC` | — |
| `sqlite.LocalTime` | — |
| `sqlite.Unixepoch` | — |
| `sqlite.JulianDayMod` | — |
| `sqlite.Auto` | — |
| `sqlite.Subsec` | Subsec keeps fractional seconds in the result, which the other functions truncate away |
| `sqlite.Ceiling` | Ceiling and Floor decide how a month-or-year offset lands on a day the target month does not have: 31 January plus one month is 2 March under Ceiling and 28 February under Floor |
| `sqlite.Floor` | — |
| `sqlite.ErrEmptyModifier` | an empty time modifier, which SQLite answers with NULL from the whole function rather than with an error |
| `sqlite.Unit` | the unit of a relative time offset |
| `sqlite.Day` | — |
| `sqlite.Hour` | — |
| `sqlite.Minute` | — |
| `sqlite.Second` | — |
| `sqlite.Month` | — |
| `sqlite.Year` | — |
| `sqlite.Date` | date(e, modifiers...), the date part as "YYYY-MM-DD" |
| `sqlite.Time` | time(e, modifiers...), the time part as "HH:MM:SS" |
| `sqlite.DateTime` | datetime(e, modifiers...) as "YYYY-MM-DD HH:MM:SS" |
| `sqlite.JulianDay` | julianday(e, modifiers...), days since noon in Greenwich on 24 November 4714 BC |
| `sqlite.UnixEpoch` | unixepoch(e, modifiers...) (3.38), seconds since 1970 |
| `sqlite.Strftime` | strftime(format, e, modifiers...), the general formatter every other function here is a shorthand for |
| `sqlite.Now` | the literal 'now', the argument every time function takes to mean the current instant |
| `sqlite.Offset` | a relative time modifier: Offset(-7, Day) is "-7 days" |
| `sqlite.Weekday` | Weekday moves forward to the next date whose day of the week is n, 0 for Sunday through 6 for Saturday, and does nothing when the date already is that day |
| `sqlite.TimeLit` | TimeLit binds a Go time as the ISO-8601 text SQLite compares and sorts correctly |

### virtual

| Name | What it is |
|---|---|
| `sqlite.Module` | the name of a SQLite virtual-table module |
| `sqlite.ModuleFTS5` | — |
| `sqlite.ModuleRTree` | — |
| `sqlite.ModuleRTreeI32` | — |
| `sqlite.ModuleGeopoly` | — |
| `sqlite.ModuleDBStat` | — |
| `sqlite.ErrNoModule` | a CREATE VIRTUAL TABLE with no module name |
| `sqlite.ErrModuleName` | a module name that is not a bare identifier |
| `sqlite.CreateVirtualTableStatement` | a complete CREATE VIRTUAL TABLE for a module this package has no builder for, or for geopoly |
| `sqlite.CreateVirtualTable` | CreateVirtualTable creates table t with module m and the arguments args |
| `sqlite.CreateGeopoly` | CreateGeopoly creates a geopoly table: SQLite's polygon index, which answers "which shapes contain this point" and "which overlap this box" without scanning |

### Writes

| Name | What it is |
|---|---|
| `sqlite.OrAction` | Conflict resolution |
| `sqlite.OrRollback` | — |
| `sqlite.OrAbort` | — |
| `sqlite.OrFail` | — |
| `sqlite.OrIgnore` | — |
| `sqlite.OrReplace` | — |
| `sqlite.InsertOr` | INSERT OR <action> INTO, the clause between the keyword and the table |
| `sqlite.Conflict` | Conflict accumulates an ON CONFLICT clause |
| `sqlite.OnConflict` | OnConflict targets the given columns, which must be a unique index |
| `sqlite.Excluded` | Excluded references the row that would have been inserted, inside a DO UPDATE |

304 exported names.

<!-- END GENERATED REFERENCE -->
