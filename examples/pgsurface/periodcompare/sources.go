package periodcompare

import (
	"github.com/ubgo/sqlb"
	"github.com/ubgo/sqlb/pg"
)

// source is one fact table's contribution: a CTE grouped by key that selects
// only the measures this fact owns. The report CTE reads them by name.
type source struct {
	name     string
	enabled  bool
	body     *sqlb.Select[sqlb.PostgresDialect]
	measures []string
	// skipped lists the filters this source could not honour.
	skipped []string
}

// table returns the CTE as a source for the outer statement.
func (s source) table() sqlb.Table { return sqlb.Table{Name: s.name, Alias: s.name} }

// groupedBody starts a source CTE: FROM fact, the dimension joins, the key
// as the first select item, the filters, GROUP BY key.
func groupedBody(p Params, fact sqlb.Table, f factColumns, sales bool) (*sqlb.Select[sqlb.PostgresDialect], []string, bool) {
	if !p.Dimension.serves(f) {
		return nil, nil, false
	}
	plan := p.Dimension.plan(f)
	preds, joins, skipped := rowFilters(p, f, plan, sales)
	s := sqlb.NewSelect(dialect).
		Exprs(sqlb.As(plan.key, colKey)).
		From(fact).
		Join(plan.joins...).
		Join(joins...).
		Where(preds...).
		GroupBy(plan.key)
	return s, skipped, true
}

// salesSource is one scan of sales_lines for all periods. Each period's
// measures are FILTER aggregates, so the three-period comparison is one
// pass over the date span, not three branches of a union.
func salesSource(p Params) source {
	f := factColumns{product: SaleProductID, vendor: SaleVendorID, store: &SaleStoreID, date: &SaleSoldOn}
	body, skipped, ok := groupedBody(p, SalesLines, f, true)
	if !ok {
		return source{name: cteSales}
	}
	earliest, latest := p.Current.From, p.Current.To
	if p.Previous.From.Before(earliest) {
		earliest = p.Previous.From
	}
	if p.Include.ThirdYear && p.Third.From.Before(earliest) {
		earliest = p.Third.From
	}
	body = body.Where(sqlb.Between(SaleSoldOn, earliest, latest))

	cur := pg.Filter(sqlb.Between(SaleSoldOn, p.Current.From, p.Current.To))
	prev := pg.Filter(sqlb.Between(SaleSoldOn, p.Previous.From, p.Previous.To))
	revenue := sqlb.Mul(sqlb.Cast[float64](SaleQty, "float8"), SaleUnitPrice)
	cost := sqlb.Mul(sqlb.Cast[float64](SaleQty, "float8"), SaleUnitCost)
	returned := sqlb.Case[int64]().When(sqlb.Less(SaleQty, 0), sqlb.Neg(SaleQty)).Else(sqlb.Param[int64](0))

	body = body.Exprs(
		sqlb.As(sqlb.Sum(SaleQty, cur), string(MetricCurrentUnits)),
		sqlb.As(sqlb.Sum(revenue, cur), string(MetricCurrentSales)),
		sqlb.As(sqlb.Sum(cost, cur), string(MetricCurrentCost)),
		sqlb.As(sqlb.Sum(sqlb.Sub(revenue, cost), cur), string(MetricCurrentMargin)),
		sqlb.As(sqlb.Sum(returned, cur), string(MetricReturnedUnits)),
		sqlb.As(sqlb.Sum(SaleQty, prev), string(MetricPreviousUnits)),
		sqlb.As(sqlb.Sum(revenue, prev), string(MetricPreviousSales)),
	)
	measures := []string{
		string(MetricCurrentUnits), string(MetricCurrentSales), string(MetricCurrentCost), string(MetricCurrentMargin),
		string(MetricReturnedUnits), string(MetricPreviousUnits), string(MetricPreviousSales),
	}
	if p.Include.ThirdYear {
		third := pg.Filter(sqlb.Between(SaleSoldOn, p.Third.From, p.Third.To))
		body = body.Exprs(
			sqlb.As(sqlb.Sum(SaleQty, third), string(MetricThirdUnits)),
			sqlb.As(sqlb.Sum(revenue, third), string(MetricThirdSales)),
		)
		measures = append(measures, string(MetricThirdUnits), string(MetricThirdSales))
	}
	return source{name: cteSales, enabled: true, body: body, measures: measures, skipped: skipped}
}

// stockSource is current stock. It has no date, so it cannot serve the
// fiscal-month dimension.
func stockSource(p Params) source {
	f := factColumns{product: StockProductID, vendor: StockVendorID, store: &StockStoreID}
	body, skipped, ok := groupedBody(p, StockLevels, f, false)
	if !ok {
		return source{name: cteStock}
	}
	body = body.Exprs(
		sqlb.As(sqlb.Sum(StockOnHand), string(MetricOnHand)),
		sqlb.As(sqlb.Sum(sqlb.Mul(sqlb.Cast[float64](StockOnHand, "float8"), StockRetail)), string(MetricOnHandRetail)),
	)
	return source{name: cteStock, enabled: true, body: body, measures: []string{string(MetricOnHand), string(MetricOnHandRetail)}, skipped: skipped}
}

// snapshotSource is stock at the Saturday closing the previous period. One
// scan, restricted to that date; the third-year snapshot would be a second
// FILTER on the same scan.
func snapshotSource(p Params) source {
	if !p.Include.Snapshot {
		return source{name: cteSnapshots}
	}
	f := factColumns{product: SnapProductID, vendor: SnapVendorID, store: &SnapStoreID, date: &SnapDate}
	body, skipped, ok := groupedBody(p, StockSnapshots, f, false)
	if !ok {
		return source{name: cteSnapshots}
	}
	body = body.Where(sqlb.Eq(SnapDate, previousSaturday(p.Previous.To)))
	body = body.Exprs(sqlb.As(sqlb.Sum(SnapQty), string(MetricPrevOnHand)))
	return source{name: cteSnapshots, enabled: true, body: body, measures: []string{string(MetricPrevOnHand)}, skipped: skipped}
}

// onOrderSource is open purchase-order quantity. No store, no date.
func onOrderSource(p Params) source {
	if !p.Include.OnOrder {
		return source{name: cteOnOrder}
	}
	f := factColumns{product: POProductID, vendor: POVendorID}
	body, skipped, ok := groupedBody(p, POLines, f, false)
	if !ok {
		return source{name: cteOnOrder}
	}
	body = body.Where(sqlb.Greater(POQtyDue, 0), sqlb.Eq(POCancelled, false))
	body = body.Exprs(
		sqlb.As(sqlb.Sum(POQtyDue), string(MetricOnOrder)),
		sqlb.As(sqlb.Sum(sqlb.Mul(sqlb.Cast[float64](POQtyDue, "float8"), POUnitPrice)), string(MetricOnOrderRetail)),
	)
	return source{name: cteOnOrder, enabled: true, body: body, measures: []string{string(MetricOnOrder), string(MetricOnOrderRetail)}, skipped: skipped}
}

// adSpendSource is advertising cost in the current period.
func adSpendSource(p Params) source {
	if !p.Include.AdSpend {
		return source{name: cteAdSpend}
	}
	f := factColumns{product: AdProductID, vendor: AdVendorID, date: &AdDay}
	body, skipped, ok := groupedBody(p, AdSpend, f, false)
	if !ok {
		return source{name: cteAdSpend}
	}
	body = body.Where(sqlb.Between(AdDay, p.Current.From, p.Current.To))
	body = body.Exprs(sqlb.As(sqlb.Sum(AdCost), string(MetricAdCost)))
	return source{name: cteAdSpend, enabled: true, body: body, measures: []string{string(MetricAdCost)}, skipped: skipped}
}
