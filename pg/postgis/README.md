# pg/postgis

The template for a Postgres extension package. It is also a usable PostGIS layer for the calls a location feature needs. Needs `CREATE EXTENSION postgis`.

| SQL | Helper |
|---|---|
| Column type | `Geometry`, EWKB hex text on the wire |
| `ST_SetSRID(ST_MakePoint(lon, lat), srid)` | `MakePoint` |
| `ST_Distance`, `ST_DistanceSphere` | `Distance`, `DistanceSphere` |
| `ST_DWithin(a, b, d)` | `DWithin`, the index-served radius predicate |
| `ST_Intersects`, `ST_Contains` | `Intersects`, `Contains` |
| `a <-> b` in `ORDER BY` | `KNN` |
| `ST_AsGeoJSON` | `AsGeoJSON` |

## How to add an extension package

Copy this directory, then change four things.

1. **The column type.** A Go type with `Scan` and `Value` in the extension's text I/O form, so any `database/sql` driver carries it. Decoding beyond text is the job of a domain library, not the builder.
2. **The helpers.** One line each over the constructors `pg` exports: `pg.Func` for a call, `pg.BinOp` for an operator that yields a value, `pg.BinPred` for an operator that yields a boolean, `pg.Expr` or `pg.Pred` when the shape is irregular, `pg.ClauseIn` for a statement clause. The first argument is the construct name that appears in the error, `"postgis.DWithin"`.
3. **The guard test.** Build one statement under `sqlb.SQLiteDialect{}` and assert `sqlb.ErrUnsupportedDialect`; build the same under Postgres and assert the exact SQL. The guard is what makes the package safe to import in a multi-engine library.
4. **The README.** A table from SQL to helper, the `CREATE EXTENSION` line, and the index the operators expect.

Nothing in `pg` changes. The extension package imports `pg` for the constructors and `sqlb` for the types, and the application imports the extension package. A helper the extension package lacks is written the same way by the application, against the same constructors.
