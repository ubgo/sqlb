# Dialects

A dialect is the smallest thing that differs between engines, and it is deliberately tiny.

```go
type Dialect interface {
	Placeholder(n int) string
	QuoteIdent(ident string) string
	Name() string
}
```

`PostgresDialect{}` renders `$1` and quotes with `"`. `SQLiteDialect{}` renders `?` and quotes with `"`. That is most of the difference between them, and keeping the interface this small is what stops the dialect from becoming the place where engine behaviour accumulates.

## Capabilities are optional interfaces

An engine that has more than the minimum says so by satisfying another interface:

| Interface | Question it answers |
|---|---|
| `Returning` | does `INSERT ... RETURNING` exist |
| `InsertAliasing` | may an INSERT target take an alias |
| `Named` | does the engine take named parameters |

A builder asks with a type assertion, so a dialect that lacks a capability is never forced to lie about it, and adding a capability does not break the dialects that do not have it. `Insert.Returning` on a dialect without `Returning` fails with `ErrNoReturning`, which names the two-statement fallback.

## Args

`Args` accumulates the bound values while a statement renders. It carries the dialect, so an expression can bind a parameter at render time rather than only at construction. That is what makes a JSON path with a bound key, a vector distance with a bound vector, or a `date_trunc` with a bound unit expressible at all.

It also carries the first error. `Unsupported` records a construct the dialect cannot render, `Fail` records anything else, and `Build` returns whichever came first. Rendering never panics and never produces half a statement.

## Failing closed

Every construct in an engine package guards itself:

```go
func Similar(a, b sqlb.Expr[string]) sqlb.Predicate {
	return pg.BinPred("trgm.Similar", "%", a, b)
}
```

`pg.BinPred` renders only under Postgres and records `trgm.Similar` as unsupported otherwise. The result is that a multi-engine application discovers its mistake at `Build`, in its own process, with the construct named:

```
sqlb: construct is not supported by this dialect
trgm.Similar under dialect "sqlite"
```

This is enforced, not documented. Every engine sub-package has a test that builds each of its *expressions* under `SQLiteDialect{}` and asserts `ErrUnsupportedDialect`. A helper that forgets its guard fails that test.

Engine **clauses** do not get that far. A clause is typed to its dialect and its statement kind — `pg.ForUpdate()` is a `pg.SelectClause`, an alias for `sqlb.Clause[postgres.Dialect, sqlb.SelectKind]` — and a statement accepts only clauses of its own dialect and kind, so `sqlb.NewSelect(sqlite.Dialect{}).Clause(pg.ForUpdate())` does not compile, and neither does `pg.UpdateFrom` on a SELECT. The negative build in `examples/typecheck` holds those cases. The `Build`-time guard stays underneath as belt-and-braces; nothing reaches it through the types.

## Adding a dialect from another repository

A community package can ship a complete engine: a dialect, its constructs, and a driver. Nothing in this module is privileged, no list here needs editing, and the engine name is a plain string rather than a member of a closed set. The constants in `dialect.go` name the three engines this repository ships; the core never branches on them, and `DialectName` asks the `Named` interface, so any name works.

`engine_test.go` in the core is the proof. It builds a DuckDB engine in package `sqlb_test`, which can reach nothing unexported, so whatever it does is what an outside package can do.

**The dialect.** Three methods, plus a capability interface for anything more.

```go
const engine = "duckdb"

type Dialect struct{}

func (Dialect) Placeholder(int) string      { return "?" }
func (Dialect) QuoteIdent(id string) string { return sqlb.Quote(id, '"') }
func (Dialect) Name() string                { return engine }
func (Dialect) SupportsReturning() bool     { return true }
```

**The constructs.** The guard rule lives in the core, so an engine package is one-line wrappers over it rather than a copy of the logic. `pg`, `sqlite` and `ch` are written exactly this way.

```go
func expr[T any](c string, r func(*sqlb.Args) string) sqlb.Expr[T] {
	return sqlb.EngineExpr[T](engine, c, r)
}

// ListValue renders list_value(items...), which only this engine has.
func ListValue[T any](items ...sqlb.Expr[T]) sqlb.Expr[[]T] { ... }
```

The full set: `EngineGuard`, `EngineExpr`, `EnginePred`, `EngineBinOp`, `EngineBinPred`, `EngineFn`, `EngineLit`, `EngineRaw`, `EngineClause`, `EngineSource`, plus `EngineList` and `EngineQuoteLit` for assembling fragments. Every one of them fails closed under another dialect with the construct named, which is the guarantee the packages in this module have and the reason to use these rather than roll your own.

**The driver.** `DBTX` is an interface and `sqlbtest` is an ordinary importable package, so a community driver runs the same conformance suite the in-repo one does. See [conformance.md](./conformance.md).

## Slots

A clause renders into a slot, which is a position in the statement grammar. `SlotValues` lists them and `Slot.String` names them.

**The set is complete, not minimal.** There is a slot at every boundary the renderer emits, so every position in a statement has a name and an outside package never needs a change here to place a clause. That is a deliberate departure from the rule that no slot ships ahead of a clause: a slot is the one extension point a community package cannot add for itself, since a position only means something if the renderer writes it, and completeness is what keeps an outside engine package first class. Several slots therefore have no clause in this module, and each says so in its doc comment.

The eleven positions in a SELECT:

| Slot | Between | Used by |
|---|---|---|
| `SlotAfterSelect` | SELECT and the column list | `DISTINCT ON`, `TOP` |
| `SlotAfterColumns` | the column list and FROM | T-SQL `INTO`; nothing here |
| `SlotAfterFrom` | the driving table and the joins | `FINAL`, `SAMPLE`, `TABLESAMPLE`, `ARRAY JOIN` |
| `SlotBeforeWhere` | the joins and WHERE | `PREWHERE` |
| `SlotAfterWhere` | WHERE and GROUP BY | nothing here |
| `SlotAfterGroupBy` | the GROUP BY list and HAVING | MySQL `WITH ROLLUP` |
| `SlotAfterHaving` | HAVING and WINDOW | nothing here |
| `SlotBeforeOrder` | WINDOW and ORDER BY | `QUALIFY` |
| `SlotAfterOrder` | ORDER BY and LIMIT | `LIMIT n BY` |
| `SlotAfterOffset` | OFFSET and the tail | `FETCH FIRST` |
| `SlotTail` | last | `FOR UPDATE`, `SETTINGS`, `ON CONFLICT` |

The write statements are covered the same way, through `SlotAfterInsert`, `SlotAfterVerb`, `SlotAfterTarget`, `SlotBeforeValues`, `SlotInsertBody`, `SlotAfterSet`, `SlotAfterWhere` and `SlotTail`.

One position is not a slot, because there is one of it per join rather than one per statement: the place a per-table hint goes, such as SQLite's `INDEXED BY`, MySQL's `USE INDEX` or T-SQL's `WITH (NOLOCK)`. `Join.Clauses` carries those. They are ordinary `Clause` values and keep their dialect guard; their `Slot` is not consulted, because the join itself is the position.

**Which statement a clause belongs to.** A statement can contain others, and the answer is not always the obvious one. A clause on a CTE body, a recursive branch or a derived table stays inside it. A set operation is the surprise: `Union` returns its receiver, so a clause on the receiver applies to the whole compound exactly as its `ORDER BY` and `LIMIT` do, while a clause on an argument branch stays inside that branch's parentheses. `TestSlotScope_InCompoundStatements` pins all six cases.

**A clause in a slot the statement does not render is an error, not an omission.** `Build` returns `ErrUnknownSlot` naming the slot. This used to be silent: the clause vanished and the statement built cleanly without it, which is the exact failure this library exists to prevent.

Three tests hold the line. One walks `SlotValues` and asserts every slot is either rendered by some statement or rejected, so a slot added later cannot be forgotten. Two more put a marker in every SELECT and write-statement slot and assert they come out in grammar order, which fails if a position is unreachable or lands in the wrong place.

## Adding a dialect