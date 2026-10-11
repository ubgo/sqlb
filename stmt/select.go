package stmt

import (
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/ubgo/sqlb/expr"
	"github.com/ubgo/sqlb/render"
)

var (
	// ErrNoColumns is returned by a SELECT with no bindings and no
	// expressions. There is nothing to select.
	ErrNoColumns = errors.New("sqlb: select has no columns")

	// ErrJoinWithoutFrom is returned when a statement has joins but no
	// driving source to join them to.
	ErrJoinWithoutFrom = errors.New("sqlb: select has joins but no FROM")

	// ErrDuplicateAlias is returned when two sources in one statement share
	// an alias. Rendering it would produce a statement whose column
	// references are ambiguous, which the database reports far less clearly.
	ErrDuplicateAlias = errors.New("sqlb: duplicate table alias in one statement")

	// ErrNoTargets is returned when One or Each is given a query whose select
	// list has items with no scan target. Such a query was built with Exprs
	// and is meant for use as a subquery, not for execution.
	ErrNoTargets = errors.New("sqlb: query has select items without scan targets")
)

// Join brings a source into a statement. Table is the common case; Source
// covers a derived table, VALUES, a function or a LATERAL body, and wins when
// both are set. On is ANDed; an empty On renders no ON clause, which CROSS
// JOIN and LATERAL ... ON TRUE need, so give a CROSS or LATERAL join
// sqlb.True() when the dialect requires an ON.
type Join struct {
	Type   JoinType
	Table  Table
	Source Source
	On     []Predicate
	// Using renders JOIN ... USING (cols) instead of ON. Named columns only,
	// never NATURAL, because a natural join changes meaning when a column is
	// added.
	Using []string
	// Clauses render immediately after this join's table reference and before
	// its ON or USING, which is where an engine puts a per-table hint:
	// SQLite's INDEXED BY, MySQL's USE INDEX, T-SQL's WITH (NOLOCK).
	//
	// This is the one position a Slot cannot name, because there is one per
	// join rather than one per statement. A clause here is still a Clause, so
	// it still carries its dialect guard; its Slot is not consulted, because
	// the join itself is the position.
	Clauses []AnyClause
}

func (j Join) source() Source {
	if j.Source != nil {
		return j.Source
	}
	return j.Table
}

// selectItem is one entry of the select list. target is nil for an item added
// through Exprs, which is what a subquery or a CTE body uses.
type selectItem struct {
	expr   func(a *Args) string
	target any
}

// CTE is one common table expression. Columns renders the optional column
// list. Prefix renders between AS and the body, for engine words such as
// MATERIALIZED; the core renders it and an engine package decides it.
type CTE struct {
	Name    string
	Columns []string
	Body    Statement
	Prefix  SQLer
	// Suffix renders after the body's closing parenthesis, for the SEARCH
	// and CYCLE clauses of a recursive CTE.
	Suffix    SQLer
	recursive bool
}

type setOp struct {
	kind  string
	other scopedStatement
}

// scopedStatement is what a set-operation branch and a recursive seed need:
// a statement that can say whether it needs a scope of its own, and how wide
// its own select list is so the branches can be compared before the engine
// sees them.
type scopedStatement interface {
	Statement
	scoped() bool
	compound() bool
	selectWidth() int
}

type namedWindow struct {
	name string
	win  Window
}

// Select accumulates a statement. Every method appends; nothing removes. That
// is what lets a library hand a Select through Apply to an application and
// still trust that its own predicates survive.
type selectCore struct {
	ctes     []CTE
	distinct bool
	items    []selectItem
	from     Source
	joins    []Join
	where    []Predicate
	groupBy  []SQLer
	having   []Predicate
	windows  []namedWindow
	setOps   []setOp
	order    []OrderTerm
	limit    int
	offset   int
}

// Select is a SELECT for the dialect D.
//
// D is inferred from NewSelect's argument and never written: a concrete
// dialect gives a statement that accepts that engine's clauses, and a
// render.Dialect interface value — the engine chosen at runtime — gives
// Select[render.Dialect], which accepts only universal constructs, because no
// engine clause is a Clause[render.Dialect, SelectKind]. That is the
// portability rule as a type rather than a convention. AnySelect names it.
//
// The universal parts live in the embedded selectCore, which is what an
// Option edits; the dialect-typed parts — the clauses and the dialect itself —
// live here.
type Select[D render.Dialect] struct {
	selectCore
	guard   *execGuard
	dialect D
	clauses map[Slot][]Clause[D, SelectKind]
}

// AnySelect is a Select whose engine is only known at runtime. It accepts
// every universal construct and no engine clause, which is exactly what a
// portable library should be building.
type AnySelect = Select[render.Dialect]

// NewSelect starts a statement for d.
//
// The execution guard is created here rather than at Build, and that is
// load-bearing now that every method returns a derived statement. Two
// statements derived from one base still share their bindings, which are
// pointers into the caller's variables, so they must share a guard: creating
// it lazily would have given each derivation its own and quietly allowed the
// overlapping execution the guard exists to refuse. A Select made as a struct
// literal still has no guard and still gets no check, which
// TestQuery_LiteralHasNoGuard pins.
func NewSelect[D render.Dialect](d D) *Select[D] {
	return &Select[D]{dialect: d, guard: &execGuard{}, selectCore: selectCore{limit: noLimit, offset: noOffset}}
}

// noLimit and noOffset mark "not set". Zero is a legal LIMIT, so a sentinel
// below zero is needed.
const (
	noLimit  = -1
	noOffset = -1
)

// Dialect returns the dialect the statement renders for.
func (s *Select[D]) Dialect() Dialect { return s.dialect }

// Columns adds bindings: select items with scan targets.
func (s *Select[D]) Columns(bs ...Binding) *Select[D] {
	c := s.derive()
	for _, b := range bs {
		c.items = append(c.items, selectItem{expr: b.SQL, target: b.Target()})
	}
	return c
}

// Exprs adds select items without scan targets. A query with such items
// cannot be run through One or Each (ErrNoTargets); it is for a subquery, a
// CTE body, an INSERT ... SELECT, or a caller that scans on its own.
func (s *Select[D]) Exprs(es ...SQLer) *Select[D] {
	c := s.derive()
	for _, e := range es {
		c.items = append(c.items, selectItem{expr: e.SQL})
	}
	return c
}

// Distinct adds DISTINCT. For DISTINCT ON use the pg package.
func (s *Select[D]) Distinct() *Select[D] { c := s.derive(); c.distinct = true; return c }

// From sets the driving source. Table satisfies Source.
func (s *Select[D]) From(src Source) *Select[D] { c := s.derive(); c.from = src; return c }

// Join adds joins, in order. Accumulates across calls, so a library and its
// caller can each contribute one.
func (s *Select[D]) Join(js ...Join) *Select[D] {
	c := s.derive()
	c.joins = append(c.joins, js...)
	return c
}

// Where adds predicates, joined with AND. Accumulates across calls, which is
// what lets a caller narrow a query a library built.
func (s *Select[D]) Where(ps ...Predicate) *Select[D] {
	c := s.derive()
	c.where = append(c.where, ps...)
	return c
}

// Order adds ordering terms, in order of significance.
func (s *Select[D]) Order(ts ...OrderTerm) *Select[D] {
	c := s.derive()
	c.order = append(c.order, ts...)
	return c
}

// Limit sets the row limit, which binds as a parameter rather than being
// rendered into the statement.
func (s *Select[D]) Limit(n int) *Select[D] { c := s.derive(); c.limit = n; return c }

// Offset sets the row offset, also bound. Paging far into a large result
// with OFFSET is slow on every engine; a keyset predicate in Where is the
// alternative.
func (s *Select[D]) Offset(n int) *Select[D] { c := s.derive(); c.offset = n; return c }

// GroupBy adds grouping expressions. Standard SQL, so it is core. Mixed Go
// types are fine, which is why it takes SQLer. GroupingSets, Cube and Rollup
// are SQLers too.
func (s *Select[D]) GroupBy(es ...SQLer) *Select[D] {
	c := s.derive()
	c.groupBy = append(c.groupBy, es...)
	return c
}

// Having adds HAVING predicates, ANDed.
func (s *Select[D]) Having(ps ...Predicate) *Select[D] {
	c := s.derive()
	c.having = append(c.having, ps...)
	return c
}

// Window declares a named window for OverNamed.
func (s *Select[D]) Window(name string, w Window) *Select[D] {
	c := s.derive()
	c.windows = append(c.windows, namedWindow{name, w})
	return c
}

// With adds a common table expression. A later CTE may reference an earlier
// one. Reference the CTE as a source with Table{Name: name, Alias: alias}.
func (s *Select[D]) With(name string, body Statement) *Select[D] {
	c := s.derive()
	c.ctes = append(c.ctes, CTE{Name: name, Body: body})
	return c
}

// WithCTE adds a fully specified CTE, for a column list or an engine prefix.
func (s *Select[D]) WithCTE(cte CTE) *Select[D] {
	c := s.derive()
	c.ctes = append(c.ctes, cte)
	return c
}

// WithRecursive adds WITH RECURSIVE name AS (seed UNION ALL step). The step
// references name as a source; the seed and the step must select the same
// number of items, which the database checks.
func (s *Select[D]) WithRecursive(name string, seed, step *Select[D]) *Select[D] {
	c := s.derive()
	c.ctes = append(c.ctes, CTE{Name: name, Body: recursiveBody[D]{seed, step}, recursive: true})
	return c
}

// WithRecursiveCTE adds a fully specified recursive CTE: use it for a
// UNION (not UNION ALL) step, a column list, or a SEARCH or CYCLE suffix.
func (s *Select[D]) WithRecursiveCTE(cte CTE) *Select[D] {
	cte.recursive = true
	c := s.derive()
	c.ctes = append(c.ctes, cte)
	return c
}

// Union renders UNION, which removes duplicate rows.
//
// Each branch of a set operation renders parenthesised, so a branch may carry
// its own ORDER BY and LIMIT. The receiver's ORDER BY, LIMIT and OFFSET apply
// to the whole compound, as in SQL, and an ORDER BY over a compound must name
// an output column: use OutputCol, not a table column.
func (s *Select[D]) Union(o *Select[D]) *Select[D] { return s.SetOp(kwUnion, o) }

// UnionAll renders UNION ALL, which keeps duplicates and does not sort.
func (s *Select[D]) UnionAll(o *Select[D]) *Select[D] { return s.SetOp(kwUnionAll, o) }

// Intersect renders INTERSECT, the rows in both branches.
func (s *Select[D]) Intersect(o *Select[D]) *Select[D] { return s.SetOp(kwIntersect, o) }

// Except renders EXCEPT, the rows in the receiver and not in the argument.
func (s *Select[D]) Except(o *Select[D]) *Select[D] { return s.SetOp(kwExcept, o) }

// ErrSetOpArity reports that the branches of a set operation select different
// numbers of columns, which every engine refuses.
//
// Build fails rather than the server, because the message a caller gets back
// otherwise describes SQL they did not write: scan.All and exec's binding
// append the reader's columns to the statement they are given, which is the
// left branch, so a left branch that also declares its own select list ends up
// one column wider than the right. The engine then reports a mismatch against
// a query the caller never typed.
var ErrSetOpArity = errors.New("sqlb: set operation branches select different numbers of columns")

// SetOp adds a set operation with the given keyword. Engine packages use it
// for INTERSECT ALL and EXCEPT ALL, which SQLite lacks.
//
// The keyword is a string rather than a closed type for the reason JoinType is
// open: INTERSECT ALL is absent from this module's constants because SQLite
// has no such thing, and naming it here would ship a constant that works on
// three engines and fails on the fourth. So the set is the caller's.
//
// The text is not. It is written between the two branches verbatim and cannot
// be quoted, so it is checked for the shape of a keyword — letters and single
// spaces. Until 2026-09-24 it was unchecked, and this doc comment said only
// who used it, which is the failure that hid it: an escape hatch nobody has
// written down as one reads like an ordinary argument.
func (s *Select[D]) SetOp(keyword string, o *Select[D]) *Select[D] {
	c := s.derive()
	c.setOps = append(c.setOps, setOp{keyword, o})
	return c
}

// Clause adds engine clauses. Each is written in its slot, in insertion order
// within the slot.
func (s *Select[D]) Clause(cs ...Clause[D, SelectKind]) *Select[D] {
	out := s.derive()
	if out.clauses == nil {
		out.clauses = make(map[Slot][]Clause[D, SelectKind])
	}
	for _, c := range cs {
		out.clauses[c.Slot()] = append(out.clauses[c.Slot()], c)
	}
	return out
}

// Query is a rendered statement: SQL text, the bound values in placeholder
// order, and the scan targets in select-list order. Targets is nil when any
// select item has no target.
//
// A Query is NOT SAFE FOR CONCURRENT USE. Executing one from two goroutines
// is a data race: it reads like inert data and is not, because Targets holds
// pointers into the caller's variables, so both write to the same memory and
// interleave each other's rows. Go's race detector reports it as a data race
// on those variables. Build the query where its destination lives, per call, which is what
// the cookbook recipes do. Reusing one sequentially is fine.
//
// One and Each refuse an overlapping execution with ErrQueryInUse rather than
// corrupting the result, for a Query made by a builder or by NewQuery. A
// Query assembled as a struct literal carries no guard and gets no check.
type Query struct {
	SQL     string
	Args    []any
	Targets []any

	// guard detects overlapping execution. It is nil for a Query built as a
	// struct literal, which is why the check is a best effort rather than a
	// guarantee; see ErrQueryInUse.
	guard *execGuard
}

// Build renders the statement. Every check fails closed; the builder spec
// lists the order.
func (s *Select[D]) Build() (Query, error) {
	args := NewArgs(s.dialect)
	var b strings.Builder
	s.WriteSQL(&b, args)
	if err := args.Err(); err != nil {
		return Query{}, err
	}
	if s.guard == nil {
		s.guard = &execGuard{}
	}
	return newQueryWith(s.guard, b.String(), args.Values(), s.targets()), nil
}

func (s *Select[D]) targets() []any {
	out := make([]any, 0, len(s.items))
	for _, it := range s.items {
		if it.target == nil {
			return nil
		}
		out = append(out, it.target)
	}
	return out
}

// WriteSQL renders into a parent's builder and Args, so a Select is a
// Statement: usable as a subquery, a CTE body or an INSERT source with one
// shared placeholder numbering.
func (s *Select[D]) WriteSQL(b *strings.Builder, args *Args) {
	if len(s.items) == 0 {
		args.Fail(ErrNoColumns)
		return
	}
	if err := s.checkAliases(); err != nil {
		args.Fail(err)
		return
	}

	s.writeCTEs(b, args)

	if len(s.setOps) > 0 {
		// A branch carrying its own ORDER BY, LIMIT or OFFSET, or a compound
		// of its own, needs its own scope. Without one those clauses attach to
		// the whole set operation instead of to the branch that declared them,
		// which is valid SQL answering a different question. See
		// TestSetOp_BranchWithLimit. The scope is a derived table, not
		// parentheses: SQLite rejects a parenthesised compound member, which
		// the conformance suite found on its first run there, and a derived
		// table means the same on every engine. A plain branch is written
		// bare, which all of them accept.
		if !s.checkSetOpArity(args) {
			return
		}
		s.writeBody(b, args)
		for _, op := range s.setOps {
			b.WriteString(sepSpace)
			b.WriteString(expr.CheckedKeyword(args, op.kind, "set operation"))
			b.WriteString(sepSpace)
			writeBranch(b, args, op.other)
		}
	} else {
		s.writeBody(b, args)
	}

	s.writeTail(b, args)

	s.writeSlot(b, args, SlotAfterOffset)
	s.writeSlot(b, args, SlotTail)

	checkSlots(args, s.clauses,
		SlotAfterSelect, SlotAfterColumns, SlotAfterFrom, SlotBeforeWhere,
		SlotAfterWhere, SlotAfterGroupBy, SlotAfterHaving, SlotBeforeOrder,
		SlotAfterOrder, SlotAfterOffset, SlotTail)
}

func (s *Select[D]) writeCTEs(b *strings.Builder, args *Args) {
	if len(s.ctes) == 0 {
		return
	}
	d := args.Dialect()
	b.WriteString(kwWith)
	for _, c := range s.ctes {
		if c.recursive {
			b.WriteString(sepSpace)
			b.WriteString(kwRecursive)
			break
		}
	}
	for i, c := range s.ctes {
		if i > 0 {
			b.WriteString(",")
		}
		b.WriteString(sepSpace)
		b.WriteString(d.QuoteIdent(c.Name))
		if len(c.Columns) > 0 {
			b.WriteString(" (")
			b.WriteString(QuoteList(d, c.Columns))
			b.WriteString(")")
		}
		b.WriteString(sepSpace)
		b.WriteString(kwAs)
		if c.Prefix != nil {
			b.WriteString(sepSpace)
			b.WriteString(c.Prefix.SQL(args))
		}
		b.WriteString(" (")
		c.Body.WriteSQL(b, args)
		b.WriteString(")")
		if c.Suffix != nil {
			b.WriteString(sepSpace)
			b.WriteString(c.Suffix.SQL(args))
		}
	}
	b.WriteString(sepSpace)
}

// writeBody renders SELECT ... HAVING ... WINDOW, the part a set operation
// combines.

// scoped reports whether this statement carries clauses that must stay inside
// its own parentheses when it is a branch of a set operation. ORDER BY, LIMIT
// and OFFSET all attach to the whole set operation otherwise.

// recursiveBody renders the seed and step of a recursive CTE.
//
// It exists rather than calling seed.UnionAll(step) for two reasons, both of
// which were bugs.
//
// UnionAll mutates its receiver, so the caller's own seed value would gain a
// UNION ALL branch and render differently everywhere else it was used.
//
// And a clause on the receiver of a set operation belongs to the compound, not
// to the branch, which is the right rule everywhere else and the wrong one
// here: a LIMIT on the seed of a recursive CTE is unambiguously the seed's,
// and promoting it to the compound would cap the whole recursion instead of
// the starting rows. So the seed is parenthesised when it carries one.
//
// This is ent issue 2167, where the same clauses attached to the wrong query.
type recursiveBody[D render.Dialect] struct{ seed, step *Select[D] }

func (r recursiveBody[D]) WriteSQL(b *strings.Builder, a *Args) {
	writeBranch(b, a, r.seed)
	b.WriteString(sepSpace)
	b.WriteString(kwUnionAll)
	b.WriteString(sepSpace)
	// The step is written bare even when scoped: wrapping it in a derived
	// table would put its reference to the CTE inside a subquery, which
	// Postgres refuses for a recursive reference.
	r.step.WriteSQL(b, a)
}

// setOpScope is the alias of the derived table a scoped set-op branch is
// wrapped in. Postgres before 16 and MySQL require a derived table to have one.
const setOpScope = "sqlb_setop"

// writeBranch writes a set-operation branch: bare when it has nothing of its
// own to scope, and as SELECT * FROM (branch) AS "sqlb_setop" when it does.
func writeBranch(b *strings.Builder, a *Args, s scopedStatement) {
	if !s.scoped() && !s.compound() {
		s.WriteSQL(b, a)
		return
	}
	b.WriteString("SELECT * FROM (")
	s.WriteSQL(b, a)
	b.WriteString(") AS ")
	b.WriteString(a.Dialect().QuoteIdent(setOpScope))
}

// trailingSlots are the slots rendered after a statement's body. A clause in
// one belongs to the whole statement when written bare after a compound, so a
// branch holding one needs a scope, exactly as one holding ORDER BY does.
var trailingSlots = []Slot{SlotBeforeOrder, SlotAfterOrder, SlotAfterOffset, SlotTail}

// scoped reports whether the statement carries anything that would attach to
// an enclosing set operation if the statement were written bare inside it.
func (s *Select[D]) scoped() bool {
	if len(s.order) > 0 || s.limit >= 0 || s.offset >= 0 {
		return true
	}
	for _, sl := range trailingSlots {
		if len(s.clauses[sl]) > 0 {
			return true
		}
	}
	return false
}

// compound reports whether the statement is itself a set operation, which as
// a branch of another needs a scope so the operators do not reassociate:
// A EXCEPT (B UNION C) is not A EXCEPT B UNION C.
// checkSetOpArity fails the build when two branches declare select lists of
// different widths.
//
// A zero-width branch is skipped rather than compared, because a width this
// statement does not know is not a width that disagrees. No such branch is
// reachable today: a statement with no bindings and no expressions fails with
// ErrNoColumns before this runs, which TestSetOp_BranchesMustAgreeInWidth
// pins. The skip stays because the day a branch may render SELECT * — a
// bindingless reader, a raw source — comparing zero against two would refuse
// a legitimate query.
func (s *Select[D]) checkSetOpArity(args *render.Args) bool {
	want := len(s.items)
	for _, op := range s.setOps {
		got := op.other.selectWidth()
		if want == 0 || got == 0 || got == want {
			continue
		}
		args.Fail(fmt.Errorf("%w: %d and %d", ErrSetOpArity, want, got))
		return false
	}
	return true
}

func (s *Select[D]) compound() bool { return len(s.setOps) > 0 }

// selectWidth is how many items this statement's own select list declares,
// counting bindings and expressions alike, since both are columns the engine
// returns. Zero means undeclared rather than empty; see checkSetOpArity.
func (s *Select[D]) selectWidth() int { return len(s.items) }

// writeTail renders the trailing clauses: the order slots, ORDER BY, LIMIT and
// OFFSET. It is a method rather than inline code because a set-op branch that
// declares them renders them inside its own parentheses.
func (s *Select[D]) writeTail(b *strings.Builder, args *Args) {
	s.writeSlot(b, args, SlotBeforeOrder)

	if len(s.order) > 0 {
		b.WriteString(sepSpace)
		b.WriteString(kwOrderBy)
		b.WriteString(sepSpace)
		for i, t := range s.order {
			if i > 0 {
				b.WriteString(sepComma)
			}
			t.WriteSQL(b, args)
		}
	}

	s.writeSlot(b, args, SlotAfterOrder)

	// LIMIT and OFFSET are bound, not interpolated. They are under the
	// library's control today and request-controlled the day an admin
	// endpoint exists.
	if s.limit >= 0 {
		b.WriteString(sepSpace)
		b.WriteString(kwLimit)
		b.WriteString(sepSpace)
		b.WriteString(args.Next(s.limit))
	}
	if s.offset >= 0 {
		b.WriteString(sepSpace)
		b.WriteString(kwOffset)
		b.WriteString(sepSpace)
		b.WriteString(args.Next(s.offset))
	}
}

func (s *Select[D]) writeBody(b *strings.Builder, args *Args) {
	b.WriteString(kwSelect)
	if s.distinct {
		b.WriteString(sepSpace)
		b.WriteString(kwDistinct)
	}
	s.writeSlot(b, args, SlotAfterSelect)
	b.WriteString(sepSpace)
	for i, it := range s.items {
		if i > 0 {
			b.WriteString(sepComma)
		}
		b.WriteString(it.expr(args))
	}

	// FROM is optional: every target engine accepts "SELECT expr" with no
	// source, which is how a constant or a function is evaluated.
	s.writeSlot(b, args, SlotAfterColumns)

	if s.from != nil {
		b.WriteString(sepSpace)
		b.WriteString(kwFrom)
		b.WriteString(sepSpace)
		b.WriteString(s.from.SourceSQL(args))
		s.writeSlot(b, args, SlotAfterFrom)
	}

	for _, j := range s.joins {
		b.WriteString(sepSpace)
		b.WriteString(expr.CheckedKeyword(args, string(j.Type), "join type"))
		b.WriteString(sepSpace)
		b.WriteString(j.source().SourceSQL(args))
		for _, c := range j.Clauses {
			b.WriteString(sepSpace)
			c.WriteSQL(b, args)
		}
		if len(j.Using) > 0 {
			b.WriteString(sepSpace)
			b.WriteString(kwUsing)
			b.WriteString(" (")
			for i, c := range j.Using {
				if i > 0 {
					b.WriteString(sepComma)
				}
				b.WriteString(args.Dialect().QuoteIdent(c))
			}
			b.WriteString(")")
		} else if len(j.On) > 0 {
			b.WriteString(sepSpace)
			b.WriteString(kwOn)
			b.WriteString(sepSpace)
			And(j.On...).WriteSQL(b, args)
		}
	}

	s.writeSlot(b, args, SlotBeforeWhere)

	if len(s.where) > 0 {
		b.WriteString(sepSpace)
		b.WriteString(kwWhere)
		b.WriteString(sepSpace)
		And(s.where...).WriteSQL(b, args)
	}

	s.writeSlot(b, args, SlotAfterWhere)

	if len(s.groupBy) > 0 {
		b.WriteString(sepSpace)
		b.WriteString(kwGroupBy)
		b.WriteString(sepSpace)
		for i, e := range s.groupBy {
			if i > 0 {
				b.WriteString(sepComma)
			}
			b.WriteString(e.SQL(args))
		}
	}

	s.writeSlot(b, args, SlotAfterGroupBy)

	if len(s.having) > 0 {
		b.WriteString(sepSpace)
		b.WriteString(kwHaving)
		b.WriteString(sepSpace)
		And(s.having...).WriteSQL(b, args)
	}

	s.writeSlot(b, args, SlotAfterHaving)

	if len(s.windows) > 0 {
		b.WriteString(sepSpace)
		b.WriteString(kwWindow)
		b.WriteString(sepSpace)
		for i, w := range s.windows {
			if i > 0 {
				b.WriteString(sepComma)
			}
			b.WriteString(args.Dialect().QuoteIdent(w.name))
			b.WriteString(sepSpace)
			b.WriteString(kwAs)
			b.WriteString(" (")
			w.win.WriteSQL(b, args)
			b.WriteString(")")
		}
	}
}

func (s *Select[D]) writeSlot(b *strings.Builder, args *Args, slot Slot) {
	for _, c := range s.clauses[slot] {
		b.WriteString(sepSpace)
		c.WriteSQL(b, args)
	}
}

func (s *Select[D]) checkAliases() error {
	if s.from == nil {
		if len(s.joins) > 0 {
			return ErrJoinWithoutFrom
		}
		return nil
	}
	seen := make(map[string]string, len(s.joins)+1)
	seen[s.from.SourceAlias()] = sourceName(s.from)
	for _, j := range s.joins {
		src := j.source()
		alias := src.SourceAlias()
		if prev, dup := seen[alias]; dup {
			return errors.Join(ErrDuplicateAlias,
				errors.New("alias "+strconv.Quote(alias)+" used by both "+
					strconv.Quote(prev)+" and "+strconv.Quote(sourceName(src))))
		}
		seen[alias] = sourceName(src)
	}
	return nil
}

// sourceName is the physical name for a Table and the alias for anything
// else, for error messages.
func sourceName(src Source) string {
	if t, ok := src.(Table); ok {
		return t.Name
	}
	return src.SourceAlias()
}
