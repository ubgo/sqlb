# `schema` — what tables exist

Table descriptions, the per-engine type spelling table, and a deterministic topological sort, so two packages that own tables can be migrated together.

**It describes and never does.** No DDL, no connection, no migration; those live above it, and `contrib/migrate` is the consumer that proves it carries its weight by executing its generated plan against a real database. `contrib/vet` reads the same type table, so a column the vet accepts is one `CreateTable` would have produced — two copies of what a `bigint` is would have drifted.

## Full documentation

The page for this package is [`docs/schema/README.md`](../docs/schema/README.md), with runnable snippets — every one of them copied from a test whose expected output `go test` checks. The documentation index is [`../docs/README.md`](../docs/README.md) and the repository README is [`../README.md`](../README.md).
