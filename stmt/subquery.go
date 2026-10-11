package stmt

import (
	"strings"

	"github.com/ubgo/sqlb/expr"
	"github.com/ubgo/sqlb/render"
)

// Subquery uses a Select as a scalar expression: (SELECT ...). The author
// states the type; the result is nullable because a subquery that returns no
// row yields NULL.
func Subquery[T any, D render.Dialect](s *Select[D]) Expr[*T] {
	return New[*T](func(a *Args) string {
		var b strings.Builder
		b.WriteString("(")
		s.WriteSQL(&b, a)
		b.WriteString(")")
		return b.String()
	})
}

type existsPredicate struct {
	s   Statement
	not bool
}

func (p existsPredicate) WriteSQL(b *strings.Builder, a *Args) {
	if p.not {
		b.WriteString("NOT ")
	}
	b.WriteString("EXISTS (")
	p.s.WriteSQL(b, a)
	b.WriteString(")")
}

// Exists and NotExists. The subquery may be correlated: it references the
// outer sources by their aliases, which the outer statement's alias check
// does not see, so a collision between an inner and an outer alias is the
// author's responsibility.
func Exists[D render.Dialect](s *Select[D]) Predicate { return existsPredicate{s, false} }

// NotExists renders NOT EXISTS (SELECT ...), the anti-join. It is usually
// faster than NOT IN and, unlike NOT IN, it is not defeated by a NULL in the
// subquery result.
func NotExists[D render.Dialect](s *Select[D]) Predicate { return existsPredicate{s, true} }

// SubqueryCompare renders "e op [quantifier] (SELECT ...)". It is the
// mechanism behind InSubquery and behind the ANY/ALL forms in engine
// packages.
//
// Both op and quantifier reach the statement text and neither can be quoted,
// so both are checked for their shape: op for an operator's and quantifier for
// a keyword's, which is what ANY, ALL and SOME are. An empty quantifier is
// legitimate and means the form without one, which is what IN and NOT IN use.
func SubqueryCompare[D render.Dialect](e SQLer, op, quantifier string, s *Select[D]) Predicate {
	return PredicateOf(func(a *Args) string {
		var b strings.Builder
		b.WriteString(e.SQL(a))
		b.WriteString(sepSpace)
		b.WriteString(expr.CheckedOperator(a, op, "operator"))
		if quantifier != "" {
			b.WriteString(sepSpace)
			b.WriteString(expr.CheckedKeyword(a, quantifier, "quantifier"))
		}
		b.WriteString(" (")
		s.WriteSQL(&b, a)
		b.WriteString(")")
		return b.String()
	})
}

// InSubquery renders e IN (SELECT ...). The subquery must select one item
// whose type is T; the compiler cannot check that across the Select, so it
// is stated in the signature by convention and checked by the database.
func InSubquery[T any, D render.Dialect](e Expr[T], s *Select[D]) Predicate {
	return SubqueryCompare(e, OpIn, "", s)
}

// NotInSubquery renders e NOT IN (SELECT ...). Beware the NULL rule: if the
// subquery returns a single NULL the whole predicate is NULL and matches
// nothing. NotExists does not have that failure.
func NotInSubquery[T any, D render.Dialect](e Expr[T], s *Select[D]) Predicate {
	return SubqueryCompare(e, OpNotIn, "", s)
}
