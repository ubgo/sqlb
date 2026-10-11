# `scan` — results as values

The other result strategy: readers that allocate per row and return values, instead of writing through pointers the caller supplied.

**Nothing here takes the query guard, and adding one would be a regression.** A `Reader` holds no caller memory, so the guard would refuse a concurrent execution that is safe, contradicting the one property this package exists to offer. `TestDynamic_SafeAcrossGoroutines` runs one shared `Query` from sixty-four goroutines under `-race`. `Map2` is the only combinator and stays that way: Go generics cannot express a variadic type list.

## Full documentation

The page for this package is [`docs/scan/README.md`](../docs/scan/README.md), with runnable snippets — every one of them copied from a test whose expected output `go test` checks. The documentation index is [`../docs/README.md`](../docs/README.md) and the repository README is [`../README.md`](../README.md).
