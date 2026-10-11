# `stmt` — statements, clauses and Query

```go
import "github.com/ubgo/sqlb/stmt"
```

Assembles SQL statements. It knows the grammar and nothing about meaning: a `Predicate` is something that can render itself, and this package writes it after `WHERE` without knowing what it compares.

That is what lets an engine package add a construct, and an outside package add a whole statement kind, with no change here.

This is also the highest layer still purely about text. A `Query` is a string and a slice of values; how those values come back into Go is the concern of [`bind`](../bind/README.md) and [`scan`](../scan/README.md).

## Contents

- [Setup](#setup)
- [SELECT](#select)
- [A builder method returns a new statement](#a-builder-method-returns-a-new-statement)
- [A statement carries its dialect in its type](#a-statement-carries-its-dialect-in-its-type)
- [Joining across a package boundary](#joining-across-a-package-boundary)
- [Grouping and aggregation](#grouping-and-aggregation)
- [Set operations](#set-operations)
- [Subqueries](#subqueries)
- [Writes](#writes)
- [Options: extending a statement a library built](#options-extending-a-statement-a-library-built)
- [Query](#query)
- [Slots: adding a clause from outside](#slots-adding-a-clause-from-outside)

## SELECT

`Exprs` names select items with no scan destination, which is what a subquery, a CTE body, or a caller that scans on its own needs.

```go
q, err := stmt.NewSelect(postgres.Dialect{}).
	Exprs(UserEmail, UserAge).
	From(Users).
	Where(expr.Greater(UserAge, 30)).
	Order(expr.OrderBy(UserAge, expr.SortDesc)).
	Limit(10).
	Build()
```

```
SELECT "u"."email", "u"."age" FROM "app_user" AS "u" WHERE ("u"."age" > $1) ORDER BY "u"."age" DESC LIMIT $2
[30 10]
```

Note that `LIMIT` binds its value rather than inlining it. Every value this package emits is a bound parameter, without exception.

Use `Columns` instead of `Exprs` when you want the query to carry its own scan destinations; see [`bind`](../bind/README.md).

### A builder method returns a new statement

Every method here copies: it derives a statement with one more clause and leaves the one you called it on alone. That is what makes a partially built statement safe to hand around and to derive from.

```go
base := stmt.NewSelect(postgres.Dialect{}).Exprs(UserEmail).From(Users)

adults := base.Where(expr.Greater(UserAge, 18))
seniors := base.Where(expr.Greater(UserAge, 65))
```

`base` is still the unfiltered query, and `adults` and `seniors` are independent. This was not always true — methods used to mutate the receiver, and `Clone` was the mandatory fix — so code written against the old shape that called a method for its effect and discarded the result now builds a statement without that clause. The compiler cannot catch that, so it is worth a grep for a line that is just `sel.Where(...)`.

`Clone` still exists and means one specific thing: **start a statement that does not share the execution guard**. A derived statement keeps it, because it holds the same bindings — pointers into the caller's variables — so two queries derived from one base must not execute at once. A clone is a separate statement and is not refused because its parent is running.

The cost is a copy of the statement's slices per method call. For any statement written by hand this is not measurable; it buys a guarantee that used to be a convention.

### A statement carries its dialect in its type

`NewSelect(postgres.Dialect{})` returns `*Select[postgres.Dialect]`; the type argument is inferred from the value and never written. It is what lets the compiler check an engine clause: a `*Select[sqlite.Dialect]` has no way to accept a `pg.SelectClause`.

```go
stmt.NewSelect(postgres.Dialect{}).Exprs(UserEmail).From(Users).Clause(pg.ForUpdate())   // ✓
stmt.NewSelect(sqlite.Dialect{}).Exprs(UserEmail).From(Users).Clause(pg.ForUpdate())     // does not compile
```

A dialect chosen at runtime — a `render.Dialect` interface value from configuration — gives `Select[render.Dialect]`, named `AnySelect`, which accepts every universal construct and no engine clause, because no engine clause is a `Clause[render.Dialect, SelectKind]`. That is the portability rule as a type: a library that must run on whatever engine its consumer chose builds an `AnySelect` and cannot accidentally use `pg`. To use an engine clause, narrow the dialect with a type switch first. Hold the concrete type where the engine is known — `var d render.Dialect = postgres.Dialect{}` refuses Postgres clauses, correctly, because the type says the engine is unknown.

Where a type is named in code, the engine packages provide the spelling: `*pg.SelectQuery`, `*sqlite.FilteredUpdate`, or `*stmt.AnySelect`.

## Setup

Every snippet on this page uses these declarations: one table owned by a library, one owned by the application, and their typed columns.

```go
import (
	"fmt"

	"github.com/ubgo/sqlb/dialect/postgres"
	"github.com/ubgo/sqlb/expr"
	"github.com/ubgo/sqlb/stmt"
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

// show prints a built statement. It is where the output under each snippet
// comes from.
func show(q stmt.Query, err error) {
	if err != nil {
		fmt.Println("error:", err)
		return
	}
	fmt.Println(q.SQL)
	if len(q.Args) > 0 {
		fmt.Println(q.Args)
	}
}

// Every snippet below renders for one engine.
var d = postgres.Dialect{}
```

## Joining across a package boundary

This is the case the library exists for. Two packages declare their own tables, and a third joins them with the compiler checking the condition.

```go
stmt.NewSelect(postgres.Dialect{}).
	Exprs(UserEmail, InvTotal).
	From(Users).
	Join(stmt.Join{
		Type:   stmt.JoinInner,
		Table:  Invoices,
		Source: Invoices,
		On:     []expr.Predicate{expr.EqExpr(UserID, InvUserID)},
	}).
	Build()
```

```
SELECT "u"."email", "i"."total" FROM "app_user" AS "u" INNER JOIN "invoice" AS "i" ON ("u"."id" = "i"."user_id")
```

`Join` also carries a `Clauses` field, for a hint that binds to that one table reference rather than to the statement — `INDEXED BY`, `TABLESAMPLE`, ClickHouse `FINAL`. On the driving table such a clause goes in the statement; on a joined table it goes in that join. Both are legal SQL, so the wrong one does not fail, it answers a different question.

`JoinType` is a string type with four named values — `JoinInner`, `JoinLeft`, `JoinRight` and `JoinCross` — and `JoinTypeValues` is the canonical iteration order over them, which is what a test covering every join shape walks instead of keeping its own list. A plugin's table is either required or optional, so those four are what ships; every extra join type is another shape the composite-query tests must cover.

`JoinType` takes a string on purpose, which is the documented escape hatch. `FULL OUTER JOIN` has no constant because MySQL lacks it, and it is reachable as `stmt.JoinType("FULL OUTER JOIN")` — a caller who writes the keyword out has said what they mean. NATURAL joins are absent in every form and will stay absent: a natural join silently changes which columns it matches on when somebody adds a column, so a query that was correct yesterday returns different rows today with nothing in the diff to show why.

## Grouping and aggregation

```go
stmt.NewSelect(postgres.Dialect{}).
	Exprs(InvUserID, expr.Sum(InvTotal)).
	From(Invoices).
	GroupBy(InvUserID).
	Having(expr.Greater(expr.Sum(InvTotal), 100)).
	Build()
```

```
SELECT "i"."user_id", sum("i"."total") FROM "invoice" AS "i" GROUP BY "i"."user_id" HAVING (sum("i"."total") > $1)
[100]
```

## Set operations

`Union`, `UnionAll`, `Intersect` and `Except` combine two selects, and `SetOp` takes the keyword as a value for the forms the core does not name — `INTERSECT ALL` and `EXCEPT ALL`, which have no constant because SQLite lacks them.

```go
stmt.NewSelect(d).Exprs(UserEmail).From(Users).
	Union(stmt.NewSelect(d).Exprs(UserEmail).From(Users).Where(expr.IsNull(UserAge))).
	Build()
```

Two things about the rendering are decisions rather than accidents, and both came out of running the conformance suite on real engines rather than reading a grammar.

A branch is **not** parenthesised unless it needs to be. SQLite rejects a parenthesised branch outright, so a plain branch renders bare; a branch that is itself a set operation, or carries its own `ORDER BY` or `LIMIT`, renders as `SELECT * FROM (branch) AS "sqlb_setop"` instead, because the operators would otherwise reassociate and `A EXCEPT (B UNION C)` is not `A EXCEPT B UNION C`. A recursive step is always bare, since Postgres refuses a recursive reference inside a subquery.

Branches of different widths fail `Build` with `ErrSetOpArity`, naming both widths, rather than reaching the server. That is worth more than it looks: `scan.All` and the binding path append the *reader's* columns to the statement they are handed, which is the left branch, so a left branch that also declares a select list of its own ends up one column wider than the right. The engine would then report a mismatch against a query the caller never typed. A zero-width branch is skipped rather than compared, because a width this package does not know is not a width that disagrees.

```go
q, err := stmt.NewSelect(d).Exprs(TenantID, Amount).From(Paid).
	Except(stmt.NewSelect(d).Exprs(FailedTenant).From(Failed)).
	Build()
// err is stmt.ErrSetOpArity: set operation branches select different numbers of columns: 2 and 1
```

## Subqueries

A `Select` is usable in four positions besides `FROM`, and each one is a function rather than a method because the outer construct decides the shape.

`Subquery` uses it as a scalar expression. The author states the Go type, and the result is `Expr[*T]` because a subquery that returns no row yields NULL:

```go
stmt.Subquery[int](inner)   // (SELECT ...)
```

`Exists` and `NotExists` ask whether the subquery returns anything. `NotExists` is the anti-join, and it is the one to reach for: it is usually faster than `NOT IN`, and unlike `NOT IN` it is not defeated by a NULL in the subquery's result.

`InSubquery` and `NotInSubquery` are the membership forms:

```go
stmt.NewSelect(d).
	Exprs(UserEmail).
	From(Users).
	Where(stmt.InSubquery(UserID, inner)).
	Build()
```

```
SELECT "u"."email" FROM "app_user" AS "u" WHERE "u"."id" IN (SELECT ...)
```

The subquery must select one item whose type is `T`. The compiler cannot check that across the `Select`, so the signature states it and the database checks it — one of the few places in this module where the promise is a convention rather than a type. `NotInSubquery` carries the NULL rule with it: if the subquery returns a single NULL the whole predicate is NULL and matches nothing, which `NotExists` does not do.

All four are built on `SubqueryCompare`, which renders `e op [quantifier] (SELECT ...)` and is what an engine package's `ANY` and `ALL` forms use. It takes a plain `SQLer` on the left, so a row constructor works there too and a composite key can be tested against a subquery in one comparison.

## Writes

```go
stmt.NewInsert(postgres.Dialect{}).
	Into(Users).
	Row(stmt.Set(UserID, "u1"), stmt.Set(UserEmail, "ada@example.com")).
	Build()
```

```
INSERT INTO "app_user" AS "u" ("id", "email") VALUES ($1, $2)
[u1 ada@example.com]
```

```go
stmt.NewUpdate(postgres.Dialect{}).
	Table(Users).
	Set(stmt.Set(UserEmail, "new@example.com")).
	Where(expr.Eq(UserID, "u1")).
	Build()
```

```
UPDATE "app_user" AS "u" SET "email" = $1 WHERE ("u"."id" = $2)
[new@example.com u1]
```

```go
stmt.NewDelete(postgres.Dialect{}).From(Users).Where(expr.Eq(UserID, "u1")).Build()
```

```
DELETE FROM "app_user" AS "u" WHERE ("u"."id" = $1)
[u1]
```

Each item passed to `Row` or `Set` is an `Assignment`, a column paired with the value or expression going into it. `stmt.Set` binds a value of the column's Go type, `SetExpr` assigns an expression of that type — `SET n = n + 1` — and `AssignTarget` is the engine door: it assigns to an expression over a column, a JSONB subscript such as `doc['customer']['tier']` or an array element, keeping the base column name for the `INSERT` column list and for error messages. `UPDATE` accepts a target; `INSERT` does not, because a column list needs names.

Every row of a multi-row insert must be the same width as the first, since the column list is taken from it. A row that is not fails `Build` with `ErrRowWidth` rather than reaching the server as a column-count error about SQL the caller never wrote.

`Row` can be called repeatedly for a multi-row insert. `RETURNING` is a capability of the dialect, so it is added by `InsertReturning`, `UpdateReturning` and `DeleteReturning`, three functions whose dialect parameter is constrained to `ReturningDialect` — a dialect with `SupportsReturning`. `D` is inferred from the statement, and a dialect without the method makes the call a compile error rather than `ErrNoReturning` at `Build`; `ErrNoReturning` remains only for a statement built as a struct literal. `pg.InsertReturning` and its siblings are the same functions instantiated for the engine.

### A write cannot be built until it is complete

The three write builders come in two forms each, and only the second has `Build`:

| Started by | Completed by | Complete type |
|---|---|---|
| `NewInsert` → `Insert` | `Row`, `Select`, or `Body` for an engine clause that supplies the whole body such as `pg.DefaultValues` | `InsertReady` |
| `NewUpdate` → `Update` | `Where`, or `All` | `FilteredUpdate` |
| `NewDelete` → `Delete` | `Where`, or `All` | `FilteredDelete` |

So an `UPDATE` with no `WHERE` does not compile into existence — `stmt.NewUpdate(d).Table(Users).Set(...).Build()` is a type error, because `*Update` has no `Build`. The whole-table case is written down with `All`, which is what makes it deliberate rather than an omission:

```go
stmt.NewUpdate(postgres.Dialect{}).Table(Users).Set(stmt.Set(UserEmail, "x")).All().Build()
```

The complete types accept more of what they already have — another `Row`, another `Where`, `Set`, `Clause`, `With`, `Returning` — so a statement is still built incrementally; the type just changes at the call that completes it. A struct literal of a complete type has `Build` and was never completed; it is empty and `Build` rejects it with `ErrNoWhere` or `ErrNoAssignments`, which is the documented literal hole and the reason those errors still exist.

## Options: extending a statement a library built

An `Option` is how code that a library has never heard of adds to a statement the library assembled. The library applies the options and keeps ownership of `Build`, so its own mandatory predicates cannot be removed: options only add.

```go
func (s *Store) Recent(opts ...stmt.Option) (stmt.Query, error) {
	return s.base().Apply(opts...).Build()
}
```

Four options ship, one per thing a caller can add: `WithJoin` adds joins, `WithWhere` adds predicates that are ANDed with the library's own, `WithColumns` adds bindings *after* the library's so its scan targets keep their positions, and `WithOrder` adds ordering terms after the library's. `Apply` runs them in order against a derived copy and returns it, leaving the receiver alone like every other method here.

`Option` is `func(*selectCore)` and carries no dialect type parameter, which is deliberate and was verified before it was built. Go infers a type parameter from a function's arguments and never from where the result is used, so an `Option[D]` returned by `WithWhere(p)` could not have inferred `D`, and every caller would have had to write it out — on the one seam that exists for callers. Editing the universal core sidesteps that: a clause carries a dialect and an option does not, so clauses are added with `Clause` on the statement where the compiler checks them, and there is no `WithClause`.

## Query

`Build` returns a `Query`:

```go
type Query struct {
	SQL     string
	Args    []any
	Targets []any
}
```

`Targets` holds the caller's own pointers, in select-list order. Not copies: `Targets[0]` **is** the `&email` you passed to `Bind`, and `exec.One` hands the slice straight to `rows.Scan`, which is how your variables get filled.

```go
var email string
var age int

q, _ := stmt.NewSelect(d).
	Columns(bind.Bind(UserEmail, &email), bind.Bind(UserAge, &age)).
	From(Users).Build()
```

```
2 targets
[0] is &email: true
[1] is &age:   true
```

Two things follow from that, and both are consequences of the same fact rather than separate rules. The order is select-list order, so inserting a column in the middle cannot shift a scan, because there is no second list to keep in step with it. And the query **holds your memory**, which is why it is not safe to share between goroutines; see [`bind`](../bind/README.md#not-safe-for-concurrent-use).

A query built from `Exprs` carries no destinations at all:

```
from Exprs: 0 targets
```

Running that through the binding helpers returns `ErrNoTargets`.

`Query` also carries an execution guard, reachable as `Acquire` and `Release`, which [`exec`](../exec/README.md) uses to refuse an overlapping execution.

A statement kind written outside this package builds its `Query` with `NewQuery`, or, if it captures scan targets, with `NewGuardedQuery` and a `Guard` it owns. The builder creates the guard once in its constructor and copies it into every statement derived from it, because derived statements share the caller's target pointers and two Queries from one family running at once write into the same variables. `pg.Merge` is the worked example: its `RETURNING` captures targets, so a MERGE and every MERGE derived from it share one guard, and `ErrQueryInUse` fires when two of their Queries overlap.

```go
func NewMyStatement() *MyStatement { return &MyStatement{guard: stmt.NewGuard()} }

func (m *MyStatement) Build() (stmt.Query, error) {
	// ... render sql, args and targets ...
	return stmt.NewGuardedQuery(m.guard, sql, args, targets), nil
}
```

The zero `Guard` detects nothing, like a `Query` struct literal; a builder that captures targets has no reason to leave it zero.

## Slots: adding a clause from outside

A `Slot` is a position in the rendered statement. An outside package cannot invent one, because a position only exists if the renderer writes it, so the slot set here is deliberately **complete rather than minimal** — there is a slot at every grammar boundary, and several have no clause in this repository at all.

```go
type Clause[D render.Dialect, K any] interface {
	AnyClause              // Slot() Slot; WriteSQL(b *strings.Builder, a *Args)
	ClauseOf(D, K)         // a marker: does nothing, its signature types the clause
}
```

A clause is typed to the dialect `D` it belongs to and the statement kind `K` it may appear in — `SelectKind`, `InsertKind`, `UpdateKind`, `DeleteKind`. `Select[D].Clause` accepts `Clause[D, SelectKind]` and nothing else, so an engine's clause on another engine's statement, or an UPDATE clause on a SELECT, is a compile error. The marker method is what makes the two parameters bite: an interface whose parameters appeared in no method would be satisfied by every clause for every instantiation. Implement the three methods, name a slot, and your clause renders in the right place with no change to this package; or call `EngineClause[D, K]`, which writes the marker for you and adds the `Build`-time guard.

A `Join` carries `AnyClause` values for its per-table hints, because a join literal cannot infer a dialect; those stay guarded at `Build`.

Here is a whole clause `sqlb` does not have, written the way a package in another repository would write it. `QUALIFY` filters on a window function, and DuckDB, Snowflake and BigQuery have it:

```go
type qualify struct{ cond expr.Predicate }

func (qualify) Slot() stmt.Slot { return stmt.SlotBeforeOrder }

// The marker types the clause to an engine and a statement kind.
func (qualify) ClauseOf(postgres.Dialect, stmt.SelectKind) {}

// No leading space: the renderer supplies the separator before calling this,
// and a clause that adds its own produces a double space.
func (q qualify) WriteSQL(b *strings.Builder, a *render.Args) {
	b.WriteString("QUALIFY ")
	q.cond.WriteSQL(b, a)
}

func Qualify(cond expr.Predicate) stmt.Clause[postgres.Dialect, stmt.SelectKind] { return qualify{cond} }
```

Attach it with `Clause`:

```go
stmt.NewSelect(d).
	Exprs(userID).
	From(events).
	Where(expr.Greater(at, 100)).
	Clause(Qualify(expr.Eq(expr.Fn[int]("row_number"), 1))).
	Order(expr.OrderBy(at, expr.SortDesc)).
	Build()
```

```
SELECT "e"."user_id" FROM "event" AS "e" WHERE ("e"."at" > $1) QUALIFY row_number() = $2 ORDER BY "e"."at" DESC
[100 1]
```

It landed between `WHERE` and `ORDER BY`, where `SlotBeforeOrder` sits, and its bound value was numbered `$2` in sequence with the rest. That is the entire implementation, in someone else's module.

`SlotValues` is the canonical list of all seventeen; `TestEverySlot_IsRenderedOrRejected` walks it and fails if a slot is neither rendered nor rejected.

That is how `pg.DistinctOn`, `ch.Prewhere` and `sqlite.IndexedBy` work, and how yours would. Nothing about them is privileged.
