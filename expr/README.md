# `expr` — typed columns, predicates and expressions

Where a database column becomes a typed Go value. `Col[T]` returns an `Expr[T]`, and every comparison, function and aggregate over it carries that type forward.

**A column name is the one value from outside that is rendered into SQL text rather than bound.** No engine accepts a placeholder where an identifier goes, so dynamic selection depends entirely on the dialect doubling an embedded quote, which keeps a hostile string one identifier. `TestCol_NameFromOutsideStaysOneIdentifier` pins it here, where the name enters.

## Full documentation

The page for this package is [`docs/expr/README.md`](../docs/expr/README.md), with runnable snippets — every one of them copied from a test whose expected output `go test` checks. The documentation index is [`../docs/README.md`](../docs/README.md) and the repository README is [`../README.md`](../README.md).
