package snippets_test

import (
	"encoding/json"

	"github.com/ubgo/sqlb"
	"github.com/ubgo/sqlb/pg"
)

// ## 7. Arrays

// ### 7.1 Facts that bite
func Example_s7_01_arrayFacts() {
	empty := pg.EmptyArray[int32]("int")
	withNull := pg.ArrayOf(sqlb.Literal[int32]("NULL"), sqlb.Param[int32](1))
	three := pg.Array[int32](1, 2, 3)
	show(sel().Exprs(
		pg.ArrayLength(empty, 1), pg.Cardinality(empty),
		sqlb.BoolExpr(pg.AnyOp(withNull, sqlb.OpEq, int32(1))), sqlb.BoolExpr(pg.AnyOp(withNull, sqlb.OpEq, int32(2))),
		sqlb.BoolExpr(pg.AllOp(withNull, sqlb.OpNotEq, int32(2))),
		pg.Subscript(pg.ArrayLit[string]("[0:1]={a,b}", "text"), 1),
		pg.SliceFrom(three, 2), pg.SliceTo(three, 2),
	).Build())
	// Output:
	// SELECT array_length('{}'::int[], $1), cardinality('{}'::int[]), $2 = ANY(ARRAY[NULL, $3]), $4 = ANY(ARRAY[NULL, $5]), $6 <> ALL(ARRAY[NULL, $7]), '[0:1]={a,b}'::text[][$8], (ARRAY[CAST($9 AS int), CAST($10 AS int), CAST($11 AS int)])[$12:], (ARRAY[CAST($13 AS int), CAST($14 AS int), CAST($15 AS int)])[:$16]
	// args: 16
}

// ### 7.2 Dedup preserving first-seen order
func Example_s7_02_dedupPreservingOrder() {
	src := pg.WithOrdinality(pg.FuncSource(sqlb.Fn[int32]("unnest", sqlb.Cast[[]int32](sqlb.Param([]int32{3, 1, 3}), "int[]")), "t", "x", "ord"))
	x, ord := ref[int32](src, "x"), ref[int64](src, "ord")
	first := sqlb.Derived(sel().Clause(pg.DistinctOn(x)).Exprs(x, ord).From(src).Order(asc(x), asc(ord)), "s")
	show(sel().Exprs(pg.ArrayAgg(ref[int32](first, "x"), pg.OrderWithin(asc(ref[int64](first, "ord"))))).From(first).Build())
	// Output:
	// SELECT array_agg("s"."x" ORDER BY "s"."ord" ASC) FROM (SELECT DISTINCT ON ("t"."x") "t"."x", "t"."ord" FROM unnest(CAST($1 AS int[])) WITH ORDINALITY AS "t" ("x", "ord") ORDER BY "t"."x" ASC, "t"."ord" ASC) AS "s"
	// args: 1
}

// ### 7.3 Set operations on arrays
func Example_s7_03_arraySetOperations() {
	v := pg.Values("v", []string{"a", "b"}, []sqlb.SQLer{sqlb.Literal[[]int32]("ARRAY[1,2,3,3,NULL]"), sqlb.Literal[[]int32]("ARRAY[3,4]")})
	a, b := ref[[]int32](v, "a"), ref[[]int32](v, "b")
	un := func(arr sqlb.Expr[[]int32]) *sqlb.Select[sqlb.PostgresDialect] {
		return sel().Exprs(pg.UnnestScalar(arr))
	}
	show(sel().Exprs(
		sqlb.As(pg.ArraySubquery[int32](un(a).Intersect(un(b))), "common"),
		sqlb.As(pg.ArraySubquery[int32](un(a).Except(un(b))), "only_in_a"),
		sqlb.As(sqlb.BoolExpr(pg.OverlapsExpr(a, b)), "overlaps"),
		sqlb.As(sqlb.BoolExpr(pg.ArrayContainsExpr(a, b)), "a_contains_b"),
		sqlb.As(sqlb.BoolExpr(pg.ArrayContainedByExpr(a, b)), "a_within_b"),
		sqlb.As(pg.ArrayRemoveNull(a), "without_nulls"),
		pg.ArrayReplace(a, 2, 20), pg.ArrayPosition(a, 3), pg.ArrayPositions(a, 3),
	).From(v).Build())
	// Output:
	// SELECT ARRAY(SELECT unnest("v"."a") INTERSECT SELECT unnest("v"."b")) AS "common", ARRAY(SELECT unnest("v"."a") EXCEPT SELECT unnest("v"."b")) AS "only_in_a", ("v"."a" && "v"."b") AS "overlaps", ("v"."a" @> "v"."b") AS "a_contains_b", ("v"."a" <@ "v"."b") AS "a_within_b", array_remove("v"."a", NULL) AS "without_nulls", array_replace("v"."a", $1, $2), array_position("v"."a", $3), array_positions("v"."a", $4) FROM (VALUES (ARRAY[1,2,3,3,NULL], ARRAY[3,4])) AS "v" ("a", "b")
	// args: 4
}

// ### 7.4 Rows whose array contains duplicates
func Example_s7_04_arrayWithDuplicates() {
	t := tbl("t", "t")
	tags := sqlb.Col[[]string](t, "tags")
	un, x := pg.Unnest(tags, "x")
	distinct := sel().Exprs(sqlb.CountOf(x, sqlb.Distinct())).From(un)
	show(sel().Exprs(tags).From(t).Where(sqlb.NotEqExpr(sqlb.Cast[int64](pg.Cardinality(tags), "bigint"), sqlb.AssertNotNull(sqlb.Subquery[int64](distinct)))).Build())
	// Output:
	// SELECT "t"."tags" FROM "t" AS "t" WHERE (CAST(cardinality("t"."tags") AS bigint) <> (SELECT count(DISTINCT "x"."elem") FROM unnest("t"."tags") AS "x" ("elem")))
}

// ### 7.5 Arrays as parameters instead of IN lists
func Example_s7_05_arrayParameters() {
	u := tbl("users", "users")
	id := sqlb.Col[int32](u, "id")
	show(sel().Exprs(id).From(u).Where(pg.InArray(id, []int32{1, 2, 3})).Build())
	show(sel().Exprs(id).From(u).Where(pg.AllOp(sqlb.Param([]int32{1, 2}), sqlb.OpNotEq, int32(0)), sqlb.IsNotNull(id)).Build())
	// The second form is "id <> ALL ($1)": pg.AllOp puts the value on the left, so the
	// snippet's exact spelling uses the typed door.
	show(sel().Exprs(id).From(u).Where(pg.AllExpr(id, sqlb.OpNotEq, sqlb.Param([]int32{1, 2})), sqlb.IsNotNull(id)).Build())
	// Output:
	// SELECT "users"."id" FROM "users" AS "users" WHERE ("users"."id" = ANY($1))
	// args: 1
	// SELECT "users"."id" FROM "users" AS "users" WHERE ($1 <> ALL($2) AND "users"."id" IS NOT NULL)
	// args: 2
	// SELECT "users"."id" FROM "users" AS "users" WHERE ("users"."id" <> ALL($1) AND "users"."id" IS NOT NULL)
	// args: 1
}

// ### 7.6 Indexing arrays
// CREATE INDEX is DDL. The two SELECTs show the operator the GIN index serves.
func Example_s7_06_indexingArrays() {
	p := tbl("posts", "posts")
	tags := sqlb.Col[[]string](p, "tags")
	show(sel().Exprs(tags).From(p).Where(pg.ArrayContains(tags, []string{"postgres"})).Build())
	show(sel().Exprs(tags).From(p).Where(pg.Any(tags, "postgres")).Build())
	// Output:
	// SELECT "posts"."tags" FROM "posts" AS "posts" WHERE (("posts"."tags" @> $1))
	// args: 1
	// SELECT "posts"."tags" FROM "posts" AS "posts" WHERE ($1 = ANY("posts"."tags"))
	// args: 1
}

// ### 7.7 JSON array to typed array and back
func Example_s7_07_jsonArrayToTypedArray() {
	elems, el := pg.ArrayElementsText(sqlb.Param(json.RawMessage(`[1,2,3]`)), "e")
	show(sel().Exprs(pg.ArraySubquery[int32](sel().Exprs(sqlb.Cast[int32](el, "int")).From(elems))).Build())
	show(sel().Exprs(pg.ToJSONB(pg.Array(1, 2, 3))).Build())
	show(sel().Exprs(pg.StringToArrayNull(sqlb.Param("a,,b"), ",", "")).Build())
	parts, part := pg.StringToTable(sqlb.Param("a b c"), " ", "p")
	show(sel().Exprs(part).From(parts).Build())
	// Output:
	// SELECT ARRAY(SELECT CAST("e"."value" AS int) FROM jsonb_array_elements_text($1) AS "e" ("value"))
	// args: 1
	// SELECT to_jsonb(ARRAY[CAST($1 AS bigint), CAST($2 AS bigint), CAST($3 AS bigint)])
	// args: 3
	// SELECT string_to_array($1, $2, $3)
	// args: 3
	// SELECT "p"."part" FROM string_to_table($1, $2) AS "p" ("part")
	// args: 2
}
