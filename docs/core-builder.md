# The core builder

The core is one package with no third-party dependencies. It holds the pieces every engine shares.

## Tables and columns

A table is a value, not a type:

```go
var Users = sqlb.Table{Name: "app_user", Alias: "u"}

var (
	UserID      = sqlb.Col[string](Users, "id")
	UserEmail   = sqlb.Col[string](Users, "email")
	UserCreated = sqlb.Col[time.Time](Users, "created_at")
)
```

`Col[T]` returns an `Expr[T]`. The type parameter is the Go type the column scans into, and it is what every later check is made against. A library exports these as package-level variables, and that export is its schema contract: the application can join on them, filter on them and scan them, and can do nothing else with them.

`Alias` is what the SQL uses. Two packages that both declare a table called `user` stay distinct because their aliases differ, and a duplicate alias in one statement is an error from `Build` rather than a silent ambiguity.

## Expressions

| Constructor | What it makes |
|---|---|
| `Col[T]` | a column reference |
| `Param[T]` | a bound parameter; the value never reaches the SQL text |
| `Literal[T]` | a constant fragment, for `''` or `FALSE` as a fallback; never for request data |
| `Fn[R]` | any function call, typed by the author at the call site |
| `Cast[R, T]` | `CAST(e AS type)`, re-typed |
| `Nullable[T]` | reinterprets `Expr[T]` as `Expr[*T]` without changing the SQL |
| `AssertNotNull[T]` | the reverse, and a promise the author makes |

`Nullable` earns its place on a LEFT JOIN. The same physical column is non-null under an INNER join and nullable under a LEFT one, and only the second needs a pointer target. It was added after an integration failure that no test over SQL text could have caught.

### One condition on that

`Bind` captures the pointer, not the value, and it lives as long as the query does. So a `Query` is **not safe for concurrent use**: executing one from two goroutines is a **data race**, because both write through the same pointers and interleave each other's rows. Build the query where its destination lives, per call, which is what every recipe does. `One` and `Each` refuse an overlapping execution with `ErrQueryInUse` rather than corrupting the result. [execution.md](./execution.md) has the detail.

## Predicates

`Eq`, `NotEq`, `Less`, `LessEq`, `Greater`, `GreaterEq` compare an expression with a Go value of the same type. The `*Expr` forms compare two expressions, which is what a join condition is. `In` and `NotIn` bind every value, and an empty list renders a constant rather than the syntax error `IN ()` would be. `IsNull`, `IsNotNull`, `Between`, `Like`, `And`, `Or` and `Not` complete the set.

Groups are always parenthesised, including a group of one. The parentheses cost nothing and their absence costs a precedence bug where an `OR` inside an `AND` widens a security check.

## Statements

`NewSelect(dialect)` builds a SELECT: `Columns` or `Exprs`, `From`, `Join`, `Where`, `GroupBy`, `Having`, `Window`, `With` and `WithRecursive`, `Union` and the other set operations, `Order`, `Limit`, `Offset`. `NewInsert`, `NewUpdate` and `NewDelete` build the writes. Every one of them ends in `Build() (Query, error)`.

`FROM` is optional, so `SELECT 1 + 1` is expressible. A join without a FROM is not, and `Build` says so.

## Build is where failure happens

`Build` returns an error rather than a panic, and it fails closed:

| Error | When |
|---|---|
| `ErrNoColumns` | a SELECT with nothing selected |
| `ErrJoinWithoutFrom` | a join with no FROM to join to |
| `ErrDuplicateAlias` | two sources with the same alias |
| `ErrNoTargets` | `One` or `Each` on a query that bound no destinations |
| `ErrNoAssignments` | an UPDATE with no SET, or an INSERT made as a struct literal with no body |
| `ErrNoWhere` | an UPDATE or DELETE made as a struct literal with no WHERE and no `All()` — the builders cannot reach this, see below |
| `ErrUnsupportedDialect` | an engine *expression* under another engine's dialect; an engine *clause* on the wrong engine or statement kind does not compile |

An UPDATE or DELETE with no WHERE and no `All()`, and an INSERT with nothing to insert, are not `Build` errors any more: they do not compile. `Build` is only defined on the type that `Where`, `All`, `Row`, `Select` or `Body` return, so the builders cannot produce those statements and `ErrNoWhere` and `ErrNoAssignments` remain only for a struct literal of the complete type. The whole-table case is written with `All()` so it is said rather than inferred from an absence.

## Extension points

A library that wants to let its callers extend a query it owns has four mechanisms, in order of preference: `Option` functions that add joins, predicates, columns or ordering; `Clause` values that render into a named slot of the statement; `Bindings` that concatenate a core binding set with a plugin's; and `Fn`, the typed door, for a construct no helper covers. The escape hatch exists so that a missing helper is never a reason to abandon the builder and go back to strings.
