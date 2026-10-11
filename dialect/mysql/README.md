# `dialect/mysql` — the MySQL dialect

Placeholders (`?`), identifier quoting (backquotes), and the capabilities MySQL 8 has.

It imports [`render`](../../render) and nothing else, which `TestDialects_ImportOnlyRender` checks. It declares `SupportsLikeEscape`, `SupportsCombinedAlter` and `SupportsUnrestrictedAddColumn`, and implements `LiteralQuoting`. It does not declare `SupportsReturning`, because MySQL has no `RETURNING`, so an `InsertReturning` on a MySQL statement does not compile.

Two rules are worth reading before relying on it:

- **Identifiers are backquoted**, because a double-quoted token is a string in MySQL unless `ANSI_QUOTES` is on, and a builder cannot depend on a server's mode.
- **A string containing a backslash cannot be written as a literal.** MySQL reads a backslash differently under `NO_BACKSLASH_ESCAPES`, and it stores an expression default as printed text and re-parses it under the session's mode, so no spelling reads the same under both. `QuoteLiteral` returns `ErrNotInlinable` for it, measured on MySQL 8.4. A bound parameter has no such problem, so this only affects DDL.

`contrib/driver-mysql` runs the shared conformance suite against a real MySQL 8.4.

## Full documentation

[`docs/dialect/README.md`](../../docs/dialect/README.md) covers every dialect and how to write another; [`docs/dialects.md`](../../docs/dialects.md) covers what a dialect decides.
