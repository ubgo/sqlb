package snippets_test

import (
	"time"

	"github.com/ubgo/sqlb"
	"github.com/ubgo/sqlb/pg"
)

// ## 4. Aggregation

// ### 4.1 GROUPING SETS with labelled subtotals
func Example_s4_01_groupingSets() {
	s := tbl("sales", "sales")
	region, product, amount := sqlb.Col[string](s, "region"), sqlb.Col[string](s, "product"), sqlb.Col[float64](s, "amount")
	label := func(c sqlb.Expr[string], all string) sqlb.Expr[string] {
		return sqlb.Case[string]().When(sqlb.Eq(pg.Grouping(c), 1), sqlb.Param(all)).Else(c)
	}
	show(sel().Exprs(sqlb.As(label(region, "ALL REGIONS"), "region"), sqlb.As(label(product, "ALL PRODUCTS"), "product"),
		sqlb.As(sqlb.Sum(amount), "total"), sqlb.As(pg.Grouping(region, product), "grouping_id")).
		From(s).GroupBy(pg.GroupingSets([]sqlb.SQLer{region, product}, []sqlb.SQLer{region}, []sqlb.SQLer{product}, nil)).
		Order(asc(pg.Grouping(region)), asc(pg.Grouping(product)), asc(region), asc(product)).Build())
	// Output:
	// SELECT CASE WHEN grouping("sales"."region") = $1 THEN $2 ELSE "sales"."region" END AS "region", CASE WHEN grouping("sales"."product") = $3 THEN $4 ELSE "sales"."product" END AS "product", sum("sales"."amount") AS "total", grouping("sales"."region", "sales"."product") AS "grouping_id" FROM "sales" AS "sales" GROUP BY GROUPING SETS (("sales"."region", "sales"."product"), ("sales"."region"), ("sales"."product"), ()) ORDER BY grouping("sales"."region") ASC, grouping("sales"."product") ASC, "sales"."region" ASC, "sales"."product" ASC
	// args: 4
}

// ### 4.2 Pivot with FILTER
func Example_s4_02_pivotWithFilter() {
	s := tbl("sales", "sales")
	region, quarter, amount := sqlb.Col[string](s, "region"), sqlb.Col[string](s, "quarter"), sqlb.Col[float64](s, "amount")
	q := func(n string) sqlb.Expr[float64] { return sqlb.Sum(amount, pg.Filter(sqlb.Eq(quarter, n))) }
	show(sel().Exprs(region, sqlb.As(q("Q1"), "q1"), sqlb.As(q("Q2"), "q2"), sqlb.As(q("Q3"), "q3"), sqlb.As(q("Q4"), "q4"),
		sqlb.As(sqlb.Count(pg.Filter(sqlb.Greater(amount, 1000))), "big_deals")).From(s).GroupBy(region).Build())
	// Output:
	// SELECT "sales"."region", sum("sales"."amount") FILTER (WHERE "sales"."quarter" = $1) AS "q1", sum("sales"."amount") FILTER (WHERE "sales"."quarter" = $2) AS "q2", sum("sales"."amount") FILTER (WHERE "sales"."quarter" = $3) AS "q3", sum("sales"."amount") FILTER (WHERE "sales"."quarter" = $4) AS "q4", count(*) FILTER (WHERE "sales"."amount" > $5) AS "big_deals" FROM "sales" AS "sales" GROUP BY "sales"."region"
	// args: 5
}

// ### 4.3 Ordered-set and hypothetical-set aggregates
func Example_s4_03_orderedAndHypotheticalSets() {
	r := tbl("requests", "requests")
	dept, latency, status := sqlb.Col[string](r, "dept"), sqlb.Col[float64](r, "latency_ms"), sqlb.Col[string](r, "status")
	show(sel().Exprs(dept,
		sqlb.As(pg.WithinGroup[[]float64]("percentile_cont", []sqlb.SQLer{pg.Array(0.5, 0.9, 0.99)}, asc(latency)), "p50_p90_p99"),
		sqlb.As(pg.PercentileDisc(0.5, latency, sqlb.SortAsc), "median_real_value"),
		sqlb.As(pg.Mode(status), "most_common_status"),
		sqlb.As(pg.WithinGroup[int64]("rank", []sqlb.SQLer{sqlb.Param(2500.0)}, asc(latency)), "rank_of_2500ms"),
	).From(r).GroupBy(dept).Build())
	// Output:
	// SELECT "requests"."dept", percentile_cont(ARRAY[CAST($1 AS float8), CAST($2 AS float8), CAST($3 AS float8)]) WITHIN GROUP (ORDER BY "requests"."latency_ms" ASC) AS "p50_p90_p99", percentile_disc($4) WITHIN GROUP (ORDER BY "requests"."latency_ms" ASC) AS "median_real_value", mode() WITHIN GROUP (ORDER BY "requests"."status" ASC) AS "most_common_status", rank($5) WITHIN GROUP (ORDER BY "requests"."latency_ms" ASC) AS "rank_of_2500ms" FROM "requests" AS "requests" GROUP BY "requests"."dept"
	// args: 5
}

// ### 4.4 string_agg / array_agg / jsonb_object_agg with DISTINCT, ORDER BY, FILTER
func Example_s4_04_aggregateModifiers() {
	ol := tbl("order_lines", "order_lines")
	customer, category, product, variant := sqlb.Col[int64](ol, "customer_id"), sqlb.Col[string](ol, "category"), sqlb.Col[int64](ol, "product_id"), sqlb.Col[int64](ol, "variant_id")
	created, refunded, note, qty, status := sqlb.Col[time.Time](ol, "created_at"), sqlb.Col[bool](ol, "refunded"), sqlb.Col[*string](ol, "note"), sqlb.Col[int32](ol, "qty"), sqlb.Col[string](ol, "status")
	show(sel().Exprs(customer,
		sqlb.As(pg.StringAgg(category, ", ", sqlb.Distinct(), pg.OrderWithin(asc(category))), "categories"),
		sqlb.As(pg.ArrayAgg(product, pg.OrderWithin(desc(created)), pg.Filter(sqlb.Not(sqlb.PredicateOf(refunded.SQL)))), "kept_products"),
		sqlb.As(sqlb.Coalesce(pg.ArrayAgg(note, pg.Filter(sqlb.IsNotNull(note))), sqlb.Literal[[]*string]("'{}'")), "notes"),
		sqlb.As(pg.JSONBObjectAgg(product, qty), "qty_by_product"),
		sqlb.As(pg.BoolAnd(sqlb.BoolExpr(sqlb.Eq(status, "paid"))), "fully_paid"),
		sqlb.As(sqlb.Aggregate[int64]("count", []sqlb.SQLer{sqlb.Row(product, variant)}, sqlb.Distinct()), "distinct_pairs"),
	).From(ol).GroupBy(customer).
		Having(sqlb.EqExpr(sqlb.Count(pg.Filter(sqlb.Eq(status, "paid"))), sqlb.Count())).Build())
	// Output:
	// SELECT "order_lines"."customer_id", string_agg(DISTINCT "order_lines"."category", $1 ORDER BY "order_lines"."category" ASC) AS "categories", array_agg("order_lines"."product_id" ORDER BY "order_lines"."created_at" DESC) FILTER (WHERE NOT ("order_lines"."refunded")) AS "kept_products", COALESCE(array_agg("order_lines"."note") FILTER (WHERE "order_lines"."note" IS NOT NULL), '{}') AS "notes", jsonb_object_agg("order_lines"."product_id", "order_lines"."qty") AS "qty_by_product", bool_and("order_lines"."status" = $2) AS "fully_paid", count(DISTINCT ("order_lines"."product_id", "order_lines"."variant_id")) AS "distinct_pairs" FROM "order_lines" AS "order_lines" GROUP BY "order_lines"."customer_id" HAVING (count(*) FILTER (WHERE "order_lines"."status" = $3) = count(*))
	// args: 3
}

// ### 4.5 Custom aggregates: first(), product()
// CREATE FUNCTION and CREATE AGGREGATE are DDL, not the query layer. The
// call that uses them renders through sqlb.Aggregate, the typed door.
func Example_s4_05_customAggregates() {
	r := tbl("returns", "returns")
	name, created, rate := sqlb.Col[string](r, "name"), sqlb.Col[time.Time](r, "created_at"), sqlb.Col[float64](r, "rate")
	show(sel().Exprs(sqlb.Aggregate[string]("first", []sqlb.SQLer{name}, pg.OrderWithin(asc(created))),
		sqlb.As(sqlb.Sub(sqlb.Aggregate[float64]("product", []sqlb.SQLer{sqlb.Add(sqlb.Param(1.0), rate)}), sqlb.Param(1.0)), "compound_growth")).
		From(r).Build())
	// Output:
	// SELECT first("returns"."name" ORDER BY "returns"."created_at" ASC), (product(($1 + "returns"."rate")) - $2) AS "compound_growth" FROM "returns" AS "returns"
	// args: 2
}

// ### 4.6 Statistical aggregates
func Example_s4_06_statisticalAggregates() {
	p := tbl("products", "products")
	price, sales := sqlb.Col[float64](p, "price"), sqlb.Col[float64](p, "sales")
	agg2 := func(name string) sqlb.Expr[float64] { return sqlb.Aggregate[float64](name, []sqlb.SQLer{sales, price}) }
	show(sel().Exprs(sqlb.As(sqlb.Aggregate[float64]("corr", []sqlb.SQLer{price, sales}), "correlation"),
		sqlb.As(agg2("regr_slope"), "slope"), sqlb.As(agg2("regr_intercept"), "intercept"), sqlb.As(agg2("regr_r2"), "r_squared"),
		sqlb.Aggregate[float64]("stddev_samp", []sqlb.SQLer{price}), sqlb.Aggregate[float64]("var_pop", []sqlb.SQLer{price}),
		sqlb.Aggregate[float64]("covar_samp", []sqlb.SQLer{price, sales})).From(p).Build())
	// Output:
	// SELECT corr("products"."price", "products"."sales") AS "correlation", regr_slope("products"."sales", "products"."price") AS "slope", regr_intercept("products"."sales", "products"."price") AS "intercept", regr_r2("products"."sales", "products"."price") AS "r_squared", stddev_samp("products"."price"), var_pop("products"."price"), covar_samp("products"."price", "products"."sales") FROM "products" AS "products"
}

// ### 4.7 range_agg and range_intersect_agg (PG14)
func Example_s4_07_rangeAgg() {
	b := tbl("bookings", "bookings")
	room, during := sqlb.Col[int64](b, "room_id"), sqlb.Col[pg.Range[time.Time]](b, "during")
	show(sel().Exprs(room, sqlb.As(pg.RangeAgg(during), "busy"), sqlb.As(sqlb.Aggregate[pg.Range[time.Time]]("range_intersect_agg", []sqlb.SQLer{during}), "always_busy")).
		From(b).GroupBy(room).Build())
	// Output:
	// SELECT "bookings"."room_id", range_agg("bookings"."during") AS "busy", range_intersect_agg("bookings"."during") AS "always_busy" FROM "bookings" AS "bookings" GROUP BY "bookings"."room_id"
}

// ### 4.8 any_value (PG16) and the "column must appear in GROUP BY" error
func Example_s4_08_anyValue() {
	o := tbl("orders", "o")
	oID, oTotal, oCustomer, oCity := sqlb.Col[int64](o, "id"), sqlb.Col[float64](o, "total"), sqlb.Col[int64](o, "customer_id"), sqlb.Col[string](o, "shipping_city")
	l := tbl("order_lines", "l")
	show(sel().Exprs(oID, oTotal, sqlb.CountOf(sqlb.Col[[]byte](l, "*"))).From(o).Join(inner(l, sqlb.EqExpr(sqlb.Col[int64](l, "order_id"), oID))).GroupBy(oID).Build())
	show(sel().Exprs(oCustomer, sqlb.As(sqlb.Aggregate[string]("any_value", []sqlb.SQLer{oCity}), "a_city")).From(o).GroupBy(oCustomer).Build())
	// Output:
	// SELECT "o"."id", "o"."total", count("l"."*") FROM "orders" AS "o" INNER JOIN "order_lines" AS "l" ON ("l"."order_id" = "o"."id") GROUP BY "o"."id"
	// SELECT "o"."customer_id", any_value("o"."shipping_city") AS "a_city" FROM "orders" AS "o" GROUP BY "o"."customer_id"
}

// ### 4.9 The CASE-inside-aggregate trap
func Example_s4_09_caseInsideAggregate() {
	dp := tbl("departments", "departments")
	employees, expenses := sqlb.Col[int32](dp, "employees"), sqlb.Col[float64](dp, "expenses")
	perHead := sqlb.Div(expenses, sqlb.Cast[float64](employees, "float8"))
	show(sel().Exprs(sqlb.Case[float64]().When(sqlb.Greater(sqlb.Min(employees), 0), sqlb.Avg(perHead)).End()).From(dp).Build())
	show(sel().Exprs(sqlb.Avg(sqlb.AssertNotNull(sqlb.Case[float64]().When(sqlb.Greater(employees, 0), perHead).End()))).From(dp).Build())
	// Output:
	// SELECT CASE WHEN min("departments"."employees") > $1 THEN avg(("departments"."expenses" / CAST("departments"."employees" AS float8))) END FROM "departments" AS "departments"
	// args: 1
	// SELECT avg(CASE WHEN "departments"."employees" > $1 THEN ("departments"."expenses" / CAST("departments"."employees" AS float8)) END) FROM "departments" AS "departments"
	// args: 1
}
