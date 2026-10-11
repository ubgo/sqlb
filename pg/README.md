# `pg` — PostgreSQL statements and constructs

Everything Postgres in one import. Statements start here, `pg.Select`, `pg.Insert`, `pg.Update` and `pg.Delete`, with the dialect in their type; and every Postgres construct that goes inside them is here too: arrays, JSONB, ranges, full-text search, `DISTINCT ON`, `ON CONFLICT`, `MERGE`, locking, and eight extension sub-packages built on the guarded constructors it exports.

**A rendering test proves the text, and only execution proves the SQL.** Running these constructs against a real Postgres 17 from `contrib/driver-pgx` found eleven whose rendering tests passed and whose SQL Postgres rejects. A new construct here is not done until it has an entry in that driver's catalogue. **Extensions never edit this package** — that is what the exported guarded constructors are for.

## Full documentation

The page for this package is [`docs/pg/README.md`](../docs/pg/README.md), with runnable snippets — every one of them copied from a test whose expected output `go test` checks. The documentation index is [`../docs/README.md`](../docs/README.md) and the repository README is [`../README.md`](../README.md).
