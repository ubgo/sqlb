package periodcompare

import (
	"errors"
	"slices"

	"github.com/ubgo/sqlb"
	"github.com/ubgo/sqlb/pg"
)

var dialect = sqlb.PostgresDialect{}

// The errors a request can provoke. Each means the caller named something
// outside a closed set, which is the only way request data can fail here:
// values bind, and names are matched rather than interpolated.
var (
	ErrUnknownDimension = errors.New("periodcompare: unknown dimension")
	ErrUnknownMetric    = errors.New("periodcompare: unknown metric in filter or sort")
	ErrUnknownOperator  = errors.New("periodcompare: unknown numeric operator")
	ErrFiscalYear       = errors.New("periodcompare: fiscal-month dimension needs FiscalYear")
)

// Result is the built statement plus what the build left out. A caller that
// shows the report can show the omissions beside it instead of showing zeros
// that look like data.
type Result struct {
	Query sqlb.Query
	// Row is the scan destination bound into Query. Copy it after each scan.
	Row *Row
	// DisabledSources names sources that cannot serve the dimension or were
	// not included; their measures are zeros in every row.
	DisabledSources []string
	// SkippedFilters names filters a source could not apply, as "source: filter".
	SkippedFilters []string
}

// Build renders the report for p. One statement returns the page and the
// total count.
func Build(p Params) (Result, error) {
	if !slices.Contains(DimensionValues, p.Dimension) {
		return Result{}, ErrUnknownDimension
	}
	if p.Dimension == ByFiscalMonth && p.FiscalYear == 0 {
		return Result{}, ErrFiscalYear
	}
	p.Page = p.Page.normalized()

	sources := []source{salesSource(p), stockSource(p), snapshotSource(p), onOrderSource(p), adSpendSource(p)}
	var res Result
	row := &Row{}
	res.Row = row

	outer := sqlb.NewSelect(dialect)
	var keys *sqlb.Select[sqlb.PostgresDialect]
	enabled := map[string]bool{}
	for _, s := range sources {
		if !s.enabled {
			res.DisabledSources = append(res.DisabledSources, s.name)
			continue
		}
		enabled[s.name] = true
		for _, f := range s.skipped {
			res.SkippedFilters = append(res.SkippedFilters, s.name+": "+f)
		}
		outer = outer.With(s.name, s.body)
		k := sqlb.NewSelect(dialect).Exprs(sqlb.Col[string](s.table(), colKey)).From(s.table())
		if keys == nil {
			keys = k
		} else {
			keys = keys.Union(k)
		}
	}
	outer = outer.With(cteKeys, keys)

	// The report CTE: keys LEFT JOIN each source, COALESCE every measure,
	// then the computed metrics over the coalesced measures.
	keysT := sqlb.Table{Name: cteKeys, Alias: "k"}
	key := sqlb.Col[string](keysT, colKey)
	rep := sqlb.NewSelect(dialect).Exprs(sqlb.As(key, colKey)).From(keysT)
	for _, s := range sources {
		if s.enabled {
			rep = rep.Join(sqlb.Join{Type: sqlb.JoinLeft, Table: s.table(), Using: []string{colKey}})
		}
	}
	m := measures{enabled: enabled}
	curUnits := m.int(cteSales, MetricCurrentUnits)
	curSales := m.float(cteSales, MetricCurrentSales)
	curCost := m.float(cteSales, MetricCurrentCost)
	curMargin := m.float(cteSales, MetricCurrentMargin)
	returned := m.int(cteSales, MetricReturnedUnits)
	prevUnits := m.int(cteSales, MetricPreviousUnits)
	prevSales := m.float(cteSales, MetricPreviousSales)
	thirdUnits := m.int(cteSales, MetricThirdUnits)
	thirdSales := m.float(cteSales, MetricThirdSales)
	onHand := m.int(cteStock, MetricOnHand)
	onHandRetail := m.float(cteStock, MetricOnHandRetail)
	prevOnHand := m.int(cteSnapshots, MetricPrevOnHand)
	onOrder := m.int(cteOnOrder, MetricOnOrder)
	onOrderRetail := m.float(cteOnOrder, MetricOnOrderRetail)
	adCost := m.float(cteAdSpend, MetricAdCost)
	if !p.Include.ThirdYear {
		thirdUnits, thirdSales = zeroInt, zeroFloat
	}
	// A ratio whose denominator comes from a disabled source is NULL, not a
	// number computed over a zero that only looks like data.
	sellThrough := pct(sqlb.Cast[float64](curUnits, "float8"), sqlb.Add(sqlb.Cast[float64](curUnits, "float8"), sqlb.Cast[float64](onHand, "float8")), sqlb.Param(100.0))
	if !enabled[cteStock] {
		sellThrough = nullRatio
	}
	roas := ratio(curSales, adCost)
	if !enabled[cteAdSpend] {
		roas = nullRatio
	}

	fCur := sqlb.Cast[float64](curUnits, "float8")
	fPrev := sqlb.Cast[float64](prevUnits, "float8")
	fRet := sqlb.Cast[float64](returned, "float8")
	hundred := sqlb.Param(100.0)

	rep = rep.Exprs(
		sqlb.As(curUnits, string(MetricCurrentUnits)),
		sqlb.As(curSales, string(MetricCurrentSales)),
		sqlb.As(curCost, string(MetricCurrentCost)),
		sqlb.As(curMargin, string(MetricCurrentMargin)),
		sqlb.As(returned, string(MetricReturnedUnits)),
		sqlb.As(prevUnits, string(MetricPreviousUnits)),
		sqlb.As(prevSales, string(MetricPreviousSales)),
		sqlb.As(thirdUnits, string(MetricThirdUnits)),
		sqlb.As(thirdSales, string(MetricThirdSales)),
		sqlb.As(onHand, string(MetricOnHand)),
		sqlb.As(onHandRetail, string(MetricOnHandRetail)),
		sqlb.As(prevOnHand, string(MetricPrevOnHand)),
		sqlb.As(onOrder, string(MetricOnOrder)),
		sqlb.As(onOrderRetail, string(MetricOnOrderRetail)),
		sqlb.As(adCost, string(MetricAdCost)),
		sqlb.As(sqlb.Sub(fCur, fPrev), string(MetricUnitsChange)),
		sqlb.As(pct(sqlb.Sub(fCur, fPrev), fPrev, hundred), string(MetricUnitsPct)),
		sqlb.As(sqlb.Sub(curSales, prevSales), string(MetricSalesChange)),
		sqlb.As(pct(sqlb.Sub(curSales, prevSales), prevSales, hundred), string(MetricSalesPct)),
		sqlb.As(pct(curMargin, curSales, hundred), string(MetricMarginPct)),
		sqlb.As(sellThrough, string(MetricSellThrough)),
		sqlb.As(pct(fRet, sqlb.Add(fCur, fRet), hundred), string(MetricReturnsPct)),
		sqlb.As(roas, string(MetricROAS)),
	)
	outer = outer.With(cteReport, rep)

	// The outer statement: the report CTE, an optional label CTE, the final
	// filters, the sort, the page, and the total from a window.
	outer = outer.From(report)
	label := sqlb.Col[string](report, colKey)
	image := sqlb.Literal[*string]("NULL")
	if lc := p.Dimension.labelCTE(p); lc != nil {
		outer = outer.With(cteLabels, lc)
		labels := sqlb.Table{Name: cteLabels, Alias: "l"}
		outer = outer.Join(sqlb.Join{Type: sqlb.JoinLeft, Table: labels, Using: []string{colKey}})
		label = sqlb.Coalesce(sqlb.Col[string](labels, "label"), sqlb.Col[string](report, colKey))
		image = sqlb.Col[*string](labels, "image_url")
	}
	col := func(mt Metric) sqlb.SQLer { c, _ := metricColumn(mt); return c }
	outer = outer.Columns(
		sqlb.Bind(sqlb.Col[string](report, colKey), &row.Key),
		sqlb.Bind(label, &row.Label),
		sqlb.Bind(image, &row.ImageURL),
		bindInt(col(MetricCurrentUnits), &row.CurrentUnits),
		bindFloat(col(MetricCurrentSales), &row.CurrentSales),
		bindFloat(col(MetricCurrentCost), &row.CurrentCost),
		bindFloat(col(MetricCurrentMargin), &row.CurrentMargin),
		bindInt(col(MetricReturnedUnits), &row.ReturnedUnits),
		bindInt(col(MetricPreviousUnits), &row.PreviousUnits),
		bindFloat(col(MetricPreviousSales), &row.PreviousSales),
		bindInt(col(MetricThirdUnits), &row.ThirdUnits),
		bindFloat(col(MetricThirdSales), &row.ThirdSales),
		bindInt(col(MetricOnHand), &row.OnHand),
		bindFloat(col(MetricOnHandRetail), &row.OnHandRetail),
		bindInt(col(MetricPrevOnHand), &row.PrevOnHand),
		bindInt(col(MetricOnOrder), &row.OnOrder),
		bindFloat(col(MetricOnOrderRetail), &row.OnOrderRetail),
		bindFloat(col(MetricAdCost), &row.AdCost),
		bindFloat(col(MetricUnitsChange), &row.UnitsChange),
		bindRatio(col(MetricUnitsPct), &row.UnitsPct),
		bindFloat(col(MetricSalesChange), &row.SalesChange),
		bindRatio(col(MetricSalesPct), &row.SalesPct),
		bindRatio(col(MetricMarginPct), &row.MarginPct),
		bindRatio(col(MetricSellThrough), &row.SellThrough),
		bindRatio(col(MetricReturnsPct), &row.ReturnsPct),
		bindRatio(col(MetricROAS), &row.ROAS),
		sqlb.Bind(sqlb.Over(sqlb.Count(), sqlb.NewWindow()), &row.Total),
	)

	preds, err := finalFilters(p)
	if err != nil {
		return Result{}, err
	}
	outer = outer.Where(preds...)

	sortBy := p.Sort.By
	if sortBy == "" {
		sortBy = MetricKey
	}
	sortCol, ok := metricColumn(sortBy)
	if !ok {
		return Result{}, ErrUnknownMetric
	}
	dir := sqlb.SortAsc
	if p.Sort.Dir == Desc {
		dir = sqlb.SortDesc
	}
	outer = outer.Order(pg.OrderByNulls(sqlb.New[any](sortCol.SQL), dir, false))
	if sortBy != MetricKey {
		// A stable tiebreak, so a page boundary never splits equal metrics
		// differently between two requests.
		outer = outer.Order(sqlb.OrderBy(sqlb.Col[string](report, colKey), sqlb.SortAsc))
	}
	outer = outer.Limit(p.Page.Size).Offset((p.Page.Number - 1) * p.Page.Size)

	q, err := outer.Build()
	if err != nil {
		return Result{}, err
	}
	res.Query = q
	return res, nil
}

// measures reads a source's measure by name, or a typed zero when the
// source is disabled. This is the replacement for "0 AS X" in every union
// branch: the zero is chosen once, here, by whether the source exists.
type measures struct{ enabled map[string]bool }

var (
	zeroInt   = sqlb.Literal[int64]("0")
	zeroFloat = sqlb.Literal[float64]("0")
	nullRatio = sqlb.Literal[*float64]("NULL")
)

func (m measures) int(src string, mt Metric) sqlb.Expr[int64] {
	if !m.enabled[src] {
		return zeroInt
	}
	return sqlb.Coalesce(sqlb.Col[int64](sqlb.Table{Name: src, Alias: src}, string(mt)), zeroInt)
}

func (m measures) float(src string, mt Metric) sqlb.Expr[float64] {
	if !m.enabled[src] {
		return zeroFloat
	}
	return sqlb.Coalesce(sqlb.Col[float64](sqlb.Table{Name: src, Alias: src}, string(mt)), zeroFloat)
}

// pct renders num / NULLIF(den, 0) * scale, nullable. A zero denominator is
// NULL, not zero and not an error.
func pct(num, den, scale sqlb.Expr[float64]) sqlb.Expr[*float64] {
	safe := sqlb.AssertNotNull(sqlb.NullIf(den, zeroFloat)) // NULL propagates through the division; the result type is *float64 below
	return sqlb.Nullable(sqlb.Mul(sqlb.Div(num, safe), scale))
}

func ratio(num, den sqlb.Expr[float64]) sqlb.Expr[*float64] {
	safe := sqlb.AssertNotNull(sqlb.NullIf(den, zeroFloat))
	return sqlb.Nullable(sqlb.Div(num, safe))
}

// The bind helpers keep the report columns typed at the scan.
func bindInt(c sqlb.SQLer, dst *int64) sqlb.Binding { return sqlb.Bind(sqlb.New[int64](c.SQL), dst) }
func bindFloat(c sqlb.SQLer, dst *float64) sqlb.Binding {
	return sqlb.Bind(sqlb.New[float64](c.SQL), dst)
}
func bindRatio(c sqlb.SQLer, dst **float64) sqlb.Binding {
	return sqlb.Bind(sqlb.New[*float64](c.SQL), dst)
}
