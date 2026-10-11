package pg

import (
	"errors"
	"slices"
	"strings"

	"github.com/ubgo/sqlb"
	"github.com/ubgo/sqlb/ddl"
)

const (
	kwCreateSequence = "CREATE SEQUENCE"
	kwAlterSequence  = "ALTER SEQUENCE"
	kwDropSequence   = "DROP SEQUENCE"
	kwAs             = "AS"
	kwIncrementBy    = "INCREMENT BY"
	kwMinValue       = "MINVALUE"
	kwMaxValue       = "MAXVALUE"
	kwStartWith      = "START WITH"
	kwCache          = "CACHE"
	kwRestartWith    = "RESTART WITH"
	kwNoCycle        = "NO CYCLE"
	kwOwnedBy        = "OWNED BY"
	kwNone           = "NONE"
	fnNextVal        = "nextval"
)

// sequenceInteger is the Go types a sequence may produce: smallint, integer
// and bigint, which is the whole set Postgres accepts.
type sequenceInteger interface{ ~int16 | ~int32 | ~int64 }

// Sequence is a Postgres sequence whose values are T, so NextVal(s) is an
// Expr[T] and DefaultExpr accepts it only on a column of T. Prefer an identity
// column for a new table; a free sequence is for a number shared by several
// tables or read before an insert.
type Sequence[T sequenceInteger] struct{ Schema, Name string }

// sqlTypeBySize spells T's SQL integer type, the sequence's AS, from its
// width. The width is measured by shifting until the sign bit is reached,
// because a named type such as type InvoiceNo int64 has itself as its dynamic
// type, so a type switch on int64 would not see it.
func sqlTypeBySize[T sequenceInteger]() string {
	v := T(1)
	bits := 1
	for v > 0 {
		v <<= 1
		bits++
	}
	switch bits {
	case 16:
		return "smallint"
	case 32:
		return "integer"
	}
	return "bigint"
}

// NextVal is nextval(s), the sequence's next value, typed to it.
func NextVal[T sequenceInteger](s Sequence[T]) sqlb.Expr[T] {
	return expr[T](constructOps, func(a *sqlb.Args) string {
		return fnNextVal + "(" + a.Literal(qualified(a.Dialect(), s.Schema, s.Name)) + ")"
	})
}

// CreateSequenceStatement is CREATE SEQUENCE.
type CreateSequenceStatement[T sequenceInteger] struct {
	s           Sequence[T]
	ifNotExists bool
	// modifier is TEMPORARY or UNLOGGED, which go between CREATE and
	// SEQUENCE and are mutually exclusive in Postgres's grammar.
	modifier             string
	inc, min, max, start *T
	noMin, noMax         bool
	cycle, noCycle       bool
	// owned writes OWNED BY, which ties the sequence's life to a column's.
	owned func(a *sqlb.Args) string
	cache int64
}

// CreateSequence creates a sequence of T, written AS its SQL integer type.
func CreateSequence[T sequenceInteger](s Sequence[T]) *CreateSequenceStatement[T] {
	return &CreateSequenceStatement[T]{s: s}
}

// IfNotExists makes creating an existing sequence a no-op.
func (x *CreateSequenceStatement[T]) IfNotExists() *CreateSequenceStatement[T] {
	c := *x
	c.ifNotExists = true
	return &c
}

// IncrementBy sets the step, 1 by default; negative counts down.
func (x *CreateSequenceStatement[T]) IncrementBy(n T) *CreateSequenceStatement[T] {
	c := *x
	c.inc = &n
	return &c
}

// MinValue sets the lowest value.
func (x *CreateSequenceStatement[T]) MinValue(n T) *CreateSequenceStatement[T] {
	c := *x
	c.min = &n
	return &c
}

// MaxValue sets the highest value; the sequence fails when it passes it.
func (x *CreateSequenceStatement[T]) MaxValue(n T) *CreateSequenceStatement[T] {
	c := *x
	c.max = &n
	return &c
}

// Start sets the first value.
func (x *CreateSequenceStatement[T]) Start(n T) *CreateSequenceStatement[T] {
	c := *x
	c.start = &n
	return &c
}

// Cache preallocates n values per session: faster, and leaves gaps when a
// session ends, which a sequence may always do.
func (x *CreateSequenceStatement[T]) Cache(n int64) *CreateSequenceStatement[T] {
	c := *x
	c.cache = n
	return &c
}

// Build renders the statement.
func (x *CreateSequenceStatement[T]) Build() (ddl.Script, error) {
	return buildOne(x.s.Name, true, func(a *sqlb.Args) string {
		var b strings.Builder
		head := kwCreateSequence
		if x.modifier != "" {
			head = kwCreate + " " + x.modifier + " " + kwSequenceObject
		}
		b.WriteString(head + ifNotExists(x.ifNotExists) + " " + qualified(a.Dialect(), x.s.Schema, x.s.Name))
		b.WriteString(" " + kwAs + " " + sqlTypeBySize[T]())
		for _, o := range []struct {
			kw string
			v  *T
		}{{kwIncrementBy, x.inc}, {kwMinValue, x.min}, {kwMaxValue, x.max}, {kwStartWith, x.start}} {
			if o.v != nil {
				b.WriteString(" " + o.kw + " " + a.Literal(int64(*o.v)))
			}
		}
		if x.noMin {
			b.WriteString(" " + kwNoMinValue)
		}
		if x.noMax {
			b.WriteString(" " + kwNoMaxValue)
		}
		if x.cache > 0 {
			b.WriteString(" " + kwCache + " " + a.Literal(x.cache))
		}
		if x.cycle {
			b.WriteString(" " + kwCycle)
		}
		if x.noCycle {
			b.WriteString(" " + kwNoCycle)
		}
		if x.owned != nil {
			b.WriteString(" " + x.owned(a))
		}
		return b.String()
	})
}

// AlterSequenceQuery is ALTER SEQUENCE with no action yet, which has no Build.
type AlterSequenceQuery[T sequenceInteger] struct{ s Sequence[T] }

// AlterSequence starts an ALTER SEQUENCE.
func AlterSequence[T sequenceInteger](s Sequence[T]) *AlterSequenceQuery[T] {
	return &AlterSequenceQuery[T]{s: s}
}

// AlterSequenceStatement is a complete ALTER SEQUENCE: one or more options,
// which Postgres applies together, or a rename, which must stand alone.
type AlterSequenceStatement[T sequenceInteger] struct {
	s        Sequence[T]
	ifExists bool
	opts     []func(a *sqlb.Args) string
	rename   bool
}

// ErrRenameAlone is returned for a sequence rename given another option too:
// RENAME TO is a statement of its own in Postgres's grammar.
var ErrRenameAlone = errors.New("sqlb/pg: a sequence rename takes no other option")

// with derives a statement with one more option.
func (x *AlterSequenceStatement[T]) with(o func(a *sqlb.Args) string) *AlterSequenceStatement[T] {
	c := *x
	c.opts = append(slices.Clone(x.opts), o)
	return &c
}

func (x *AlterSequenceQuery[T]) start() *AlterSequenceStatement[T] {
	return &AlterSequenceStatement[T]{s: x.s}
}

func literalOption(kw string, n int64) func(a *sqlb.Args) string {
	return func(a *sqlb.Args) string { return kw + " " + a.Literal(n) }
}

func ownedBy[T sequenceInteger](col sqlb.Expr[T]) func(a *sqlb.Args) string {
	return func(a *sqlb.Args) string {
		if col.Name() == "" {
			a.Fail(ddl.ErrNotAColumn)
			return ""
		}
		d := a.Dialect()
		return kwOwnedBy + " " + col.Table().Qualified(d) + "." + d.QuoteIdent(col.Name())
	}
}

// Restart makes the next value n.
func (x *AlterSequenceQuery[T]) Restart(n T) *AlterSequenceStatement[T] { return x.start().Restart(n) }

// OwnedBy ties the sequence to a column, so dropping the column drops it. The
// column is of the sequence's own type.
func (x *AlterSequenceQuery[T]) OwnedBy(col sqlb.Expr[T]) *AlterSequenceStatement[T] {
	return x.start().OwnedBy(col)
}

// OwnedByNone unties the sequence from its column.
func (x *AlterSequenceQuery[T]) OwnedByNone() *AlterSequenceStatement[T] {
	return x.start().OwnedByNone()
}

// IncrementBy changes the step.
func (x *AlterSequenceQuery[T]) IncrementBy(n T) *AlterSequenceStatement[T] {
	return x.start().IncrementBy(n)
}

// MinValue changes the lowest value.
func (x *AlterSequenceQuery[T]) MinValue(n T) *AlterSequenceStatement[T] {
	return x.start().MinValue(n)
}

// MaxValue changes the highest value.
func (x *AlterSequenceQuery[T]) MaxValue(n T) *AlterSequenceStatement[T] {
	return x.start().MaxValue(n)
}

// Cache changes how many values a session preallocates, at least 1.
func (x *AlterSequenceQuery[T]) Cache(n int64) *AlterSequenceStatement[T] { return x.start().Cache(n) }

// Cycle makes the sequence wrap past its bound instead of failing.
func (x *AlterSequenceQuery[T]) Cycle() *AlterSequenceStatement[T] { return x.start().Cycle() }

// NoCycle makes the sequence fail past its bound, the default.
func (x *AlterSequenceQuery[T]) NoCycle() *AlterSequenceStatement[T] { return x.start().NoCycle() }

// RenameTo renames the sequence to to's name, taking the new declaration the
// code will use rather than a string. A statement of its own: chaining an
// option onto it fails Build with ErrRenameAlone.
func (x *AlterSequenceQuery[T]) RenameTo(to Sequence[T]) *AlterSequenceStatement[T] {
	c := x.start()
	c.rename = true
	c.opts = []func(a *sqlb.Args) string{func(a *sqlb.Args) string {
		if to.Name == "" {
			a.Fail(ErrEmptyName)
			return ""
		}
		return kwRenameTo + " " + a.Dialect().QuoteIdent(to.Name)
	}}
	return c
}

// Restart adds RESTART WITH n.
func (x *AlterSequenceStatement[T]) Restart(n T) *AlterSequenceStatement[T] {
	return x.with(literalOption(kwRestartWith, int64(n)))
}

// OwnedBy adds OWNED BY col.
func (x *AlterSequenceStatement[T]) OwnedBy(col sqlb.Expr[T]) *AlterSequenceStatement[T] {
	return x.with(ownedBy(col))
}

// OwnedByNone adds OWNED BY NONE.
func (x *AlterSequenceStatement[T]) OwnedByNone() *AlterSequenceStatement[T] {
	return x.with(func(*sqlb.Args) string { return kwOwnedBy + " " + kwNone })
}

// IncrementBy adds INCREMENT BY n.
func (x *AlterSequenceStatement[T]) IncrementBy(n T) *AlterSequenceStatement[T] {
	return x.with(literalOption(kwIncrementBy, int64(n)))
}

// MinValue adds MINVALUE n.
func (x *AlterSequenceStatement[T]) MinValue(n T) *AlterSequenceStatement[T] {
	return x.with(literalOption(kwMinValue, int64(n)))
}

// MaxValue adds MAXVALUE n.
func (x *AlterSequenceStatement[T]) MaxValue(n T) *AlterSequenceStatement[T] {
	return x.with(literalOption(kwMaxValue, int64(n)))
}

// ErrBadCache is returned for a sequence cache below 1, which Postgres
// refuses.
var ErrBadCache = errors.New("sqlb/pg: a sequence cache must be at least 1")

// Cache adds CACHE n.
func (x *AlterSequenceStatement[T]) Cache(n int64) *AlterSequenceStatement[T] {
	return x.with(func(a *sqlb.Args) string {
		if n < 1 {
			a.Fail(ErrBadCache)
			return ""
		}
		return kwCache + " " + a.Literal(n)
	})
}

// Cycle adds CYCLE.
func (x *AlterSequenceStatement[T]) Cycle() *AlterSequenceStatement[T] {
	return x.with(func(*sqlb.Args) string { return kwCycle })
}

// NoCycle adds NO CYCLE.
func (x *AlterSequenceStatement[T]) NoCycle() *AlterSequenceStatement[T] {
	return x.with(func(*sqlb.Args) string { return kwNoCycle })
}

// IfExists makes altering a missing sequence a no-op.
func (x *AlterSequenceStatement[T]) IfExists() *AlterSequenceStatement[T] {
	c := *x
	c.ifExists = true
	return &c
}

// Build renders the statement.
func (x *AlterSequenceStatement[T]) Build() (ddl.Script, error) {
	return buildOne(x.s.Name, true, func(a *sqlb.Args) string {
		if len(x.opts) == 0 {
			a.Fail(ddl.ErrZeroValue)
			return ""
		}
		if x.rename && len(x.opts) > 1 {
			a.Fail(ErrRenameAlone)
			return ""
		}
		parts := make([]string, len(x.opts))
		for i, o := range x.opts {
			parts[i] = o(a)
		}
		return kwAlterSequence + ifExists(x.ifExists) + " " + qualified(a.Dialect(), x.s.Schema, x.s.Name) + " " + strings.Join(parts, " ")
	})
}

// DropSequence drops a sequence, which fails while a default uses it unless
// Cascade is given.
func DropSequence[T sequenceInteger](s Sequence[T]) *DropObjectStatement {
	return &DropObjectStatement{kw: kwDropSequence, schema: s.Schema, name: s.Name}
}

// DropObjectStatement is DROP SEQUENCE, DROP VIEW, DROP MATERIALIZED VIEW or
// DROP FUNCTION:
// one object, with IF EXISTS and CASCADE.
type DropObjectStatement struct {
	kw, schema, name string
	// suffix is a function's argument list, which is part of the name
	// Postgres drops by, and empty for every other object.
	suffix string
	// err is a failure the constructor found in the value it took, carried
	// here because a constructor returns a statement rather than an error;
	// Build reports it before rendering anything.
	err                         error
	ifExists, cascade, restrict bool
}

// IfExists makes dropping a missing object a no-op.
func (x *DropObjectStatement) IfExists() *DropObjectStatement { c := *x; c.ifExists = true; return &c }

// Cascade also drops what depends on the object.
func (x *DropObjectStatement) Cascade() *DropObjectStatement { c := *x; c.cascade = true; return &c }

// Build renders the statement.
func (x *DropObjectStatement) Build() (ddl.Script, error) {
	if x.err != nil {
		return nil, x.err
	}
	return buildOne(x.name, true, func(a *sqlb.Args) string {
		return x.kw + ifExists(x.ifExists) + " " + qualified(a.Dialect(), x.schema, x.name) + x.suffix + cascade(x.cascade) + restrict(x.restrict)
	})
}
