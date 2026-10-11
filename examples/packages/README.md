# packages

One file per layer, and the source of every snippet in the per-package documentation.

`sqlb` is built as a stack — `render` knows nothing about SQL, `expr` builds typed expressions, `stmt` builds statements, `exec` runs them — and the layers can be used directly by anyone replacing one of them. This directory shows each layer as a consumer would actually call it, importing the layer package rather than the root facade, which is why the snippets read `expr.Col` and `stmt.NewSelect` rather than `sqlb.Col` and `sqlb.NewSelect`.

## The files

| File | Layer | What it covers |
|---|---|---|
| `render_test.go` | [`render`](../../docs/render/README.md) | placeholders, quoting, bound values, and writing a dialect |
| `expr_test.go` | [`expr`](../../docs/expr/README.md) | tables, typed columns, predicates, functions, ordering |
| `stmt_test.go` | [`stmt`](../../docs/stmt/README.md) | SELECT, the writes, joins, clauses, subqueries |
| `outsideclause_test.go` | [`stmt`](../../docs/stmt/README.md) | adding a clause from outside the module, which is what the slot set exists for |
| `exec_test.go` | [`exec`](../../docs/exec/README.md) | `DBTX`, `One`, `Each`, and the errors worth knowing |
| `results_test.go` | [`bind`](../../docs/bind/README.md) · [`scan`](../../docs/scan/README.md) | the two result strategies, and when each is the right one |
| `schema_test.go` | [`schema`](../../docs/schema/README.md) | declaring tables so two packages can be migrated together |
| `schema_usecases_test.go` | [`schema`](../../docs/schema/README.md) | what a description-only layer is actually used for |
| `ddl_test.go` | [`ddl`](../../docs/ddl/README.md) | creating, changing and removing a table from the columns its queries use |

55 examples in total.

## Why the documentation is generated from here

A snippet in a README is prose: nothing reads it, so it rots silently. This repository had a page documenting a method called `Clauses` for a week — the method is `Clause`, and no tool had ever read that line.

So every snippet on a per-package page is copied from an example here, where it is a runnable Go `Example` whose printed output `go test` checks. Three gates keep the copy honest: `check-docs.py` verifies links and anchors, `check-doc-setup.py` compiles the Setup block of every page, and `check-doc-names.py` reads every Go fence in `docs/` and fails on a qualified name or chained method that does not exist.

```sh
go test ./examples/packages -v
```
