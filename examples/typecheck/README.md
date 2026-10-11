# typecheck

The one file in this repository that **must not compile**.

`bad.go` sits behind the `typecheck` build tag and holds statements that are wrong in exactly the ways the type system is supposed to catch, each a blank assignment with a comment saying what is wrong. `task typecheck` builds it with that tag and every error reported, and **fails unless each of those statements has a compiler error on its own line**, naming any that compiles. A build that merely fails is not enough: Go stops at ten errors and one error fails the build, so a single failing line used to hide every other line that had quietly started compiling. The script prints how many statements it checked:

```sh
task typecheck
```

## The four classes of promise

**Typed expressions** — eight statements. A column is an `Expr[T]`, so its type follows it everywhere:

```go
_ = sqlb.Bind(name, &n)        // string column into *int
_ = sqlb.Eq(at, "yesterday")   // time column compared with a string
_ = sqlb.EqExpr(name, flag)    // join between string and bool
_ = vector.L2(name, emb)       // distance over a string column
_ = sqlb.Sum(name)             // sum over a non-numeric column
```

**Typestate on writes** — three statements. An incomplete write has no `Build` method at all, so it is not a check that runs, it is a method that is absent:

```go
_, _ = sqlb.NewUpdate(d).Table(t).Set(sqlb.Set(flag, true)).Build()  // UPDATE with no WHERE and no All()
_, _ = sqlb.NewDelete(d).From(t).Build()                             // DELETE with no WHERE and no All()
_, _ = sqlb.NewInsert(d).Into(t).Build()                             // INSERT with no rows, no query, no body
```

**Engine clauses** — six statements. A clause carries both its dialect and its statement kind in its type:

```go
sqlb.NewSelect(lite).Exprs(name).From(t).Clause(pg.ForUpdate())   // Postgres clause on a SQLite statement
sqlb.NewSelect(d).Exprs(name).From(t).Clause(pg.UpdateFrom(t))    // UPDATE clause on a SELECT
sqlb.NewSelect(d).Exprs(name).From(t).Clause(ch.Final())          // ClickHouse clause on a Postgres statement
sqlb.NewSelect(runtime).Exprs(name).From(t).Clause(pg.ForUpdate()) // engine clause on a runtime-chosen dialect
```

**Capabilities** — two statements. `RETURNING` is a type constraint on the dialect, so an engine that lacks it has no such call to write:

```go
sqlb.InsertReturning(sqlb.NewInsert(sqlb.ClickHouseDialect{})… )  // ClickHouse has no RETURNING
sqlb.InsertReturning(sqlb.NewInsert(runtime)… )                   // capability unknown at compile time
```

## Why this exists

A claim about a type system that no test can break is not worth printing. "Comparing a column with the wrong type is a compile error" is exactly such a claim: every ordinary test in the repository is code that *does* compile, so none of them can tell you whether the wrong code would have been rejected. This file is the only thing here that can.

It is also why the build tag matters. Without it the file would break every ordinary build, so it would be deleted within a week and the guarantee would quietly become prose again.

## Adding to it

When a new construct makes a new class of mistake impossible, add the mistake here. The file is a list of promises, and each line is one the compiler keeps. If a line ever starts compiling, either the construct got looser or the promise was never true — both are findings, and the gate is what turns them into a failed build instead of a surprise.

The honest residue is written down rather than hidden: an engine **expression** is not in this file, because it cannot be. `vector.L2` under the SQLite dialect compiles and fails at `Build` with `ErrUnsupportedDialect` naming the construct. Two of its statements pin exactly that boundary by using a runtime-chosen dialect, where the compiler has nothing left to check.
