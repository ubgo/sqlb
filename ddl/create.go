package ddl

import (
	"errors"
	"fmt"
	"slices"
	"sort"
	"strings"

	"github.com/ubgo/sqlb/expr"
	"github.com/ubgo/sqlb/render"
	"github.com/ubgo/sqlb/schema"
)

// ErrForeignColumn is returned when a statement is given a column declared on
// another table. CREATE TABLE users with Column(sessions.UserID) would create
// a column the queries read from a different table, which is the drift this
// package exists to prevent.
var ErrForeignColumn = errors.New("sqlb/ddl: column belongs to another table")

// ErrDuplicateColumn is returned when a CREATE TABLE defines one column twice.
var ErrDuplicateColumn = errors.New("sqlb/ddl: column defined twice")

// ErrUnknownPosition is returned for an engine clause aimed at a position the
// statement does not render; the clause would otherwise vanish.
var ErrUnknownPosition = errors.New("sqlb/ddl: clause asks for a position this statement does not render")

// createCore is what CREATE TABLE and its complete form share.
type createCore[D render.Dialect] struct {
	dialect     D
	table       expr.Table
	ifNotExists bool
	columns     []ColumnSpec
	constraints []Constraint
	clauses     []TableClause[D]
}

func (c createCore[D]) derive() createCore[D] {
	c.columns = slices.Clone(c.columns)
	c.constraints = slices.Clone(c.constraints)
	c.clauses = slices.Clone(c.clauses)
	return c
}

// CreateTable is a CREATE TABLE with no column yet. It has no Build: a table
// with no column is rejected by SQLite and useless on Postgres, so the
// statement is not complete until Column has been called, and the incomplete
// form cannot be built. That is a type, not a check.
type CreateTable[D render.Dialect] struct{ core createCore[D] }

// CreateTableReady is a CREATE TABLE with at least one column. It is the only
// form with Build.
type CreateTableReady[D render.Dialect] struct{ core createCore[D] }

// NewCreateTable starts a CREATE TABLE of t for d. The dialect is in the type,
// so an engine's table clause for another engine does not compile; an engine
// package spells this pg.CreateTable(t) or sqlite.CreateTable(t).
func NewCreateTable[D render.Dialect](d D, t expr.Table) *CreateTable[D] {
	return &CreateTable[D]{core: createCore[D]{dialect: d, table: t}}
}

// IfNotExists makes the statement do nothing when the table exists. It checks
// the name only, not the shape: a table that exists with other columns is left
// as it is.
func (c *CreateTable[D]) IfNotExists() *CreateTable[D] {
	n := c.core.derive()
	n.ifNotExists = true
	return &CreateTable[D]{core: n}
}

// Constraint adds universal table constraints.
func (c *CreateTable[D]) Constraint(cs ...Constraint) *CreateTable[D] {
	n := c.core.derive()
	n.constraints = append(n.constraints, cs...)
	return &CreateTable[D]{core: n}
}

// Clause adds engine clauses typed to this statement's dialect.
func (c *CreateTable[D]) Clause(cs ...TableClause[D]) *CreateTable[D] {
	n := c.core.derive()
	n.clauses = append(n.clauses, cs...)
	return &CreateTable[D]{core: n}
}

// Column adds the first columns, and so completes the statement. It takes at
// least one, so an empty call does not compile.
func (c *CreateTable[D]) Column(first ColumnSpec, more ...ColumnSpec) *CreateTableReady[D] {
	n := c.core.derive()
	n.columns = append(append(n.columns, first), more...)
	return &CreateTableReady[D]{core: n}
}

// IfNotExists makes the statement do nothing when the table exists.
func (c *CreateTableReady[D]) IfNotExists() *CreateTableReady[D] {
	n := c.core.derive()
	n.ifNotExists = true
	return &CreateTableReady[D]{core: n}
}

// Column adds columns.
func (c *CreateTableReady[D]) Column(cols ...ColumnSpec) *CreateTableReady[D] {
	n := c.core.derive()
	n.columns = append(n.columns, cols...)
	return &CreateTableReady[D]{core: n}
}

// Constraint adds universal table constraints.
func (c *CreateTableReady[D]) Constraint(cs ...Constraint) *CreateTableReady[D] {
	n := c.core.derive()
	n.constraints = append(n.constraints, cs...)
	return &CreateTableReady[D]{core: n}
}

// Clause adds engine clauses typed to this statement's dialect.
func (c *CreateTableReady[D]) Clause(cs ...TableClause[D]) *CreateTableReady[D] {
	n := c.core.derive()
	n.clauses = append(n.clauses, cs...)
	return &CreateTableReady[D]{core: n}
}

// Table is the table the statement creates, for a builder that composes a
// CREATE TABLE into a larger script, as SQLite's table rebuild does.
func (c *CreateTableReady[D]) Table() expr.Table { return c.core.table }

// Build renders the statement. Every value in it is a literal, so the Script
// carries no arguments.
func (c *CreateTableReady[D]) Build() (Script, error) {
	if c.core.table.Name == "" || len(c.core.columns) == 0 {
		return nil, ErrZeroValue
	}
	a, err := inlineArgs(c.core.dialect)
	if err != nil {
		return nil, err
	}
	var b strings.Builder
	c.core.write(&b, a)
	if err := a.Err(); err != nil {
		return nil, err
	}
	return Script{{SQL: b.String(), Transactional: true}}, nil
}

func (c createCore[D]) write(b *strings.Builder, a *render.Args) {
	d := a.Dialect()
	byPos := map[Position][]TableClause[D]{}
	for _, cl := range c.clauses {
		if !slices.Contains(PositionValues, cl.Position()) {
			a.Fail(fmt.Errorf("%w: %d", ErrUnknownPosition, cl.Position()))
			return
		}
		byPos[cl.Position()] = append(byPos[cl.Position()], cl)
	}

	b.WriteString(kwCreate)
	for _, cl := range byPos[PositionModifier] {
		b.WriteString(sepSpace)
		writeBare(b, a, cl)
	}
	b.WriteString(sepSpace)
	b.WriteString(kwTable)
	b.WriteString(sepSpace)
	if c.ifNotExists {
		b.WriteString(kwIfNotExists)
		b.WriteString(sepSpace)
	}
	b.WriteString(c.table.Qualified(d))
	b.WriteString(sepSpace)
	b.WriteString(parOpen)

	seen := map[string]bool{}
	for _, col := range c.columns {
		seen[col.ColumnName()] = true
	}
	extras, ok := c.columnExtras(a, byPos[PositionColumn], seen)
	if !ok {
		return
	}
	// Every position but the column one, which columnExtras checked: a
	// PARTITION BY key names columns from the option position, and a clause
	// is checked wherever it renders.
	for _, pos := range []Position{PositionModifier, PositionElement, PositionOption} {
		for _, cl := range byPos[pos] {
			if !c.checkNamed(a, cl, seen) {
				return
			}
		}
	}
	defined := map[string]bool{}
	n := 0
	sep := func() {
		if n > 0 {
			b.WriteString(sepComma)
		}
		n++
	}
	for _, col := range c.columns {
		if !sameTable(col.ColumnTable(), c.table) {
			a.Fail(fmt.Errorf("%w: %s is declared on %s, not %s", ErrForeignColumn, col.ColumnName(), col.ColumnTable().Name, c.table.Name))
			return
		}
		if defined[col.ColumnName()] {
			a.Fail(fmt.Errorf("%w: %s", ErrDuplicateColumn, col.ColumnName()))
			return
		}
		defined[col.ColumnName()] = true
		sep()
		col.WriteColumn(b, a, extras[col.ColumnName()])
	}
	for _, con := range c.constraints {
		if !c.checkTable(a, con) {
			return
		}
		sep()
		con.WriteConstraint(b, a)
	}
	for _, cl := range byPos[PositionElement] {
		sep()
		writeBare(b, a, cl)
	}
	b.WriteString(parClose)
	prevListed := false
	if !c.checkWhole(a) {
		return
	}
	for _, cl := range orderOptions(byPos[PositionOption]) {
		_, listed := cl.(ListedOption)
		if listed && prevListed {
			b.WriteString(sepComma)
		} else {
			b.WriteString(sepSpace)
		}
		writeBare(b, a, cl)
		prevListed = listed
	}
}

// columnExtras renders every column clause and files it under the column it
// names. A clause naming no column, a column of another table, or one the
// statement does not define fails Build rather than vanishing.
func (c createCore[D]) columnExtras(a *render.Args, cls []TableClause[D], defined map[string]bool) (map[string]ColumnExtras, bool) {
	out := map[string]ColumnExtras{}
	// Ranked column clauses are sorted by the same rule as table options, so
	// clauses sharing a part render in the order the engine's grammar
	// requires. Sorting the whole list rather than each column's is correct
	// because the sort is stable: every column's own clauses keep their
	// relative rank order, and clauses for different columns land in
	// different entries of out. See OptionRank.
	for _, cl := range orderOptions(cls) {
		cc, ok := cl.(ColumnClause[D])
		if !ok || cc.Target() == nil {
			a.Fail(ErrNoColumnTarget)
			return nil, false
		}
		name := cc.Target().Name()
		if !c.checkRef(a, cc.Target(), defined) {
			return nil, false
		}
		x := out[name]
		if !x.add(a, cc) {
			return nil, false
		}
		out[name] = x
	}
	return out, true
}

// checkNamed checks the columns a table-level clause names, when it says.
func (c createCore[D]) checkNamed(a *render.Args, cl TableClause[D], defined map[string]bool) bool {
	if !c.checkTable(a, cl) {
		return false
	}
	n, ok := cl.(ColumnNamer)
	if !ok {
		return true
	}
	for _, r := range n.NamedColumns() {
		if !c.checkRef(a, r, defined) {
			return false
		}
	}
	return true
}

// checkWhole hands the statement's own description to every clause that
// checks the whole table, a TableChecker, and fails Build on its error.
func (c createCore[D]) checkWhole(a *render.Args) bool {
	var desc *schema.Table
	for _, cl := range c.clauses {
		tc, ok := cl.(TableChecker)
		if !ok {
			continue
		}
		if desc == nil {
			d, err := c.describe()
			if err != nil {
				a.Fail(err)
				return false
			}
			desc = &d
		}
		if err := tc.CheckTable(*desc); err != nil {
			a.Fail(err)
			return false
		}
	}
	return true
}

// checkTable fails Build for a clause or constraint that says which table it
// belongs to, a TableConstraint's, and names another: a key declared on one
// table and placed in another's CREATE TABLE would otherwise be created there.
func (c createCore[D]) checkTable(a *render.Args, x any) bool {
	tn, ok := x.(TableNamer)
	// An unnamed key or an inline reference declares no table of its own.
	if !ok || tn.ClauseTable().Name == "" || sameTable(tn.ClauseTable(), c.table) {
		return true
	}
	a.Fail(fmt.Errorf("%w: a constraint of %s in CREATE TABLE %s", ErrForeignColumn, tn.ClauseTable().Name, c.table.Name))
	return false
}

// checkRef fails Build for a column that is not a column, is declared on
// another table, or is not defined by this statement.
func (c createCore[D]) checkRef(a *render.Args, r ColumnRef, defined map[string]bool) bool {
	switch {
	case r == nil || r.Name() == "":
		a.Fail(ErrNotAColumn)
	case !sameTable(r.Table(), c.table):
		a.Fail(fmt.Errorf("%w: %s is declared on %s, not %s", ErrForeignColumn, r.Name(), r.Table().Name, c.table.Name))
	case !defined[r.Name()]:
		a.Fail(fmt.Errorf("%w: %s", ErrUnknownColumn, r.Name()))
	default:
		return true
	}
	return false
}

// sameTable compares by schema and name. The alias is not part of a table's
// identity: two declarations of one table under different aliases, which a
// plugin makes when it borrows a table, are the same table here.
func sameTable(x, y expr.Table) bool { return x.Name == y.Name && x.Schema == y.Schema }

// inlineArgs returns the Args every DDL statement renders through. A dialect
// that cannot write a literal cannot render DDL, because every value in DDL is
// one, so it fails here, before any statement text exists.
func inlineArgs(d render.Dialect) (*render.Args, error) {
	if _, ok := d.(render.LiteralQuoting); !ok {
		return nil, fmt.Errorf("%w: %q", render.ErrNoLiteralQuoting, render.DialectName(d))
	}
	return render.NewInlineArgs(d), nil
}

// clauseWriter is what every DDL clause is, whatever its position.
type clauseWriter interface {
	WriteSQL(b *strings.Builder, a *render.Args)
}

// writeBare renders a table or ALTER clause with every column written by its
// bare name. Nothing inside CREATE TABLE or ALTER TABLE can name a column
// through an alias, because the statement declares none, so a qualified
// column there is always an error at the server. Doing it here rather than in
// each clause means a raw clause from ddl.NewTableClause or NewAlterClause is
// right without knowing the rule: "i"."name" in a raw CHECK was a real
// failure, found by the raw recipe that now pins it.
func writeBare(b *strings.Builder, a *render.Args, cl clauseWriter) {
	b.WriteString(a.Bare(func() string {
		var sb strings.Builder
		cl.WriteSQL(&sb, a)
		return sb.String()
	}))
}

// orderOptions returns the options at PositionOption in the order the engine's
// grammar requires: the ranked ones sorted by OptionRank, each into a position
// a ranked option already held, and every unranked one left exactly where the
// caller put it. See OptionRank for why the statement sorts rather than
// refusing, and why an unranked option does not move.
//
// The sort is stable, so two options of one rank keep the caller's order,
// which is what MySQL needs: its table options are one rank because it takes
// them in any order, and only PARTITION BY must follow them.
//
// It returns a copy rather than reordering its argument. That copy is
// defensive, not load-bearing, and the difference was measured: Build groups
// the clauses into a map it creates afresh on every call, so the slice passed
// here belongs to one Build and reordering it in place would reorder nothing
// a template or a derived statement can see — removing the copy leaves
// TestOptionRank_SortingDoesNotReorderTheTemplate green. It stays because the
// rule in this repository is that a function taking a slice it did not make
// does not write to it, and orderOptions should remain safe if a later caller
// passes it a statement's own slice.
func orderOptions[D render.Dialect](cls []TableClause[D]) []TableClause[D] {
	var slots []int
	var ranked []TableClause[D]
	for i, cl := range cls {
		if rankOf(cl) > 0 {
			slots = append(slots, i)
			ranked = append(ranked, cl)
		}
	}
	if len(ranked) < 2 {
		return cls
	}
	sort.SliceStable(ranked, func(i, j int) bool { return rankOf(ranked[i]) < rankOf(ranked[j]) })
	out := slices.Clone(cls)
	for k, i := range slots {
		out[i] = ranked[k]
	}
	return out
}

// rankOf is a clause's OptionRank, or zero for one that has none. It takes the
// clause type rather than any, so nothing but a clause can be asked.
func rankOf[D render.Dialect](cl TableClause[D]) OptionRank {
	if r, ok := cl.(OptionRanker); ok {
		return r.OptionRank()
	}
	return 0
}
