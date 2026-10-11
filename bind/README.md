# `bind` — results through caller pointers

The shorter of the two result strategies. `Bind(col, &dst)` captures a pointer, and the select list and the scan targets come from one slice so they cannot drift apart.

**`Binding` is an interface owned by `stmt`, and must stay one.** The statement layer knows only that an item renders and carries an opaque target; it must never learn that the target is a pointer. That is what lets `bind` be its own package, and what keeps a third result strategy someone else's package rather than a fork — `contrib/structbind` is the proof, since a reflection-driven binder cannot use `Bind` at all.

## Full documentation

The page for this package is [`docs/bind/README.md`](../docs/bind/README.md), with runnable snippets — every one of them copied from a test whose expected output `go test` checks. The documentation index is [`../docs/README.md`](../docs/README.md) and the repository README is [`../README.md`](../README.md).
