package stmt

import (
	"errors"
	"fmt"
	"github.com/ubgo/sqlb/render"
	"sort"
	"strings"
)

var (
	// ErrNotAColumn is returned when an assignment target or an INSERT column
	// list holds something other than a bare column made by Col. An
	// expression has no name to assign to, and rendering it as an empty
	// identifier would produce SQL the database rejects much later.
	ErrNotAColumn = errors.New("sqlb: assignment target is not a column")

	// ErrNoAssignments is returned by an UPDATE with nothing to SET, or an
	// INSERT with neither rows nor a source query. There is nothing to write.
	ErrNoAssignments = errors.New("sqlb: statement has no assignments")

	// ErrNoWhere is returned by an UPDATE or DELETE with no WHERE and no
	// All(). The whole table is a real target, but it must be said out loud,
	// because the cost of the accidental case is the entire table.
	ErrNoWhere = errors.New("sqlb: update or delete has no WHERE; call All() to affect every row")

	// ErrRowWidth is returned when an INSERT row has a different number of
	// values from the column list. The builder cannot know which value was
	// meant for which column, so it refuses rather than aligning by position.
	ErrRowWidth = errors.New("sqlb: insert row width differs from column list")
)

// Assignment is one column = value pair for INSERT and UPDATE. Built with
// Set, SetExpr or SetDefault, so the value type matches the column type.
type Assignment struct {
	col   string
	value SQLer
	// target, when set, renders the assignment target instead of the bare
	// column: a JSONB subscript such as doc['a']['b'], or an array element.
	// UPDATE accepts it; INSERT does not, because a column list needs names.
	target func(a *Args) string
}

// AssignTarget makes an assignment whose target is an expression over col,
// for engine packages: doc['customer']['tier'] = value. col is the base
// column, kept for the INSERT column list and for error messages.
func AssignTarget(col string, target func(a *Args) string, value SQLer) Assignment {
	return Assignment{col: col, value: value, target: target}
}

// Set assigns a bound value to a column of the same Go type.
func Set[T any](col Expr[T], v T) Assignment { return Assignment{col: col.Name(), value: Param(v)} }

// SetExpr assigns an expression of the column's type: SET n = n + 1.
func SetExpr[T any](col Expr[T], e Expr[T]) Assignment { return Assignment{col: col.Name(), value: e} }

// Column returns the target column name.
//
// This accessor and the two below exist for the engine packages: ON CONFLICT
// and MERGE render assignments outside the core's UPDATE and need the parts
// separately, which is the only reason an Assignment is not opaque.
func (as Assignment) Column() string { return as.col }

// ValueSQL renders the assigned value alone, without the column or the equals.
func (as Assignment) ValueSQL(a *Args) string { return as.value.SQL(a) }

// SQL renders the whole assignment, "column = value".
func (as Assignment) SQL(a *Args) string {
	if as.target != nil {
		return as.target(a) + " = " + as.value.SQL(a)
	}
	return a.Dialect().QuoteIdent(as.col) + " = " + as.value.SQL(a)
}

// SetDefault assigns DEFAULT.
func SetDefault[T any](col Expr[T]) Assignment {
	return Assignment{col: col.Name(), value: Raw(func(*Args) string { return kwDefault })}
}

// InsertReady is an INSERT that has a body — rows, a query, or an engine
// clause supplying one — and can therefore be built.
//
// It is the second of two types because an INSERT with nothing to insert is
// a mistake the compiler can catch: Build lives here and not on Insert, so
// NewInsert(d).Into(t).Build() does not compile. Reaching this type is what
// Row, Select and Body do. A value made as a struct literal has no body and
// no dialect and is rejected at Build, which is the documented literal hole.
type InsertReady[D render.Dialect] struct {
	guard     *execGuard
	dialect   D
	ctes      []CTE
	table     Table
	columns   []string
	rows      [][]SQLer
	source    Statement
	returning []Binding
	clauses   map[Slot][]Clause[D, InsertKind]
}

// Insert is an INSERT that has no body yet. It has no Build method.
//
// Row, Select and Body each return an InsertReady, which does. That is the
// whole of the typestate: the only way to obtain a value with Build is to
// have given the statement something to insert, so the empty INSERT that
// used to be ErrNoAssignments at Build is now a type error at the call site.
type Insert[D render.Dialect] struct{ st InsertReady[D] }

// NewInsert starts an INSERT for d.
func NewInsert[D render.Dialect](d D) *Insert[D] {
	return &Insert[D]{st: InsertReady[D]{dialect: d, guard: &execGuard{}}}
}

// Dialect reports the dialect this statement renders for, so an Option or
// a helper can branch on the engine without the caller threading it through.
func (i *Insert[D]) Dialect() Dialect { return i.st.dialect }

// Into names the table. The alias renders only when the dialect satisfies
// InsertAliasing; otherwise the bare table renders and RETURNING uses bare
// column names for bindings made from Col.
func (i *Insert[D]) Into(t Table) *Insert[D] { c := i.derive(); c.st.table = t; return c }

// With adds a CTE, for INSERT ... SELECT from a CTE or a data-modifying CTE.
func (i *Insert[D]) With(name string, body Statement) *Insert[D] {
	c := i.derive()
	c.st.ctes = append(c.st.ctes, CTE{Name: name, Body: body})
	return c
}

// Columns names the columns for an INSERT ... SELECT.
func (i *Insert[D]) Columns(cols ...SQLer) *Insert[D] {
	out := i.derive()
	out.st.addColumns(cols)
	return out
}

// Clause adds an engine clause in a slot other than the body: pg.OnConflict
// at the tail, pg.OverridingSystemValue after the target. A clause that
// supplies the whole body, pg.DefaultValues, goes through Body instead,
// because it is what makes the statement complete.
func (i *Insert[D]) Clause(cs ...Clause[D, InsertKind]) *Insert[D] {
	out := i.derive()
	out.st.addClauses(cs)
	return out
}

// Row adds one row of assignments and completes the statement. The first row
// fixes the column list; every later row must assign the same columns in the
// same order.
func (i *Insert[D]) Row(as ...Assignment) *InsertReady[D] {
	r := i.st.derive()
	r.addRow(as)
	return r
}

// Select makes the rows come from a query, INSERT INTO t (cols) SELECT ...,
// and completes the statement.
func (i *Insert[D]) Select(s *Select[D]) *InsertReady[D] {
	r := i.st.derive()
	r.source = s
	return r
}

// Body completes the statement with an engine clause that supplies the whole
// body, such as pg.DefaultValues. It is the third door to InsertReady, and it
// exists because DEFAULT VALUES is a legitimate INSERT with no rows and no
// query, which the typestate would otherwise make unwritable.
func (i *Insert[D]) Body(c Clause[D, InsertKind]) *InsertReady[D] {
	r := i.st.derive()
	r.addClauses([]Clause[D, InsertKind]{c})
	return r
}

// Clone returns an independent copy with its own execution guard.
func (i *Insert[D]) Clone() *Insert[D] {
	if i == nil {
		return nil
	}
	return &Insert[D]{st: *i.st.Clone()}
}

func (i *Insert[D]) derive() *Insert[D] { return &Insert[D]{st: *i.st.derive()} }

// Row adds another row. The column list was fixed by the first.
func (i *InsertReady[D]) Row(as ...Assignment) *InsertReady[D] {
	c := i.derive()
	c.addRow(as)
	return c
}

// With adds a CTE.
func (i *InsertReady[D]) With(name string, body Statement) *InsertReady[D] {
	c := i.derive()
	c.ctes = append(c.ctes, CTE{Name: name, Body: body})
	return c
}

// Clause adds an engine clause: pg.OnConflict at the tail, pg.OverridingSystemValue
// after the target.
func (i *InsertReady[D]) Clause(cs ...Clause[D, InsertKind]) *InsertReady[D] {
	c := i.derive()
	c.addClauses(cs)
	return c
}

// Dialect reports the dialect this statement renders for.
func (i *InsertReady[D]) Dialect() Dialect { return i.dialect }

func (i *InsertReady[D]) addRow(as []Assignment) {
	if i.columns == nil {
		for _, a := range as {
			i.columns = append(i.columns, a.col)
		}
	}
	vals := make([]SQLer, len(as))
	for j, a := range as {
		vals[j] = a.value
	}
	i.rows = append(i.rows, vals)
}

func (i *InsertReady[D]) addColumns(cols []SQLer) {
	for _, c := range cols {
		if n, ok := c.(interface{ Name() string }); ok && n.Name() != "" {
			i.columns = append(i.columns, n.Name())
		} else {
			i.columns = append(i.columns, "")
		}
	}
}

func (i *InsertReady[D]) addClauses(cs []Clause[D, InsertKind]) {
	if i.clauses == nil {
		i.clauses = make(map[Slot][]Clause[D, InsertKind])
	}
	for _, c := range cs {
		i.clauses[c.Slot()] = append(i.clauses[c.Slot()], c)
	}
}

// Build renders the statement, or returns the first error it found. The
// scan targets come from the RETURNING bindings, so One and Each work on the
// result without a second list.
func (i *InsertReady[D]) Build() (Query, error) {
	if i.guard == nil {
		i.guard = &execGuard{}
	}
	return build(i.dialect, i, bindingTargets(i.returning), i.guard)
}

// WriteSQL renders the INSERT into b. It satisfies Statement, which is what
// lets this statement be the body of a CTE in another one.
func (i *InsertReady[D]) WriteSQL(b *strings.Builder, args *Args) {
	writeCTEs(b, args, i.ctes)
	d := args.Dialect()
	b.WriteString(kwInsert)
	// SQLite's OR REPLACE and its siblings sit between INSERT and INTO, which
	// is the only reason this is two keywords and a slot rather than one.
	writeSlot(b, args, i.clauses, SlotAfterInsert)
	b.WriteString(sepSpace)
	b.WriteString(kwInto)
	b.WriteString(sepSpace)
	aliased := false
	if ia, ok := d.(InsertAliasing); ok && ia.SupportsInsertAlias() {
		aliased = true
		b.WriteString(i.table.SQL(d))
	} else {
		b.WriteString(i.table.Qualified(d))
	}
	writeSlot(b, args, i.clauses, SlotAfterTarget)
	switch {
	case len(i.clauses[SlotInsertBody]) > 0:
		// An engine clause supplies the whole body: DEFAULT VALUES.
		writeSlot(b, args, i.clauses, SlotInsertBody)
	case i.source != nil:
		if len(i.columns) > 0 {
			writeColumnList(b, d, i.columns, args)
		}
		writeSlot(b, args, i.clauses, SlotBeforeValues)
		b.WriteString(sepSpace)
		i.source.WriteSQL(b, args)
	case len(i.rows) > 0:
		writeColumnList(b, d, i.columns, args)
		writeSlot(b, args, i.clauses, SlotBeforeValues)
		b.WriteString(sepSpace)
		b.WriteString(kwValues)
		b.WriteString(sepSpace)
		for r, row := range i.rows {
			if len(row) != len(i.columns) {
				args.Fail(ErrRowWidth)
				return
			}
			if r > 0 {
				b.WriteString(sepComma)
			}
			b.WriteString("(")
			for j, v := range row {
				if j > 0 {
					b.WriteString(sepComma)
				}
				b.WriteString(v.SQL(args))
			}
			b.WriteString(")")
		}
	default:
		args.Fail(ErrNoAssignments)
		return
	}
	writeSlot(b, args, i.clauses, SlotTail)
	writeReturning(b, args, i.returning, aliased)
	checkSlots(args, i.clauses, SlotAfterInsert, SlotAfterTarget, SlotInsertBody, SlotBeforeValues, SlotTail)
}

func writeColumnList(b *strings.Builder, d Dialect, cols []string, args *Args) {
	b.WriteString(" (")
	for j, c := range cols {
		if c == "" {
			args.Fail(ErrNotAColumn)
			return
		}
		if j > 0 {
			b.WriteString(sepComma)
		}
		b.WriteString(d.QuoteIdent(c))
	}
	b.WriteString(")")
}

// FilteredUpdate is an UPDATE whose target rows are stated — by a WHERE or
// by All — and can therefore be built.
//
// Build lives here and not on Update, so an UPDATE with no WHERE does not
// compile into existence. That sentence was the task list's day-one promise;
// it shipped as ErrNoWhere at Build, and this is what makes it true. A value
// made as a struct literal has no predicate, no table and no dialect and is
// rejected at Build, which is the documented literal hole.
type FilteredUpdate[D render.Dialect] struct {
	guard     *execGuard
	dialect   D
	ctes      []CTE
	table     Table
	sets      []Assignment
	where     []Predicate
	all       bool
	returning []Binding
	clauses   map[Slot][]Clause[D, UpdateKind]
}

// Update is an UPDATE whose target rows are not yet stated. It has no Build
// method. Where and All each return a FilteredUpdate, which does.
type Update[D render.Dialect] struct{ st FilteredUpdate[D] }

// NewUpdate starts an UPDATE for d.
func NewUpdate[D render.Dialect](d D) *Update[D] {
	return &Update[D]{st: FilteredUpdate[D]{dialect: d, guard: &execGuard{}}}
}

// Dialect reports the dialect this statement renders for, so an Option or
// a helper can branch on the engine without the caller threading it through.
func (u *Update[D]) Dialect() Dialect { return u.st.dialect }

// Table names the table to update.
func (u *Update[D]) Table(t Table) *Update[D] { c := u.derive(); c.st.table = t; return c }

// With attaches a CTE, for "update the rows this query found". It renders
// before the UPDATE keyword, not inside it.
func (u *Update[D]) With(name string, body Statement) *Update[D] {
	c := u.derive()
	c.st.ctes = append(c.st.ctes, CTE{Name: name, Body: body})
	return c
}

// Set adds assignments. Calling it more than once accumulates rather than
// replacing, so a library and a caller can each contribute.
func (u *Update[D]) Set(as ...Assignment) *Update[D] {
	c := u.derive()
	c.st.sets = append(c.st.sets, as...)
	return c
}

// Clause adds an engine clause, such as pg.UpdateFrom in SlotAfterSet.
func (u *Update[D]) Clause(cs ...Clause[D, UpdateKind]) *Update[D] {
	c := u.derive()
	c.st.addClauses(cs)
	return c
}

// Where states the target rows and completes the statement. Predicates are
// joined with AND, and more may be added on the result.
func (u *Update[D]) Where(ps ...Predicate) *FilteredUpdate[D] {
	f := u.st.derive()
	f.where = append(f.where, ps...)
	return f
}

// All states that every row is the target and completes the statement. It is
// what makes an UPDATE with no WHERE deliberate rather than an omission.
func (u *Update[D]) All() *FilteredUpdate[D] {
	f := u.st.derive()
	f.all = true
	return f
}

// Clone returns an independent copy with its own execution guard.
func (u *Update[D]) Clone() *Update[D] {
	if u == nil {
		return nil
	}
	return &Update[D]{st: *u.st.Clone()}
}

func (u *Update[D]) derive() *Update[D] { return &Update[D]{st: *u.st.derive()} }

// Dialect reports the dialect this statement renders for.
func (u *FilteredUpdate[D]) Dialect() Dialect { return u.dialect }

// With attaches a CTE.
func (u *FilteredUpdate[D]) With(name string, body Statement) *FilteredUpdate[D] {
	c := u.derive()
	c.ctes = append(c.ctes, CTE{Name: name, Body: body})
	return c
}

// Set adds assignments.
func (u *FilteredUpdate[D]) Set(as ...Assignment) *FilteredUpdate[D] {
	c := u.derive()
	c.sets = append(c.sets, as...)
	return c
}

// Clause adds an engine clause.
func (u *FilteredUpdate[D]) Clause(cs ...Clause[D, UpdateKind]) *FilteredUpdate[D] {
	c := u.derive()
	c.addClauses(cs)
	return c
}

// Where adds predicates, joined with AND.
func (u *FilteredUpdate[D]) Where(ps ...Predicate) *FilteredUpdate[D] {
	c := u.derive()
	c.where = append(c.where, ps...)
	return c
}

func (u *FilteredUpdate[D]) addClauses(cs []Clause[D, UpdateKind]) {
	if u.clauses == nil {
		u.clauses = make(map[Slot][]Clause[D, UpdateKind])
	}
	for _, c := range cs {
		u.clauses[c.Slot()] = append(u.clauses[c.Slot()], c)
	}
}

// Build renders the statement, or returns the first error it found. The
// scan targets come from the RETURNING bindings, so One and Each work on the
// result without a second list.
func (u *FilteredUpdate[D]) Build() (Query, error) {
	if u.guard == nil {
		u.guard = &execGuard{}
	}
	return build(u.dialect, u, bindingTargets(u.returning), u.guard)
}

// WriteSQL renders the UPDATE into b. It satisfies Statement, which is what
// lets this statement be the body of a CTE in another one.
func (u *FilteredUpdate[D]) WriteSQL(b *strings.Builder, args *Args) {
	if len(u.sets) == 0 {
		args.Fail(ErrNoAssignments)
		return
	}
	if len(u.where) == 0 && !u.all {
		args.Fail(ErrNoWhere)
		return
	}
	writeCTEs(b, args, u.ctes)
	d := args.Dialect()
	b.WriteString(kwUpdate)
	writeSlot(b, args, u.clauses, SlotAfterVerb)
	b.WriteString(sepSpace)
	b.WriteString(u.table.SQL(d))
	writeSlot(b, args, u.clauses, SlotAfterTarget)
	b.WriteString(sepSpace)
	b.WriteString(kwSet)
	b.WriteString(sepSpace)
	for j, s := range u.sets {
		if s.col == "" {
			args.Fail(ErrNotAColumn)
			return
		}
		if j > 0 {
			b.WriteString(sepComma)
		}
		b.WriteString(s.SQL(args))
	}
	writeSlot(b, args, u.clauses, SlotAfterSet)
	if len(u.where) > 0 {
		b.WriteString(sepSpace)
		b.WriteString(kwWhere)
		b.WriteString(sepSpace)
		And(u.where...).WriteSQL(b, args)
	}
	writeSlot(b, args, u.clauses, SlotAfterWhere)
	writeSlot(b, args, u.clauses, SlotTail)
	writeReturning(b, args, u.returning, true)
	checkSlots(args, u.clauses, SlotAfterVerb, SlotAfterTarget, SlotAfterSet, SlotAfterWhere, SlotTail)
}

// FilteredDelete is a DELETE whose target rows are stated — by a WHERE or by
// All — and can therefore be built. Build lives here and not on Delete, for
// the reason given on FilteredUpdate.
type FilteredDelete[D render.Dialect] struct {
	guard     *execGuard
	dialect   D
	ctes      []CTE
	table     Table
	where     []Predicate
	all       bool
	returning []Binding
	clauses   map[Slot][]Clause[D, DeleteKind]
}

// Delete is a DELETE whose target rows are not yet stated. It has no Build
// method. Where and All each return a FilteredDelete, which does.
type Delete[D render.Dialect] struct{ st FilteredDelete[D] }

// NewDelete starts a DELETE for d.
func NewDelete[D render.Dialect](d D) *Delete[D] {
	return &Delete[D]{st: FilteredDelete[D]{dialect: d, guard: &execGuard{}}}
}

// Dialect reports the dialect this statement renders for, so an Option or
// a helper can branch on the engine without the caller threading it through.
func (x *Delete[D]) Dialect() Dialect { return x.st.dialect }

// From names the table to delete from.
func (x *Delete[D]) From(t Table) *Delete[D] { c := x.derive(); c.st.table = t; return c }

// With attaches a CTE, for "delete the rows this query found".
func (x *Delete[D]) With(name string, body Statement) *Delete[D] {
	c := x.derive()
	c.st.ctes = append(c.st.ctes, CTE{Name: name, Body: body})
	return c
}

// Clause adds an engine clause, such as pg.DeleteUsing in SlotAfterTarget.
func (x *Delete[D]) Clause(cs ...Clause[D, DeleteKind]) *Delete[D] {
	c := x.derive()
	c.st.addClauses(cs)
	return c
}

// Where states the target rows and completes the statement.
func (x *Delete[D]) Where(ps ...Predicate) *FilteredDelete[D] {
	f := x.st.derive()
	f.where = append(f.where, ps...)
	return f
}

// All says the whole table is the target and completes the statement, which
// is what makes a DELETE with no WHERE deliberate rather than an omission.
func (x *Delete[D]) All() *FilteredDelete[D] {
	f := x.st.derive()
	f.all = true
	return f
}

// Clone returns an independent copy with its own execution guard.
func (x *Delete[D]) Clone() *Delete[D] {
	if x == nil {
		return nil
	}
	return &Delete[D]{st: *x.st.Clone()}
}

func (x *Delete[D]) derive() *Delete[D] { return &Delete[D]{st: *x.st.derive()} }

// Dialect reports the dialect this statement renders for.
func (x *FilteredDelete[D]) Dialect() Dialect { return x.dialect }

// With attaches a CTE.
func (x *FilteredDelete[D]) With(name string, body Statement) *FilteredDelete[D] {
	c := x.derive()
	c.ctes = append(c.ctes, CTE{Name: name, Body: body})
	return c
}

// Clause adds an engine clause.
func (x *FilteredDelete[D]) Clause(cs ...Clause[D, DeleteKind]) *FilteredDelete[D] {
	c := x.derive()
	c.addClauses(cs)
	return c
}

// Where adds predicates, joined with AND.
func (x *FilteredDelete[D]) Where(ps ...Predicate) *FilteredDelete[D] {
	c := x.derive()
	c.where = append(c.where, ps...)
	return c
}

func (x *FilteredDelete[D]) addClauses(cs []Clause[D, DeleteKind]) {
	if x.clauses == nil {
		x.clauses = make(map[Slot][]Clause[D, DeleteKind])
	}
	for _, c := range cs {
		x.clauses[c.Slot()] = append(x.clauses[c.Slot()], c)
	}
}

// Build renders the statement, or returns the first error it found. The
// scan targets come from the RETURNING bindings, so One and Each work on the
// result without a second list.
func (x *FilteredDelete[D]) Build() (Query, error) {
	if x.guard == nil {
		x.guard = &execGuard{}
	}
	return build(x.dialect, x, bindingTargets(x.returning), x.guard)
}

// WriteSQL renders the DELETE into b. It satisfies Statement, which is what
// lets this statement be the body of a CTE in another one.
func (x *FilteredDelete[D]) WriteSQL(b *strings.Builder, args *Args) {
	if len(x.where) == 0 && !x.all {
		args.Fail(ErrNoWhere)
		return
	}
	writeCTEs(b, args, x.ctes)
	b.WriteString(kwDelete)
	writeSlot(b, args, x.clauses, SlotAfterVerb)
	b.WriteString(sepSpace)
	b.WriteString(kwFrom)
	b.WriteString(sepSpace)
	b.WriteString(x.table.SQL(args.Dialect()))
	writeSlot(b, args, x.clauses, SlotAfterTarget)
	if len(x.where) > 0 {
		b.WriteString(sepSpace)
		b.WriteString(kwWhere)
		b.WriteString(sepSpace)
		And(x.where...).WriteSQL(b, args)
	}
	writeSlot(b, args, x.clauses, SlotAfterWhere)
	writeSlot(b, args, x.clauses, SlotTail)
	writeReturning(b, args, x.returning, true)
	checkSlots(args, x.clauses, SlotAfterVerb, SlotAfterTarget, SlotAfterWhere, SlotTail)
}

// ReturningDialect is a dialect that has RETURNING: render.Dialect plus the
// capability method. It is a constraint rather than an interface value on
// purpose. RETURNING used to be a method on the complete write types, checked
// at Build by a type assertion, which meant an INSERT ... RETURNING on
// ClickHouse compiled and failed later. A method cannot be conditional on a
// statement's dialect, but a function's type parameter can be, so the three
// functions below take the statement, infer D from it, and refuse a D without
// the method at compile time. A dialect declines the capability by not
// implementing it, never by returning false, and the runtime check in
// writeReturning is what happens when one breaks that rule: an outside dialect
// that implements SupportsReturning and answers false gets ErrNoReturning at
// Build rather than SQL its engine rejects. It does not guard a statement built
// as a literal, which this comment claimed until 2026-09-24: a literal still
// carries a D, and every D satisfying this constraint answers true, so a
// literal never reaches it.
type ReturningDialect interface {
	render.Dialect
	SupportsReturning() bool
}

// InsertReturning adds RETURNING with scan targets to an INSERT, so One and
// Each work on the result. D must have RETURNING, or this does not compile.
func InsertReturning[D ReturningDialect](s *InsertReady[D], bs ...Binding) *InsertReady[D] {
	c := s.derive()
	c.returning = append(c.returning, bs...)
	return c
}

// UpdateReturning adds RETURNING with scan targets to an UPDATE.
func UpdateReturning[D ReturningDialect](s *FilteredUpdate[D], bs ...Binding) *FilteredUpdate[D] {
	c := s.derive()
	c.returning = append(c.returning, bs...)
	return c
}

// DeleteReturning adds RETURNING with scan targets to a DELETE, so the deleted
// rows can be read back in the same statement.
func DeleteReturning[D ReturningDialect](s *FilteredDelete[D], bs ...Binding) *FilteredDelete[D] {
	c := s.derive()
	c.returning = append(c.returning, bs...)
	return c
}

// ErrNoReturning is returned when RETURNING was requested on a dialect that
// does not have it. The message names the fallback: insert, then select.
var ErrNoReturning = errors.New("sqlb: dialect has no RETURNING; use the two-statement fallback")

// writeReturning renders RETURNING. aliased says whether the statement
// rendered its table with an alias; INSERT does not, so its RETURNING uses
// bare column names.
func writeReturning(b *strings.Builder, args *Args, bs []Binding, aliased bool) {
	if len(bs) == 0 {
		return
	}
	if r, ok := args.Dialect().(Returning); !ok || !r.SupportsReturning() {
		args.Fail(ErrNoReturning)
		return
	}
	b.WriteString(sepSpace)
	b.WriteString(kwReturning)
	b.WriteString(sepSpace)
	for j, bind := range bs {
		if j > 0 {
			b.WriteString(sepComma)
		}
		if n := bind.ColumnName(); n != "" && !aliased {
			b.WriteString(args.Dialect().QuoteIdent(n))
		} else {
			b.WriteString(bind.SQL(args))
		}
	}
}

func bindingTargets(bs []Binding) []any {
	if len(bs) == 0 {
		return nil
	}
	return Targets(bs)
}

func build(d render.Dialect, st Statement, targets []any, g *execGuard) (Query, error) {
	args := NewArgs(d)
	var b strings.Builder
	st.WriteSQL(&b, args)
	if err := args.Err(); err != nil {
		return Query{}, err
	}
	return newQueryWith(g, b.String(), args.Values(), targets), nil
}

// ErrUnknownSlot is returned when a statement carries a clause for a slot
// that statement does not render.
//
// Why it is an error and not a shrug: a clause in an unrendered slot used to
// vanish, producing a statement that built cleanly and silently omitted what
// the caller asked for. That is the one failure shape this package exists to
// prevent, and it is reachable from outside the module, where a third-party
// dialect cannot add a slot of its own. Failing closed turns a missing clause
// into a message naming the slot.
var ErrUnknownSlot = errors.New("sqlb: clause asks for a slot this statement does not render")

// checkSlots fails the build for any clause the statement would drop. Each
// statement passes the slots it actually renders, in its own order.
func checkSlots[C AnyClause](args *Args, clauses map[Slot][]C, rendered ...Slot) {
	if len(clauses) == 0 {
		return
	}
	// Sorted, so a statement with two misplaced clauses names the same one
	// every run rather than whichever the map happened to yield first.
	unrendered := make([]Slot, 0, len(clauses))
	for slot, cs := range clauses {
		// No test reaches this and none can through the API: every entry in
		// the map is made by addClauses, which appends a clause as it creates
		// the key, so a slot present with no clauses does not occur. It stays
		// because the alternative is reporting ErrUnknownSlot for a slot
		// carrying nothing, and it would become reachable the moment anything
		// removed a clause from the map.
		if len(cs) == 0 {
			continue
		}
		found := false
		for _, r := range rendered {
			if r == slot {
				found = true
				break
			}
		}
		if !found {
			unrendered = append(unrendered, slot)
		}
	}
	if len(unrendered) == 0 {
		return
	}
	sort.Slice(unrendered, func(i, j int) bool { return unrendered[i] < unrendered[j] })
	args.Fail(fmt.Errorf("%w: %s", ErrUnknownSlot, unrendered[0]))
}

func writeSlot[C AnyClause](b *strings.Builder, args *Args, clauses map[Slot][]C, slot Slot) {
	for _, c := range clauses[slot] {
		b.WriteString(sepSpace)
		c.WriteSQL(b, args)
	}
}

// writeCTEs is shared by every statement kind.
func writeCTEs(b *strings.Builder, args *Args, ctes []CTE) {
	if len(ctes) == 0 {
		return
	}
	(&Select[render.Dialect]{selectCore: selectCore{ctes: ctes}}).writeCTEs(b, args)
}
