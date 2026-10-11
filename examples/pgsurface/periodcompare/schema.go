// Package periodcompare builds a period-comparison report over several fact
// tables in Postgres: sales for the current, previous and third-year
// periods, current stock, stock at a past snapshot date, open purchase
// orders and advertising spend, all keyed by one chosen dimension.
//
// The shape it avoids is the wide UNION ALL, where every source repeats the
// full measure list with typed zeros so the branches line up by position.
// Here each source is one CTE that selects only its own measures, a keys CTE
// unions the distinct keys, and the report CTE LEFT JOINs each source back
// onto the keys with COALESCE. Same-table periods are one scan with
// FILTER instead of one branch per period. Computed metrics live in the
// report CTE, so the outer query filters and sorts on them by name, and the
// total row count comes from a window function in the same statement.
//
// Everything is typed: every column, every join, every scan target. A
// source that cannot serve the chosen dimension is left out, its measures
// render as typed zeros, and the build result names it.
package periodcompare

import (
	"time"

	"github.com/ubgo/sqlb"
)

// Fact and dimension tables. Aliases are per statement; every source is its
// own CTE, so the product and category aliases repeat across sources
// without collision.
var (
	Products   = sqlb.Table{Name: "products", Alias: "p"}
	Vendors    = sqlb.Table{Name: "vendors", Alias: "v"}
	Categories = sqlb.Table{Name: "categories", Alias: "c"}
	Stores     = sqlb.Table{Name: "stores", Alias: "st"}
	Calendar   = sqlb.Table{Name: "fiscal_calendar", Alias: "fc"}
	Keywords   = sqlb.Table{Name: "product_keywords", Alias: "kw"}

	SalesLines     = sqlb.Table{Name: "sales_lines", Alias: "s"}
	StockLevels    = sqlb.Table{Name: "stock_levels", Alias: "sl"}
	StockSnapshots = sqlb.Table{Name: "stock_snapshots", Alias: "ss"}
	POLines        = sqlb.Table{Name: "po_lines", Alias: "po"}
	AdSpend        = sqlb.Table{Name: "ad_spend", Alias: "ad"}
)

// The typed columns of every table the report reads.
var (
	ProductID       = sqlb.Col[string](Products, "id")
	ProductVendorID = sqlb.Col[string](Products, "vendor_id")
	ProductCatID    = sqlb.Col[string](Products, "category_id")
	ProductName     = sqlb.Col[string](Products, "name")
	ProductCode     = sqlb.Col[string](Products, "code")
	ProductImage    = sqlb.Col[*string](Products, "image_url")

	VendorID   = sqlb.Col[string](Vendors, "id")
	VendorName = sqlb.Col[string](Vendors, "name")

	CategoryID   = sqlb.Col[string](Categories, "id")
	Department   = sqlb.Col[string](Categories, "department")
	ClassName    = sqlb.Col[string](Categories, "class_name")
	CategoryName = sqlb.Col[string](Categories, "name")

	StoreID   = sqlb.Col[int32](Stores, "id")
	StoreName = sqlb.Col[string](Stores, "name")

	CalDay         = sqlb.Col[time.Time](Calendar, "day")
	CalFiscalYear  = sqlb.Col[int32](Calendar, "fiscal_year")
	CalPeriodIndex = sqlb.Col[int32](Calendar, "period_index")
	CalPeriodLabel = sqlb.Col[string](Calendar, "period_label")

	KwProductID = sqlb.Col[string](Keywords, "product_id")
	KwKeywordID = sqlb.Col[int64](Keywords, "keyword_id")

	SaleProductID = sqlb.Col[string](SalesLines, "product_id")
	SaleVendorID  = sqlb.Col[string](SalesLines, "vendor_id")
	SaleStoreID   = sqlb.Col[int32](SalesLines, "store_id")
	SaleSoldOn    = sqlb.Col[time.Time](SalesLines, "sold_on")
	SaleQty       = sqlb.Col[int64](SalesLines, "qty")
	SaleUnitPrice = sqlb.Col[float64](SalesLines, "unit_price")
	SaleListPrice = sqlb.Col[float64](SalesLines, "list_price")
	SaleUnitCost  = sqlb.Col[float64](SalesLines, "unit_cost")
	SaleIsGift    = sqlb.Col[bool](SalesLines, "is_gift")

	StockProductID = sqlb.Col[string](StockLevels, "product_id")
	StockVendorID  = sqlb.Col[string](StockLevels, "vendor_id")
	StockStoreID   = sqlb.Col[int32](StockLevels, "store_id")
	StockOnHand    = sqlb.Col[int64](StockLevels, "on_hand")
	StockRetail    = sqlb.Col[float64](StockLevels, "retail_price")

	SnapProductID = sqlb.Col[string](StockSnapshots, "product_id")
	SnapVendorID  = sqlb.Col[string](StockSnapshots, "vendor_id")
	SnapStoreID   = sqlb.Col[int32](StockSnapshots, "store_id")
	SnapDate      = sqlb.Col[time.Time](StockSnapshots, "snapshot_date")
	SnapQty       = sqlb.Col[int64](StockSnapshots, "qty")
	SnapRetail    = sqlb.Col[float64](StockSnapshots, "retail_price")

	POProductID = sqlb.Col[string](POLines, "product_id")
	POVendorID  = sqlb.Col[string](POLines, "vendor_id")
	POQtyDue    = sqlb.Col[int64](POLines, "qty_due")
	POUnitPrice = sqlb.Col[float64](POLines, "unit_price")
	POCancelled = sqlb.Col[bool](POLines, "cancelled")

	AdProductID = sqlb.Col[string](AdSpend, "product_id")
	AdVendorID  = sqlb.Col[string](AdSpend, "vendor_id")
	AdDay       = sqlb.Col[time.Time](AdSpend, "day")
	AdCost      = sqlb.Col[float64](AdSpend, "cost")
)

// Names of the CTEs and of the columns they expose. Constants, because the
// outer query references them by name and a typo would be a runtime error.
const (
	cteSales     = "sales"
	cteStock     = "stock"
	cteSnapshots = "snapshots"
	cteOnOrder   = "on_order"
	cteAdSpend   = "ad_spend"
	cteKeys      = "keys"
	cteLabels    = "labels"
	cteReport    = "report"

	colKey = "key"
)
