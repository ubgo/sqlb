# Engine packages

## The rule

A construct lives in the core only when Postgres, MySQL 8, SQLite and ClickHouse all accept it with the same syntax and the same meaning. Anything else lives in that engine's package, even when two of the four agree.

The reason is that a near-match is worse than no match. If the core rendered a function that three engines have and the fourth spells differently, a query would build cleanly and fail at the database for exactly one deployment, which is the failure that takes longest to find. Pushing it into the engine package turns that into a `Build` error naming the construct.

The core keeps the mechanisms, and the engine package makes the decisions. `Aggregate`, `WinFn`, `Window`, `Fn`, `Clause`, `SourceOf` and `SubqueryCompare` are all in the core, because the shape of an aggregate or a window is universal. Which aggregates and which window functions exist is an engine decision.

## Engines from outside this repository

The three below are the ones this repository ships. They have no privilege a community package lacks: a dialect is three methods, the guard rule that makes constructs fail closed is exported, and the engine name is a plain string rather than an entry in a closed list. [dialects.md](./dialects.md) has the shape, and `engine_test.go` in the core proves it by building one from an external test package.

## Which engines are real

Three engines have a dialect in the core and a construct package. Only one of the three has ever been executed.

| Engine | Dialect | Constructs | Executed against a real server |
|---|---|---|---|
| SQLite | `sqlb.SQLiteDialect` | `sqlite/`, 304 exported names | **Yes.** Every helper runs against a real database in `contrib/driver-sqlite`, including FTS5 and the conflict-resolution differences. |
| Postgres | `sqlb.PostgresDialect` | `pg/`, 978 exported names, plus eight extension sub-packages | Yes. `contrib/driver-pgx` executes the conformance suite and every construct in `pg` and its extension packages against a real Postgres 17 — 593 cases — gated on `SQLB_PG_DSN`, and the same suite runs against a Postgres 18 container for the constructs 17 refuses. |
| ClickHouse | `sqlb.ClickHouseDialect` | `ch/`, 25 exported names | No. |
| MySQL 8 | `sqlb.MySQLDialect` | `mysql/`, 331 exported names | **Yes, all of it.** `contrib/driver-mysql` executes every construct and the DDL, and runs the shared conformance suite, on a real MySQL 8.4, gated on `SQLB_MYSQL_DSN`. Its first run found that the core's `LIKE` escape clause was a syntax error in MySQL's default mode. |

All four dialects live in the core, beside each other in `dialect.go`. A dialect is about ten lines, every engine needs one, and a caller naming the engine it builds for should not have to import that engine's construct package to do it. The constructs themselves stay in the engine package.

Read the right-hand column honestly. A `pg` helper is proven to render the text its test says it renders, which catches a wrong operator or a missing guard. It is not proven to be SQL Postgres accepts. `sqlite` is the only package here where both are true, and it is only true because its driver is pure Go and needs no server.

## pg

`github.com/ubgo/sqlb/pg` holds what only Postgres has. A library that imports it is a Postgres library, and its import list says so.

| Area | What is covered |
|---|---|
| JSONB and SQL/JSON | the operator family, `JSON_TABLE`, `JSON_OBJECT`, `JSON_QUERY`, `JSON_VALUE`, `JSON_EXISTS`, path queries, subscripts, `jsonb_set` and its lax form |
| Arrays | `ANY`, `ALL`, containment and overlap, slices, `unnest` with ordinality, positions, literals, and the shape functions: `array_dims`, `array_ndims`, `array_lower`, `array_upper`, `array_reverse`, `array_sort`, `array_shuffle`, `array_sample`, `trim_array` |
| Ranges | typed ranges and multiranges, containment, overlap, adjacency, `range_agg` |
| Text | `ILIKE`, `SIMILAR TO`, the regex family, `substring(FROM)`, `normalize`, quoting, hashing, `format` |
| Full text | `tsvector`, `tsquery`, ranking, headlines, weights |
| Sources | `LATERAL`, `ROWS FROM`, set-returning functions, `generate_series`, record sets with a typed column list |
| Statements | `DISTINCT ON`, `FOR UPDATE` and its variants, `ON CONFLICT`, `MERGE`, `RETURNING`, `UPDATE ... FROM`, `DELETE ... USING`, `TRUNCATE`, `LOCK TABLE` |
| Recursion | `SEARCH DEPTH FIRST`, `SEARCH BREADTH FIRST`, `CYCLE` |
| Session | `set_config`, `current_setting`, advisory locks, catalogue and size functions |

### Setup

The snippets in this section use these declarations.

```go
import (
	"fmt"

	"github.com/ubgo/sqlb"
	"github.com/ubgo/sqlb/pg"
)

var (
	Docs = sqlb.Table{Name: "docs", Alias: "d"}
	Tags = sqlb.Col[[]string](Docs, "tags")
)

// Every snippet renders for one engine.
var d = sqlb.PostgresDialect{}

// show prints a built statement. It is where the output under each snippet
// comes from.
func show(q sqlb.Query, err error) {
	if err != nil {
		fmt.Println("error:", err)
		return
	}
	fmt.Println(q.SQL)
	if len(q.Args) > 0 {
		fmt.Println(q.Args)
	}
}

// A locked statement, so the pg import is used by the Setup block itself.
var _ = pg.NewLockTable(d, Docs)
```

### Array shape

Postgres arrays may be multidimensional and are **not necessarily 1-based**, which is the trap these exist for. A slice produced by `array_remove` or by a slice expression can start at any index, so code that assumes `1` is wrong on arrays it did not construct.

```go
sqlb.NewSelect(d).Exprs(
	sqlb.As(pg.ArrayDims(Tags), "shape"),
	sqlb.As(pg.ArrayNDims(Tags), "dims"),
	sqlb.As(pg.ArrayLower(Tags, 1), "lo"),
	sqlb.As(pg.ArrayUpper(Tags, 1), "hi"),
).From(Docs)
```

```
SELECT array_dims("d"."tags") AS "shape", array_ndims("d"."tags") AS "dims", array_lower("d"."tags", $1) AS "lo", array_upper("d"."tags", $2) AS "hi" FROM "docs" AS "d"
[1 1]
```

All four of these return `*int32` rather than `int32`, and `ArrayDims` returns `*string`. That is deliberate: every one of them is NULL for an empty array, so the pointer forces the empty case to be handled rather than read as a zero that means something else. Binding one to a non-pointer is a compile error, which is the form the rule actually takes at a call site.

`ArrayLength` is the count and is a different question from `ArrayUpper`; on a 1-based array they agree, and on any other array they do not.

### Array reshaping

```go
sqlb.NewSelect(d).Exprs(
	sqlb.As(pg.ArraySort(Tags), "sorted"),
	sqlb.As(pg.ArrayReverse(Tags), "reversed"),
	sqlb.As(pg.TrimArray(Tags, 2), "trimmed"),
	sqlb.As(pg.ArrayShuffle(Tags), "shuffled"),
	sqlb.As(pg.ArraySample(Tags, 3), "sample"),
).From(Docs)
```

```
SELECT array_sort("d"."tags") AS "sorted", array_reverse("d"."tags") AS "reversed", trim_array("d"."tags", $1) AS "trimmed", array_shuffle("d"."tags") AS "shuffled", array_sample("d"."tags", $2) AS "sample" FROM "docs" AS "d"
[2 3]
```

Version floors worth knowing, since the failure is a runtime error from the server rather than anything this library can catch: `trim_array` is Postgres 14, `array_shuffle` and `array_sample` are 16, and `array_sort` and `array_reverse` are 17. `ArraySortDesc` is a separate constructor rather than an option on `ArraySort` for exactly that reason: the two-argument form is 17, and a caller on 16 can use `ArraySort` without meeting the difference as a production error.

```go
sqlb.NewSelect(d).Exprs(pg.ArraySortDesc(Tags, true)).From(Docs)
```

```
SELECT array_sort("d"."tags", $1) FROM "docs" AS "d"
[true]
```

### LOCK TABLE

```go
pg.NewLockTable(d, Docs).In(pg.LockShareRowExclusive).NoWait()
```

```
LOCK TABLE "docs" AS "d" IN SHARE ROW EXCLUSIVE MODE NOWAIT
```

Three things the API cannot warn you about, so they are here. A table lock is **held until the end of the transaction** and cannot be released early. `LOCK TABLE` outside a transaction is an error. And omitting `In` means `ACCESS EXCLUSIVE`, the strongest mode, which blocks even a plain `SELECT` — so naming the mode is almost always right.

`TableLockModeValues` lists all eight, weakest first, which is the conflict ordering: `LockAccessShare` blocks almost nothing, `LockAccessExclusive` blocks everything. Locking several tables in one statement is not a convenience but a correctness measure, since it takes the locks in one step and avoids deadlocking against a transaction taking the same locks in the other order.

Most callers wanting to stop concurrent writes to some **rows** want `pg.ForUpdate` instead, which locks rows rather than the table.

### TRUNCATE

```go
pg.NewTruncate(d, Docs).RestartIdentity().Cascade()
```

```
TRUNCATE TABLE "docs" AS "d" RESTART IDENTITY CASCADE
```

`Cascade` is a spelled-out method rather than an option value because it truncates **tables the caller did not name**, including tables owned by another package, and that should be visible at the call site and in a diff. `RestartIdentity` resets owned sequences, which is usually what somebody truncating in a test actually wants.

`TRUNCATE` is not transactional on every engine and fires no per-row triggers. `Delete` with an explicit `All()` is the checked alternative.

### Everything here fails closed

```
sqlb: construct is not supported by this dialect
pg.array under dialect "sqlite"
```

Every construct in this package records its name when rendered through another dialect, so a Postgres-only helper that reaches a SQLite statement fails at `Build`, in your process, naming the construct — never at the database.

Extensions are not in `pg` itself. Each has its own sub-package; see [extensions.md](./extensions.md).

## sqlite

`github.com/ubgo/sqlb/sqlite` holds what only SQLite has.

| Area | What is covered |
|---|---|
| Conflict resolution | `INSERT OR REPLACE` and its four siblings through `InsertOr`, and the `ON CONFLICT` upsert with `Excluded`, target and update conditions |
| JSON1 | `json_extract`, `->` and `->>`, `json_valid`, `json_type`, `json_array_length`, `json_set`, `json_insert`, `json_replace`, `json_remove`, `json_patch`, `json_array`, `json_object`, the group aggregates, and `json_each` and `json_tree` as FROM sources |
| Text and search | `GLOB`, FTS5 `MATCH` with `rank`, `bm25`, `highlight` and `snippet`, `instr`, `hex`, `quote`, `typeof`, `ifnull`, `iif`, `format`, `group_concat` |
| Time | `date`, `time`, `datetime`, `julianday`, `unixepoch`, `strftime`, and the modifier set including a relative `Offset` |
| Rowid and session | `rowid`, `last_insert_rowid`, `changes`, `total_changes`, `random`, `randomblob`, the scalar `min` and `max`, `sqlite_version` |
| Planner hints | `INDEXED BY` and `NOT INDEXED` |

Two things this package documents that a reader is likely to get wrong, and that its execution tests prove:

**`OR REPLACE` and `DO UPDATE` are not interchangeable.** `OR REPLACE` deletes the conflicting row and inserts a new one, so delete triggers fire and the rowid changes. `DO UPDATE` modifies in place. Anything holding a rowid reference breaks under the first and survives the second.

**SQLite has no date type.** A time is ISO-8601 text, a Julian day, or a Unix epoch integer, and the column's declared type does not say which. Every helper takes a value plus modifiers and returns text, so the schema has to be consistent on its own. Modifiers apply left to right, which is how `start of month`, `+1 month`, `-1 day` gives the month's last day.

## ch

`github.com/ubgo/sqlb/ch` holds what only ClickHouse has: `PREWHERE`, `FINAL`, `SAMPLE`, `ARRAY JOIN`, `LIMIT BY`, statement settings, and the aggregate and time functions that carry ClickHouse spellings.

Its status is worth stating plainly, because 25 names are enough to look supported and not enough to be. It covers the clauses that change how a ClickHouse query is planned and a handful of its aggregate and time functions. It does not cover the type system, the engine-specific table functions, or the insert paths. Nothing in it has been executed against a server. Treat it as proof that the builder's extension points carry a second engine, not as a ClickHouse client. It grows when a consumer needs it, which is the rule the whole repository is scoped by.

## Where a shared construct goes

`FULL OUTER JOIN` is the useful example. Postgres, SQLite and ClickHouse have it; MySQL does not. Under the rule it cannot be in the core, so it is `pg.JoinFull`. Three engines agreeing is not enough, and the rule does not take votes.

It is also the case that shows the rule's cost. SQLite has `FULL OUTER JOIN` too, so a SQLite user reaching for it finds nothing, because nobody has needed it there yet. The rule keeps the core honest and pushes duplication into the engine packages; `sqlite.OnConflict` and `pg.OnConflict` render nearly the same text for the same reason. That duplication is deliberate: the day one engine changes its spelling, only that package moves.
