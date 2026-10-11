# advancedsql

An external catalogue of advanced PostgreSQL queries, translated into `sqlb` section by section. Six sections, six test files, 53 examples, and the section numbers in the test names are the catalogue's own so the examples can be read in the order they were written.

| File | Section |
|---|---|
| `window1_test.go` · `window2_test.go` | Window functions, in two parts |
| `cte_test.go` | Common table expressions |
| `exercises_test.go` | Practice exercises, including islands-and-gaps |
| `misc_test.go` | Miscellaneous notes and interview-style solutions |
| `schema_test.go` | The tables the rest build against |

## The question it answers

The grammar catalogues answer "does this construct render". They cannot answer the one that matters: **can this builder write a real analyst's query**, rather than one row of a syntax table.

The finding is worth stating, because it is the kind of result that only counts if it was possible for it to come out badly. Every query here needed only constructs that already existed, plus `AssertNotNull` for a window result compared with a non-null column. Nothing was added to the library to make the translation work.

## Translation rules, stated once

- **`SELECT *` and `SELECT e.*` bind the table's known columns.** `sqlb` never emits a star, so a new column cannot silently change an old scan.
- **A window result the source compared with a non-null column goes through `sqlb.AssertNotNull`**, with the run-time reason written beside it.
- **Two source statements are not valid SQL as written** — a doubled `OVER` in 1.2, and a `SELECT` with no `FROM` in section 3. The tests render the statement the surrounding prose described, and say so in a comment.

## Limits

These tests prove rendering and the compile-time checks. None of this SQL has run against a Postgres server from here; execution is [`contrib/driver-pgx`](../../../contrib/driver-pgx)'s job, and the difference has already cost this repository eleven constructs that rendered cleanly and that Postgres rejects.

```sh
go test ./examples/pgsurface/advancedsql -v
```
