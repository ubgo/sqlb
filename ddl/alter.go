package ddl

import (
	"fmt"
	"slices"
	"strings"

	"github.com/ubgo/sqlb/expr"
	"github.com/ubgo/sqlb/render"
)

// Action is a universal ALTER TABLE action: one every engine writes the same
// way. There are three, adding, dropping and renaming a column; everything
// else an engine can alter is an AlterClause in its own package.
type Action interface {
	// ActionTable is the table the action's column belongs to, which the
	// statement checks against its own.
	ActionTable() expr.Table
	// Standalone reports whether the action must be a statement of its own.
	// In Postgres's grammar RENAME is a top-level form of ALTER TABLE, not one
	// of the actions a comma may join, so combining it is a syntax error.
	Standalone() bool
	WriteAction(b *strings.Builder, a *render.Args)
}

// AddColumn adds a column. On SQLite the definition is subject to that
// engine's limits on ADD COLUMN.
func AddColumn(c ColumnSpec) Action { return addColumn{c} }

// DropColumn drops a column.
func DropColumn[T any](c expr.Expr[T]) Action { return dropColumn{name: c.Name(), table: c.Table()} }

// RenameColumn renames a column. It takes the old column and the new one rather
// than two strings: the code that used the old name is the code that must
// change, and the new declaration is the one the queries will use. Both are of
// one Go type, because a rename does not change what the column stores.
func RenameColumn[T any](from, to expr.Expr[T]) Action {
	return renameColumn{from: from.Name(), to: to.Name(), table: from.Table(), toTable: to.Table()}
}

type addColumn struct{ c ColumnSpec }

func (x addColumn) ActionTable() expr.Table { return x.c.ColumnTable() }
func (addColumn) Standalone() bool          { return false }
func (x addColumn) WriteAction(b *strings.Builder, a *render.Args) {
	b.WriteString(kwAddColumn)
	b.WriteString(sepSpace)
	if !checkAddColumn(a, x.c, ColumnExtras{}) {
		return
	}
	x.c.WriteColumn(b, a, ColumnExtras{})
}

type dropColumn struct {
	name  string
	table expr.Table
}

func (x dropColumn) ActionTable() expr.Table { return x.table }
func (dropColumn) Standalone() bool          { return false }
func (x dropColumn) WriteAction(b *strings.Builder, a *render.Args) {
	if x.name == "" {
		a.Fail(ErrNotAColumn)
		return
	}
	b.WriteString(kwDropColumn)
	b.WriteString(sepSpace)
	b.WriteString(a.Dialect().QuoteIdent(x.name))
}

type renameColumn struct {
	from, to       string
	table, toTable expr.Table
}

func (x renameColumn) ActionTable() expr.Table { return x.table }
func (renameColumn) Standalone() bool          { return true }
func (x renameColumn) WriteAction(b *strings.Builder, a *render.Args) {
	if x.from == "" || x.to == "" {
		a.Fail(ErrNotAColumn)
		return
	}
	if !sameTable(x.table, x.toTable) {
		a.Fail(fmt.Errorf("%w: renaming %s to a column declared on %s", ErrForeignColumn, x.from, x.toTable.Name))
		return
	}
	d := a.Dialect()
	b.WriteString(kwRename)
	b.WriteString(sepSpace)
	b.WriteString(d.QuoteIdent(x.from))
	b.WriteString(sepSpace)
	b.WriteString(kwTo)
	b.WriteString(sepSpace)
	b.WriteString(d.QuoteIdent(x.to))
}

// TableNamer is implemented by an engine ALTER TABLE action that belongs to
// one table, one that adds a column declared on it, so the statement can
// refuse it on any other with ErrForeignColumn. An action that also names
// columns implements ColumnNamer, and each is checked the same way.
type TableNamer interface {
	ClauseTable() expr.Table
}

// alterItem is one action in the order it was added, universal or engine: the
// order is the order the statements run, so an engine action written before a
// universal one must not be moved after it.
type alterItem[D render.Dialect] struct {
	action Action
	clause AlterClause[D]
}

// alterCore is what ALTER TABLE and its complete form share.
type alterCore[D render.Dialect] struct {
	dialect D
	table   expr.Table
	items   []alterItem[D]
}

func (c alterCore[D]) derive() alterCore[D] {
	c.items = slices.Clone(c.items)
	return c
}

func (c alterCore[D]) withActions(as []Action) alterCore[D] {
	n := c.derive()
	for _, x := range as {
		n.items = append(n.items, alterItem[D]{action: x})
	}
	return n
}

func (c alterCore[D]) withClauses(cs []AlterClause[D]) alterCore[D] {
	n := c.derive()
	for _, x := range cs {
		n.items = append(n.items, alterItem[D]{clause: x})
	}
	return n
}

// AlterTable is an ALTER TABLE with no action yet. It has no Build, so an
// ALTER TABLE that does nothing does not compile.
type AlterTable[D render.Dialect] struct{ core alterCore[D] }

// AlterTableReady is an ALTER TABLE with at least one action, the only form
// with Build.
type AlterTableReady[D render.Dialect] struct{ core alterCore[D] }

// NewAlterTable starts an ALTER TABLE of t for d.
func NewAlterTable[D render.Dialect](d D, t expr.Table) *AlterTable[D] {
	return &AlterTable[D]{core: alterCore[D]{dialect: d, table: t}}
}

// Action adds universal actions, at least one, and so completes the statement.
func (s *AlterTable[D]) Action(first Action, more ...Action) *AlterTableReady[D] {
	return &AlterTableReady[D]{core: s.core.withActions(append([]Action{first}, more...))}
}

// Clause adds engine actions typed to this statement's dialect, at least one,
// and so completes the statement.
func (s *AlterTable[D]) Clause(first AlterClause[D], more ...AlterClause[D]) *AlterTableReady[D] {
	return &AlterTableReady[D]{core: s.core.withClauses(append([]AlterClause[D]{first}, more...))}
}

// Action adds universal actions.
func (s *AlterTableReady[D]) Action(as ...Action) *AlterTableReady[D] {
	return &AlterTableReady[D]{core: s.core.withActions(as)}
}

// Clause adds engine actions typed to this statement's dialect.
func (s *AlterTableReady[D]) Clause(cs ...AlterClause[D]) *AlterTableReady[D] {
	return &AlterTableReady[D]{core: s.core.withClauses(cs)}
}

// Build renders the actions in the order added, universal and engine alike. On a
// dialect with render.CombinedAlter, consecutive actions that may be joined
// share one statement, applied under one lock; a standalone action, RENAME
// among them, is always a statement of its own. On any other dialect, SQLite
// among them, every action is a statement, because a list is a syntax error
// there. The author's code is the same either way.
func (s *AlterTableReady[D]) Build() (Script, error) {
	if s.core.table.Name == "" || len(s.core.items) == 0 {
		return nil, ErrZeroValue
	}
	a, err := inlineArgs(s.core.dialect)
	if err != nil {
		return nil, err
	}
	var parts []rendered
	for _, it := range s.core.items {
		var b strings.Builder
		if act := it.action; act != nil {
			if !s.core.onTable(a, act.ActionTable()) {
				break
			}
			act.WriteAction(&b, a)
			parts = append(parts, rendered{sql: b.String(), standalone: act.Standalone()})
			continue
		}
		if !s.core.clauseOnTable(a, it.clause) {
			break
		}
		writeBare(&b, a, it.clause)
		parts = append(parts, rendered{sql: b.String(), standalone: it.clause.Standalone()})
	}
	if err := a.Err(); err != nil {
		return nil, err
	}
	head := kwAlter + sepSpace + kwTable + sepSpace + s.core.table.Qualified(a.Dialect()) + sepSpace
	combine := false
	// Go cannot type-assert a value of type parameter D directly; converting to
	// any is the one spelling of a capability check on a generic dialect.
	if c, ok := any(s.core.dialect).(render.CombinedAlter); ok {
		combine = c.SupportsCombinedAlter()
	}
	return groupActions(head, parts, combine), nil
}

// onTable fails Build for an action belonging to another table.
func (c alterCore[D]) onTable(a *render.Args, t expr.Table) bool {
	if sameTable(t, c.table) {
		return true
	}
	a.Fail(fmt.Errorf("%w: an action on %s in ALTER TABLE %s", ErrForeignColumn, t.Name, c.table.Name))
	return false
}

// clauseOnTable checks an engine action that says which table or columns it
// belongs to; one that says nothing, VALIDATE CONSTRAINT say, has no table to
// check.
func (c alterCore[D]) clauseOnTable(a *render.Args, cl AlterClause[D]) bool {
	if tn, ok := cl.(TableNamer); ok && tn.ClauseTable().Name != "" && !c.onTable(a, tn.ClauseTable()) {
		return false
	}
	if cn, ok := cl.(ColumnNamer); ok {
		for _, r := range cn.NamedColumns() {
			if r == nil || r.Name() == "" {
				a.Fail(ErrNotAColumn)
				return false
			}
			if !c.onTable(a, r.Table()) {
				return false
			}
		}
	}
	return true
}

// rendered is one action's text and whether it must stand alone.
type rendered struct {
	sql        string
	standalone bool
}

// groupActions turns actions into statements in their order. Where the dialect
// combines, consecutive actions that may be joined share one statement, and a
// standalone action ends the group and is a statement of its own; elsewhere
// every action is a statement.
func groupActions(head string, parts []rendered, combine bool) Script {
	var out Script
	var group []string
	flush := func() {
		if len(group) > 0 {
			out = append(out, Statement{SQL: head + strings.Join(group, sepComma), Transactional: true})
			group = nil
		}
	}
	for _, p := range parts {
		if !combine || p.standalone {
			flush()
			out = append(out, Statement{SQL: head + p.sql, Transactional: true})
			continue
		}
		group = append(group, p.sql)
	}
	flush()
	return out
}
