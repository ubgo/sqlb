# pg/earth

The earthdistance extension for `sqlb`. Needs `CREATE EXTENSION cube` then `CREATE EXTENSION earthdistance`, and a GiST index on `ll_to_earth(lat, lon)` for the box search.

| SQL | Helper |
|---|---|
| `ll_to_earth(lat, lon)` | `LLToEarth` |
| `earth_distance(a, b)` | `Distance` |
| `earth_box(p, r)` | `Box` |
| `box @> p` | `BoxContains` |

Radius search, box first for the index, exact distance second:

```go
here := earth.LLToEarth(sqlb.Param(lat), sqlb.Param(lon))
there := earth.LLToEarth(PlaceLat, PlaceLon)
sel.Where(earth.BoxContains(earth.Box(here, radius), there)).
	Where(sqlb.LessEq(earth.Distance(here, there), radius))
```

Beyond "what is within r metres", use `pg/postgis`.
