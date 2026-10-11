# `exec` — running a statement

`DBTX`, `One` and `Each`. It takes an interface that both `*sql.DB` and `*sql.Tx` satisfy, so it has no driver and no opinion about one.

**A `Query` is not safe to share between goroutines, and this is the layer that says so.** It holds pointers into the caller's variables, so two goroutines executing one query interleave each other's rows silently. `One` and `Each` refuse an overlapping execution with `ErrQueryInUse`; the guard is inherited by every statement derived from the builder. It is detection, not prevention.

## Full documentation

The page for this package is [`docs/exec/README.md`](../docs/exec/README.md), with runnable snippets — every one of them copied from a test whose expected output `go test` checks. The documentation index is [`../docs/README.md`](../docs/README.md) and the repository README is [`../README.md`](../README.md).
