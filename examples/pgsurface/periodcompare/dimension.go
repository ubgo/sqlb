package periodcompare

import (
	"time"

	"github.com/ubgo/sqlb"
)

// Dimension is the grouping of the report. A closed set.
type Dimension string

// The dimensions the report can group by. A request names one of these and
// the value is matched against the set, never interpolated.
const (
	ByVendor      Dimension = "vendor"
	ByStore       Dimension = "store"
	ByProduct     Dimension = "product"
	ByDepartment  Dimension = "department"
	ByClass       Dimension = "class"
	ByFiscalMonth Dimension = "fiscal_month"
)

// DimensionValues lists every dimension, for validation at the API edge.
var DimensionValues = []Dimension{ByVendor, ByStore, ByProduct, ByDepartment, ByClass, ByFiscalMonth}

// factColumns is what a fact table offers the dimension: the product and
// vendor ids every fact has, plus the optional store id and date. A nil
// store or date means the fact cannot serve that dimension.
type factColumns struct {
	product sqlb.Expr[string]
	vendor  sqlb.Expr[string]
	store   *sqlb.Expr[int32]
	date    *sqlb.Expr[time.Time]
}

// keyPlan is how one source computes the key: the expression and the joins
// it needs. Joins are appended to the source CTE; every source CTE is its own
// statement, so the dimension aliases repeat safely.
type keyPlan struct {
	key   sqlb.Expr[string]
	joins []sqlb.Join
	// hasProducts and hasCategories say which dimension tables the plan
	// already joined, so a filter that needs them reuses the join instead
	// of adding a second one under the same alias.
	hasProducts   bool
	hasCategories bool
}

// Serves reports whether a fact with these columns can group by d.
func (d Dimension) serves(f factColumns) bool {
	switch d {
	case ByStore:
		return f.store != nil
	case ByFiscalMonth:
		return f.date != nil
	default:
		return true
	}
}

// plan builds the key for a fact. The label is the key for every dimension
// except fiscal month, whose key is the period index so that the same period
// of three different years lands on one row; the label is attached at the
// end from the calendar of the current fiscal year.
func (d Dimension) plan(f factColumns) keyPlan {
	productJoin := sqlb.Join{Type: sqlb.JoinInner, Table: Products, On: []sqlb.Predicate{sqlb.EqExpr(ProductID, f.product)}}
	categoryJoin := sqlb.Join{Type: sqlb.JoinInner, Table: Categories, On: []sqlb.Predicate{sqlb.EqExpr(CategoryID, ProductCatID)}}
	switch d {
	case ByVendor:
		return keyPlan{key: VendorName, joins: []sqlb.Join{
			{Type: sqlb.JoinInner, Table: Vendors, On: []sqlb.Predicate{sqlb.EqExpr(VendorID, f.vendor)}},
		}}
	case ByStore:
		return keyPlan{key: StoreName, joins: []sqlb.Join{
			{Type: sqlb.JoinInner, Table: Stores, On: []sqlb.Predicate{sqlb.EqExpr(StoreID, *f.store)}},
		}}
	case ByProduct:
		return keyPlan{key: sqlb.Concat(ProductCode, sqlb.Param(" | "), ProductName), joins: []sqlb.Join{productJoin}, hasProducts: true}
	case ByDepartment:
		return keyPlan{key: Department, joins: []sqlb.Join{productJoin, categoryJoin}, hasProducts: true, hasCategories: true}
	case ByClass:
		return keyPlan{key: sqlb.Concat(Department, sqlb.Param(" / "), ClassName), joins: []sqlb.Join{productJoin, categoryJoin}, hasProducts: true, hasCategories: true}
	case ByFiscalMonth:
		return keyPlan{key: sqlb.Cast[string](CalPeriodIndex, "text"), joins: []sqlb.Join{
			{Type: sqlb.JoinInner, Table: Calendar, On: []sqlb.Predicate{sqlb.EqExpr(CalDay, *f.date)}},
		}}
	}
	return keyPlan{}
}

// labelCTE returns a CTE that maps key to a display label, or nil when the
// key is its own label. Fiscal month maps the period index to the period
// label of the current fiscal year. Product attaches the image.
func (d Dimension) labelCTE(p Params) *sqlb.Select[sqlb.PostgresDialect] {
	switch d {
	case ByFiscalMonth:
		return sqlb.NewSelect(dialect).Distinct().
			Exprs(sqlb.As(sqlb.Cast[string](CalPeriodIndex, "text"), colKey), sqlb.As(CalPeriodLabel, "label"), sqlb.As(sqlb.Literal[*string]("NULL"), "image_url")).
			From(Calendar).
			Where(sqlb.Eq(CalFiscalYear, p.FiscalYear))
	case ByProduct:
		return sqlb.NewSelect(dialect).
			Exprs(sqlb.As(sqlb.Concat(ProductCode, sqlb.Param(" | "), ProductName), colKey), sqlb.As(sqlb.Concat(ProductCode, sqlb.Param(" | "), ProductName), "label"), sqlb.As(sqlb.Min(ProductImage), "image_url")).
			From(Products).
			GroupBy(ProductCode, ProductName)
	}
	return nil
}
