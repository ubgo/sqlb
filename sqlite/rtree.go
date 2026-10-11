package sqlite

import (
	"errors"
	"strings"

	"github.com/ubgo/sqlb"
	"github.com/ubgo/sqlb/ddl"
)

// R*Tree, SQLite's spatial index: a virtual table of an integer id and one to
// five dimensions, each a minimum and a maximum coordinate, answering "which
// boxes overlap this one" without scanning. It is compiled into the modernc
// driver contrib/driver-sqlite uses; with another build, check the module is
// there, as CREATE VIRTUAL TABLE fails with "no such module: rtree" when it is
// not.

const (
	kwUsingRTree    = "USING rtree"
	kwUsingRTreeI32 = "USING rtree_i32"
	// rtreeMaxDims is the most dimensions an R*Tree has.
	rtreeMaxDims = 5
)

// ErrRTreeDims is an R*Tree with no dimension or more than five, the range
// SQLite accepts.
var ErrRTreeDims = errors.New("sqlb/sqlite: an R*Tree has one to five dimensions")

// Dim is one R*Tree dimension: the columns holding its minimum and maximum.
type Dim struct{ min, max ddl.ColumnRef }

// Dimension pairs a dimension's minimum and maximum columns. R*Tree stores
// coordinates as 32-bit floats; RTree's Integer stores them as 32-bit
// integers, and either way the Go type of a coordinate column is a float or
// an integer.
func Dimension[T float32 | float64 | int32 | int64](lo, hi sqlb.Expr[T]) Dim {
	return Dim{min: lo, max: hi}
}

// CreateRTreeQuery is CREATE VIRTUAL TABLE ... USING rtree with no columns
// yet, which has no Build.
type CreateRTreeQuery struct {
	t           sqlb.Table
	ifNotExists bool
	integer     bool
}

// CreateRTree starts an R*Tree table t, whose columns are declared on t as for
// any table.
func CreateRTree(t sqlb.Table) *CreateRTreeQuery { return &CreateRTreeQuery{t: t} }

// IfNotExists makes creating an existing R*Tree a no-op.
func (x *CreateRTreeQuery) IfNotExists() *CreateRTreeQuery { c := *x; c.ifNotExists = true; return &c }

// Integer stores the coordinates as 32-bit integers, rtree_i32, for grid
// data where float rounding would misplace a box.
func (x *CreateRTreeQuery) Integer() *CreateRTreeQuery { c := *x; c.integer = true; return &c }

// Columns gives the id and the dimensions, in order.
func (x *CreateRTreeQuery) Columns(id sqlb.Expr[int64], first Dim, more ...Dim) *CreateRTreeStatement {
	return &CreateRTreeStatement{q: *x, id: id, dims: append([]Dim{first}, more...)}
}

// CreateRTreeStatement is a complete CREATE VIRTUAL TABLE ... USING rtree.
type CreateRTreeStatement struct {
	q    CreateRTreeQuery
	id   ddl.ColumnRef
	dims []Dim
}

// Build renders the statement.
func (x *CreateRTreeStatement) Build() (ddl.Script, error) {
	if len(x.dims) == 0 || len(x.dims) > rtreeMaxDims {
		return nil, ErrRTreeDims
	}
	cols := []ddl.ColumnRef{x.id}
	for _, d := range x.dims {
		cols = append(cols, d.min, d.max)
	}
	for _, r := range cols {
		if r == nil || r.Name() == "" {
			return nil, ddl.ErrNotAColumn
		}
		if !onTable(r, x.q.t) {
			return nil, ddl.ErrForeignColumn
		}
	}
	return buildOne(x.q.t.Name, func(a *sqlb.Args) string {
		d := a.Dialect()
		names := make([]string, len(cols))
		for i, r := range cols {
			names[i] = d.QuoteIdent(r.Name())
		}
		using := kwUsingRTree
		if x.q.integer {
			using = kwUsingRTreeI32
		}
		return kwCreateVirtualTable + ifNotExists(x.q.ifNotExists) + " " + x.q.t.Qualified(d) + " " + using + "(" + strings.Join(names, ", ") + ")"
	})
}
