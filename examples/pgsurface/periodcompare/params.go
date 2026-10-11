package periodcompare

import "time"

// DateRange is inclusive on both ends.
type DateRange struct {
	From, To time.Time
}

// NumericOp is the closed set of comparisons a numeric filter may use.
type NumericOp string

// The comparisons a numeric filter may use.
const (
	OpGreater   NumericOp = "gt"
	OpGreaterEq NumericOp = "gte"
	OpLess      NumericOp = "lt"
	OpLessEq    NumericOp = "lte"
	OpEqual     NumericOp = "eq"
)

// NumericFilter compares a computed metric with a value, after aggregation.
type NumericFilter struct {
	Metric Metric
	Op     NumericOp
	Value  float64
}

// Filters narrow the rows. Each filter states which sources it applies to
// in filters.go; a filter never applies silently to fewer sources than the
// caller expects, because Build reports what it skipped.
type Filters struct {
	VendorIDs   []string
	StoreIDs    []int32
	Departments []string
	Classes     []string
	KeywordIDs  []int64
	// AllowedVendors is the tenant restriction: the vendors this caller may
	// see at all. Empty means unrestricted. It is applied to every source.
	AllowedVendors []string
	// ExcludeReturns keeps only positive quantities. Sales only.
	ExcludeReturns bool
	// ExcludeMarkdowns keeps lines sold at list price; IncludeMarkdowns keeps
	// lines sold below it. Sales only. On stock sources they restrict to the
	// products that sold under the same rule in the current period.
	ExcludeMarkdowns bool
	IncludeMarkdowns bool
	// KeyIn keeps only the named keys, after grouping.
	KeyIn []string
	// Numeric filters apply to computed metrics, after grouping.
	Numeric []NumericFilter
}

// Include switches optional sources on.
type Include struct {
	ThirdYear bool
	OnOrder   bool
	AdSpend   bool
	Snapshot  bool
}

// SortDir is ASC or DESC.
type SortDir string

// The two sort directions.
const (
	Asc  SortDir = "asc"
	Desc SortDir = "desc"
)

// Sort names a metric or the key, and a direction.
type Sort struct {
	By  Metric
	Dir SortDir
}

// Page is 1-based.
type Page struct {
	Size   int
	Number int
}

// Params is one report request.
type Params struct {
	Dimension Dimension
	Current   DateRange
	Previous  DateRange
	Third     DateRange
	// FiscalYear is the year whose period labels decorate a fiscal-month
	// report. Required for ByFiscalMonth, ignored otherwise.
	FiscalYear int32
	Filters    Filters
	Include    Include
	Sort       Sort
	Page       Page
}

// Defaults.
const (
	DefaultPageSize = 100
	MaxPageSize     = 5000
)

func (p Page) normalized() Page {
	if p.Number < 1 {
		p.Number = 1
	}
	if p.Size < 1 {
		p.Size = DefaultPageSize
	}
	if p.Size > MaxPageSize {
		p.Size = MaxPageSize
	}
	return p
}

// previousSaturday is the snapshot date convention: stock at the end of the
// last full week before or on the date.
func previousSaturday(t time.Time) time.Time {
	days := int(t.Weekday()) + 1
	if days > 7 {
		days = 7
	}
	return t.AddDate(0, 0, -days)
}
