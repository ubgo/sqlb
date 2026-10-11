# `sqlbtest` — the shared conformance suite

Twelve cases plus `RunReturning`, written once and run by every driver, because rendering SQL and executing it are two different claims.

**`Run` holds a runtime dialect and therefore cannot ask for a capability**, which is why the `RETURNING` case is `RunReturning`, called with a concrete dialect. `contrib/driver-sqlite` runs the suite in memory and file-backed; `contrib/driver-pgx` runs it and `RunReturning` against a real Postgres 17. A driver that passes it has proven the thing the builder's own tests cannot.

It also holds `ZeroValues`, which needs no database: it builds the zero value of every exported struct type in a package, found by reading the package's source, and fails on one that panics inside `Build` or builds without an error. `ddl`, `pg`, `sqlite`, `mysql` and `ch` run it, and a construct package from another repository can too.

## Full documentation

The page for this package is [`docs/conformance.md`](../docs/conformance.md), with runnable snippets — every one of them copied from a test whose expected output `go test` checks. The documentation index is [`../docs/README.md`](../docs/README.md) and the repository README is [`../README.md`](../README.md).
