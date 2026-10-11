# pgsurface

The Postgres surface, area by area. `pg` is by far the largest package in the module — around five hundred exported names to the core's two hundred and sixty — and this is where that surface is exercised as code rather than listed as a table.

## The files

| File | What it covers |
|---|---|
| `expr_test.go` | The expression surface: operators, arrays, JSONB, ranges, text and pattern matching, full-text search, the type casts |
| `select_test.go` | Statements: `DISTINCT ON`, window functions, CTEs including recursive ones, set operations, `LATERAL`, locking clauses |
| `write_test.go` | Writes: `ON CONFLICT` in both forms, `MERGE`, `RETURNING`, data-modifying CTEs, `DEFAULT VALUES` |
| `schema_test.go` | The tables the other three files build against |

89 examples in total. The sub-directories go further:

| Directory | What it is |
|---|---|
| [`advancedsql/`](./advancedsql) | An external catalogue of advanced PostgreSQL queries, translated section by section — 53 examples |
| [`snippets/`](./snippets) | A 21-section catalogue of advanced Postgres snippets, in the original order — 159 examples |
| [`periodcompare/`](./periodcompare) | One worked report end to end: a multi-source period comparison — 5 examples |

```sh
task surface                       # every example, with its SQL
go test ./examples/pgsurface/...   # all of it
```

## What this proves, and what it does not

It proves the **text**. Each example is a Go `Example` whose `// Output:` block is the rendered SQL, so a change in rendering fails the build.

It does not prove the **SQL**. Nothing here opens a database — the root module has no driver and cannot have one. That distinction is not academic: running these constructs against a real Postgres 17 from [`contrib/driver-pgx`](../../contrib/driver-pgx) found eleven whose rendering tests passed and whose SQL Postgres rejects, including bare parameters the server cannot type inside `ARRAY[]` and `jsonb_build_object`, and `->` with a text parameter silently taking the key form. A `pg` construct is not finished until it has an entry in that driver's catalogue.
