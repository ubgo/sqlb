# `dialect` — one package per engine

```go
import "github.com/ubgo/sqlb/dialect/postgres"
```

A dialect is the smaller half of an engine: how a bound parameter is written, how an identifier is quoted, and which optional capabilities the engine has. The constructs are the larger half and live in [`pg`, `sqlite` and `ch`](../engines.md).

Each ships as its own package, importing [`render`](../render/README.md) and nothing else.

## Contents

- [Setup](#setup)
- [The three that ship](#the-three-that-ship)
- [Why each is its own package](#why-each-is-its-own-package)
- [Capabilities are declined, not denied](#capabilities-are-declined-not-denied)
- [Using one](#using-one)

## The three that ship

| Package | Placeholder | Quoting | Capabilities |
|---|---|---|---|
| `dialect/postgres` | `$1`, `$2` | `"ident"` | `Returning`, `InsertAliasing` |
| `dialect/sqlite` | `?` | `"ident"` | `Returning` |
| `dialect/mysql` | `?` | `` `ident` `` | none of those two; `LikeEscaping`, `LiteralQuoting`, `NumericParameters` |
| `dialect/clickhouse` | `?` | `` `ident` `` | none |

A fourth capability, `LikeEscaping`, says whether the engine accepts an `ESCAPE` clause on `LIKE`. Postgres, SQLite and MySQL implement it; ClickHouse is not known to accept the clause and declines. See [searching for text a person typed](../expr/README.md#searching-for-text-a-person-typed) for why a predicate asks.

`CaseInsensitiveLike` says whether the engine has `ILIKE`. Only Postgres implements it, and it is the engine that needs it, because its `LIKE` is case-sensitive where SQLite's is not for ASCII. The fold predicates, `ContainsFold` and its siblings, write `ILIKE` for a dialect that declares it and `lower(e) LIKE lower(pattern)` for every other, so an outside dialect that declines it gets the portable form rather than an error. See [ignoring case, the same on every engine](../expr/README.md#ignoring-case-the-same-on-every-engine).

`NumericParameters` is MySQL's and nobody else's. In a prepared statement MySQL widens a bare integer parameter that meets a `DECIMAL` to `DECIMAL(65,30)`, and `sum()` of an integer column is a `DECIMAL` there, so `COALESCE(sum(x), ?)` and `sum(x) + ?` returned `40.000000000000000000000000000000` for 40, which does not scan into the `int64` an `Expr[int64]` promises. `Coalesce` and the arithmetic operators ask for the capability and write `CAST(? AS SIGNED)` (or `UNSIGNED`) for a bare integer parameter; a column, a float, a string, a NULL, `CASE` and `NULLIF` were each measured not to widen and render as before.

Each also exports `Name`, the engine name an unsupported-construct error prints and an engine package guards on.

## Setup

The snippets below use one table.

```go
import (
	"github.com/ubgo/sqlb/expr"
)

var Users = expr.Table{Name: "app_user", Alias: "u"}
```

## Why each is its own package

This is not organisation for its own sake. If the dialects this module ships were defined inside the core while an outside one had to reach the core through an interface, the built-ins would be taking a shortcut nobody else could take, and "a community package can add an engine" would be a claim resting on nothing.

Written this way, **adding Postgres and adding an engine nobody has written yet are the same act.** `TestDialects_ImportOnlyRender` reads each package's import list and fails on anything but `render`, so the shortcut cannot creep back in.

## Capabilities are declined, not denied

ClickHouse is the one to study. It has no `RETURNING`, and it says so by **not implementing the interface** rather than by implementing it and returning false:

```go
// clickhouse.Dialect has no SupportsReturning method at all.
_, ok := any(clickhouse.Dialect{}).(render.Returning)  // false
```

A write builder discovers the absence with a type assertion, so an engine is never made to lie about a capability it does not have. That is why `Dialect` itself is two methods rather than twelve: a wide dialect interface is where query builders go to die, because every engine has to answer every question whether or not the question applies to it.

The same holds for `InsertAliasing`. Only Postgres accepts `INSERT INTO t AS alias`, so only `postgres.Dialect` implements it.

MySQL declines `Returning` the same way, so `InsertReturning` on a MySQL statement does not compile. Its identifiers are backquoted because a double-quoted token is a string in MySQL unless `ANSI_QUOTES` is on. `mysql.Dialect` also implements `UnrestrictedAddColumn` and `CombinedAlter`, which Postgres implements too, and declines `UntypedParameters`, which only Postgres needs: Postgres cannot infer a type for a bound parameter passed to `concat()`, and the capability is how `sqlb.Concat` casts one.

And for `LiteralQuoting`, which writes a value into the statement text where no parameter is allowed. `postgres.Dialect`, `sqlite.Dialect` and `mysql.Dialect` implement it, MySQL refusing a string with a backslash because no spelling reads the same under both settings of `NO_BACKSLASH_ESCAPES`; `clickhouse.Dialect` does not, because a quoting rule written from documentation and never executed is the one kind of unverified claim that is an injection when wrong. SQLite also declines one value: `time.Time`, since SQLite has no time type and no spelling that compares equal to a time the driver bound. See [render: writing a literal](../render/README.md#writing-a-literal).

## Using one

Pass it to a statement constructor:

```go
q, err := stmt.NewSelect(postgres.Dialect{}).From(Users).Build()
```

If you already import an engine's construct package, it re-exports its own dialect so you need no second import:

```go
pg.Dialect{}      // = postgres.Dialect
sqlite.Dialect{}
ch.Dialect{}
```

And the root facade carries the older spellings, which are the same types:

```go
sqlb.PostgresDialect{}
sqlb.SQLiteDialect{}
sqlb.ClickHouseDialect{}
```

To write your own, see [writing a dialect](../render/README.md#writing-a-dialect).
