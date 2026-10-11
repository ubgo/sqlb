package snippets_test

import (
	"time"

	"github.com/ubgo/sqlb"
	"github.com/ubgo/sqlb/pg"
)

// ## 18. Pivot, unpivot, dedup, test data

// ### 18.1 crosstab (tablefunc)
// CREATE EXTENSION is DDL. crosstab takes its two queries as text, so they
// are author constants here; the typed column list is pg.RecordSet.
func Example_s18_01_crosstab() {
	ct := pg.RecordSet(sqlb.Fn[[]byte]("crosstab",
		sqlb.Param("SELECT region, quarter, sum(amount) FROM sales GROUP BY 1, 2 ORDER BY 1, 2"),
		sqlb.Param("SELECT unnest(ARRAY['Q1', 'Q2', 'Q3', 'Q4'])")), "ct",
		pg.ColumnDef{Name: "region", Type: "text"}, pg.ColumnDef{Name: "q1", Type: "numeric"}, pg.ColumnDef{Name: "q2", Type: "numeric"},
		pg.ColumnDef{Name: "q3", Type: "numeric"}, pg.ColumnDef{Name: "q4", Type: "numeric"})
	show(sel().Exprs(ref[string](ct, "region"), ref[float64](ct, "q1"), ref[float64](ct, "q2"), ref[float64](ct, "q3"), ref[float64](ct, "q4")).From(ct).Build())
	// Output:
	// SELECT "ct"."region", "ct"."q1", "ct"."q2", "ct"."q3", "ct"."q4" FROM crosstab($1, $2) AS "ct" ("region" text, "q1" numeric, "q2" numeric, "q3" numeric, "q4" numeric)
	// args: 2
}

// ### 18.2 Dynamic pivot: generate the FILTER query from the data
// \gexec is psql. The generating SELECT renders.
func Example_s18_02_dynamicPivot() {
	s := tbl("sales", "sales")
	quarter := sqlb.Col[string](s, "quarter")
	q := sqlb.Derived(sel().Distinct().Exprs(quarter).From(s), "q")
	qQuarter := ref[string](q, "quarter")
	cols := pg.StringAgg(pg.Format("sum(amount) FILTER (WHERE quarter = %L) AS %I", qQuarter, qQuarter), ", ", pg.OrderWithin(asc(qQuarter)))
	show(sel().Exprs(pg.Format("SELECT region, %s FROM sales GROUP BY region ORDER BY region", cols)).From(q).Build())
	// Output:
	// SELECT format($1, string_agg(format($2, "q"."quarter", "q"."quarter"), $3 ORDER BY "q"."quarter" ASC)) FROM (SELECT DISTINCT "sales"."quarter" FROM "sales" AS "sales") AS "q"
	// args: 3
}

// ### 18.3 Unpivot
func Example_s18_03_unpivot() {
	s := tbl("sales_wide", "s")
	region, id := sqlb.Col[string](s, "region"), sqlb.Col[int64](s, "id")
	q := func(n string) sqlb.Expr[float64] { return sqlb.Col[float64](s, n) }
	v := pg.Values("v", []string{"quarter", "amount"},
		[]sqlb.SQLer{sqlb.Param("Q1"), q("q1")}, []sqlb.SQLer{sqlb.Param("Q2"), q("q2")}, []sqlb.SQLer{sqlb.Param("Q3"), q("q3")}, []sqlb.SQLer{sqlb.Param("Q4"), q("q4")})
	show(sel().Exprs(region, ref[string](v, "quarter"), ref[float64](v, "amount")).From(s).Join(crossLateral(v)).Build())
	sRow := pg.RowRef(s)
	kv := pg.FuncSource(sqlb.Fn[[]byte]("jsonb_each_text", pg.DeleteKey(pg.ToJSONB(sRow), "id")), "kv", "key", "value")
	show(sel().Exprs(id, ref[string](kv, "key"), ref[string](kv, "value")).From(s).Join(cross(kv)).Build())
	// Output:
	// SELECT "s"."region", "v"."quarter", "v"."amount" FROM "sales_wide" AS "s" CROSS JOIN LATERAL (VALUES ($1, "s"."q1"), ($2, "s"."q2"), ($3, "s"."q3"), ($4, "s"."q4")) AS "v" ("quarter", "amount")
	// args: 4
	// SELECT "s"."id", "kv"."key", "kv"."value" FROM "sales_wide" AS "s" CROSS JOIN jsonb_each_text((to_jsonb("s") - $1)) AS "kv" ("key", "value")
	// args: 1
}

// ### 18.4 Latest row per key
// CREATE INDEX is DDL. The DISTINCT ON renders.
func Example_s18_04_latestRowPerKey() {
	r := tbl("readings", "readings")
	device, ts, reading := sqlb.Col[int64](r, "device_id"), sqlb.Col[time.Time](r, "ts"), sqlb.Col[float64](r, "reading")
	show(sel().Clause(pg.DistinctOn(device)).Exprs(device, ts, reading).From(r).Order(asc(device), desc(ts)).Build())
	// Output:
	// SELECT DISTINCT ON ("readings"."device_id") "readings"."device_id", "readings"."ts", "readings"."reading" FROM "readings" AS "readings" ORDER BY "readings"."device_id" ASC, "readings"."ts" DESC
}

// ### 18.5 Realistic test data
func Example_s18_05_testData() {
	show(sel().Exprs(pg.Setseed(0.42)).Build())
	g, n := pg.GenerateSeries(1, 100000, 0, "g")
	u := tbl("users", "users")
	id, email, created, plan, score, tags := sqlb.Col[int64](u, "id"), sqlb.Col[string](u, "email"), sqlb.Col[time.Time](u, "created_at"), sqlb.Col[string](u, "plan"), sqlb.Col[float64](u, "score"), sqlb.Col[[]string](u, "tags")
	random := pg.Random()
	planPick := pg.SubscriptExpr(pg.Array("free", "pro", "team"), pg.WidthBucketThresholds(random, []float64{0, 0.7, 0.95}))
	i, _ := pg.GenerateSeries(1, 4, 0, "i")
	tagList := pg.ArraySubquery[string](sel().Exprs(pg.ConcatOp(sqlb.Param("tag"), sqlb.Cast[string](ref[int64](i, "n"), "text"))).From(i))
	show(sqlb.NewInsert(d).Into(u).Columns(id, email, created, plan, score, tags).Select(sel().Exprs(
		n, pg.ConcatOp(pg.ConcatOp(sqlb.Param("user"), sqlb.Cast[string](n, "text")), sqlb.Param("@example.com")),
		pg.SubIntervalExpr(pg.Now(), pg.IntervalMul(pg.IntervalLit("365 days"), random)),
		planPick, pg.RandomNormal(50, 10), tagList,
	).From(g)).Build())
	// Output:
	// SELECT setseed($1)
	// args: 1
	// INSERT INTO "users" AS "users" ("id", "email", "created_at", "plan", "score", "tags")
	// SELECT "g"."n", (($1 || CAST("g"."n" AS text)) || $2), (now() - (random() * interval '365 days')), (ARRAY[CAST($3 AS text), CAST($4 AS text), CAST($5 AS text)])[width_bucket(random(), $6)], random_normal($7, $8), ARRAY(SELECT ($9 || CAST("i"."n" AS text)) FROM generate_series(CAST($10 AS bigint), CAST($11 AS bigint)) AS "i" ("n")) FROM generate_series(CAST($12 AS bigint), CAST($13 AS bigint)) AS "g" ("n")
	// args: 13
}

// ### 18.6 Text histogram
func Example_s18_06_textHistogram() {
	r := tbl("requests", "requests")
	latency := sqlb.Col[float64](r, "latency_ms")
	bucket := pg.WidthBucket(latency, 0, 1000, 10)
	show(sel().Exprs(sqlb.As(bucket, "bucket"), sqlb.As(sqlb.Mul(sqlb.Sub(bucket, sqlb.Param[int32](1)), sqlb.Param[int32](100)), "from_ms"), sqlb.As(sqlb.Count(), "n"),
		sqlb.As(pg.RepeatExpr(sqlb.Param("#"), sqlb.Cast[int32](sqlb.Div(sqlb.Mul(sqlb.Param(50.0), sqlb.Cast[float64](sqlb.Count(), "float8")), sqlb.Cast[float64](sqlb.Over(sqlb.Max(sqlb.Count()), sqlb.NewWindow()), "float8")), "int")), "bar")).
		From(r).GroupBy(bucket).Order(asc(bucket)).Build())
	// Output:
	// SELECT width_bucket("requests"."latency_ms", $1, $2, $3) AS "bucket", ((width_bucket("requests"."latency_ms", $4, $5, $6) - $7) * $8) AS "from_ms", count(*) AS "n", repeat($9, CAST((($10 * CAST(count(*) AS float8)) / CAST(max(count(*)) OVER () AS float8)) AS int)) AS "bar" FROM "requests" AS "requests" GROUP BY width_bucket("requests"."latency_ms", $11, $12, $13) ORDER BY width_bucket("requests"."latency_ms", $14, $15, $16) ASC
	// args: 16
}

// ### 18.7 FETCH FIRST ... WITH TIES (PG13)
func Example_s18_07_fetchWithTies() {
	s := tbl("scores", "scores")
	score := sqlb.Col[int32](s, "score")
	show(sel().Exprs(sqlb.Col[[]byte](s, "*")).From(s).Order(desc(score)).Clause(pg.FetchFirst(3, true)).Build())
	// Output:
	// SELECT "scores"."*" FROM "scores" AS "scores" ORDER BY "scores"."score" DESC FETCH FIRST $1 ROWS WITH TIES
	// args: 1
}

// ## 19. Analytics patterns

// ### 19.1 Strict-order funnel
func Example_s19_01_strictFunnel() {
	ev := tbl("events", "events")
	user := sqlb.Col[int64](ev, "user_id")
	u := sqlb.Derived(sel().Distinct().Exprs(user).From(ev), "u")
	uUser := ref[int64](u, "user_id")
	e := tbl("events", "e")
	eUser, eName, eTs := sqlb.Col[int64](e, "user_id"), sqlb.Col[string](e, "name"), sqlb.Col[time.Time](e, "ts")
	step := func(alias, col, name string, after *sqlb.Expr[time.Time]) sqlb.Source {
		s := sel().Exprs(sqlb.As(sqlb.Min(eTs), col)).From(e).Where(sqlb.EqExpr(eUser, uUser), sqlb.Eq(eName, name))
		if after != nil {
			s = s.Where(sqlb.GreaterExpr(eTs, *after))
		}
		return sqlb.Derived(s, alias)
	}
	a := step("a", "s1", "view", nil)
	aS1 := ref[time.Time](a, "s1")
	b := step("b", "s2", "add_to_cart", &aS1)
	bS2 := ref[time.Time](b, "s2")
	c := step("c", "s3", "purchase", &bS2)
	s1, s3 := ref[time.Time](a, "s1"), ref[time.Time](c, "s3")
	show(sel().Exprs(sqlb.As(sqlb.Count(), "users"), sqlb.As(sqlb.CountOf(s1), "viewed"), sqlb.As(sqlb.CountOf(ref[time.Time](b, "s2")), "added"), sqlb.As(sqlb.CountOf(s3), "purchased"),
		sqlb.As(pg.Round(sqlb.Div(sqlb.Mul(sqlb.Param(100.0), sqlb.Cast[float64](sqlb.CountOf(s3), "float8")), sqlb.AssertNotNull(sqlb.NullIf(sqlb.Cast[float64](sqlb.CountOf(s1), "float8"), sqlb.Param(0.0)))), 1), "conversion_pct")).
		From(u).Join(leftLateral(a), leftLateral(b), leftLateral(c)).Build())
	// Output:
	// SELECT count(*) AS "users", count("a"."s1") AS "viewed", count("b"."s2") AS "added", count("c"."s3") AS "purchased", round((($1 * CAST(count("c"."s3") AS float8)) / NULLIF(CAST(count("a"."s1") AS float8), $2))::numeric, $3) AS "conversion_pct" FROM (SELECT DISTINCT "events"."user_id" FROM "events" AS "events") AS "u" LEFT JOIN LATERAL (SELECT min("e"."ts") AS "s1" FROM "events" AS "e" WHERE ("e"."user_id" = "u"."user_id" AND "e"."name" = $4)) AS "a" ON (TRUE) LEFT JOIN LATERAL (SELECT min("e"."ts") AS "s2" FROM "events" AS "e" WHERE ("e"."user_id" = "u"."user_id" AND "e"."name" = $5 AND "e"."ts" > "a"."s1")) AS "b" ON (TRUE) LEFT JOIN LATERAL (SELECT min("e"."ts") AS "s3" FROM "events" AS "e" WHERE ("e"."user_id" = "u"."user_id" AND "e"."name" = $6 AND "e"."ts" > "b"."s2")) AS "c" ON (TRUE)
	// args: 6
}

// ### 19.2 Cohort retention matrix
func Example_s19_02_cohortRetention() {
	o := tbl("orders", "orders")
	user, created := sqlb.Col[int64](o, "user_id"), sqlb.Col[time.Time](o, "created_at")
	cohorts := sel().Exprs(user, sqlb.As(sqlb.Cast[time.Time](pg.DateTrunc(pg.FieldMonth, sqlb.Min(created)), "date"), "cohort")).From(o).GroupBy(user)
	activity := sel().Distinct().Exprs(user, sqlb.As(sqlb.Cast[time.Time](pg.DateTrunc(pg.FieldMonth, created), "date"), "month")).From(o)
	c, a := tbl("cohorts", "c"), tbl("activity", "a")
	cUser, cCohort, aUser, aMonth := sqlb.Col[int64](c, "user_id"), sqlb.Col[time.Time](c, "cohort"), sqlb.Col[int64](a, "user_id"), sqlb.Col[time.Time](a, "month")
	size := sqlb.Cast[float64](sqlb.CountOf(cUser, sqlb.Distinct()), "float8")
	m := func(n string) sqlb.Expr[float64] {
		retained := sqlb.CountOf(aUser, sqlb.Distinct(), pg.Filter(sqlb.EqExpr(aMonth, pg.AddIntervalExpr(cCohort, pg.IntervalLit(n)))))
		return pg.Round(sqlb.Div(sqlb.Mul(sqlb.Param(100.0), sqlb.Cast[float64](retained, "float8")), size), 1)
	}
	show(sel().With("cohorts", cohorts).With("activity", activity).
		Exprs(cCohort, sqlb.As(sqlb.CountOf(cUser, sqlb.Distinct()), "size"), sqlb.As(m("1 month"), "m1"), sqlb.As(m("2 months"), "m2"), sqlb.As(m("3 months"), "m3")).
		From(c).Join(sqlb.Join{Type: sqlb.JoinLeft, Table: a, Using: []string{"user_id"}}).GroupBy(cCohort).Order(asc(cCohort)).Build())
	// Output:
	// WITH "cohorts" AS (SELECT "orders"."user_id", CAST(date_trunc('month', min("orders"."created_at")) AS date) AS "cohort" FROM "orders" AS "orders" GROUP BY "orders"."user_id"),
	// "activity" AS (SELECT DISTINCT "orders"."user_id", CAST(date_trunc('month', "orders"."created_at") AS date) AS "month" FROM "orders" AS "orders")
	// SELECT "c"."cohort", count(DISTINCT "c"."user_id") AS "size", round((($1 * CAST(count(DISTINCT "a"."user_id") FILTER (WHERE "a"."month" = ("c"."cohort" + interval '1 month')) AS float8)) / CAST(count(DISTINCT "c"."user_id") AS float8))::numeric, $2) AS "m1", round((($3 * CAST(count(DISTINCT "a"."user_id") FILTER (WHERE "a"."month" = ("c"."cohort" + interval '2 months')) AS float8)) / CAST(count(DISTINCT "c"."user_id") AS float8))::numeric, $4) AS "m2", round((($5 * CAST(count(DISTINCT "a"."user_id") FILTER (WHERE "a"."month" = ("c"."cohort" + interval '3 months')) AS float8)) / CAST(count(DISTINCT "c"."user_id") AS float8))::numeric, $6) AS "m3" FROM "cohorts" AS "c" LEFT JOIN "activity" AS "a" USING ("user_id") GROUP BY "c"."cohort" ORDER BY "c"."cohort" ASC
	// args: 6
}

// ### 19.3 Rolling 30-day active users
func Example_s19_03_rollingActiveUsers() {
	days, day := pg.GenerateSeriesTime(t0, t1, "1 day", "d")
	e := tbl("events", "e")
	eUser, eTs := sqlb.Col[int64](e, "user_id"), sqlb.Col[time.Time](e, "ts")
	mau := sel().Exprs(sqlb.CountOf(eUser, sqlb.Distinct())).From(e).
		Where(sqlb.GreaterEqExpr(eTs, pg.SubIntervalExpr(day, pg.IntervalLit("29 days"))), sqlb.LessExpr(eTs, pg.AddIntervalExpr(day, pg.IntervalLit("1 day"))))
	show(sel().Exprs(sqlb.Cast[time.Time](day, "date"), sqlb.As(sqlb.Subquery[int64](mau), "mau")).From(days).Build())
	// Output:
	// SELECT CAST("d"."ts" AS date), (SELECT count(DISTINCT "e"."user_id") FROM "events" AS "e" WHERE ("e"."ts" >= ("d"."ts" - interval '29 days') AND "e"."ts" < ("d"."ts" + interval '1 day'))) AS "mau" FROM generate_series($1, $2, interval '1 day') AS "d" ("ts")
	// args: 2
}

// ### 19.4 Z-score anomaly detection against the trailing window
func Example_s19_04_zScore() {
	m := tbl("metrics", "metrics")
	ts, value := sqlb.Col[time.Time](m, "ts"), sqlb.Col[float64](m, "value")
	w := sqlb.NewWindow().OrderBy(asc(ts)).Frame(sqlb.FrameRows, sqlb.Preceding(100), sqlb.Preceding(1))
	z := sqlb.Div(sqlb.Sub(value, sqlb.OverNamed(sqlb.Avg(value), "w")), sqlb.AssertNotNull(sqlb.NullIf(sqlb.OverNamed(pg.Stddev(value), "w"), sqlb.Param(0.0))))
	s := sqlb.Derived(sel().Exprs(ts, value, sqlb.As(z, "z")).From(m).Window("w", w), "s")
	show(sel().Exprs(ref[time.Time](s, "ts"), ref[float64](s, "value"), ref[float64](s, "z")).From(s).Where(sqlb.Greater(pg.Abs(ref[float64](s, "z")), 3)).Build())
	// Output:
	// SELECT "s"."ts", "s"."value", "s"."z" FROM (SELECT "metrics"."ts", "metrics"."value", (("metrics"."value" - avg("metrics"."value") OVER "w") / NULLIF(stddev("metrics"."value") OVER "w", $1)) AS "z" FROM "metrics" AS "metrics" WINDOW "w" AS (ORDER BY "metrics"."ts" ASC ROWS BETWEEN 100 PRECEDING AND 1 PRECEDING)) AS "s" WHERE (abs("s"."z") > $2)
	// args: 2
}

// ### 19.5 Pareto: which customers make 80% of revenue
func Example_s19_05_pareto() {
	cr := tbl("customer_revenue", "customer_revenue")
	customer, revenue := sqlb.Col[int64](cr, "customer_id"), sqlb.Col[float64](cr, "revenue")
	running := sqlb.NewWindow().OrderBy(desc(revenue)).Frame(sqlb.FrameRows, sqlb.UnboundedPreceding(), sqlb.CurrentRow())
	show(sel().Exprs(customer, revenue,
		sqlb.As(sqlb.Div(sqlb.Over(sqlb.Sum(revenue), running), sqlb.Over(sqlb.Sum(revenue), sqlb.NewWindow())), "cumulative_share"),
		sqlb.As(sqlb.Div(sqlb.Cast[float64](sqlb.RowNumber().Over(sqlb.NewWindow().OrderBy(desc(revenue))), "numeric"), sqlb.Cast[float64](sqlb.Over(sqlb.Count(), sqlb.NewWindow()), "numeric")), "customer_share")).
		From(cr).Order(desc(revenue)).Build())
	// Output:
	// SELECT "customer_revenue"."customer_id", "customer_revenue"."revenue", (sum("customer_revenue"."revenue") OVER (ORDER BY "customer_revenue"."revenue" DESC ROWS BETWEEN UNBOUNDED PRECEDING AND CURRENT ROW) / sum("customer_revenue"."revenue") OVER ()) AS "cumulative_share", (CAST(row_number() OVER (ORDER BY "customer_revenue"."revenue" DESC) AS numeric) / CAST(count(*) OVER () AS numeric)) AS "customer_share" FROM "customer_revenue" AS "customer_revenue" ORDER BY "customer_revenue"."revenue" DESC
}

// ### 19.6 Allocate session time across hour buckets
func Example_s19_06_allocateAcrossBuckets() {
	s := tbl("sessions", "s")
	start, end := sqlb.Col[time.Time](s, "start_ts"), sqlb.Col[time.Time](s, "end_ts")
	buckets, bucket := pg.GenerateSeriesExpr(pg.DateTrunc(pg.FieldHour, start), pg.DateTrunc(pg.FieldHour, end), pg.IntervalLit("1 hour"), "bucket")
	busy := pg.Diff(pg.Least(end, pg.AddIntervalExpr(bucket, pg.IntervalLit("1 hour"))), pg.Greatest(start, bucket))
	show(sel().Exprs(bucket, sqlb.As(sqlb.Aggregate[pg.Interval]("sum", []sqlb.SQLer{busy}), "busy_time")).From(s).Join(crossLateral(buckets)).
		GroupBy(bucket).Order(asc(bucket)).Build())
	// Output:
	// SELECT "bucket"."ts", sum((LEAST("s"."end_ts", ("bucket"."ts" + interval '1 hour')) - GREATEST("s"."start_ts", "bucket"."ts"))) AS "busy_time" FROM "sessions" AS "s" CROSS JOIN LATERAL generate_series(date_trunc('hour', "s"."start_ts"), date_trunc('hour', "s"."end_ts"), interval '1 hour') AS "bucket" ("ts") GROUP BY "bucket"."ts" ORDER BY "bucket"."ts" ASC
}

// ### 19.7 Missing ids / gaps in a sequence
func Example_s19_07_gapsInSequence() {
	o := tbl("orders", "orders")
	id := sqlb.Col[int64](o, "id")
	s := sqlb.Derived(sel().Exprs(id, sqlb.As(pg.Lead(id, 1).Over(sqlb.NewWindow().OrderBy(asc(id))), "next_id")).From(o), "s")
	sID, next := ref[int64](s, "id"), sqlb.AssertNotNull(ref[*int64](s, "next_id"))
	show(sel().Exprs(sqlb.As(sqlb.Add(sID, sqlb.Param[int64](1)), "gap_start"), sqlb.As(sqlb.Sub(next, sqlb.Param[int64](1)), "gap_end")).From(s).
		Where(sqlb.Greater(sqlb.Sub(next, sID), 1)).Build())
	// Output:
	// SELECT ("s"."id" + $1) AS "gap_start", ("s"."next_id" - $2) AS "gap_end" FROM (SELECT "orders"."id", lead("orders"."id", $3) OVER (ORDER BY "orders"."id" ASC) AS "next_id" FROM "orders" AS "orders") AS "s" WHERE (("s"."next_id" - "s"."id") > $4)
	// args: 4
}

// ### 19.8 Weighted median
func Example_s19_08_weightedMedian() {
	samples := tbl("samples", "samples")
	value, weight := sqlb.Col[float64](samples, "value"), sqlb.Col[float64](samples, "weight")
	s := sqlb.Derived(sel().Exprs(value, sqlb.As(sqlb.Over(sqlb.Sum(weight), sqlb.NewWindow().OrderBy(asc(value))), "cum"), sqlb.As(sqlb.Over(sqlb.Sum(weight), sqlb.NewWindow()), "total")).From(samples), "s")
	sValue, cum, total := ref[float64](s, "value"), ref[float64](s, "cum"), ref[float64](s, "total")
	show(sel().Exprs(sValue).From(s).Where(sqlb.GreaterEqExpr(cum, sqlb.Div(total, sqlb.Param(2.0)))).Order(asc(sValue)).Limit(1).Build())
	// Output:
	// SELECT "s"."value" FROM (SELECT "samples"."value", sum("samples"."weight") OVER (ORDER BY "samples"."value" ASC) AS "cum", sum("samples"."weight") OVER () AS "total" FROM "samples" AS "samples") AS "s" WHERE ("s"."cum" >= ("s"."total" / $1)) ORDER BY "s"."value" ASC LIMIT $2
	// args: 2
}

// ### 19.9 p95 latency per minute with request counts
func Example_s19_09_p95PerMinute() {
	r := tbl("requests", "requests")
	ts, latency := sqlb.Col[time.Time](r, "ts"), sqlb.Col[float64](r, "latency_ms")
	minute := pg.DateTrunc(pg.FieldMinute, ts)
	show(sel().Exprs(sqlb.As(minute, "minute"), sqlb.As(sqlb.Count(), "requests"), sqlb.As(pg.PercentileCont(0.95, asc(latency)), "p95"), sqlb.As(sqlb.Max(latency), "worst")).
		From(r).Where(sqlb.GreaterExpr(ts, pg.SubIntervalExpr(pg.Now(), pg.IntervalLit("1 hour")))).GroupBy(minute).Order(asc(minute)).Build())
	// Output:
	// SELECT date_trunc('minute', "requests"."ts") AS "minute", count(*) AS "requests", percentile_cont($1) WITHIN GROUP (ORDER BY "requests"."latency_ms" ASC) AS "p95", max("requests"."latency_ms") AS "worst" FROM "requests" AS "requests" WHERE ("requests"."ts" > (now() - interval '1 hour')) GROUP BY date_trunc('minute', "requests"."ts") ORDER BY date_trunc('minute', "requests"."ts") ASC
	// args: 1
}

// ### 19.10 Forward-fill a sparse series onto a regular grid
func Example_s19_10_forwardFill() {
	grid, gTs := pg.GenerateSeriesTime(t0, t1, "5 minutes", "g")
	r := tbl("readings", "r")
	sensor, rTs, value := sqlb.Col[int64](r, "sensor_id"), sqlb.Col[time.Time](r, "ts"), sqlb.Col[float64](r, "value")
	last := sqlb.Derived(sel().Exprs(value).From(r).Where(sqlb.Eq(sensor, 3), sqlb.LessEqExpr(rTs, gTs)).Order(desc(rTs)).Limit(1), "r")
	show(sel().Exprs(gTs, ref[*float64](last, "value")).From(grid).Join(leftLateral(last)).Build())
	// Output:
	// SELECT "g"."ts", "r"."value" FROM generate_series($1, $2, interval '5 minutes') AS "g" ("ts") LEFT JOIN LATERAL (SELECT "r"."value" FROM "readings" AS "r" WHERE ("r"."sensor_id" = $3 AND "r"."ts" <= "g"."ts") ORDER BY "r"."ts" DESC LIMIT $4) AS "r" ON (TRUE)
	// args: 4
}
