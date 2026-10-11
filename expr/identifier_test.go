package expr_test

import (
	"testing"

	"github.com/ubgo/sqlb/dialect/postgres"
	"github.com/ubgo/sqlb/expr"
	"github.com/ubgo/sqlb/render"
)

// TestCol_NameFromOutsideStaysOneIdentifier pins the property that dynamic
// column selection depends on.
//
// Everywhere else in this library a value from outside the program arrives as
// a bound argument, so the question never comes up. A column name cannot be
// bound — no engine accepts a placeholder where an identifier goes — so when
// the select list is chosen at runtime, an attacker-controlled string is
// rendered into the SQL text. What keeps that safe is that the dialect doubles
// an embedded quote, which makes the whole string one identifier no matter
// what it contains; the query then fails with "no such column" rather than
// running a second statement.
//
// This is a property of Quote rather than of the dynamic reader, and it is
// pinned here because that is where the name enters. If a dialect ever stopped
// doubling, the failure would be silent and remote from the cause.
func TestCol_NameFromOutsideStaysOneIdentifier(t *testing.T) {
	users := expr.Table{Name: "users", Alias: "u"}

	for _, tc := range []struct{ name, want string }{
		{
			`email" FROM users; DROP TABLE users --`,
			`"u"."email"" FROM users; DROP TABLE users --"`,
		},
		{`a"b`, `"u"."a""b"`},
		{`*`, `"u"."*"`}, // quoted, so it selects a column named *, not every column
		{``, `"u".""`},
	} {
		a := render.NewArgs(postgres.Dialect{})
		if got := expr.Col[any](users, tc.name).SQL(a); got != tc.want {
			t.Errorf("Col(%q)\n got %s\nwant %s", tc.name, got, tc.want)
		}
	}
}
