# pg/unaccent

The unaccent extension for `sqlb`. Needs `CREATE EXTENSION unaccent`.

| SQL | Helper |
|---|---|
| `unaccent(text)` | `Unaccent` |
| `unaccent('dict'::regdictionary, text)` | `WithDictionary` |

Accent-insensitive equality: `sqlb.EqExpr(sqlb.Lower(unaccent.Unaccent(name)), sqlb.Lower(unaccent.Unaccent(sqlb.Param(q))))`. Combine with `pg/trgm` for accent-insensitive fuzzy search.
