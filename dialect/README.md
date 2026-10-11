# `dialect` — one package per engine

`postgres`, `sqlite` and `clickhouse`, each its own package importing `render` and nothing else.

**The built-in three are not privileged.** They import exactly what a dialect from another repository imports, so adding Postgres and adding an engine nobody has written are the same act. `TestDialects_ImportOnlyRender` reads each package's import list and fails on anything else. **A dialect declines a capability by not implementing the interface, never by returning false**, so an engine is never made to lie about what it supports.

## Full documentation

The page for this package is [`docs/dialect/README.md`](../docs/dialect/README.md), with runnable snippets — every one of them copied from a test whose expected output `go test` checks. The documentation index is [`../docs/README.md`](../docs/README.md) and the repository README is [`../README.md`](../README.md).
