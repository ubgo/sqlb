package expr

import (
	"github.com/ubgo/sqlb/render"
	"strings"
)

// Number is the set of Go types that arithmetic operators accept.
type Number interface {
	~int | ~int8 | ~int16 | ~int32 | ~int64 |
		~uint | ~uint8 | ~uint16 | ~uint32 | ~uint64 |
		~float32 | ~float64
}

// Binary renders "(left op right)" typed R. Engine packages use it for their
// operators; the core uses it for arithmetic.
//
// op is checked for the SHAPE of an operator, not against a list: the
// operators an engine has come from its extensions, so a list would refuse a
// valid one. See CheckedOperator for what the shape is and why an operator
// cannot simply be quoted instead.
func Binary[R any](left render.SQLer, op string, right render.SQLer) Expr[R] {
	return New[R](func(a *render.Args) string {
		l := typedOperand(a, left)
		o := CheckedOperator(a, op, "operator")
		return "(" + l + sepSpace + o + sepSpace + typedOperand(a, right) + ")"
	})
}

// typedOperand renders s, and when it rendered as exactly one new placeholder
// on a dialect implementing render.NumericParameters, gives that placeholder a
// type. It is the arithmetic and COALESCE counterpart of the text typing in
// Concat: on MySQL a bare integer parameter beside a DECIMAL widens the result
// to DECIMAL(65,30), which an Expr[int64] cannot scan. Every other operand, and
// every other dialect, renders exactly as before.
func typedOperand(a *render.Args, s render.SQLer) string {
	before := len(a.Values())
	sql := s.SQL(a)
	if n, ok := a.Dialect().(render.NumericParameters); ok &&
		len(a.Values()) == before+1 && sql == a.Dialect().Placeholder(before+1) {
		return n.NumericParam(sql, a.Values()[before])
	}
	return sql
}

// Add renders l + r.
//
// The result of this family is typed as the operand type, which matches SQL
// for same-type operands. The trap is Div: integer division truncates in SQL
// as it does in Go, so a ratio needs a cast first.
func Add[T Number](l, r Expr[T]) Expr[T] { return Binary[T](l, OpAdd, r) }

// Sub renders l - r.
func Sub[T Number](l, r Expr[T]) Expr[T] { return Binary[T](l, OpSub, r) }

// Mul renders l * r.
func Mul[T Number](l, r Expr[T]) Expr[T] { return Binary[T](l, OpMul, r) }

// Div renders l / r, which truncates on integer operands. Cast one side for
// a real quotient.
func Div[T Number](l, r Expr[T]) Expr[T] { return Binary[T](l, OpDiv, r) }

// Mod renders l % r.
func Mod[T Number](l, r Expr[T]) Expr[T] { return Binary[T](l, OpMod, r) }

// Neg renders (-e), parenthesised so it composes without a precedence
// surprise next to another operator.
func Neg[T Number](e Expr[T]) Expr[T] {
	return New[T](func(a *render.Args) string { return "(-" + e.SQL(a) + ")" })
}

// Concat renders concat(a, b, ...), the function form, because the ||
// operator is not concatenation in MySQL. Every target engine has concat().
//
// concat takes any type, so Postgres cannot infer one for a bare bound
// parameter among its arguments and refuses the statement; the conformance
// suite found that on its first Postgres run. An argument that rendered as
// exactly one new placeholder is therefore given a type through
// render.UntypedParameters when the dialect asks for it, and every other
// argument, and every other dialect, renders as it always did.
func Concat(parts ...Expr[string]) Expr[string] {
	return New[string](func(a *render.Args) string {
		out := make([]string, len(parts))
		for i, p := range parts {
			before := len(a.Values())
			sql := p.SQL(a)
			if u, ok := a.Dialect().(render.UntypedParameters); ok &&
				len(a.Values()) == before+1 && sql == a.Dialect().Placeholder(before+1) {
				sql = u.TextParam(sql)
			}
			out[i] = sql
		}
		return fnConcat + "(" + strings.Join(out, sepComma) + ")"
	})
}

// As aliases a select item: expr AS name. Needed for a CTE or derived table
// whose columns the outer query references by name, and for ORDER BY alias.
func As[T any](e Expr[T], name string) Expr[T] {
	return New[T](func(a *render.Args) string {
		return e.SQL(a) + sepSpace + kwAs + sepSpace + a.Dialect().QuoteIdent(name)
	})
}

// OutputCol references a select-list alias by name, typed T. It is how an
// ORDER BY over a UNION names its sort key, since SQL requires an output
// column name there and rejects a table-qualified one.
func OutputCol[T any](name string) Expr[T] {
	return New[T](func(a *render.Args) string { return a.Dialect().QuoteIdent(name) })
}

// Cast renders CAST(e AS sqlType) and re-types the expression as R. The SQL
// type is a constant chosen by the author, never request data.
func Cast[R, T any](e Expr[T], sqlType string) Expr[R] {
	return New[R](func(a *render.Args) string { return "CAST(" + e.SQL(a) + " AS " + sqlType + ")" })
}

// Fn renders a function call name(args...) typed R. It is the typed door for
// the long tail of SQL functions; the author states the result type once, at
// the call, and a search for "sqlb.Fn[" lists every such promise.
//
// name is checked for the shape of a function name — an identifier, or a
// schema-qualified pair — because it cannot be quoted: quoting
// pg_catalog.date_trunc as one identifier names a function that does not
// exist. A function whose name needs quoting is reachable through Raw.
func Fn[R any](name string, args ...render.SQLer) Expr[R] {
	return New[R](func(a *render.Args) string {
		out := make([]string, len(args))
		for i, arg := range args {
			out[i] = arg.SQL(a)
		}
		return CheckedFunctionName(a, name, "function") + "(" + strings.Join(out, sepComma) + ")"
	})
}

// True renders TRUE. It is the ON clause of a LATERAL join, which has no
// join condition of its own.
func True() Predicate { return constPredicate(sqlTrue) }

// False renders FALSE, which fails a query closed. It is what an empty IN
// list renders as, since IN () is a syntax error in every engine.
func False() Predicate { return constPredicate(sqlFalse) }

type constPredicate string

func (c constPredicate) WriteSQL(b *strings.Builder, _ *render.Args) { b.WriteString(string(c)) }

// Between renders e BETWEEN lo AND hi with bound ends.
func Between[T any](e Expr[T], lo, hi T) Predicate {
	return between[T]{e, Param(lo), Param(hi), false}
}

// NotBetween is the negated form.
func NotBetween[T any](e Expr[T], lo, hi T) Predicate {
	return between[T]{e, Param(lo), Param(hi), true}
}

type between[T any] struct {
	e      Expr[T]
	lo, hi Expr[T]
	not    bool
}

func (p between[T]) WriteSQL(b *strings.Builder, a *render.Args) {
	b.WriteString(p.e.SQL(a))
	if p.not {
		b.WriteString(" NOT")
	}
	b.WriteString(" BETWEEN ")
	b.WriteString(p.lo.SQL(a))
	b.WriteString(" AND ")
	b.WriteString(p.hi.SQL(a))
}

// Like and NotLike. The pattern is bound, never in the SQL text.
func Like(e Expr[string], pattern string) Predicate { return compareExpr{e, OpLike, Param(pattern)} }

// NotLike renders e NOT LIKE $n, the negation of Like.
func NotLike(e Expr[string], pattern string) Predicate {
	return compareExpr{e, OpNotLike, Param(pattern)}
}

// predicateFunc adapts a render function to Predicate.
type predicateFunc func(a *render.Args) string

func (p predicateFunc) WriteSQL(b *strings.Builder, a *render.Args) { b.WriteString(p(a)) }

// PredicateOf makes a Predicate from a render function. Engine packages use
// it for their operators.
func PredicateOf(render func(a *render.Args) string) Predicate { return predicateFunc(render) }

// Compare renders "left op right" as a predicate. Engine packages use it for
// operators the core does not name.
//
// op is checked for the shape of an operator, as Binary's is. This is the door
// a request-derived operator reaches in practice:
// examples/pgsurface/periodcompare/filters.go maps a request's comparison enum
// through a lookup table and hands the result here. That code is right, and
// its comment says the map is "a lookup, never a format" — but the discipline
// was the caller's alone until the shape was checked here.
func Compare(left render.SQLer, op string, right render.SQLer) Predicate {
	return compareExpr{left, op, right}
}

// BoolExpr turns a predicate into a boolean expression, so it can be
// selected, aliased or used in a CASE.
func BoolExpr(p Predicate) Expr[bool] {
	return New[bool](func(a *render.Args) string { return PredicateSQL(p, a) })
}

// Coalesce renders COALESCE(first, rest...). The result type is T; the
// caller decides nullability by choosing a not-null last argument.
//
// A bare integer parameter among the arguments is typed on a dialect that
// asks through render.NumericParameters, because MySQL otherwise returns
// COALESCE(sum(x), ?) as DECIMAL(65,30) and the result no longer scans into T.
func Coalesce[T any](first Expr[T], rest ...Expr[T]) Expr[T] {
	return New[T](func(a *render.Args) string {
		out := make([]string, 0, 1+len(rest))
		out = append(out, typedOperand(a, first))
		for _, r := range rest {
			out = append(out, typedOperand(a, r))
		}
		return "COALESCE(" + strings.Join(out, sepComma) + ")"
	})
}

// NullIf renders NULLIF(a, b). It is always nullable, so the result is *T.
func NullIf[T any](l, r Expr[T]) Expr[*T] {
	return New[*T](func(a *render.Args) string { return "NULLIF(" + l.SQL(a) + sepComma + r.SQL(a) + ")" })
}

// Row renders a row constructor (a, b, c), for row comparisons and keyset
// pagination: RowLess(Row(createdAt, id), Row(Param(t), Param(id))).
func Row(items ...render.SQLer) render.SQLer {
	return render.Raw(func(a *render.Args) string {
		out := make([]string, len(items))
		for i, it := range items {
			out[i] = it.SQL(a)
		}
		return "(" + strings.Join(out, sepComma) + ")"
	})
}

// RowLess renders l < r for two row constructors, the shape a keyset page
// uses to say "after this row" in one comparison.
//
// Element types are not checked across the two rows; the database checks
// them. That is the one place in this package where the compiler cannot
// help, so keep the rows short and adjacent in the source.
func RowLess(l, r render.SQLer) Predicate { return compareExpr{l, OpLess, r} }

// RowGreater renders l > r for two row constructors.
func RowGreater(l, r render.SQLer) Predicate { return compareExpr{l, OpGreater, r} }

// RowEq renders l = r for two row constructors.
func RowEq(l, r render.SQLer) Predicate { return compareExpr{l, OpEq, r} }

// RowIn renders (a, b) IN ((?, ?), (?, ?)), a membership test for a composite
// key against a list of tuples.
//
// It exists because the single-column forms cannot express it. In and InExprs
// take an Expr[T], and a row constructor is a bare SQLer with no element type
// to be T, so before this the only way to ask "is this pair one of these
// pairs" was to OR a chain of RowEq, which asks the planner a different
// question and grows a clause per pair.
//
// Every element is whatever the caller passed, so bind the values with Param
// rather than writing them into the row. The subquery form needs nothing new:
// stmt.SubqueryCompare takes a SQLer, so (a, b) IN (SELECT a, b FROM ...) is
// SubqueryCompare(Row(a, b), OpIn, "", sel).
//
// Arity is not checked, for the same reason RowLess cannot check it: Row
// returns an opaque SQLer and the number of elements is gone by the time this
// sees it. A pair tested against a triple is an error from the database.
//
// An empty list renders FALSE, matching In: an IN with nothing in it is a
// syntax error, and dropping the predicate would widen the result set.
func RowIn(row render.SQLer, rows ...render.SQLer) Predicate {
	return rowIn(row, OpIn, rows)
}

// RowNotIn renders (a, b) NOT IN ((?, ?), ...). An empty list renders TRUE.
//
// The NULL rule that applies to NotIn applies here and is easier to hit,
// because a row is NULL if any element of it is: one nullable column in the
// list makes the whole predicate NULL, which matches nothing. Where that is
// possible, an anti-join with NotExists says what was meant.
func RowNotIn(row render.SQLer, rows ...render.SQLer) Predicate {
	return rowIn(row, OpNotIn, rows)
}

func rowIn(row render.SQLer, op string, rows []render.SQLer) Predicate {
	return predicateFunc(func(a *render.Args) string {
		if len(rows) == 0 {
			if op == OpIn {
				return sqlFalse
			}
			return sqlTrue
		}
		out := make([]string, len(rows))
		for i, r := range rows {
			out[i] = r.SQL(a)
		}
		return row.SQL(a) + sepSpace + op + " (" + strings.Join(out, sepComma) + ")"
	})
}

// CaseBuilder accumulates a searched CASE. Else yields Expr[T]; End yields
// Expr[*T], because a CASE with no ELSE is null when no branch matches.
type CaseBuilder[T any] struct {
	whens []caseWhen[T]
}

type caseWhen[T any] struct {
	cond Predicate
	then Expr[T]
}

// Case starts a searched CASE expression.
func Case[T any]() CaseBuilder[T] { return CaseBuilder[T]{} }

// When adds a branch. The builder is a value and When copies the branch list,
// so a partially built CASE can be shared as a base without a later When on
// one copy appearing in another.
func (c CaseBuilder[T]) When(cond Predicate, then Expr[T]) CaseBuilder[T] {
	c.whens = append(append([]caseWhen[T](nil), c.whens...), caseWhen[T]{cond, then})
	return c
}

func (c CaseBuilder[T]) render(a *render.Args, els *Expr[T]) string {
	var b strings.Builder
	b.WriteString("CASE")
	for _, w := range c.whens {
		b.WriteString(" WHEN ")
		w.cond.WriteSQL(&b, a)
		b.WriteString(" THEN ")
		b.WriteString(w.then.SQL(a))
	}
	if els != nil {
		b.WriteString(" ELSE ")
		b.WriteString(els.SQL(a))
	}
	b.WriteString(" END")
	return b.String()
}

// Else closes the CASE with an ELSE branch. Not null when every branch is not null.
func (c CaseBuilder[T]) Else(e Expr[T]) Expr[T] {
	return New[T](func(a *render.Args) string { return c.render(a, &e) })
}

// End closes the CASE without ELSE. The result is nullable.
func (c CaseBuilder[T]) End() Expr[*T] {
	return New[*T](func(a *render.Args) string { return c.render(a, nil) })
}
