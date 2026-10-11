package ddl

import (
	"errors"
	"fmt"
	"strings"

	"github.com/ubgo/sqlb/expr"
	"github.com/ubgo/sqlb/render"
)

// ColumnRef is a column a clause names, whatever its Go type. Every expr.Expr
// made by Col satisfies it, which is what lets PRIMARY KEY (a, b) take columns
// of two Go types in one call; an expression that is not a column has no name
// and is refused at Build with ErrNotAColumn.
type ColumnRef interface {
	Name() string
	Table() expr.Table
}

// ColumnNamer is implemented by a table clause that names columns of the
// statement's own table, a primary key or a unique constraint. The statement
// checks each one at Build: a clause naming a column the table does not define
// fails with ErrUnknownColumn instead of reaching the server as an error about
// SQL the caller never wrote, and one naming another table's column fails with
// ErrForeignColumn.
type ColumnNamer interface {
	NamedColumns() []ColumnRef
}

// ErrUnknownColumn is returned for a clause naming a column the CREATE TABLE
// does not define.
var ErrUnknownColumn = errors.New("sqlb/ddl: clause names a column the table does not define")

// ErrNoColumnTarget is returned for a clause at PositionColumn that does not
// say which column it belongs to; it would otherwise vanish.
var ErrNoColumnTarget = errors.New("sqlb/ddl: a column clause must name its column")

// ErrDefaultOnGenerated is returned for a column that has a default and a
// clause that supplies its value, an identity or a generated expression. Every
// engine here rejects the pair, so it is refused before the server sees it.
var ErrDefaultOnGenerated = errors.New("sqlb/ddl: a column with a generated value cannot also have a default")

// ColumnPart is where inside one column's definition a column clause renders.
// The grammar differs by position rather than by engine, so the positions are
// universal and the clauses that fill them are the engine's.
type ColumnPart int

// The parts of a column definition a column clause can fill.
const (
	// PartAfterType follows the type immediately, where COLLATE must go.
	PartAfterType ColumnPart = iota + 1
	// PartConstraint follows the default: a column-level key, a reference.
	PartConstraint
	// PartValueSource is a constraint that supplies the column's value, an
	// identity or AUTO_INCREMENT: the column cannot also have a default.
	PartValueSource
	// PartComputed is a generated expression: the column cannot have a
	// default, and NOT NULL is not written, because the application never
	// writes the column and the expression decides whether it is NULL.
	PartComputed
	// PartAfterNotNull is written immediately after NOT NULL, which is the
	// only position SQLite accepts a conflict clause on it in: NOT NULL ON
	// CONFLICT REPLACE. It is a part of its own rather than PartAfterType
	// because the clause modifies the NOT NULL rather than the type, and a
	// definition that does not write NOT NULL has nothing for it to attach
	// to, which fails Build with ErrNoNotNull rather than rendering a clause
	// that reads as a constraint of its own.
	PartAfterNotNull
)

// ErrNoDefault reports a column clause that needs the column to have a default,
// on a column that declares none. The case it exists for is SQLite's
// NOT NULL ON CONFLICT REPLACE, which writes the column's default in place of
// a NULL: with no default there is nothing to write, and SQLite falls back to
// ABORT, so the row is refused while the schema says it would be kept.
var ErrNoDefault = errors.New("sqlb/ddl: clause needs the column to have a default")

// DefaultRequirer is implemented by a column clause that only means what it
// says when the column has a default.
//
// # Why this is refused at Build rather than documented
//
// SQLite's NOT NULL ON CONFLICT REPLACE on a column with no default is
// accepted by CREATE TABLE and behaves as ABORT, measured on 3.50.3. The
// failure therefore does not happen when the schema is installed, nor in any
// test that never writes a NULL to that column; it happens the first time
// production does, as "NOT NULL constraint failed", from a table whose
// declaration promised the opposite. A comment warning about that is read by
// the person who already knows. Build is the one place the mistake can be
// seen before it ships.
//
// # Why the check lives in ddl and the decision in the engine
//
// Only the column definition knows whether a default was declared, and the
// clause cannot see it — the same reason ErrNoNotNull lives here. So the
// engine clause says whether it needs a default, which is a fact about its
// own semantics, and ddl, which holds the definition, refuses the mismatch.
// ddl learns nothing about SQLite, and a clause from another engine with the
// same need reuses the check by implementing this interface.
type DefaultRequirer interface {
	RequiresDefault() bool
}

// ErrNoNotNull reports a clause placed at PartAfterNotNull on a column whose
// definition writes no NOT NULL, because it is nullable or generated. The
// clause would otherwise render where the engine reads it as something else.
var ErrNoNotNull = errors.New("sqlb/ddl: clause needs a NOT NULL to attach to")

// ColumnPartValues is every part a column definition renders, so a test can
// check each is written.
var ColumnPartValues = []ColumnPart{PartAfterType, PartConstraint, PartValueSource, PartComputed, PartAfterNotNull}

// ColumnClause is an engine clause that belongs inside one column's
// definition, an identity or a collation. It goes on the statement with the
// other clauses, at PositionColumn, and the statement routes it into the
// column it names; a clause naming a column the statement does not define
// fails at Build.
type ColumnClause[D render.Dialect] interface {
	TableClause[D]
	Target() ColumnRef
	Part() ColumnPart
}

// NewColumnClause makes a ColumnClause for an engine package. The render
// function writes no leading space and renders in bare-column mode, since a
// generated expression names its table's columns without an alias.
func NewColumnClause[D render.Dialect](target ColumnRef, part ColumnPart, render func(a *render.Args) string) ColumnClause[D] {
	return columnClause[D]{target: target, part: part, render: render}
}

// NewRankedColumnClause is NewColumnClause for a clause whose engine fixes its
// order among the clauses sharing its part. Postgres is the case that needs
// it: STORAGE, COMPRESSION and COLLATE all render straight after a column's
// type, and its grammar takes them only in that order — measured on 17, every
// other order is a syntax error. The statement sorts ranked column clauses
// exactly as it sorts table options; see OptionRank for why it sorts rather
// than refusing an order it could fix.
func NewRankedColumnClause[D render.Dialect](rank OptionRank, target ColumnRef, part ColumnPart, render func(a *render.Args) string) ColumnClause[D] {
	return columnClause[D]{target: target, part: part, rank: rank, render: render}
}

// NewTableClauseOn makes a TableClause that names columns of the statement's
// own table, so the statement checks each exists.
func NewTableClauseOn[D render.Dialect](pos Position, cols []ColumnRef, render func(a *render.Args) string) TableClause[D] {
	return namingClause[D]{tableClause: tableClause[D]{pos: pos, render: render}, cols: cols}
}

// NewOptionClauseOn is NewOptionClause for an option that names columns of the
// statement's own table, as PARTITION BY does, so the statement both checks
// each column exists and renders the option at its rank.
func NewOptionClauseOn[D render.Dialect](rank OptionRank, cols []ColumnRef, render func(a *render.Args) string) TableClause[D] {
	return namingClause[D]{tableClause: tableClause[D]{pos: PositionOption, rank: rank, render: render}, cols: cols}
}

type columnClause[D render.Dialect] struct {
	target ColumnRef
	part   ColumnPart
	rank   OptionRank
	render func(a *render.Args) string
}

func (columnClause[D]) Position() Position                            { return PositionColumn }
func (c columnClause[D]) WriteSQL(b *strings.Builder, a *render.Args) { b.WriteString(c.render(a)) }
func (columnClause[D]) TableClauseOf(D)                               {}
func (c columnClause[D]) Target() ColumnRef                           { return c.target }
func (c columnClause[D]) Part() ColumnPart                            { return c.part }
func (c columnClause[D]) OptionRank() OptionRank                      { return c.rank }

type namingClause[D render.Dialect] struct {
	tableClause[D]
	cols []ColumnRef
}

func (c namingClause[D]) NamedColumns() []ColumnRef { return c.cols }

// ColumnExtras is what the statement's column clauses add to one column's
// definition, already rendered, which a statement passes to WriteColumn. It is
// exported so a ColumnSpec written outside this package can honour it.
type ColumnExtras struct {
	// AfterType is written straight after the type.
	AfterType []string
	// Constraints is written after the default.
	Constraints []string
	// ValueSource is set when a constraint supplies the value, so a default
	// is refused.
	ValueSource bool
	// Computed is set for a generated column: no default and no NOT NULL.
	Computed bool
	// AfterNotNull is written immediately after NOT NULL, and is refused when
	// the definition writes none; see PartAfterNotNull.
	AfterNotNull []string
	// RequiresDefault is set when a clause needs the column to have a
	// default, so a definition without one is refused with ErrNoDefault; see
	// DefaultRequirer. Exported, like the rest of ColumnExtras, so a
	// ColumnSpec written outside this package can honour it.
	RequiresDefault bool
}

// add renders one column clause into x at its part, failing Build on a part
// outside ColumnPartValues.
func (x *ColumnExtras) add(a *render.Args, cc interface {
	Part() ColumnPart
	WriteSQL(*strings.Builder, *render.Args)
}) bool {
	sql := a.Bare(func() string {
		var sb strings.Builder
		cc.WriteSQL(&sb, a)
		return sb.String()
	})
	if dr, ok := cc.(DefaultRequirer); ok && dr.RequiresDefault() {
		x.RequiresDefault = true
	}
	switch cc.Part() {
	case PartAfterType:
		x.AfterType = append(x.AfterType, sql)
	case PartConstraint:
		x.Constraints = append(x.Constraints, sql)
	case PartValueSource:
		x.Constraints = append(x.Constraints, sql)
		x.ValueSource = true
	case PartComputed:
		x.Constraints = append(x.Constraints, sql)
		x.Computed = true
	case PartAfterNotNull:
		x.AfterNotNull = append(x.AfterNotNull, sql)
	default:
		a.Fail(fmt.Errorf("%w: column part %d", ErrUnknownPosition, cc.Part()))
		return false
	}
	return true
}

// WriteColumnWith renders spec with column clauses that must all name it, for
// an engine ALTER TABLE action that adds a column with a collation or an
// identity. A clause naming another column fails Build with ErrUnknownColumn,
// as it would in CREATE TABLE.
func WriteColumnWith[D render.Dialect](b *strings.Builder, a *render.Args, spec ColumnSpec, clauses []ColumnClause[D]) {
	var x ColumnExtras
	for _, cc := range clauses {
		t := cc.Target()
		switch {
		case t == nil || t.Name() == "":
			a.Fail(ErrNoColumnTarget)
			return
		case !sameTable(t.Table(), spec.ColumnTable()) || t.Name() != spec.ColumnName():
			a.Fail(fmt.Errorf("%w: %s is not the column being added, %s", ErrUnknownColumn, t.Name(), spec.ColumnName()))
			return
		}
		if !x.add(a, cc) {
			return
		}
	}
	spec.WriteColumn(b, a, x)
}
