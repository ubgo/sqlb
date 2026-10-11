package snippets_test

import (
	"time"

	"github.com/ubgo/sqlb"
	"github.com/ubgo/sqlb/pg"
)

// ## 8. Range types, exclusion constraints, temporal modelling

// ### 8.1 No double bookings, enforced by the database
// Not the query layer: CREATE EXTENSION and CREATE TABLE with an EXCLUDE
// constraint. The library ships it as migration SQL.

// ### 8.2 Range bound gotchas
func Example_s8_02_rangeBounds() {
	show(sel().Exprs(
		pg.Int8Range(1, 5, pg.BoundsIncInc),
		pg.DateRange(sqlb.Param(t0), sqlb.Param(t1), pg.BoundsIncInc),
		pg.NumRange(sqlb.Param(1.0), sqlb.Param(5.0), pg.BoundsIncInc),
		pg.TstzRangeExpr(pg.NullBound(), pg.Now(), pg.BoundsIncExc),
		sqlb.BoolExpr(pg.IsEmpty(pg.Int8Range(5, 5, pg.BoundsIncExc))),
		sqlb.BoolExpr(pg.RangeContains(pg.Int8Range(5, 5, pg.BoundsIncExc), 5)),
		sqlb.BoolExpr(pg.UpperInf(pg.TstzRangeExpr(pg.Now(), pg.NullBound(), pg.BoundsIncExc))),
	).Build())
	// Output:
	// SELECT int8range($1, $2, '[]'), daterange($3, $4, '[]'), numrange($5, $6, '[]'), tstzrange(NULL, now(), '[)'), isempty(int8range($7, $8, '[)')), (int8range($9, $10, '[)') @> CAST($11 AS bigint)), upper_inf(tstzrange(now(), NULL, '[)'))
	// args: 11
}

// ### 8.3 Free slots via multirange subtraction (PG14)
func Example_s8_03_freeSlots() {
	b := tbl("bookings", "bookings")
	room, during, status := sqlb.Col[int64](b, "room_id"), sqlb.Col[pg.Range[time.Time]](b, "during"), sqlb.Col[string](b, "status")
	window := pg.TstzRange(t0, t1, pg.BoundsIncExc)
	free := pg.UnnestMultirange(pg.MultirangeDiff(pg.TstzMultirange(window), pg.RangeAggTyped(during)))
	show(sel().Exprs(sqlb.As(free, "free_slot")).From(b).
		Where(sqlb.Eq(room, 3), sqlb.Eq(status, "active"), pg.RangeOverlaps(during, window)).Build())
	// Output:
	// SELECT unnest((tstzmultirange(tstzrange($1, $2, '[)')) - range_agg("bookings"."during"))) AS "free_slot" FROM "bookings" AS "bookings" WHERE ("bookings"."room_id" = $3 AND "bookings"."status" = $4 AND ("bookings"."during" && tstzrange($5, $6, '[)')))
	// args: 6
}

// ### 8.4 Merge overlapping intervals without multiranges
func Example_s8_04_mergeIntervals() {
	iv := tbl("intervals", "intervals")
	id, s, e := sqlb.Col[int64](iv, "id"), sqlb.Col[time.Time](iv, "s"), sqlb.Col[time.Time](iv, "e")
	prevMax := sqlb.Over(sqlb.Max(e), sqlb.NewWindow().PartitionBy(id).OrderBy(asc(s), asc(e)).Frame(sqlb.FrameRows, sqlb.UnboundedPreceding(), sqlb.Preceding(1)))
	newGrp := sqlb.Case[int32]().When(sqlb.GreaterExpr(s, prevMax), sqlb.Param[int32](1)).Else(sqlb.Param[int32](0))
	x := sqlb.Derived(sel().Exprs(id, s, e, sqlb.As(newGrp, "new_grp")).From(iv), "x")
	xID, xS, xE := ref[int64](x, "id"), ref[time.Time](x, "s"), ref[time.Time](x, "e")
	grp := sqlb.Over(sqlb.Sum(ref[int32](x, "new_grp")), sqlb.NewWindow().PartitionBy(xID).OrderBy(asc(xS), asc(xE)).Frame(sqlb.FrameRows, sqlb.UnboundedPreceding(), sqlb.CurrentRow()))
	y := sqlb.Derived(sel().Exprs(xID, xS, xE, sqlb.As(grp, "grp")).From(x), "y")
	yID, yS, yE, yGrp := ref[int64](y, "id"), ref[time.Time](y, "s"), ref[time.Time](y, "e"), ref[int32](y, "grp")
	show(sel().Exprs(yID, sqlb.As(sqlb.Min(yS), "merged_start"), sqlb.As(sqlb.Max(yE), "merged_end")).From(y).
		GroupBy(yID, yGrp).Order(asc(yID), asc(sqlb.OutputCol[time.Time]("merged_start"))).Build())
	// Output:
	// SELECT "y"."id", min("y"."s") AS "merged_start", max("y"."e") AS "merged_end" FROM (SELECT "x"."id", "x"."s", "x"."e", sum("x"."new_grp") OVER (PARTITION BY "x"."id" ORDER BY "x"."s" ASC, "x"."e" ASC ROWS BETWEEN UNBOUNDED PRECEDING AND CURRENT ROW) AS "grp" FROM (SELECT "intervals"."id", "intervals"."s", "intervals"."e", CASE WHEN "intervals"."s" > max("intervals"."e") OVER (PARTITION BY "intervals"."id" ORDER BY "intervals"."s" ASC, "intervals"."e" ASC ROWS BETWEEN UNBOUNDED PRECEDING AND 1 PRECEDING) THEN $1 ELSE $2 END AS "new_grp" FROM "intervals" AS "intervals") AS "x") AS "y" GROUP BY "y"."id", "y"."grp" ORDER BY "y"."id" ASC, "merged_start" ASC
	// args: 2
}

// ### 8.5 Maximum concurrency (sweep line)
func Example_s8_05_sweepLine() {
	sess := tbl("sessions", "sessions")
	start, end := sqlb.Col[time.Time](sess, "start_ts"), sqlb.Col[time.Time](sess, "end_ts")
	starts := sel().Exprs(sqlb.As(start, "ts"), sqlb.As(sqlb.Param[int32](1), "delta")).From(sess)
	ends := sel().Exprs(end, sqlb.Param[int32](-1)).From(sess)
	ev := sqlb.Derived(starts.UnionAll(ends), "ev")
	ts, delta := ref[time.Time](ev, "ts"), ref[int32](ev, "delta")
	running := sqlb.Over(sqlb.Sum(delta), sqlb.NewWindow().OrderBy(asc(ts), asc(delta)).Frame(sqlb.FrameRows, sqlb.UnboundedPreceding(), sqlb.CurrentRow()))
	s := sqlb.Derived(sel().Exprs(ts, sqlb.As(running, "running")).From(ev), "s")
	show(sel().Exprs(ref[time.Time](s, "ts"), sqlb.As(ref[int32](s, "running"), "concurrent")).From(s).
		Order(desc(ref[int32](s, "running")), asc(ref[time.Time](s, "ts"))).Limit(1).Build())
	// Output:
	// SELECT "s"."ts", "s"."running" AS "concurrent" FROM (SELECT "ev"."ts", sum("ev"."delta") OVER (ORDER BY "ev"."ts" ASC, "ev"."delta" ASC ROWS BETWEEN UNBOUNDED PRECEDING AND CURRENT ROW) AS "running" FROM (SELECT "sessions"."start_ts" AS "ts", $1 AS "delta" FROM "sessions" AS "sessions" UNION ALL SELECT "sessions"."end_ts", $2 FROM "sessions" AS "sessions") AS "ev") AS "s" ORDER BY "s"."running" DESC, "s"."ts" ASC LIMIT $3
	// args: 3
}

// ### 8.6 Slowly changing dimension (type 2) with an as-of query
// The CREATE TABLE is DDL. The close-and-open statement and the two reads render.
func Example_s8_06_slowlyChangingDimension() {
	ph := tbl("price_history", "price_history")
	product, price, valid := sqlb.Col[int64](ph, "product_id"), sqlb.Col[float64](ph, "price"), sqlb.Col[pg.Range[time.Time]](ph, "valid")
	closed := sqlb.UpdateReturning(sqlb.NewUpdate(d).Table(ph).
		Set(sqlb.SetExpr(valid, pg.TstzRangeExpr(pg.LowerBound(valid), pg.Now(), pg.BoundsIncExc))).
		Where(sqlb.Eq(product, 7), pg.UpperInf(valid)), sqlb.Bind(product, &sink.i64))
	show(sqlb.NewInsert(d).With("closed", closed).Into(ph).Columns(product, price).
		Select(sel().Exprs(sqlb.Param[int64](7), sqlb.Param(9.5))).Build())
	show(sel().Exprs(price).From(ph).Where(sqlb.Eq(product, 7), pg.RangeContains(valid, t1)).Build())
	show(sel().Exprs(price).From(ph).Where(sqlb.Eq(product, 7), pg.UpperInf(valid)).Build())
	// Output:
	// WITH "closed" AS (UPDATE "price_history" AS "price_history" SET "valid" = tstzrange(lower("price_history"."valid"), now(), '[)') WHERE ("price_history"."product_id" = $1 AND upper_inf("price_history"."valid")) RETURNING "price_history"."product_id") INSERT INTO "price_history" AS "price_history" ("product_id", "price")
	// SELECT $2, $3
	// args: 3
	// SELECT "price_history"."price" FROM "price_history" AS "price_history" WHERE ("price_history"."product_id" = $1 AND ("price_history"."valid" @> CAST($2 AS timestamptz)))
	// args: 2
	// SELECT "price_history"."price" FROM "price_history" AS "price_history" WHERE ("price_history"."product_id" = $1 AND upper_inf("price_history"."valid"))
	// args: 1
}

// ### 8.7 Temporal primary keys and foreign keys (PG18)
// Not the query layer: CREATE TABLE with WITHOUT OVERLAPS and PERIOD keys.

// ### 8.8 Gaps between consecutive bookings
func Example_s8_08_gapsBetweenBookings() {
	b := tbl("bookings", "bookings")
	room, during := sqlb.Col[int64](b, "room_id"), sqlb.Col[pg.Range[time.Time]](b, "during")
	prev := pg.Lag(during, 1).Over(sqlb.NewWindow().PartitionBy(room).OrderBy(asc(during)))
	s := sqlb.Derived(sel().Exprs(room, during, sqlb.As(prev, "prev")).From(b), "s")
	sPrev, sDuring := sqlb.AssertNotNull(ref[*pg.Range[time.Time]](s, "prev")), ref[pg.Range[time.Time]](s, "during")
	show(sel().Exprs(ref[int64](s, "room_id"), sqlb.As(pg.TstzRangeExpr(pg.UpperBound(sPrev), pg.LowerBound(sDuring), pg.BoundsIncExc), "gap")).
		From(s).Where(sqlb.IsNotNull(ref[*pg.Range[time.Time]](s, "prev")), sqlb.LessExpr(pg.UpperBound(sPrev), pg.LowerBound(sDuring))).Build())
	// Output:
	// SELECT "s"."room_id", tstzrange(upper("s"."prev"), lower("s"."during"), '[)') AS "gap" FROM (SELECT "bookings"."room_id", "bookings"."during", lag("bookings"."during", $1) OVER (PARTITION BY "bookings"."room_id" ORDER BY "bookings"."during" ASC) AS "prev" FROM "bookings" AS "bookings") AS "s" WHERE ("s"."prev" IS NOT NULL AND upper("s"."prev") < lower("s"."during"))
	// args: 1
}
