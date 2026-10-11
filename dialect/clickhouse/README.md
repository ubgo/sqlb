# `dialect/clickhouse` — the ClickHouse dialect

Placeholders (`?`), identifier quoting (backticks), and the capabilities this engine has.

It imports [`render`](../../render) and nothing else — which is exactly what a dialect written in another repository imports. That is the point of the layout: adding ClickHouse and adding an engine nobody has written yet are the same act, and `TestDialects_ImportOnlyRender` reads this package's import list and fails on anything else.

It omits both `SupportsReturning` and `SupportsLikeEscape` **entirely**, which is how a dialect declines a capability here: not by returning false, but by not implementing the interface, so `InsertReturning` against it does not compile and `expr.Contains` records `ErrNoLikeEscape` rather than emitting a clause the engine would reject. `TestClickHouse_DeclinesReturning` pins the first.

The constructs ClickHouse spells differently are not here. They live in the engine construct package, because a caller naming the engine it builds for should not have to import several hundred constructs to do it.

## Full documentation

[`docs/dialect/README.md`](../../docs/dialect/README.md) covers all three and how to write a fourth; [`docs/dialects.md`](../../docs/dialects.md) covers what a dialect decides and how a construct fails closed under the wrong one.
