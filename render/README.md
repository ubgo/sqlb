# `render` — text, placeholders and bound values

The bottom layer. It owns `Dialect`, `Args`, `SQLer`, `Raw`, `Quote` and `ErrUnsupportedDialect`, and it imports only the standard library.

**It says nothing about SQL, and that is load-bearing.** Operators, join types, sort directions and keywords are SQL vocabulary and belong to the layers above, because a Gremlin or MongoDB target has no `JOIN` and no `<>`. `TestRender_ServesANonSQLTarget` drives this layer with a non-SQL dialect and `TestRender_ImportsOnlyStdlib` pins the floor. A construct, operator or keyword added here is a defect even when it compiles.

## Full documentation

The page for this package is [`docs/render/README.md`](../docs/render/README.md), with runnable snippets — every one of them copied from a test whose expected output `go test` checks. The documentation index is [`../docs/README.md`](../docs/README.md) and the repository README is [`../README.md`](../README.md).
