# `dialect/sqlite` — the SQLite dialect

Placeholders (`?`), identifier quoting (double quotes), and the capabilities this engine has.

It imports [`render`](../../render) and nothing else — which is exactly what a dialect written in another repository imports. That is the point of the layout: adding SQLite and adding an engine nobody has written yet are the same act, and `TestDialects_ImportOnlyRender` reads this package's import list and fails on anything else.

It declares `SupportsReturning` and `SupportsLikeEscape`. The second is worth reading carefully, because the distinction is easy to get backwards: SQLite accepts an explicit `ESCAPE` clause, which is what the capability reports, but it has no *default* escape character the way Postgres and MySQL do. So `expr.Contains` must write `ESCAPE` out rather than relying on a backslash — a pattern that assumed the backslash would match zero rows here, silently, and look like it worked.

The constructs SQLite spells differently are not here. They live in the engine construct package, because a caller naming the engine it builds for should not have to import several hundred constructs to do it.

## Full documentation

[`docs/dialect/README.md`](../../docs/dialect/README.md) covers all three and how to write a fourth; [`docs/dialects.md`](../../docs/dialects.md) covers what a dialect decides and how a construct fails closed under the wrong one.
