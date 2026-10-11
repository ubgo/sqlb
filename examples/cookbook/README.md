# cookbook

Twelve recipes, each a complete piece of code and the SQL it renders. And, in the four packages beside them, the one thing this library exists to make possible.

## The four packages

A library that ships its own tables cannot serve the application that embeds it. The application wants the library's rows joined to tables the library has never heard of, in one statement; the library cannot write that join, and the application cannot either unless the library hands over something to join *on*. What it hands over is typed columns.

These four directories are that story, and they are four directories on purpose — the shape is the point.

| Package | Owns | Imports | The shape it demonstrates |
|---|---|---|---|
| [`authlite/`](./authlite) | `auth_user`, `auth_identity` | `sqlb` | A library that owns tables and exports typed columns, a bindings function, and query methods open to extension |
| [`phone/`](./phone) | nothing | `sqlb`, `authlite` | A plugin that borrows someone else's table under its own alias |
| [`workspace/`](./workspace) | `ws_member` | `sqlb` | A second library that owns tables and imports no identity library at all |
| [`app/`](./app) | `app_profile`, `app_subscription` | all three | The application: nobody's dependency, and the only package that knows the whole picture |

`workspace` never learns that `authlite` exists. `authlite` never learns that any of the other three exist. One statement reaches all four.

The walkthrough is [`docs/cross-package.md`](../../docs/cross-package.md). The composite is **executed** against a real database in [`contrib/driver-sqlite/crosspackage_test.go`](../../contrib/driver-sqlite/crosspackage_test.go), which is where the half a rendering test cannot reach is pinned: a LEFT-join miss reported as a miss rather than as an empty value.

## The recipes

`example_test.go`, in order:

| # | Recipe | What it shows |
|---:|---|---|
| 1 | Load a composite user | One statement over four tables from three owners |
| 2 | Extend a library query | Options add a join, a filter and a column; the library's own predicate survives |
| 3 | Switch dialect | The same code renders for SQLite |
| 4 | Ad hoc query | Inline tables, no package-level declarations, still type-checked |
| 5 | Vector search | A pgvector distance as column, filter and sort key |
| 6 | Unsupported dialect | A Postgres construct fails at `Build` under SQLite, naming itself |
| 7 | JSONB | A bound key path and a containment filter that can use a GIN index |
| 8 | Job queue | `DISTINCT ON` and `FOR UPDATE SKIP LOCKED` through the clause slots |
| 9 | ClickHouse latest per user | `FINAL`, `PREWHERE`, `LIMIT BY`, `SETTINGS` |
| 10 | ClickHouse hourly | Typed aggregates with `GROUP BY` |
| 11 | ClickHouse ARRAY JOIN | The clause and the typed element in one call |
| 12 | Hook in transaction | An application write through the library's own `DBTX` |

`plugins_test.go` holds the one piece of behaviour no recipe can print: `Exists` on a nested join result, which is what tells "no row" from "a row with nothing in it".

## Running them

```sh
task cookbook                                              # every recipe, with its SQL
go test ./examples/cookbook -run Example_loadComposite -v   # one recipe
```

Each recipe is a Go `Example`, so `go test` compares its printed SQL with the `// Output:` block. If a recipe and its documentation ever disagree, the recipe is right.
