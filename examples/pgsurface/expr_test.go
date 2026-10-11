package pgsurface_test

import (
	"encoding/json"

	"github.com/ubgo/sqlb"
	"github.com/ubgo/sqlb/pg"
)

// 11.1 Numeric

func Example_numeric_arithmetic() {
	show(sel().Columns(
		sqlb.Bind(sqlb.Add(OQty, sqlb.Param[int32](1)), &sink.i32),
		sqlb.Bind(sqlb.Sub(OTotal, OPrice), &sink.f64),
		sqlb.Bind(sqlb.Mul(OPrice, sqlb.Param(1.2)), &sink.f64),
		sqlb.Bind(sqlb.Div(OQty, sqlb.Param[int32](2)), &sink.i32),
		sqlb.Bind(pg.DivExact(OQty, sqlb.Param[int32](2)), &sink.f64),
		sqlb.Bind(sqlb.Mod(OQty, sqlb.Param[int32](3)), &sink.i32),
		sqlb.Bind(sqlb.Neg(OTotal), &sink.f64),
	).From(Orders).Build())
	// Output:
	// SELECT ("o"."qty" + $1), ("o"."total" - "o"."price"), ("o"."price" * $2), ("o"."qty" / $3), ("o"."qty"::numeric / $4), ("o"."qty" % $5), (-"o"."total") FROM "orders" AS "o"
	// args: [1 1.2 2 2 3]
}

func Example_numeric_functions() {
	show(sel().Columns(
		sqlb.Bind(pg.Abs(OTotal), &sink.f64),
		sqlb.Bind(pg.Round(OTotal, 2), &sink.f64),
		sqlb.Bind(pg.Floor(OTotal), &sink.f64),
		sqlb.Bind(pg.Ceil(OTotal), &sink.f64),
		sqlb.Bind(pg.Trunc(OTotal, 0), &sink.f64),
		sqlb.Bind(pg.Power(OTotal, 2), &sink.f64),
		sqlb.Bind(pg.Sqrt(OTotal), &sink.f64),
		sqlb.Bind(pg.Ln(OTotal), &sink.f64),
		sqlb.Bind(pg.WidthBucket(OTotal, 0, 100, 10), &sink.i32),
		sqlb.Bind(pg.Random(), &sink.f64),
	).From(Orders).Build())
	// Output:
	// SELECT abs("o"."total"), round("o"."total"::numeric, $1), floor("o"."total"), ceil("o"."total"), trunc("o"."total"::numeric, $2), power("o"."total", $3), sqrt("o"."total"), ln("o"."total"), width_bucket("o"."total", $4, $5, $6), random() FROM "orders" AS "o"
	// args: [2 0 2 0 100 10]
}

// 11.2 Text

func Example_text_patterns() {
	show(sel().Columns(sqlb.Bind(UID, &sink.s)).From(Users).Where(
		sqlb.Like(UEmail, "%@example.com"),
		pg.ILike(UName, "ada%"),
		pg.LikeEscape(UName, "100\\%", "\\"),
		pg.SimilarTo(UName, "(A|B)%"),
		pg.Regex(UName, "^A"),
		pg.RegexI(UName, "^a"),
		pg.NotRegex(UEmail, "@spam\\."),
	).Build())
	// Output:
	// SELECT "u"."id" FROM "users" AS "u" WHERE ("u"."email" LIKE $1 AND ("u"."name" ILIKE $2) AND "u"."name" LIKE $3 ESCAPE $4 AND ("u"."name" SIMILAR TO $5) AND ("u"."name" ~ $6) AND ("u"."name" ~* $7) AND ("u"."email" !~ $8))
	// args: [%@example.com ada% 100\% \ (A|B)% ^A ^a @spam\.]
}

func Example_text_functions() {
	show(sel().Columns(
		sqlb.Bind(sqlb.Concat(UName, sqlb.Param(" <"), UEmail, sqlb.Param(">")), &sink.s),
		sqlb.Bind(pg.ConcatOp(UName, UEmail), &sink.s),
		sqlb.Bind(pg.Upper(UName), &sink.s),
		sqlb.Bind(pg.Length(UName), &sink.i32),
		sqlb.Bind(pg.Substring(UEmail, 1, 3), &sink.s),
		sqlb.Bind(pg.Left(UEmail, 5), &sink.s),
		sqlb.Bind(pg.Trim(UName), &sink.s),
		sqlb.Bind(pg.LPad(UName, 10, "*"), &sink.s),
		sqlb.Bind(pg.Position(UEmail, "@"), &sink.i32),
		sqlb.Bind(pg.Replace(UEmail, "@", " at "), &sink.s),
		sqlb.Bind(pg.SplitPart(UEmail, "@", 2), &sink.s),
		sqlb.Bind(pg.ConcatWs("-", UName, UEmail), &sink.s),
		sqlb.Bind(pg.MD5(UEmail), &sink.s),
		sqlb.Bind(pg.SHA256Hex(UEmail), &sink.s),
		sqlb.Bind(pg.RegexpReplace(UName, "\\s+", " ", "g"), &sink.s),
		sqlb.Bind(pg.RegexpMatch(UEmail, "^(.*)@"), &sink.ss),
		sqlb.Bind(pg.Format("%s <%s>", UName, UEmail), &sink.s),
		sqlb.Bind(pg.ToChar(UCreatedAt, "YYYY-MM-DD"), &sink.s),
	).From(Users).Build())
	// Output:
	// SELECT concat("u"."name", CAST($1 AS text), "u"."email", CAST($2 AS text)), ("u"."name" || "u"."email"), upper("u"."name"), length("u"."name"), substring("u"."email", CAST($3 AS int), CAST($4 AS int)), left("u"."email", $5), btrim("u"."name"), lpad("u"."name", $6, $7), strpos("u"."email", $8), replace("u"."email", $9, $10), split_part("u"."email", $11, $12), concat_ws($13, "u"."name", "u"."email"), md5("u"."email"), encode(sha256("u"."email"::bytea), 'hex'), regexp_replace("u"."name", $14, $15, $16), regexp_match("u"."email", $17), format($18, "u"."name", "u"."email"), to_char("u"."created_at", $19) FROM "users" AS "u"
	// args: [ < > 1 3 5 10 * @ @  at  @ 2 - \s+   g ^(.*)@ %s <%s> YYYY-MM-DD]
}

// 11.3 Date and time

func Example_time_functions() {
	show(sel().Columns(
		sqlb.Bind(pg.Now(), &sink.t),
		sqlb.Bind(pg.CurrentDate(), &sink.t),
		sqlb.Bind(pg.DateTrunc(pg.FieldMonth, UCreatedAt), &sink.t),
		sqlb.Bind(pg.Extract(pg.FieldEpoch, UCreatedAt), &sink.f64),
		sqlb.Bind(pg.AtTimeZone(UCreatedAt, "Europe/Berlin"), &sink.t),
		sqlb.Bind(pg.AddInterval(UCreatedAt, "1 day"), &sink.t),
		sqlb.Bind(pg.Diff(pg.Now(), UCreatedAt), &sink.iv),
		sqlb.Bind(pg.Age(pg.Now(), UCreatedAt), &sink.iv),
		sqlb.Bind(pg.DateBin("15 minutes", UCreatedAt, t0), &sink.t),
		sqlb.Bind(pg.ToTimestamp(UName, "YYYY-MM-DD"), &sink.t),
	).From(Users).Where(
		sqlb.Greater(UCreatedAt, t0),
		pg.OverlapsPeriod(UCreatedAt, pg.Now(), sqlb.Param(t0), sqlb.Param(t1)),
	).Build())
	// Output:
	// SELECT now(), current_date, date_trunc('month', "u"."created_at"), extract(epoch FROM "u"."created_at"), "u"."created_at" AT TIME ZONE $1, ("u"."created_at" + $2::interval), (now() - "u"."created_at"), age(now(), "u"."created_at"), date_bin($3::interval, "u"."created_at", $4), to_timestamp("u"."name", $5) FROM "users" AS "u" WHERE ("u"."created_at" > $6 AND ("u"."created_at", now()) OVERLAPS ($7, $8))
	// args: [Europe/Berlin 1 day 15 minutes 2026-09-01 00:00:00 +0000 UTC YYYY-MM-DD 2026-09-01 00:00:00 +0000 UTC 2026-09-01 00:00:00 +0000 UTC 2026-09-16 12:00:00 +0000 UTC]
}

// 11.4 Arrays

func Example_array_operators() {
	show(sel().Columns(
		sqlb.Bind(pg.Subscript(UTags, 1), &sink.sp),
		sqlb.Bind(pg.Slice(UTags, 1, 3), &sink.ss),
		sqlb.Bind(pg.Cardinality(UTags), &sink.i32),
		sqlb.Bind(pg.ArrayAppend(UTags, "new"), &sink.ss),
		sqlb.Bind(pg.ArrayRemove(UTags, "old"), &sink.ss),
		sqlb.Bind(pg.ArrayToString(UTags, ","), &sink.s),
		sqlb.Bind(pg.ArrayConcat(UTags, pg.Array("x", "y")), &sink.ss),
		sqlb.Bind(pg.ArrayPosition(UTags, "admin"), &sink.i32p),
	).From(Users).Where(
		pg.ArrayContains(UTags, []string{"admin"}),
		pg.Overlaps(UTags, []string{"a", "b"}),
		pg.Any(UTags, "staff"),
		pg.AllOp(UTags, sqlb.OpNotEq, "banned"),
	).Build())
	// Output:
	// SELECT "u"."tags"[$1], "u"."tags"[$2:$3], cardinality("u"."tags"), array_append("u"."tags", $4), array_remove("u"."tags", $5), array_to_string("u"."tags", $6), ("u"."tags" || ARRAY[CAST($7 AS text), CAST($8 AS text)]), array_position("u"."tags", $9) FROM "users" AS "u" WHERE (("u"."tags" @> $10) AND ("u"."tags" && $11) AND $12 = ANY("u"."tags") AND $13 <> ALL("u"."tags"))
	// args: [1 1 3 new old , x y admin [admin] [a b] staff banned]
}

func Example_array_unnestSource() {
	src, tag := pg.Unnest(UTags, "t")
	show(sel().Columns(sqlb.Bind(tag, &sink.s), sqlb.Bind(sqlb.Count(), &sink.i64)).From(Users).
		Join(sqlb.Join{Type: sqlb.JoinCross, Source: src}).
		GroupBy(tag).Order(sqlb.OrderBy(sqlb.Count(), sqlb.SortDesc)).Build())
	// Output:
	// SELECT "t"."elem", count(*) FROM "users" AS "u" CROSS JOIN unnest("u"."tags") AS "t" ("elem") GROUP BY "t"."elem" ORDER BY count(*) DESC
}

// 11.5 JSON and JSONB

func Example_json_access() {
	show(sel().Columns(
		sqlb.Bind(pg.Get(UMeta, "plan"), &sink.j),
		sqlb.Bind(pg.JSONText(UMeta, "plan"), &sink.s),
		sqlb.Bind(pg.PathGet(UMeta, []string{"limits", "seats"}), &sink.j),
		sqlb.Bind(pg.PathText(UMeta, []string{"limits", "seats"}), &sink.s),
		sqlb.Bind(pg.GetIndex(UMeta, 0), &sink.j),
		sqlb.Bind(pg.JSONBTypeof(UMeta), &sink.s),
		sqlb.Bind(pg.PathQueryFirst(UMeta, "$.items[0].id"), &sink.j),
	).From(Users).Where(
		pg.Contains(UMeta, json.RawMessage(`{"plan":"pro"}`)),
		pg.ContainedBy(UMeta, json.RawMessage(`{"plan":"pro","x":1}`)),
		pg.HasKey(UMeta, "plan"),
		pg.HasAnyKey(UMeta, []string{"a", "b"}),
		pg.HasAllKeys(UMeta, []string{"a", "b"}),
		pg.PathExists(UMeta, "$.limits.seats ? (@ > 5)"),
		pg.PathMatch(UMeta, "$.beta == true"),
	).Build())
	// Output:
	// SELECT ("u"."meta" -> $1), ("u"."meta" ->> $2), ("u"."meta" #> $3), ("u"."meta" #>> $4), ("u"."meta" -> CAST($5 AS int)), jsonb_typeof("u"."meta"), jsonb_path_query_first("u"."meta", $6::jsonpath) FROM "users" AS "u" WHERE (("u"."meta" @> $7) AND ("u"."meta" <@ $8) AND ("u"."meta" ? $9) AND ("u"."meta" ?| $10) AND ("u"."meta" ?& $11) AND "u"."meta" @? $12::jsonpath AND "u"."meta" @@ $13::jsonpath)
	// args: [plan plan [limits seats] [limits seats] 0 $.items[0].id {"plan":"pro"} {"plan":"pro","x":1} plan [a b] [a b] $.limits.seats ? (@ > 5) $.beta == true]
}

func Example_json_change() {
	show(upd().Table(Users).Set(
		sqlb.SetExpr(UMeta, pg.JSONBSet(UMeta, []string{"limits", "seats"}, json.RawMessage(`10`), true)),
	).Where(sqlb.Eq(UID, "usr_1")).Build())
	show(sel().Columns(
		sqlb.Bind(pg.JSONConcat(UMeta, sqlb.Param(json.RawMessage(`{"beta":true}`))), &sink.j),
		sqlb.Bind(pg.DeleteKey(UMeta, "beta"), &sink.j),
		sqlb.Bind(pg.DeletePath(UMeta, []string{"limits", "seats"}), &sink.j),
		sqlb.Bind(pg.StripNulls(UMeta), &sink.j),
		sqlb.Bind(pg.JSONBInsert(UMeta, []string{"tags", "0"}, json.RawMessage(`"new"`)), &sink.j),
	).From(Users).Build())
	// Output:
	// UPDATE "users" AS "u" SET "meta" = jsonb_set("u"."meta", $1, $2, $3) WHERE ("u"."id" = $4)
	// args: [[limits seats] 10 true usr_1]
	// SELECT ("u"."meta" || $1), ("u"."meta" - $2), ("u"."meta" #- $3), jsonb_strip_nulls("u"."meta"), jsonb_insert("u"."meta", $4, $5) FROM "users" AS "u"
	// args: [{"beta":true} beta [limits seats] [tags 0] "new"]
}

func Example_json_build() {
	show(sel().Columns(
		sqlb.Bind(pg.BuildObject(pg.Key("id"), UID, pg.Key("email"), UEmail), &sink.j),
		sqlb.Bind(pg.BuildArray(UID, UEmail), &sink.j),
		sqlb.Bind(pg.ToJSONB(UTags), &sink.j),
		sqlb.Bind(pg.RowToJSON(Users), &sink.j),
	).From(Users).Build())
	// Output:
	// SELECT jsonb_build_object(CAST($1 AS text), "u"."id", CAST($2 AS text), "u"."email"), jsonb_build_array("u"."id", "u"."email"), to_jsonb("u"."tags"), row_to_json("u") FROM "users" AS "u"
	// args: [id email]
}

func Example_json_setReturning() {
	src, item := pg.ArrayElements(UMeta, "it")
	show(sel().Columns(sqlb.Bind(pg.JSONText(item, "id"), &sink.s)).From(Users).
		Join(sqlb.Join{Type: sqlb.JoinCross, Source: pg.Lateral(src)}).Build())
	each, k, v := pg.Each(UMeta, "kv")
	show(sel().Columns(sqlb.Bind(k, &sink.s), sqlb.Bind(v, &sink.j)).From(Users).
		Join(sqlb.Join{Type: sqlb.JoinCross, Source: pg.Lateral(each)}).Build())
	keys, key := pg.ObjectKeys(UMeta, "k")
	show(sel().Columns(sqlb.Bind(key, &sink.s)).From(Users).
		Join(sqlb.Join{Type: sqlb.JoinCross, Source: pg.Lateral(keys)}).Build())
	pq, val := pg.PathQuery(UMeta, "$.items[*]", "pq")
	show(sel().Columns(sqlb.Bind(val, &sink.j)).From(Users).
		Join(sqlb.Join{Type: sqlb.JoinCross, Source: pg.Lateral(pq)}).Build())
	// Output:
	// SELECT ("it"."value" ->> $1) FROM "users" AS "u" CROSS JOIN LATERAL jsonb_array_elements("u"."meta") AS "it" ("value")
	// args: [id]
	// SELECT "kv"."key", "kv"."value" FROM "users" AS "u" CROSS JOIN LATERAL jsonb_each("u"."meta") AS "kv" ("key", "value")
	// SELECT "k"."key" FROM "users" AS "u" CROSS JOIN LATERAL jsonb_object_keys("u"."meta") AS "k" ("key")
	// SELECT "pq"."value" FROM "users" AS "u" CROSS JOIN LATERAL jsonb_path_query("u"."meta", $1::jsonpath) AS "pq" ("value")
	// args: [$.items[*]]
}

func Example_json_aggregates() {
	show(sel().Columns(
		sqlb.Bind(PUserID, &sink.s),
		sqlb.Bind(pg.JSONBAgg(PTitle, pg.OrderWithin(sqlb.OrderBy(PCreatedAt, sqlb.SortDesc))), &sink.j),
		sqlb.Bind(pg.JSONBObjectAgg(PID, PTitle), &sink.j),
		sqlb.Bind(pg.JSONAgg(pg.BuildObject(pg.Key("id"), PID)), &sink.j),
	).From(Posts).GroupBy(PUserID).Build())
	// Output:
	// SELECT "p"."user_id", jsonb_agg("p"."title" ORDER BY "p"."created_at" DESC), jsonb_object_agg("p"."id", "p"."title"), json_agg(jsonb_build_object(CAST($1 AS text), "p"."id")) FROM "posts" AS "p" GROUP BY "p"."user_id"
	// args: [id]
}

// 11.6 Ranges

func Example_range_operators() {
	show(sel().Columns(
		sqlb.Bind(pg.LowerBound(BPeriod), &sink.tp),
		sqlb.Bind(pg.UpperBound(BPeriod), &sink.tp),
		sqlb.Bind(pg.LowerInc(BPeriod), &sink.b),
		sqlb.Bind(pg.RangeUnion(BPeriod, pg.TstzRange(t0, t1, pg.BoundsIncExc)), &sink.r),
		sqlb.Bind(pg.RangeIntersect(BPeriod, pg.TstzRange(t0, t1, pg.BoundsIncInc)), &sink.r),
	).From(Bookings).Where(
		pg.RangeContains(BPeriod, t1),
		pg.RangeOverlaps(BPeriod, pg.TstzRange(t0, t1, pg.BoundsIncExc)),
		pg.Adjacent(BPeriod, pg.TstzRange(t0, t1, pg.BoundsIncExc)),
		sqlb.Not(pg.IsEmpty(BPeriod)),
		pg.StrictlyLeft(BPeriod, pg.TstzRange(t1, t1, pg.BoundsIncExc)),
	).Build())
	// Output:
	// SELECT lower("b"."period"), upper("b"."period"), lower_inc("b"."period"), ("b"."period" + tstzrange($1, $2, '[)')), ("b"."period" * tstzrange($3, $4, '[]')) FROM "bookings" AS "b" WHERE (("b"."period" @> CAST($5 AS timestamptz)) AND ("b"."period" && tstzrange($6, $7, '[)')) AND ("b"."period" -|- tstzrange($8, $9, '[)')) AND NOT (isempty("b"."period")) AND ("b"."period" << tstzrange($10, $11, '[)')))
	// args: [2026-09-01 00:00:00 +0000 UTC 2026-09-16 12:00:00 +0000 UTC 2026-09-01 00:00:00 +0000 UTC 2026-09-16 12:00:00 +0000 UTC 2026-09-16 12:00:00 +0000 UTC 2026-09-01 00:00:00 +0000 UTC 2026-09-16 12:00:00 +0000 UTC 2026-09-01 00:00:00 +0000 UTC 2026-09-16 12:00:00 +0000 UTC 2026-09-16 12:00:00 +0000 UTC 2026-09-16 12:00:00 +0000 UTC]
}

// 11.7 Full text search

func Example_fts_searchRankHeadline() {
	q := pg.WebsearchToTSQuery("english", "typed sql -orm")
	show(sel().Columns(
		sqlb.Bind(PID, &sink.s),
		sqlb.Bind(pg.TSRank(PSearch, q), &sink.f32),
		sqlb.Bind(pg.TSHeadline("english", PBody, q, "MaxWords=20"), &sink.s2),
	).From(Posts).Where(pg.Matches(PSearch, q)).
		Order(sqlb.OrderBy(pg.TSRankCD(PSearch, q), sqlb.SortDesc)).Build())
	// Output:
	// SELECT "p"."id", ts_rank("p"."search", websearch_to_tsquery('english', $1)), ts_headline('english', "p"."body", websearch_to_tsquery('english', $2), $3) FROM "posts" AS "p" WHERE (("p"."search" @@ websearch_to_tsquery('english', $4))) ORDER BY ts_rank_cd("p"."search", websearch_to_tsquery('english', $5)) DESC
	// args: [typed sql -orm typed sql -orm MaxWords=20 typed sql -orm typed sql -orm]
}

func Example_fts_buildVectorAndQuery() {
	vec := pg.TSVectorConcat(
		pg.SetWeight(pg.ToTSVector("english", PTitle), pg.WeightA),
		pg.SetWeight(pg.ToTSVector("english", PBody), pg.WeightB),
	)
	q := pg.TSAnd(pg.PlainToTSQuery("english", "typed"), pg.TSNot(pg.PhraseToTSQuery("english", "raw sql")))
	show(sel().Columns(sqlb.Bind(PID, &sink.s)).From(Posts).Where(pg.Matches(vec, q)).Build())
	// Output:
	// SELECT "p"."id" FROM "posts" AS "p" WHERE (((setweight(to_tsvector('english', "p"."title"), 'A') || setweight(to_tsvector('english', "p"."body"), 'B')) @@ (plainto_tsquery('english', $1) && !! phraseto_tsquery('english', $2))))
	// args: [typed raw sql]
}

// 11.8 UUID, sequences, system

func Example_misc_systemFunctions() {
	show(sel().Columns(
		sqlb.Bind(pg.GenRandomUUID(), &sink.s),
		sqlb.Bind(pg.Nextval("users_seq"), &sink.i64),
		sqlb.Bind(pg.CurrentUser(), &sink.s),
		sqlb.Bind(pg.Version(), &sink.s),
		sqlb.Bind(pg.NumNonnulls(UName, UDeletedAt), &sink.i32),
	).From(Users).Build())
	// Output:
	// SELECT gen_random_uuid(), nextval('users_seq'), current_user, version(), num_nonnulls("u"."name", "u"."deleted_at") FROM "users" AS "u"
}

// 11.9 Conditional

func Example_conditional_caseCoalesceNullif() {
	tier := sqlb.Case[string]().
		When(sqlb.Greater(OTotal, 1000), sqlb.Param("gold")).
		When(sqlb.Greater(OTotal, 100), sqlb.Param("silver")).
		Else(sqlb.Param("bronze"))
	maybe := sqlb.Case[string]().When(sqlb.Eq(OStatus, "paid"), OID).End()
	show(sel().Columns(
		sqlb.Bind(tier, &sink.s),
		sqlb.Bind(maybe, &sink.sp),
		sqlb.Bind(sqlb.Coalesce(sqlb.Nullable(OStatus), sqlb.Param[*string](nil), sqlb.Literal[*string]("'none'")), &sink.sp),
		sqlb.Bind(sqlb.NullIf(OStatus, sqlb.Param("")), &sink.sp),
		sqlb.Bind(pg.Greatest(OTotal, OPrice), &sink.f64),
		sqlb.Bind(pg.Least(OTotal, OPrice), &sink.f64),
		sqlb.Bind(sqlb.BoolExpr(sqlb.Greater(OTotal, 50)), &sink.b),
	).From(Orders).Build())
	// Output:
	// SELECT CASE WHEN "o"."total" > $1 THEN $2 WHEN "o"."total" > $3 THEN $4 ELSE $5 END, CASE WHEN "o"."status" = $6 THEN "o"."id" END, COALESCE("o"."status", $7, 'none'), NULLIF("o"."status", $8), GREATEST("o"."total", "o"."price"), LEAST("o"."total", "o"."price"), "o"."total" > $9 FROM "orders" AS "o"
	// args: [1000 gold 100 silver bronze paid <nil>  50]
}

// 12. Aggregates

func Example_aggregates_all() {
	show(sel().Columns(
		sqlb.Bind(sqlb.Count(), &sink.i64),
		sqlb.Bind(sqlb.CountOf(OUserID, sqlb.Distinct()), &sink.i64),
		sqlb.Bind(sqlb.Sum(OTotal), &sink.f64),
		sqlb.Bind(sqlb.Avg(OQty), &sink.f64),
		sqlb.Bind(sqlb.Min(OCreatedAt), &sink.t),
		sqlb.Bind(sqlb.Max(OTotal), &sink.f64),
		sqlb.Bind(pg.ArrayAgg(OID, pg.OrderWithin(sqlb.OrderBy(OCreatedAt, sqlb.SortDesc))), &sink.ss),
		sqlb.Bind(pg.StringAgg(OStatus, ", ", sqlb.Distinct()), &sink.s),
		sqlb.Bind(pg.BoolAnd(sqlb.BoolExpr(sqlb.Greater(OTotal, 0))), &sink.b),
		sqlb.Bind(sqlb.CountOf(OID, pg.Filter(sqlb.Eq(OStatus, "paid"))), &sink.i64),
		sqlb.Bind(pg.PercentileCont(0.95, sqlb.OrderBy(OTotal, sqlb.SortAsc)), &sink.f64),
		sqlb.Bind(pg.PercentileDisc(0.5, OTotal, sqlb.SortAsc), &sink.f64),
		sqlb.Bind(pg.Mode(OStatus), &sink.s),
		sqlb.Bind(pg.Stddev(OTotal), &sink.f64),
		sqlb.Bind(sqlb.Aggregate[float64]("corr", []sqlb.SQLer{OTotal, OPrice}), &sink.f64),
	).From(Orders).Build())
	// Output:
	// SELECT count(*), count(DISTINCT "o"."user_id"), sum("o"."total"), avg("o"."qty"), min("o"."created_at"), max("o"."total"), array_agg("o"."id" ORDER BY "o"."created_at" DESC), string_agg(DISTINCT "o"."status", $1), bool_and("o"."total" > $2), count("o"."id") FILTER (WHERE "o"."status" = $3), percentile_cont($4) WITHIN GROUP (ORDER BY "o"."total" ASC), percentile_disc($5) WITHIN GROUP (ORDER BY "o"."total" ASC), mode() WITHIN GROUP (ORDER BY "o"."status" ASC), stddev("o"."total"), corr("o"."total", "o"."price") FROM "orders" AS "o"
	// args: [,  0 paid 0.95 0.5]
}

// 13. Set-returning functions as sources

func Example_srf_sources() {
	g, n := pg.GenerateSeries(1, 5, 2, "g")
	show(sel().Columns(sqlb.Bind(n, &sink.i64)).From(g).Build())
	gs, i := pg.GenerateSubscripts(UTags, 1, "s")
	show(sel().Columns(sqlb.Bind(i, &sink.i32)).From(Users).Join(sqlb.Join{Type: sqlb.JoinCross, Source: gs}).Build())
	parts, part := pg.RegexpSplitToTable(UName, "\\s+", "w")
	show(sel().Columns(sqlb.Bind(part, &sink.s)).From(Users).Join(sqlb.Join{Type: sqlb.JoinCross, Source: pg.Lateral(parts)}).Build())
	elems, el := pg.ArrayElementsText(UMeta, "e")
	show(sel().Columns(sqlb.Bind(el, &sink.s)).From(Users).Join(sqlb.Join{Type: sqlb.JoinCross, Source: pg.WithOrdinality(elems)}).Build())
	// Output:
	// SELECT "g"."n" FROM generate_series(CAST($1 AS bigint), CAST($2 AS bigint), CAST($3 AS bigint)) AS "g" ("n")
	// args: [1 5 2]
	// SELECT "s"."i" FROM "users" AS "u" CROSS JOIN generate_subscripts("u"."tags", $1) AS "s" ("i")
	// args: [1]
	// SELECT "w"."part" FROM "users" AS "u" CROSS JOIN LATERAL regexp_split_to_table("u"."name", $1) AS "w" ("part")
	// args: [\s+]
	// SELECT "e"."value" FROM "users" AS "u" CROSS JOIN jsonb_array_elements_text("u"."meta") WITH ORDINALITY AS "e" ("value")
}

// 22. The typed door for anything not named

func Example_escape_typedFn() {
	show(sel().Columns(
		sqlb.Bind(sqlb.Fn[float64]("width_bucket", OTotal, sqlb.Param(0), sqlb.Param(100), sqlb.Param(10)), &sink.f64),
		sqlb.Bind(sqlb.New[string](func(a *sqlb.Args) string { return "pg_typeof(" + OTotal.SQL(a) + ")::text" }), &sink.s),
	).From(Orders).Build())
	// Output:
	// SELECT width_bucket("o"."total", $1, $2, $3), pg_typeof("o"."total")::text FROM "orders" AS "o"
	// args: [0 100 10]
}
