package periodcompare

import "github.com/ubgo/sqlb"

// Metric names every column of the report CTE that a caller may sort or
// filter on. It is the public vocabulary of the report and a closed set, so
// a sort field from a request is matched against it and never interpolated.
type Metric string

// Every metric the report computes. A sort or filter field from a request is
// matched against this set, which is what keeps request text out of the SQL.
const (
	MetricKey           Metric = "key"
	MetricCurrentUnits  Metric = "current_units"
	MetricCurrentSales  Metric = "current_sales"
	MetricCurrentCost   Metric = "current_cost"
	MetricCurrentMargin Metric = "current_margin"
	MetricReturnedUnits Metric = "returned_units"
	MetricPreviousUnits Metric = "previous_units"
	MetricPreviousSales Metric = "previous_sales"
	MetricThirdUnits    Metric = "third_units"
	MetricThirdSales    Metric = "third_sales"
	MetricOnHand        Metric = "on_hand"
	MetricOnHandRetail  Metric = "on_hand_retail"
	MetricPrevOnHand    Metric = "previous_on_hand"
	MetricOnOrder       Metric = "on_order"
	MetricOnOrderRetail Metric = "on_order_retail"
	MetricAdCost        Metric = "ad_cost"
	MetricUnitsChange   Metric = "units_change"
	MetricUnitsPct      Metric = "units_pct"
	MetricSalesChange   Metric = "sales_change"
	MetricSalesPct      Metric = "sales_pct"
	MetricMarginPct     Metric = "margin_pct"
	MetricSellThrough   Metric = "sell_through"
	MetricReturnsPct    Metric = "returns_pct"
	MetricROAS          Metric = "roas"
)

// report is the report CTE as a source, for the outer query.
var report = sqlb.Table{Name: cteReport, Alias: "r"}

// metricColumns maps every metric to its typed column on the report CTE.
// Numeric filters and sorts go through this map, so an unknown metric is
// rejected and a known one is typed.
func metricColumn(m Metric) (sqlb.SQLer, bool) {
	switch m {
	case MetricKey:
		return sqlb.Col[string](report, string(m)), true
	case MetricCurrentUnits, MetricReturnedUnits, MetricPreviousUnits, MetricThirdUnits,
		MetricOnHand, MetricPrevOnHand, MetricOnOrder:
		return sqlb.Col[int64](report, string(m)), true
	case MetricCurrentSales, MetricCurrentCost, MetricCurrentMargin, MetricPreviousSales, MetricThirdSales,
		MetricOnHandRetail, MetricOnOrderRetail, MetricAdCost, MetricUnitsChange, MetricSalesChange:
		return sqlb.Col[float64](report, string(m)), true
	case MetricUnitsPct, MetricSalesPct, MetricMarginPct, MetricSellThrough, MetricReturnsPct, MetricROAS:
		return sqlb.Col[*float64](report, string(m)), true
	}
	return nil, false
}

// Row is the scan destination. Ratios are pointers because a zero
// denominator yields NULL, which is the honest value and not 0.
type Row struct {
	Key      string
	Label    string
	ImageURL *string

	CurrentUnits  int64
	CurrentSales  float64
	CurrentCost   float64
	CurrentMargin float64
	ReturnedUnits int64
	PreviousUnits int64
	PreviousSales float64
	ThirdUnits    int64
	ThirdSales    float64
	OnHand        int64
	OnHandRetail  float64
	PrevOnHand    int64
	OnOrder       int64
	OnOrderRetail float64
	AdCost        float64

	UnitsChange float64
	UnitsPct    *float64
	SalesChange float64
	SalesPct    *float64
	MarginPct   *float64
	SellThrough *float64
	ReturnsPct  *float64
	ROAS        *float64

	// Total is the row count before pagination, from count(*) OVER ().
	Total int64
}
