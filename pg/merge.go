package pg

import (
	"errors"
	"slices"
	"strings"

	"github.com/ubgo/sqlb"
)

const (
	constructMerge = "pg.Merge"

	kwMergeInto           = "MERGE INTO"
	kwUsing               = "USING"
	kwOn                  = "ON"
	kwWhenMatched         = "WHEN MATCHED"
	kwWhenNotMatched      = "WHEN NOT MATCHED"
	kwWhenNotMatchedBySrc = "WHEN NOT MATCHED BY SOURCE"
	kwThenUpdateSet       = "THEN UPDATE SET"
	kwThenDelete          = "THEN DELETE"
	kwThenDoNothing       = "THEN DO NOTHING"
	kwThenInsert          = "THEN INSERT"
	kwAnd                 = "AND"
	kwReturning           = "RETURNING"
	fnMergeAction         = "merge_action"
)

type mergeWhen struct {
	kind    string
	cond    []sqlb.Predicate
	update  []sqlb.Assignment
	insert  []sqlb.Assignment
	delete  bool
	nothing bool
}

// Merge builds a MERGE statement (Postgres 15; BY SOURCE and RETURNING need
// 17). It is a Statement, so it can be a CTE body.
//
// Every method returns a derived statement and leaves its receiver alone, as the
// core's builders do, including the WHEN branches: finishing a branch derives
// from the Merge it started from. Derived statements share one Guard, because
// RETURNING captures the caller's scan targets and every Query built from the
// family writes into the same variables; two of them executing at once is the
// overlap ErrQueryInUse reports.
type Merge struct {
	dialect   Dialect
	guard     sqlb.Guard
	target    sqlb.Table
	source    sqlb.Source
	on        []sqlb.Predicate
	whens     []mergeWhen
	returning []sqlb.Binding
}

// ErrIncompleteMerge is returned by Build for a MERGE without its target, its
// source, its join condition or a WHEN branch. Each is required by the
// grammar; a MERGE with no USING dereferenced a nil source inside Build until
// 2026-09-22, and that was reachable through NewMerge, not only a literal.
var ErrIncompleteMerge = errors.New("sqlb/pg: MERGE needs Into, Using, On and at least one WHEN branch")

// NewMerge starts a MERGE for d. The dialect is typed: MERGE is here as a
// Postgres construct, so another engine's dialect does not compile.
func NewMerge(d Dialect) *Merge { return &Merge{dialect: d, guard: sqlb.NewGuard()} }

// derive copies the statement so a method can change the copy. Every slice is
// cloned so an append cannot write into the receiver's backing array; the
// guard is copied, not renewed, because derived statements share targets.
func (m *Merge) derive() *Merge {
	// A WhenBuilder{} literal holds no Merge; deriving from it gives an empty
	// one, which Build refuses with ErrIncompleteMerge instead of a nil
	// dereference inside ThenUpdate.
	if m == nil {
		return &Merge{}
	}
	c := *m
	c.on = slices.Clone(m.on)
	c.whens = slices.Clone(m.whens)
	c.returning = slices.Clone(m.returning)
	return &c
}

// Into names the target table, the one rows are merged into.
func (m *Merge) Into(t sqlb.Table) *Merge { c := m.derive(); c.target = t; return c }

// Using sets the source and the ON condition.
func (m *Merge) Using(src sqlb.Source, on ...sqlb.Predicate) *Merge {
	c := m.derive()
	c.source, c.on = src, slices.Clone(on)
	return c
}

// WhenBuilder finishes one WHEN branch.
type WhenBuilder struct {
	m *Merge
	w mergeWhen
}

// WhenMatched starts a branch for rows the ON condition matched. The optional
// conditions narrow it further, and branches are evaluated in the order they
// were added, so the first match wins.
func (m *Merge) WhenMatched(cond ...sqlb.Predicate) WhenBuilder {
	return WhenBuilder{m, mergeWhen{kind: kwWhenMatched, cond: slices.Clone(cond)}}
}

// WhenNotMatched starts a branch for source rows with no target row. This is
// the insert half of an upsert.
func (m *Merge) WhenNotMatched(cond ...sqlb.Predicate) WhenBuilder {
	return WhenBuilder{m, mergeWhen{kind: kwWhenNotMatched, cond: slices.Clone(cond)}}
}

// WhenNotMatchedBySource starts a branch for target rows with no source row
// (PG17). It is how a merge deletes rows the source no longer has, which
// ON CONFLICT cannot express at all.
func (m *Merge) WhenNotMatchedBySource(cond ...sqlb.Predicate) WhenBuilder {
	return WhenBuilder{m, mergeWhen{kind: kwWhenNotMatchedBySrc, cond: slices.Clone(cond)}}
}

// ThenUpdate finishes the branch with UPDATE SET, and returns the Merge so
// the next branch chains from it.
func (w WhenBuilder) ThenUpdate(as ...sqlb.Assignment) *Merge {
	w.w.update = slices.Clone(as)
	c := w.m.derive()
	c.whens = append(c.whens, w.w)
	return c
}

// ThenInsert finishes the branch with INSERT.
func (w WhenBuilder) ThenInsert(as ...sqlb.Assignment) *Merge {
	w.w.insert = slices.Clone(as)
	c := w.m.derive()
	c.whens = append(c.whens, w.w)
	return c
}

// ThenDelete finishes the branch with DELETE.
func (w WhenBuilder) ThenDelete() *Merge {
	w.w.delete = true
	c := w.m.derive()
	c.whens = append(c.whens, w.w)
	return c
}

// ThenDoNothing finishes the branch with DO NOTHING, which is how a branch
// claims rows so a later branch does not act on them.
func (w WhenBuilder) ThenDoNothing() *Merge {
	w.w.nothing = true
	c := w.m.derive()
	c.whens = append(c.whens, w.w)
	return c
}

// Returning adds RETURNING with scan targets (PG17). Pair it with
// MergeAction to learn which branch each returned row took.
func (m *Merge) Returning(bs ...sqlb.Binding) *Merge {
	c := m.derive()
	c.returning = append(c.returning, bs...)
	return c
}

// MergeAction renders merge_action() for RETURNING: 'INSERT', 'UPDATE' or 'DELETE'.
func MergeAction() sqlb.Expr[string] { return fn[string](constructMerge, fnMergeAction) }

// Build renders the MERGE, or returns the first error it found.
func (m *Merge) Build() (sqlb.Query, error) {
	args := sqlb.NewArgs(m.dialect)
	var b strings.Builder
	m.WriteSQL(&b, args)
	if err := args.Err(); err != nil {
		return sqlb.Query{}, err
	}
	var targets []any
	if len(m.returning) > 0 {
		targets = sqlb.Targets(m.returning)
	}
	return sqlb.NewGuardedQuery(m.guard, b.String(), args.Values(), targets), nil
}

// WriteSQL renders the MERGE into b, satisfying sqlb.Statement.
func (m *Merge) WriteSQL(b *strings.Builder, a *sqlb.Args) {
	if !guard(a, constructMerge) {
		return
	}
	if m.target.Name == "" || m.source == nil || len(m.on) == 0 || len(m.whens) == 0 {
		a.Fail(ErrIncompleteMerge)
		return
	}
	d := a.Dialect()
	b.WriteString(kwMergeInto)
	b.WriteString(" ")
	b.WriteString(m.target.SQL(d))
	b.WriteString(" ")
	b.WriteString(kwUsing)
	b.WriteString(" ")
	b.WriteString(m.source.SourceSQL(a))
	b.WriteString(" ")
	b.WriteString(kwOn)
	b.WriteString(" ")
	b.WriteString(sqlb.PredicateSQL(sqlb.And(m.on...), a))
	for _, w := range m.whens {
		b.WriteString(" ")
		b.WriteString(w.kind)
		if len(w.cond) > 0 {
			b.WriteString(" ")
			b.WriteString(kwAnd)
			b.WriteString(" ")
			b.WriteString(sqlb.PredicateSQL(sqlb.And(w.cond...), a))
		}
		switch {
		case w.nothing:
			b.WriteString(" ")
			b.WriteString(kwThenDoNothing)
		case w.delete:
			b.WriteString(" ")
			b.WriteString(kwThenDelete)
		case len(w.update) > 0:
			b.WriteString(" ")
			b.WriteString(kwThenUpdateSet)
			b.WriteString(" ")
			for i, as := range w.update {
				if i > 0 {
					b.WriteString(", ")
				}
				b.WriteString(as.SQL(a))
			}
		case len(w.insert) > 0:
			b.WriteString(" ")
			b.WriteString(kwThenInsert)
			b.WriteString(" (")
			for i, as := range w.insert {
				if i > 0 {
					b.WriteString(", ")
				}
				b.WriteString(d.QuoteIdent(as.Column()))
			}
			b.WriteString(") ")
			b.WriteString(kwValues)
			b.WriteString(" (")
			for i, as := range w.insert {
				if i > 0 {
					b.WriteString(", ")
				}
				b.WriteString(as.ValueSQL(a))
			}
			b.WriteString(")")
		}
	}
	if len(m.returning) > 0 {
		b.WriteString(" ")
		b.WriteString(kwReturning)
		b.WriteString(" ")
		for i, r := range m.returning {
			if i > 0 {
				b.WriteString(", ")
			}
			b.WriteString(r.SQL(a))
		}
	}
}
