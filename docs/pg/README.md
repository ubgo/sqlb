# `pg` — PostgreSQL statements and constructs

```go
import "github.com/ubgo/sqlb/pg"
```

Everything Postgres in one import: the statements start here (`pg.Select`, `pg.Insert`, `pg.Update`, `pg.Delete`), and every construct that goes inside them is here too. By a wide margin the largest package in this repository.

That ratio is the universality rule working rather than a problem. A construct lives in the core only when Postgres, MySQL 8, SQLite and ClickHouse all spell it the same way and mean the same thing, and almost nothing useful passes that test. The core keeps the mechanisms — `Aggregate`, `WinFn`, `Window`, `Fn`, `Clause`, `SourceOf` — and this package makes the decisions about which functions and clauses exist.

**Every construct here is executed against a Postgres server**, by [`contrib/driver-pgx`](../../contrib/driver-pgx), in addition to the rendering tests in this package. The rendering tests catch a wrong operator, a missing guard or a precedence bug; the execution catches SQL that Postgres rejects, which the rendering tests cannot, and the first full run found eleven such cases — see [a bare parameter has no type](#a-bare-parameter-has-no-type-in-some-positions). Two constructs, `ArrayReverse` and `ArraySort`, need Postgres 18 and skip on older servers; `TerminateBackend` is the one construct not executed, because it would end the test's own connection.

## Contents

- [A bare parameter has no type in some positions](#a-bare-parameter-has-no-type-in-some-positions)
- [Clause types](#clause-types)
- [Setup](#setup)
- [Starting a statement](#starting-a-statement)
- [Arrays](#arrays)
- [JSON and JSONB](#json-and-jsonb)
- [Ranges](#ranges)
- [Full-text search](#full-text-search)
- [Window functions](#window-functions)
- [Locking](#locking)
- [Writes beyond INSERT](#writes-beyond-insert)
- [Dates and times](#dates-and-times)
- [Everything fails closed](#everything-fails-closed)
- [Building your own helper](#building-your-own-helper)
- [Extension sub-packages](#extension-sub-packages)
- [Roles and ownership](#roles-and-ownership)
- [Functions that are not trigger functions](#functions-that-are-not-trigger-functions)
- [Range, shell and base types](#range-shell-and-base-types)
- [The ALTER forms a migration needs](#the-alter-forms-a-migration-needs)
- [The forms inside a statement](#the-forms-inside-a-statement)
- [Named string types: which are closed](#named-string-types-which-are-closed)
- [Reference](#reference)

## A bare parameter has no type in some positions

Found by executing, not by reading. Postgres infers a bound parameter's type from where it appears; in a position with no context — a row of `pg.Values`, the seed of `WithRecursive`, an argument to a function with several overloads — it is read as `text`, and the statement fails with `operator does not exist: bigint = text` or `function generate_series(unknown, unknown, unknown) is not unique`. SQLite has no such rule, which is why nothing in this repository noticed until `contrib/driver-pgx` ran the constructs.

Every construct in this package that puts a parameter where the grammar gives it no type casts it: `pg.Array` renders `ARRAY[CAST($1 AS bigint), …]` from the Go type of its elements, `pg.Key` and `pg.JSONObject` cast their keys to `text` because `jsonb_build_object` and `JSON_OBJECT` are variadic `any`, `pg.GetIndex` casts to `int` because `->` with a text parameter is the key form and would silently look up the key `"0"`, and `pg.GenerateSeries` and `pg.GenerateSeriesScalar` cast to `bigint` because the function is overloaded. Each was found by running the construct against a real server after its rendering test had passed. For a `VALUES` list or a recursive seed, where the parameter is the caller's, cast the first row and the rest follow it:

```go
pg.Values("v", []string{"id", "label"},
	[]render.SQLer{sqlb.Cast[int64](sqlb.Param(int64(1)), "bigint"), sqlb.Cast[string](sqlb.Param("one"), "text")},
	[]render.SQLer{sqlb.Param(int64(2)), sqlb.Param("two")})
```

## Clause types

Every clause constructor in this package returns one of four aliases for the core's typed clause, so a statement of another engine or of the wrong statement kind refuses it at compile time: `pg.SelectClause`, `pg.InsertClause`, `pg.UpdateClause` and `pg.DeleteClause`, each an alias for `sqlb.Clause[pg.Dialect, sqlb.<Kind>Kind]`. `ClauseIn[K]` builds one for an extension package, with the statement kind named by the caller.

## Setup

Every snippet on this page uses these declarations.

```go
import (
	"encoding/json"
	"time"

	"github.com/ubgo/sqlb/expr"
)

// A library declares its table and its typed columns, and exports them.
var (
	Posts     = expr.Table{Name: "posts", Alias: "p"}
	PostID    = expr.Col[string](Posts, "id")
	PostTitle = expr.Col[string](Posts, "title")
	PostTags  = expr.Col[[]string](Posts, "tags")
	PostMeta  = expr.Col[json.RawMessage](Posts, "meta")
	PostViews = expr.Col[int](Posts, "views")
	PostMadeAt = expr.Col[time.Time](Posts, "made_at")
)
```

## Starting a statement

A Postgres user imports this one package. Statements start here, and every Postgres construct that goes inside them is here too, so there is no second package to learn. `Select` takes the select items directly, in the order the SQL reads them; `Insert`, `Update` and `Delete` take the table. Everything after the first call is `stmt`.

```go
pg.Select(PostID, PostTitle).From(Posts).Where(expr.Eq(PostID, "p1")).Build()
pg.Insert(Posts).Row(stmt.Set(PostTitle, "Ada")).Build()
pg.Update(Posts).Set(stmt.Set(PostTitle, "x")).Where(expr.Eq(PostID, "p1")).Build()
pg.Delete(Posts).Where(expr.Eq(PostID, "p1")).Build()
```

`pg.Select()` with no items is `sqlb.NewSelect(pg.Dialect{})`, for a statement whose items are bindings added with `Columns`, which is what a query you intend to scan wants. Each constructor is exactly that long form and nothing more, which `TestEntry_IsTheLongFormExactly` pins byte for byte. What it adds is the dialect in the type: a statement started here is a Postgres statement to the compiler, so a SQLite clause on it does not compile.

A type named in user code is the alias, never the spelled-out dialect:

```go
func Lookup(id string) *pg.SelectQuery {
	return pg.Select(PostID).From(Posts).Where(expr.Eq(PostID, id))
}

var f *pg.FilteredUpdate
```

`SelectQuery`, `InsertQuery`, `InsertReady`, `UpdateQuery`, `FilteredUpdate`, `DeleteQuery` and `FilteredDelete` are aliases of the `stmt` types, so a value passes through either name with no conversion, pinned by `TestEntry_AliasesAreTheSameTypes`. The typestate on writes is unchanged: `InsertQuery`, `UpdateQuery` and `DeleteQuery` have no `Build`; `InsertReady`, `FilteredUpdate` and `FilteredDelete` do. The table's DDL starts here as well. `pg.CreateTable`, `pg.AlterTable` and `pg.DropTable` are `ddl.NewCreateTable`, `ddl.NewAlterTable` and `ddl.NewDropTable` with the dialect written in, pinned the same way by `TestEntry_DDLIsTheLongFormExactly`, and they take the columns the queries above already use:

```go
pg.CreateTable(Posts).IfNotExists().Column(ddl.Column(PostTitle)).Build()
```

The builders, the column types, the Postgres rules they follow and what they refuse are on the [`ddl` page](../ddl/README.md).

`InsertReturning`, `UpdateReturning` and `DeleteReturning` are here because `RETURNING` is a capability of the dialect, checked by a type constraint, and this is the package where the engine is already known.

These lived in a separate package, `pgq`, until 2026-09-21. They moved here because two packages that both read as "Postgres" left a reader guessing which one held a name.

## Arrays

A Postgres array column is `Expr[[]T]`, so the element type travels with it and the compiler checks what you put in:

```go
pg.ArrayContains(PostTags, []string{"go", "sql"})  // "p"."tags" @> $1
pg.Cardinality(PostTags)                           // cardinality("p"."tags")
pg.ArrayPosition(PostTags, "go")                   // array_position("p"."tags", $1)
```

The whole Go slice is bound as one parameter rather than expanded, which is also why `pg.ArrayContains` replaces a long `IN` list without the placeholder count changing per call.

`ArrayPosition` returns `Expr[*int32]`, not `Expr[int32]`, because Postgres returns `NULL` when the element is absent. That is the type telling you about a case you would otherwise meet at the scan.

`Unnest` is the one to know, because it returns two things — a `Source` for the `FROM` clause and a typed expression for the element — so the element type is checked in the same call that introduces it:

```go
src, tag := pg.Unnest(PostTags, "t")

q, err := stmt.NewSelect(pg.Dialect{}).
	Exprs(tag).
	From(src).
	Build()
```

```sql
SELECT "t"."elem" FROM unnest("p"."tags") AS "t" ("elem")
```

The returned `Source` is what goes in `From`, and `tag` is `Expr[string]` because `PostTags` was `Expr[[]string]` — the element type is derived, not declared again.

`UnnestOrdinal` returns a third value, the 1-based position, which is how you preserve array order through a join. `ArrayAgg` goes the other way, collecting a column back into `Expr[[]T]`.

## JSON and JSONB

A JSONB column is `Expr[json.RawMessage]`, so it scans straight into the standard library's type with no wrapper:

```go
pg.HasKey(PostMeta, "author")                      // "p"."meta" ? $1
pg.PathText(PostMeta, []string{"author", "name"})  // ("p"."meta" #>> $1)
pg.JSONBSet(PostMeta, []string{"seen"}, json.RawMessage(`true`), true)
// jsonb_set("p"."meta", $1, $2, $3)
```

The final `true` on `JSONBSet` is `create_missing`, Postgres's own fourth argument: with it the path is created when absent, without it a missing path is left alone.

A path is a `[]string` rather than a dotted string, so a key containing a dot is not ambiguous and nothing has to be escaped.

`pg.JSONBAgg` and `pg.JSONObjectAgg` build a document database-side, which is what [`scan.JSON`](../scan/README.md#aggregating-in-the-database-instead) reads: one row back instead of a join's rectangle.

The `sqljson.go` family is the SQL:2016 standard spelling that Postgres 17 added — `JSONQuery`, `JSONValue`, `JSONExists`, `JSONTable` — kept separate from the older operator forms because they are different constructs with different NULL behaviour, not synonyms.

## Ranges

Postgres range types are a genuine distinguishing feature, and the bounds are where the subtlety lives:

```go
pg.Int4Range(1, 10, pg.BoundsIncExc)  // int4range($1, $2, '[)')
pg.RangeContains(r, 5)                // (r @> $3)
pg.RangeOverlaps(a, b)                // (a && b)
```

`Bounds` is a named type with four values — `BoundsIncInc`, `BoundsIncExc`, `BoundsExcInc`, `BoundsExcExc` — rather than a string, because `'[)'` and `'[]'` differ by one row at the boundary and a typo between them is not an error.

`Range[T]` and `Multirange[T]` are typed, so a `Range[int32]` and a `Range[time.Time]` do not mix.

## Full-text search

The two types are distinct, which is the point:

```go
v := pg.ToTSVector("english", PostTitle)
q := pg.ToTSQuery("english", "ada & lovelace")

pg.TSRank(v, q)  // ts_rank(to_tsvector('english', "p"."title"), to_tsquery('english', $1))
```

`pg.TSHeadline` returns the matched text with markers around it, for a results page.

`TSVector` and `TSQuery` are separate Go types, so passing a query where a vector belongs does not compile. That is a real mistake to make — both are text at the database — and it is exactly the class this library exists to catch.

`PlainToTSQuery`, `PhraseToTSQuery` and `WebsearchToTSQuery` are the three parsers for user-typed input; `WebsearchToTSQuery` is the one that will not error on arbitrary text, which makes it the right default for a search box.

## Window functions

Built on the core's `WinFn` and `Window` mechanisms, so `OVER` and framing are core and the function list is engine:

```go
pg.Lag(PostViews, 1)               // lag("p"."views", $1) OVER ()   WinFn[*int]: the first row has none
pg.LagDefault(PostViews, 1, zero)  // lag("p"."views", $1, $2) OVER ()  WinFn[int]: it cannot be null
pg.Lead(PostViews, 1)              // lead("p"."views", $1) OVER ()
pg.CumeDist()                      // cume_dist() OVER ()
pg.Ntile(4)                        // ntile($1) OVER ()
```

Each returns a `WinFn[T]`, not an expression: `.Over(window)` or `.OverNamed(name)` is what turns it into one, which is how the same function is reused across several windows.

`RowNumber`, `Rank` and `DenseRank` are **not** here: all four engines spell them identically, so they are core, as [`expr.RowNumber`](../expr/README.md). That split is the universality rule, and it is the reason this package is worth reading alongside `expr` rather than instead of it.

`Lag` and `LagDefault` differ in return type, not just arguments: without a default the first row is `NULL`, so it is `Expr[*int]`; with one it never is. The compiler makes you handle the difference.

## Locking

Row locking is a statement clause with options:

```go
pg.ForUpdate()                 // FOR UPDATE
pg.ForUpdate(pg.SkipLocked())  // FOR UPDATE SKIP LOCKED
pg.ForUpdate(pg.NoWait())      // FOR UPDATE NOWAIT
pg.ForShare()
```

`SKIP LOCKED` is how a work queue is drained by several workers without them blocking each other, and it is one of the strongest reasons to reach for Postgres.

`pg.LockTable` is a statement of its own rather than a clause, because `LOCK TABLE` is its own SQL statement. `TableLockMode` is a named type with all eight modes, so `LockShareRowExclusive` cannot be misspelled into something that parses.

Advisory locks (`AdvisoryXactLock`, `TryAdvisoryXactLock`) are the application-level ones, held to the end of the transaction.

## Writes beyond INSERT

```go
pg.OnConflict(PostID).DoUpdate(stmt.Set(PostTitle, "x")).Clause()  // upsert
pg.UpdateFrom(other)                                               // UPDATE ... FROM
pg.DeleteUsing(other)                                              // DELETE ... USING
pg.NewMerge(pg.Dialect{}, ...)                                     // MERGE (Postgres 15+)
pg.NewTruncate(pg.Dialect{}, Posts)                                // TRUNCATE
```

`Truncate` and `LockTable` are statement kinds, not clauses, and both satisfy `sqlb.Statement` so they run through the same `exec` path as everything else.

## Dates and times

```go
pg.DateTrunc(pg.FieldMonth, PostMadeAt)  // date_trunc('month', "p"."made_at")
pg.AtTimeZone(PostMadeAt, "UTC")         // "p"."made_at" AT TIME ZONE $1
pg.Extract(pg.FieldYear, PostMadeAt)     // extract(year FROM "p"."made_at")
```

The field goes into the SQL text as a keyword while the time zone is bound, because Postgres accepts a parameter in one position and not the other. That asymmetry is the reason `DateField` is a closed type and the zone is an ordinary string.

`DateField` is a named type with all seventeen fields, for the same reason `Bounds` is: `'moth'` is not a syntax error in most of these, it is a runtime one.

## Everything fails closed

Every construct here records `Args.Unsupported`, so building it under another dialect fails rather than rendering something that engine would accept differently:

```go
_, err := stmt.NewSelect(sqlite.Dialect{}).
	Exprs(PostID).
	From(Posts).
	Where(pg.ArrayContains(PostTags, []string{"go"})).
	Build()
// err: sqlb: construct is not supported by this dialect
//      pg.array under dialect "sqlite"
```

The name in the second line is the guarded primitive that refused, not always the function you called — `ArrayContains` is built on `pg.array`, so that is what reports.

That is the whole reason the engine split exists, and it is tested rather than asserted.

## Building your own helper

`pg` exports its guarded constructors, so a Postgres function this package lacks does not need a fork:

```go
func Soundex(e sqlb.Expr[string]) sqlb.Expr[string] {
	return pg.Func[string]("soundex", e)
}
```

`Expr`, `Pred`, `BinOp`, `BinPred`, `Func` and `ClauseIn` all carry the guard, so your helper fails closed under SQLite exactly as `pg.ArrayContains` does.

## Extension sub-packages

Eight extensions live under `pg/`, each built on those same exported constructors so that adding one never edits `pg`:

| Package | Extension |
|---|---|
| `pg/vector` | pgvector |
| `pg/trgm` | pg_trgm |
| `pg/fuzzy` | fuzzystrmatch |
| `pg/ltree` | ltree |
| `pg/crypto` | pgcrypto |
| `pg/earth` | earthdistance |
| `pg/unaccent` | unaccent |
| `pg/postgis` | PostGIS |

[extensions.md](../extensions.md) is how to add a ninth.

## Roles and ownership

A role is a value, declared by whoever knows the deployment's names, and both spellings satisfy `RoleRef`:

```go
app := pg.Role{Name: "app"}

pg.SetOwner(Reports, app)                       // ALTER VIEW "reports" OWNER TO "app"
pg.AlterPolicy(Isolation).To(app)               // ALTER POLICY ... TO "app"
pg.CreatePolicy(Isolation).To(app, pg.RolePublic)
```

`SetOwner` takes any `Ownable` — a view, a materialized view, a sequence, an enum, a composite, a domain, a range type or a base type — and the object kind in the statement comes from the Go type, so it cannot say `VIEW` of a sequence. This package creates no role and grants no privilege: `CREATE ROLE` and `GRANT` stay the operator's, because they change who may do what to objects this module does not own. What a library may do is hand its own objects to a role its caller named.

## Functions that are not trigger functions

The signature is typed and the body is not:

```go
Add := pg.Function{Name: "add", Params: []pg.FunctionArg{
	pg.Arg("a", ddl.RawType[int64]("bigint")),
	pg.Arg("b", ddl.RawType[int64]("bigint")),
}}

pg.CreateFunction(Add, pg.LangSQL, "SELECT a + b").
	OrReplace().
	Returns(pg.Returns(ddl.RawType[int64]("bigint"))).
	Volatility(pg.Immutable).
	NullInput(pg.Strict).
	Security(pg.Definer).
	Set("search_path", "auth, pg_catalog")
```

A parameter is a name and a `ddl.SQLType[T]`, the same pair a column and a composite attribute already are, so the signature costs no new vocabulary and the compiler checks it. The body is a string the library author writes: it is a program in another language, nothing here parses it, and it is dollar-quoted with a tag it does not contain. A body that does not compile is reported by the server.

`Params` lives on the `Function` value rather than on the create statement because a function's argument types are part of its name — `DropFunction`, `CommentOn` and `AddExtensionMember` each identify one by its signature, and declaring the parameters once is what keeps them from having to be told again. A trigger function leaves `Params` empty, which is the one signature Postgres fixes for you; `CreateTriggerFunction` refuses a `Function` that declares any, rather than dropping them in silence.

`Returns`, `ReturnsSetOf` and `ReturnsTable` are the three result shapes; a function whose results are output parameters needs none, and one with neither is refused with `ErrNoResult` rather than rendering a statement that means nothing. `CreateCFunction` is the two-string form for a symbol in a shared object. The SQL-standard body, `BEGIN ATOMIC ... END`, is not built: it is a list of statements rather than a string, and the same function written with a body string under `LangSQL` is what this builds instead.

## Range, shell and base types

A range type is one statement over an existing type, and it is typed as the SQL type of the column that holds one:

```go
Span := pg.RangeType[int64]{Name: "int8range_local"}

pg.Guarded(pg.CreateRangeType(Span, ddl.RawType[int64]("bigint")).
	SubtypeOpClass(pg.NewOpClass[int64]("int8_ops")).
	MultirangeTypeName("int8multirange_local"))

ddl.Column(sqlb.Col[pg.Range[int64]](Bookings, "span")).As(Span)
```

Declaring the type and declaring the column that holds it are the same act, and a column of any other Go type does not compile.

A base type is the other end of the scale — it is how a type is added to Postgres rather than composed out of its types. Its input function must return the type itself, so the name has to exist first, which is what `CreateShellType` is for:

```go
pg.CreateShellType(MyInt)
pg.CreateFunction(In, pg.LangInternal, "int4in").Returns(pg.Returns(MyInt))
pg.CreateFunction(Out, pg.LangInternal, "int4out").Returns(pg.Returns(ddl.RawType[string]("cstring")))
pg.CreateBaseType(MyInt, In, Out).InternalLength(4).PassedByValue().Alignment(pg.AlignInt4)
```

Getting the length, alignment or storage wrong corrupts every value stored under the type rather than failing, so a base type is built here because the statement is a statement like any other, not because a library should reach for one.

## The ALTER forms a migration needs

The forms a library writes for a schema it already has:

```go
pg.AlterIndex(EmailIx).SetStorage(pg.IntParameter("fillfactor", 70))
pg.AlterIndex(ParentIx).AttachPartition(PartIx)     // a partitioned index, one partition at a time
pg.AlterMaterializedView(Counts).RenameColumn("n", "user_count")
pg.RenameViewColumn(Active, "email", "address")
pg.AlterDomain(Positive).SetDefault(1)
pg.AddExtensionMember(Ext, Touch)                   // what an extension ships
pg.SetExtensionSchema(Ext, pg.Schema{Name: "ext"})

pg.AlterTable(Users).Clause(
	pg.AlterIdentitySequence(UserID).IncrementBy(10).StartWith(100),
	pg.Inherit(People), pg.NoInherit(People),        // each a statement of its own
	pg.OfType(UserRow), pg.NotOfType(),
	pg.DisableRule(NoDelete),
)
```

A storage parameter is the one open set: the names come from the access method and from extensions, so the name is written as an identifier and the value as a literal by the dialect, never by concatenation.

Inheritance, typed tables and rules are each a mechanism a new schema should not reach for — partitioning replaces the first, a declared column list the second, a trigger the third. That is a reason not to start one and no reason for the statement to be unbuildable: the library that has to write the migration is the library that inherited the schema, and until these landed the raw escape hatch was the only answer. This package still creates no rule.

The detector in `pg/ddl_coverage_test.go` classifies every command `psql` lists and every `ALTER TABLE` form it reports; since 2026-09-24 it has no `partial` and no `left-out` entry in either list, and what it still does not build is what it marks `operator` — roles, grants, replication, tablespaces and server tuning, which a library's schema does not own.

`AlterIdentitySequence` is the sequence behind an identity column, which has no declaration to name because Postgres creates it with the column and drops it with the column; `AlterSequence`, which takes a declared `pg.Sequence`, has nothing to be handed. Each option carries its own `SET`, which is the form Postgres takes and is why several of them share one `ALTER COLUMN`.

## The forms inside a statement

A statement having a constructor is not the same as every form of it being buildable, and the second pass on 2026-09-24 read every synopsis `psql` prints against what this package exports. What it found was a statement's own options: a domain that could be created but not given a default or a `NOT NULL`, a view whose column list could not be declared, a trigger that could not see the rows its statement changed, an index whose operator class could not be given parameters. Those are all built now, and three things from that pass are worth knowing before you write one.

**Order is grammar, and the statement supplies it.** Postgres fixes the order of a table's options — `INHERITS`, `PARTITION BY`, `USING`, `WITH`, `ON COMMIT` — and of a column's clauses — `STORAGE`, `COMPRESSION`, `COLLATE` — and answers every other order with a syntax error, measured on 17. You do not have to know it. Since 2026-09-24 the statement writes these clauses in that order whatever order you pass them in; this call passes every one of them backwards:

```go
pg.CreateTable(Items).Column(ddl.Column(ID), ddl.Column(Body)).Clause(
	pg.OnCommit(pg.DeleteRows),
	pg.TableStorage(pg.IntParameter("fillfactor", 70)),
	pg.TableAccessMethod(pg.Heap),
	pg.Inherits(People),
	pg.ColumnCollate(Body, "C"),
	pg.ColumnCompression(Body, "pglz"),
	pg.ColumnStorage(Body, pg.StorageExternal),
	pg.Temporary(), // ON COMMIT is refused on anything but a temporary table
)
```

```sql
CREATE TEMPORARY TABLE "items" (..., "body" TEXT STORAGE external COMPRESSION "pglz" COLLATE "C" NOT NULL) INHERITS (...) USING "heap" WITH ("fillfactor" = 70) ON COMMIT DELETE ROWS
```

Until that date the statement wrote them in the order given, so `WITH (...)` before `USING heap` built cleanly and failed on the server. Each clause now carries a rank from one table in `pg`, and `ddl` sorts by it. A clause you make yourself with `ddl.NewTableClause` has no rank and stays exactly where you put it, so a raw `TABLESPACE` belongs last in your list. The reasoning, including why the statement sorts rather than refusing a wrong order, is in [Clause order is the engine's](../ddl/README.md#clause-order-is-the-engines-not-the-callers); `ExampleOptionRank` in `examples/packages` is the compiled version, and `contrib/driver-pgx` runs the reversed statement on a real server.

**A trigger can see what the statement changed.** Transition tables are the difference between one call per statement and one per row:

```go
pg.CreateTrigger(AfterInsert).After(pg.OnInsert()).
	Referencing("", "new_rows").
	ExecuteWith(CountChanges, "insert")          // the arguments a function reads out of TG_ARGV
```

Beside them are the forms that had no spelling at all: `Constraint()` for a trigger deferred to commit, `InsteadOf` for a view Postgres cannot update by itself, and `OnTruncate`, the one event a row trigger cannot have.

**What the server refuses is measured, not read.** `ALTER EXTENSION ... ADD` accepted an `Index` and a `Statistics` here for a few hours, and both render perfectly; Postgres refuses both with "cannot add an object of this type to an extension". Neither is an `ExtensionMember` now, and `DependsOnExtension` is the form that does work for them. Two more measured rules live in their doc comments: a column's `STORAGE` is dropped in silence when the column is inherited, so set it on the parent, and a collation's recorded `Version` may only be given where the provider reports one, which ICU always does and the libc `C` locale never does.

`CreateTableAs` also arrived in that pass. It had been classified as the operator's on the reading that a table with no declaration is not a library's — but SQLite and MySQL both build it, which made that a difference between engines rather than a decision, and a summary table rebuilt from a query is exactly a library's.

## Named string types: which are closed

A named string type with constants beside it looks like a closed set and is not one. Go lets any caller write `pg.TrimSide("anything")`, and where the value is concatenated into the statement rather than quoted, that string **is** the statement. Every named string type in this package was audited on 2026-09-24 and falls into one of three groups, each with a different answer, because they are three different problems.

**Closed sets validate.** `TableLockMode`, `TrimSide`, `NormalForm`, `JSONKind`, `SampleMethod`, `DateField`, `Weight` and `Bounds` are keywords the grammar allows, and a keyword in quotes is not a keyword — validation is the only defence there is. Each has a `Values` slice and each render site writes the value through a checker that fails the build with `ErrUnknownValue`:

```go
pg.NewLockTable(pg.Dialect{}, Items).In(pg.TableLockMode("ACCESS SHARE MODE; DROP TABLE items; --"))
// Build: sqlb/pg: value is not one of this type's constants: lock mode "..."
```

The sites were found by grepping for each type rather than from memory, which mattered: `DateField` had four, two of them quoting and two not, and `Bounds` had three across two files. `TestClosedSets_EverySiteRefusesAHostileValueAndTakesARealOne` runs both halves at all eighteen — a hostile value refused, and a real constant still building — because a validation that refuses everything passes the first half and is useless. It was verified by making every `valid()` return true and watching all eighteen fail.

**Open sets quote instead.** `AccessMethod`, `IndexMethod` and `Language` are identifiers an extension adds, so a list here would break the extension that brings a real one. They need no validation and must not have any: the dialect writes them through `QuoteIdent`, so a hostile value is one quoted identifier that does not exist and the server says so.

**Two are neither, and check a shape.** `ExcludeOp` is an operator token, which cannot be quoted at all, so it is checked against the character set Postgres builds an operator name from. `TypeCategory` is a single character. `CompareOp`, which `AnyExpr` and `AllExpr` now take instead of a bare `string`, allows an operator token or one of the four word operators Postgres accepts before `ANY` — `LIKE`, `NOT LIKE`, `ILIKE` and `NOT ILIKE`, measured, because `SIMILAR TO ANY` and `IS DISTINCT FROM ANY` are syntax errors there. The core's untyped constants, `sqlb.OpEq` and its siblings, are still what you pass.

`TSVector`, `TSQuery`, `BitString` and `Interval` are none of the three: they are value types that are never concatenated into a statement. The literal constructors around them — `IntervalLit`, `TimestampLit`, `TimestamptzLit`, `DateLit`, `ArrayLit`, `Regclass` — now write their text through the dialect's literal quoting rather than by doubling the quote or not escaping at all. That is not cosmetic: with `standard_conforming_strings` off, Postgres reads a backslash in a plain literal as an escape, so doubling the quote leaves a lone backslash unterminated. `BitLit` is the exception and checks a shape, because `B'1011'` is its own syntax that quoting would turn into an ordinary string.

**What is left unchecked, deliberately.** `pg.BinOp`, `pg.BinPred` and `pg.Func` write their operator and function name into the statement as is — an operator cannot be quoted and a function name may be schema-qualified. They are the extension author's escape hatch and say so. So do `EmptyArray`, `ArrayLit` and `RangeExpr` for their type and function names, and `Returning`, `DefaultOnEmpty` and `DefaultOnError` for the literal the `JSON_VALUE` grammar requires. Each is the author's text, never a value from a request. The eight extension sub-packages were scanned and hold none of this shape: every caller string in them reaches the server as a bound parameter or through `pg.QuoteLit`.

## Reference

<!-- BEGIN GENERATED REFERENCE -->

Generated from the source by `task docs:reference`. Every exported name in `pg` appears here.

### Aggregates

| Name | What it is |
|---|---|
| `pg.Filter` | Filter adds FILTER (WHERE p) after an aggregate |
| `pg.OrderWithin` | OrderWithin adds ORDER BY inside the aggregate: array_agg(x ORDER BY y) |
| `pg.ArrayAgg` | ArrayAgg collects values into a slice |
| `pg.StringAgg` | StringAgg joins strings with a bound separator |
| `pg.BoolAnd` | bool_and(e): true when every row in the group is true |
| `pg.BoolOr` | bool_or(e): true when any row is |
| `pg.Stddev` | stddev(e), the sample standard deviation |
| `pg.Variance` | variance(e), the sample variance |
| `pg.WithinGroup` | an ordered-set aggregate: name(args) WITHIN GROUP (ORDER BY ...) |
| `pg.PercentileCont` | percentile_cont(f) WITHIN GROUP (ORDER BY e) |
| `pg.PercentileDisc` | PercentileDisc keeps the ordered expression's type |
| `pg.Mode` | mode() WITHIN GROUP (ORDER BY e) |
| `pg.GroupingSets` | GROUPING SETS ((a), (b)), one result group per set |
| `pg.Cube` | CUBE (a, b), every combination of the items, which is 2^n groups |
| `pg.Rollup` | ROLLUP (a, b), the hierarchical subtotals: (a, b), (a) and the grand total, which is n+1 groups rather than 2^n |
| `pg.Grouping` | grouping(e): 1 on a super-aggregate row, else 0 |

### alter

| Name | What it is |
|---|---|
| `pg.AlterClause` | a Postgres ALTER TABLE action |
| `pg.AddColumnIfNotExists` | AddColumnIfNotExists adds a column unless it exists, which makes the action safe to run on every boot |
| `pg.DropColumnClause` | DROP COLUMN IF EXISTS, optionally with CASCADE |
| `pg.DropColumnIfExists` | DropColumnIfExists drops a column if it exists, so an uninstall or a second run of a migration is not an error |
| `pg.SetDefault` | SetDefault sets a column's default to a value of its own Go type, written as a literal |
| `pg.SetDefaultExpr` | SetDefaultExpr sets a column's default to an expression of its own type |
| `pg.DropDefault` | DropDefault removes a column's default |
| `pg.SetNotNull` | SetNotNull refuses NULL from now on |
| `pg.DropNotNull` | DropNotNull allows NULL |
| `pg.AlterTypeClause` | ALTER COLUMN .. |
| `pg.AlterType` | AlterType changes a column's type |
| `pg.AddIdentity` | AddIdentity makes an existing integer column an identity column |
| `pg.Constraint` | a Postgres table constraint that ALTER TABLE can add: a primary key, a unique constraint, a foreign key or a check |
| `pg.TableClause` | — |
| `pg.Validatable` | a constraint Postgres can add NOT VALID: a foreign key or a check, and nothing else, so AddConstraintNotValid on a unique key does not compile rather than failing on the server |
| `pg.AddConstraint` | AddConstraint adds a constraint to an existing table, checking every row under a lock that blocks writes for the whole scan |
| `pg.AddConstraintNotValid` | AddConstraintNotValid adds a foreign key or check without checking existing rows, so the strong lock is brief; ValidateConstraint then checks them under a lock that allows writes |
| `pg.ValidateConstraint` | ValidateConstraint checks existing rows against a constraint added NOT VALID |
| `pg.ErrRenameAcrossTables` | returned by RenameConstraint for two constraints of different tables: a constraint is renamed within its table and cannot move to another |
| `pg.RenameConstraint` | RenameConstraint renames a constraint within its table, the one migration a declared constraint needs when its name changes |
| `pg.DropConstraintClause` | DROP CONSTRAINT, optionally IF EXISTS and CASCADE |
| `pg.DropConstraint` | DropConstraint drops a declared constraint |
| `pg.RenameTo` | RenameTo renames the table to to's name; the schema is unchanged, which is SetSchema's job |
| `pg.SetSchema` | SetSchema moves the table to another schema |
| `pg.EnableRowLevelSecurity` | EnableRowLevelSecurity turns on row security, after which a role that is not the owner sees no row until a policy grants it |

### Later ALTER forms

| Name | What it is |
|---|---|
| `pg.SetExpression` | SetExpression changes a generated column's expression to e, an expression of the column's own Go type, so a column of another type does not compile |
| `pg.DropColumnPartClause` | an ALTER COLUMN action that removes something a column carries and takes IF EXISTS: DROP EXPRESSION and DROP IDENTITY |
| `pg.DropExpression` | DropExpression turns a generated column into a plain one, keeping the values it holds; from then on the column is written like any other |
| `pg.DropIdentity` | DropIdentity stops a column generating values, keeping those it holds and dropping the sequence behind it |
| `pg.SetIdentityKind` | SetIdentityKind changes whether an identity column refuses an explicit value, between Always and ByDefault |
| `pg.RestartIdentityClause` | the ALTER COLUMN action that sets an identity column's sequence back to its start value, or to With's |
| `pg.RestartIdentity` | RestartIdentity restarts an identity column's sequence from the value the column was declared with |
| `pg.AddUniqueUsingIndex` | AddUniqueUsingIndex promotes an existing unique index to a named unique constraint |
| `pg.AddPrimaryKeyUsingIndex` | AddPrimaryKeyUsingIndex promotes an existing unique index to the table's primary key, the same zero-downtime step as AddUniqueUsingIndex |
| `pg.ErrIndexSchema` | returned when the Index handed to AddUniqueUsingIndex or AddPrimaryKeyUsingIndex declares a schema its table does not, which is a declaration the statement cannot honour: the index name is written bare, so a schema set here would otherwise be dropped in silence |
| `pg.Movable` | a declared object SET SCHEMA can move: a view, a materialized view, a sequence, an enum, a composite type or a domain |
| `pg.SetSchemaStatement` | the SET SCHEMA statement of one declared object, whose ALTER keyword names the object kind |
| `pg.SetObjectSchema` | SetObjectSchema moves a declared object into another schema, the migration a library writes when it stops living in the search path and takes a schema of its own |
| `pg.ViewOption` | a storage option of a view, the closed set Postgres defines for one |
| `pg.ViewCheckOption` | — |
| `pg.ViewSecurityBarrier` | — |
| `pg.ViewSecurityInvoker` | — |
| `pg.ViewSetting` | one of a view's options with its value: the closed set of names Postgres defines for a view, each taking its own value type, so a boolean option cannot be given a check option |
| `pg.CheckOptionSetting` | CheckOptionSetting refuses a write through the view that the view would not show, the option CreateView also writes as WITH .. |
| `pg.SecurityBarrierSetting` | SecurityBarrierSetting stops Postgres pushing a caller's own WHERE condition below the view's, which would otherwise let a cheap function in that condition see rows the view hides |
| `pg.SecurityInvokerSetting` | SecurityInvokerSetting runs the view's own query with the caller's privileges rather than the view owner's, so the caller's row-level security policies apply to the tables under it |
| `pg.AlterViewOptionsStatement` | the ALTER VIEW statement that sets and resets a view's options |
| `pg.AlterViewOptions` | AlterViewOptions starts a change to a view's options |

### alterobjects

| Name | What it is |
|---|---|
| `pg.StorageParameter` | one storage parameter of an index, a table or a materialized view: a name and a typed value |
| `pg.IntParameter` | a whole-numbered parameter, fillfactor being the usual one |
| `pg.FloatParameter` | a fractional parameter, an autovacuum scale factor being the usual one |
| `pg.BoolParameter` | an on-or-off parameter |
| `pg.TextParameter` | a parameter whose value is a word or a phrase |
| `pg.AccessMethod` | a table access method, the storage engine a table or a materialized view's rows are kept in |
| `pg.Heap` | the access method a stock Postgres has and every table uses unless an extension provides another |
| `pg.AlterIndexQuery` | ALTER INDEX with no action yet, which has no Build |
| `pg.AlterIndex` | AlterIndex starts an ALTER INDEX on a declared index |
| `pg.AlterIndexStatement` | a complete ALTER INDEX |
| `pg.AlterMaterializedViewQuery` | ALTER MATERIALIZED VIEW with no action yet, which has no Build |
| `pg.AlterMaterializedView` | AlterMaterializedView starts an ALTER MATERIALIZED VIEW |
| `pg.AlterMaterializedViewStatement` | a complete ALTER MATERIALIZED VIEW |
| `pg.RenameViewColumnStatement` | ALTER VIEW .. |
| `pg.RenameViewColumn` | RenameViewColumn renames one of a view's output columns, the migration a library writes when a name it exposed changes without its body changing |
| `pg.ExtensionMember` | a declared object an extension can own |
| `pg.MemberTable` | MemberTable names a table as an extension's member, the one member kind that is not a declared object value of this package: a table is a sqlb.Table |
| `pg.ExtensionMemberStatement` | ALTER EXTENSION .. |
| `pg.AddExtensionMember` | AddExtensionMember makes an existing object part of an extension, so that dropping the extension drops the object and a dump writes the extension rather than the object |
| `pg.DropExtensionMember` | DropExtensionMember takes an object back out of an extension, leaving the object in place; it is what an upgrade runs for an object the new version no longer ships |
| `pg.SetExtensionSchemaStatement` | ALTER EXTENSION .. |
| `pg.SetExtensionSchema` | SetExtensionSchema moves an extension's objects into another schema, the statement a library runs when it stops leaving an extension's types in the search path |

### Arrays

| Name | What it is |
|---|---|
| `pg.Array` | ARRAY[$1::type, $2::type, ...] with every element bound and cast to the type T binds as |
| `pg.ArrayContains` | col @> $n; ArrayContainedBy col <@ $n; Overlaps col && $n |
| `pg.ArrayContainedBy` | col <@ $n: every element of col appears in v |
| `pg.Overlaps` | col && $n: the two arrays share at least one element |
| `pg.ArrayConcat` | l \|\| r |
| `pg.Any` | $n = ANY(col): the array holds v |
| `pg.AnyOp` | $n op ANY(col) for an operator other than equality, such as a comparison or a pattern match against every element |
| `pg.AllOp` | $n op ALL(col) |
| `pg.Subscript` | col[n] |
| `pg.Slice` | col[lo:hi] |
| `pg.Cardinality` | cardinality(col): the total element count across every dimension |
| `pg.ArrayLength` | array_length(col, dim), 1-based |
| `pg.ArrayAppend` | array_append(col, v), adding one element at the end |
| `pg.ArrayPrepend` | array_prepend(v, col) |
| `pg.ArrayRemove` | array_remove(col, v), dropping every element equal to v rather than the first |
| `pg.ArrayReplace` | array_replace(col, from, to), replacing every match |
| `pg.ArrayCat` | array_cat(l, r), the two arrays end to end |
| `pg.ArrayPosition` | array_position(col, v): the 1-based index of the first match |
| `pg.ArrayToString` | array_to_string(col, sep) |
| `pg.StringToArray` | string_to_array(e, sep) |
| `pg.Unnest` | unnest(col) AS alias (elem) and returns the typed element |
| `pg.UnnestOrdinal` | Unnest WITH ORDINALITY: (elem, n) |
| `pg.GenerateSeries` | generate_series(CAST($1 AS bigint), ...) AS alias (n) for integers |
| `pg.GenerateSubscripts` | generate_subscripts(col, dim) AS alias (i) |
| `pg.InArray` | col = ANY($1) with one bound array |
| `pg.ArrayDims` | The dimension and shape functions |
| `pg.ArrayNDims` | array_ndims(col), the number of dimensions |
| `pg.ArrayLower` | array_lower(col, dim), the first subscript of a dimension |
| `pg.ArrayUpper` | array_upper(col, dim), the last subscript of a dimension |
| `pg.ArrayReverse` | The reshaping functions, all Postgres 16 or later |
| `pg.ArraySort` | array_sort(col) |
| `pg.ArraySortDesc` | array_sort(col, descending), with the direction bound |
| `pg.ArrayShuffle` | array_shuffle(col) |
| `pg.ArraySample` | array_sample(col, n), n elements chosen at random |
| `pg.TrimArray` | trim_array(col, n), the array without its last n elements |

### closedsets

| Name | What it is |
|---|---|
| `pg.ErrUnknownValue` | The named string types, and which of them are closed |
| `pg.SampleMethodValues` | the two sampling methods every Postgres has |
| `pg.TrimSideValues` | every side trim accepts |
| `pg.NormalFormValues` | every Unicode normalization form |
| `pg.JSONKindValues` | every qualifier IS JSON accepts, the empty one included: JSONAny is a legitimate value and tests only that the text parses as JSON |
| `pg.DateFieldValues` | every field extract and date_trunc accept, the four in more.go included |
| `pg.WeightValues` | the four tsvector labels |
| `pg.BoundsValues` | the four inclusivity combinations |
| `pg.CompareOp` | the operator a comparison against ANY or ALL uses |

### entry

| Name | What it is |
|---|---|
| `pg.SelectQuery` | The statement constructors |
| `pg.InsertQuery` | the statement Insert returns |
| `pg.InsertReady` | a complete INSERT, the only form with Build |
| `pg.UpdateQuery` | the statement Update returns |
| `pg.FilteredUpdate` | an UPDATE that has said which rows, the only form with Build |
| `pg.DeleteQuery` | the statement Delete returns, with no Build until Where or All |
| `pg.FilteredDelete` | a DELETE that has said which rows, the only form with Build |
| `pg.Select` | Select starts a SELECT for Postgres |
| `pg.Insert` | Insert starts an INSERT into t for Postgres |
| `pg.Update` | Update starts an UPDATE of t for Postgres |
| `pg.Delete` | Delete starts a DELETE from t for Postgres |
| `pg.InsertReturning` | — |
| `pg.UpdateReturning` | — |
| `pg.DeleteReturning` | — |
| `pg.CreateTableQuery` | The DDL statement constructors, spellings of the ddl package's with the dialect written in, exactly as Select is of sqlb.NewSelect |
| `pg.CreateTableReady` | a CREATE TABLE with at least one column, the only form with Build |
| `pg.AlterTableQuery` | an ALTER TABLE with no action yet, which has no Build |
| `pg.AlterTableReady` | an ALTER TABLE with at least one action |
| `pg.DropTableQuery` | a DROP TABLE, complete from its constructor |
| `pg.CreateTable` | CreateTable starts a CREATE TABLE of t for Postgres |
| `pg.AlterTable` | AlterTable starts an ALTER TABLE of t for Postgres |
| `pg.DropTable` | DropTable drops one or more tables for Postgres, one statement per table |

### exclude

| Name | What it is |
|---|---|
| `pg.ExcludeOp` | the operator an exclusion constraint compares with: two rows conflict when every element's operator returns true |
| `pg.OpOverlaps` | — |
| `pg.OpEquals` | — |
| `pg.OpAdjacent` | — |
| `pg.ExcludeElement` | one column of an exclusion constraint and its operator |
| `pg.ExcludeOn` | ExcludeOn pairs a column with the operator two rows' values are compared by |
| `pg.ExcludeClause` | an EXCLUDE table constraint |
| `pg.Exclude` | Exclude makes a named exclusion constraint: no two rows may have every element's operator true at once |
| `pg.LikeOption` | what CREATE TABLE .. |
| `pg.IncludingDefaults` | — |
| `pg.IncludingConstraints` | — |
| `pg.IncludingGenerated` | — |
| `pg.IncludingIdentity` | — |
| `pg.IncludingIndexes` | — |
| `pg.IncludingComments` | — |
| `pg.IncludingStatistics` | — |
| `pg.IncludingStorage` | — |
| `pg.IncludingCompression` | — |
| `pg.CreateTableLikeStatement` | CREATE TABLE t (LIKE src ...) |
| `pg.CreateTableLike` | CreateTableLike creates t with src's columns, and whatever of src the options name |

### Extension plumbing

| Name | What it is |
|---|---|
| `pg.Guard` | The exported constructors an extension package builds on |
| `pg.Expr` | Expr makes a guarded typed expression from a render function |
| `pg.Pred` | Pred makes a guarded predicate from a render function |
| `pg.BinOp` | "left op right" as a guarded expression typed R |
| `pg.BinPred` | "left op right" as a guarded predicate |
| `pg.Func` | a guarded function call typed R |
| `pg.ClauseIn` | ClauseIn makes a guarded clause for a statement slot, typed to this engine and to the statement kind K, which the caller names: an extension package writing a SELECT clause instantiates ClauseIn[sqlb.SelectKind] |
| `pg.QuoteLit` | an author-chosen constant as a string literal, written by the dialect's own quoter: plainly when it holds no backslash, and in the E'...' form when it does, so it reads the same whatever the server's standard_conforming_strings |
| `pg.List` | SQLers comma-separated |

### Full-text search

| Name | What it is |
|---|---|
| `pg.TSVector` | a Postgres tsvector in text form: a document reduced to its lexemes, with their positions and weights |
| `pg.TSQuery` | a Postgres tsquery in text form: the search expression matched against a TSVector |
| `pg.Weight` | a tsvector label: A, B, C or D |
| `pg.WeightA` | — |
| `pg.WeightB` | — |
| `pg.WeightC` | — |
| `pg.WeightD` | — |
| `pg.ToTSVector` | to_tsvector(config, e) |
| `pg.WebsearchToTSQuery` | the safe form for user input: it never raises a syntax error |
| `pg.PlainToTSQuery` | plainto_tsquery(config, $n): every word ANDed, with operators in the input treated as text |
| `pg.PhraseToTSQuery` | phraseto_tsquery(config, $n): the words in order and adjacent, which is a phrase search |
| `pg.ToTSQuery` | ToTSQuery raises a syntax error on malformed input |
| `pg.Matches` | vector @@ query |
| `pg.TSRank` | TSRank and TSRankCD |
| `pg.TSRankCD` | ts_rank_cd(v, q), the cover-density ranking |
| `pg.TSHeadline` | ts_headline(config, text, query, options) |
| `pg.SetWeight` | setweight(v, 'A') |
| `pg.Strip` | strip(v), dropping positions and weights |
| `pg.TSVectorConcat` | a \|\| b for weighted vectors |
| `pg.TSAnd` | l && r, both terms required |
| `pg.TSOr` | l \|\| r, either term |
| `pg.TSNot` | !! q, excluding the term |
| `pg.TSFollowedBy` | l <-> r: l immediately followed by r, the phrase operator |

### function

| Name | What it is |
|---|---|
| `pg.ErrNoResult` | returned for a function that neither says what it returns nor carries an output parameter, which Postgres refuses: there is nothing to call the function for and nothing to type the call site with |
| `pg.ErrTriggerSignature` | returned by CreateTriggerFunction for a Function that declares parameters |
| `pg.Language` | the language a function's body is written in |
| `pg.LangPLpgSQL` | — |
| `pg.LangSQL` | — |
| `pg.LangC` | — |
| `pg.LangInternal` | — |
| `pg.Volatility` | what the planner may assume about a function's result: whether two calls with the same arguments must agree, and for how long |
| `pg.Immutable` | — |
| `pg.Stable` | — |
| `pg.Volatile` | — |
| `pg.NullInput` | what the server does with a call that has a NULL argument |
| `pg.Strict` | — |
| `pg.CalledOnNullInput` | — |
| `pg.Security` | whose privileges a function's body runs with |
| `pg.Invoker` | — |
| `pg.Definer` | — |
| `pg.Parallel` | whether a function may run in a parallel query plan |
| `pg.ParallelSafe` | — |
| `pg.ParallelRestricted` | — |
| `pg.ParallelUnsafe` | — |
| `pg.FunctionArg` | one parameter of a function: a mode, a name and a typed SQL type |
| `pg.Arg` | an input parameter of type t |
| `pg.OutArg` | an output parameter, one of the values the function returns |
| `pg.InOutArg` | a parameter that is both read and returned |
| `pg.VariadicArg` | the last parameter, an array type whose elements the caller passes one by one |
| `pg.FunctionResult` | what a function returns: one value of a type, a set of them, or a table of named columns |
| `pg.Returns` | Returns one value of type t |
| `pg.ReturnsSetOf` | a set of values of type t, which a query reads as rows |
| `pg.ReturnsTable` | a set of rows of the named columns, the form a query reads as a table without a column definition list at the call site |
| `pg.TypeRef` | a declared SQL type named in a position where its Go type says nothing about the statement, a base type's element or a transform's type |
| `pg.NamedType` | NamedType refers to t where only its name is written |
| `pg.CreateRoutineStatement` | CREATE FUNCTION for a function that is not a trigger function: a signature, what it returns, a language and a body |
| `pg.CreateFunction` | CreateFunction creates a function whose body is written in lang |
| `pg.CreateCFunction` | CreateCFunction creates a function implemented by linkSymbol in the shared object objFile, the two-string form of CREATE FUNCTION |

### index

| Name | What it is |
|---|---|
| `pg.Index` | a Postgres index, declared once as a value and named by every statement that creates, renames, drops or comments on it |
| `pg.IndexMethod` | an index access method |
| `pg.BTree` | — |
| `pg.Hash` | — |
| `pg.GiST` | — |
| `pg.SPGiST` | — |
| `pg.GIN` | — |
| `pg.BRIN` | — |
| `pg.OpClass` | an operator class, typed to the Go type of the column it indexes, so jsonb_path_ops on a text column does not compile |
| `pg.NewOpClass` | NewOpClass makes an operator class for columns of Go type T |
| `pg.NullableOps` | the same operator class for a nullable column of the same type, a *string where the class was made for string |
| `pg.JSONBPathOps` | — |
| `pg.JSONBOps` | — |
| `pg.IndexParam` | one storage parameter in WITH (...), an extension's tuning knob such as HNSW's m |
| `pg.NewIndexParam` | NewIndexParam makes a storage parameter with an integer value, which is what every index parameter a library tunes takes |
| `pg.IndexElement` | one key of an index: a column or an expression, with its sort order and operator class |
| `pg.IndexKeyElem` | a column or expression key |
| `pg.IndexKey` | IndexKey indexes a column |
| `pg.IndexExpr` | IndexExpr indexes an expression, lower(email) say; a query uses the index only when it filters on the same expression |
| `pg.CreateIndexQuery` | a CREATE INDEX with no key yet, which has no Build |
| `pg.CreateIndexReady` | a CREATE INDEX with at least one key |
| `pg.CreateIndex` | CreateIndex starts an index ix on table t |
| `pg.RenameIndexStatement` | ALTER INDEX .. |
| `pg.RenameIndex` | RenameIndex renames an index; its schema is unchanged |
| `pg.DropIndexStatement` | DROP INDEX |
| `pg.DropIndex` | DropIndex drops one or more indexes, one statement each, since DROP INDEX CONCURRENTLY takes one index |
| `pg.Commentable` | a declared object COMMENT ON can name |
| `pg.CommentQuery` | COMMENT ON an object with no text yet, which has no Build |
| `pg.CommentOn` | CommentOn starts a comment on a declared object: an Index, a Schema, an Extension or an Enum |
| `pg.CommentOnConstraint` | CommentOnConstraint starts a comment on a declared table constraint: COMMENT ON CONSTRAINT name ON table |
| `pg.CommentOnTable` | CommentOnTable starts a comment on a table |
| `pg.CommentOnColumn` | CommentOnColumn starts a comment on a column |
| `pg.CommentStatement` | a complete COMMENT ON |

### JSON and JSONB

| Name | What it is |
|---|---|
| `pg.JSON` | the typed form of a json or jsonb column: it scans into T and values T as JSON, so the Go struct is the schema |
| `pg.Get` | col->key, a JSON value at a bound key |
| `pg.GetIndex` | col->$1::int for an array element |
| `pg.JSONText` | col->>key, the text at a bound key |
| `pg.PathGet` | col#>path with a bound text[] path |
| `pg.PathText` | col#>>path |
| `pg.Contains` | col @> $n: true when col contains the JSON document v |
| `pg.ContainedBy` | col <@ $n |
| `pg.HasKey` | col ? key: the object has that top-level key |
| `pg.HasAnyKey` | col ?\| keys: at least one of the keys is present |
| `pg.HasAllKeys` | col ?& keys: every key is present |
| `pg.JSONConcat` | l \|\| r, a shallow merge |
| `pg.DeleteKey` | col - key, removing a top-level key |
| `pg.DeletePath` | col #- path, removing a nested key by path |
| `pg.PathExists` | col @? jsonpath: the path matches anything |
| `pg.PathMatch` | col @@ jsonpath, evaluating the path as a boolean expression |
| `pg.JSONBSet` | jsonb_set(col, path, value, create_missing) |
| `pg.JSONBInsert` | jsonb_insert(col, path, value) |
| `pg.BuildObject` | jsonb_build_object(k1, v1, ...) |
| `pg.Key` | a bound object key for BuildObject, cast to text because the function is variadic "any" and gives a bare parameter no type |
| `pg.BuildArray` | jsonb_build_array(...) |
| `pg.ToJSONB` | to_jsonb(e) |
| `pg.RowToJSON` | row_to_json(alias), the whole row as a JSON object |
| `pg.JSONBTypeof` | jsonb_typeof(e): "object", "array", "string", "number", "boolean" or "null" |
| `pg.JSONBArrayLength` | jsonb_array_length(e) |
| `pg.StripNulls` | jsonb_strip_nulls(e), dropping object keys whose value is null |
| `pg.Pretty` | jsonb_pretty(e), indented text |
| `pg.ArrayElements` | Set-returning JSON functions as FROM sources |
| `pg.ArrayElementsText` | jsonb_array_elements_text(col) AS alias (value) |
| `pg.Each` | jsonb_each(col) AS alias (key, value) |
| `pg.ObjectKeys` | jsonb_object_keys(col) AS alias (key) |
| `pg.PathQuery` | jsonb_path_query(col, path) AS alias (value) |
| `pg.PathQueryFirst` | jsonb_path_query_first(col, path) as a scalar |
| `pg.JSONBAgg` | jsonb_agg(e), the group as a JSON array |
| `pg.JSONAgg` | json_agg(e), the json rather than jsonb form |
| `pg.JSONBObjectAgg` | jsonb_object_agg(key, value), the group as a JSON object |

### JSON_TABLE

| Name | What it is |
|---|---|
| `pg.JSONTableColumn` | one column of a JSON_TABLE (PG17) |
| `pg.Name` | — |
| `pg.Type` | — |
| `pg.Path` | — |
| `pg.Ordinality` | — |
| `pg.Exists` | — |
| `pg.DefaultOnEmpty` | — |
| `pg.DefaultOnError` | — |
| `pg.Nested` | — |
| `pg.JSONTableNested` | a NESTED PATH with its own column list |
| `pg.Columns` | — |
| `pg.JSONTable` | JSON_TABLE(doc, 'path' COLUMNS (...)) AS alias, a FROM source |

### Advisory locks and row locking

| Name | What it is |
|---|---|
| `pg.LockOption` | LockOption modifies a row-locking clause |
| `pg.Of` | Of restricts the lock to the named tables |
| `pg.NoWait` | NoWait errors instead of waiting for a locked row |
| `pg.SkipLocked` | SkipLocked skips locked rows: the job-queue idiom, with LIMIT |
| `pg.ForUpdate` | FOR UPDATE, the strongest row lock: it blocks another FOR UPDATE, a FOR SHARE, and any update or delete of the row |
| `pg.ForNoKeyUpdate` | FOR NO KEY UPDATE, which still allows another transaction to reference the row with a foreign key |
| `pg.ForShare` | FOR SHARE: several readers may hold it, and it blocks writers |
| `pg.ForKeyShare` | FOR KEY SHARE, the weakest: it blocks only a change to the key |
| `pg.ForUpdateSkipLocked` | the queue idiom in one call |

### LOCK TABLE

| Name | What it is |
|---|---|
| `pg.TableLockMode` | one of the eight table-level lock modes Postgres defines |
| `pg.LockAccessShare` | — |
| `pg.LockRowShare` | — |
| `pg.LockRowExclusive` | — |
| `pg.LockShareUpdateExclusive` | — |
| `pg.LockShare` | — |
| `pg.LockShareRowExclusive` | — |
| `pg.LockExclusive` | — |
| `pg.LockAccessExclusive` | — |
| `pg.TableLockModeValues` | the canonical iteration order over the lock modes, weakest first, so a validator or a documentation generator reads the same list this package writes from |
| `pg.ErrNoLockTables` | returned by Build when no table was named |
| `pg.LockTable` | a LOCK TABLE statement |
| `pg.NewLockTable` | NewLockTable starts a LOCK TABLE for d |

### Maths

| Name | What it is |
|---|---|
| `pg.Abs` | abs(e) |
| `pg.Ceil` | ceil(e), rounding toward positive infinity |
| `pg.Floor` | floor(e), rounding toward negative infinity |
| `pg.Sign` | sign(e): -1, 0 or 1 |
| `pg.Round` | round(e, places) |
| `pg.Trunc` | trunc(e, places), rounding toward zero |
| `pg.Power` | power(base, exp) |
| `pg.Sqrt` | sqrt(e) |
| `pg.Exp` | exp(e), e raised to the operand |
| `pg.Ln` | ln(e), the natural logarithm |
| `pg.Log10` | log(e) |
| `pg.Random` | random(), a value in [0,1) |
| `pg.DivExact` | (l::numeric / r), so integer division does not truncate |
| `pg.WidthBucket` | width_bucket(e, lo, hi, count) |
| `pg.GCD` | gcd(l, r), the greatest common divisor |
| `pg.GenRandomUUID` | gen_random_uuid(), typed string |
| `pg.Nextval` | nextval('seq') |
| `pg.CurrentUser` | current_user, which takes no parentheses |
| `pg.Version` | version(), the server version banner |
| `pg.BackendPID` | pg_backend_pid(), the server-side process id of this connection |
| `pg.NumNonnulls` | num_nonnulls(items...), counting how many arguments are not NULL |

### MERGE

| Name | What it is |
|---|---|
| `pg.Merge` | a MERGE statement (Postgres 15; BY SOURCE and RETURNING need 17) |
| `pg.ErrIncompleteMerge` | returned by Build for a MERGE without its target, its source, its join condition or a WHEN branch |
| `pg.NewMerge` | NewMerge starts a MERGE for d |
| `pg.WhenBuilder` | WhenBuilder finishes one WHEN branch |
| `pg.MergeAction` | merge_action() for RETURNING: 'INSERT', 'UPDATE' or 'DELETE' |

### Miscellaneous

| Name | What it is |
|---|---|
| `pg.IntervalLit` | the literal interval 'text' |
| `pg.AddIntervalExpr` | (e + iv) for an interval expression, so a literal interval and a bound one use the same form |
| `pg.SubIntervalExpr` | e - iv with an interval expression |
| `pg.ArrayOf` | ARRAY[e1, e2, ...] from expressions, typed by the element |
| `pg.AnyExpr` | left op ANY(array) and AllExpr left op ALL(array), for an array expression such as a path column, where Any and AllOp take a value |
| `pg.AllExpr` | left op ALL(array): the comparison holds against every element |
| `pg.GenerateSeriesTime` | generate_series($from, $to, interval) AS alias (ts) over timestamps, with the step an author constant |
| `pg.GenerateSeriesExpr` | generate_series(from, to, step) from expressions |
| `pg.ErrIncompleteSpec` | returned for a caller-written specification with a required part empty: a RecordSet or JSON_TABLE column list with no columns, a column with no name or type, a NESTED PATH with no path, a JSON object pair with no value |
| `pg.ColumnDef` | one typed column of a record-set alias: AS x(sku text, ...) |
| `pg.RecordSet` | RecordSet makes a function call a source with a typed column list, which jsonb_to_recordset and crosstab need |
| `pg.PathQueryVars` | jsonb_path_query(col, path, vars) AS alias (value) |
| `pg.JSONBSetExpr` | JSONBSet with the new value as an expression, for a subquery that rebuilds an array |

### Further constructs

| Name | What it is |
|---|---|
| `pg.RowsFrom` | ROWS FROM (f1(), f2(), ...) [WITH ORDINALITY] AS alias (cols), which zips several set-returning functions and pads the shorter with NULL |
| `pg.UnnestMulti` | unnest(a1, a2, ...) [WITH ORDINALITY] AS alias (cols): several arrays zipped, the shorter padded with NULL |
| `pg.Variadic` | Variadic marks an array argument as VARIADIC in a function call |
| `pg.Named` | name => value, the named-argument notation |
| `pg.SubstringRegex` | substring(e FROM pattern): the first capture group, or the whole match with no group |
| `pg.TrimSide` | BOTH, LEADING or TRAILING |
| `pg.TrimBoth` | — |
| `pg.TrimLeading` | — |
| `pg.TrimTrailing` | — |
| `pg.TrimChars` | trim(side chars FROM e) |
| `pg.NormalForm` | a Unicode normalization form for Normalize |
| `pg.NFC` | — |
| `pg.NFD` | — |
| `pg.NFKC` | — |
| `pg.NFKD` | — |
| `pg.Normalize` | normalize(e, FORM) (PG13) |
| `pg.RegexpInstr` | regexp_instr(e, pattern): the 1-based position of the first match, or 0 when there is none |
| `pg.RegexpSubstr` | regexp_substr(e, pattern) |
| `pg.OctetLength` | octet_length(e), the byte count |
| `pg.QuoteIdent` | quote_ident(e), quoting a value for use as an identifier |
| `pg.QuoteLiteral` | quote_literal(e), quoting a value for use as a literal |
| `pg.StringToTable` | string_to_table(e, sep) AS alias (part) (PG14) |
| `pg.StringToArrayNull` | string_to_array(e, sep, nullStr), the three-argument form |
| `pg.SliceFrom` | col[lo:], a slice with an open upper end |
| `pg.SliceTo` | col[:hi], a slice with an open lower end |
| `pg.SubscriptExpr` | col[index] with an expression index |
| `pg.ArrayPositions` | array_positions(col, v) |
| `pg.ArrayRemoveNull` | array_remove(col, NULL) |
| `pg.EmptyArray` | '{}'::type[], the typed empty array |
| `pg.ArrayLit` | an array literal in text form, for the odd shapes a Param cannot express such as a non-default lower bound |
| `pg.WidthBucketThresholds` | width_bucket(e, thresholds[]), the array form, for weighted picks |
| `pg.ExtractFrom` | extract(field FROM e) for any operand, such as an interval or an age() result |
| `pg.FieldIsoDow` | — |
| `pg.FieldIsoYear` | — |
| `pg.FieldTimezone` | — |
| `pg.FieldMillennium` | — |
| `pg.FieldMicroseconds` | — |
| `pg.FieldMilliseconds` | — |
| `pg.FieldDecade` | — |
| `pg.FieldCentury` | — |
| `pg.FieldTimezoneHour` | — |
| `pg.FieldTimezoneMinute` | — |
| `pg.FieldJulian` | — |
| `pg.DateTruncZone` | date_trunc('field', e, 'zone') (PG12) |
| `pg.TimestampLit` | timestamp '...', a typed literal for an author constant |
| `pg.TimestamptzLit` | '...'::timestamptz, the zone-aware form |
| `pg.DateLit` | date '...' |
| `pg.Infinity` | 'infinity'::timestamptz for open-ended validity |
| `pg.OverlapsInterval` | (start1, interval1) OVERLAPS (start2, interval2) |
| `pg.IntervalMul` | (iv * n), for random() * interval '365 days' |
| `pg.JustifyInterval` | justify_interval(iv) |
| `pg.RangeExpr` | a range from two expressions, so a bound may be NULL or a column rather than a value |
| `pg.NullBound` | the unbounded side of a range, rendered as NULL |
| `pg.DateRange` | daterange(lo, hi, bounds) |
| `pg.NumRange` | numrange(lo, hi, bounds) |
| `pg.TstzRangeExpr` | tstzrange(lo, hi, bounds) from expressions |
| `pg.Int4Range` | int4range(lo, hi, bounds) from two bound values |
| `pg.UpperInf` | UpperInf and LowerInf test for an unbounded side |
| `pg.LowerInf` | lower_inf(r): the range has no lower bound |
| `pg.Multirange` | a Postgres multirange in text form, typed by element |
| `pg.TstzMultirange` | tstzmultirange(r) |
| `pg.MultirangeDiff` | (a - b) for multiranges |
| `pg.RangeAggTyped` | range_agg(r) typed as a multirange, and RangeIntersectAgg renders range_intersect_agg(r) |
| `pg.RangeIntersectAgg` | range_intersect_agg(r), the range common to every row in the group |
| `pg.UnnestMultirange` | unnest(m) as a scalar set-returning expression |
| `pg.Regclass` | 'name'::regclass, an author constant naming a relation |
| `pg.BitAnd` | l & r |
| `pg.BitOr` | l \| r |
| `pg.BitXor` | l # r |
| `pg.BitNot` | ~e |
| `pg.ShiftLeft` | e << n |
| `pg.ShiftRight` | e >> n |
| `pg.BitString` | a bit(n) or varbit value in text form |
| `pg.ErrNotBits` | returned by BitLit for text that is not a bit string |
| `pg.BitLit` | B'1011' |
| `pg.BitStringOr` | l \| r for two bit strings, which is a different operand type from BitOr even though the operator is the same |
| `pg.GetBit` | get_bit(e, n), the 0-based bit at n |
| `pg.BitCount` | bit_count(e), the number of set bits |
| `pg.PowerOp` | (base ^ exp), the operator form of exponentiation |
| `pg.Radians` | radians(e), converting degrees to radians |
| `pg.Sin` | sin(e), the operand in radians |
| `pg.Cos` | cos(e), the operand in radians |
| `pg.Asin` | asin(e), returning radians |
| `pg.Div` | div(l, r), integer division truncated toward zero |
| `pg.ModFn` | mod(l, r) |
| `pg.RoundHalf` | round(e), to the nearest integer with halves away from zero |
| `pg.TruncZero` | trunc(e), toward zero, with no digit count |
| `pg.RandomNormal` | random_normal(mean, stddev) (PG16), a normally distributed value rather than the uniform one random() gives |
| `pg.Setseed` | setseed(seed), making the following random() calls reproducible in the same session |
| `pg.AdvisoryXactLock` | pg_advisory_xact_lock(key), a transaction-scoped advisory lock |
| `pg.AdvisoryXactLock2` | pg_advisory_xact_lock(namespace, key), the two-argument form |
| `pg.TryAdvisoryXactLock` | pg_try_advisory_xact_lock(key), which returns false rather than waiting |
| `pg.Hashtext` | hashtext(e), the hash Postgres uses internally |
| `pg.SizePretty` | pg_size_pretty(bytes), a human-readable byte count |
| `pg.TotalRelationSize` | pg_total_relation_size(oid): the table, its indexes and its TOAST data together |
| `pg.RelationSize` | pg_relation_size(oid): the table alone, without indexes |
| `pg.IndexesSize` | pg_indexes_size(oid), the indexes alone |
| `pg.ColumnSize` | pg_column_size(e), the on-disk bytes of one value including compression |
| `pg.XidAge` | age(xid), transactions elapsed since xid |
| `pg.FormatType` | format_type(typid, typmod), the SQL name of a type as it would be written in DDL |
| `pg.CancelBackend` | pg_cancel_backend(pid), cancelling the current query on that connection but leaving it open |
| `pg.TerminateBackend` | pg_terminate_backend(pid), closing the connection outright |
| `pg.BlockingPids` | pg_blocking_pids(pid), the backends holding the locks that pid is waiting for |
| `pg.InputIsValid` | pg_input_is_valid(value, type) (PG16): whether the text parses as that type |
| `pg.GenerateSeriesScalar` | generate_series(from, to) in the select list rather than in FROM |
| `pg.UnnestScalar` | unnest(arr) in the select list |
| `pg.RepeatExpr` | repeat(e, n) with an expression count |
| `pg.PowerExpr` | power(base, exp) with an expression exponent |
| `pg.SubstrExpr` | substr(e, from, count) with expression bounds |
| `pg.ArrayElementsScalar` | jsonb_array_elements(doc) in the select list |
| `pg.PathQueryScalar` | jsonb_path_query(doc, path) in the select list |
| `pg.CurrentDatabase` | current_database() |
| `pg.HasRole` | pg_has_role(user, role, privilege), where privilege is MEMBER or USAGE |
| `pg.CurrentXactID` | pg_current_xact_id(), assigning one if this transaction has not written yet |
| `pg.CurrentXactIDIfAssigned` | pg_current_xact_id_if_assigned(), NULL when the transaction has not written |
| `pg.TxidCurrent` | txid_current(), the pre-PG13 spelling of CurrentXactID |
| `pg.ExportSnapshot` | pg_export_snapshot(), so another session can see exactly this transaction's view |
| `pg.CurrentWalLsn` | pg_current_wal_lsn(), the primary's write position |
| `pg.WalLsnDiff` | pg_wal_lsn_diff(a, b), the byte distance between two WAL positions |
| `pg.LastXactReplayTimestamp` | pg_last_xact_replay_timestamp(), the commit time of the last replayed transaction |
| `pg.GetSerialSequence` | pg_get_serial_sequence(table, column), the sequence name behind a serial or identity column |
| `pg.Setval` | setval(sequence, value), moving a sequence forward |
| `pg.Encode` | encode(bytes, format), core Postgres: format is 'hex', 'base64' or 'escape' |
| `pg.DaysBetween` | l - r for two dates, which Postgres types as an integer day count rather than an interval |
| `pg.DatePlusDays` | d + days, where an integer added to a date means days |
| `pg.DateMinusDays` | d - days |
| `pg.OverlapsExpr` | l && r for two array expressions |
| `pg.ArrayContainsExpr` | l @> r for two array expressions |
| `pg.ArrayContainedByExpr` | l <@ r for two array expressions |
| `pg.UUIDv7Timestamp` | UUIDv7Timestamp extracts the millisecond timestamp from a UUID v7 on any Postgres version: the first 48 bits, read as hex, as epoch milliseconds |
| `pg.SliceExprs` | (col)[lo:hi] with expression bounds |
| `pg.TSRankCDNormalized` | ts_rank_cd(vector, query, normalization) |
| `pg.SetConfig` | set_config(name, value, is_local), which is how a session setting takes a bound value |
| `pg.SetLocal` | SetLocal binds a setting for the current transaction |
| `pg.CurrentSetting` | current_setting('name', true): NULL when unset |

### objectforms

| Name | What it is |
|---|---|
| `pg.ErrCascadeNeedsDrop` | returned for a CASCADE on an ALTER DOMAIN action that is not a DROP CONSTRAINT, the one action Postgres allows it after |
| `pg.ErrSingleExpression` | returned for a one-expression statistics object given a second element or a kind, which Postgres's grammar has no form for: the single-expression form measures one expression's own distribution and the multi-element form measures how elements depend on each other |
| `pg.DependsOnExtensionStatement` | the [NO] DEPENDS ON EXTENSION form, which says an object is dropped with an extension without being a member of it |
| `pg.Dependable` | an object that can be made to depend on an extension |
| `pg.DependsOnExtension` | DependsOnExtension makes an object depend on an extension, so that dropping the extension drops the object too |
| `pg.NoDependsOnExtension` | NoDependsOnExtension breaks that tie, leaving the object in place when the extension is dropped |
| `pg.CreateCollationFrom` | CreateCollationFrom copies an existing collation under a new name, the form that takes no options at all |
| `pg.CreateStatisticsExpr` | CreateStatisticsExpr creates statistics for one expression, the form that takes a single element and no kinds |
| `pg.CommentOnDomainConstraint` | CommentOnDomainConstraint starts a comment on a domain's constraint: COMMENT ON CONSTRAINT name ON DOMAIN domain |
| `pg.AlterRangeTypeQuery` | ALTER TYPE on a range type with no action yet, which has no Build |
| `pg.AlterRangeType` | AlterRangeType starts an ALTER TYPE on a range type |
| `pg.AlterTypeNameStatement` | ALTER TYPE .. |
| `pg.AlterBaseTypeQuery` | ALTER TYPE on a base type with no action yet, which has no Build |
| `pg.AlterBaseType` | AlterBaseType starts an ALTER TYPE on a base type |
| `pg.AlterBaseTypeStatement` | ALTER TYPE .. |

### objects

| Name | What it is |
|---|---|
| `pg.ErrEmptyName` | returned for an object value with no name |
| `pg.Schema` | a Postgres schema |
| `pg.Extension` | a Postgres extension |
| `pg.Enum` | a Postgres enum type whose labels are values of the Go type T, so a typo in a label is a compile error and a column using the type must be a Col[T] |
| `pg.CreateSchemaStatement` | CREATE SCHEMA |
| `pg.CreateSchema` | CreateSchema creates a schema |
| `pg.DropSchemaStatement` | DROP SCHEMA |
| `pg.DropSchema` | DropSchema drops a schema, which fails while it holds anything unless Cascade is given |
| `pg.CreateExtensionStatement` | CREATE EXTENSION |
| `pg.CreateExtension` | CreateExtension installs an extension into the database |
| `pg.DropExtensionStatement` | DROP EXTENSION |
| `pg.DropExtension` | DropExtension removes an extension, which fails while a column uses one of its types unless Cascade is given |
| `pg.CreateEnumStatement` | CREATE TYPE .. |
| `pg.CreateEnum` | CreateEnum creates an enum with its labels in order |
| `pg.AlterEnumQuery` | ALTER TYPE on an enum with no action yet, which has no Build |
| `pg.AlterEnum` | AlterEnum starts an ALTER TYPE on an enum |
| `pg.AddValueStatement` | ALTER TYPE .. |
| `pg.AlterEnumStatement` | an ALTER TYPE that renames a label or the type |
| `pg.DropTypeStatement` | DROP TYPE |
| `pg.DropType` | DropType drops an enum, which fails while a column uses it unless Cascade is given |
| `pg.Guardable` | a statement Postgres has no IF NOT EXISTS for, which knows the one condition it raises when repeated |
| `pg.GuardedStatement` | a statement wrapped so that running it twice is a no-op |
| `pg.Guarded` | Guarded makes a create that Postgres cannot write IF NOT EXISTS for safe to repeat, by catching the one condition that statement raises on a second run and nothing else:  DO $sqlb$ BEGIN CREATE TYPE ...; EXCEPTION WHEN duplicate_object THEN NULL; END $sqlb$  Like IF NOT EXISTS it checks the name only: an object of that name with another definition is left as it is |
| `pg.ConstraintStatement` | ALTER TABLE .. |
| `pg.AddConstraintStatement` | ALTER TABLE ADD c as its own statement, for Guarded, on the table the constraint's TableConstraint names |
| `pg.ErrUnnamedConstraint` | returned for an unnamed constraint where a name is what makes repeating the statement safe: under Guarded, an unnamed foreign key raises no duplicate_object and is simply added a second time |
| `pg.RenameSchemaStatement` | ALTER SCHEMA .. |
| `pg.RenameSchema` | RenameSchema renames a schema |

### Operators

| Name | What it is |
|---|---|
| `pg.IsDistinctFrom` | the null-safe inequality; IsNotDistinctFrom the null-safe equality |
| `pg.IsNotDistinctFrom` | l IS NOT DISTINCT FROM r, equality that treats two NULLs as equal |
| `pg.IsTrue` | e IS TRUE |
| `pg.IsFalse` | e IS FALSE, which is true for false and not for NULL |
| `pg.IsUnknown` | e IS UNKNOWN, true only for NULL |
| `pg.LikeEscape` | LIKE $1 ESCAPE $2 so a literal % or _ can be matched |
| `pg.Collate` | e COLLATE "name" |
| `pg.ConcatOp` | (a \|\| b), the operator form |
| `pg.Greatest` | greatest(a, b, ...), the largest argument, skipping NULLs |
| `pg.Least` | least(a, b, ...), the smallest argument, skipping NULLs |

### partition

| Name | What it is |
|---|---|
| `pg.ErrNoBound` | returned for a partition with neither bounds nor Default |
| `pg.PartitionByRange` | PartitionByRange partitions the table by ranges of its key, time most often |
| `pg.PartitionByList` | PartitionByList partitions by listed values of one column, a tenant or a region |
| `pg.PartitionByHash` | PartitionByHash spreads rows over a fixed number of partitions by hash |
| `pg.PartitionBound` | FOR VALUES .. |
| `pg.FromTo` | a range partition's bounds, from inclusive to exclusive, typed to the key, so a partition of a timestamp key cannot be bounded by numbers |
| `pg.ListValues` | a list partition's values, typed to the key, which is taken only so its Go type fixes T |
| `pg.HashOf` | a hash partition: the rows whose key hashes to remainder modulo modulus |
| `pg.DefaultPartition` | DefaultPartition takes every row no other partition does |
| `pg.CreatePartitionQuery` | CREATE TABLE .. |
| `pg.CreatePartition` | CreatePartition starts a partition child of parent |
| `pg.CreatePartitionStatement` | a complete CREATE TABLE .. |
| `pg.AttachPartition` | AttachPartition attaches an existing table as a partition, scanning it to check the bounds; a CHECK constraint matching them lets Postgres skip the scan |
| `pg.DetachStatement` | ALTER TABLE .. |
| `pg.DetachPartition` | DetachPartition detaches child from parent, leaving it an ordinary table |

### Dialect

| Name | What it is |
|---|---|
| `pg.Dialect` | the Postgres dialect, re-exported so a caller that already imports this package for its constructs does not need a second import to name the engine |
| `pg.Is` | whether d is the Postgres dialect |
| `pg.SelectClause` | a clause for a SELECT on this engine |
| `pg.InsertClause` | a clause for an INSERT on this engine |
| `pg.UpdateClause` | a clause for an UPDATE on this engine |
| `pg.DeleteClause` | a clause for a DELETE on this engine |

### pg18

| Name | What it is |
|---|---|
| `pg.GeneratedVirtual` | GeneratedVirtual makes col a virtual generated column computed by e, evaluated when read rather than stored, which is Postgres 18's default for GENERATED ALWAYS AS |
| `pg.ErrConstraintAlterMix` | returned for an ALTER CONSTRAINT that combines INHERIT or NO INHERIT with the other options: Postgres 18's grammar has them as two separate forms |
| `pg.AlterConstraintClause` | ALTER CONSTRAINT on a declared constraint: its deferrability, whether it is enforced, or whether a NOT NULL constraint is inherited |
| `pg.AlterConstraint` | AlterConstraint starts an ALTER CONSTRAINT on c |
| `pg.PeriodColumn` | a range column that a temporal key is over: the last column of a primary or unique key WITHOUT OVERLAPS, or the PERIOD of a foreign key |
| `pg.Period` | Period marks a range column for a temporal key |

### policy

| Name | What it is |
|---|---|
| `pg.Policy` | a row-level security policy on one table; its name is unique per table, which is why the table is part of the value |
| `pg.Table` | — |
| `pg.PolicyCommand` | the command a policy applies to |
| `pg.AllCommands` | — |
| `pg.SelectCommand` | — |
| `pg.InsertCommand` | — |
| `pg.UpdateCommand` | — |
| `pg.DeleteCommand` | — |
| `pg.PolicyRole` | one of the role-independent targets a policy can name, which a library can write without knowing any of a deployment's role names |
| `pg.RolePublic` | — |
| `pg.RoleCurrentUser` | — |
| `pg.RoleSessionUser` | — |
| `pg.CreatePolicyStatement` | CREATE POLICY |
| `pg.CreatePolicy` | CreatePolicy creates a policy |
| `pg.AlterPolicyQuery` | ALTER POLICY with no action yet, which has no Build |
| `pg.AlterPolicy` | AlterPolicy starts an ALTER POLICY |
| `pg.AlterPolicyStatement` | a complete ALTER POLICY |
| `pg.ErrRenamePolicyAlone` | returned for an ALTER POLICY that both renames the policy and changes it, which Postgres has no form for |
| `pg.DropPolicyStatement` | DROP POLICY .. |
| `pg.DropPolicy` | DropPolicy drops a policy from its table |

### Ranges and multiranges

| Name | What it is |
|---|---|
| `pg.Range` | a Postgres range in its text form, for example "[2026-01-01,2026-02-01)" |
| `pg.Bounds` | the inclusivity string a range constructor takes: "[)", "[]", "(]", "()" |
| `pg.BoundsIncExc` | — |
| `pg.BoundsIncInc` | — |
| `pg.BoundsExcInc` | — |
| `pg.BoundsExcExc` | — |
| `pg.TstzRange` | tstzrange($1, $2, 'bounds') |
| `pg.Int8Range` | int8range($1, $2, 'bounds') |
| `pg.RangeContains` | range @> $n for an element |
| `pg.RangeContainsRange` | l @> r; RangeOverlaps l && r; Adjacent l -\|- r; StrictlyLeft l << r; StrictlyRight l >> r |
| `pg.RangeContainedBy` | l <@ r: every point of l lies in r |
| `pg.RangeOverlaps` | l && r: the two share at least one point |
| `pg.Adjacent` | l -\|- r: the two touch without overlapping |
| `pg.StrictlyLeft` | l << r: every point of l is before every point of r |
| `pg.StrictlyRight` | l >> r |
| `pg.RangeUnion` | l + r |
| `pg.RangeIntersect` | l * r, the common part, empty when they do not meet |
| `pg.RangeDiff` | l - r |
| `pg.LowerBound` | LowerBound and UpperBound are nullable: an unbounded side is NULL |
| `pg.UpperBound` | upper(r), NULL when the range is unbounded above |
| `pg.IsEmpty` | isempty(r) |
| `pg.LowerInc` | lower_inc(r): whether the lower bound is inclusive |
| `pg.UpperInc` | upper_inc(r) |
| `pg.RangeMerge` | range_merge(l, r), the smallest range containing both |
| `pg.RangeAgg` | range_agg(r), a multirange, typed as text here |

### Recursive CTE options

| Name | What it is |
|---|---|
| `pg.SearchDepthFirst` | a recursive-CTE suffix (PG14): SEARCH DEPTH FIRST BY cols SET ordercol |
| `pg.SearchBreadthFirst` | the breadth-first form |
| `pg.Cycle` | a recursive-CTE suffix (PG14): CYCLE cols SET flagcol USING pathcol |
| `pg.Both` | "search cycle" so one CTE.Suffix carries both clauses |

### roles

| Name | What it is |
|---|---|
| `pg.RoleRef` | a role a statement can name: a Role the deployment declared, or one of the role-independent keywords a library can write without knowing any of a deployment's names |
| `pg.Role` | a Postgres role, declared once as a value and named by every statement that gives it something to own or a policy to be subject to |
| `pg.Ownable` | a declared object OWNER TO can move to another role |
| `pg.SetOwnerStatement` | the OWNER TO statement of one declared object, whose ALTER keyword names the object kind |
| `pg.SetOwner` | SetOwner gives a declared object to another role, the migration a library writes when the role that ran its install is not the role that will run it from then on |

### Select clauses

| Name | What it is |
|---|---|
| `pg.JoinFull` | FULL OUTER JOIN |
| `pg.OrderByNulls` | OrderByNulls makes an order term with NULLS FIRST or NULLS LAST |
| `pg.OrderUsing` | OrderUsing makes an "ORDER BY expr USING op" term, for operator classes such as pgvector's distance operators |
| `pg.LimitAll` | LIMIT ALL in the after-order slot: explicit "no limit" |
| `pg.Materialized` | a CTE prefix: MATERIALIZED (true) or NOT MATERIALIZED (false) |
| `pg.IntersectAll` | INTERSECT ALL, which keeps duplicate rows where INTERSECT collapses them |
| `pg.ExceptAll` | EXCEPT ALL, subtracting one occurrence per duplicate rather than every occurrence |
| `pg.ArraySubquery` | ARRAY(SELECT ...), typed as a slice of the author's element type |
| `pg.AnyOf` | e op ANY (SELECT ...); AllOf renders e op ALL (SELECT ...) |
| `pg.AllOf` | e op ALL (SELECT ...): the comparison holds against every row the subquery returns, and an empty subquery makes it true |
| `pg.Lateral` | Lateral marks a source as LATERAL, so its body may reference columns of sources to its left |
| `pg.Values` | Values makes a VALUES list a source: "(VALUES ($1, $2), ($3, $4)) AS alias (a, b)" |
| `pg.FuncSource` | FuncSource makes a set-returning function call a source: "generate_series($1, $2) AS g (n)" |
| `pg.WithOrdinality` | WithOrdinality adds "WITH ORDINALITY", which appends a bigint row number column |

### sequence

| Name | What it is |
|---|---|
| `pg.Sequence` | a Postgres sequence whose values are T, so NextVal(s) is an Expr[T] and DefaultExpr accepts it only on a column of T |
| `pg.NextVal` | nextval(s), the sequence's next value, typed to it |
| `pg.CreateSequenceStatement` | CREATE SEQUENCE |
| `pg.CreateSequence` | CreateSequence creates a sequence of T, written AS its SQL integer type |
| `pg.AlterSequenceQuery` | ALTER SEQUENCE with no action yet, which has no Build |
| `pg.AlterSequence` | AlterSequence starts an ALTER SEQUENCE |
| `pg.AlterSequenceStatement` | a complete ALTER SEQUENCE: one or more options, which Postgres applies together, or a rename, which must stand alone |
| `pg.ErrRenameAlone` | returned for a sequence rename given another option too: RENAME TO is a statement of its own in Postgres's grammar |
| `pg.ErrBadCache` | returned for a sequence cache below 1, which Postgres refuses |
| `pg.DropSequence` | DropSequence drops a sequence, which fails while a default uses it unless Cascade is given |
| `pg.DropObjectStatement` | DROP SEQUENCE, DROP VIEW, DROP MATERIALIZED VIEW or DROP FUNCTION: one object, with IF EXISTS and CASCADE |

### Table sources

| Name | What it is |
|---|---|
| `pg.SampleMethod` | the sampling method TABLESAMPLE uses |
| `pg.SampleBernoulli` | — |
| `pg.SampleSystem` | — |
| `pg.TableSample` | TABLESAMPLE method (percent) [REPEATABLE (seed)] after a table reference |
| `pg.DistinctOn` | DISTINCT ON (exprs) after SELECT |
| `pg.FetchFirst` | FETCH FIRST $n ROWS ONLY, or ROWS WITH TIES, after OFFSET |
| `pg.OverridingSystemValue` | OverridingSystemValue and OverridingUserValue are INSERT clauses for identity columns |
| `pg.OverridingUserValue` | OVERRIDING USER VALUE, which makes the column take the generated value and ignore the one supplied |

### SQL/JSON

| Name | What it is |
|---|---|
| `pg.JSONOption` | JSONOption modifies a SQL/JSON constructor or aggregate (PG16) |
| `pg.AbsentOnNull` | AbsentOnNull drops null values from the object or array |
| `pg.NullOnNull` | NullOnNull keeps null values, the default, stated explicitly |
| `pg.WithUniqueKeys` | WithUniqueKeys rejects duplicate keys |
| `pg.ReturningJSONB` | ReturningJSONB selects jsonb output; the default is json |
| `pg.JSONOrderBy` | JSONOrderBy orders the elements of JSON_ARRAYAGG |
| `pg.JSONPair` | one 'key': value of JSON_OBJECT |
| `pg.Value` | — |
| `pg.Pair` | Pair makes a JSONPair |
| `pg.JSONObject` | JSON_OBJECT('k': v, .. |
| `pg.JSONArrayAgg` | JSON_ARRAYAGG(e [ORDER BY ...] [ABSENT ON NULL]) |
| `pg.JSONObjectAgg` | JSON_OBJECTAGG(k: v [ABSENT ON NULL]) |
| `pg.JSONKind` | the IS JSON qualifier |
| `pg.JSONAny` | — |
| `pg.JSONAnyValue` | — |
| `pg.JSONObj` | — |
| `pg.JSONArr` | — |
| `pg.JSONScalar` | — |
| `pg.IsJSON` | e IS JSON [kind] [WITH UNIQUE KEYS] (PG16) |
| `pg.JSONValueOption` | JSONValueOption configures JSON_VALUE and JSON_QUERY (PG17) |
| `pg.Returning` | Returning sets the RETURNING type of JSON_VALUE |
| `pg.WithWrapper` | WithWrapper wraps the result in an array always |
| `pg.WithoutWrapper` | the value bare, and raises when the path matched more than one |
| `pg.WithConditionalWrapper` | WithConditionalWrapper wraps only when the path matched more than one value |
| `pg.JSONValue` | JSON_VALUE(doc, 'path' ...) typed R (PG17) |
| `pg.JSONQuery` | JSON_QUERY(doc, 'path' ...) (PG17) |
| `pg.JSONExists` | JSON_EXISTS(doc, 'path') (PG17) |
| `pg.JSONSubscript` | doc['a']['b'] (PG14), the subscript form of a path lookup |
| `pg.SetSubscript` | an UPDATE target: SET doc['customer']['tier'] = value |
| `pg.PathQueryArray` | jsonb_path_query_array(doc, 'path') |
| `pg.JSONBSetLax` | jsonb_set_lax(col, path, value, create_missing, null_treatment) |
| `pg.JSONNull` | the JSON null literal, distinct from SQL NULL |
| `pg.EmptyJSONArray` | EmptyJSONArray and EmptyJSONObject are the COALESCE fallbacks for an aggregate over zero rows |
| `pg.EmptyJSONObject` | '{}'::jsonb, the COALESCE fallback for an object aggregate over zero rows |
| `pg.RowRef` | RowRef references a whole row by its table alias, for to_jsonb(t) and count(t.*)-style uses |

### table

| Name | What it is |
|---|---|
| `pg.DropClause` | a Postgres option after DROP TABLE's table name |
| `pg.ErrMixedReferences` | returned for a foreign key whose referenced columns are on more than one table, which no engine accepts |
| `pg.ErrUnknownKeyword` | returned for a referential action, deferral or identity kind outside the named constants, such as a zero value |
| `pg.RefAction` | what a foreign key does when the row it references is deleted or updated |
| `pg.RefNoAction` | — |
| `pg.RefRestrict` | — |
| `pg.RefCascade` | — |
| `pg.RefSetNull` | — |
| `pg.RefSetDefault` | — |
| `pg.Deferral` | when a deferrable constraint is checked |
| `pg.InitiallyImmediate` | — |
| `pg.InitiallyDeferred` | — |
| `pg.IdentityKind` | whether an identity column refuses an explicit value |
| `pg.Always` | — |
| `pg.ByDefault` | — |
| `pg.TableConstraint` | a named constraint of one table, declared once |
| `pg.PrimaryKeyClause` | a PRIMARY KEY table constraint |
| `pg.PrimaryKey` | PrimaryKey makes the table's primary key from one or more of its columns |
| `pg.UniqueClause` | a UNIQUE table constraint |
| `pg.Unique` | Unique makes a named unique constraint over one or more columns |
| `pg.ColumnPair` | one column of a foreign key and the column it references |
| `pg.RefPair` | RefPair pairs a column with the column it references |
| `pg.ForeignKeyClause` | a FOREIGN KEY table constraint |
| `pg.ForeignKey` | ForeignKey makes a named foreign key from one or more pairs, all referencing one table |
| `pg.References` | the one-column foreign key, left for Postgres to name |
| `pg.CheckClause` | a named CHECK table constraint, the same one ddl.Check writes |
| `pg.Check` | Check makes a named CHECK constraint |
| `pg.Identity` | Identity makes col an identity column, its value drawn from a sequence Postgres owns |
| `pg.GeneratedStored` | GeneratedStored makes col a stored generated column computed by e, which is of the column's own Go type |
| `pg.ColumnCollate` | ColumnCollate gives a text column a collation, written straight after its type |
| `pg.Unlogged` | Unlogged makes the table skip the write-ahead log: faster, lost on a crash, and not replicated |
| `pg.Cascade` | Cascade makes DROP TABLE also drop what depends on the table: views and the foreign keys of other tables, never the other tables themselves |

### tableforms

| Name | What it is |
|---|---|
| `pg.Inherit` | Inherit makes the table a child of parent, so a query on the parent also reads this table's rows and the child must already have every one of the parent's columns with the same types |
| `pg.NoInherit` | NoInherit detaches the table from parent, leaving both tables and all their rows in place; the parent simply stops seeing this table's rows |
| `pg.OfType` | OfType makes the table a typed table of a composite type, so its column list is that type's attribute list and changing the type changes the table |
| `pg.NotOfType` | NotOfType breaks the tie between a typed table and its composite type, leaving the table with the columns it has as its own declaration |
| `pg.Rule` | a Postgres rewrite rule on one table, declared as a value so that the statements enabling and disabling one need not be told the name twice |
| `pg.EnableRule` | EnableRule turns a rule back on for ordinary statements, the default state of a rule that has been disabled |
| `pg.DisableRule` | DisableRule stops a rule rewriting anything, without dropping it |
| `pg.EnableReplicaRule` | EnableReplicaRule turns the rule on only where session_replication_role is replica, which is how a rule applies on a subscriber and not on the origin |
| `pg.EnableAlwaysRule` | EnableAlwaysRule turns the rule on in every session_replication_role, replication included |
| `pg.IdentitySequenceClause` | the ALTER COLUMN action that changes the sequence behind an identity column |
| `pg.AlterIdentitySequence` | AlterIdentitySequence changes the sequence an identity column draws from, naming it through the column because it has no name of its own that a declaration could hold: Postgres creates it with the column, owns it and drops it with the column, so AlterSequence, which takes a declared pg.Sequence, has nothing to be handed |

### tableshape

| Name | What it is |
|---|---|
| `pg.ErrRecursiveColumns` | returned for a recursive view with no column list, which Postgres refuses: the recursive reference in the body needs names before the body has produced any |
| `pg.OnCommitAction` | what happens to a temporary table's rows when the transaction that created it commits |
| `pg.PreserveRows` | — |
| `pg.DeleteRows` | — |
| `pg.DropAtCommit` | — |
| `pg.Temporary` | Temporary makes the table live in the session's temporary schema and vanish when the session ends |
| `pg.OnCommit` | OnCommit says what happens to a temporary table's rows at commit |
| `pg.Inherits` | Inherits makes the new table a child of one or more parents, so a query on a parent also reads this table's rows |
| `pg.TableAccessMethod` | TableAccessMethod stores the table's rows in an access method other than the server's default |
| `pg.TableStorage` | TableStorage sets the table's storage parameters at creation, the same open set AlterIndex and AlterMaterializedView take |
| `pg.ColumnStorage` | ColumnStorage says what the server may do with one column's value when it is too wide for its row: the column-level counterpart of the type's own strategy |
| `pg.ColumnCompression` | ColumnCompression names the algorithm a variable-length column's values are compressed with, pglz or lz4 where the server was built with it |
| `pg.MatchType` | how a composite foreign key treats a row with some columns NULL and some not |
| `pg.MatchFull` | — |
| `pg.MatchSimple` | — |
| `pg.OnTruncate` | OnTruncate fires on TRUNCATE |
| `pg.CreateTableAsStatement` | CREATE TABLE .. |
| `pg.CreateTableAs` | CreateTableAs makes a table whose columns come from a query's select list and fills it with that query's rows |
| `pg.SetColumnStorage` | SetColumnStorage changes an existing column's storage strategy, the ALTER half of ColumnStorage |
| `pg.SetColumnCompression` | SetColumnCompression changes an existing column's compression algorithm, the ALTER half of ColumnCompression, and like it applies to values written from now on |

### Text

| Name | What it is |
|---|---|
| `pg.ILike` | e ILIKE $n, case-insensitive LIKE with a bound pattern |
| `pg.NotILike` | e NOT ILIKE $n |
| `pg.SimilarTo` | e SIMILAR TO $n |
| `pg.Regex` | e ~ $n, a case-sensitive POSIX regular-expression match |
| `pg.RegexI` | e ~* $n, the case-insensitive match |
| `pg.NotRegex` | e !~ $n |
| `pg.NotRegexI` | e !~* $n |
| `pg.Upper` | upper(e) |
| `pg.Initcap` | initcap(e), capitalising the first letter of each word |
| `pg.Length` | length(e), in characters |
| `pg.Reverse` | reverse(e) |
| `pg.Substring` | substring(e, from, count) with bound positions |
| `pg.Left` | left(e, n), the first n characters |
| `pg.Right` | right(e, n), the last n characters |
| `pg.Trim` | btrim(e), removing whitespace from both ends |
| `pg.LTrim` | ltrim(e), the leading end only |
| `pg.RTrim` | rtrim(e), the trailing end only |
| `pg.LPad` | lpad(e, n, fill) |
| `pg.RPad` | rpad(e, n, fill), padding and truncating on the right |
| `pg.Position` | strpos(e, needle): 1-based, 0 when absent |
| `pg.StartsWith` | starts_with(e, prefix) |
| `pg.Replace` | replace(e, from, to), every occurrence |
| `pg.SplitPart` | split_part(e, sep, n), the nth field, 1-based |
| `pg.ConcatWs` | concat_ws(sep, parts...), joining with a separator and skipping NULL parts rather than propagating them |
| `pg.Repeat` | repeat(e, n) |
| `pg.MD5` | md5(e), returning hex text |
| `pg.SHA256Hex` | encode(sha256(e::bytea), 'hex') |
| `pg.RegexpReplace` | regexp_replace(e, pattern, replacement, flags) |
| `pg.RegexpMatch` | regexp_match(e, pattern): the capture groups, or NULL |
| `pg.RegexpCount` | regexp_count(e, pattern) (PG15), the number of matches |
| `pg.RegexpSplitToArray` | regexp_split_to_array(e, pattern) |
| `pg.RegexpSplitToTable` | regexp_split_to_table(e, pattern) AS alias (part) |
| `pg.ToChar` | to_char(e, format) for any input type |
| `pg.Format` | format(fmt, args...) |

### tier4

| Name | What it is |
|---|---|
| `pg.Domain` | a Postgres domain: a base type with constraints, whose values are T, so it is a ddl.SQLType[T] and a column of another Go type cannot use it |
| `pg.DomainValue` | the VALUE keyword, the value being checked in a domain's CHECK, typed to the domain |
| `pg.DomainConstraint` | a named constraint of one domain, declared once, the domain's counterpart of TableConstraint: CreateDomain's Check, and AlterDomain's AddConstraint, ValidateConstraint and DropConstraint all take it and refuse one declared on another domain |
| `pg.ErrForeignDomain` | returned for a DomainConstraint of one domain used in a statement about another |
| `pg.DomainCheck` | one named CHECK of a domain |
| `pg.CreateDomainStatement` | CREATE DOMAIN |
| `pg.CreateDomain` | CreateDomain creates a domain over base, a type for the same Go type |
| `pg.AlterDomainQuery` | ALTER DOMAIN with no action yet, which has no Build |
| `pg.AlterDomain` | AlterDomain starts an ALTER DOMAIN |
| `pg.AlterDomainStatement` | a complete ALTER DOMAIN |
| `pg.DropDomain` | DropDomain drops a domain, which fails while a column uses it unless Cascade is given |
| `pg.Collation` | a Postgres collation |
| `pg.CreateCollationStatement` | CREATE COLLATION |
| `pg.CreateCollation` | CreateCollation creates a collation for locale, from the operating system's library unless ICU is given |
| `pg.DropCollation` | DropCollation drops a collation |
| `pg.ColumnCollation` | ColumnCollation gives a text column a declared collation, qualified by its schema; ColumnCollate takes a built-in collation's name |
| `pg.Statistics` | an extended statistics object, which tells the planner that two columns are correlated so it stops multiplying their selectivities |
| `pg.StatKind` | what an extended statistics object measures |
| `pg.NDistinct` | — |
| `pg.Dependencies` | — |
| `pg.MCV` | — |
| `pg.CreateStatisticsStatement` | CREATE STATISTICS |
| `pg.CreateStatistics` | CreateStatistics creates statistics over at least two columns of t, which is the fewest Postgres accepts, so fewer does not compile |
| `pg.DropStatistics` | DropStatistics drops a statistics object |
| `pg.AlterExtensionStatement` | ALTER EXTENSION .. |
| `pg.UpdateExtension` | UpdateExtension updates an installed extension to the version its package files provide, which is how a library picks up a new pgvector |
| `pg.RenameMaterializedViewStatement` | ALTER MATERIALIZED VIEW .. |
| `pg.RenameMaterializedView` | RenameMaterializedView renames a materialized view; its schema is unchanged |

### tier5

| Name | What it is |
|---|---|
| `pg.AlterViewStatement` | ALTER VIEW .. |
| `pg.SetViewColumnDefault` | SetViewColumnDefault gives a view's column a default, a literal of the column's own Go type |
| `pg.DropViewColumnDefault` | DropViewColumnDefault removes a view column's default |
| `pg.DisableRowLevelSecurity` | DisableRowLevelSecurity turns row-level security off for the table, the inverse of EnableRowLevelSecurity |
| `pg.ForceRowLevelSecurity` | ForceRowLevelSecurity applies the table's policies to its owner too |
| `pg.NoForceRowLevelSecurity` | NoForceRowLevelSecurity exempts the owner again, the inverse of ForceRowLevelSecurity |
| `pg.Composite` | a composite type, declared once: a named row of typed attributes, CREATE TYPE .. |
| `pg.Attribute` | one attribute of a composite type: a name and a typed SQL type, the type's Go parameter checked when the attribute is made and erased after, since a composite's attributes are of different types |
| `pg.Attr` | Attr makes an attribute of type t |
| `pg.CreateCompositeStatement` | CREATE TYPE .. |
| `pg.CreateComposite` | CreateComposite creates a composite type with at least one attribute |
| `pg.AlterCompositeQuery` | ALTER TYPE on a composite with no action yet, which has no Build |
| `pg.AlterComposite` | AlterComposite starts an ALTER TYPE on a composite |
| `pg.AlterCompositeStatement` | a complete ALTER TYPE on a composite, one action |
| `pg.DropComposite` | DropComposite drops a composite type, which fails while a column uses it unless Cascade is given |

### Dates and times

| Name | What it is |
|---|---|
| `pg.Interval` | a Postgres interval in its text form, for example "1 day" or "01:30:00" |
| `pg.DateField` | DateField names the field for Extract and DateTrunc |
| `pg.FieldYear` | — |
| `pg.FieldQuarter` | — |
| `pg.FieldMonth` | — |
| `pg.FieldWeek` | — |
| `pg.FieldDay` | — |
| `pg.FieldHour` | — |
| `pg.FieldMinute` | — |
| `pg.FieldSecond` | — |
| `pg.FieldDow` | — |
| `pg.FieldDoy` | — |
| `pg.FieldEpoch` | — |
| `pg.Now` | now(), the transaction start time |
| `pg.ClockTimestamp` | clock_timestamp(), the wall clock |
| `pg.StatementTimestamp` | statement_timestamp(), fixed for the statement |
| `pg.CurrentDate` | current_date, which takes no parentheses |
| `pg.DateTrunc` | date_trunc('field', e) |
| `pg.DateBin` | date_bin(stride, e, origin): fixed-width buckets |
| `pg.Extract` | extract(field FROM e) |
| `pg.AtTimeZone` | e AT TIME ZONE $n |
| `pg.AddInterval` | e + $n::interval; SubInterval renders e - $n::interval |
| `pg.SubInterval` | (e - interval '...') with a constant interval |
| `pg.Diff` | (l - r), a timestamp difference, as an Interval |
| `pg.Age` | age(l, r) |
| `pg.MakeInterval` | make_interval(days => $1, hours => $2) |
| `pg.ToTimestamp` | to_timestamp(e, format) |
| `pg.ToDate` | to_date(e, format), parsing text with a Postgres format string such as 'YYYY-MM-DD' |
| `pg.EpochSeconds` | to_timestamp(seconds) for a float epoch |
| `pg.OverlapsPeriod` | (s1, e1) OVERLAPS (s2, e2) |

### trigger

| Name | What it is |
|---|---|
| `pg.ErrEmptyBody` | returned for a trigger function with no body |
| `pg.Function` | a Postgres function, declared once as a value and named by every statement that creates, drops, comments on or executes it |
| `pg.Params` | — |
| `pg.CreateFunctionStatement` | CREATE FUNCTION .. |
| `pg.CreateTriggerFunction` | CreateTriggerFunction creates a PL/pgSQL trigger function with body, which is a program rather than an expression and so the one place a whole body is a string: the library author writes it, it is never built from input, and it is dollar-quoted with a tag it does not contain |
| `pg.DropFunction` | DropFunction drops a function, naming it by the signature its declaration carries, which is what tells two functions of one name apart |
| `pg.Trigger` | a trigger on one table |
| `pg.TriggerEvent` | what fires a trigger: an INSERT, an UPDATE, an UPDATE of named columns, or a DELETE |
| `pg.OnInsert` | OnInsert fires on INSERT |
| `pg.OnUpdate` | OnUpdate fires on any UPDATE |
| `pg.OnUpdateOf` | OnUpdateOf fires on an UPDATE whose SET names one of cols |
| `pg.OnDelete` | OnDelete fires on DELETE |
| `pg.Old` | a column's value before the change, in a row trigger's WHEN |
| `pg.New` | a column's value after the change, in a row trigger's WHEN |
| `pg.CreateTriggerQuery` | CREATE TRIGGER with no timing yet, which has no Build |
| `pg.CreateTrigger` | CreateTrigger starts a trigger |
| `pg.TimedTriggerQuery` | CREATE TRIGGER with its timing and events and no function yet, which has no Build |
| `pg.CreateTriggerStatement` | a complete CREATE TRIGGER |
| `pg.DropTriggerStatement` | DROP TRIGGER .. |
| `pg.DropTrigger` | DropTrigger drops a trigger from its table |
| `pg.RenameTriggerStatement` | ALTER TRIGGER .. |
| `pg.RenameTrigger` | RenameTrigger renames a trigger on its table; to names the same table |

### TRUNCATE

| Name | What it is |
|---|---|
| `pg.Truncate` | a TRUNCATE statement |
| `pg.NewTruncate` | NewTruncate starts a TRUNCATE for d |
| `pg.ErrNoTables` | returned by Build when no table was named, which would otherwise render a statement the database rejects |

### typedef

| Name | What it is |
|---|---|
| `pg.RangeType` | a Postgres range type over the values of Go type T, declared once as a value |
| `pg.CreateRangeTypeStatement` | CREATE TYPE .. |
| `pg.CreateRangeType` | CreateRangeType creates a range type over subtype, the SQL type of the values the range bounds |
| `pg.BaseType` | a Postgres base type, a type implemented by functions rather than composed from other types, whose values a Go program sees as T |
| `pg.CreateShellTypeStatement` | CREATE TYPE with nothing after the name |
| `pg.CreateShellType` | CreateShellType declares the name of a base type before the type itself exists |
| `pg.TypeAlignment` | the storage alignment a base type's values need |
| `pg.AlignChar` | — |
| `pg.AlignInt2` | — |
| `pg.AlignInt4` | — |
| `pg.AlignDouble` | — |
| `pg.TypeStorage` | how a base type's values are stored when they do not fit in a row |
| `pg.StoragePlain` | — |
| `pg.StorageExternal` | — |
| `pg.StorageExtended` | — |
| `pg.StorageMain` | — |
| `pg.TypeCategory` | the class a base type belongs to, which decides which implicit casts the parser will consider in an ambiguous call |
| `pg.CategoryUser` | — |
| `pg.CategoryNumeric` | — |
| `pg.CategoryString` | — |
| `pg.CategoryDateTime` | — |
| `pg.CategoryBoolean` | — |
| `pg.CategoryGeometric` | — |
| `pg.CategoryNetwork` | — |
| `pg.CategoryRange` | — |
| `pg.CategoryTimespan` | — |
| `pg.CreateBaseTypeStatement` | the CREATE TYPE that fills in a shell type |
| `pg.CreateBaseType` | CreateBaseType turns a shell type into a base type, with input the function that parses a value's text form and output the one that prints it |

### view

| Name | What it is |
|---|---|
| `pg.View` | a Postgres view |
| `pg.MaterializedView` | a Postgres materialized view: a view whose rows are stored, and read stale until refreshed |
| `pg.CheckOption` | what a view's WITH CHECK OPTION checks: an INSERT or UPDATE through the view must produce a row the view shows |
| `pg.Local` | — |
| `pg.Cascaded` | — |
| `pg.CreateViewQuery` | CREATE VIEW with no body yet, which has no Build |
| `pg.CreateView` | CreateView starts a view |
| `pg.CreateViewStatement` | a complete CREATE VIEW |
| `pg.RenameViewStatement` | ALTER VIEW .. |
| `pg.RenameView` | RenameView renames a view; its schema is unchanged |
| `pg.DropView` | DropView drops a view |
| `pg.CreateMaterializedViewQuery` | CREATE MATERIALIZED VIEW with no body yet |
| `pg.CreateMaterializedView` | CreateMaterializedView starts a materialized view |
| `pg.CreateMaterializedViewStatement` | a complete CREATE MATERIALIZED VIEW |
| `pg.RefreshStatement` | REFRESH MATERIALIZED VIEW |
| `pg.RefreshMaterializedView` | RefreshMaterializedView recomputes a materialized view, blocking reads of it while it runs |
| `pg.DropMaterializedView` | DropMaterializedView drops a materialized view |

### Window functions

| Name | What it is |
|---|---|
| `pg.FrameGroups` | the GROUPS frame mode |
| `pg.ExcludeCurrentRow` | — |
| `pg.ExcludeGroup` | — |
| `pg.ExcludeTies` | — |
| `pg.ExcludeNoOthers` | — |
| `pg.PercentRank` | percent_rank(), the rank as a fraction from 0 to 1 |
| `pg.CumeDist` | cume_dist(), the cumulative distribution |
| `pg.Ntile` | Ntile splits the partition into n buckets |
| `pg.Lag` | Lag and Lead without a default are nullable: the row may not exist |
| `pg.Lead` | lead(e, offset), the row ahead |
| `pg.LagDefault` | lag(e, offset, default) |
| `pg.LeadDefault` | lead(e, offset, default), non-null for the same reason |

### Writes

| Name | What it is |
|---|---|
| `pg.ErrConflictTarget` | returned for ON CONFLICT DO UPDATE with no conflict target: Postgres accepts DO NOTHING without one and refuses DO UPDATE, because it cannot tell which unique key's row to update |
| `pg.Conflict` | Conflict accumulates an ON CONFLICT clause for an Insert |
| `pg.OnConflict` | OnConflict targets the given columns (a unique index) |
| `pg.OnConstraint` | OnConstraint targets a named constraint |
| `pg.Excluded` | Excluded references the row proposed for insertion: EXCLUDED.column |
| `pg.DefaultValues` | INSERT INTO t DEFAULT VALUES |
| `pg.UpdateFrom` | UPDATE .. |
| `pg.DeleteUsing` | DELETE FROM t USING sources |

978 exported names.

<!-- END GENERATED REFERENCE -->
