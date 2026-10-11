# `ch` — ClickHouse constructs

`FINAL`, `PREWHERE`, `LIMIT BY`, `ARRAY JOIN`, conditional aggregates and `SETTINGS`.

**This is not a ClickHouse client, and the README should not be read as claiming one.** It exists to prove the extension points carry a second engine, and it has never been executed against a server — say so rather than implying coverage. It is also where a capability is declined by *not implementing an interface*: `clickhouse.Dialect` omits `SupportsReturning` entirely, so a `RETURNING` call does not compile rather than failing at runtime.

## Full documentation

The page for this package is [`docs/ch/README.md`](../docs/ch/README.md), with runnable snippets — every one of them copied from a test whose expected output `go test` checks. The documentation index is [`../docs/README.md`](../docs/README.md) and the repository README is [`../README.md`](../README.md).
