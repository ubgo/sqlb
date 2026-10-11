# `ddl` — CREATE, ALTER and DROP TABLE

Builds the statements that create, change and remove a library's tables, over the same `Col[T]` values its queries already use, so a column cannot be created under one name or type and read under another. It is authored DDL, never derived: a builder renders what its author wrote, and when and in what order the statements run stays the application's decision.

**Every value in DDL is a literal**, because no engine accepts a placeholder there, so a value the dialect cannot write faithfully fails `Build` rather than rendering something that looks right. **And a statement cannot be built until it is complete**: a `CREATE TABLE` with no column and an `ALTER TABLE` with no action have no `Build` method, so they do not compile.

## Full documentation

The page for this package is [`docs/ddl/README.md`](../docs/ddl/README.md), with runnable snippets — every one of them copied from a test whose expected output `go test` checks. The documentation index is [`../docs/README.md`](../docs/README.md) and the repository README is [`../README.md`](../README.md).
