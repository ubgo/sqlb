// Package packages_test holds one runnable example per layer package.
//
// Every snippet in docs/<package>/README.md is copied from here rather than
// typed into the prose, because a Go Example is compile-checked and its
// "Output:" comment is checked by go test. A documentation snippet that is
// merely plausible is the most expensive kind of wrong.
package packages_test

import (
	"fmt"

	"github.com/ubgo/sqlb/dialect/postgres"
	"github.com/ubgo/sqlb/dialect/sqlite"
	"github.com/ubgo/sqlb/render"
)

// ExampleArgs shows the two things the render layer does: bind a value and
// quote an identifier.
func ExampleArgs() {
	a := render.NewArgs(postgres.Dialect{})

	fmt.Println(a.Dialect().QuoteIdent("email"))
	fmt.Println(a.Next("ada@example.com"))
	fmt.Println(a.Next(36))
	fmt.Println(a.Values())

	// Output:
	// "email"
	// $1
	// $2
	// [ada@example.com 36]
}

// ExampleRaw shows the way into the builder for a fragment this module has no
// type for. Going raw costs type checking, not safety: the function still
// receives Args, so the value is still bound and the placeholder still
// numbered by the dialect.
func ExampleRaw() {
	a := render.NewArgs(postgres.Dialect{})

	frag := render.Raw(func(a *render.Args) string {
		return "lower(" + a.Dialect().QuoteIdent("email") + ") = " + a.Next("ADA@EXAMPLE.COM")
	})

	fmt.Println(frag.SQL(a))
	fmt.Println(a.Values())

	// Output:
	// lower("email") = $1
	// [ADA@EXAMPLE.COM]
}

// ExampleDialect_differences shows what a dialect actually decides. These two
// are the entire difference between Postgres and SQLite at this layer.
func ExampleDialect_differences() {
	for _, d := range []render.Dialect{postgres.Dialect{}, sqlite.Dialect{}} {
		a := render.NewArgs(d)
		fmt.Printf("%-10s %s %s\n", render.DialectName(d), d.QuoteIdent("user"), a.Next("x"))
	}

	// Output:
	// postgres   "user" $1
	// sqlite     "user" ?
}

// ExampleArgs_Unsupported shows how a construct fails closed. An engine
// package records the construct's name, and Build returns the error instead of
// handing the database a statement it will reject.
func ExampleArgs_Unsupported() {
	a := render.NewArgs(sqlite.Dialect{})
	a.Unsupported("pg.Merge")

	fmt.Println(a.Err())

	// Output:
	// sqlb: construct is not supported by this dialect
	// pg.Merge under dialect "sqlite"
}

// ExampleArgs_Fail shows the sticky-error rule: the first error wins, so every
// render function can keep a single string return value and Build still fails
// closed.
func ExampleArgs_Fail() {
	a := render.NewArgs(postgres.Dialect{})
	a.Unsupported("first.Construct")
	a.Unsupported("second.Construct")

	fmt.Println(a.Err())

	// Output:
	// sqlb: construct is not supported by this dialect
	// first.Construct under dialect "postgres"
}
