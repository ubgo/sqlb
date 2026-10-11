# `stmt` — statements, clauses and Query

SELECT, INSERT, UPDATE and DELETE, the clause slots that let a package outside this module add a clause, and the `Query` a statement builds to.

**A builder method returns a derived statement and never writes to its receiver.** `base.Where(x)` leaves `base` alone, so deriving several queries from one template is safe by construction rather than by remembering `Clone`. **And a write cannot be built until it is complete**: an unfiltered `UPDATE` has no `Build` method at all, so it does not compile. Both are pinned by tests that were verified by breaking them.

## Full documentation

The page for this package is [`docs/stmt/README.md`](../docs/stmt/README.md), with runnable snippets — every one of them copied from a test whose expected output `go test` checks. The documentation index is [`../docs/README.md`](../docs/README.md) and the repository README is [`../README.md`](../README.md).
