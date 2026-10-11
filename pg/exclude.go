package pg

import (
	"fmt"
	"slices"
	"strings"

	"github.com/ubgo/sqlb"
	"github.com/ubgo/sqlb/ddl"
)

const (
	kwExclude     = "EXCLUDE"
	kwLike        = "LIKE"
	kwIncluding   = "INCLUDING"
	kwCreateTable = "CREATE TABLE"
)

// ExcludeOp is the operator an exclusion constraint compares with: two rows
// conflict when every element's operator returns true. It is open, as index
// methods are, because an extension brings its own operators; the ones a
// library reaches for are named.
type ExcludeOp string

// The exclusion operators a library uses. OpOverlaps on a range is the
// no-double-booking constraint; OpEquals on a second column scopes it, which
// needs the btree_gist extension for a scalar under GiST.
const (
	OpOverlaps ExcludeOp = "&&"
	OpEquals   ExcludeOp = "="
	OpAdjacent ExcludeOp = "-|-"
)

// ExcludeElement is one column of an exclusion constraint and its operator.
type ExcludeElement struct {
	col ddl.ColumnRef
	op  ExcludeOp
}

// ExcludeOn pairs a column with the operator two rows' values are compared by.
func ExcludeOn[T any](col sqlb.Expr[T], op ExcludeOp) ExcludeElement {
	return ExcludeElement{col: col, op: op}
}

// ExcludeClause is an EXCLUDE table constraint.
type ExcludeClause struct {
	con    TableConstraint
	method IndexMethod
	elems  []ExcludeElement
	// include and with are the index parameters the constraint's own index
	// takes; both render before WHERE, as Postgres's grammar fixes.
	include []ddl.ColumnRef
	with    []StorageParameter
	where   sqlb.Predicate
}

// Exclude makes a named exclusion constraint: no two rows may have every
// element's operator true at once. It is backed by an index of method m,
// usually pg.GiST. It is the constraint a UNIQUE cannot express, a room booked
// for two overlapping time ranges.
func Exclude(c TableConstraint, m IndexMethod, first ExcludeElement, more ...ExcludeElement) ExcludeClause {
	return ExcludeClause{con: c, method: m, elems: append([]ExcludeElement{first}, more...)}
}

// ClauseTable is the constraint's declared table.
func (x ExcludeClause) ClauseTable() sqlb.Table { return x.con.Table }

// Where limits the constraint to rows matching p, cancelled bookings excluded.
func (x ExcludeClause) Where(p sqlb.Predicate) ExcludeClause {
	x.elems = slices.Clone(x.elems)
	x.where = p
	return x
}

// Position places the constraint among the table's elements.
func (ExcludeClause) Position() ddl.Position { return ddl.PositionElement }

// NamedColumns is the constraint's columns.
func (x ExcludeClause) NamedColumns() []ddl.ColumnRef {
	out := make([]ddl.ColumnRef, len(x.elems))
	for i, e := range x.elems {
		out[i] = e.col
	}
	return out
}

// WriteSQL renders the constraint.
func (x ExcludeClause) WriteSQL(b *strings.Builder, a *sqlb.Args) {
	if len(x.elems) == 0 {
		a.Fail(ddl.ErrZeroValue)
		return
	}
	d := a.Dialect()
	parts := make([]string, len(x.elems))
	for i, e := range x.elems {
		if e.op == "" {
			a.Fail(fmt.Errorf("%w: an exclusion element needs an operator", ErrUnknownKeyword))
			return
		}
		parts[i] = d.QuoteIdent(e.col.Name()) + " " + kwWith + " " + closedWord(a, e.op, e.op.valid(), "exclusion operator")
	}
	b.WriteString(namedHead(a, x.con) + kwExclude)
	if x.method != "" {
		b.WriteString(" " + kwUsing + " " + d.QuoteIdent(string(x.method)))
	}
	b.WriteString(" (" + strings.Join(parts, ", ") + ")")
	b.WriteString(writeIndexParameters(a, x.include, x.with))
	if x.where != nil {
		w := a.Bare(func() string {
			var w strings.Builder
			x.where.WriteSQL(&w, a)
			return w.String()
		})
		b.WriteString(" " + kwWhere + " (" + w + ")")
	}
}

// TableClauseOf marks the clause as Postgres's.
func (ExcludeClause) TableClauseOf(Dialect) {}

func (x ExcludeClause) constraintName() string { return x.con.Name }

// LikeOption is what CREATE TABLE ... (LIKE src) copies besides the columns.
// There is deliberately no "including everything": LIKE without INCLUDING
// GENERATED silently turns a generated column into a plain one, so each thing
// copied is named where a reader sees it.
type LikeOption int

// The things LIKE can copy.
const (
	IncludingDefaults LikeOption = iota + 1
	IncludingConstraints
	IncludingGenerated
	IncludingIdentity
	IncludingIndexes
	IncludingComments
	IncludingStatistics
	IncludingStorage
	IncludingCompression
)

func (o LikeOption) keyword() (string, bool) {
	switch o {
	case IncludingDefaults:
		return "DEFAULTS", true
	case IncludingConstraints:
		return "CONSTRAINTS", true
	case IncludingGenerated:
		return "GENERATED", true
	case IncludingIdentity:
		return "IDENTITY", true
	case IncludingIndexes:
		return "INDEXES", true
	case IncludingComments:
		return "COMMENTS", true
	case IncludingStatistics:
		return "STATISTICS", true
	case IncludingStorage:
		return "STORAGE", true
	case IncludingCompression:
		return "COMPRESSION", true
	}
	return "", false
}

// CreateTableLikeStatement is CREATE TABLE t (LIKE src ...).
type CreateTableLikeStatement struct {
	t, src      sqlb.Table
	opts        []LikeOption
	ifNotExists bool
}

// CreateTableLike creates t with src's columns, and whatever of src the
// options name. It is its own statement because it has no column list of its
// own, which CreateTable's typestate requires; an archive or staging table is
// what it is for.
func CreateTableLike(t, src sqlb.Table, opts ...LikeOption) *CreateTableLikeStatement {
	return &CreateTableLikeStatement{t: t, src: src, opts: opts}
}

// IfNotExists makes creating an existing table a no-op.
func (x *CreateTableLikeStatement) IfNotExists() *CreateTableLikeStatement {
	c := *x
	c.opts = slices.Clone(x.opts)
	c.ifNotExists = true
	return &c
}

// Build renders the statement.
func (x *CreateTableLikeStatement) Build() (ddl.Script, error) {
	if x.src.Name == "" {
		return nil, ErrEmptyName
	}
	return buildOne(x.t.Name, true, func(a *sqlb.Args) string {
		d := a.Dialect()
		s := kwCreateTable + ifNotExists(x.ifNotExists) + " " + x.t.Qualified(d) + " (" + kwLike + " " + x.src.Qualified(d)
		for _, o := range x.opts {
			kw, ok := o.keyword()
			s += " " + kwIncluding + " " + keywordOf(a, kw, ok, "CreateTableLike")
		}
		return s + ")"
	})
}
