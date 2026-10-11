# `ch` — ClickHouse constructs

```go
import "github.com/ubgo/sqlb/ch"
```

The ClickHouse construct package: the clauses and functions ClickHouse spells differently from everyone else, built on the core's extension points.

It is deliberately small. `ch` is 25 names covering the clauses that change how a query is planned plus a handful of functions, and it exists to prove that the extension points carry a second engine rather than to be a ClickHouse client. **Nothing in this package has ever been executed against a ClickHouse server.** Every helper is checked as rendered text.

## Contents

- [Setup](#setup)
- [Reading a collapsing table](#reading-a-collapsing-table)
- [Latest row per group without a window function](#latest-row-per-group-without-a-window-function)
- [Conditional aggregates](#conditional-aggregates)
- [Settings](#settings)
- [Everything here fails closed](#everything-here-fails-closed)
- [Reference](#reference)

## Setup

Every snippet on this page uses these declarations.

```go
import (
	"github.com/ubgo/sqlb/expr"
)

// A library declares its table and its typed columns, and exports them.
var (
	Events    = expr.Table{Name: "events", Alias: "e"}
	EventID   = expr.Col[string](Events, "id")
	EventUser = expr.Col[string](Events, "user_id")
	EventKind = expr.Col[string](Events, "kind")
	EventAt   = expr.Col[string](Events, "at")
	EventCost = expr.Col[float64](Events, "cost")
)
```

## Reading a collapsing table

`ReplacingMergeTree` collapses duplicate rows in the background, so a plain read can see both the old row and the new one. `FINAL` forces the collapse at read time, and it binds to the driving table rather than to the statement, which is why it is a per-table clause:

```go
q, err := stmt.NewSelect(ch.Dialect{}).
	Exprs(EventID, EventUser).
	From(Events).
	Clause(ch.Final()).
	Build()
```

```sql
SELECT `e`.`id`, `e`.`user_id` FROM `events` AS `e` FINAL
```

ClickHouse quotes identifiers with backticks, which is the dialect's doing and not something a construct decides.

`ch.Sample(0.1)` is placed the same way and asks for an approximate answer over a sampled table.

## Latest row per group without a window function

`LIMIT n BY` is the construct ClickHouse has and nobody else does: the first `n` rows per group, after `ORDER BY`. It is how "the latest event per user" is written without a window function, and it reads far better than the `ROW_NUMBER() = 1` subquery every other engine needs:

```go
q, err := stmt.NewSelect(ch.Dialect{}).
	Exprs(EventUser, EventKind, EventAt).
	From(Events).
	Order(expr.OrderBy(EventAt, expr.SortDesc)).
	Clause(ch.LimitBy(1, EventUser)).
	Build()
```

```sql
SELECT `e`.`user_id`, `e`.`kind`, `e`.`at` FROM `events` AS `e` ORDER BY `e`.`at` DESC LIMIT ? BY `e`.`user_id`
```

The `1` is bound rather than written into the text, like every other value here.

## Conditional aggregates

ClickHouse spells a filtered aggregate as a suffix on the function rather than as SQL's `FILTER (WHERE …)`, so these are `ch` rather than core:

```go
paid := ch.SumIf(EventCost, expr.Eq(EventKind, "purchase"))
best := ch.ArgMax(EventID, EventCost)
users := ch.UniqExact(EventUser)
```

```sql
sumIf(`e`.`cost`, `e`.`kind` = ?)
argMax(`e`.`id`, `e`.`cost`)
uniqExact(`e`.`user_id`)
```

`ch.ArgMax` is worth knowing about even if you never use ClickHouse: it answers "the id of the row with the highest cost" in one expression, which is a correlated subquery or a window function anywhere else.

## Settings

`SETTINGS` is per statement and ClickHouse-only:

```go
q, err := stmt.NewSelect(ch.Dialect{}).
	Exprs(EventID).
	From(Events).
	Clause(ch.Settings(ch.JoinUseNulls())).
	Build()
```

```sql
SELECT `e`.`id` FROM `events` AS `e` SETTINGS join_use_nulls = ?
```

`JoinUseNulls` is the one setting given a name of its own, because it changes what a `LEFT JOIN` miss scans as: with it, a missing row is `NULL` rather than a type's default value, which is what a `Nullable[T]` destination expects.

## Everything here fails closed

Every construct in this package records `Args.Unsupported`, so building it under another dialect fails rather than rendering something Postgres would accept with a different meaning:

```go
_, err := stmt.NewSelect(postgres.Dialect{}).
	Exprs(EventID).
	From(Events).
	Clause(ch.LimitBy(1, EventUser)).
	Build()
// err: sqlb: construct is not supported by this dialect
//      ch.LimitBy under dialect "postgres"
```

That is the rule the whole engine split exists for, and it is tested rather than asserted: see `ch_test.go`.

## Reference

<!-- BEGIN GENERATED REFERENCE -->

Generated from the source by `task docs:reference`. Every exported name in `ch` appears here.

### ClickHouse constructs

| Name | What it is |
|---|---|
| `ch.DialectClickHouse` | the engine name reported by the dialect |
| `ch.Dialect` | sqlb.ClickHouseDialect |
| `ch.Is` | whether d is the ClickHouse dialect |
| `ch.SettingJoinUseNulls` | — |
| `ch.SettingMaxThreads` | — |
| `ch.ErrEmptySetting` | recorded for SETTINGS with no settings, or a Setting{} with no name: Setting has exported fields and is written as a literal, and either rendered SQL ClickHouse rejects |
| `ch.Setting` | one SETTINGS name = value pair |
| `ch.Name` | — |
| `ch.Value` | — |
| `ch.SelectClause` | a clause for a SELECT on ClickHouse: an alias for the core's typed clause, so a statement of another dialect refuses it at compile time |
| `ch.Prewhere` | PREWHERE (preds) before WHERE |
| `ch.Final` | FINAL and SAMPLE are per table reference, not per statement |
| `ch.Sample` | SAMPLE ratio, for approximate queries over a sampled table |
| `ch.ArrayJoin` | ARRAY JOIN col AS alias and returns the element expression, typed T, in the same call so the element type is checked |
| `ch.LimitBy` | LIMIT n BY exprs after ORDER BY: the first n rows per group, which is how "latest event per user" is written without a window function |
| `ch.Settings` | SETTINGS name = value, .. |
| `ch.JoinUseNulls` | the setting that makes a LEFT JOIN miss a NULL instead of a default value, so Nullable[T] scans as intended |
| `ch.ArgMax` | argMax(value, by): the value from the row where by is largest |
| `ch.Uniq` | uniq(e), the approximate distinct count |
| `ch.UniqExact` | uniqExact(e), the exact distinct count |
| `ch.SumIf` | sumIf(e, cond), the -If combinator on sum |
| `ch.CountIf` | countIf(cond) |
| `ch.Count` | count() |
| `ch.ToStartOfHour` | ToStartOfHour truncates a time to the hour |
| `ch.ToStartOfDay` | ToStartOfDay truncates a time to the day |

25 exported names.

<!-- END GENERATED REFERENCE -->
