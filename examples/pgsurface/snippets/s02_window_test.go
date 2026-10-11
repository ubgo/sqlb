package snippets_test

import (
	"time"

	"github.com/ubgo/sqlb"
	"github.com/ubgo/sqlb/pg"
)

// ## 2. Window functions

// ### 2.1 Gaps and islands: consecutive-day streaks
func Example_s2_01_gapsAndIslands() {
	act := tbl("activity", "activity")
	aUser, aAt := sqlb.Col[int64](act, "user_id"), sqlb.Col[time.Time](act, "activity_at")
	days := sqlb.Derived(sel().Distinct().Exprs(aUser, sqlb.As(sqlb.Cast[time.Time](aAt, "date"), "day")).From(act), "d")
	dUser, dDay := ref[int64](days, "user_id"), ref[time.Time](days, "day")
	rn := sqlb.RowNumber().Over(sqlb.NewWindow().PartitionBy(dUser).OrderBy(asc(dDay)))
	inner := sel().Exprs(dUser, dDay, sqlb.As(pg.DateMinusDays(dDay, sqlb.Cast[int32](rn, "int")), "grp")).From(days)
	s := sqlb.Derived(inner, "s")
	sUser, sDay, sGrp := ref[int64](s, "user_id"), ref[time.Time](s, "day"), ref[time.Time](s, "grp")

	show(sel().Exprs(sUser, sqlb.As(sqlb.Min(sDay), "streak_start"), sqlb.As(sqlb.Max(sDay), "streak_end"), sqlb.As(sqlb.Count(), "days")).
		From(s).GroupBy(sUser, sGrp).Order(asc(sUser), asc(sqlb.OutputCol[time.Time]("streak_start"))).Build())
	// Output:
	// SELECT "s"."user_id", min("s"."day") AS "streak_start", max("s"."day") AS "streak_end", count(*) AS "days" FROM (SELECT "d"."user_id", "d"."day", ("d"."day" - CAST(row_number() OVER (PARTITION BY "d"."user_id" ORDER BY "d"."day" ASC) AS int)) AS "grp" FROM (SELECT DISTINCT "activity"."user_id", CAST("activity"."activity_at" AS date) AS "day" FROM "activity" AS "activity") AS "d") AS "s" GROUP BY "s"."user_id", "s"."grp" ORDER BY "s"."user_id" ASC, "streak_start" ASC
}

// ### 2.2 Sessionization with an inactivity gap
func Example_s2_02_sessionization() {
	ev := tbl("events", "events")
	eUser, eTs := sqlb.Col[int64](ev, "user_id"), sqlb.Col[time.Time](ev, "ts")
	byUser := sqlb.NewWindow().PartitionBy(eUser).OrderBy(asc(eTs))
	ordered := sel().Exprs(eUser, eTs, sqlb.As(pg.Lag(eTs, 1).Over(byUser), "prev_ts")).From(ev)

	o := tbl("ordered", "o")
	oUser, oTs, oPrev := ref[int64](o, "user_id"), ref[time.Time](o, "ts"), ref[*time.Time](o, "prev_ts")
	gap := sqlb.GreaterExpr(pg.Diff(oTs, sqlb.AssertNotNull(oPrev)), pg.IntervalLit("30 minutes"))
	flagged := sel().Exprs(oUser, oTs, oPrev,
		sqlb.As(sqlb.Case[int32]().When(sqlb.Or(sqlb.IsNull(oPrev), gap), sqlb.Param[int32](1)).Else(sqlb.Param[int32](0)), "new_session")).From(o)

	f := tbl("flagged", "f")
	fUser, fTs, fNew := ref[int64](f, "user_id"), ref[time.Time](f, "ts"), ref[int32](f, "new_session")
	running := sqlb.NewWindow().PartitionBy(fUser).OrderBy(asc(fTs)).Frame(sqlb.FrameRows, sqlb.UnboundedPreceding(), sqlb.CurrentRow())
	numbered := sel().Exprs(fUser, fTs, sqlb.As(sqlb.Over(sqlb.Sum(fNew), running), "session_no")).From(f)

	n := tbl("numbered", "n")
	nUser, nTs, nSess := ref[int64](n, "user_id"), ref[time.Time](n, "ts"), ref[int64](n, "session_no")
	show(sel().With("ordered", ordered).With("flagged", flagged).With("numbered", numbered).
		Exprs(nUser, nSess, sqlb.As(sqlb.Min(nTs), "started"), sqlb.As(sqlb.Max(nTs), "ended"), sqlb.As(sqlb.Count(), "events"),
			sqlb.As(pg.Diff(sqlb.Max(nTs), sqlb.Min(nTs)), "duration")).
		From(n).GroupBy(nUser, nSess).Order(asc(nUser), asc(nSess)).Build())
	// Output:
	// WITH "ordered" AS (SELECT "events"."user_id", "events"."ts", lag("events"."ts", $1) OVER (PARTITION BY "events"."user_id" ORDER BY "events"."ts" ASC) AS "prev_ts" FROM "events" AS "events"),
	// "flagged" AS (SELECT "o"."user_id", "o"."ts", "o"."prev_ts", CASE WHEN ("o"."prev_ts" IS NULL OR ("o"."ts" - "o"."prev_ts") > interval '30 minutes') THEN $2 ELSE $3 END AS "new_session" FROM "ordered" AS "o"),
	// "numbered" AS (SELECT "f"."user_id", "f"."ts", sum("f"."new_session") OVER (PARTITION BY "f"."user_id" ORDER BY "f"."ts" ASC ROWS BETWEEN UNBOUNDED PRECEDING AND CURRENT ROW) AS "session_no" FROM "flagged" AS "f")
	// SELECT "n"."user_id", "n"."session_no", min("n"."ts") AS "started", max("n"."ts") AS "ended", count(*) AS "events", (max("n"."ts") - min("n"."ts")) AS "duration" FROM "numbered" AS "n" GROUP BY "n"."user_id", "n"."session_no" ORDER BY "n"."user_id" ASC, "n"."session_no" ASC
	// args: 3
}

// ### 2.3 Frame clauses: RANGE with an offset, GROUPS, EXCLUDE
func Example_s2_03_frames() {
	p := tbl("payments", "payments")
	ts, amount := sqlb.Col[time.Time](p, "ts"), sqlb.Col[float64](p, "amount")
	show(sel().Exprs(ts, amount,
		sqlb.As(sqlb.Over(sqlb.Sum(amount), sqlb.NewWindow().OrderBy(asc(ts)).Frame(sqlb.FrameRange, sqlb.PrecedingExpr("interval '7 days'"), sqlb.CurrentRow())), "rolling_7d"),
		sqlb.As(sqlb.Over(sqlb.Avg(amount), sqlb.NewWindow().OrderBy(asc(ts)).Frame(sqlb.FrameRows, sqlb.Preceding(3), sqlb.Following(3)).Exclude(pg.ExcludeCurrentRow)), "neighbours_avg"),
		sqlb.As(sqlb.Over(sqlb.Sum(amount), sqlb.NewWindow().OrderBy(asc(sqlb.Cast[time.Time](ts, "date"))).Frame(pg.FrameGroups, sqlb.Preceding(1), sqlb.Following(1))), "three_day_total"),
		sqlb.As(sqlb.Over(sqlb.Count(), sqlb.NewWindow().OrderBy(asc(amount)).Frame(sqlb.FrameRange, sqlb.Preceding(10), sqlb.Following(10)).Exclude(pg.ExcludeTies)), "within_10_no_ties"),
	).From(p).Build())
	// Output:
	// SELECT "payments"."ts", "payments"."amount", sum("payments"."amount") OVER (ORDER BY "payments"."ts" ASC RANGE BETWEEN interval '7 days' PRECEDING AND CURRENT ROW) AS "rolling_7d", avg("payments"."amount") OVER (ORDER BY "payments"."ts" ASC ROWS BETWEEN 3 PRECEDING AND 3 FOLLOWING EXCLUDE CURRENT ROW) AS "neighbours_avg", sum("payments"."amount") OVER (ORDER BY CAST("payments"."ts" AS date) ASC GROUPS BETWEEN 1 PRECEDING AND 1 FOLLOWING) AS "three_day_total", count(*) OVER (ORDER BY "payments"."amount" ASC RANGE BETWEEN 10 PRECEDING AND 10 FOLLOWING EXCLUDE TIES) AS "within_10_no_ties" FROM "payments" AS "payments"
}

// ### 2.4 The default frame trap
func Example_s2_04_defaultFrameTrap() {
	p := tbl("payments", "payments")
	day, amount := sqlb.Col[time.Time](p, "day"), sqlb.Col[float64](p, "amount")
	byDay := sqlb.NewWindow().OrderBy(asc(day))
	show(sel().Exprs(day, amount,
		sqlb.As(sqlb.Over(sqlb.Sum(amount), byDay), "peers_included"),
		sqlb.As(sqlb.Over(sqlb.Sum(amount), byDay.Frame(sqlb.FrameRows, sqlb.UnboundedPreceding(), sqlb.CurrentRow())), "true_running"),
		sqlb.As(sqlb.LastValue(amount).Over(byDay), "wrong_last"),
		sqlb.As(sqlb.LastValue(amount).Over(byDay.Frame(sqlb.FrameRows, sqlb.UnboundedPreceding(), sqlb.UnboundedFollowing())), "real_last"),
	).From(p).Build())
	// Output:
	// SELECT "payments"."day", "payments"."amount", sum("payments"."amount") OVER (ORDER BY "payments"."day" ASC) AS "peers_included", sum("payments"."amount") OVER (ORDER BY "payments"."day" ASC ROWS BETWEEN UNBOUNDED PRECEDING AND CURRENT ROW) AS "true_running", last_value("payments"."amount") OVER (ORDER BY "payments"."day" ASC) AS "wrong_last", last_value("payments"."amount") OVER (ORDER BY "payments"."day" ASC ROWS BETWEEN UNBOUNDED PRECEDING AND UNBOUNDED FOLLOWING) AS "real_last" FROM "payments" AS "payments"
}

// ### 2.5 Carry forward the last non-null value (IGNORE NULLS emulation)
func Example_s2_05_carryForward() {
	r := tbl("readings", "readings")
	ts, val := sqlb.Col[time.Time](r, "ts"), sqlb.Col[*float64](r, "val")
	inner := sel().Exprs(ts, val, sqlb.As(sqlb.Over(sqlb.CountOf(val), sqlb.NewWindow().OrderBy(asc(ts))), "grp")).From(r)
	s := sqlb.Derived(inner, "s")
	sTs, sVal, sGrp := ref[time.Time](s, "ts"), ref[*float64](s, "val"), ref[int64](s, "grp")
	show(sel().Exprs(sTs, sVal, sqlb.As(sqlb.FirstValue(sVal).Over(sqlb.NewWindow().PartitionBy(sGrp).OrderBy(asc(sTs))), "filled")).
		From(s).Order(asc(sTs)).Build())
	// Output:
	// SELECT "s"."ts", "s"."val", first_value("s"."val") OVER (PARTITION BY "s"."grp" ORDER BY "s"."ts" ASC) AS "filled" FROM (SELECT "readings"."ts", "readings"."val", count("readings"."val") OVER (ORDER BY "readings"."ts" ASC) AS "grp" FROM "readings" AS "readings") AS "s" ORDER BY "s"."ts" ASC
}

// ### 2.6 Rank, share, quartile, and top-N per group with named windows
func Example_s2_06_namedWindows() {
	e := tbl("employees", "employees")
	dept, name, salary := sqlb.Col[string](e, "dept"), sqlb.Col[string](e, "name"), sqlb.Col[float64](e, "salary")
	byDept := sqlb.NewWindow().PartitionBy(dept)
	inner := sel().Exprs(dept, name, salary,
		sqlb.As(sqlb.Rank().OverNamed("w"), "rnk"),
		sqlb.As(sqlb.Div(salary, sqlb.Over(sqlb.Sum(salary), byDept)), "share_of_dept"),
		sqlb.As(sqlb.Sub(salary, sqlb.Over(sqlb.Avg(salary), byDept)), "vs_dept_avg"),
		sqlb.As(pg.Ntile(4).OverNamed("w"), "quartile"),
		sqlb.As(pg.CumeDist().OverNamed("w"), "cume"),
		sqlb.As(pg.PercentRank().OverNamed("w"), "pct_rank"),
	).From(e).Window("w", sqlb.NewWindow().PartitionBy(dept).OrderBy(desc(salary)))
	s := sqlb.Derived(inner, "s")
	show(sel().Exprs(ref[string](s, "dept"), ref[string](s, "name"), ref[float64](s, "salary"), ref[int64](s, "rnk")).
		From(s).Where(sqlb.LessEq(ref[int64](s, "rnk"), 3)).Build())
	// Output:
	// SELECT "s"."dept", "s"."name", "s"."salary", "s"."rnk" FROM (SELECT "employees"."dept", "employees"."name", "employees"."salary", rank() OVER "w" AS "rnk", ("employees"."salary" / sum("employees"."salary") OVER (PARTITION BY "employees"."dept")) AS "share_of_dept", ("employees"."salary" - avg("employees"."salary") OVER (PARTITION BY "employees"."dept")) AS "vs_dept_avg", ntile($1) OVER "w" AS "quartile", cume_dist() OVER "w" AS "cume", percent_rank() OVER "w" AS "pct_rank" FROM "employees" AS "employees" WINDOW "w" AS (PARTITION BY "employees"."dept" ORDER BY "employees"."salary" DESC)) AS "s" WHERE ("s"."rnk" <= $2)
	// args: 2
}

// ### 2.7 Cumulative distinct count
func Example_s2_07_cumulativeDistinct() {
	ev := tbl("events", "events")
	user, ts := sqlb.Col[int64](ev, "user_id"), sqlb.Col[time.Time](ev, "ts")
	f := sqlb.Derived(sel().Exprs(user, sqlb.As(sqlb.Min(sqlb.Cast[time.Time](ts, "date")), "day")).From(ev).GroupBy(user), "f")
	fDay := ref[time.Time](f, "day")
	show(sel().Exprs(fDay, sqlb.As(sqlb.Over(sqlb.Sum(sqlb.Count()), sqlb.NewWindow().OrderBy(asc(fDay))), "cumulative_distinct_users")).
		From(f).GroupBy(fDay).Order(asc(fDay)).Build())
	// Output:
	// SELECT "f"."day", sum(count(*)) OVER (ORDER BY "f"."day" ASC) AS "cumulative_distinct_users" FROM (SELECT "events"."user_id", min(CAST("events"."ts" AS date)) AS "day" FROM "events" AS "events" GROUP BY "events"."user_id") AS "f" GROUP BY "f"."day" ORDER BY "f"."day" ASC
}

// ### 2.8 Capped running total (window functions cannot do this)
func Example_s2_08_cappedRunningTotal() {
	ledger := tbl("ledger", "ledger")
	lID, lAmount := sqlb.Col[int64](ledger, "id"), sqlb.Col[float64](ledger, "amount")
	ordered := sel().Exprs(lID, lAmount, sqlb.As(sqlb.RowNumber().Over(sqlb.NewWindow().OrderBy(asc(lID))), "rn")).From(ledger)
	o := tbl("ordered", "o")
	oID, oAmount, oRn := ref[int64](o, "id"), ref[float64](o, "amount"), ref[int64](o, "rn")
	run := tbl("run", "r")
	rRn, rTotal := ref[int64](run, "rn"), ref[float64](run, "capped_total")
	clamp := func(e sqlb.Expr[float64]) sqlb.Expr[float64] {
		return pg.Least(pg.Greatest(e, sqlb.Param(0.0)), sqlb.Param(100.0))
	}
	seed := sel().Exprs(oID, oAmount, oRn, sqlb.As(sqlb.Cast[float64](clamp(oAmount), "numeric"), "capped_total")).From(o).Where(sqlb.Eq(oRn, 1))
	step := sel().Exprs(oID, oAmount, oRn, clamp(sqlb.Add(rTotal, oAmount))).
		From(run).Join(inner(o, sqlb.EqExpr(oRn, sqlb.Add(rRn, sqlb.Param[int64](1)))))
	show(sel().With("ordered", ordered).WithRecursive("run", seed, step).
		Exprs(ref[int64](run, "id"), ref[float64](run, "amount"), rTotal).From(run).Order(asc(rRn)).Build())
	// Output:
	// WITH RECURSIVE "ordered" AS (SELECT "ledger"."id", "ledger"."amount", row_number() OVER (ORDER BY "ledger"."id" ASC) AS "rn" FROM "ledger" AS "ledger"),
	// "run" AS (SELECT "o"."id", "o"."amount", "o"."rn", CAST(LEAST(GREATEST("o"."amount", $1), $2) AS numeric) AS "capped_total" FROM "ordered" AS "o" WHERE ("o"."rn" = $3) UNION ALL SELECT "o"."id", "o"."amount", "o"."rn", LEAST(GREATEST(("r"."capped_total" + "o"."amount"), $4), $5) FROM "run" AS "r" INNER JOIN "ordered" AS "o" ON ("o"."rn" = ("r"."rn" + $6)))
	// SELECT "r"."id", "r"."amount", "r"."capped_total" FROM "run" AS "r" ORDER BY "r"."rn" ASC
	// args: 6
}

// ### 2.9 Moving median over a time window
func Example_s2_09_movingMedian() {
	p := tbl("payments", "p")
	pTs, pAmount := sqlb.Col[time.Time](p, "ts"), sqlb.Col[float64](p, "amount")
	q := tbl("payments", "q")
	qTs, qAmount := sqlb.Col[time.Time](q, "ts"), sqlb.Col[float64](q, "amount")
	median := sel().Exprs(sqlb.As(pg.PercentileCont(0.5, asc(qAmount)), "median_7d")).From(q).
		Where(sqlb.GreaterExpr(qTs, pg.SubIntervalExpr(pTs, pg.IntervalLit("7 days"))), sqlb.LessEqExpr(qTs, pTs))
	m := sqlb.Derived(median, "m")
	show(sel().Exprs(pTs, pAmount, ref[float64](m, "median_7d")).From(p).Join(crossLateral(m)).Build())
	// Output:
	// SELECT "p"."ts", "p"."amount", "m"."median_7d" FROM "payments" AS "p" CROSS JOIN LATERAL (SELECT percentile_cont($1) WITHIN GROUP (ORDER BY "q"."amount" ASC) AS "median_7d" FROM "payments" AS "q" WHERE ("q"."ts" > ("p"."ts" - interval '7 days') AND "q"."ts" <= "p"."ts")) AS "m"
	// args: 1
}

// ### 2.10 Year-over-year with a division guard
func Example_s2_10_yearOverYear() {
	mr := tbl("monthly_revenue", "monthly_revenue")
	month, revenue := sqlb.Col[time.Time](mr, "month"), sqlb.Col[float64](mr, "revenue")
	ly := sqlb.AssertNotNull(pg.Lag(revenue, 12).Over(sqlb.NewWindow().OrderBy(asc(month)))) // NULL propagates through the arithmetic
	show(sel().Exprs(month, revenue, sqlb.As(ly, "revenue_ly"),
		sqlb.As(pg.Round(sqlb.Div(sqlb.Mul(sqlb.Param(100.0), sqlb.Sub(revenue, ly)), sqlb.AssertNotNull(sqlb.NullIf(ly, sqlb.Param(0.0)))), 1), "yoy_pct")).
		From(mr).Build())
	// Output:
	// SELECT "monthly_revenue"."month", "monthly_revenue"."revenue", lag("monthly_revenue"."revenue", $1) OVER (ORDER BY "monthly_revenue"."month" ASC) AS "revenue_ly", round((($2 * ("monthly_revenue"."revenue" - lag("monthly_revenue"."revenue", $3) OVER (ORDER BY "monthly_revenue"."month" ASC))) / NULLIF(lag("monthly_revenue"."revenue", $4) OVER (ORDER BY "monthly_revenue"."month" ASC), $5))::numeric, $6) AS "yoy_pct" FROM "monthly_revenue" AS "monthly_revenue"
	// args: 6
}

// ### 2.11 Window over an aggregate
func Example_s2_11_windowOverAggregate() {
	s := tbl("sales", "sales")
	region, amount := sqlb.Col[string](s, "region"), sqlb.Col[float64](s, "amount")
	total := sqlb.Sum(amount)
	show(sel().Exprs(region, sqlb.As(total, "total"),
		sqlb.As(pg.Round(sqlb.Div(sqlb.Mul(sqlb.Param(100.0), total), sqlb.Over(sqlb.Sum(total), sqlb.NewWindow())), 2), "pct_of_all"),
		sqlb.As(sqlb.Rank().Over(sqlb.NewWindow().OrderBy(desc(total))), "rnk")).
		From(s).GroupBy(region).Build())
	// Output:
	// SELECT "sales"."region", sum("sales"."amount") AS "total", round((($1 * sum("sales"."amount")) / sum(sum("sales"."amount")) OVER ())::numeric, $2) AS "pct_of_all", rank() OVER (ORDER BY sum("sales"."amount") DESC) AS "rnk" FROM "sales" AS "sales" GROUP BY "sales"."region"
	// args: 2
}
