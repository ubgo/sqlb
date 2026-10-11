package vector_test

import (
	"fmt"
	"strings"

	"github.com/ubgo/sqlb"
	"github.com/ubgo/sqlb/pg"
	"github.com/ubgo/sqlb/pg/vector"
)

// The pgvector README, query by query, as checked examples. Index DDL is
// out of scope; the shape each index serves is what these show.

var (
	d     = sqlb.PostgresDialect{}
	items = sqlb.Table{Name: "items", Alias: "items"}
	id    = sqlb.Col[int64](items, "id")
	emb   = sqlb.Col[vector.Vector](items, "embedding")
	half  = sqlb.Col[vector.HalfVector](items, "embedding")
	spv   = sqlb.Col[vector.SparseVector](items, "embedding")
	bits  = sqlb.Col[vector.BitVector](items, "embedding")
	cat   = sqlb.Col[int32](items, "category_id")
)

func sel() *sqlb.Select[sqlb.PostgresDialect] { return sqlb.NewSelect(d) }

func show(q sqlb.Query, err error) {
	if err != nil {
		fmt.Println("error:", err)
		return
	}
	sql := strings.ReplaceAll(q.SQL, ") SELECT ", ")\nSELECT ")
	sql = strings.ReplaceAll(sql, `), "`, "),\n\"")
	fmt.Println(sql)
	fmt.Println("args:", q.Args)
}

// Insert vectors: pgvector-go's first example. The Vector value renders in
// text form and the driver sends it as a string.
func Example_insert() {
	show(sqlb.NewInsert(d).Into(items).
		Row(sqlb.Set(emb, vector.Vector{1, 2, 3})).
		Row(sqlb.Set(emb, vector.Vector{4, 5, 6})).Build())
	// Output:
	// INSERT INTO "items" AS "items" ("embedding") VALUES ($1), ($2)
	// args: [[1 2 3] [4 5 6]]
}

// Nearest neighbours by L2: ORDER BY distance LIMIT n is the shape an HNSW
// or IVFFlat index serves. The same expression is the SELECT item.
func Example_nearest() {
	q := vector.ParamVector(vector.Vector{3, 1, 2})
	dist := vector.L2(emb, q)
	show(sel().Exprs(id, sqlb.As(dist, "distance")).From(items).
		Order(sqlb.OrderBy(dist, sqlb.SortAsc)).Limit(5).Build())
	// Output:
	// SELECT "items"."id", ("items"."embedding" <-> $1::vector(3)) AS "distance" FROM "items" AS "items" ORDER BY ("items"."embedding" <-> $2::vector(3)) ASC LIMIT $3
	// args: [[3 1 2] [3 1 2] 5]
}

// Nearest neighbours to a row: the query vector is a subquery.
func Example_nearestToRow() {
	target := sqlb.AssertNotNull(sqlb.Subquery[vector.Vector](sel().Exprs(emb).From(items).Where(sqlb.Eq(id, int64(1)))))
	dist := vector.L2(emb, target)
	show(sel().Exprs(id).From(items).Where(sqlb.NotEq(id, int64(1))).
		Order(sqlb.OrderBy(dist, sqlb.SortAsc)).Limit(5).Build())
	// Output:
	// SELECT "items"."id" FROM "items" AS "items" WHERE ("items"."id" <> $1) ORDER BY ("items"."embedding" <-> (SELECT "items"."embedding" FROM "items" AS "items" WHERE ("items"."id" = $2))) ASC LIMIT $3
	// args: [1 1 5]
}

// Cosine similarity as 1 minus cosine distance, inner product negated back
// to positive, and a distance filter in WHERE.
func Example_distances() {
	q := vector.ParamVector(vector.Vector{3, 1, 2})
	sim := sqlb.Binary[float64](sqlb.Param(1.0), sqlb.OpSub, vector.Cosine(emb, q))
	ip := sqlb.Binary[float64](vector.InnerProduct(emb, q), sqlb.OpMul, sqlb.Param(-1.0))
	show(sel().Exprs(id, sqlb.As(sim, "cosine_similarity"), sqlb.As(ip, "inner_product")).From(items).
		Where(sqlb.Less(vector.L2(emb, q), 5.0)).Build())
	// Output:
	// SELECT "items"."id", ($1 - ("items"."embedding" <=> $2::vector(3))) AS "cosine_similarity", (("items"."embedding" <#> $3::vector(3)) * $4) AS "inner_product" FROM "items" AS "items" WHERE (("items"."embedding" <-> $5::vector(3)) < $6)
	// args: [1 [3 1 2] [3 1 2] -1 [3 1 2] 5]
}

// Filtering with an approximate index: a WHERE on another column, and the
// iterative scan setting bound for the transaction so the index keeps
// scanning until LIMIT rows pass the filter. Two statements, one
// transaction.
func Example_filteredWithIterativeScan() {
	show(sel().Exprs(vector.SetHNSWIterativeScan(vector.ScanRelaxedOrder), vector.SetEfSearch(100)).Build())
	q := vector.ParamVector(vector.Vector{3, 1, 2})
	show(sel().Exprs(id).From(items).Where(sqlb.Eq(cat, int32(123))).
		Order(sqlb.OrderBy(vector.L2(emb, q), sqlb.SortAsc)).Limit(5).Build())
	// Output:
	// SELECT set_config('hnsw.iterative_scan', $1, true), set_config('hnsw.ef_search', $2, true)
	// args: [relaxed_order 100]
	// SELECT "items"."id" FROM "items" AS "items" WHERE ("items"."category_id" = $1) ORDER BY ("items"."embedding" <-> $2::vector(3)) ASC LIMIT $3
	// args: [123 [3 1 2] 5]
}

// A relaxed-order scan can return rows slightly out of order. The README's
// fix: a MATERIALIZED CTE for the scan and an outer ORDER BY.
func Example_relaxedOrderStrictResult() {
	q := vector.ParamVector(vector.Vector{3, 1, 2})
	dist := vector.L2(emb, q)
	inner := sel().Exprs(id, sqlb.As(dist, "distance")).From(items).
		Order(sqlb.OrderBy(dist, sqlb.SortAsc)).Limit(5)
	relaxed := sqlb.Table{Name: "relaxed_results", Alias: "relaxed_results"}
	rDist := sqlb.Col[float64](relaxed, "distance")
	show(sel().WithCTE(sqlb.CTE{Name: "relaxed_results", Body: inner, Prefix: pg.Materialized(true)}).
		Exprs(sqlb.Col[int64](relaxed, "id"), rDist).From(relaxed).
		Order(sqlb.OrderBy(rDist, sqlb.SortAsc)).Build())
	// Output:
	// WITH "relaxed_results" AS MATERIALIZED (SELECT "items"."id", ("items"."embedding" <-> $1::vector(3)) AS "distance" FROM "items" AS "items" ORDER BY ("items"."embedding" <-> $2::vector(3)) ASC LIMIT $3)
	// SELECT "relaxed_results"."id", "relaxed_results"."distance" FROM "relaxed_results" AS "relaxed_results" ORDER BY "relaxed_results"."distance" ASC
	// args: [[3 1 2] [3 1 2] 5]
}

// Half-precision: the column is halfvec, or a vector column is cast to
// halfvec so an expression index on the cast serves the search.
func Example_halfPrecision() {
	q := vector.ParamHalf(vector.HalfVector{3, 1, 2})
	show(sel().Exprs(id).From(items).
		Order(sqlb.OrderBy(vector.L2(vector.CastHalf(emb, 3), q), sqlb.SortAsc)).Limit(5).Build())
	show(sel().Exprs(id, vector.L2Norm(half)).From(items).
		Order(sqlb.OrderBy(vector.Cosine(half, q), sqlb.SortAsc)).Limit(5).Build())
	// Output:
	// SELECT "items"."id" FROM "items" AS "items" ORDER BY ("items"."embedding"::halfvec(3) <-> $1::halfvec(3)) ASC LIMIT $2
	// args: [[3 1 2] 5]
	// SELECT "items"."id", l2_norm("items"."embedding") FROM "items" AS "items" ORDER BY ("items"."embedding" <=> $1::halfvec(3)) ASC LIMIT $2
	// args: [[3 1 2] 5]
}

// Binary quantization with re-ranking: the bit index finds candidates by
// Hamming distance, the full vector re-ranks them.
func Example_binaryQuantizeRerank() {
	q := vector.ParamVector(vector.Vector{3, 1, 2})
	candidates := sqlb.Derived(sel().Exprs(id, emb).From(items).
		Order(sqlb.OrderBy(vector.Hamming(vector.CastBit(vector.BinaryQuantize(emb), 3), vector.BinaryQuantize(q)), sqlb.SortAsc)).
		Limit(20), "c")
	c := sqlb.Ref(candidates)
	cID, cEmb := sqlb.Col[int64](c, "id"), sqlb.Col[vector.Vector](c, "embedding")
	show(sel().Exprs(cID).From(candidates).Order(sqlb.OrderBy(vector.L2(cEmb, q), sqlb.SortAsc)).Limit(5).Build())
	// Output:
	// SELECT "c"."id" FROM (SELECT "items"."id", "items"."embedding" FROM "items" AS "items" ORDER BY (binary_quantize("items"."embedding")::bit(3) <~> binary_quantize($1::vector(3))) ASC LIMIT $2) AS "c" ORDER BY ("c"."embedding" <-> $3::vector(3)) ASC LIMIT $4
	// args: [[3 1 2] 20 [3 1 2] 5]
}

// A native bit column: Hamming and Jaccard directly.
func Example_binaryVectors() {
	q := vector.ParamBit("101")
	show(sel().Exprs(id, vector.HammingDistance(bits, q)).From(items).
		Order(sqlb.OrderBy(vector.Jaccard(bits, q), sqlb.SortAsc)).Limit(5).Build())
	// Output:
	// SELECT "items"."id", hamming_distance("items"."embedding", $1::bit(3)) FROM "items" AS "items" ORDER BY ("items"."embedding" <%> $2::bit(3)) ASC LIMIT $3
	// args: [101 101 5]
}

// Subvector indexing: an index on the first dimensions of a Matryoshka
// embedding serves the candidate scan, the full vector re-ranks.
func Example_subvectorRerank() {
	q := vector.ParamVector(vector.Vector{3, 1, 2, 4})
	head := vector.CastVector(vector.Subvector(emb, 1, 2), 2)
	qHead := vector.CastVector(vector.Subvector(q, 1, 2), 2)
	candidates := sqlb.Derived(sel().Exprs(id, emb).From(items).
		Order(sqlb.OrderBy(vector.Cosine(head, qHead), sqlb.SortAsc)).Limit(20), "c")
	c := sqlb.Ref(candidates)
	cID, cEmb := sqlb.Col[int64](c, "id"), sqlb.Col[vector.Vector](c, "embedding")
	show(sel().Exprs(cID).From(candidates).Order(sqlb.OrderBy(vector.Cosine(cEmb, q), sqlb.SortAsc)).Limit(5).Build())
	// Output:
	// SELECT "c"."id" FROM (SELECT "items"."id", "items"."embedding" FROM "items" AS "items" ORDER BY (subvector("items"."embedding", $1, $2)::vector(2) <=> subvector($3::vector(4), $4, $5)::vector(2)) ASC LIMIT $6) AS "c" ORDER BY ("c"."embedding" <=> $7::vector(4)) ASC LIMIT $8
	// args: [1 2 [3 1 2 4] 1 2 20 [3 1 2 4] 5]
}

// Sparse vectors: the same operators over sparsevec, with the text form
// {index:value,...}/dims bound as the parameter.
func Example_sparse() {
	q := vector.ParamSparse(vector.NewSparse([]float32{0, 1, 0, 2, 0}))
	show(sel().Exprs(id).From(items).Order(sqlb.OrderBy(vector.L2(spv, q), sqlb.SortAsc)).Limit(5).Build())
	// Output:
	// SELECT "items"."id" FROM "items" AS "items" ORDER BY ("items"."embedding" <-> $1::sparsevec(5)) ASC LIMIT $2
	// args: [{2:1,4:2}/5 5]
}

// Aggregates and arithmetic: the centroid of a category, and a query
// vector shifted by another.
func Example_aggregatesAndArithmetic() {
	show(sel().Exprs(cat, sqlb.As(vector.Avg(emb), "centroid"), sqlb.As(vector.Sum(emb), "total")).From(items).GroupBy(cat).Build())
	q := vector.ParamVector(vector.Vector{3, 1, 2})
	shift := vector.ParamVector(vector.Vector{0, 0, 1})
	show(sel().Exprs(id).From(items).
		Order(sqlb.OrderBy(vector.L2(emb, vector.L2Normalize(vector.Add(q, shift))), sqlb.SortAsc)).Limit(5).Build())
	// Output:
	// SELECT "items"."category_id", avg("items"."embedding") AS "centroid", sum("items"."embedding") AS "total" FROM "items" AS "items" GROUP BY "items"."category_id"
	// args: []
	// SELECT "items"."id" FROM "items" AS "items" ORDER BY ("items"."embedding" <-> l2_normalize(($1::vector(3) + $2::vector(3)))) ASC LIMIT $3
	// args: [[3 1 2] [0 0 1] 5]
}

// A float array column or literal becomes a vector by cast, the README's
// way in from an existing schema.
func Example_castFromArray() {
	arr := sqlb.Col[[]float32](items, "arr")
	show(sel().Exprs(id, vector.Dims(vector.CastVector(arr, 0))).From(items).
		Where(sqlb.Less(vector.L2(vector.CastVector(arr, 3), vector.ParamVector(vector.Vector{1, 2, 3})), 1.0)).Build())
	// Output:
	// SELECT "items"."id", vector_dims("items"."arr"::vector) FROM "items" AS "items" WHERE (("items"."arr"::vector(3) <-> $1::vector(3)) < $2)
	// args: [[1 2 3] 1]
}

// Hybrid search with Reciprocal Rank Fusion, the README's example: a
// semantic CTE ranked by cosine distance, a keyword CTE ranked by
// ts_rank_cd, a FULL JOIN on id, and 1/(k+rank) summed.
func Example_hybridSearchRRF() {
	docs := sqlb.Table{Name: "documents", Alias: "documents"}
	dID, content, dEmb := sqlb.Col[int64](docs, "id"), sqlb.Col[string](docs, "content"), sqlb.Col[vector.Vector](docs, "embedding")

	q := vector.ParamVector(vector.Vector{3, 1, 2})
	dist := vector.Cosine(dEmb, q)
	semantic := sel().Exprs(dID, sqlb.As(sqlb.Rank().Over(sqlb.NewWindow().OrderBy(sqlb.OrderBy(dist, sqlb.SortAsc))), "rank")).
		From(docs).Order(sqlb.OrderBy(dist, sqlb.SortAsc)).Limit(20)

	tsq := pg.PlainToTSQuery("english", "postgres vector search")
	tsv := pg.ToTSVector("english", content)
	score := pg.TSRankCD(tsv, tsq)
	keyword := sel().Exprs(dID, sqlb.As(sqlb.Rank().Over(sqlb.NewWindow().OrderBy(sqlb.OrderBy(score, sqlb.SortDesc))), "rank")).
		From(docs).Where(pg.Matches(tsv, tsq)).Order(sqlb.OrderBy(score, sqlb.SortDesc)).Limit(20)

	s := sqlb.Table{Name: "semantic_search", Alias: "s"}
	k := sqlb.Table{Name: "keyword_search", Alias: "k"}
	sID, sRank := sqlb.Col[int64](s, "id"), sqlb.Nullable(sqlb.Col[int64](s, "rank"))
	kID, kRank := sqlb.Col[int64](k, "id"), sqlb.Nullable(sqlb.Col[int64](k, "rank"))
	// 1/(k+rank) is NULL for a side the FULL JOIN did not match; COALESCE makes it 0.
	rrf := func(rank sqlb.Expr[*int64]) sqlb.Expr[float64] {
		score := sqlb.Binary[*float64](sqlb.Param(1.0), sqlb.OpDiv, sqlb.Binary[*float64](sqlb.Param(60), sqlb.OpAdd, rank))
		return sqlb.AssertNotNull(sqlb.Coalesce(score, sqlb.Nullable(sqlb.Param(0.0))))
	}
	total := sqlb.Binary[float64](rrf(sRank), sqlb.OpAdd, rrf(kRank))
	show(sel().With("semantic_search", semantic).With("keyword_search", keyword).
		Exprs(sqlb.As(sqlb.Coalesce(sqlb.Nullable(sID), sqlb.Nullable(kID)), "id"), sqlb.As(total, "score")).
		From(s).Join(sqlb.Join{Type: pg.JoinFull, Table: k, On: []sqlb.Predicate{sqlb.EqExpr(sID, kID)}}).
		Order(sqlb.OrderBy(sqlb.OutputCol[float64]("score"), sqlb.SortDesc)).Limit(5).Build())
	// Output:
	// WITH "semantic_search" AS (SELECT "documents"."id", rank() OVER (ORDER BY ("documents"."embedding" <=> $1::vector(3)) ASC) AS "rank" FROM "documents" AS "documents" ORDER BY ("documents"."embedding" <=> $2::vector(3)) ASC LIMIT $3),
	// "keyword_search" AS (SELECT "documents"."id", rank() OVER (ORDER BY ts_rank_cd(to_tsvector('english', "documents"."content"), plainto_tsquery('english', $4)) DESC) AS "rank" FROM "documents" AS "documents" WHERE ((to_tsvector('english', "documents"."content") @@ plainto_tsquery('english', $5))) ORDER BY ts_rank_cd(to_tsvector('english', "documents"."content"), plainto_tsquery('english', $6)) DESC LIMIT $7)
	// SELECT COALESCE("s"."id", "k"."id") AS "id", (COALESCE(($8 / ($9 + "s"."rank")), $10) + COALESCE(($11 / ($12 + "k"."rank")), $13)) AS "score" FROM "semantic_search" AS "s" FULL JOIN "keyword_search" AS "k" ON ("s"."id" = "k"."id") ORDER BY "score" DESC LIMIT $14
	// args: [[3 1 2] [3 1 2] 20 postgres vector search postgres vector search postgres vector search 20 1 60 0 1 60 0 5]
}
