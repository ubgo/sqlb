# sqlb documentation

`sqlb` is a typed SQL layer for Go libraries that own database tables and cannot depend on an ORM. The [repository README](../README.md) is the short version; these documents are the long one, in reading order.

| Document | What it answers |
|---|---|
| [cross-package.md](./cross-package.md) | The problem the library exists for: four packages that do not import each other, composed into one statement, and the three ownership shapes. |
| [switching-databases.md](./switching-databases.md) | Moving a library or application between Postgres, MySQL and SQLite by changing one dialect value: what engines disagree on, how each difference is handled, and how the same rows everywhere is proven. |
| [core-builder.md](./core-builder.md) | What the core gives you: tables, typed columns, predicates, statements, and how a query carries its own scan targets. |
| [dialects.md](./dialects.md) | How a statement renders per engine, what a dialect decides, and how a construct fails closed under the wrong one. |
| [engines.md](./engines.md) | The `pg`, `sqlite`, `mysql` and `ch` packages, which engines are real, and the rule that decides what may live in the core. |
| [extensions.md](./extensions.md) | The extension sub-packages, and how to add a Postgres extension without editing `pg`. |
| [execution.md](./execution.md) | `DBTX`, `One`, `Each`, and why nothing is passed for the destination. |
| [conformance.md](./conformance.md) | The shared suite in `sqlbtest`, what each case proves, and how to run it against your engine. |
| [examples.md](./examples.md) | The compile-checked catalogues under `examples/`, and what each one covers. |

## One page per package

The documents above are by topic. These are by package, one for each layer, with the snippets you would actually write. Every snippet is copied from `examples/packages/`, where it is a runnable Go Example whose output `go test` checks, so nothing here is merely plausible.

| Package | What it is | Read it to learn |
|---|---|---|
| [render](./render/README.md) | placeholders, quoting, bound values | how a value becomes a parameter, and how to write a dialect |
| [dialect](./dialect/README.md) | one package per engine | what a dialect decides, and how a capability is declined rather than denied |
| [expr](./expr/README.md) | typed columns and predicates | why a column is a value, and what will not compile |
| [stmt](./stmt/README.md) | SELECT, INSERT, UPDATE, DELETE | building a statement, joining across a package boundary, adding a clause |
| [exec](./exec/README.md) | running a statement | `DBTX`, `One`, `Each`, and the errors worth knowing |
| [bind](./bind/README.md) | results through caller pointers | the shorter strategy, and the concurrency hazard it carries |
| [scan](./scan/README.md) | results as values | the strategy without that hazard, and why `Map2` is the only combinator |
| [schema](./schema/README.md) | what tables exist | declaring tables so two packages can be migrated together |
| [ddl](./ddl/README.md) | CREATE, ALTER and DROP TABLE | creating a library's tables from the columns its queries already use |
| [ddl: not built](./ddl/not-built.md) | everything the DDL does not build | the raw SQL for every missing statement on each engine, the known limits, and future work |

And one page per engine construct package, which are by far the largest:

| Package | Names | Read it to learn |
|---|---:|---|
| [pg](./pg/README.md) | 978 | starting a Postgres statement, arrays, JSONB, ranges, full-text search, locking, and the eight extension sub-packages |
| [sqlite](./sqlite/README.md) | 214 | starting a SQLite statement, JSON without a JSON type, the two upsert mechanisms, FTS5, and the connection-scoped functions |
| [mysql](./mysql/README.md) | 170 | starting a MySQL statement, the upsert with its row alias, locking reads, full-text search, and MySQL's DDL |
| [ch](./ch/README.md) | 25 | `FINAL`, `LIMIT BY`, conditional aggregates, `SETTINGS` |

Each ends with a complete reference table generated from the source, so no construct is discoverable only by reading the code.

Read them in that order and you have read the stack bottom to top.

## The three ideas

**A column is a typed value.** `sqlb.Col[string](Users, "email")` is an `Expr[string]`. Comparing it with a number does not compile, binding it to an `*int` does not compile, and joining it to a `bool` column does not compile. A library exports these values, and the application composing them gets the same checking the library had.

**A query carries its scan targets.** `Bind(col, &dst)` pairs an expression with the pointer it scans into. `Build` collects them in SELECT order, and the execution helpers scan through them. There is no second list of destinations to keep in step with the column list.

**The core holds only universal SQL.** A construct lives in the core when Postgres, MySQL 8, SQLite and ClickHouse all accept it with the same syntax and meaning. Everything else lives in an engine package and fails at `Build` under another dialect, with the construct named. Postgres, SQLite and MySQL are checked by execution: the shared conformance suite runs on each of them. ClickHouse is the quarter still checked by reading its documentation, since nothing here has run against a ClickHouse server.
