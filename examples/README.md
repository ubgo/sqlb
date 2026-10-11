# examples

Code that exists to be read, and to prove claims that prose cannot.

Nothing here is a toy. Every statement in this tree is built by a test whose expected SQL is written into the test, so a change in rendering fails the build rather than quietly changing what the documentation says. When a page in [`docs/`](../docs/README.md) shows a snippet, this is usually where it came from.

## The tree

| Directory | What it is | Size |
|---|---|---:|
| [`cookbook/`](./cookbook) | Twelve recipes, and the four packages that tell the cross-package story the library exists for | 12 examples |
| [`packages/`](./packages) | One file per layer — `render`, `expr`, `stmt`, `exec`, results, `schema` — the source of the per-package documentation snippets | 43 examples |
| [`typecheck/`](./typecheck) | The one file in the repository that must **not** compile | 8 statements |
| [`pgsurface/`](./pgsurface) | The Postgres surface, area by area | 89 examples |
| [`pgsurface/advancedsql/`](./pgsurface/advancedsql) | An external catalogue of advanced PostgreSQL queries, translated section by section | 53 examples |
| [`pgsurface/snippets/`](./pgsurface/snippets) | A 21-section catalogue of advanced Postgres snippets, in the original order | 159 examples |
| [`pgsurface/periodcompare/`](./pgsurface/periodcompare) | One worked report: a multi-source period comparison, the hardest shape a reporting API has | 5 examples |

The pgvector README is translated query by query in [`pg/vector/example_test.go`](../pg/vector/example_test.go), which lives beside the package it documents rather than here.

## Running them

```sh
task cookbook   # the recipes with their rendered SQL
task surface    # the Postgres catalogue with its rendered SQL
task typecheck  # compiles the negative build and fails if it succeeds

go test ./examples/...                                    # all of it
go test ./examples/cookbook -run Example_loadComposite -v  # one recipe
```

A `PASS` means the SQL in that example's `// Output:` block is byte-for-byte what the builder produced. There is no mode in which a passing example prints something other than what is written down.

## Why an example rather than a test

A test answers "does this work". An example answers "what does this look like", and prints the SQL so the answer can be read without running anything. Both are checked by `go test`, so the second costs nothing in trust: the documentation and the assertion are the same three lines.

The limit is worth stating plainly. An example proves the **text**; only execution proves the **SQL**. Nothing in this tree opens a database — the core module has no driver and cannot. Execution lives in [`contrib/driver-sqlite`](../contrib/driver-sqlite) and [`contrib/driver-pgx`](../contrib/driver-pgx), where the same constructs run against real servers, and where eleven `pg` constructs were once found whose rendering tests passed and whose SQL Postgres rejects.
