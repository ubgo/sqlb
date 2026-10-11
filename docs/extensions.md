# Postgres extensions

Nothing extension-specific lives in `pg`. Each `CREATE EXTENSION` has its own sub-package, so adding one is adding a directory and never an edit to `pg`.

| Package | Extension | What it carries |
|---|---|---|
| `pg/vector` | `vector` | the whole pgvector surface: `Vector`, `HalfVector`, `SparseVector` and `BitVector`; the six distance operators and their function forms; `l2_normalize`, `subvector`, `binary_quantize`; element-wise aggregates and arithmetic; the casts; and the query-time settings |
| `pg/trgm` | `pg_trgm` | the similarity and distance operators and their functions |
| `pg/fuzzy` | `fuzzystrmatch` | phonetic codes and edit distance |
| `pg/ltree` | `ltree` | the `Path` type, ancestor and descendant operators, lquery and ltxtquery matching |
| `pg/crypto` | `pgcrypto` | hashing, password hashing and symmetric encryption |
| `pg/earth` | `earthdistance` | great-circle distance and the indexed bounding box |
| `pg/unaccent` | `unaccent` | diacritic stripping |
| `pg/postgis` | `postgis` | geometry, distance, radius and nearest-neighbour, and the template for the next one |

Each package has its own README with a table from SQL to helper, the `CREATE EXTENSION` line, and the index its operators expect.

## What `pg` exports for them

An extension package is built from nine guarded constructors, and nothing else from `pg`:

| Constructor | Renders |
|---|---|
| `pg.Func[R]` | a function call |
| `pg.BinOp[R]` | an operator that yields a value |
| `pg.BinPred` | an operator that yields a boolean |
| `pg.Expr[T]` | an expression of any shape |
| `pg.Pred` | a predicate of any shape |
| `pg.ClauseIn` | a clause in a statement slot |
| `pg.Guard` | the dialect check, for a helper that renders in several pieces |
| `pg.QuoteLit` | an author constant as a quoted literal |
| `pg.List` | comma-separated operands |

Every one of them takes the construct name that appears in the error, and every one records `Unsupported` under another dialect. That is what makes an extension package safe to import in a multi-engine application: its constructs fail closed exactly like the ones in `pg`.

```go
package trgm

// Similar renders a % b: similarity above pg_trgm.similarity_threshold.
func Similar(a, b sqlb.Expr[string]) sqlb.Predicate {
	return pg.BinPred("trgm.Similar", "%", a, b)
}
```

## Adding an extension

Copy `pg/postgis` and change four things.

1. **The column type.** A Go type with `Scan` and `Value` in the extension's text I/O form, so any `database/sql` driver carries it without a codec. Decoding beyond text belongs to a domain library, not to a query builder.
2. **The helpers.** One line each over the constructors above, each with the construct name it reports.
3. **The guard test.** Build every expression under `sqlb.SQLiteDialect{}` and assert `ErrUnsupportedDialect`; build the same under Postgres and assert the exact SQL. A clause needs no such test: `pg.ClauseIn[K]` returns a clause typed to Postgres and to the statement kind `K`, so the wrong engine or the wrong statement is a compile error.
4. **The README.** The SQL-to-helper table, the `CREATE EXTENSION` line, and the index the operators need.

An application that needs a helper the package does not have writes it the same way, against the same constructors, without waiting for a release. That is the point of exporting them.

## Installing what an extension needs

Every extension package exports the `pg.Extension` it needs, `vector.Extension`, `trgm.Extension` and so on, so `pg.CreateExtension(vector.Extension).IfNotExists()` installs it without the name being spelled. `earth.Cube` is the one dependency named separately, since `earthdistance` needs `cube` and this module never writes `CASCADE` on `CREATE EXTENSION`.

The index methods and operator classes belong to the package that owns them, so adding one never edits `pg`: `vector.HNSW` and `vector.IVFFlat` with `vector.CosineOps`, `L2Ops`, `IPOps`, `L1Ops` and the `HalfCosineOps`, `HalfL2Ops`, `HalfIPOps` classes for `halfvec`, tuned by `vector.M`, `vector.EfConstruction` and `vector.Lists`; `trgm.GinOps` and `trgm.GistOps`; `ltree.GistOps`. An operator class is typed to the Go type of the column it indexes, so `vector.CosineOps` on a text column does not compile, and `pg.NullableOps(trgm.GinOps)` is the same class for a nullable column. The statements themselves are on the [`ddl` page](./ddl/README.md#postgres-schemas-extensions-enums-indexes-and-comments).
