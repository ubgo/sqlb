package periodcompare_test

import (
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/ubgo/sqlb/examples/pgsurface/periodcompare"
)

var (
	cur   = periodcompare.DateRange{From: date(2026, 9, 1), To: date(2026, 9, 30)}
	prev  = periodcompare.DateRange{From: date(2025, 9, 2), To: date(2025, 10, 1)}
	third = periodcompare.DateRange{From: date(2024, 9, 3), To: date(2024, 10, 2)}
)

func date(y int, m time.Month, d int) time.Time { return time.Date(y, m, d, 0, 0, 0, 0, time.UTC) }

// show prints the SQL split at the CTE boundaries so a reader can follow it.
func show(r periodcompare.Result, err error) {
	if err != nil {
		fmt.Println("error:", err)
		return
	}
	sql := r.Query.SQL
	sql = strings.ReplaceAll(sql, `), "`, "),\n\"")
	sql = strings.ReplaceAll(sql, ") SELECT ", ")\nSELECT ")
	fmt.Println(sql)
	fmt.Println("args:", len(r.Query.Args))
	if len(r.DisabledSources) > 0 {
		fmt.Println("disabled:", r.DisabledSources)
	}
	if len(r.SkippedFilters) > 0 {
		fmt.Println("skipped:", r.SkippedFilters)
	}
}

// The smallest report: sales and stock by vendor, two periods.
func Example_vendorBasic() {
	show(periodcompare.Build(periodcompare.Params{
		Dimension: periodcompare.ByVendor,
		Current:   cur,
		Previous:  prev,
		Page:      periodcompare.Page{Size: 50, Number: 1},
	}))
	// Output:
	// WITH "sales" AS (SELECT "v"."name" AS "key", sum("s"."qty") FILTER (WHERE "s"."sold_on" BETWEEN $1 AND $2) AS "current_units", sum((CAST("s"."qty" AS float8) * "s"."unit_price")) FILTER (WHERE "s"."sold_on" BETWEEN $3 AND $4) AS "current_sales", sum((CAST("s"."qty" AS float8) * "s"."unit_cost")) FILTER (WHERE "s"."sold_on" BETWEEN $5 AND $6) AS "current_cost", sum(((CAST("s"."qty" AS float8) * "s"."unit_price") - (CAST("s"."qty" AS float8) * "s"."unit_cost"))) FILTER (WHERE "s"."sold_on" BETWEEN $7 AND $8) AS "current_margin", sum(CASE WHEN "s"."qty" < $9 THEN (-"s"."qty") ELSE $10 END) FILTER (WHERE "s"."sold_on" BETWEEN $11 AND $12) AS "returned_units", sum("s"."qty") FILTER (WHERE "s"."sold_on" BETWEEN $13 AND $14) AS "previous_units", sum((CAST("s"."qty" AS float8) * "s"."unit_price")) FILTER (WHERE "s"."sold_on" BETWEEN $15 AND $16) AS "previous_sales" FROM "sales_lines" AS "s" INNER JOIN "vendors" AS "v" ON ("v"."id" = "s"."vendor_id") WHERE ("s"."is_gift" = $17 AND "s"."sold_on" BETWEEN $18 AND $19) GROUP BY "v"."name"),
	// "stock" AS (SELECT "v"."name" AS "key", sum("sl"."on_hand") AS "on_hand", sum((CAST("sl"."on_hand" AS float8) * "sl"."retail_price")) AS "on_hand_retail" FROM "stock_levels" AS "sl" INNER JOIN "vendors" AS "v" ON ("v"."id" = "sl"."vendor_id") GROUP BY "v"."name"),
	// "keys" AS (SELECT "sales"."key" FROM "sales" AS "sales" UNION SELECT "stock"."key" FROM "stock" AS "stock"),
	// "report" AS (SELECT "k"."key" AS "key", COALESCE("sales"."current_units", 0) AS "current_units", COALESCE("sales"."current_sales", 0) AS "current_sales", COALESCE("sales"."current_cost", 0) AS "current_cost", COALESCE("sales"."current_margin", 0) AS "current_margin", COALESCE("sales"."returned_units", 0) AS "returned_units", COALESCE("sales"."previous_units", 0) AS "previous_units", COALESCE("sales"."previous_sales", 0) AS "previous_sales", 0 AS "third_units", 0 AS "third_sales", COALESCE("stock"."on_hand", 0) AS "on_hand", COALESCE("stock"."on_hand_retail", 0) AS "on_hand_retail", 0 AS "previous_on_hand", 0 AS "on_order", 0 AS "on_order_retail", 0 AS "ad_cost", (CAST(COALESCE("sales"."current_units", 0) AS float8) - CAST(COALESCE("sales"."previous_units", 0) AS float8)) AS "units_change", (((CAST(COALESCE("sales"."current_units", 0) AS float8) - CAST(COALESCE("sales"."previous_units", 0) AS float8)) / NULLIF(CAST(COALESCE("sales"."previous_units", 0) AS float8), 0)) * $20) AS "units_pct", (COALESCE("sales"."current_sales", 0) - COALESCE("sales"."previous_sales", 0)) AS "sales_change", (((COALESCE("sales"."current_sales", 0) - COALESCE("sales"."previous_sales", 0)) / NULLIF(COALESCE("sales"."previous_sales", 0), 0)) * $21) AS "sales_pct", ((COALESCE("sales"."current_margin", 0) / NULLIF(COALESCE("sales"."current_sales", 0), 0)) * $22) AS "margin_pct", ((CAST(COALESCE("sales"."current_units", 0) AS float8) / NULLIF((CAST(COALESCE("sales"."current_units", 0) AS float8) + CAST(COALESCE("stock"."on_hand", 0) AS float8)), 0)) * $23) AS "sell_through", ((CAST(COALESCE("sales"."returned_units", 0) AS float8) / NULLIF((CAST(COALESCE("sales"."current_units", 0) AS float8) + CAST(COALESCE("sales"."returned_units", 0) AS float8)), 0)) * $24) AS "returns_pct", NULL AS "roas" FROM "keys" AS "k" LEFT JOIN "sales" AS "sales" USING ("key") LEFT JOIN "stock" AS "stock" USING ("key"))
	// SELECT "r"."key", "r"."key", NULL, "r"."current_units", "r"."current_sales", "r"."current_cost", "r"."current_margin", "r"."returned_units", "r"."previous_units", "r"."previous_sales", "r"."third_units", "r"."third_sales", "r"."on_hand", "r"."on_hand_retail", "r"."previous_on_hand", "r"."on_order", "r"."on_order_retail", "r"."ad_cost", "r"."units_change", "r"."units_pct", "r"."sales_change", "r"."sales_pct", "r"."margin_pct", "r"."sell_through", "r"."returns_pct", "r"."roas", count(*) OVER () FROM "report" AS "r" ORDER BY "r"."key" ASC NULLS LAST LIMIT $25 OFFSET $26
	// args: 26
	// disabled: [snapshots on_order ad_spend]
}

// Everything on: three periods, snapshot, on-order, ad spend, every filter,
// a numeric filter on a computed metric, sorted by a ratio, page 3.
func Example_productEverything() {
	show(periodcompare.Build(periodcompare.Params{
		Dimension: periodcompare.ByProduct,
		Current:   cur,
		Previous:  prev,
		Third:     third,
		Include:   periodcompare.Include{ThirdYear: true, OnOrder: true, AdSpend: true, Snapshot: true},
		Filters: periodcompare.Filters{
			AllowedVendors:   []string{"v1", "v2"},
			VendorIDs:        []string{"v1"},
			StoreIDs:         []int32{10, 11},
			Departments:      []string{"Shoes"},
			KeywordIDs:       []int64{7},
			ExcludeReturns:   true,
			ExcludeMarkdowns: true,
			KeyIn:            []string{"A1 | Boot"},
			Numeric:          []periodcompare.NumericFilter{{Metric: periodcompare.MetricSellThrough, Op: periodcompare.OpGreater, Value: 20}},
		},
		Sort: periodcompare.Sort{By: periodcompare.MetricROAS, Dir: periodcompare.Desc},
		Page: periodcompare.Page{Size: 25, Number: 3},
	}))
	// Output:
	// WITH "sales" AS (SELECT concat("p"."code", CAST($1 AS text),
	// "p"."name") AS "key", sum("s"."qty") FILTER (WHERE "s"."sold_on" BETWEEN $2 AND $3) AS "current_units", sum((CAST("s"."qty" AS float8) * "s"."unit_price")) FILTER (WHERE "s"."sold_on" BETWEEN $4 AND $5) AS "current_sales", sum((CAST("s"."qty" AS float8) * "s"."unit_cost")) FILTER (WHERE "s"."sold_on" BETWEEN $6 AND $7) AS "current_cost", sum(((CAST("s"."qty" AS float8) * "s"."unit_price") - (CAST("s"."qty" AS float8) * "s"."unit_cost"))) FILTER (WHERE "s"."sold_on" BETWEEN $8 AND $9) AS "current_margin", sum(CASE WHEN "s"."qty" < $10 THEN (-"s"."qty") ELSE $11 END) FILTER (WHERE "s"."sold_on" BETWEEN $12 AND $13) AS "returned_units", sum("s"."qty") FILTER (WHERE "s"."sold_on" BETWEEN $14 AND $15) AS "previous_units", sum((CAST("s"."qty" AS float8) * "s"."unit_price")) FILTER (WHERE "s"."sold_on" BETWEEN $16 AND $17) AS "previous_sales", sum("s"."qty") FILTER (WHERE "s"."sold_on" BETWEEN $18 AND $19) AS "third_units", sum((CAST("s"."qty" AS float8) * "s"."unit_price")) FILTER (WHERE "s"."sold_on" BETWEEN $20 AND $21) AS "third_sales" FROM "sales_lines" AS "s" INNER JOIN "products" AS "p" ON ("p"."id" = "s"."product_id") INNER JOIN "categories" AS "c" ON ("c"."id" = "p"."category_id") WHERE ("s"."vendor_id" = ANY($22) AND "s"."vendor_id" = ANY($23) AND "s"."store_id" = ANY($24) AND "c"."department" = ANY($25) AND "s"."product_id" IN (SELECT "kw"."product_id" FROM "product_keywords" AS "kw" WHERE ("kw"."keyword_id" = ANY($26))) AND "s"."qty" > $27 AND "s"."unit_price" >= "s"."list_price" AND "s"."is_gift" = $28 AND "s"."sold_on" BETWEEN $29 AND $30) GROUP BY concat("p"."code", CAST($31 AS text),
	// "p"."name")),
	// "stock" AS (SELECT concat("p"."code", CAST($32 AS text),
	// "p"."name") AS "key", sum("sl"."on_hand") AS "on_hand", sum((CAST("sl"."on_hand" AS float8) * "sl"."retail_price")) AS "on_hand_retail" FROM "stock_levels" AS "sl" INNER JOIN "products" AS "p" ON ("p"."id" = "sl"."product_id") INNER JOIN "categories" AS "c" ON ("c"."id" = "p"."category_id") WHERE ("sl"."vendor_id" = ANY($33) AND "sl"."vendor_id" = ANY($34) AND "sl"."store_id" = ANY($35) AND "c"."department" = ANY($36) AND "sl"."product_id" IN (SELECT "kw"."product_id" FROM "product_keywords" AS "kw" WHERE ("kw"."keyword_id" = ANY($37))) AND "sl"."product_id" IN (SELECT DISTINCT "s"."product_id" FROM "sales_lines" AS "s" WHERE ("s"."sold_on" BETWEEN $38 AND $39 AND "s"."unit_price" >= "s"."list_price"))) GROUP BY concat("p"."code", CAST($40 AS text),
	// "p"."name")),
	// "snapshots" AS (SELECT concat("p"."code", CAST($41 AS text),
	// "p"."name") AS "key", sum("ss"."qty") AS "previous_on_hand" FROM "stock_snapshots" AS "ss" INNER JOIN "products" AS "p" ON ("p"."id" = "ss"."product_id") INNER JOIN "categories" AS "c" ON ("c"."id" = "p"."category_id") WHERE ("ss"."vendor_id" = ANY($42) AND "ss"."vendor_id" = ANY($43) AND "ss"."store_id" = ANY($44) AND "c"."department" = ANY($45) AND "ss"."product_id" IN (SELECT "kw"."product_id" FROM "product_keywords" AS "kw" WHERE ("kw"."keyword_id" = ANY($46))) AND "ss"."product_id" IN (SELECT DISTINCT "s"."product_id" FROM "sales_lines" AS "s" WHERE ("s"."sold_on" BETWEEN $47 AND $48 AND "s"."unit_price" >= "s"."list_price")) AND "ss"."snapshot_date" = $49) GROUP BY concat("p"."code", CAST($50 AS text),
	// "p"."name")),
	// "on_order" AS (SELECT concat("p"."code", CAST($51 AS text),
	// "p"."name") AS "key", sum("po"."qty_due") AS "on_order", sum((CAST("po"."qty_due" AS float8) * "po"."unit_price")) AS "on_order_retail" FROM "po_lines" AS "po" INNER JOIN "products" AS "p" ON ("p"."id" = "po"."product_id") INNER JOIN "categories" AS "c" ON ("c"."id" = "p"."category_id") WHERE ("po"."vendor_id" = ANY($52) AND "po"."vendor_id" = ANY($53) AND "c"."department" = ANY($54) AND "po"."product_id" IN (SELECT "kw"."product_id" FROM "product_keywords" AS "kw" WHERE ("kw"."keyword_id" = ANY($55))) AND "po"."product_id" IN (SELECT DISTINCT "s"."product_id" FROM "sales_lines" AS "s" WHERE ("s"."sold_on" BETWEEN $56 AND $57 AND "s"."unit_price" >= "s"."list_price")) AND "po"."qty_due" > $58 AND "po"."cancelled" = $59) GROUP BY concat("p"."code", CAST($60 AS text),
	// "p"."name")),
	// "ad_spend" AS (SELECT concat("p"."code", CAST($61 AS text),
	// "p"."name") AS "key", sum("ad"."cost") AS "ad_cost" FROM "ad_spend" AS "ad" INNER JOIN "products" AS "p" ON ("p"."id" = "ad"."product_id") INNER JOIN "categories" AS "c" ON ("c"."id" = "p"."category_id") WHERE ("ad"."vendor_id" = ANY($62) AND "ad"."vendor_id" = ANY($63) AND "c"."department" = ANY($64) AND "ad"."product_id" IN (SELECT "kw"."product_id" FROM "product_keywords" AS "kw" WHERE ("kw"."keyword_id" = ANY($65))) AND "ad"."product_id" IN (SELECT DISTINCT "s"."product_id" FROM "sales_lines" AS "s" WHERE ("s"."sold_on" BETWEEN $66 AND $67 AND "s"."unit_price" >= "s"."list_price")) AND "ad"."day" BETWEEN $68 AND $69) GROUP BY concat("p"."code", CAST($70 AS text),
	// "p"."name")),
	// "keys" AS (SELECT "sales"."key" FROM "sales" AS "sales" UNION SELECT "stock"."key" FROM "stock" AS "stock" UNION SELECT "snapshots"."key" FROM "snapshots" AS "snapshots" UNION SELECT "on_order"."key" FROM "on_order" AS "on_order" UNION SELECT "ad_spend"."key" FROM "ad_spend" AS "ad_spend"),
	// "report" AS (SELECT "k"."key" AS "key", COALESCE("sales"."current_units", 0) AS "current_units", COALESCE("sales"."current_sales", 0) AS "current_sales", COALESCE("sales"."current_cost", 0) AS "current_cost", COALESCE("sales"."current_margin", 0) AS "current_margin", COALESCE("sales"."returned_units", 0) AS "returned_units", COALESCE("sales"."previous_units", 0) AS "previous_units", COALESCE("sales"."previous_sales", 0) AS "previous_sales", COALESCE("sales"."third_units", 0) AS "third_units", COALESCE("sales"."third_sales", 0) AS "third_sales", COALESCE("stock"."on_hand", 0) AS "on_hand", COALESCE("stock"."on_hand_retail", 0) AS "on_hand_retail", COALESCE("snapshots"."previous_on_hand", 0) AS "previous_on_hand", COALESCE("on_order"."on_order", 0) AS "on_order", COALESCE("on_order"."on_order_retail", 0) AS "on_order_retail", COALESCE("ad_spend"."ad_cost", 0) AS "ad_cost", (CAST(COALESCE("sales"."current_units", 0) AS float8) - CAST(COALESCE("sales"."previous_units", 0) AS float8)) AS "units_change", (((CAST(COALESCE("sales"."current_units", 0) AS float8) - CAST(COALESCE("sales"."previous_units", 0) AS float8)) / NULLIF(CAST(COALESCE("sales"."previous_units", 0) AS float8), 0)) * $71) AS "units_pct", (COALESCE("sales"."current_sales", 0) - COALESCE("sales"."previous_sales", 0)) AS "sales_change", (((COALESCE("sales"."current_sales", 0) - COALESCE("sales"."previous_sales", 0)) / NULLIF(COALESCE("sales"."previous_sales", 0), 0)) * $72) AS "sales_pct", ((COALESCE("sales"."current_margin", 0) / NULLIF(COALESCE("sales"."current_sales", 0), 0)) * $73) AS "margin_pct", ((CAST(COALESCE("sales"."current_units", 0) AS float8) / NULLIF((CAST(COALESCE("sales"."current_units", 0) AS float8) + CAST(COALESCE("stock"."on_hand", 0) AS float8)), 0)) * $74) AS "sell_through", ((CAST(COALESCE("sales"."returned_units", 0) AS float8) / NULLIF((CAST(COALESCE("sales"."current_units", 0) AS float8) + CAST(COALESCE("sales"."returned_units", 0) AS float8)), 0)) * $75) AS "returns_pct", (COALESCE("sales"."current_sales", 0) / NULLIF(COALESCE("ad_spend"."ad_cost", 0), 0)) AS "roas" FROM "keys" AS "k" LEFT JOIN "sales" AS "sales" USING ("key") LEFT JOIN "stock" AS "stock" USING ("key") LEFT JOIN "snapshots" AS "snapshots" USING ("key") LEFT JOIN "on_order" AS "on_order" USING ("key") LEFT JOIN "ad_spend" AS "ad_spend" USING ("key")),
	// "labels" AS (SELECT concat("p"."code", CAST($76 AS text),
	// "p"."name") AS "key", concat("p"."code", CAST($77 AS text),
	// "p"."name") AS "label", min("p"."image_url") AS "image_url" FROM "products" AS "p" GROUP BY "p"."code", "p"."name")
	// SELECT "r"."key", COALESCE("l"."label", "r"."key"),
	// "l"."image_url", "r"."current_units", "r"."current_sales", "r"."current_cost", "r"."current_margin", "r"."returned_units", "r"."previous_units", "r"."previous_sales", "r"."third_units", "r"."third_sales", "r"."on_hand", "r"."on_hand_retail", "r"."previous_on_hand", "r"."on_order", "r"."on_order_retail", "r"."ad_cost", "r"."units_change", "r"."units_pct", "r"."sales_change", "r"."sales_pct", "r"."margin_pct", "r"."sell_through", "r"."returns_pct", "r"."roas", count(*) OVER () FROM "report" AS "r" LEFT JOIN "labels" AS "l" USING ("key") WHERE ("r"."key" = ANY($78) AND "r"."sell_through" > $79) ORDER BY "r"."roas" DESC NULLS LAST, "r"."key" ASC LIMIT $80 OFFSET $81
	// args: 81
	// skipped: [on_order: StoreIDs ad_spend: StoreIDs]
}

// Grouping by store: sources without a store column are left out and their
// measures are typed zeros. The build says so.
func Example_storeOmitsStorelessSources() {
	show(periodcompare.Build(periodcompare.Params{
		Dimension: periodcompare.ByStore,
		Current:   cur,
		Previous:  prev,
		Include:   periodcompare.Include{OnOrder: true, AdSpend: true},
		Filters:   periodcompare.Filters{StoreIDs: []int32{10}},
	}))
	// Output:
	// WITH "sales" AS (SELECT "st"."name" AS "key", sum("s"."qty") FILTER (WHERE "s"."sold_on" BETWEEN $1 AND $2) AS "current_units", sum((CAST("s"."qty" AS float8) * "s"."unit_price")) FILTER (WHERE "s"."sold_on" BETWEEN $3 AND $4) AS "current_sales", sum((CAST("s"."qty" AS float8) * "s"."unit_cost")) FILTER (WHERE "s"."sold_on" BETWEEN $5 AND $6) AS "current_cost", sum(((CAST("s"."qty" AS float8) * "s"."unit_price") - (CAST("s"."qty" AS float8) * "s"."unit_cost"))) FILTER (WHERE "s"."sold_on" BETWEEN $7 AND $8) AS "current_margin", sum(CASE WHEN "s"."qty" < $9 THEN (-"s"."qty") ELSE $10 END) FILTER (WHERE "s"."sold_on" BETWEEN $11 AND $12) AS "returned_units", sum("s"."qty") FILTER (WHERE "s"."sold_on" BETWEEN $13 AND $14) AS "previous_units", sum((CAST("s"."qty" AS float8) * "s"."unit_price")) FILTER (WHERE "s"."sold_on" BETWEEN $15 AND $16) AS "previous_sales" FROM "sales_lines" AS "s" INNER JOIN "stores" AS "st" ON ("st"."id" = "s"."store_id") WHERE ("s"."store_id" = ANY($17) AND "s"."is_gift" = $18 AND "s"."sold_on" BETWEEN $19 AND $20) GROUP BY "st"."name"),
	// "stock" AS (SELECT "st"."name" AS "key", sum("sl"."on_hand") AS "on_hand", sum((CAST("sl"."on_hand" AS float8) * "sl"."retail_price")) AS "on_hand_retail" FROM "stock_levels" AS "sl" INNER JOIN "stores" AS "st" ON ("st"."id" = "sl"."store_id") WHERE ("sl"."store_id" = ANY($21)) GROUP BY "st"."name"),
	// "keys" AS (SELECT "sales"."key" FROM "sales" AS "sales" UNION SELECT "stock"."key" FROM "stock" AS "stock"),
	// "report" AS (SELECT "k"."key" AS "key", COALESCE("sales"."current_units", 0) AS "current_units", COALESCE("sales"."current_sales", 0) AS "current_sales", COALESCE("sales"."current_cost", 0) AS "current_cost", COALESCE("sales"."current_margin", 0) AS "current_margin", COALESCE("sales"."returned_units", 0) AS "returned_units", COALESCE("sales"."previous_units", 0) AS "previous_units", COALESCE("sales"."previous_sales", 0) AS "previous_sales", 0 AS "third_units", 0 AS "third_sales", COALESCE("stock"."on_hand", 0) AS "on_hand", COALESCE("stock"."on_hand_retail", 0) AS "on_hand_retail", 0 AS "previous_on_hand", 0 AS "on_order", 0 AS "on_order_retail", 0 AS "ad_cost", (CAST(COALESCE("sales"."current_units", 0) AS float8) - CAST(COALESCE("sales"."previous_units", 0) AS float8)) AS "units_change", (((CAST(COALESCE("sales"."current_units", 0) AS float8) - CAST(COALESCE("sales"."previous_units", 0) AS float8)) / NULLIF(CAST(COALESCE("sales"."previous_units", 0) AS float8), 0)) * $22) AS "units_pct", (COALESCE("sales"."current_sales", 0) - COALESCE("sales"."previous_sales", 0)) AS "sales_change", (((COALESCE("sales"."current_sales", 0) - COALESCE("sales"."previous_sales", 0)) / NULLIF(COALESCE("sales"."previous_sales", 0), 0)) * $23) AS "sales_pct", ((COALESCE("sales"."current_margin", 0) / NULLIF(COALESCE("sales"."current_sales", 0), 0)) * $24) AS "margin_pct", ((CAST(COALESCE("sales"."current_units", 0) AS float8) / NULLIF((CAST(COALESCE("sales"."current_units", 0) AS float8) + CAST(COALESCE("stock"."on_hand", 0) AS float8)), 0)) * $25) AS "sell_through", ((CAST(COALESCE("sales"."returned_units", 0) AS float8) / NULLIF((CAST(COALESCE("sales"."current_units", 0) AS float8) + CAST(COALESCE("sales"."returned_units", 0) AS float8)), 0)) * $26) AS "returns_pct", NULL AS "roas" FROM "keys" AS "k" LEFT JOIN "sales" AS "sales" USING ("key") LEFT JOIN "stock" AS "stock" USING ("key"))
	// SELECT "r"."key", "r"."key", NULL, "r"."current_units", "r"."current_sales", "r"."current_cost", "r"."current_margin", "r"."returned_units", "r"."previous_units", "r"."previous_sales", "r"."third_units", "r"."third_sales", "r"."on_hand", "r"."on_hand_retail", "r"."previous_on_hand", "r"."on_order", "r"."on_order_retail", "r"."ad_cost", "r"."units_change", "r"."units_pct", "r"."sales_change", "r"."sales_pct", "r"."margin_pct", "r"."sell_through", "r"."returns_pct", "r"."roas", count(*) OVER () FROM "report" AS "r" ORDER BY "r"."key" ASC NULLS LAST LIMIT $27 OFFSET $28
	// args: 28
	// disabled: [snapshots on_order ad_spend]
}

// Fiscal month: the key is the period index, so the same period of three
// years lands on one row; the label comes from the current year's calendar.
func Example_fiscalMonth() {
	show(periodcompare.Build(periodcompare.Params{
		Dimension:  periodcompare.ByFiscalMonth,
		FiscalYear: 2026,
		Current:    cur,
		Previous:   prev,
		Third:      third,
		Include:    periodcompare.Include{ThirdYear: true},
		Sort:       periodcompare.Sort{By: periodcompare.MetricKey, Dir: periodcompare.Asc},
	}))
	// Output:
	// WITH "sales" AS (SELECT CAST("fc"."period_index" AS text) AS "key", sum("s"."qty") FILTER (WHERE "s"."sold_on" BETWEEN $1 AND $2) AS "current_units", sum((CAST("s"."qty" AS float8) * "s"."unit_price")) FILTER (WHERE "s"."sold_on" BETWEEN $3 AND $4) AS "current_sales", sum((CAST("s"."qty" AS float8) * "s"."unit_cost")) FILTER (WHERE "s"."sold_on" BETWEEN $5 AND $6) AS "current_cost", sum(((CAST("s"."qty" AS float8) * "s"."unit_price") - (CAST("s"."qty" AS float8) * "s"."unit_cost"))) FILTER (WHERE "s"."sold_on" BETWEEN $7 AND $8) AS "current_margin", sum(CASE WHEN "s"."qty" < $9 THEN (-"s"."qty") ELSE $10 END) FILTER (WHERE "s"."sold_on" BETWEEN $11 AND $12) AS "returned_units", sum("s"."qty") FILTER (WHERE "s"."sold_on" BETWEEN $13 AND $14) AS "previous_units", sum((CAST("s"."qty" AS float8) * "s"."unit_price")) FILTER (WHERE "s"."sold_on" BETWEEN $15 AND $16) AS "previous_sales", sum("s"."qty") FILTER (WHERE "s"."sold_on" BETWEEN $17 AND $18) AS "third_units", sum((CAST("s"."qty" AS float8) * "s"."unit_price")) FILTER (WHERE "s"."sold_on" BETWEEN $19 AND $20) AS "third_sales" FROM "sales_lines" AS "s" INNER JOIN "fiscal_calendar" AS "fc" ON ("fc"."day" = "s"."sold_on") WHERE ("s"."is_gift" = $21 AND "s"."sold_on" BETWEEN $22 AND $23) GROUP BY CAST("fc"."period_index" AS text)),
	// "keys" AS (SELECT "sales"."key" FROM "sales" AS "sales"),
	// "report" AS (SELECT "k"."key" AS "key", COALESCE("sales"."current_units", 0) AS "current_units", COALESCE("sales"."current_sales", 0) AS "current_sales", COALESCE("sales"."current_cost", 0) AS "current_cost", COALESCE("sales"."current_margin", 0) AS "current_margin", COALESCE("sales"."returned_units", 0) AS "returned_units", COALESCE("sales"."previous_units", 0) AS "previous_units", COALESCE("sales"."previous_sales", 0) AS "previous_sales", COALESCE("sales"."third_units", 0) AS "third_units", COALESCE("sales"."third_sales", 0) AS "third_sales", 0 AS "on_hand", 0 AS "on_hand_retail", 0 AS "previous_on_hand", 0 AS "on_order", 0 AS "on_order_retail", 0 AS "ad_cost", (CAST(COALESCE("sales"."current_units", 0) AS float8) - CAST(COALESCE("sales"."previous_units", 0) AS float8)) AS "units_change", (((CAST(COALESCE("sales"."current_units", 0) AS float8) - CAST(COALESCE("sales"."previous_units", 0) AS float8)) / NULLIF(CAST(COALESCE("sales"."previous_units", 0) AS float8), 0)) * $24) AS "units_pct", (COALESCE("sales"."current_sales", 0) - COALESCE("sales"."previous_sales", 0)) AS "sales_change", (((COALESCE("sales"."current_sales", 0) - COALESCE("sales"."previous_sales", 0)) / NULLIF(COALESCE("sales"."previous_sales", 0), 0)) * $25) AS "sales_pct", ((COALESCE("sales"."current_margin", 0) / NULLIF(COALESCE("sales"."current_sales", 0), 0)) * $26) AS "margin_pct", NULL AS "sell_through", ((CAST(COALESCE("sales"."returned_units", 0) AS float8) / NULLIF((CAST(COALESCE("sales"."current_units", 0) AS float8) + CAST(COALESCE("sales"."returned_units", 0) AS float8)), 0)) * $27) AS "returns_pct", NULL AS "roas" FROM "keys" AS "k" LEFT JOIN "sales" AS "sales" USING ("key")),
	// "labels" AS (SELECT DISTINCT CAST("fc"."period_index" AS text) AS "key", "fc"."period_label" AS "label", NULL AS "image_url" FROM "fiscal_calendar" AS "fc" WHERE ("fc"."fiscal_year" = $28))
	// SELECT "r"."key", COALESCE("l"."label", "r"."key"),
	// "l"."image_url", "r"."current_units", "r"."current_sales", "r"."current_cost", "r"."current_margin", "r"."returned_units", "r"."previous_units", "r"."previous_sales", "r"."third_units", "r"."third_sales", "r"."on_hand", "r"."on_hand_retail", "r"."previous_on_hand", "r"."on_order", "r"."on_order_retail", "r"."ad_cost", "r"."units_change", "r"."units_pct", "r"."sales_change", "r"."sales_pct", "r"."margin_pct", "r"."sell_through", "r"."returns_pct", "r"."roas", count(*) OVER () FROM "report" AS "r" LEFT JOIN "labels" AS "l" USING ("key") ORDER BY "r"."key" ASC NULLS LAST LIMIT $29 OFFSET $30
	// args: 30
	// disabled: [stock snapshots on_order ad_spend]
}

// A class filter on a dimension that does not join categories adds the
// join; on one that does, it reuses it.
func Example_classFilterJoins() {
	show(periodcompare.Build(periodcompare.Params{
		Dimension: periodcompare.ByVendor,
		Current:   cur, Previous: prev,
		Filters: periodcompare.Filters{Classes: []string{"Running"}},
	}))
	// Output:
	// WITH "sales" AS (SELECT "v"."name" AS "key", sum("s"."qty") FILTER (WHERE "s"."sold_on" BETWEEN $1 AND $2) AS "current_units", sum((CAST("s"."qty" AS float8) * "s"."unit_price")) FILTER (WHERE "s"."sold_on" BETWEEN $3 AND $4) AS "current_sales", sum((CAST("s"."qty" AS float8) * "s"."unit_cost")) FILTER (WHERE "s"."sold_on" BETWEEN $5 AND $6) AS "current_cost", sum(((CAST("s"."qty" AS float8) * "s"."unit_price") - (CAST("s"."qty" AS float8) * "s"."unit_cost"))) FILTER (WHERE "s"."sold_on" BETWEEN $7 AND $8) AS "current_margin", sum(CASE WHEN "s"."qty" < $9 THEN (-"s"."qty") ELSE $10 END) FILTER (WHERE "s"."sold_on" BETWEEN $11 AND $12) AS "returned_units", sum("s"."qty") FILTER (WHERE "s"."sold_on" BETWEEN $13 AND $14) AS "previous_units", sum((CAST("s"."qty" AS float8) * "s"."unit_price")) FILTER (WHERE "s"."sold_on" BETWEEN $15 AND $16) AS "previous_sales" FROM "sales_lines" AS "s" INNER JOIN "vendors" AS "v" ON ("v"."id" = "s"."vendor_id") INNER JOIN "products" AS "p" ON ("p"."id" = "s"."product_id") INNER JOIN "categories" AS "c" ON ("c"."id" = "p"."category_id") WHERE ("c"."class_name" = ANY($17) AND "s"."is_gift" = $18 AND "s"."sold_on" BETWEEN $19 AND $20) GROUP BY "v"."name"),
	// "stock" AS (SELECT "v"."name" AS "key", sum("sl"."on_hand") AS "on_hand", sum((CAST("sl"."on_hand" AS float8) * "sl"."retail_price")) AS "on_hand_retail" FROM "stock_levels" AS "sl" INNER JOIN "vendors" AS "v" ON ("v"."id" = "sl"."vendor_id") INNER JOIN "products" AS "p" ON ("p"."id" = "sl"."product_id") INNER JOIN "categories" AS "c" ON ("c"."id" = "p"."category_id") WHERE ("c"."class_name" = ANY($21)) GROUP BY "v"."name"),
	// "keys" AS (SELECT "sales"."key" FROM "sales" AS "sales" UNION SELECT "stock"."key" FROM "stock" AS "stock"),
	// "report" AS (SELECT "k"."key" AS "key", COALESCE("sales"."current_units", 0) AS "current_units", COALESCE("sales"."current_sales", 0) AS "current_sales", COALESCE("sales"."current_cost", 0) AS "current_cost", COALESCE("sales"."current_margin", 0) AS "current_margin", COALESCE("sales"."returned_units", 0) AS "returned_units", COALESCE("sales"."previous_units", 0) AS "previous_units", COALESCE("sales"."previous_sales", 0) AS "previous_sales", 0 AS "third_units", 0 AS "third_sales", COALESCE("stock"."on_hand", 0) AS "on_hand", COALESCE("stock"."on_hand_retail", 0) AS "on_hand_retail", 0 AS "previous_on_hand", 0 AS "on_order", 0 AS "on_order_retail", 0 AS "ad_cost", (CAST(COALESCE("sales"."current_units", 0) AS float8) - CAST(COALESCE("sales"."previous_units", 0) AS float8)) AS "units_change", (((CAST(COALESCE("sales"."current_units", 0) AS float8) - CAST(COALESCE("sales"."previous_units", 0) AS float8)) / NULLIF(CAST(COALESCE("sales"."previous_units", 0) AS float8), 0)) * $22) AS "units_pct", (COALESCE("sales"."current_sales", 0) - COALESCE("sales"."previous_sales", 0)) AS "sales_change", (((COALESCE("sales"."current_sales", 0) - COALESCE("sales"."previous_sales", 0)) / NULLIF(COALESCE("sales"."previous_sales", 0), 0)) * $23) AS "sales_pct", ((COALESCE("sales"."current_margin", 0) / NULLIF(COALESCE("sales"."current_sales", 0), 0)) * $24) AS "margin_pct", ((CAST(COALESCE("sales"."current_units", 0) AS float8) / NULLIF((CAST(COALESCE("sales"."current_units", 0) AS float8) + CAST(COALESCE("stock"."on_hand", 0) AS float8)), 0)) * $25) AS "sell_through", ((CAST(COALESCE("sales"."returned_units", 0) AS float8) / NULLIF((CAST(COALESCE("sales"."current_units", 0) AS float8) + CAST(COALESCE("sales"."returned_units", 0) AS float8)), 0)) * $26) AS "returns_pct", NULL AS "roas" FROM "keys" AS "k" LEFT JOIN "sales" AS "sales" USING ("key") LEFT JOIN "stock" AS "stock" USING ("key"))
	// SELECT "r"."key", "r"."key", NULL, "r"."current_units", "r"."current_sales", "r"."current_cost", "r"."current_margin", "r"."returned_units", "r"."previous_units", "r"."previous_sales", "r"."third_units", "r"."third_sales", "r"."on_hand", "r"."on_hand_retail", "r"."previous_on_hand", "r"."on_order", "r"."on_order_retail", "r"."ad_cost", "r"."units_change", "r"."units_pct", "r"."sales_change", "r"."sales_pct", "r"."margin_pct", "r"."sell_through", "r"."returns_pct", "r"."roas", count(*) OVER () FROM "report" AS "r" ORDER BY "r"."key" ASC NULLS LAST LIMIT $27 OFFSET $28
	// args: 28
	// disabled: [snapshots on_order ad_spend]
}

// The build refuses what it cannot type: an unknown metric, an unknown
// operator, a fiscal report without a year.
func TestBuild_RefusesUnknownVocabulary(t *testing.T) {
	base := periodcompare.Params{Dimension: periodcompare.ByVendor, Current: cur, Previous: prev}
	bad := base
	bad.Filters.Numeric = []periodcompare.NumericFilter{{Metric: "nope", Op: periodcompare.OpGreater, Value: 1}}
	if _, err := periodcompare.Build(bad); !errors.Is(err, periodcompare.ErrUnknownMetric) {
		t.Fatalf("unknown metric: got %v", err)
	}
	bad = base
	bad.Filters.Numeric = []periodcompare.NumericFilter{{Metric: periodcompare.MetricROAS, Op: "like", Value: 1}}
	if _, err := periodcompare.Build(bad); !errors.Is(err, periodcompare.ErrUnknownOperator) {
		t.Fatalf("unknown operator: got %v", err)
	}
	bad = base
	bad.Sort = periodcompare.Sort{By: "DROP TABLE"}
	if _, err := periodcompare.Build(bad); !errors.Is(err, periodcompare.ErrUnknownMetric) {
		t.Fatalf("unknown sort: got %v", err)
	}
	bad = base
	bad.Dimension = periodcompare.ByFiscalMonth
	if _, err := periodcompare.Build(bad); !errors.Is(err, periodcompare.ErrFiscalYear) {
		t.Fatalf("fiscal without year: got %v", err)
	}
	bad = base
	bad.Dimension = "galaxy"
	if _, err := periodcompare.Build(bad); !errors.Is(err, periodcompare.ErrUnknownDimension) {
		t.Fatalf("unknown dimension: got %v", err)
	}
}

// Every request value is bound; none appears in the SQL text.
func TestBuild_BindsEveryValue(t *testing.T) {
	r, err := periodcompare.Build(periodcompare.Params{
		Dimension: periodcompare.ByVendor, Current: cur, Previous: prev,
		Filters: periodcompare.Filters{VendorIDs: []string{"v'1"}, KeyIn: []string{"x'y"}, Departments: []string{"D'1"}},
		Page:    periodcompare.Page{Size: 7, Number: 2},
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, needle := range []string{"v'1", "x'y", "D'1", "2026-09", "LIMIT 7", "OFFSET 7"} {
		if strings.Contains(r.Query.SQL, needle) {
			t.Fatalf("%q leaked into SQL text", needle)
		}
	}
	if len(r.Query.Targets) == 0 || r.Row == nil {
		t.Fatal("report must bind scan targets")
	}
}
