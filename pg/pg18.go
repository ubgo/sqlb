package pg

import (
	"errors"
	"strings"

	"github.com/ubgo/sqlb"
	"github.com/ubgo/sqlb/ddl"
)

// The Postgres 18 DDL: virtual generated columns, constraints that are
// declared but not enforced, the wider ALTER CONSTRAINT, and temporal keys,
// a primary or unique key WITHOUT OVERLAPS over a range and a foreign key that
// references a PERIOD. Each needs Postgres 18 or later; on 17 the server
// refuses the statement, and the execution tests skip there loudly.

const (
	kwVirtual         = "VIRTUAL"
	kwNotEnforced     = "NOT ENFORCED"
	kwEnforced        = "ENFORCED"
	kwAlterConstraint = "ALTER CONSTRAINT"
	kwNotDeferrable   = "NOT DEFERRABLE"
	kwInherit         = "INHERIT"
	kwNoInherit       = "NO INHERIT"
	kwWithoutOverlaps = "WITHOUT OVERLAPS"
	kwPeriod          = "PERIOD"
)

// GeneratedVirtual makes col a virtual generated column computed by e,
// evaluated when read rather than stored, which is Postgres 18's default for
// GENERATED ALWAYS AS. It takes no disk space and cannot be indexed; use
// GeneratedStored for one that must be. Postgres 18 or later.
func GeneratedVirtual[T any](col, e sqlb.Expr[T]) ddl.ColumnClause[Dialect] {
	return ddl.NewColumnClause[Dialect](col, ddl.PartComputed, func(a *sqlb.Args) string {
		return kwGeneratedAlwaysAs + " (" + e.SQL(a) + ") " + kwVirtual
	})
}

// NotEnforced declares the check without enforcing it: Postgres records it,
// but rows that break it are accepted. A check cannot be switched to enforced
// afterwards, measured on Postgres 18, which refuses ALTER CONSTRAINT ...
// ENFORCED on one; drop it and add it enforced instead. Postgres 18 or later.
func (c CheckClause) NotEnforced() CheckClause { c.notEnforced = true; return c }

// NotEnforced declares the foreign key without enforcing it, which is how a
// reference is introduced on data not yet clean: AlterConstraint(...).Enforced()
// later turns it on and checks every row. Postgres 18 or later.
func (f ForeignKeyClause) NotEnforced() ForeignKeyClause { f.notEnforced = true; return f }

// ErrConstraintAlterMix is returned for an ALTER CONSTRAINT that combines
// INHERIT or NO INHERIT with the other options: Postgres 18's grammar has them
// as two separate forms.
var ErrConstraintAlterMix = errors.New("sqlb/pg: ALTER CONSTRAINT takes INHERIT or NO INHERIT alone")

// AlterConstraintClause is ALTER CONSTRAINT on a declared constraint: its
// deferrability, whether it is enforced, or whether a NOT NULL constraint is
// inherited. It is an ALTER TABLE action, refused on another table's ALTER
// TABLE.
type AlterConstraintClause struct {
	con      TableConstraint
	deferral Deferral
	noDefer  bool
	enforced *bool
	inherit  *bool
}

// AlterConstraint starts an ALTER CONSTRAINT on c.
func AlterConstraint(c TableConstraint) AlterConstraintClause { return AlterConstraintClause{con: c} }

// Deferrable makes the constraint deferrable, checked when d says. Changing
// deferrability works for foreign keys on Postgres 17, and for the other
// constraint kinds on 18.
func (x AlterConstraintClause) Deferrable(d Deferral) AlterConstraintClause {
	x.deferral, x.noDefer = d, false
	return x
}

// NotDeferrable makes the constraint checked at once, the default.
func (x AlterConstraintClause) NotDeferrable() AlterConstraintClause {
	x.deferral, x.noDefer = 0, true
	return x
}

// Enforced turns an unenforced foreign key on, checking every row. Postgres
// 18 changes enforceability for foreign keys only and refuses it for a check,
// measured. Postgres 18 or later.
func (x AlterConstraintClause) Enforced() AlterConstraintClause { v := true; x.enforced = &v; return x }

// NotEnforced stops enforcing a foreign key; a check is refused, as for
// Enforced. Postgres 18 or later.
func (x AlterConstraintClause) NotEnforced() AlterConstraintClause {
	v := false
	x.enforced = &v
	return x
}

// Inherit makes a NOT NULL constraint apply to inheriting tables.
// Postgres 18 or later.
func (x AlterConstraintClause) Inherit() AlterConstraintClause { v := true; x.inherit = &v; return x }

// NoInherit stops a NOT NULL constraint applying to inheriting tables.
// Postgres 18 or later.
func (x AlterConstraintClause) NoInherit() AlterConstraintClause {
	v := false
	x.inherit = &v
	return x
}

// ClauseTable is the constraint's table, for ALTER TABLE's check.
func (x AlterConstraintClause) ClauseTable() sqlb.Table { return x.con.Table }

// WriteSQL renders the action.
func (x AlterConstraintClause) WriteSQL(b *strings.Builder, a *sqlb.Args) {
	if !x.con.Valid() {
		a.Fail(ddl.ErrZeroValue)
		return
	}
	var parts []string
	if x.inherit != nil {
		if x.deferral != 0 || x.noDefer || x.enforced != nil {
			a.Fail(ErrConstraintAlterMix)
			return
		}
		if *x.inherit {
			parts = append(parts, kwInherit)
		} else {
			parts = append(parts, kwNoInherit)
		}
	}
	switch {
	case x.noDefer:
		parts = append(parts, kwNotDeferrable)
	case x.deferral != 0:
		kw, ok := x.deferral.keyword()
		parts = append(parts, keywordOf(a, kw, ok, "Deferrable"))
	}
	if x.enforced != nil {
		if *x.enforced {
			parts = append(parts, kwEnforced)
		} else {
			parts = append(parts, kwNotEnforced)
		}
	}
	if len(parts) == 0 {
		a.Fail(ddl.ErrZeroValue)
		return
	}
	b.WriteString(kwAlterConstraint + " " + a.Dialect().QuoteIdent(x.con.Name) + " " + strings.Join(parts, " "))
}

// Standalone reports that the action may share a statement.
func (AlterConstraintClause) Standalone() bool { return false }

// AlterClauseOf marks the action as Postgres's.
func (AlterConstraintClause) AlterClauseOf(Dialect) {}

// PeriodColumn is a range column that a temporal key is over: the last column
// of a primary or unique key WITHOUT OVERLAPS, or the PERIOD of a foreign key.
// It is made by Period from a column of a range type, so a column of any other
// type does not compile there.
type PeriodColumn struct{ col ddl.ColumnRef }

// Period marks a range column for a temporal key.
func Period[T any](col sqlb.Expr[Range[T]]) PeriodColumn { return PeriodColumn{col: col} }

// WithoutOverlaps makes the key temporal: two rows may share the other key
// columns as long as their period ranges do not overlap, the key a booking or
// a price valid over an interval needs. The period is the key's last column.
// A GiST index backs the key, so scalar columns need the btree_gist extension.
// Postgres 18 or later.
func (k PrimaryKeyClause) WithoutOverlaps(p PeriodColumn) PrimaryKeyClause {
	k.cols = append(append([]ddl.ColumnRef(nil), k.cols...), p.col)
	k.overlaps = true
	return k
}

// WithoutOverlaps makes the unique key temporal; see
// PrimaryKeyClause.WithoutOverlaps. Postgres 18 or later.
func (u UniqueClause) WithoutOverlaps(p PeriodColumn) UniqueClause {
	u.cols = append(append([]ddl.ColumnRef(nil), u.cols...), p.col)
	u.overlaps = true
	return u
}

// Period makes the foreign key temporal: each row's period must be covered by
// the union of the referenced rows' periods, over a temporal key of the
// referenced table. local is this table's range column, ref the referenced
// table's. Postgres 18 or later.
func (f ForeignKeyClause) Period(local, ref PeriodColumn) ForeignKeyClause {
	f.pairs = append([]ColumnPair(nil), f.pairs...)
	f.period = &[2]ddl.ColumnRef{local.col, ref.col}
	return f
}

// quoteKeyCols writes a key's columns, the last marked WITHOUT OVERLAPS for a
// temporal key.
func quoteKeyCols(a *sqlb.Args, cols []ddl.ColumnRef, overlaps bool) string {
	s := quoteCols(a, cols)
	if !overlaps {
		return s
	}
	return strings.TrimSuffix(s, ")") + " " + kwWithoutOverlaps + ")"
}
