# pg/ltree

The ltree extension for `sqlb`. Needs `CREATE EXTENSION ltree` and, for the operators to use an index, `CREATE INDEX ... USING gist (path)`.

| SQL | Helper |
|---|---|
| Column type | `Path`, text on the wire |
| `$n::ltree`, a bound path with the cast | `Lit` |
| `a @> b`, `a <@ b` | `AncestorOf`, `DescendantOf` |
| `path ~ lquery` | `MatchesLQuery` |
| `path @ ltxtquery` | `MatchesTxtQuery` |
| `nlevel`, `subpath`, `subltree`, `index`, `lca` | `NLevel`, `Subpath`, `Subltree`, `Index`, `LCA` |
| `a \|\| b` | `Concat` |

Descendants of a node, ordered by depth:

```go
sel.Columns(sqlb.Bind(CatID, &id), sqlb.Bind(CatPath, &path)).
	From(Categories).
	Where(ltree.DescendantOf(CatPath, ltree.Lit("top.science"))).
	Order(sqlb.OrderBy(ltree.NLevel(CatPath), sqlb.SortAsc))
```

The same query as a recursive CTE needs no extension, so the choice is between an index that answers subtree questions in one scan and a portable shape; see the snippets package, sections 1 and 20.6, for both.
