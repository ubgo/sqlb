package sqlite

import (
	"fmt"
	"slices"
	"strings"

	"github.com/ubgo/sqlb"
)

// Conflict resolution.
//
// SQLite has two mechanisms and they are not interchangeable. OR REPLACE and
// its siblings sit between INSERT and INTO and act on any constraint
// violation. ON CONFLICT targets one unique index and is the upsert that
// Postgres also has. The difference that matters: OR REPLACE deletes the
// conflicting row and inserts a new one, so delete triggers fire, the rowid
// changes, and any row referencing it by rowid is now wrong. DO UPDATE
// modifies the existing row in place.

// OrAction is the conflict-resolution algorithm of an INSERT OR clause.
type OrAction string

// The five algorithms SQLite accepts, in the order the documentation lists
// them. ROLLBACK aborts the whole transaction; ABORT aborts the statement and
// keeps earlier changes; FAIL stops at the failing row and keeps the rows
// already inserted; IGNORE skips the row and carries on; REPLACE deletes the
// conflicting row first.
const (
	OrRollback OrAction = "ROLLBACK"
	OrAbort    OrAction = "ABORT"
	OrFail     OrAction = "FAIL"
	OrIgnore   OrAction = "IGNORE"
	OrReplace  OrAction = "REPLACE"
)

// valid reports whether the action is one of the five constants above.
//
// OrAction is a named string rather than an int, so a value outside the set is
// writable and used to be rendered into the statement verbatim — the one place
// in this package where a closed set was not closed. Every site that renders
// one now asks this first and fails the build instead.
func (a OrAction) valid() bool {
	switch a {
	case OrRollback, OrAbort, OrFail, OrIgnore, OrReplace:
		return true
	}
	return false
}

// InsertOr renders INSERT OR <action> INTO, the clause between the keyword
// and the table.
//
// Prefer OnConflict where it fits. OR IGNORE hides every constraint violation
// including the ones that are bugs, and OR REPLACE changes the rowid, which
// silently breaks anything referencing the row by it.
func InsertOr(action OrAction) InsertClause {
	return insertClause(sqlb.SlotAfterInsert, constructWrite, func(a *sqlb.Args) string {
		if !action.valid() {
			a.Fail(fmt.Errorf("%w: OrAction %q", ErrUnknownKeyword, action))
			return ""
		}
		return "OR " + string(action)
	})
}

// Conflict accumulates an ON CONFLICT clause. Build it with OnConflict, then
// DoNothing or DoUpdate.
//
// SQLite spells this exactly as Postgres does, and it is still here rather
// than in the core: two engines agreeing is not the rule, and MySQL spells
// the same idea ON DUPLICATE KEY UPDATE.
type Conflict struct {
	columns     []sqlb.SQLer
	targetWhere []sqlb.Predicate
	update      []sqlb.Assignment
	updateWhere []sqlb.Predicate
	nothing     bool
}

// OnConflict targets the given columns, which must be a unique index. No
// columns means any conflict, which SQLite accepts only with DoNothing.
func OnConflict(cols ...sqlb.SQLer) *Conflict {
	return &Conflict{columns: append([]sqlb.SQLer(nil), cols...)}
}

// derive copies the clause so a method leaves its receiver alone. Until
// 2026-09-24 the methods below appended to the receiver and returned it, so a
// *Conflict held and used twice accumulated both uses — the same silent wrong
// answer the statement builders were fixed for, and invisible to the gate that
// pins them, because that gate walks types with a Build method and a clause
// builder has none.
func (c *Conflict) derive() *Conflict {
	d := *c
	d.columns = slices.Clone(c.columns)
	d.targetWhere = slices.Clone(c.targetWhere)
	d.update = slices.Clone(c.update)
	d.updateWhere = slices.Clone(c.updateWhere)
	return &d
}

// TargetWhere adds the WHERE of a partial unique index to the target, so the
// clause names the same index the schema declares.
func (c *Conflict) TargetWhere(ps ...sqlb.Predicate) *Conflict {
	d := c.derive()
	d.targetWhere = append(d.targetWhere, ps...)
	return d
}

// DoNothing renders DO NOTHING: the conflicting row is skipped.
func (c *Conflict) DoNothing() InsertClause {
	d := c.derive()
	d.nothing = true
	return d.clause()
}

// DoUpdate renders DO UPDATE SET. Use Excluded to read the row that would
// have been inserted.
func (c *Conflict) DoUpdate(as ...sqlb.Assignment) *Conflict {
	d := c.derive()
	d.update = append(d.update, as...)
	return d
}

// UpdateWhere adds a condition to the DO UPDATE, so a row is only modified
// when it is worth modifying.
func (c *Conflict) UpdateWhere(ps ...sqlb.Predicate) *Conflict {
	d := c.derive()
	d.updateWhere = append(d.updateWhere, ps...)
	return d
}

// Clause finishes a DoUpdate chain.
func (c *Conflict) Clause() InsertClause { return c.derive().clause() }

func (c *Conflict) clause() InsertClause {
	return insertClause(sqlb.SlotTail, constructWrite, func(a *sqlb.Args) string {
		d := a.Dialect()
		var b strings.Builder
		b.WriteString("ON CONFLICT")
		if len(c.columns) > 0 {
			b.WriteString(" (")
			for i, col := range c.columns {
				if i > 0 {
					b.WriteString(", ")
				}
				// A conflict target names a bare column, never an alias.
				if n, ok := col.(interface{ Name() string }); ok && n.Name() != "" {
					b.WriteString(d.QuoteIdent(n.Name()))
				} else {
					b.WriteString(col.SQL(a))
				}
			}
			b.WriteString(")")
			if len(c.targetWhere) > 0 {
				b.WriteString(" WHERE ")
				b.WriteString(sqlb.PredicateSQL(sqlb.And(c.targetWhere...), a))
			}
		}
		if c.nothing || len(c.update) == 0 {
			b.WriteString(" DO NOTHING")
			return b.String()
		}
		b.WriteString(" DO UPDATE SET ")
		for i, as := range c.update {
			if i > 0 {
				b.WriteString(", ")
			}
			b.WriteString(as.SQL(a))
		}
		if len(c.updateWhere) > 0 {
			b.WriteString(" WHERE ")
			b.WriteString(sqlb.PredicateSQL(sqlb.And(c.updateWhere...), a))
		}
		return b.String()
	})
}

// Excluded references the row that would have been inserted, inside a
// DO UPDATE. SQLite names it excluded, as Postgres does.
func Excluded[T any](col sqlb.Expr[T]) sqlb.Expr[T] {
	return expr[T](constructWrite, func(a *sqlb.Args) string {
		return "excluded." + a.Dialect().QuoteIdent(col.Name())
	})
}
