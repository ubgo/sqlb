package periodcompare

import (
	"github.com/ubgo/sqlb"
	"github.com/ubgo/sqlb/pg"
)

// rowFilters returns the predicates and joins a source needs for the
// caller's filters, given that source's columns. The rules for which filter
// touches which source are written here, once, and Build records any filter
// a source could not honour.
func rowFilters(p Params, f factColumns, plan keyPlan, sales bool) (preds []sqlb.Predicate, joins []sqlb.Join, skipped []string) {
	fl := p.Filters
	if len(fl.AllowedVendors) > 0 {
		preds = append(preds, pg.InArray(f.vendor, fl.AllowedVendors))
	}
	if len(fl.VendorIDs) > 0 {
		preds = append(preds, pg.InArray(f.vendor, fl.VendorIDs))
	}
	if len(fl.StoreIDs) > 0 {
		if f.store != nil {
			preds = append(preds, pg.InArray(*f.store, fl.StoreIDs))
		} else {
			skipped = append(skipped, "StoreIDs")
		}
	}
	if len(fl.Departments) > 0 || len(fl.Classes) > 0 {
		if !plan.hasProducts {
			joins = append(joins, sqlb.Join{Type: sqlb.JoinInner, Table: Products, On: []sqlb.Predicate{sqlb.EqExpr(ProductID, f.product)}})
		}
		if !plan.hasCategories {
			joins = append(joins, sqlb.Join{Type: sqlb.JoinInner, Table: Categories, On: []sqlb.Predicate{sqlb.EqExpr(CategoryID, ProductCatID)}})
		}
		if len(fl.Departments) > 0 {
			preds = append(preds, pg.InArray(Department, fl.Departments))
		}
		if len(fl.Classes) > 0 {
			preds = append(preds, pg.InArray(ClassName, fl.Classes))
		}
	}
	if len(fl.KeywordIDs) > 0 {
		tagged := sqlb.NewSelect(dialect).Exprs(KwProductID).From(Keywords).Where(pg.InArray(KwKeywordID, fl.KeywordIDs))
		preds = append(preds, sqlb.InSubquery(f.product, tagged))
	}
	if sales {
		if fl.ExcludeReturns {
			preds = append(preds, sqlb.Greater(SaleQty, 0))
		}
		if fl.ExcludeMarkdowns {
			preds = append(preds, sqlb.GreaterEqExpr(SaleUnitPrice, SaleListPrice))
		}
		if fl.IncludeMarkdowns {
			preds = append(preds, sqlb.LessExpr(SaleUnitPrice, SaleListPrice))
		}
		preds = append(preds, sqlb.Eq(SaleIsGift, false))
	} else if fl.ExcludeMarkdowns || fl.IncludeMarkdowns {
		// Markdown is a property of a sale, not of stock. A stock source
		// honours the filter by restricting to the products that sold under
		// the same rule in the current period.
		preds = append(preds, sqlb.InSubquery(f.product, soldProducts(p)))
	}
	return preds, joins, skipped
}

// soldProducts is the set of products with a current-period sale under the
// markdown rule in force.
func soldProducts(p Params) *sqlb.Select[sqlb.PostgresDialect] {
	s := sqlb.NewSelect(dialect).Distinct().Exprs(SaleProductID).From(SalesLines).
		Where(sqlb.Between(SaleSoldOn, p.Current.From, p.Current.To))
	if p.Filters.ExcludeMarkdowns {
		s = s.Where(sqlb.GreaterEqExpr(SaleUnitPrice, SaleListPrice))
	}
	if p.Filters.IncludeMarkdowns {
		s = s.Where(sqlb.LessExpr(SaleUnitPrice, SaleListPrice))
	}
	return s
}

// finalFilters are the predicates on the report CTE: key membership and
// numeric comparisons on computed metrics. They are plain WHERE clauses,
// because the metrics are columns of a CTE, not aliases of the same SELECT.
func finalFilters(p Params) ([]sqlb.Predicate, error) {
	var preds []sqlb.Predicate
	if len(p.Filters.KeyIn) > 0 {
		preds = append(preds, pg.InArray(sqlb.Col[string](report, colKey), p.Filters.KeyIn))
	}
	for _, nf := range p.Filters.Numeric {
		col, ok := metricColumn(nf.Metric)
		if !ok || nf.Metric == MetricKey {
			return nil, ErrUnknownMetric
		}
		op, ok := numericOps[nf.Op]
		if !ok {
			return nil, ErrUnknownOperator
		}
		preds = append(preds, sqlb.Compare(col, op, sqlb.Param(nf.Value)))
	}
	return preds, nil
}

// numericOps maps the request vocabulary to SQL operators. The map is the
// only place a request string becomes SQL text, and it is a lookup, never a
// format.
var numericOps = map[NumericOp]string{
	OpGreater:   sqlb.OpGreater,
	OpGreaterEq: sqlb.OpGreaterEq,
	OpLess:      sqlb.OpLess,
	OpLessEq:    sqlb.OpLessEq,
	OpEqual:     sqlb.OpEq,
}
