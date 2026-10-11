# pg/trgm

The pg_trgm extension for `sqlb`. Needs `CREATE EXTENSION pg_trgm`.

| SQL | Helper |
|---|---|
| `a % b` | `Similar` |
| `a <% b`, `a <<% b` | `WordSimilar`, `StrictWordSimilar` |
| `a <-> b`, `a <<-> b` | `Distance`, `WordDistance`, for `ORDER BY` with a GiST index |
| `similarity`, `word_similarity`, `strict_word_similarity` | `Similarity`, `WordSimilarity`, `StrictWordSimilarity` |
| `show_trgm` | `ShowTrgm` |

The `%` family is served by `gin_trgm_ops`; the `<->` family needs `gist_trgm_ops`. `ILIKE '%x%'` on a trigram-indexed column is `pg.ILike`, not a helper here, because the operator is core Postgres; only the index is the extension's.

```go
needle := sqlb.Param("jon smith")
sel.Exprs(name, sqlb.As(trgm.Similarity(name, needle), "sim")).
	From(users).Where(trgm.Similar(name, needle)).
	Order(sqlb.OrderBy(trgm.Distance(name, needle), sqlb.SortAsc)).Limit(10)
```
