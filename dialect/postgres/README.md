# `dialect/postgres` — the PostgreSQL dialect

Placeholders (`$1`, `$2`), identifier quoting (double quotes), and the capabilities this engine has.

It imports [`render`](../../render) and nothing else — which is exactly what a dialect written in another repository imports. That is the point of the layout: adding PostgreSQL and adding an engine nobody has written yet are the same act, and `TestDialects_ImportOnlyRender` reads this package's import list and fails on anything else.

It declares `SupportsReturning`, so `InsertReturning` and its siblings compile against it, and `SupportsLikeEscape`, so `expr.Contains` and its siblings emit an `ESCAPE` clause rather than leaving a user-typed `%` to match far more rows than intended. It also declares `SupportsILike`, so the fold predicates, `expr.ContainsFold` and its siblings, write `ILIKE` here and `lower()` on both sides on every other engine.

The constructs PostgreSQL spells differently are not here. They live in the engine construct package, because a caller naming the engine it builds for should not have to import several hundred constructs to do it.

## Full documentation

[`docs/dialect/README.md`](../../docs/dialect/README.md) covers all three and how to write a fourth; [`docs/dialects.md`](../../docs/dialects.md) covers what a dialect decides and how a construct fails closed under the wrong one.
