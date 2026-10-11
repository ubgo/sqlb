package packages_test

import (
	"fmt"
	"strings"

	"github.com/ubgo/sqlb/dialect/postgres"
	"github.com/ubgo/sqlb/expr"
	"github.com/ubgo/sqlb/render"
)

// Users is what a library exports: a table and its typed columns. The
// application composing them gets the same checking the library had.
var (
	Users     = expr.Table{Name: "app_user", Alias: "u"}
	UserID    = expr.Col[string](Users, "id")
	UserEmail = expr.Col[string](Users, "email")
	UserAge   = expr.Col[int](Users, "age")
)

// render1 renders one predicate and prints it with its bound values.
func render1(p expr.Predicate) {
	a := render.NewArgs(postgres.Dialect{})
	var b strings.Builder
	p.WriteSQL(&b, a)
	fmt.Println(b.String(), a.Values())
}

// ExampleCol shows the central idea: a column is a value with a Go type.
func ExampleCol() {
	a := render.NewArgs(postgres.Dialect{})
	fmt.Println(UserEmail.SQL(a))
	fmt.Println(UserEmail.Name(), UserEmail.Table().Name)

	// Output:
	// "u"."email"
	// email app_user
}

// ExampleEq shows a comparison against a Go value. The value is bound, never
// interpolated, and its type has to match the column's.
func ExampleEq() {
	render1(expr.Eq(UserEmail, "ada@example.com"))
	render1(expr.Greater(UserAge, 30))

	// expr.Eq(UserEmail, 30) does not compile: the column is Expr[string].

	// Output:
	// "u"."email" = $1 [ada@example.com]
	// "u"."age" > $1 [30]
}

// ExampleAnd shows predicates composing. Parentheses are always written, so a
// mixed AND and OR cannot change meaning by accident.
func ExampleAnd() {
	render1(expr.And(
		expr.Eq(UserEmail, "ada@example.com"),
		expr.Or(expr.Greater(UserAge, 30), expr.IsNull(UserAge)),
	))

	// Output:
	// ("u"."email" = $1 AND ("u"."age" > $2 OR "u"."age" IS NULL)) [ada@example.com 30]
}

// ExampleParam shows binding a value as an expression, which is what puts a
// bound value anywhere an expression is allowed rather than only on the right
// of a comparison.
func ExampleParam() {
	a := render.NewArgs(postgres.Dialect{})
	fmt.Println(expr.Lower(expr.Param("ADA")).SQL(a), a.Values())

	// Output:
	// lower($1) [ADA]
}

// ExampleNullable shows the same physical column being non-null under an inner
// join and nullable under a left join. Nothing about the SQL changes; the Go
// type does, so a left-joined column cannot be bound to a non-pointer.
func ExampleNullable() {
	optional := expr.Nullable(UserEmail)

	a := render.NewArgs(postgres.Dialect{})
	fmt.Println(optional.SQL(a))
	fmt.Printf("%T\n", optional)

	// Output:
	// "u"."email"
	// expr.Expr[*string]
}

// ExampleSum shows an aggregate. The result type follows the input, so summing
// an int column yields Expr[int] and binding it to a string does not compile.
func ExampleSum() {
	a := render.NewArgs(postgres.Dialect{})
	fmt.Println(expr.Sum(UserAge).SQL(a))
	fmt.Println(expr.CountOf(UserID).SQL(a))
	fmt.Println(expr.Count().SQL(a))

	// Output:
	// sum("u"."age")
	// count("u"."id")
	// count(*)
}

// ExampleFn shows the escape hatch for a function the core does not name. It
// is typed, so the result still participates in the checking.
func ExampleFn() {
	a := render.NewArgs(postgres.Dialect{})
	initials := expr.Fn[string]("left", UserEmail, expr.Param(1))
	fmt.Println(initials.SQL(a), a.Values())

	// Output:
	// left("u"."email", $1) [1]
}

// ExampleCase shows a searched CASE. CaseBuilder accumulates branches as a
// value, Else yields Expr[T] because the result cannot be null, and End would
// yield Expr[*T] because a CASE with no ELSE is null when no branch matches.
func ExampleCase() {
	a := render.NewArgs(postgres.Dialect{})
	seniority := expr.Case[string]().
		When(expr.Greater(UserAge, 30), expr.Param("senior")).
		Else(expr.Param("junior"))
	fmt.Println(seniority.SQL(a), a.Values())

	// Output:
	// CASE WHEN "u"."age" > $1 THEN $2 ELSE $3 END [30 senior junior]
}

// ExampleBinary shows the door for an operator the core does not spell. The
// Op constants (OpEq, OpLike, OpAdd and the rest) name the operators the
// core's own predicates use; Binary takes the operator as a value, so an
// engine operator such as Postgres's regex match composes with everything
// else and stays typed.
func ExampleBinary() {
	a := render.NewArgs(postgres.Dialect{})
	startsWithAda := expr.Binary[bool](UserEmail, "~", expr.Param("^ada"))
	fmt.Println(startsWithAda.SQL(a), a.Values())

	// Output:
	// ("u"."email" ~ $1) [^ada]
}

// ageBand is a predicate the package does not ship, written the way a
// consumer writes one: a type whose WriteSQL renders through PredicateSQL,
// which is also how the engine packages embed a predicate inside a function
// call.
type ageBand struct{ lo, hi int }

func (p ageBand) WriteSQL(b *strings.Builder, a *render.Args) {
	b.WriteString(expr.PredicateSQL(
		expr.And(expr.GreaterEq(UserAge, p.lo), expr.Less(UserAge, p.hi)), a))
}

// ExamplePredicateSQL shows a consumer's own predicate composing with the
// package's, in this case the And inside ageBand.
func ExamplePredicateSQL() {
	render1(ageBand{18, 30})

	// Output:
	// ("u"."age" >= $1 AND "u"."age" < $2) [18 30]
}
