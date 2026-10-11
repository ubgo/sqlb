package pg

import (
	"errors"
	"strings"

	"github.com/ubgo/sqlb"
	"github.com/ubgo/sqlb/ddl"
)

const (
	constructOnConflict = "pg.OnConflict"

	kwOnConflict   = "ON CONFLICT"
	kwOnConstraint = "ON CONSTRAINT"
	kwDoNothing    = "DO NOTHING"
	kwDoUpdateSet  = "DO UPDATE SET"
	kwWhere        = "WHERE"
	kwExcluded     = "EXCLUDED"
)

// ErrConflictTarget is returned for ON CONFLICT DO UPDATE with no conflict
// target: Postgres accepts DO NOTHING without one and refuses DO UPDATE,
// because it cannot tell which unique key's row to update.
var ErrConflictTarget = errors.New("sqlb/pg: ON CONFLICT DO UPDATE needs conflict columns or a constraint")

// Conflict accumulates an ON CONFLICT clause for an Insert. Build it with
// OnConflict or OnConstraint, then DoNothing or DoUpdate.
type Conflict struct {
	columns     []sqlb.SQLer
	constraint  string
	targetWhere []sqlb.Predicate
	update      []sqlb.Assignment
	updateWhere []sqlb.Predicate
	nothing     bool
	// invalid is set by OnConstraint given a zero TableConstraint.
	invalid bool
}

// OnConflict targets the given columns (a unique index). No columns means
// any conflict, which is valid only with DoNothing.
func OnConflict(cols ...sqlb.SQLer) *Conflict { return &Conflict{columns: cols} }

// OnConstraint targets a named constraint.
func OnConstraint(c TableConstraint) *Conflict {
	if !c.Valid() {
		return &Conflict{invalid: true}
	}
	return &Conflict{constraint: c.Name}
}

// TargetWhere adds the WHERE of a partial unique index to the target.
func (c *Conflict) TargetWhere(ps ...sqlb.Predicate) *Conflict {
	c.targetWhere = append(c.targetWhere, ps...)
	return c
}

// DoNothing renders DO NOTHING.
func (c *Conflict) DoNothing() InsertClause { c.nothing = true; return c.clause() }

// DoUpdate renders DO UPDATE SET assignments. Use Excluded for the proposed row.
func (c *Conflict) DoUpdate(as ...sqlb.Assignment) *Conflict {
	c.update = append(c.update, as...)
	return c
}

// UpdateWhere adds a condition to the DO UPDATE.
func (c *Conflict) UpdateWhere(ps ...sqlb.Predicate) *Conflict {
	c.updateWhere = append(c.updateWhere, ps...)
	return c
}

// Clause finishes a DoUpdate chain.
func (c *Conflict) Clause() InsertClause { return c.clause() }

func (c *Conflict) clause() InsertClause {
	return insertClause(sqlb.SlotTail, constructOnConflict, func(a *sqlb.Args) string {
		if c.invalid {
			a.Fail(ddl.ErrZeroValue)
			return ""
		}
		d := a.Dialect()
		var b strings.Builder
		b.WriteString(kwOnConflict)
		switch {
		case c.constraint != "":
			b.WriteString(" ")
			b.WriteString(kwOnConstraint)
			b.WriteString(" ")
			b.WriteString(d.QuoteIdent(c.constraint))
		case len(c.columns) > 0:
			b.WriteString(" (")
			for i, col := range c.columns {
				if i > 0 {
					b.WriteString(", ")
				}
				// A conflict target is a bare column name, never alias-qualified.
				if n, ok := col.(interface{ Name() string }); ok && n.Name() != "" {
					b.WriteString(d.QuoteIdent(n.Name()))
				} else {
					b.WriteString(col.SQL(a))
				}
			}
			b.WriteString(")")
			if len(c.targetWhere) > 0 {
				b.WriteString(" ")
				b.WriteString(kwWhere)
				b.WriteString(" ")
				b.WriteString(sqlb.PredicateSQL(sqlb.And(c.targetWhere...), a))
			}
		}
		if c.nothing || len(c.update) == 0 {
			b.WriteString(" ")
			b.WriteString(kwDoNothing)
			return b.String()
		}
		if c.constraint == "" && len(c.columns) == 0 {
			a.Fail(ErrConflictTarget)
			return ""
		}
		b.WriteString(" ")
		b.WriteString(kwDoUpdateSet)
		b.WriteString(" ")
		for i, as := range c.update {
			if i > 0 {
				b.WriteString(", ")
			}
			b.WriteString(as.SQL(a))
		}
		if len(c.updateWhere) > 0 {
			b.WriteString(" ")
			b.WriteString(kwWhere)
			b.WriteString(" ")
			b.WriteString(sqlb.PredicateSQL(sqlb.And(c.updateWhere...), a))
		}
		return b.String()
	})
}

// Excluded references the row proposed for insertion: EXCLUDED.column.
func Excluded[T any](col sqlb.Expr[T]) sqlb.Expr[T] {
	return expr[T](constructOnConflict, func(a *sqlb.Args) string {
		return kwExcluded + "." + a.Dialect().QuoteIdent(col.Name())
	})
}

const (
	constructWriteClause = "pg.write"

	kwDefaultValues = "DEFAULT VALUES"
	kwFrom          = "FROM"
	kwUsingDelete   = "USING"
)

// DefaultValues renders INSERT INTO t DEFAULT VALUES. MySQL and ClickHouse
// have no such form.
func DefaultValues() InsertClause {
	return insertClause(sqlb.SlotInsertBody, constructWriteClause, func(*sqlb.Args) string {
		return kwDefaultValues
	})
}

// UpdateFrom renders UPDATE ... FROM sources after the SET list, so SET and
// WHERE may reference them. MySQL uses a multi-table UPDATE instead.
func UpdateFrom(srcs ...sqlb.Source) UpdateClause {
	return updateClause(sqlb.SlotAfterSet, constructWriteClause, func(a *sqlb.Args) string {
		out := make([]string, len(srcs))
		for i, s := range srcs {
			out[i] = s.SourceSQL(a)
		}
		return kwFrom + " " + strings.Join(out, ", ")
	})
}

// DeleteUsing renders DELETE FROM t USING sources.
func DeleteUsing(srcs ...sqlb.Source) DeleteClause {
	return deleteClause(sqlb.SlotAfterTarget, constructWriteClause, func(a *sqlb.Args) string {
		out := make([]string, len(srcs))
		for i, s := range srcs {
			out[i] = s.SourceSQL(a)
		}
		return kwUsingDelete + " " + strings.Join(out, ", ")
	})
}
