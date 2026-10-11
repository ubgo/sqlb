package snippets_test

import (
	"time"

	"github.com/ubgo/sqlb"
	"github.com/ubgo/sqlb/pg"
)

// ## 10. Date and time edge cases

// ### 10.1 timestamp versus timestamptz and the AT TIME ZONE flip
func Example_s10_01_atTimeZoneFlip() {
	utc := pg.AtTimeZone(pg.Now(), "UTC")
	show(sel().Exprs(utc, pg.AtTimeZone(utc, "Asia/Kolkata"), sqlb.BoolExpr(sqlb.EqExpr(pg.AtTimeZone(utc, "UTC"), pg.Now()))).Build())
	// Output:
	// SELECT now() AT TIME ZONE $1, now() AT TIME ZONE $2 AT TIME ZONE $3, now() AT TIME ZONE $4 AT TIME ZONE $5 = now()
	// args: 5
}

// ### 10.2 DST: '1 day' is not '24 hours'
// SET timezone is session state, not a query. The comparison renders.
func Example_s10_02_dstDayVersusHours() {
	v := pg.Values("v", []string{"ts"}, []sqlb.SQLer{pg.TimestamptzLit("2024-03-09 12:00")})
	ts := ref[time.Time](v, "ts")
	show(sel().Exprs(ts, sqlb.As(pg.AddIntervalExpr(ts, pg.IntervalLit("1 day")), "next_day_same_wall_clock"),
		sqlb.As(pg.AddIntervalExpr(ts, pg.IntervalLit("24 hours")), "plus_24h")).From(v).Build())
	// Output:
	// SELECT "v"."ts", ("v"."ts" + interval '1 day') AS "next_day_same_wall_clock", ("v"."ts" + interval '24 hours') AS "plus_24h" FROM (VALUES ('2024-03-09 12:00'::timestamptz)) AS "v" ("ts")
}

// ### 10.3 Bucketing: date_trunc with a zone, date_bin with an origin
func Example_s10_03_bucketing() {
	ev := tbl("events", "events")
	created := sqlb.Col[time.Time](ev, "created_at")
	show(sel().Exprs(sqlb.As(pg.DateTruncZone(pg.FieldDay, created, "America/New_York"), "local_day")).From(ev).Build())
	show(sel().Exprs(sqlb.As(pg.DateBin("15 minutes", created, t0), "bucket")).From(ev).Build())
	t := tbl("t", "t")
	dd := sqlb.Col[time.Time](t, "d")
	show(sel().Exprs(sqlb.As(pg.DateTrunc(pg.FieldWeek, dd), "iso_monday"), sqlb.As(pg.ToChar(dd, "IYYY-IW"), "iso_week")).From(t).Build())
	// Output:
	// SELECT date_trunc('day', "events"."created_at", $1) AS "local_day" FROM "events" AS "events"
	// args: 1
	// SELECT date_bin($1::interval, "events"."created_at", $2) AS "bucket" FROM "events" AS "events"
	// args: 2
	// SELECT date_trunc('week', "t"."d") AS "iso_monday", to_char("t"."d", $1) AS "iso_week" FROM "t" AS "t"
	// args: 1
}

// ### 10.4 Month arithmetic is not reversible
func Example_s10_04_monthArithmetic() {
	jan31 := pg.DateLit("2024-01-31")
	month := pg.IntervalLit("1 month")
	show(sel().Exprs(pg.AddIntervalExpr(jan31, month), pg.SubIntervalExpr(pg.AddIntervalExpr(jan31, month), month),
		sqlb.Cast[time.Time](pg.AddIntervalExpr(pg.DateTrunc(pg.FieldMonth, pg.DateLit("2024-02-10")), pg.IntervalLit("1 month - 1 day")), "date")).Build())
	// Output:
	// SELECT (date '2024-01-31' + interval '1 month'), ((date '2024-01-31' + interval '1 month') - interval '1 month'), CAST((date_trunc('month', date '2024-02-10') + interval '1 month - 1 day') AS date)
}

// ### 10.5 Interval comparison is fuzzy
func Example_s10_05_intervalComparison() {
	show(sel().Exprs(
		sqlb.BoolExpr(sqlb.EqExpr(pg.IntervalLit("1 month"), pg.IntervalLit("30 days"))),
		sqlb.BoolExpr(sqlb.EqExpr(pg.IntervalLit("1 day"), pg.IntervalLit("24 hours"))),
		pg.JustifyInterval(pg.IntervalLit("35 days 26 hours")),
		pg.ExtractFrom(pg.FieldEpoch, pg.IntervalLit("1 month")),
	).Build())
	// Output:
	// SELECT interval '1 month' = interval '30 days', interval '1 day' = interval '24 hours', justify_interval(interval '35 days 26 hours'), extract(epoch FROM interval '1 month')
}

// ### 10.6 now() is frozen for the whole transaction
// BEGIN, pg_sleep and COMMIT are session control. The three clocks render.
func Example_s10_06_frozenNow() {
	show(sel().Exprs(pg.Now(), pg.StatementTimestamp(), pg.ClockTimestamp()).Build())
	// Output:
	// SELECT now(), statement_timestamp(), clock_timestamp()
}

// ### 10.7 Business days between two dates
func Example_s10_07_businessDays() {
	days, day := pg.GenerateSeriesExpr(sqlb.Cast[time.Time](sqlb.Param(t0), "date"), pg.DateMinusDays(sqlb.Cast[time.Time](sqlb.Param(t1), "date"), sqlb.Param(1)), pg.IntervalLit("1 day"), "d")
	h := tbl("holidays", "h")
	show(sel().Exprs(sqlb.As(sqlb.Count(), "business_days")).From(days).
		Where(sqlb.Less(pg.Extract(pg.FieldIsoDow, day), 6),
			sqlb.NotExists(sel().Exprs(sqlb.Param(1)).From(h).Where(sqlb.EqExpr(sqlb.Col[time.Time](h, "holiday"), sqlb.Cast[time.Time](day, "date"))))).Build())
	// Output:
	// SELECT count(*) AS "business_days" FROM generate_series(CAST($1 AS date), (CAST($2 AS date) - $3), interval '1 day') AS "d" ("ts") WHERE (extract(isodow FROM "d"."ts") < $4 AND NOT EXISTS (SELECT $5 FROM "holidays" AS "h" WHERE ("h"."holiday" = CAST("d"."ts" AS date))))
	// args: 5
}

// ### 10.8 Nth weekday of a month (third Friday)
func Example_s10_08_thirdFriday() {
	v := pg.Values("v", []string{"d"}, []sqlb.SQLer{pg.DateLit("2024-09-10")})
	dd := ref[time.Time](v, "d")
	first := pg.DateTrunc(pg.FieldMonth, dd)
	// ((5 - isodow(first) + 7) % 7) * interval '1 day' + interval '2 weeks'
	offset := sqlb.Mod(sqlb.Add(sqlb.Sub(sqlb.Param[int32](5), sqlb.Cast[int32](pg.Extract(pg.FieldIsoDow, first), "int")), sqlb.Param[int32](7)), sqlb.Param[int32](7))
	shift := pg.IntervalMul(pg.IntervalLit("1 day"), offset)
	show(sel().Exprs(sqlb.As(sqlb.Cast[time.Time](pg.AddIntervalExpr(pg.AddIntervalExpr(first, shift), pg.IntervalLit("2 weeks")), "date"), "third_friday")).From(v).Build())
	// Output:
	// SELECT CAST(((date_trunc('month', "v"."d") + (((($1 - CAST(extract(isodow FROM date_trunc('month', "v"."d")) AS int)) + $2) % $3) * interval '1 day')) + interval '2 weeks') AS date) AS "third_friday" FROM (VALUES (date '2024-09-10')) AS "v" ("d")
	// args: 3
}

// ### 10.9 Age, dow, epoch, and formatting traps
func Example_s10_09_ageDowEpoch() {
	mar1, jan31 := pg.TimestampLit("2024-03-01"), pg.TimestampLit("2024-01-31")
	show(sel().Exprs(
		pg.Age(mar1, jan31), pg.Diff(mar1, jan31),
		pg.DaysBetween(pg.DateLit("2024-03-01"), pg.DateLit("2024-01-31")),
		sqlb.As(pg.ExtractFrom(pg.FieldYear, pg.Age(pg.CurrentDate(), pg.DateLit("1990-06-15"))), "age_years"),
		pg.Extract(pg.FieldDow, pg.DateLit("2024-09-15")), pg.Extract(pg.FieldIsoDow, pg.DateLit("2024-09-15")),
		pg.EpochSeconds(sqlb.Param(1726500000.123)), pg.EpochSeconds(sqlb.Div(sqlb.Cast[float64](sqlb.Param[int64](1726500000123), "float8"), sqlb.Param(1000.0))),
		pg.ToChar(pg.Now(), `YYYY-MM-DD"T"HH24:MI:SS.MSOF`),
		sqlb.BoolExpr(sqlb.GreaterExpr(pg.Infinity(), pg.Now())),
	).Build())
	// Output:
	// SELECT age(timestamp '2024-03-01', timestamp '2024-01-31'), (timestamp '2024-03-01' - timestamp '2024-01-31'), (date '2024-03-01' - date '2024-01-31'), extract(year FROM age(current_date, date '1990-06-15')) AS "age_years", extract(dow FROM date '2024-09-15'), extract(isodow FROM date '2024-09-15'), to_timestamp($1), to_timestamp((CAST($2 AS float8) / $3)), to_char(now(), $4), 'infinity'::timestamptz > now()
	// args: 4
}

// ### 10.10 OVERLAPS and half-open semantics
func Example_s10_10_overlaps() {
	show(sel().Exprs(sqlb.BoolExpr(pg.OverlapsPeriod(pg.DateLit("2024-01-01"), pg.DateLit("2024-01-10"), pg.DateLit("2024-01-10"), pg.DateLit("2024-01-20")))).Build())
	show(sel().Exprs(sqlb.BoolExpr(pg.OverlapsInterval(pg.TimestampLit("2024-01-01"), pg.IntervalLit("3 days"), pg.TimestampLit("2024-01-03"), pg.IntervalLit("1 day")))).Build())
	// Output:
	// SELECT (date '2024-01-01', date '2024-01-10') OVERLAPS (date '2024-01-10', date '2024-01-20')
	// SELECT (timestamp '2024-01-01', interval '3 days') OVERLAPS (timestamp '2024-01-03', interval '1 day')
}

// ### 10.11 Immutable date expression for an index on timestamptz
// CREATE INDEX is DDL. The query must repeat the indexed expression exactly.
func Example_s10_11_immutableDateExpression() {
	ev := tbl("events", "events")
	created := sqlb.Col[time.Time](ev, "created_at")
	show(sel().Exprs(created).From(ev).Where(sqlb.Eq(sqlb.Cast[time.Time](pg.AtTimeZone(created, "UTC"), "date"), t1)).Build())
	// Output:
	// SELECT "events"."created_at" FROM "events" AS "events" WHERE (CAST("events"."created_at" AT TIME ZONE $1 AS date) = $2)
	// args: 2
}
