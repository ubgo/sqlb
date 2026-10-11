package pg

import (
	"slices"
	"strings"

	"github.com/ubgo/sqlb"
	"github.com/ubgo/sqlb/ddl"
)

// The ALTER TABLE forms the detector recorded as deliberately left out, and
// the one it recorded as partial.
//
// Three of them were left out for a reason that is about a mechanism rather
// than about this module: inheritance predates partitioning, rules predate
// triggers, and a typed table is rare. Those are good reasons not to reach for
// one and no reason at all for the statement to be unbuildable, because the
// library that has to write the migration is the library that inherited the
// schema — a table already sitting under a parent, or already carrying a rule
// an old release created, is exactly the case where the form is needed and the
// raw escape hatch was the only answer. Each one below therefore builds, and
// each doc comment says what to use instead when the schema is new.
//
// The partial form is the sequence behind an identity column. A declared
// pg.Sequence is altered with AlterSequence, but an identity's sequence has no
// declaration to name: Postgres makes it, owns it and drops it with the
// column. AlterIdentitySequence names it the only way there is, through the
// column, and takes the options in the column's own integer type.

const (
	kwOfType        = "OF"
	kwNotOfType     = "NOT OF"
	kwEnableRule    = "ENABLE RULE"
	kwDisableRule   = "DISABLE RULE"
	kwEnableReplica = "ENABLE REPLICA RULE"
	kwEnableAlways  = "ENABLE ALWAYS RULE"
)

// Inherit makes the table a child of parent, so a query on the parent also
// reads this table's rows and the child must already have every one of the
// parent's columns with the same types.
//
// Inheritance predates declarative partitioning, which replaces it for the one
// thing most people used it for, and a new schema should use CreatePartition
// instead: a partition is checked to be disjoint, is pruned by the planner and
// cannot be forgotten. This form exists for the schema that already inherits,
// where detaching a child is a migration somebody has to write.
func Inherit(parent sqlb.Table) AlterClause {
	return alterAction{standalone: true, sql: func(a *sqlb.Args) string {
		return inheritance(a, kwInherit, parent)
	}}
}

// NoInherit detaches the table from parent, leaving both tables and all their
// rows in place; the parent simply stops seeing this table's rows.
func NoInherit(parent sqlb.Table) AlterClause {
	return alterAction{standalone: true, sql: func(a *sqlb.Args) string {
		return inheritance(a, kwNoInherit, parent)
	}}
}

// inheritance writes an INHERIT or NO INHERIT naming a parent table.
func inheritance(a *sqlb.Args, kw string, parent sqlb.Table) string {
	if parent.Name == "" {
		a.Fail(ErrEmptyName)
		return ""
	}
	return kw + " " + parent.Qualified(a.Dialect())
}

// OfType makes the table a typed table of a composite type, so its column list
// is that type's attribute list and changing the type changes the table. It is
// the one form where a table's shape is declared somewhere other than the
// table, which is why a new schema should declare the columns instead; the
// table's columns must already match the type exactly, which Postgres checks.
func OfType[T any](c Composite[T]) AlterClause {
	return alterAction{standalone: true, sql: func(a *sqlb.Args) string {
		if c.Name == "" {
			a.Fail(ErrEmptyName)
			return ""
		}
		return kwOfType + " " + qualified(a.Dialect(), c.Schema, c.Name)
	}}
}

// NotOfType breaks the tie between a typed table and its composite type,
// leaving the table with the columns it has as its own declaration.
func NotOfType() AlterClause {
	return alterAction{standalone: true, sql: func(*sqlb.Args) string { return kwNotOfType }}
}

// Rule is a Postgres rewrite rule on one table, declared as a value so that
// the statements enabling and disabling one need not be told the name twice.
// Its name is unique per table, which is why the table is part of the value,
// exactly as a Trigger's and a Policy's are.
//
// This package creates no rule. A rule rewrites a statement before it runs,
// which means a caller's INSERT can silently become something else, and every
// use of one a trigger cannot serve is a use nobody should start today; the
// rules that exist are in schemas older than that advice, and their migrations
// are what these statements are for. The exception Postgres itself depends on,
// the rule behind a view, is created by CREATE VIEW and not by hand.
type Rule struct {
	Name  string
	Table sqlb.Table
}

// EnableRule turns a rule back on for ordinary statements, the default state
// of a rule that has been disabled.
func EnableRule(r Rule) AlterClause { return ruleAction(kwEnableRule, r) }

// DisableRule stops a rule rewriting anything, without dropping it. It is the
// safe first half of removing a rule: disable it, watch, then drop it, rather
// than dropping it and finding out what depended on the rewrite.
func DisableRule(r Rule) AlterClause { return ruleAction(kwDisableRule, r) }

// EnableReplicaRule turns the rule on only where session_replication_role is
// replica, which is how a rule applies on a subscriber and not on the origin.
func EnableReplicaRule(r Rule) AlterClause { return ruleAction(kwEnableReplica, r) }

// EnableAlwaysRule turns the rule on in every session_replication_role,
// replication included.
func EnableAlwaysRule(r Rule) AlterClause { return ruleAction(kwEnableAlways, r) }

// ruleAction writes one of the four rule actions, checking the rule names the
// table the statement is about, since a rule of another table would otherwise
// be reported by the server as a rule that does not exist.
func ruleAction(kw string, r Rule) AlterClause {
	return alterAction{table: r.Table, sql: func(a *sqlb.Args) string {
		if r.Name == "" {
			a.Fail(ErrEmptyName)
			return ""
		}
		return kw + " " + a.Dialect().QuoteIdent(r.Name)
	}}
}

// IdentitySequenceClause is the ALTER COLUMN action that changes the sequence
// behind an identity column.
type IdentitySequenceClause[T identityInteger] struct {
	col  sqlb.Expr[T]
	opts []func(a *sqlb.Args) string
}

// AlterIdentitySequence changes the sequence an identity column draws from,
// naming it through the column because it has no name of its own that a
// declaration could hold: Postgres creates it with the column, owns it and
// drops it with the column, so AlterSequence, which takes a declared
// pg.Sequence, has nothing to be handed.
//
// Every option is typed to the column's own integer type, so a bound that the
// column cannot hold does not compile. Restarting the sequence is
// RestartIdentity, which is its own action because Postgres writes RESTART
// without the SET each option here carries.
func AlterIdentitySequence[T identityInteger](col sqlb.Expr[T]) IdentitySequenceClause[T] {
	return IdentitySequenceClause[T]{col: col}
}

// with derives a clause carrying one more option, never writing to the
// receiver, so a base clause can be used twice.
func (x IdentitySequenceClause[T]) with(o func(a *sqlb.Args) string) IdentitySequenceClause[T] {
	x.opts = append(slices.Clone(x.opts), o)
	return x
}

// IncrementBy is how far the sequence moves per value; a negative step counts
// down.
func (x IdentitySequenceClause[T]) IncrementBy(n T) IdentitySequenceClause[T] {
	return x.with(identityOption(kwIncrementBy, n))
}

// MinValue is the lowest value the sequence may hand out.
func (x IdentitySequenceClause[T]) MinValue(n T) IdentitySequenceClause[T] {
	return x.with(identityOption(kwMinValue, n))
}

// MaxValue is the highest value the sequence may hand out, after which it
// fails unless Cycle is set.
func (x IdentitySequenceClause[T]) MaxValue(n T) IdentitySequenceClause[T] {
	return x.with(identityOption(kwMaxValue, n))
}

// StartWith is the value a later RESTART with no argument goes back to. It
// does not move the sequence now, which is RestartIdentity's job.
func (x IdentitySequenceClause[T]) StartWith(n T) IdentitySequenceClause[T] {
	return x.with(identityOption(kwStartWith, n))
}

// Cache is how many values the server hands a session at once. A cache above
// one leaves gaps whenever a session ends, which for an identity key is
// usually fine and is never fine where the key is expected to be dense.
func (x IdentitySequenceClause[T]) Cache(n int64) IdentitySequenceClause[T] {
	return x.with(func(a *sqlb.Args) string {
		if n < 1 {
			a.Fail(ErrBadCache)
			return ""
		}
		return kwSet + " " + kwCache + " " + a.Literal(n)
	})
}

// Cycle starts the sequence again at its minimum when it runs out, which for a
// key column means the next value collides with a row that is still there.
func (x IdentitySequenceClause[T]) Cycle() IdentitySequenceClause[T] {
	return x.with(func(*sqlb.Args) string { return kwSet + " " + kwCycle })
}

// NoCycle fails when the sequence runs out, the default.
func (x IdentitySequenceClause[T]) NoCycle() IdentitySequenceClause[T] {
	return x.with(func(*sqlb.Args) string { return kwSet + " " + kwNoCycle })
}

// identityOption writes one SET <option> <value>, the form Postgres takes for
// an identity column's sequence: each option carries its own SET, which is
// what lets several of them share one ALTER COLUMN. Measured on 17.10.
func identityOption[T identityInteger](kw string, n T) func(a *sqlb.Args) string {
	return func(a *sqlb.Args) string { return kwSet + " " + kw + " " + a.Literal(int64(n)) }
}

// WriteSQL renders the action.
func (x IdentitySequenceClause[T]) WriteSQL(b *strings.Builder, a *sqlb.Args) {
	if len(x.opts) == 0 {
		a.Fail(ddl.ErrZeroValue)
		return
	}
	onColumn(x.col, func(a *sqlb.Args) string {
		return strings.Join(renderOptions(a, x.opts), " ")
	}).WriteSQL(b, a)
}

// Standalone reports that the action may share a statement.
func (IdentitySequenceClause[T]) Standalone() bool { return false }

// AlterClauseOf marks the action as Postgres's.
func (IdentitySequenceClause[T]) AlterClauseOf(Dialect) {}

// NamedColumns is the identity column whose sequence is changed.
func (x IdentitySequenceClause[T]) NamedColumns() []ddl.ColumnRef {
	return []ddl.ColumnRef{x.col}
}
