# The catalogues

`examples/` holds code that exists to be read and to prove claims. Every statement in it is built by a test whose expected SQL is part of the test, so a change in rendering fails the build rather than quietly changing what the documentation says.

| Directory | What it covers |
|---|---|
| `examples/typecheck` | Nineteen deliberately wrong statements behind a build tag, in four classes: typed expressions, write typestate, engine clauses and dialect capabilities. The gate compiles the file and fails if it succeeds. |
| `examples/cookbook` | Twelve recipes, from a plain select to plugin composition, vector search and the unsupported-dialect failure. Four packages — `authlite`, `phone`, `workspace` and `app` — show the cross-package story the library exists for; [cross-package.md](./cross-package.md) walks through it. |
| `examples/pgsurface` | The Postgres surface, area by area: expressions, selects, writes, windows, set operations. |
| `examples/pgsurface/advancedsql` | An external catalogue of advanced PostgreSQL queries, translated section by section: window functions, CTEs, practice exercises including islands-and-gaps, and interview-style solutions. |
| `examples/pgsurface/snippets` | A twenty-one section catalogue of advanced Postgres snippets, section by section and in the original order. 193 SQL blocks, each an `Example` or a comment saying why it renders nothing. |
| `examples/pgsurface/periodcompare` | One worked report: a multi-source period comparison with per-period aggregates, computed ratios and a total row. |
| `pg/vector/example_test.go` | The pgvector README, query by query. |

## The negative build

`examples/typecheck/bad.go` is the only file in the repository that must not compile. Five of its statements, each of which `task typecheck` requires to fail on its own line:

```go
_ = sqlb.Bind(name, &n)        // string column into *int
_ = sqlb.Eq(at, "yesterday")   // time column compared with a string
_ = sqlb.EqExpr(name, flag)    // join between string and bool
_ = vector.L2(name, emb)       // distance over a string column
_ = sqlb.Sum(name)             // sum over a non-numeric column
```

The other fourteen cover write typestate, engine clauses typed to their dialect and statement kind, and `RETURNING` as a capability constraint; [`examples/typecheck/README.md`](../examples/typecheck/README.md) lists all four classes. `task typecheck` builds the file with the `typecheck` tag and fails if the compiler accepts it. A claim about the type system that no test can break is not worth printing, and this is the test that can break it.

## Reading them

```sh
task cookbook   # the recipes with their rendered SQL
task surface    # the Postgres catalogue with its rendered SQL
```

Each recipe is an `Example`, so a mismatch between the rendered SQL and the documented SQL is a test failure. Running one:

```
$ go test ./examples/cookbook -run Example_vectorSearch -v
=== RUN   Example_vectorSearch
--- PASS: Example_vectorSearch (0.00s)
PASS
ok  	github.com/ubgo/sqlb/examples/cookbook	0.550s
```

A `PASS` there means the SQL in the `// Output:` block is byte-for-byte what the builder produced.

Both are `Example` functions, so the SQL in the `// Output:` block is checked by `go test`. What you read is what the builder currently produces.
