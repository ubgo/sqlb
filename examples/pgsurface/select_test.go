package pgsurface_test

import (
	"github.com/ubgo/sqlb"
	"github.com/ubgo/sqlb/pg"
	"github.com/ubgo/sqlb/pg/vector"
)

// 5.2 WITH

func Example_with_simpleCTE() {
	active := sel().Exprs(UID, UEmail).From(Users).Where(sqlb.Eq(UActive, true))
	a := sqlb.Table{Name: "active", Alias: "a"}
	show(sel().With("active", active).
		Columns(sqlb.Bind(sqlb.Col[string](a, "email"), &sink.s)).
		From(a).Build())
	// Output:
	// WITH "active" AS (SELECT "u"."id", "u"."email" FROM "users" AS "u" WHERE ("u"."active" = $1)) SELECT "a"."email" FROM "active" AS "a"
	// args: [true]
}

func Example_with_twoCTEs() {
	first := sel().Exprs(UID).From(Users).Where(sqlb.Eq(UActive, true))
	f := sqlb.Table{Name: "first", Alias: "f"}
	second := sel().Exprs(sqlb.Col[string](f, "id")).From(f)
	s := sqlb.Table{Name: "second", Alias: "s"}
	show(sel().With("first", first).With("second", second).
		Columns(sqlb.Bind(sqlb.Col[string](s, "id"), &sink.s)).
		From(s).Build())
	// Output:
	// WITH "first" AS (SELECT "u"."id" FROM "users" AS "u" WHERE ("u"."active" = $1)), "second" AS (SELECT "f"."id" FROM "first" AS "f") SELECT "s"."id" FROM "second" AS "s"
	// args: [true]
}

func Example_with_recursive() {
	sub := sqlb.Table{Name: "sub", Alias: "s"}
	subID := sqlb.Col[int64](sub, "id")
	seed := sel().Exprs(CID, CParentID, CName).From(Cats).Where(sqlb.IsNull(CParentID))
	step := sel().Exprs(CID, CParentID, CName).From(Cats).
		Join(sqlb.Join{Type: sqlb.JoinInner, Table: sub, On: []sqlb.Predicate{sqlb.EqExpr(sqlb.Nullable(subID), CParentID)}})
	show(sel().WithRecursive("sub", seed, step).
		Columns(sqlb.Bind(subID, &sink.i64), sqlb.Bind(sqlb.Col[string](sub, "name"), &sink.s)).
		From(sub).Build())
	// Output:
	// WITH RECURSIVE "sub" AS (SELECT "c"."id", "c"."parent_id", "c"."name" FROM "categories" AS "c" WHERE ("c"."parent_id" IS NULL) UNION ALL SELECT "c"."id", "c"."parent_id", "c"."name" FROM "categories" AS "c" INNER JOIN "sub" AS "s" ON ("s"."id" = "c"."parent_id")) SELECT "s"."id", "s"."name" FROM "sub" AS "s"
}

func Example_with_materialized() {
	body := sel().Exprs(UID).From(Users)
	t := sqlb.Table{Name: "t", Alias: "t"}
	show(sel().WithCTE(sqlb.CTE{Name: "t", Body: body, Prefix: pg.Materialized(true)}).
		Columns(sqlb.Bind(sqlb.Col[string](t, "id"), &sink.s)).From(t).Build())
	// Output:
	// WITH "t" AS MATERIALIZED (SELECT "u"."id" FROM "users" AS "u") SELECT "t"."id" FROM "t" AS "t"
}

func Example_with_notMaterialized() {
	body := sel().Exprs(UID).From(Users)
	t := sqlb.Table{Name: "t", Alias: "t"}
	show(sel().WithCTE(sqlb.CTE{Name: "t", Body: body, Prefix: pg.Materialized(false)}).
		Columns(sqlb.Bind(sqlb.Col[string](t, "id"), &sink.s)).From(t).Build())
	// Output:
	// WITH "t" AS NOT MATERIALIZED (SELECT "u"."id" FROM "users" AS "u") SELECT "t"."id" FROM "t" AS "t"
}

func Example_with_dataModifyingCTE() {
	del := sqlb.DeleteReturning(sqlb.NewDelete(d).From(Users).Where(sqlb.IsNotNull(UDeletedAt)), sqlb.Bind(UID, &sink.s))
	gone := sqlb.Table{Name: "gone", Alias: "g"}
	show(sel().With("gone", del).
		Columns(sqlb.Bind(sqlb.Count(), &sink.i64)).From(gone).Build())
	// Output:
	// WITH "gone" AS (DELETE FROM "users" AS "u" WHERE ("u"."deleted_at" IS NOT NULL) RETURNING "u"."id") SELECT count(*) FROM "gone" AS "g"
}

func Example_with_columnAliasList() {
	body := sel().Exprs(UID, UEmail).From(Users)
	t := sqlb.Table{Name: "t", Alias: "t"}
	show(sel().WithCTE(sqlb.CTE{Name: "t", Body: body, Columns: []string{"a", "b"}}).
		Columns(sqlb.Bind(sqlb.Col[string](t, "b"), &sink.s)).From(t).Build())
	// Output:
	// WITH "t" ("a", "b") AS (SELECT "u"."id", "u"."email" FROM "users" AS "u") SELECT "t"."b" FROM "t" AS "t"
}

// 5.3 Target list

func Example_target_columnsAndAlias() {
	show(sel().Columns(
		sqlb.Bind(UID, &sink.s),
		sqlb.Bind(sqlb.As(UEmail, "user_email"), &sink.s2),
	).From(Users).Build())
	// Output:
	// SELECT "u"."id", "u"."email" AS "user_email" FROM "users" AS "u"
}

func Example_target_distinct() {
	show(sel().Distinct().Columns(sqlb.Bind(UPlan, (*Plan)(&sink.s))).From(Users).Build())
	// Output:
	// SELECT DISTINCT "u"."plan" FROM "users" AS "u"
}

func Example_target_distinctOn() {
	show(sel().Clause(pg.DistinctOn(PUserID)).
		Columns(sqlb.Bind(PUserID, &sink.s), sqlb.Bind(PTitle, &sink.s2)).
		From(Posts).
		Order(sqlb.OrderBy(PUserID, sqlb.SortAsc), sqlb.OrderBy(PCreatedAt, sqlb.SortDesc)).Build())
	// Output:
	// SELECT DISTINCT ON ("p"."user_id") "p"."user_id", "p"."title" FROM "posts" AS "p" ORDER BY "p"."user_id" ASC, "p"."created_at" DESC
}

func Example_target_expression() {
	show(sel().Columns(sqlb.Bind(sqlb.Mul(OPrice, sqlb.Cast[float64](OQty, "float8")), &sink.f64)).From(Orders).Build())
	// Output:
	// SELECT ("o"."price" * CAST("o"."qty" AS float8)) FROM "orders" AS "o"
}

func Example_target_scalarSubquery() {
	maxViews := sel().Exprs(sqlb.Max(PViews)).From(Posts).Where(sqlb.EqExpr(PUserID, UID))
	show(sel().Columns(
		sqlb.Bind(UID, &sink.s),
		sqlb.Bind(sqlb.Subquery[int64](maxViews), &sink.i64p),
	).From(Users).Build())
	// Output:
	// SELECT "u"."id", (SELECT max("p"."views") FROM "posts" AS "p" WHERE ("p"."user_id" = "u"."id")) FROM "users" AS "u"
}

func Example_target_arraySubquery() {
	ids := sel().Exprs(PID).From(Posts).Where(sqlb.EqExpr(PUserID, UID))
	show(sel().Columns(sqlb.Bind(UID, &sink.s), sqlb.Bind(pg.ArraySubquery[string](ids), &sink.ss)).From(Users).Build())
	// Output:
	// SELECT "u"."id", ARRAY(SELECT "p"."id" FROM "posts" AS "p" WHERE ("p"."user_id" = "u"."id")) FROM "users" AS "u"
}

func Example_target_rowConstructorAndWholeRow() {
	show(sel().Columns(
		sqlb.Bind(sqlb.As(sqlb.Cast[string](sqlb.New[string](sqlb.Row(UID, UEmail).SQL), "text"), "pair"), &sink.s),
		sqlb.Bind(pg.RowToJSON(Users), &sink.j),
	).From(Users).Build())
	// Output:
	// SELECT CAST(("u"."id", "u"."email") AS text) AS "pair", row_to_json("u") FROM "users" AS "u"
}

func Example_target_functionCastCollate() {
	show(sel().Columns(
		sqlb.Bind(sqlb.Lower(UEmail), &sink.s),
		sqlb.Bind(sqlb.Cast[string](UAge, "text"), &sink.s2),
		sqlb.Bind(pg.Collate(UName, "C"), &sink.s3),
	).From(Users).Build())
	// Output:
	// SELECT lower("u"."email"), CAST("u"."age" AS text), "u"."name" COLLATE "C" FROM "users" AS "u"
}

func Example_target_literal() {
	show(sel().Columns(
		sqlb.Bind(sqlb.Param[int32](1), &sink.i32),
		sqlb.Bind(sqlb.Literal[string]("'a'"), &sink.s),
	).From(Users).Build())
	// Output:
	// SELECT $1, 'a' FROM "users" AS "u"
	// args: [1]
}

// 5.4 FROM

func Example_from_subquery() {
	inner := sel().Exprs(PUserID, sqlb.As(sqlb.Count(), "n")).From(Posts).GroupBy(PUserID)
	t := sqlb.Derived(inner, "t")
	n := sqlb.Col[int64](sqlb.Ref(t), "n")
	show(sel().Columns(sqlb.Bind(n, &sink.i64)).From(t).Where(sqlb.Greater(n, 5)).Build())
	// Output:
	// SELECT "t"."n" FROM (SELECT "p"."user_id", count(*) AS "n" FROM "posts" AS "p" GROUP BY "p"."user_id") AS "t" WHERE ("t"."n" > $1)
	// args: [5]
}

func Example_from_lateral() {
	latest := sel().Exprs(PTitle).From(Posts).Where(sqlb.EqExpr(PUserID, UID)).
		Order(sqlb.OrderBy(PCreatedAt, sqlb.SortDesc)).Limit(1)
	lp := pg.Lateral(sqlb.Derived(latest, "lp"))
	show(sel().Columns(
		sqlb.Bind(UID, &sink.s),
		sqlb.Bind(sqlb.Nullable(sqlb.Col[string](sqlb.Ref(lp), "title")), &sink.sp),
	).From(Users).
		Join(sqlb.Join{Type: sqlb.JoinLeft, Source: lp, On: []sqlb.Predicate{sqlb.True()}}).Build())
	// Output:
	// SELECT "u"."id", "lp"."title" FROM "users" AS "u" LEFT JOIN LATERAL (SELECT "p"."title" FROM "posts" AS "p" WHERE ("p"."user_id" = "u"."id") ORDER BY "p"."created_at" DESC LIMIT $1) AS "lp" ON (TRUE)
	// args: [1]
}

func Example_from_values() {
	v := pg.Values("v", []string{"id", "name"},
		[]sqlb.SQLer{sqlb.Param(1), sqlb.Param("a")},
		[]sqlb.SQLer{sqlb.Param(2), sqlb.Param("b")},
	)
	show(sel().Columns(sqlb.Bind(sqlb.Col[string](sqlb.Ref(v), "name"), &sink.s)).From(v).Build())
	// Output:
	// SELECT "v"."name" FROM (VALUES ($1, $2), ($3, $4)) AS "v" ("id", "name")
	// args: [1 a 2 b]
}

func Example_from_generateSeries() {
	g, n := pg.GenerateSeries(1, 10, 0, "g")
	show(sel().Columns(sqlb.Bind(n, &sink.i64)).From(g).Build())
	// Output:
	// SELECT "g"."n" FROM generate_series(CAST($1 AS bigint), CAST($2 AS bigint)) AS "g" ("n")
	// args: [1 10]
}

func Example_from_unnestWithOrdinality() {
	src, tag, n := pg.UnnestOrdinal(UTags, "t")
	show(sel().Columns(sqlb.Bind(tag, &sink.s), sqlb.Bind(n, &sink.i64)).From(Users).
		Join(sqlb.Join{Type: sqlb.JoinCross, Source: src}).Build())
	// Output:
	// SELECT "t"."elem", "t"."n" FROM "users" AS "u" CROSS JOIN unnest("u"."tags") WITH ORDINALITY AS "t" ("elem", "n")
}

func Example_from_tablesample() {
	show(sel().Columns(sqlb.Bind(UID, &sink.s)).From(Users).Clause(pg.TableSample(pg.SampleBernoulli, 10, -1)).Build())
	// Output:
	// SELECT "u"."id" FROM "users" AS "u" TABLESAMPLE BERNOULLI (10)
}

func Example_from_tablesampleRepeatable() {
	show(sel().Columns(sqlb.Bind(UID, &sink.s)).From(Users).Clause(pg.TableSample(pg.SampleSystem, 5, 42)).Build())
	// Output:
	// SELECT "u"."id" FROM "users" AS "u" TABLESAMPLE SYSTEM (5) REPEATABLE (42)
}

// 5.5 Joins

func Example_join_inner() {
	show(sel().Columns(sqlb.Bind(UID, &sink.s), sqlb.Bind(PTitle, &sink.s2)).From(Users).
		Join(sqlb.Join{Type: sqlb.JoinInner, Table: Posts, On: []sqlb.Predicate{sqlb.EqExpr(PUserID, UID)}}).Build())
	// Output:
	// SELECT "u"."id", "p"."title" FROM "users" AS "u" INNER JOIN "posts" AS "p" ON ("p"."user_id" = "u"."id")
}

func Example_join_leftNullable() {
	show(sel().Columns(sqlb.Bind(UID, &sink.s), sqlb.Bind(sqlb.Nullable(PrBio), &sink.sp)).From(Users).
		Join(sqlb.Join{Type: sqlb.JoinLeft, Table: Profiles, On: []sqlb.Predicate{sqlb.EqExpr(PrUserID, UID)}}).Build())
	// Output:
	// SELECT "u"."id", "pr"."bio" FROM "users" AS "u" LEFT JOIN "profiles" AS "pr" ON ("pr"."user_id" = "u"."id")
}

func Example_join_rightAndFull() {
	show(sel().Columns(sqlb.Bind(sqlb.Nullable(UID), &sink.sp), sqlb.Bind(sqlb.Nullable(PrBio), &sink.sp)).From(Users).
		Join(sqlb.Join{Type: pg.JoinFull, Table: Profiles, On: []sqlb.Predicate{sqlb.EqExpr(PrUserID, UID)}}).Build())
	// Output:
	// SELECT "u"."id", "pr"."bio" FROM "users" AS "u" FULL JOIN "profiles" AS "pr" ON ("pr"."user_id" = "u"."id")
}

func Example_join_cross() {
	show(sel().Columns(sqlb.Bind(UID, &sink.s), sqlb.Bind(CName, &sink.s2)).From(Users).
		Join(sqlb.Join{Type: sqlb.JoinCross, Table: Cats}).Build())
	// Output:
	// SELECT "u"."id", "c"."name" FROM "users" AS "u" CROSS JOIN "categories" AS "c"
}

func Example_join_using() {
	show(sel().Columns(sqlb.Bind(UID, &sink.s)).From(Users).
		Join(sqlb.Join{Type: sqlb.JoinInner, Table: Profiles, Using: []string{"user_id"}}).Build())
	// Output:
	// SELECT "u"."id" FROM "users" AS "u" INNER JOIN "profiles" AS "pr" USING ("user_id")
}

func Example_join_complexCondition() {
	show(sel().Columns(sqlb.Bind(UID, &sink.s)).From(Users).
		Join(sqlb.Join{Type: sqlb.JoinInner, Table: Posts, On: []sqlb.Predicate{
			sqlb.EqExpr(PUserID, UID),
			sqlb.Eq(PPublished, true),
			sqlb.Or(sqlb.Greater(PViews, 100), sqlb.Like(PTitle, "Hello%")),
		}}).Build())
	// Output:
	// SELECT "u"."id" FROM "users" AS "u" INNER JOIN "posts" AS "p" ON ("p"."user_id" = "u"."id" AND "p"."published" = $1 AND ("p"."views" > $2 OR "p"."title" LIKE $3))
	// args: [true 100 Hello%]
}

func Example_join_self() {
	parent := sqlb.Table{Name: "categories", Alias: "parent"}
	parentID := sqlb.Col[int64](parent, "id")
	parentName := sqlb.Col[string](parent, "name")
	show(sel().Columns(sqlb.Bind(CName, &sink.s), sqlb.Bind(sqlb.Nullable(parentName), &sink.sp)).From(Cats).
		Join(sqlb.Join{Type: sqlb.JoinLeft, Table: parent, On: []sqlb.Predicate{sqlb.EqExpr(sqlb.Nullable(parentID), CParentID)}}).Build())
	// Output:
	// SELECT "c"."name", "parent"."name" FROM "categories" AS "c" LEFT JOIN "categories" AS "parent" ON ("parent"."id" = "c"."parent_id")
}

// 5.6 WHERE

func Example_where_comparisonsAndLogic() {
	show(sel().Columns(sqlb.Bind(UID, &sink.s)).From(Users).Where(
		sqlb.Or(
			sqlb.And(sqlb.GreaterEq(UAge, 18), sqlb.Less(UAge, 65)),
			sqlb.Not(sqlb.Eq(UActive, true)),
		),
		sqlb.NotEq(UPlan, PlanPro),
	).Build())
	// Output:
	// SELECT "u"."id" FROM "users" AS "u" WHERE ((("u"."age" >= $1 AND "u"."age" < $2) OR NOT ("u"."active" = $3)) AND "u"."plan" <> $4)
	// args: [18 65 true pro]
}

func Example_where_between() {
	show(sel().Columns(sqlb.Bind(UID, &sink.s)).From(Users).Where(
		sqlb.Between(UCreatedAt, t0, t1),
		sqlb.NotBetween(UAge, 30, 40),
	).Build())
	// Output:
	// SELECT "u"."id" FROM "users" AS "u" WHERE ("u"."created_at" BETWEEN $1 AND $2 AND "u"."age" NOT BETWEEN $3 AND $4)
	// args: [2026-09-01 00:00:00 +0000 UTC 2026-09-16 12:00:00 +0000 UTC 30 40]
}

func Example_where_nullTests() {
	show(sel().Columns(sqlb.Bind(UID, &sink.s)).From(Users).Where(
		sqlb.IsNull(UDeletedAt),
		pg.IsDistinctFrom(UName, sqlb.Param("")),
		pg.IsNotDistinctFrom(UPlan, sqlb.Param(PlanPro)),
		pg.IsTrue(UActive),
		pg.IsUnknown(UActive),
	).Build())
	// Output:
	// SELECT "u"."id" FROM "users" AS "u" WHERE ("u"."deleted_at" IS NULL AND ("u"."name" IS DISTINCT FROM $1) AND ("u"."plan" IS NOT DISTINCT FROM $2) AND "u"."active" IS TRUE AND "u"."active" IS UNKNOWN)
	// args: [ pro]
}

func Example_where_inList() {
	show(sel().Columns(sqlb.Bind(UID, &sink.s)).From(Users).Where(sqlb.In(UPlan, PlanPro, "free")).Build())
	// Output:
	// SELECT "u"."id" FROM "users" AS "u" WHERE ("u"."plan" IN ($1, $2))
	// args: [pro free]
}

func Example_where_inArrayParam() {
	// One array parameter instead of a list: the statement text is stable
	// for any list length.
	show(sel().Columns(sqlb.Bind(UID, &sink.s)).From(Users).Where(pg.InArray(UPlan, []Plan{PlanPro, "free"})).Build())
	// Output:
	// SELECT "u"."id" FROM "users" AS "u" WHERE ("u"."plan" = ANY($1))
	// args: [[pro free]]
}

func Example_where_inSubquery() {
	buyers := sel().Exprs(OUserID).From(Orders).Where(sqlb.Greater(OTotal, 100))
	show(sel().Columns(sqlb.Bind(UID, &sink.s)).From(Users).Where(sqlb.InSubquery(UID, buyers), sqlb.NotInSubquery(UID, sel().Exprs(PUserID).From(Posts))).Build())
	// Output:
	// SELECT "u"."id" FROM "users" AS "u" WHERE ("u"."id" IN (SELECT "o"."user_id" FROM "orders" AS "o" WHERE ("o"."total" > $1)) AND "u"."id" NOT IN (SELECT "p"."user_id" FROM "posts" AS "p"))
	// args: [100]
}

func Example_where_existsCorrelated() {
	has := sel().Exprs(sqlb.Param(1)).From(Posts).Where(sqlb.EqExpr(PUserID, UID), sqlb.Eq(PPublished, true))
	show(sel().Columns(sqlb.Bind(UID, &sink.s)).From(Users).Where(sqlb.Exists(has), sqlb.NotExists(sel().Exprs(sqlb.Param(1)).From(Orders).Where(sqlb.EqExpr(OUserID, UID)))).Build())
	// Output:
	// SELECT "u"."id" FROM "users" AS "u" WHERE (EXISTS (SELECT $1 FROM "posts" AS "p" WHERE ("p"."user_id" = "u"."id" AND "p"."published" = $2)) AND NOT EXISTS (SELECT $3 FROM "orders" AS "o" WHERE ("o"."user_id" = "u"."id")))
	// args: [1 true 1]
}

func Example_where_anyAllSubquery() {
	totals := sel().Exprs(OTotal).From(Orders)
	show(sel().Columns(sqlb.Bind(OID, &sink.s)).From(Orders).Where(
		pg.AnyOf(OTotal, sqlb.OpGreater, totals),
		pg.AllOf(OTotal, sqlb.OpLessEq, totals),
	).Build())
	// Output:
	// SELECT "o"."id" FROM "orders" AS "o" WHERE ("o"."total" > ANY (SELECT "o"."total" FROM "orders" AS "o") AND "o"."total" <= ALL (SELECT "o"."total" FROM "orders" AS "o"))
}

func Example_where_rowComparisonKeyset() {
	show(sel().Columns(sqlb.Bind(PID, &sink.s)).From(Posts).
		Where(sqlb.RowLess(sqlb.Row(PCreatedAt, PID), sqlb.Row(sqlb.Param(t1), sqlb.Param("post_9")))).
		Order(sqlb.OrderBy(PCreatedAt, sqlb.SortDesc), sqlb.OrderBy(PID, sqlb.SortDesc)).
		Limit(20).Build())
	// Output:
	// SELECT "p"."id" FROM "posts" AS "p" WHERE (("p"."created_at", "p"."id") < ($1, $2)) ORDER BY "p"."created_at" DESC, "p"."id" DESC LIMIT $3
	// args: [2026-09-16 12:00:00 +0000 UTC post_9 20]
}

// 5.7 GROUP BY and HAVING

func Example_group_byExpressionHaving() {
	day := pg.DateTrunc(pg.FieldDay, OCreatedAt)
	show(sel().Columns(sqlb.Bind(day, &sink.t), sqlb.Bind(sqlb.Sum(OTotal), &sink.f64)).From(Orders).
		GroupBy(day).Having(sqlb.Greater(sqlb.Sum(OTotal), 1000)).
		Order(sqlb.OrderBy(day, sqlb.SortAsc)).Build())
	// Output:
	// SELECT date_trunc('day', "o"."created_at"), sum("o"."total") FROM "orders" AS "o" GROUP BY date_trunc('day', "o"."created_at") HAVING (sum("o"."total") > $1) ORDER BY date_trunc('day', "o"."created_at") ASC
	// args: [1000]
}

func Example_group_groupingSets() {
	show(sel().Columns(
		sqlb.Bind(sqlb.Nullable(OStatus), &sink.sp),
		sqlb.Bind(sqlb.Nullable(OUserID), &sink.sp),
		sqlb.Bind(sqlb.Count(), &sink.i64),
		sqlb.Bind(pg.Grouping(OStatus), &sink.i32),
	).From(Orders).
		GroupBy(pg.GroupingSets([]sqlb.SQLer{OStatus}, []sqlb.SQLer{OUserID}, nil)).Build())
	// Output:
	// SELECT "o"."status", "o"."user_id", count(*), grouping("o"."status") FROM "orders" AS "o" GROUP BY GROUPING SETS (("o"."status"), ("o"."user_id"), ())
}

func Example_group_cubeRollup() {
	show(sel().Columns(sqlb.Bind(sqlb.Count(), &sink.i64)).From(Orders).
		GroupBy(pg.Cube(OStatus, OUserID)).Build())
	show(sel().Columns(sqlb.Bind(sqlb.Count(), &sink.i64)).From(Orders).
		GroupBy(pg.Rollup(OStatus, OUserID)).Build())
	// Output:
	// SELECT count(*) FROM "orders" AS "o" GROUP BY CUBE ("o"."status", "o"."user_id")
	// SELECT count(*) FROM "orders" AS "o" GROUP BY ROLLUP ("o"."status", "o"."user_id")
}

// 5.8 WINDOW

func Example_window_inline() {
	w := sqlb.NewWindow().PartitionBy(PUserID).OrderBy(sqlb.OrderBy(PViews, sqlb.SortDesc))
	show(sel().Columns(
		sqlb.Bind(PID, &sink.s),
		sqlb.Bind(sqlb.Rank().Over(w), &sink.i64),
		sqlb.Bind(sqlb.Over(sqlb.Sum(PViews), w), &sink.i64),
	).From(Posts).Build())
	// Output:
	// SELECT "p"."id", rank() OVER (PARTITION BY "p"."user_id" ORDER BY "p"."views" DESC), sum("p"."views") OVER (PARTITION BY "p"."user_id" ORDER BY "p"."views" DESC) FROM "posts" AS "p"
}

func Example_window_named() {
	w := sqlb.NewWindow().PartitionBy(PUserID).OrderBy(sqlb.OrderBy(PCreatedAt, sqlb.SortAsc))
	show(sel().Columns(
		sqlb.Bind(sqlb.RowNumber().OverNamed("w"), &sink.i64),
		sqlb.Bind(pg.Lag(PViews, 1).OverNamed("w"), &sink.i64p),
		sqlb.Bind(pg.LeadDefault(PViews, 1, sqlb.Param[int64](0)).OverNamed("w"), &sink.i64),
	).From(Posts).Window("w", w).Build())
	// Output:
	// SELECT row_number() OVER "w", lag("p"."views", $1) OVER "w", lead("p"."views", $2, $3) OVER "w" FROM "posts" AS "p" WINDOW "w" AS (PARTITION BY "p"."user_id" ORDER BY "p"."created_at" ASC)
	// args: [1 1 0]
}

func Example_window_frames() {
	w := sqlb.NewWindow().OrderBy(sqlb.OrderBy(OCreatedAt, sqlb.SortAsc)).
		Frame(sqlb.FrameRows, sqlb.Preceding(6), sqlb.CurrentRow())
	w2 := sqlb.NewWindow().OrderBy(sqlb.OrderBy(OCreatedAt, sqlb.SortAsc)).
		Frame(sqlb.FrameRange, sqlb.UnboundedPreceding(), sqlb.CurrentRow()).Exclude(pg.ExcludeCurrentRow)
	w3 := sqlb.NewWindow().OrderBy(sqlb.OrderBy(OCreatedAt, sqlb.SortAsc)).
		Frame(pg.FrameGroups, sqlb.Preceding(1), sqlb.Following(1))
	show(sel().Columns(
		sqlb.Bind(sqlb.Over(sqlb.Avg(OTotal), w), &sink.f64),
		sqlb.Bind(sqlb.Over(sqlb.Sum(OTotal), w2), &sink.f64),
		sqlb.Bind(sqlb.FirstValue(OID).Over(w3), &sink.sp),
		sqlb.Bind(sqlb.NthValue(OID, 2).Over(w3), &sink.sp),
		sqlb.Bind(pg.Ntile(4).Over(w), &sink.i32),
		sqlb.Bind(pg.PercentRank().Over(w), &sink.f64),
	).From(Orders).Build())
	// Output:
	// SELECT avg("o"."total") OVER (ORDER BY "o"."created_at" ASC ROWS BETWEEN 6 PRECEDING AND CURRENT ROW), sum("o"."total") OVER (ORDER BY "o"."created_at" ASC RANGE BETWEEN UNBOUNDED PRECEDING AND CURRENT ROW EXCLUDE CURRENT ROW), first_value("o"."id") OVER (ORDER BY "o"."created_at" ASC GROUPS BETWEEN 1 PRECEDING AND 1 FOLLOWING), nth_value("o"."id", $1) OVER (ORDER BY "o"."created_at" ASC GROUPS BETWEEN 1 PRECEDING AND 1 FOLLOWING), ntile($2) OVER (ORDER BY "o"."created_at" ASC ROWS BETWEEN 6 PRECEDING AND CURRENT ROW), percent_rank() OVER (ORDER BY "o"."created_at" ASC ROWS BETWEEN 6 PRECEDING AND CURRENT ROW) FROM "orders" AS "o"
	// args: [2 4]
}

// 5.9 ORDER BY

func Example_order_nullsAndUsingAndCollate() {
	show(sel().Columns(sqlb.Bind(UID, &sink.s)).From(Users).Order(
		pg.OrderByNulls(UDeletedAt, sqlb.SortDesc, false),
		sqlb.OrderBy(pg.Collate(UName, "de_DE"), sqlb.SortAsc),
		sqlb.OrderBy(sqlb.Add(UAge, sqlb.Param[int32](1)), sqlb.SortAsc),
	).Build())
	// Output:
	// SELECT "u"."id" FROM "users" AS "u" ORDER BY "u"."deleted_at" DESC NULLS LAST, "u"."name" COLLATE "de_DE" ASC, ("u"."age" + $1) ASC
	// args: [1]
}

func Example_order_usingOperator() {
	show(sel().Columns(sqlb.Bind(DcID, &sink.s)).From(Docs).
		Order(pg.OrderUsing(vector.InnerProduct(DcEmbedding, sqlb.Param(vector.Vector{1, 2})), "<")).Limit(5).Build())
	// Output:
	// SELECT "dc"."id" FROM "docs" AS "dc" ORDER BY ("dc"."embedding" <#> $1) USING < LIMIT $2
	// args: [[1 2] 5]
}

// 5.10 LIMIT, OFFSET, FETCH

func Example_limit_offsetFetchTies() {
	show(sel().Columns(sqlb.Bind(UID, &sink.s)).From(Users).Order(sqlb.OrderBy(UAge, sqlb.SortDesc)).Offset(10).Clause(pg.FetchFirst(5, true)).Build())
	show(sel().Columns(sqlb.Bind(UID, &sink.s)).From(Users).Clause(pg.LimitAll()).Offset(3).Build())
	// Output:
	// SELECT "u"."id" FROM "users" AS "u" ORDER BY "u"."age" DESC OFFSET $1 FETCH FIRST $2 ROWS WITH TIES
	// args: [10 5]
	// SELECT "u"."id" FROM "users" AS "u" LIMIT ALL OFFSET $1
	// args: [3]
}

// 5.11 Row locking

func Example_lock_variants() {
	show(sel().Columns(sqlb.Bind(JID, &sink.s)).From(Jobs).Where(sqlb.LessEq(JRunAt, t1)).
		Order(sqlb.OrderBy(JRunAt, sqlb.SortAsc)).Limit(1).Clause(pg.ForUpdate(pg.SkipLocked())).Build())
	show(sel().Columns(sqlb.Bind(UID, &sink.s)).From(Users).
		Join(sqlb.Join{Type: sqlb.JoinInner, Table: Profiles, On: []sqlb.Predicate{sqlb.EqExpr(PrUserID, UID)}}).
		Clause(pg.ForNoKeyUpdate(pg.Of(Users), pg.NoWait())).Build())
	show(sel().Columns(sqlb.Bind(UID, &sink.s)).From(Users).Clause(pg.ForShare()).Build())
	show(sel().Columns(sqlb.Bind(UID, &sink.s)).From(Users).Clause(pg.ForKeyShare()).Build())
	// Output:
	// SELECT "j"."id" FROM "jobs" AS "j" WHERE ("j"."run_at" <= $1) ORDER BY "j"."run_at" ASC LIMIT $2 FOR UPDATE SKIP LOCKED
	// args: [2026-09-16 12:00:00 +0000 UTC 1]
	// SELECT "u"."id" FROM "users" AS "u" INNER JOIN "profiles" AS "pr" ON ("pr"."user_id" = "u"."id") FOR NO KEY UPDATE OF "u" NOWAIT
	// SELECT "u"."id" FROM "users" AS "u" FOR SHARE
	// SELECT "u"."id" FROM "users" AS "u" FOR KEY SHARE
}

// 6. Set operations

func Example_setops_all() {
	a := sel().Exprs(UEmail).From(Users)
	b := sel().Exprs(PTitle).From(Posts)
	show(sel().Columns(sqlb.Bind(sqlb.As(UEmail, "email"), &sink.s)).From(Users).Union(b).
		Order(sqlb.OrderBy(sqlb.OutputCol[string]("email"), sqlb.SortAsc)).Limit(10).Build())
	show(sel().Columns(sqlb.Bind(UEmail, &sink.s)).From(Users).UnionAll(b).Build())
	show(sel().Columns(sqlb.Bind(UEmail, &sink.s)).From(Users).Intersect(b).Build())
	show(pg.IntersectAll(sel().Columns(sqlb.Bind(UEmail, &sink.s)).From(Users), a).Build())
	show(sel().Columns(sqlb.Bind(UEmail, &sink.s)).From(Users).Except(b).Build())
	show(pg.ExceptAll(sel().Columns(sqlb.Bind(UEmail, &sink.s)).From(Users), a).Build())
	// Output:
	// SELECT "u"."email" AS "email" FROM "users" AS "u" UNION SELECT "p"."title" FROM "posts" AS "p" ORDER BY "email" ASC LIMIT $1
	// args: [10]
	// SELECT "u"."email" FROM "users" AS "u" UNION ALL SELECT "p"."title" FROM "posts" AS "p"
	// SELECT "u"."email" FROM "users" AS "u" INTERSECT SELECT "p"."title" FROM "posts" AS "p"
	// SELECT "u"."email" FROM "users" AS "u" INTERSECT ALL SELECT "u"."email" FROM "users" AS "u"
	// SELECT "u"."email" FROM "users" AS "u" EXCEPT SELECT "p"."title" FROM "posts" AS "p"
	// SELECT "u"."email" FROM "users" AS "u" EXCEPT ALL SELECT "u"."email" FROM "users" AS "u"
}
