package advancedsql_test

import (
	"github.com/ubgo/sqlb"
	"github.com/ubgo/sqlb/pg"
)

// Section 3. WITH clause and CTE.

// 3 employees above the average salary. The README compares "salary >
// average_salary" against an unnamed column; here the CTE column is named.
func Example_s3_aboveAverage() {
	body := sel().Exprs(sqlb.As(sqlb.Cast[int32](sqlb.Avg(Salary), "int"), "avg_salary")).From(Employee)
	avg := sqlb.Table{Name: "average_salary", Alias: "average_salary"}
	show(sel().With("average_salary", body).
		Columns(EmpBindings(&sink.emp)...).
		From(Employee).
		Join(sqlb.Join{Type: sqlb.JoinCross, Table: avg}).
		Where(sqlb.GreaterExpr(Salary, sqlb.Col[int32](avg, "avg_salary"))).Build())
	// Output:
	// WITH "average_salary" AS (SELECT CAST(avg("e"."salary") AS int) AS "avg_salary" FROM "employee" AS "e") SELECT "e"."emp_id", "e"."emp_name", "e"."dept_name", "e"."salary" FROM "employee" AS "e" CROSS JOIN "average_salary" AS "average_salary" WHERE ("e"."salary" > "average_salary"."avg_salary")
}

// 3 the same problem three ways: sales per store, the average, and the
// comparison through nested derived tables.
func Example_s3_salesDerivedTables() {
	perStore := sel().Exprs(SalesStore, sqlb.As(sqlb.Sum(SalesCost), "sales_per_store")).From(Sales).GroupBy(SalesStore)
	show(sel().Columns(sqlb.Bind(SalesStore, &sink.i32), sqlb.Bind(sqlb.As(sqlb.Sum(SalesCost), "sales_per_store"), &sink.f64)).
		From(Sales).GroupBy(SalesStore).Build())

	sps := sqlb.Derived(perStore, "sales_per_store")
	spsSales := cteCol[float64](sps, "sales_per_store")
	avgSales := sel().Exprs(sqlb.As(pg.Round(sqlb.Avg(spsSales), 2), "avg_sales")).From(sps)
	show(sel().Columns(sqlb.Bind(sqlb.As(pg.Round(sqlb.Avg(spsSales), 2), "avg_sales"), &sink.f64)).From(sps).Build())

	avg := sqlb.Derived(avgSales, "avg_sales")
	show(sel().Columns(
		sqlb.Bind(cteCol[int32](sps, "store_id"), &sink.i32),
		sqlb.Bind(spsSales, &sink.f64),
		sqlb.Bind(cteCol[float64](avg, "avg_sales"), &sink.f64),
	).From(sps).
		Join(sqlb.Join{Type: sqlb.JoinInner, Source: avg, On: []sqlb.Predicate{sqlb.GreaterExpr(spsSales, cteCol[float64](avg, "avg_sales"))}}).
		Build())
	// Output:
	// SELECT "s"."store_id", sum("s"."cost") AS "sales_per_store" FROM "sales" AS "s" GROUP BY "s"."store_id"
	// SELECT round(avg("sales_per_store"."sales_per_store")::numeric, $1) AS "avg_sales" FROM (SELECT "s"."store_id", sum("s"."cost") AS "sales_per_store" FROM "sales" AS "s" GROUP BY "s"."store_id") AS "sales_per_store"
	// args: [2]
	// SELECT "sales_per_store"."store_id", "sales_per_store"."sales_per_store", "avg_sales"."avg_sales" FROM (SELECT "s"."store_id", sum("s"."cost") AS "sales_per_store" FROM "sales" AS "s" GROUP BY "s"."store_id") AS "sales_per_store" INNER JOIN (SELECT round(avg("sales_per_store"."sales_per_store")::numeric, $1) AS "avg_sales" FROM (SELECT "s"."store_id", sum("s"."cost") AS "sales_per_store" FROM "sales" AS "s" GROUP BY "s"."store_id") AS "sales_per_store") AS "avg_sales" ON ("sales_per_store"."sales_per_store" > "avg_sales"."avg_sales")
	// args: [2]
}

// 3 the same with two CTEs that name their columns. The README's final
// SELECT has no FROM; this is the statement the prose describes.
func Example_s3_salesCTEs() {
	sps := sqlb.Table{Name: "sales_per_store", Alias: "sales_per_store"}
	spsSales := sqlb.Col[float64](sps, "sales_per_store")
	avg := sqlb.Table{Name: "avg_sales", Alias: "avg_sales"}
	avgSales := sqlb.Col[float64](avg, "avg_sales")
	show(sel().
		WithCTE(sqlb.CTE{Name: "sales_per_store", Columns: []string{"store_id", "sales_per_store"},
			Body: sel().Exprs(SalesStore, sqlb.As(sqlb.Sum(SalesCost), "sales_per_store")).From(Sales).GroupBy(SalesStore)}).
		WithCTE(sqlb.CTE{Name: "avg_sales", Columns: []string{"avg_sales"},
			Body: sel().Exprs(sqlb.As(pg.Round(sqlb.Avg(spsSales), 2), "avg_sales")).From(sps)}).
		Columns(sqlb.Bind(sqlb.Col[int32](sps, "store_id"), &sink.i32), sqlb.Bind(spsSales, &sink.f64)).
		From(sps).Join(sqlb.Join{Type: sqlb.JoinCross, Table: avg}).
		Where(sqlb.GreaterExpr(spsSales, avgSales)).Build())
	// Output:
	// WITH "sales_per_store" ("store_id", "sales_per_store") AS (SELECT "s"."store_id", sum("s"."cost") AS "sales_per_store" FROM "sales" AS "s" GROUP BY "s"."store_id"), "avg_sales" ("avg_sales") AS (SELECT round(avg("sales_per_store"."sales_per_store")::numeric, $1) AS "avg_sales" FROM "sales_per_store" AS "sales_per_store") SELECT "sales_per_store"."store_id", "sales_per_store"."sales_per_store" FROM "sales_per_store" AS "sales_per_store" CROSS JOIN "avg_sales" AS "avg_sales" WHERE ("sales_per_store"."sales_per_store" > "avg_sales"."avg_sales")
	// args: [2]
}

// 3 a three-CTE chain over payments, with an ORDER BY inside a CTE body.
func Example_s3_revenueChain() {
	rev := sqlb.Table{Name: "revenue", Alias: "revenue"}
	revCustomer := sqlb.Col[int32](rev, "customer_id")
	revRevenue := sqlb.Col[float64](rev, "revenue")
	rps := sqlb.Table{Name: "revenue_per_store", Alias: "revenue_per_store"}
	rpsCustomer := sqlb.Col[int32](rps, "customer_id")
	rpsRevenue := sqlb.Col[float64](rps, "revenue_per_store")
	avg := sqlb.Table{Name: "avg_revenue", Alias: "avg_revenue"}
	avgRevenue := sqlb.Col[float64](avg, "avg_revenue")
	show(sel().
		WithCTE(sqlb.CTE{Name: "revenue", Columns: []string{"customer_id", "revenue"},
			Body: sel().Exprs(PayCustomer, sqlb.As(sqlb.Mul(sqlb.Cast[float64](PayRental, "float8"), PayAmount), "revenue")).From(Payment)}).
		WithCTE(sqlb.CTE{Name: "revenue_per_store", Columns: []string{"customer_id", "revenue_per_store"},
			Body: sel().Exprs(revCustomer, sqlb.As(sqlb.Sum(revRevenue), "revenue_per_store")).From(rev).GroupBy(revCustomer).Order(sqlb.OrderBy(revCustomer, sqlb.SortAsc))}).
		WithCTE(sqlb.CTE{Name: "avg_revenue", Columns: []string{"avg_revenue"},
			Body: sel().Exprs(sqlb.As(pg.Round(sqlb.Avg(rpsRevenue), 2), "avg_revenue")).From(rps)}).
		Columns(sqlb.Bind(rpsCustomer, &sink.i32), sqlb.Bind(rpsRevenue, &sink.f64)).
		From(rps).Join(sqlb.Join{Type: sqlb.JoinCross, Table: avg}).
		Where(sqlb.GreaterExpr(rpsRevenue, avgRevenue)).Build())
	// Output:
	// WITH "revenue" ("customer_id", "revenue") AS (SELECT "p"."customer_id", (CAST("p"."rental_id" AS float8) * "p"."amount") AS "revenue" FROM "payment" AS "p"), "revenue_per_store" ("customer_id", "revenue_per_store") AS (SELECT "revenue"."customer_id", sum("revenue"."revenue") AS "revenue_per_store" FROM "revenue" AS "revenue" GROUP BY "revenue"."customer_id" ORDER BY "revenue"."customer_id" ASC), "avg_revenue" ("avg_revenue") AS (SELECT round(avg("revenue_per_store"."revenue_per_store")::numeric, $1) AS "avg_revenue" FROM "revenue_per_store" AS "revenue_per_store") SELECT "revenue_per_store"."customer_id", "revenue_per_store"."revenue_per_store" FROM "revenue_per_store" AS "revenue_per_store" CROSS JOIN "avg_revenue" AS "avg_revenue" WHERE ("revenue_per_store"."revenue_per_store" > "avg_revenue"."avg_revenue")
	// args: [2]
}
