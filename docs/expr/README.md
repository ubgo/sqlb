# `expr` — typed columns, predicates and expressions

```go
import "github.com/ubgo/sqlb/expr"
```

The typed half of the builder. It answers what one piece means and what Go type it has, and refuses to know that statements exist.

An `Expr` does not know it will appear in a `SELECT`, and a `Predicate` does not know whether it is a `WHERE` or a `JOIN` condition. That is what lets [`stmt`](../stmt/README.md) put them wherever the grammar allows, and what lets a library export columns that an application it has never heard of can compose.

## Contents

- [Setup](#setup)
- [A column is a typed value](#a-column-is-a-typed-value)
- [Comparisons](#comparisons)
- [Comparing two expressions](#comparing-two-expressions)
- [Searching for text a person typed](#searching-for-text-a-person-typed)
- [Ignoring case, the same on every engine](#ignoring-case-the-same-on-every-engine)
- [Composite keys and row constructors](#composite-keys-and-row-constructors)
- [Composing predicates](#composing-predicates)
- [Your own predicate](#your-own-predicate)
- [An operator the core does not spell](#an-operator-the-core-does-not-spell)
- [Arithmetic](#arithmetic)
- [Bound values anywhere](#bound-values-anywhere)
- [Nullability is a Go type](#nullability-is-a-go-type)
- [Aggregates and functions](#aggregates-and-functions)
- [Conditional values with CASE](#conditional-values-with-case)
- [Ordering](#ordering)
- [Window functions](#window-functions)
- [A subquery as a source](#a-subquery-as-a-source)
- [The zero Expr](#the-zero-expr)
- [Why Expr is a struct and not an interface](#why-expr-is-a-struct-and-not-an-interface)

## A column is a typed value

```go
var (
	Users     = expr.Table{Name: "app_user", Alias: "u"}
	UserID    = expr.Col[string](Users, "id")
	UserEmail = expr.Col[string](Users, "email")
	UserAge   = expr.Col[int](Users, "age")
)
```

`UserEmail` is an `Expr[string]`. It renders qualified by the table's alias, and it remembers where it came from:

```go
UserEmail.SQL(a)      // "u"."email"
UserEmail.Name()      // email
UserEmail.Table().Name // app_user
```

The alias lives on the table rather than on the statement because a library in another module declares its join at package level, before any statement exists.

## Setup

Every snippet on this page uses these declarations. They are the whole setup: two tables, their typed columns, and an `Args` to render a fragment into.

```go
import (
	"fmt"
	"strings"

	"github.com/ubgo/sqlb/dialect/postgres"
	"github.com/ubgo/sqlb/expr"
	"github.com/ubgo/sqlb/render"
)

// A library declares its table and its typed columns, and exports them.
var (
	Users     = expr.Table{Name: "app_user", Alias: "u"}
	UserID    = expr.Col[string](Users, "id")
	UserEmail = expr.Col[string](Users, "email")
	UserAge   = expr.Col[int](Users, "age")
)

// The application declares its own, and never imports the library's schema.
var (
	Invoices  = expr.Table{Name: "invoice", Alias: "i"}
	InvUserID = expr.Col[string](Invoices, "user_id")
	InvTotal  = expr.Col[int](Invoices, "total")
)

// render1 prints one fragment with the values it bound. It is where the output
// under each snippet comes from.
func render1(p expr.Predicate) {
	a := render.NewArgs(postgres.Dialect{})
	var b strings.Builder
	p.WriteSQL(&b, a)
	fmt.Println(b.String(), a.Values())
}
```

## Comparisons

The value is bound, never interpolated, and its type has to match the column's.

```go
expr.Eq(UserEmail, "ada@example.com")   // "u"."email" = $1
expr.Greater(UserAge, 30)               // "u"."age" > $1
```

`expr.Eq(UserEmail, 30)` **does not compile**: the column is an `Expr[string]`. That is the whole point — the mistake is caught where it is written rather than by the database at runtime.

Comparing two expressions uses the `Expr` forms, which is how a join condition is written:

```go
expr.EqExpr(UserID, InvUserID)   // "u"."id" = "i"."user_id"
```

Also available: `NotEq`, `Less`, `LessEq`, `GreaterEq`, `In`, `NotIn`, `IsNull`, `IsNotNull`, `Like`, `Between`, and the `*Expr` variant of each.

### Comparing two expressions

The `*Expr` forms compare two expressions rather than an expression and a value, and both sides share `T`, so a join between a string column and a bool column does not compile. The family is `EqExpr`, `NotEqExpr`, `LessExpr`, `LessEqExpr`, `GreaterExpr` and `GreaterEqExpr`.

```go
expr.NotEqExpr(UserID, InvUserID)               // "u"."id" <> "i"."user_id"
expr.GreaterEqExpr(InvTotal, expr.Param(100))   // "i"."total" >= $1
```

`InExprs` is the membership question asked of expressions rather than of bound values, which is how a column is tested against other columns:

```go
expr.InExprs(UserID, InvUserID, expr.Param("u1"))   // "u"."id" IN ("i"."user_id", $1)
```

`NotBetween` and `NotLike` are the negated forms of `Between` and `Like`, and `NullIf` renders `NULLIF(a, b)` typed `Expr[*T]`, because producing a NULL is the entire purpose of it.

Three more constructors exist for the places where a predicate has to become something else. `True` and `False` are the constant predicates: `True` is the `ON` clause of a LATERAL join, which has no condition of its own, and `False` is what an empty `In` list renders as, since `IN ()` is a syntax error everywhere and dropping the predicate would widen the result. `BoolExpr` turns a predicate back into an `Expr[bool]`, so a condition can be selected, aliased, or used as the value of a `CASE` branch:

```go
expr.As(expr.BoolExpr(expr.Greater(UserAge, 30)), "is_senior")
```

```
(("u"."age" > $1)) AS "is_senior"
[30]
```

### Searching for text a person typed

`Like` takes a **pattern**, so every character in it is pattern syntax. That is right when the caller wrote the pattern and wrong when somebody typed it into a search box: the `%` and `_` in what they typed then mean "anything" rather than themselves, so searching for `50%` also matches `50`, `500` and `150x`. Nothing fails; the answer is wrong and the query looks correct in a log.

`Contains`, `StartsWith` and `EndsWith` take a **value**. They escape it, add the wildcards, and write an `ESCAPE` clause:

```go
expr.Contains(UserName, "50%")
```

```
"u"."first_name" LIKE $1 ESCAPE '!'
[%50!%%]
```

`NotContains` is the negation, and `EscapeLike` is exported for a pattern you assemble yourself.

Prefer `StartsWith` where it fits: a prefix match can use a B-tree index on the column and a leading wildcard cannot, which on a table of any size is an index scan against a sequential one.

The `ESCAPE` clause is not decoration. Postgres and MySQL treat a backslash in a pattern as an escape character by default, and **SQLite has no default escape character at all**, so a backslash-escaped pattern matches nothing there without the clause. That difference is silent in both directions, which is why these predicates ask the dialect through `render.LikeEscaping` and return `ErrNoLikeEscape` rather than rendering something that matches the wrong rows.

The escape character is `!`, not a backslash, because the clause is statement text: in MySQL's default mode a backslash inside a string literal escapes the closing quote, so `ESCAPE '\'` is an unterminated string there. `!` is ordinary text in every engine's literals, and a backslash in the searched value is then just a character. `EscapeLike` escapes `!`, `%` and `_` to match.

### Ignoring case, the same on every engine

The engines disagree about case, and the disagreement is silent. Measured on 2026-09-28:

| Question | Postgres 17/18 | SQLite | MySQL 8.4, default collation | MySQL 8.4, `utf8mb4_bin` |
|---|---|---|---|---|
| `'Ada' = 'ada'` | false | false | **true** | false |
| `'café' = 'cafe'` | false | false | **true** | false |
| `'Ada' LIKE 'ada%'` | false | **true** | **true** | false |

So `Eq`, `In` and `Contains` return different rows on different engines for the same data, and code that means "ignore case" has to know which engine it is on to say so. Six predicates say it once, and the dialect picks the spelling:

| Predicate | Postgres | SQLite, MySQL, any other dialect |
|---|---|---|
| `EqFold(e, v)` | `lower(e) = lower($1)` | `lower(e) = lower(?)` |
| `InFold(e, v...)` / `NotInFold` | `lower(e) IN (lower($1), ...)` | the same with `?` |
| `ContainsFold`, `StartsWithFold`, `EndsWithFold` | `e ILIKE $1 ESCAPE '!'` | `lower(e) LIKE lower(?) ESCAPE '!'` |

```go
expr.ContainsFold(UserEmail, "50%OFF")
```

```
"u"."email" ILIKE $1 ESCAPE '!'
[%50!%OFF%]
```

The pattern forms escape the value exactly as `Contains` does, so a typed `%` still means itself. An empty `InFold` is `FALSE` and an empty `NotInFold` is `TRUE`, as with `In` and `NotIn`. There is no `NeFold` or `NotContainsFold`, because `expr.Not(...)` of the positive form means the same thing with the same answer for a NULL.

**Why a capability and not a check of the engine's name.** ILIKE is chosen by `render.CaseInsensitiveLike`, which only `postgres.Dialect` implements. A dialect from another repository that has ILIKE gets it by implementing one method, and one that does not gets the portable form rather than an error. Both spellings answer the same question: the conformance suite runs on Postgres a second time with the capability removed, and every case returns the same ids.

**Both sides are folded by the engine, never by Go.** The value goes through the engine's own `lower()`, not `strings.ToLower`, because the value and the column must be folded by the same rule. SQLite's `lower()` folds ASCII only, so a value lowered in Go would fold `É` while the column kept it, and a stored value would stop matching an exact copy of itself.

**Beyond ASCII, the collation decides.** ASCII answers are identical on every engine, and `sqlbtest`'s `case_fold` case holds every shipped engine to one list of expected ids. Beyond ASCII, measured:

| Question | Postgres, `COLLATE "C"` | Postgres, `pg_c_utf8` or a Unicode locale | SQLite | MySQL, either collation |
|---|---|---|---|---|
| `EqFold(e, "école")` against `ÉCOLE` | no match | match | no match | match |
| `EqFold(e, "CAFE")` against `café` | no match | no match | no match | match on the default collation, not on `utf8mb4_bin` |

Folding case cannot make a collation accent-sensitive. If accents must count on MySQL, declare the column `utf8mb4_bin`, which is also what keeps a plain `Eq` exact there.

**Index.** `lower(e)` is not `e`, so a plain index on the column does not serve these. For `EqFold` and `InFold`, declare the index on `lower(e)` on every engine, which is also how a case-insensitive unique key is written. On Postgres, an `ILIKE` with a leading wildcard is served by a `pg_trgm` GIN index, which [`pg/trgm`](../pg/README.md) declares.

## Composite keys and row constructors

A row constructor is several expressions treated as one value, `(a, b)`, and it is how a composite key is compared in one question rather than in a chain of them.

```go
expr.RowEq(
	expr.Row(UserID, UserEmail),
	expr.Row(expr.Param("u1"), expr.Param("ada@example.com")),
)
```

```
("u"."id", "u"."email") = ($1, $2)
[u1 ada@example.com]
```

`RowLess` and `RowGreater` are the ordered forms, and they are what keyset pagination is: "the row after this one" as a single comparison, which an index on the same columns can satisfy and a per-column `OR` chain cannot.

`RowIn` and `RowNotIn` ask the membership question against a list of tuples:

```go
expr.RowIn(
	expr.Row(UserID, UserAge),
	expr.Row(expr.Param("u1"), expr.Param(24)),
	expr.Row(expr.Param("u2"), expr.Param(33)),
)
```

```
("u"."id", "u"."age") IN (($1, $2), ($3, $4))
[u1 24 u2 33]
```

An empty list renders `FALSE` for `RowIn` and `TRUE` for `RowNotIn`, the same fail-closed rule as `In` and `NotIn`. Beware `RowNotIn` against nullable columns: a row is NULL if any element of it is, so one NULL makes the whole predicate NULL and it matches nothing. Where that is possible, `stmt.NotExists` says what was meant.

The subquery form needs nothing from this package, because `stmt.SubqueryCompare` takes a plain `SQLer` and a row constructor is one:

```go
stmt.SubqueryCompare(expr.Row(UserID, UserEmail), expr.OpIn, "", inner)
```

```
("u"."id", "u"."email") IN (SELECT ...)
```

Two limits are worth knowing. **Arity is not checked**: `Row` returns an opaque `SQLer`, so the number of elements is gone before a comparison sees it, and a pair tested against a triple is an error from the database rather than from the compiler. **Element types are not checked across the two sides** either, for the same reason. Keep the rows short and adjacent in the source; that is the one place in this package where the compiler cannot help.

These are core rather than engine constructs because all four reference engines accept both the tuple list and the subquery form. SQLite is the one that had to be checked rather than read, since it only grew row values in 3.15, and it is verified against a real database in `contrib/driver-sqlite`.

## Composing predicates

Parentheses are always written, so mixing `AND` and `OR` cannot change meaning by accident:

```go
expr.And(
	expr.Eq(UserEmail, "ada@example.com"),
	expr.Or(expr.Greater(UserAge, 30), expr.IsNull(UserAge)),
)
```

```
("u"."email" = $1 AND ("u"."age" > $2 OR "u"."age" IS NULL))
[ada@example.com 30]
```

## Your own predicate

`Predicate` is an open interface — a type with `WriteSQL` — so a consumer can add a condition the package does not ship. Render the inside with `PredicateSQL`, which is also how the engine packages embed a predicate inside a function call:

```go
type ageBand struct{ lo, hi int }

func (p ageBand) WriteSQL(b *strings.Builder, a *render.Args) {
	b.WriteString(expr.PredicateSQL(
		expr.And(expr.GreaterEq(UserAge, p.lo), expr.Less(UserAge, p.hi)), a))
}
```

`ageBand{18, 30}` renders `("u"."age" >= $1 AND "u"."age" < $2)` and composes with everything else here.

## An operator the core does not spell

The `Op` constants (`OpEq`, `OpLike`, `OpAdd` and the rest) name the operators the core's own predicates use. `Binary` takes the operator as a value, so an engine operator — Postgres's regex match, say — composes with everything else and stays typed:

```go
expr.Binary[bool](UserEmail, "~", expr.Param("^ada"))   // ("u"."email" ~ $1)
```

The operator is checked for its SHAPE, not against a list. A list would be wrong here: the operators an engine has come from its extensions, so naming them would refuse a valid one. A symbolic token built from SQL's operator characters passes, and so does a keyword phrase such as `IS NOT DISTINCT FROM` — both forms are needed, because a third of the core's own vocabulary is words. What is refused is a value that closes the expression and appends a statement, which is the one thing a shape can usefully catch: an operator cannot be quoted, so `"a" "=" "b"` is a syntax error and quoting is not available as a defence.

`Fn` and `Aggregate` are checked the same way against a function name's shape — an identifier, or a schema-qualified pair — because quoting `pg_catalog.date_trunc` as one identifier names a function that does not exist. A function whose name genuinely needs quoting is reachable through `Raw`.

`CheckedOperator`, `CheckedKeyword` and `CheckedFunctionName` are those three rules as functions, exported because the statement layer applies the same ones to a join keyword, a set operation and a subquery's quantifier, and one rule in two copies is the thing that drifts. An engine package building its own construct can use them for the same reason. Each writes the value or fails the render naming it, with `ErrUnknownValue`.

The whole set lives in `constants.go`, grouped by what it is for:

| Group | Constants |
|---|---|
| Comparison | `OpEq`, `OpNotEq`, `OpLess`, `OpLessEq`, `OpGreater`, `OpGreaterEq` |
| Membership and NULL | `OpIn`, `OpNotIn`, `OpIsNull`, `OpIsNotNull` |
| Arithmetic | `OpAdd`, `OpSub`, `OpMul`, `OpDiv`, `OpMod`, `OpConcat` |
| Three-valued logic | `OpIsDistinctFrom`, `OpIsNotDistinctFrom`, `OpIsTrue`, `OpIsFalse`, `OpIsUnknown` |
| Pattern | `OpLike`, `OpNotLike` |
| Connectives | `LogicAnd`, `LogicOr`, `LogicNot` |

`OpValues` is the canonical iteration order over the comparison and membership operators, and it exists so that a validator or a generator reads the same list the builder writes from rather than growing a second copy that drifts. They are constants rather than inline strings for one reason: `>=` mistyped as `=>` fails loudly and `<>` mistyped as `<` does not, so naming them removes the class of mistake that does not announce itself.

## Arithmetic

`Add`, `Sub`, `Mul`, `Div`, `Mod` and `Neg` take operands of one `Number` type and keep that type:

```go
expr.Mul(InvTotal, expr.Param(2))              // ("i"."total" * $1)
expr.Neg(expr.Sub(InvTotal, expr.Param(5)))    // (-("i"."total" - $1))
```

The result being the operand type matches SQL for same-type operands, and it carries SQL's trap with it: `Div` on integers truncates exactly as Go's `/` does, so a ratio needs a `Cast` on one side first. Each of these is `Binary` under the name, so it is parenthesised, and an engine operator nested inside one keeps its meaning rather than re-associating.

`Concat` is the function form, `concat(a, b, ...)`, rather than the `OpConcat` operator, because `||` is not concatenation in MySQL. It is also the one core construct that asks the dialect for help with typing: `concat` accepts any type, so Postgres cannot infer one for a bare bound parameter among its arguments and refuses the statement, which is a failure the conformance suite found on its first real Postgres run.

## Bound values anywhere

`Param` binds a Go value as an expression, which puts it anywhere an expression is allowed rather than only on the right of a comparison:

```go
expr.Lower(expr.Param("ADA"))   // lower($1), with ADA bound
```

This is what a JSON path with a bound key, a vector distance with a bound vector, or any function with a bound argument needs.

## Nullability is a Go type

The same physical column is non-null under an inner join and nullable under a left join. Nothing about the SQL changes; the Go type does:

```go
optional := expr.Nullable(UserEmail)   // expr.Expr[*string]
```

Binding `optional` to a `*string` compiles; binding it to a `string` does not. `AssertNotNull` is the opposite, and it is a promise the author makes.

## Aggregates and functions

```go
expr.Sum(UserAge)       // sum("u"."age")   → Expr[int]
expr.CountOf(UserID)    // count("u"."id")  → Expr[int64]
expr.Count()            // count(*)
```

The result type follows the input, so summing an `int` column yields `Expr[int]` and binding it to a string does not compile.

The six every engine has are `Count`, `CountOf`, `Sum`, `Avg`, `Min` and `Max`. `Sum`, `Min` and `Max` keep the operand type and `Count` and `CountOf` are `int64`. `Avg` is `float64`, which is a decision rather than an accident: SQL returns numeric for integer input, and scanning numeric into a `float64` loses precision past 2^53. That is acceptable for an average and is stated here rather than discovered.

Each of them takes `AggOption` values, and `Distinct` is the only one the core ships, because `count(DISTINCT x)` is the one modifier all four engines spell the same way:

```go
expr.CountOf(UserID, expr.Distinct())   // count(DISTINCT "u"."id")
```

An option writes into an `AggSpec`, whose other two fields are render hooks the core sets for nobody: `Inner` renders after the arguments and inside the parentheses, and `Suffix` after the closing parenthesis. That is how `pg` adds `FILTER (WHERE ...)` and an `ORDER BY` inside the call without this package learning that either exists. `Aggregate` is the constructor underneath all six and the typed door for an aggregate the core does not name.

For a function the core does not name, `Fn` is the typed escape hatch:

```go
expr.Fn[string]("left", UserEmail, expr.Param(1))   // left("u"."email", $1)
```

The result is still an `Expr[string]`, so it still participates in the checking.

## Conditional values with CASE

`Case` starts a searched CASE and `CaseBuilder` accumulates the branches as a value, so a partially built CASE can be shared as a base. `Else` closes it as `Expr[T]` because the result cannot be null; `End` closes it as `Expr[*T]`, because a CASE with no ELSE is null when no branch matches:

```go
expr.Case[string]().
	When(expr.Greater(UserAge, 30), expr.Param("senior")).
	Else(expr.Param("junior"))
```

```
CASE WHEN "u"."age" > $1 THEN $2 ELSE $3 END
[30 senior junior]
```

## Ordering

An ordering term is an expression plus a direction, which is why `OrderTerm` lives in this package rather than in `stmt` despite `ORDER BY` being a clause. `OrderBy` makes one from any typed expression, and a statement's `Order` and a window's own ordering take the same value:

```go
expr.OrderBy(UserAge, expr.SortDesc)   // "u"."age" DESC
```

`SortDirection` is the type, `SortAsc` and `SortDesc` are its two values, and `SortDirectionValues` is the canonical iteration order over them, so a test that must cover both directions walks that slice instead of keeping its own pair.

A direction outside those two fails `Build` with `ErrUnknownValue`, and so does a `Nulls` outside `NullsFirst` and `NullsLast`. That check exists because a named string type with constants and no validation is not a closed set — the constants are a suggestion and any string reaches the statement text. Ten types in this repository were that shape until 2026-09-24, several of them a live injection, so every closed set here now refuses a value outside its own constants. `NullsOrder` is the type and `NullsOrderValues` its iteration order; it was a bare `string` field until the same day, with no constants at all. An empty `Nulls` is legitimate and means the engine's own default, which differs between them, so it is not an error. `FrameModeValues` does the same for a window's `ROWS` and `RANGE`; `GROUPS` is absent because MySQL 8 has no such frame, which makes it an engine's construct rather than one of this package's.

`OrderTerm` is an open struct rather than an opaque value because two of its four fields are hooks for engines the core does not speak for:

| Field | What it holds |
|---|---|
| `Expr` | The render function for the term's expression. |
| `Direction` | `SortAsc` or `SortDesc`. |
| `Nulls` | Rendered verbatim after the direction, `NULLS LAST` for example. The core names no constant for it, because MySQL has no `NULLS FIRST`. |
| `Using` | Renders `USING op` in place of the direction, which is Postgres's operator-ordering form. |

`OutputCol` names a select-list alias, typed, for the one position where SQL demands an output column name and rejects a table-qualified one — an `ORDER BY` over a set operation:

```go
expr.OrderBy(expr.OutputCol[int]("total"), expr.SortDesc)   // "total" DESC
```

## Window functions

A window function is two halves: the function, and the window it runs over. `WinFn[T]` is the first half, and it is deliberately **not** an `Expr`, so it cannot be selected until `Over` has given it a window. A window function without `OVER` is a SQL error, and the missing type is what makes it a compile error instead.

`RowNumber`, `Rank` and `DenseRank` are the ranking functions every engine has, typed `int64` and never null because a rank always exists for a row. `FirstValue`, `LastValue` and `NthValue` read a value out of the frame and are typed `*T` on purpose: a frame can be empty, and the pointer is what makes the caller handle that instead of scanning a NULL into a value type at run time.

`Window` is a value and every method returns a copy, so a base window declared once can be shared and narrowed without a later caller changing it underneath the first:

```go
byUser := expr.NewWindow().PartitionBy(InvUserID)

rank := expr.Rank().Over(byUser.OrderBy(expr.OrderBy(InvTotal, expr.SortDesc)))
running := expr.Over(expr.Sum(InvTotal),
	byUser.Frame(expr.FrameRows, expr.UnboundedPreceding(), expr.CurrentRow()))
```

```
rank() OVER (PARTITION BY "i"."user_id" ORDER BY "i"."total" DESC)
sum("i"."total") OVER (PARTITION BY "i"."user_id" ROWS BETWEEN UNBOUNDED PRECEDING AND CURRENT ROW)
```

`Over` takes any expression — in practice an aggregate — and turns it into a window call. `OverNamed` attaches a window that the statement declared once and referenced by name, which is how three functions share one specification instead of repeating it.

`NewWinFn` is the escape hatch, and every engine package's window function is built from it:

```go
expr.NewWinFn[float64](func(a *render.Args) string { return "percent_rank()" })
```

### Frames

`Frame(mode, start, end)` writes `mode BETWEEN start AND end`. `FrameMode` has the two values every engine has, `FrameRows` and `FrameRange`: `ROWS` counts physical rows and `RANGE` counts peer groups, so under `RANGE` tied rows share a frame and `CurrentRow` means the whole peer group rather than one row. That is the default frame most people are surprised by. `GROUPS` is Postgres and SQLite only and lives in `pg`.

The bounds are constructors rather than constants, because two of them carry an offset:

| Bound | Renders |
|---|---|
| `UnboundedPreceding()` | `UNBOUNDED PRECEDING`, the first row of the partition |
| `UnboundedFollowing()` | `UNBOUNDED FOLLOWING`, the last row of the partition |
| `CurrentRow()` | `CURRENT ROW` |
| `Preceding(n)` and `Following(n)` | the count, then the keyword |
| `PrecedingExpr(sql)` and `FollowingExpr(sql)` | the expression, then the keyword |

A `FrameBound` offset is written as a literal rather than bound, because Postgres requires a constant in that position and rejects a placeholder. The `*Expr` pair exists for a `RANGE` frame over time, whose offset is an interval rather than a count — `expr.PrecedingExpr("interval '7 days'")` — and its argument is author-written SQL, the same promise `Cast` asks for, never request data.

`Exclude` sets the `EXCLUDE` part of the frame and takes a `FrameExclude`. The mechanism is here and the values are engine constants, because MySQL and ClickHouse have no `EXCLUDE` at all.

`LastValue` carries the one trap worth stating twice: under the default frame it returns the current row rather than the partition's last, so it usually wants an explicit frame ending at `UnboundedFollowing`.

## A subquery as a source

`Source` is anything that can appear after `FROM` or `JOIN`, and `Table` is only its simplest form. `Derived` makes a statement into a source, and `Ref` turns any source back into a `Table` so that columns can be declared against it and stay typed:

```go
t := expr.Derived(inner, "t")
n := expr.Col[int64](expr.Ref(t), "n")   // "t"."n"
```

`Ref` uses the alias as both the name and the alias, which is what a CTE reference needs: used as a `FROM` source it renders `"t" AS "t"`.

`SourceOf` is the escape hatch — a render function and an alias — and it is how the engine packages build a `VALUES` list, a set-returning function source or a `LATERAL` body with no change here.

`QuoteList` renders a list of identifiers comma-separated and quoted for the dialect. It is in this layer because a column list, an `INSERT`'s or a conflict target's, is identifiers rather than expressions, and every layer above wants the same spelling of them.

## The zero Expr

`Expr[T]` is a struct, so `expr.Expr[string]{}` compiles and no constructor ever ran on it. Rendering one records `ErrZeroExpr` on the `Args` and writes nothing, and `Build` then fails with that error naming the constructors that would have made a real expression. Until 2026-09-22 it called a nil render function and panicked inside `Build`, which is a worse answer to the same mistake. Nothing in ordinary use produces one; it is the price of `Expr` being a struct, which the next section explains is worth paying.

## Why Expr is a struct and not an interface

`Expr[T]` is a concrete generic struct, and that is deliberate. An interface can be implemented from outside, and anything that can be implemented from outside can be implemented with the wrong type. Being impossible to satisfy is exactly what makes the type parameter a guarantee rather than a suggestion.

A package that needs an untyped fragment uses [`render.Raw`](../render/README.md#raw-the-way-in-for-an-untyped-fragment) and says so in its signature.

Everything else here is open: `Predicate`, `Source` and `Statement` have no unexported methods, so any package can implement them.
