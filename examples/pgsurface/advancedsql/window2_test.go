package advancedsql_test

import (
	"github.com/ubgo/sqlb"
	"github.com/ubgo/sqlb/pg"
)

// Section 2. SQL Window Function Part 2.

var byPriceDesc = sqlb.NewWindow().PartitionBy(ProdCat).OrderBy(sqlb.OrderBy(ProdPrice, sqlb.SortDesc))

// 2.1 first_value: the most expensive product per category.
func Example_s2_1_firstValue() {
	show(sel().Columns(sqlb.Bindings(ProdBindings(&sink.prod), []sqlb.Binding{
		sqlb.Bind(sqlb.As(sqlb.FirstValue(ProdName).Over(byPriceDesc), "most_exp_per_category"), &sink.sp),
	})...).From(Product).Build())
	// Output:
	// SELECT "p"."product_name", "p"."product_category", "p"."brand", "p"."price", first_value("p"."product_name") OVER (PARTITION BY "p"."product_category" ORDER BY "p"."price" DESC) AS "most_exp_per_category" FROM "product" AS "p"
}

// 2.1 last_value with the default frame, which is the trap the next section fixes.
func Example_s2_1_lastValueDefaultFrame() {
	show(sel().Columns(sqlb.Bindings(ProdBindings(&sink.prod), []sqlb.Binding{
		sqlb.Bind(sqlb.As(sqlb.LastValue(ProdName).Over(byPriceDesc), "least_exp_product"), &sink.sp),
	})...).From(Product).Build())
	// Output:
	// SELECT "p"."product_name", "p"."product_category", "p"."brand", "p"."price", last_value("p"."product_name") OVER (PARTITION BY "p"."product_category" ORDER BY "p"."price" DESC) AS "least_exp_product" FROM "product" AS "p"
}

// 2.1 the lowest payment per customer through a CTE and rank = 1.
func Example_s2_1_lowestPaymentCTE() {
	w := sqlb.NewWindow().PartitionBy(PayCustomer).OrderBy(sqlb.OrderBy(PayAmount, sqlb.SortAsc))
	body := sel().Exprs(PayID, PayCustomer, PayAmount, sqlb.As(sqlb.Rank().Over(w), "rank")).From(Payment)
	rk := sqlb.Table{Name: "rk", Alias: "rk"}
	rank := sqlb.Col[int64](rk, "rank")
	customer := sqlb.Col[int32](rk, "customer_id")
	show(sel().With("rk", body).Columns(
		sqlb.Bind(sqlb.Col[int32](rk, "payment_id"), &sink.i32),
		sqlb.Bind(customer, &sink.i32),
		sqlb.Bind(sqlb.Col[float64](rk, "amount"), &sink.f64),
		sqlb.Bind(rank, &sink.i64),
	).From(rk).Where(sqlb.Eq(rank, 1)).Order(sqlb.OrderBy(customer, sqlb.SortAsc)).Build())
	// Output:
	// WITH "rk" AS (SELECT "p"."payment_id", "p"."customer_id", "p"."amount", rank() OVER (PARTITION BY "p"."customer_id" ORDER BY "p"."amount" ASC) AS "rank" FROM "payment" AS "p") SELECT "rk"."payment_id", "rk"."customer_id", "rk"."amount", "rk"."rank" FROM "rk" AS "rk" WHERE ("rk"."rank" = $1) ORDER BY "rk"."customer_id" ASC
	// args: [1]
}

// 2.2 frame clauses on last_value.
func Example_s2_2_frames() {
	frames := []sqlb.Window{
		byPriceDesc.Frame(sqlb.FrameRange, sqlb.UnboundedPreceding(), sqlb.CurrentRow()),
		byPriceDesc.Frame(sqlb.FrameRange, sqlb.UnboundedPreceding(), sqlb.UnboundedFollowing()),
		byPriceDesc.Frame(sqlb.FrameRows, sqlb.UnboundedPreceding(), sqlb.UnboundedFollowing()),
		byPriceDesc.Frame(sqlb.FrameRows, sqlb.Preceding(2), sqlb.Following(2)),
	}
	for _, w := range frames {
		show(sel().Columns(sqlb.Bindings(ProdBindings(&sink.prod), []sqlb.Binding{
			sqlb.Bind(sqlb.LastValue(ProdName).Over(w), &sink.sp),
		})...).From(Product).Build())
	}
	// Output:
	// SELECT "p"."product_name", "p"."product_category", "p"."brand", "p"."price", last_value("p"."product_name") OVER (PARTITION BY "p"."product_category" ORDER BY "p"."price" DESC RANGE BETWEEN UNBOUNDED PRECEDING AND CURRENT ROW) FROM "product" AS "p"
	// SELECT "p"."product_name", "p"."product_category", "p"."brand", "p"."price", last_value("p"."product_name") OVER (PARTITION BY "p"."product_category" ORDER BY "p"."price" DESC RANGE BETWEEN UNBOUNDED PRECEDING AND UNBOUNDED FOLLOWING) FROM "product" AS "p"
	// SELECT "p"."product_name", "p"."product_category", "p"."brand", "p"."price", last_value("p"."product_name") OVER (PARTITION BY "p"."product_category" ORDER BY "p"."price" DESC ROWS BETWEEN UNBOUNDED PRECEDING AND UNBOUNDED FOLLOWING) FROM "product" AS "p"
	// SELECT "p"."product_name", "p"."product_category", "p"."brand", "p"."price", last_value("p"."product_name") OVER (PARTITION BY "p"."product_category" ORDER BY "p"."price" DESC ROWS BETWEEN 2 PRECEDING AND 2 FOLLOWING) FROM "product" AS "p"
}

// 2.2 the lowest amount per customer with an explicit full frame.
func Example_s2_2_lowestPaymentFrame() {
	w := sqlb.NewWindow().PartitionBy(PayCustomer).OrderBy(sqlb.OrderBy(PayAmount, sqlb.SortDesc)).
		Frame(sqlb.FrameRange, sqlb.UnboundedPreceding(), sqlb.UnboundedFollowing())
	show(sel().Columns(
		sqlb.Bind(PayID, &sink.i32), sqlb.Bind(PayCustomer, &sink.i32), sqlb.Bind(PayAmount, &sink.f64),
		sqlb.Bind(sqlb.As(sqlb.LastValue(PayAmount).Over(w), "lowest_payment"), &sink.f64p),
	).From(Payment).Build())
	// Output:
	// SELECT "p"."payment_id", "p"."customer_id", "p"."amount", last_value("p"."amount") OVER (PARTITION BY "p"."customer_id" ORDER BY "p"."amount" DESC RANGE BETWEEN UNBOUNDED PRECEDING AND UNBOUNDED FOLLOWING) AS "lowest_payment" FROM "payment" AS "p"
}

// 2.3 one named window with a frame, used by first_value and last_value.
func Example_s2_3_namedWindowWithFrame() {
	w := byPriceDesc.Frame(sqlb.FrameRange, sqlb.UnboundedPreceding(), sqlb.UnboundedFollowing())
	show(sel().Columns(sqlb.Bindings(ProdBindings(&sink.prod), []sqlb.Binding{
		sqlb.Bind(sqlb.As(sqlb.FirstValue(ProdName).OverNamed("w"), "most_exp_product"), &sink.sp),
		sqlb.Bind(sqlb.As(sqlb.LastValue(ProdName).OverNamed("w"), "least_exp_product"), &sink.sp2),
	})...).From(Product).Window("w", w).Build())
	// Output:
	// SELECT "p"."product_name", "p"."product_category", "p"."brand", "p"."price", first_value("p"."product_name") OVER "w" AS "most_exp_product", last_value("p"."product_name") OVER "w" AS "least_exp_product" FROM "product" AS "p" WINDOW "w" AS (PARTITION BY "p"."product_category" ORDER BY "p"."price" DESC RANGE BETWEEN UNBOUNDED PRECEDING AND UNBOUNDED FOLLOWING)
}

// 2.4 nth_value with the default frame and with a named full frame.
func Example_s2_4_nthValue() {
	show(sel().Columns(sqlb.Bindings(ProdBindings(&sink.prod), []sqlb.Binding{
		sqlb.Bind(sqlb.As(sqlb.NthValue(ProdName, 2).Over(byPriceDesc), "second_most_expensive"), &sink.sp),
	})...).From(Product).Build())
	w := byPriceDesc.Frame(sqlb.FrameRange, sqlb.UnboundedPreceding(), sqlb.UnboundedFollowing())
	show(sel().Columns(sqlb.Bindings(ProdBindings(&sink.prod), []sqlb.Binding{
		sqlb.Bind(sqlb.As(sqlb.NthValue(ProdName, 2).OverNamed("w"), "second_most_expensive"), &sink.sp),
	})...).From(Product).Window("w", w).Build())
	// Output:
	// SELECT "p"."product_name", "p"."product_category", "p"."brand", "p"."price", nth_value("p"."product_name", $1) OVER (PARTITION BY "p"."product_category" ORDER BY "p"."price" DESC) AS "second_most_expensive" FROM "product" AS "p"
	// args: [2]
	// SELECT "p"."product_name", "p"."product_category", "p"."brand", "p"."price", nth_value("p"."product_name", $1) OVER "w" AS "second_most_expensive" FROM "product" AS "p" WINDOW "w" AS (PARTITION BY "p"."product_category" ORDER BY "p"."price" DESC RANGE BETWEEN UNBOUNDED PRECEDING AND UNBOUNDED FOLLOWING)
	// args: [2]
}

// 2.5 ntile into three buckets, labelled in the outer query.
func Example_s2_5_ntile() {
	body := sel().Exprs(ProdName, ProdCat, ProdPrice, sqlb.As(pg.Ntile(3).Over(sqlb.NewWindow().OrderBy(sqlb.OrderBy(ProdPrice, sqlb.SortDesc))), "bucket")).
		From(Product).Where(sqlb.Eq(ProdCat, "Phone"))
	pb := sqlb.Table{Name: "phone_bucket", Alias: "phone_bucket"}
	bucket := sqlb.Col[int32](pb, "bucket")
	label := sqlb.Case[string]().
		When(sqlb.Eq(bucket, 1), sqlb.Param("Expensive Phone")).
		When(sqlb.Eq(bucket, 2), sqlb.Param("Mid Phone")).
		When(sqlb.Eq(bucket, 3), sqlb.Param("Cheaper Phone")).
		End()
	show(sel().With("phone_bucket", body).Columns(
		sqlb.Bind(sqlb.Col[string](pb, "product_name"), &sink.s),
		sqlb.Bind(label, &sink.sp),
	).From(pb).Build())
	// Output:
	// WITH "phone_bucket" AS (SELECT "p"."product_name", "p"."product_category", "p"."price", ntile($1) OVER (ORDER BY "p"."price" DESC) AS "bucket" FROM "product" AS "p" WHERE ("p"."product_category" = $2)) SELECT "phone_bucket"."product_name", CASE WHEN "phone_bucket"."bucket" = $3 THEN $4 WHEN "phone_bucket"."bucket" = $5 THEN $6 WHEN "phone_bucket"."bucket" = $7 THEN $8 END FROM "phone_bucket" AS "phone_bucket"
	// args: [3 Phone 1 Expensive Phone 2 Mid Phone 3 Cheaper Phone]
}

// 2.6 cume_dist, raw and rounded to a percentage.
func Example_s2_6_cumeDist() {
	w := sqlb.NewWindow().OrderBy(sqlb.OrderBy(ProdPrice, sqlb.SortDesc))
	show(sel().Columns(sqlb.Bindings(ProdBindings(&sink.prod), []sqlb.Binding{
		sqlb.Bind(sqlb.As(pg.CumeDist().Over(w), "cume_dist_by_price"), &sink.f64),
	})...).From(Product).Build())
	pct := pg.Round(sqlb.Mul(pg.CumeDist().Over(w), sqlb.Param(100.0)), 2)
	show(sel().Columns(sqlb.Bindings(ProdBindings(&sink.prod), []sqlb.Binding{
		sqlb.Bind(sqlb.As(pct, "cume_dist_by_price"), &sink.f64),
	})...).From(Product).Build())
	// Output:
	// SELECT "p"."product_name", "p"."product_category", "p"."brand", "p"."price", cume_dist() OVER (ORDER BY "p"."price" DESC) AS "cume_dist_by_price" FROM "product" AS "p"
	// SELECT "p"."product_name", "p"."product_category", "p"."brand", "p"."price", round((cume_dist() OVER (ORDER BY "p"."price" DESC) * $1)::numeric, $2) AS "cume_dist_by_price" FROM "product" AS "p"
	// args: [100 2]
}

// 2.6 the top 30 percent by price, with a percent sign appended.
func Example_s2_6_cumeDistCTE() {
	w := sqlb.NewWindow().OrderBy(sqlb.OrderBy(ProdPrice, sqlb.SortDesc))
	pct := pg.Round(sqlb.Mul(pg.CumeDist().Over(w), sqlb.Param(100.0)), 2)
	body := sel().Exprs(ProdName, ProdCat, ProdPrice, sqlb.As(pct, "cume_dist_by_price")).From(Product)
	cd := sqlb.Table{Name: "cume_dist_t", Alias: "cume_dist_t"}
	cdPct := sqlb.Col[float64](cd, "cume_dist_by_price")
	show(sel().With("cume_dist_t", body).Columns(
		sqlb.Bind(sqlb.Col[string](cd, "product_name"), &sink.s),
		sqlb.Bind(sqlb.As(pg.ConcatOp(sqlb.Cast[string](cdPct, "text"), sqlb.Param("%")), "cume_dist_pct"), &sink.s2),
	).From(cd).Where(sqlb.LessEq(cdPct, 30)).Build())
	// Output:
	// WITH "cume_dist_t" AS (SELECT "p"."product_name", "p"."product_category", "p"."price", round((cume_dist() OVER (ORDER BY "p"."price" DESC) * $1)::numeric, $2) AS "cume_dist_by_price" FROM "product" AS "p") SELECT "cume_dist_t"."product_name", (CAST("cume_dist_t"."cume_dist_by_price" AS text) || $3) AS "cume_dist_pct" FROM "cume_dist_t" AS "cume_dist_t" WHERE ("cume_dist_t"."cume_dist_by_price" <= $4)
	// args: [100 2 % 30]
}

// 2.7 percent_rank, rounded, and looked up for one product.
func Example_s2_7_percentRank() {
	w := sqlb.NewWindow().OrderBy(sqlb.OrderBy(ProdPrice, sqlb.SortAsc))
	pct := pg.Round(sqlb.Mul(pg.PercentRank().Over(w), sqlb.Param(100.0)), 2)
	show(sel().Columns(sqlb.Bindings(ProdBindings(&sink.prod), []sqlb.Binding{
		sqlb.Bind(sqlb.As(pct, "pct_rank"), &sink.f64),
	})...).From(Product).Build())
	body := sel().Exprs(ProdName, ProdCat, ProdPrice, sqlb.As(pct, "pct_rank")).From(Product)
	pr := sqlb.Table{Name: "pct_rank", Alias: "pct_rank"}
	name := sqlb.Col[string](pr, "product_name")
	show(sel().With("pct_rank", body).Columns(
		sqlb.Bind(name, &sink.s),
		sqlb.Bind(sqlb.Col[float64](pr, "pct_rank"), &sink.f64),
	).From(pr).Where(sqlb.Eq(name, "Galaxy Z Fold 3")).Build())
	// Output:
	// SELECT "p"."product_name", "p"."product_category", "p"."brand", "p"."price", round((percent_rank() OVER (ORDER BY "p"."price" ASC) * $1)::numeric, $2) AS "pct_rank" FROM "product" AS "p"
	// args: [100 2]
	// WITH "pct_rank" AS (SELECT "p"."product_name", "p"."product_category", "p"."price", round((percent_rank() OVER (ORDER BY "p"."price" ASC) * $1)::numeric, $2) AS "pct_rank" FROM "product" AS "p") SELECT "pct_rank"."product_name", "pct_rank"."pct_rank" FROM "pct_rank" AS "pct_rank" WHERE ("pct_rank"."product_name" = $3)
	// args: [100 2 Galaxy Z Fold 3]
}
