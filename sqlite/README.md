# `sqlite` — SQLite statements and constructs

Everything SQLite in one import. Statements start here, `sqlite.Select`, `sqlite.Insert`, `sqlite.Update` and `sqlite.Delete`, with the dialect in their type; and every SQLite construct that goes inside them is here too: JSON1 (every function prefixed `JSON`, since `sqlite.Insert` is the statement), both upsert mechanisms, FTS5, the connection-scoped functions, and the index hints.

**Every construct here is executed, not merely rendered.** `contrib/driver-sqlite` runs all of them against a real database, which is the only reason this package can be called proven. **And LIKE escaping is a capability, not a default**: SQLite has no escape character at all, so a backslash-escaped pattern matches zero rows unless `ESCAPE` is written out — which is why `expr.Contains` asks the dialect first and records `ErrNoLikeEscape` when it declines.

## Full documentation

The page for this package is [`docs/sqlite/README.md`](../docs/sqlite/README.md), with runnable snippets — every one of them copied from a test whose expected output `go test` checks. The documentation index is [`../docs/README.md`](../docs/README.md) and the repository README is [`../README.md`](../README.md).
