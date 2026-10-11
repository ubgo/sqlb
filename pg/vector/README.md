# pg/vector

The pgvector extension for `sqlb`, the whole query surface of the [pgvector README](https://github.com/pgvector/pgvector) and the types of [pgvector-go](https://github.com/pgvector/pgvector-go). Needs `CREATE EXTENSION vector`. Index DDL (`hnsw`, `ivfflat`, operator classes, partial and expression indexes) is versioned SQL the application applies; every query shape those indexes serve is here, as a checked example in `example_test.go`.

## Types

| pgvector | Go | Wire form |
|---|---|---|
| `vector(n)` | `Vector`, a `[]float32` | `[1,2,3]` |
| `halfvec(n)` | `HalfVector`, a `[]float32`; the server rounds to half precision | `[1,2,3]` |
| `sparsevec(n)` | `SparseVector{Dimensions, Indices, Values}`, `NewSparse(dense)` | `{2:1.5,4:2}/5`, 1-based on the wire, 0-based in Go like pgvector-go |
| `bit(n)` | `BitVector`, a string of 0 and 1 | `0101` |

All four implement `Scan` and `Value`, so `database/sql` and pgx carry them as text without a codec. `Dense` is the type set with distance operators (`Vector | HalfVector | SparseVector`); `Elementwise` is the set with arithmetic and `subvector` (`Vector | HalfVector`).

## Operators and functions

| SQL | Helper | On |
|---|---|---|
| `a <-> b` L2 | `L2` | Dense |
| `a <=> b` cosine | `Cosine` | Dense |
| `a <#> b` negative inner product | `InnerProduct` | Dense |
| `a <+> b` L1 | `L1` | Dense |
| `a <~> b` Hamming, `a <%> b` Jaccard | `Hamming`, `Jaccard` | BitVector |
| `l2_distance`, `cosine_distance`, `inner_product` (positive), `l1_distance` | `L2Distance`, `CosineDistance`, `InnerProductOf`, `L1Distance` | Dense |
| `hamming_distance`, `jaccard_distance` | `HammingDistance`, `JaccardDistance` | BitVector |
| `vector_dims`, `vector_norm` | `Dims`, `Norm` | Vector |
| `l2_norm` | `L2Norm` | HalfVector, SparseVector |
| `l2_normalize` | `L2Normalize` | Dense |
| `subvector(v, start, count)` | `Subvector` | Elementwise |
| `binary_quantize(v)` | `BinaryQuantize`, returns `BitVector`; cast with `CastBit` | Elementwise |
| `sum(v)`, `avg(v)` element-wise | `Sum`, `Avg` | Dense; Avg on Elementwise |
| `a + b`, `a - b`, `a * b`, `a \|\| b` | `Add`, `Sub`, `Mul`, `Concat` | Dense; Concat on Elementwise |
| `e::vector(n)`, `::halfvec(n)`, `::sparsevec(n)`, `::bit(n)` | `CastVector`, `CastHalf`, `CastSparse`, `CastBit`; dims 0 omits `(n)` | any expression |
| `$n::vector(n)` bound with its dimension | `ParamVector`, `ParamHalf`, `ParamSparse`, `ParamBit` | values |

Every distance is an `Expr[float64]`: one expression is the SELECT item, the WHERE operand and the ORDER BY term. Under a dialect other than Postgres, `Build` returns `sqlb.ErrUnsupportedDialect` naming the construct.

A bare `$n` has no type until Postgres sees the other operand. Against a column that is fine, so `sqlb.Param(v)` works in `L2(col, sqlb.Param(v))`. As a function argument it is not, so `ParamVector` adds the cast with the dimension taken from the value.

## Query-time settings

`SET` cannot take a bind parameter; `set_config(name, value, true)` can, and lasts for the transaction like `SET LOCAL`. Run the returned expression in a `SELECT` inside the transaction that runs the search.

| Setting | Helper |
|---|---|
| `hnsw.ef_search` | `SetEfSearch(n)` |
| `ivfflat.probes` | `SetProbes(n)` |
| `hnsw.iterative_scan`, `ivfflat.iterative_scan` | `SetHNSWIterativeScan(mode)`, `SetIVFFlatIterativeScan(mode)` with `ScanOff`, `ScanStrictOrder`, `ScanRelaxedOrder` |
| `hnsw.max_scan_tuples`, `ivfflat.max_probes` | `SetMaxScanTuples(n)`, `SetMaxProbes(n)` |
| `enable_seqscan = off`, `enable_indexscan = off` | `ForceIndex()`, `ForceExact()` |

The general form is `pg.SetLocal(name, value)` and `pg.CurrentSetting(name)`.

## The README's queries, as examples

Each is an `Example_*` in `example_test.go` and `go test` checks the SQL.

| pgvector README section | Example |
|---|---|
| Insert vectors | `Example_insert` |
| Nearest neighbours by L2, ORDER BY LIMIT | `Example_nearest` |
| Nearest neighbours to a row | `Example_nearestToRow` |
| Cosine similarity, inner product, distance in WHERE | `Example_distances` |
| Filtering with an approximate index, iterative scans, `ef_search` | `Example_filteredWithIterativeScan` |
| Relaxed order with strict results, `MATERIALIZED` CTE | `Example_relaxedOrderStrictResult` |
| Half-precision vectors and half-precision indexing by cast | `Example_halfPrecision` |
| Binary quantization with re-ranking | `Example_binaryQuantizeRerank` |
| Binary vectors, Hamming and Jaccard | `Example_binaryVectors` |
| Subvector indexing with re-ranking | `Example_subvectorRerank` |
| Sparse vectors | `Example_sparse` |
| Aggregates and arithmetic | `Example_aggregatesAndArithmetic` |
| Cast from a float array | `Example_castFromArray` |
| Hybrid search with Reciprocal Rank Fusion | `Example_hybridSearchRRF` |

Not here, and why: `CREATE INDEX` and its parameters are DDL; `COPY` bulk load is a driver feature (pgx `CopyFrom`); `SET maintenance_work_mem` and the parallel worker settings are for index builds, not queries, and `pg.SetLocal` renders them if a migration runner wants them; Citus distribution is DDL. pgvector-go's pgx binary codec is a driver registration for the integration module, not a query construct.
