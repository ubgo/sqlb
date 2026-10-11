package expr_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/ubgo/sqlb/dialect/clickhouse"
	"github.com/ubgo/sqlb/dialect/postgres"
	"github.com/ubgo/sqlb/dialect/sqlite"
	"github.com/ubgo/sqlb/expr"
	"github.com/ubgo/sqlb/render"
)

func bound(t *testing.T, p expr.Predicate) (string, []any) {
	t.Helper()
	a := render.NewArgs(postgres.Dialect{})
	var b strings.Builder
	p.WriteSQL(&b, a)
	return b.String(), a.Values()
}

// TestEscapeLike covers the three characters LIKE treats as syntax under
// ESCAPE '!': the two wildcards and the escape character itself, and that a
// backslash is now ordinary text, because the escape character is not one.
func TestEscapeLike(t *testing.T) {
	for _, tc := range []struct{ in, want string }{
		{"plain", "plain"},
		{"50%", `50!%`},
		{"a_b", `a!_b`},
		{"wow!", "wow!!"},
		{`back\slash`, `back\slash`},
		{`%_!`, `!%!_!!`},
		{"", ""},
	} {
		if got := expr.EscapeLike(tc.in); got != tc.want {
			t.Errorf("EscapeLike(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

// TestSearchPredicates_EscapeAndWrap pins where each wildcard goes and that the
// value is escaped before it is wrapped, not after.
func TestSearchPredicates_EscapeAndWrap(t *testing.T) {
	col := expr.Col[string](expr.Table{Name: "p", Alias: "p"}, "name")

	for _, tc := range []struct {
		name string
		pred expr.Predicate
		sql  string
		arg  string
	}{
		{"Contains", expr.Contains(col, "50%"), `"p"."name" LIKE $1 ESCAPE '!'`, `%50!%%`},
		{"StartsWith", expr.StartsWith(col, "50%"), `"p"."name" LIKE $1 ESCAPE '!'`, `50!%%`},
		{"EndsWith", expr.EndsWith(col, "50%"), `"p"."name" LIKE $1 ESCAPE '!'`, `%50!%`},
		{"NotContains", expr.NotContains(col, "50%"), `"p"."name" NOT LIKE $1 ESCAPE '!'`, `%50!%%`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			sql, args := bound(t, tc.pred)
			if sql != tc.sql {
				t.Fatalf("got  %s\nwant %s", sql, tc.sql)
			}
			if len(args) != 1 || args[0] != tc.arg {
				t.Fatalf("bound %q, want %q", args, tc.arg)
			}
		})
	}
}

// TestSearchPredicates_ValueNeverReachesTheSQL pins that escaping did not turn
// into interpolation. The wildcards belong in the bound value, not in the
// statement, so that a pattern is still one placeholder however it was built.
func TestSearchPredicates_ValueNeverReachesTheSQL(t *testing.T) {
	col := expr.Col[string](expr.Table{Name: "p", Alias: "p"}, "name")
	sql, args := bound(t, expr.Contains(col, "secret"))

	if strings.Contains(sql, "secret") || strings.Contains(sql, "%") {
		t.Fatalf("the value or its wildcards reached the SQL: %s", sql)
	}
	if len(args) != 1 {
		t.Fatalf("args = %v, want one", args)
	}
}

// TestLike_StillTakesARawPattern pins that the escaping predicates did not
// change Like itself.
//
// Like takes a pattern and every character in it is pattern syntax, which is
// right when the caller wrote the pattern. Contains and its siblings are for
// when a person did.
func TestLike_StillTakesARawPattern(t *testing.T) {
	col := expr.Col[string](expr.Table{Name: "p", Alias: "p"}, "name")
	_, args := bound(t, expr.Like(col, "a%b_c"))
	if args[0] != "a%b_c" {
		t.Fatalf("Like altered its pattern: %q", args[0])
	}
}

// TestSearchPredicates_RefuseWithoutTheCapability pins the failure mode that
// matters more than the success one.
//
// A dialect that cannot write an ESCAPE clause cannot escape a user's text,
// and rendering the pattern without the clause would match the wrong rows. A
// search returning too much looks like it worked, so this refuses at Build
// instead.
func TestSearchPredicates_RefuseWithoutTheCapability(t *testing.T) {
	col := expr.Col[string](expr.Table{Name: "p", Alias: "p"}, "name")

	a := render.NewArgs(noEscapeDialect{})
	var b strings.Builder
	expr.Contains(col, "50%").WriteSQL(&b, a)

	if !errors.Is(a.Err(), expr.ErrNoLikeEscape) {
		t.Fatalf("want ErrNoLikeEscape, got %v", a.Err())
	}
}

// noEscapeDialect is a dialect that does not implement render.LikeEscaping,
// which is how a dialect declines a capability here: by not having the method,
// never by having it and returning false.
type noEscapeDialect struct{}

func (noEscapeDialect) Placeholder(int) string         { return "?" }
func (noEscapeDialect) QuoteIdent(ident string) string { return render.Quote(ident, '"') }
func (noEscapeDialect) Name() string                   { return "no-escape" }

// TestShippedDialects_DeclareLikeEscaping pins which of the three can escape.
//
// Postgres and SQLite can, and ClickHouse is not known to accept the clause,
// so it declines. If that ever changes, this test is where the claim lives.
func TestShippedDialects_DeclareLikeEscaping(t *testing.T) {
	for _, c := range []struct {
		d    render.Dialect
		want bool
	}{
		{postgres.Dialect{}, true},
		{sqlite.Dialect{}, true},
		{clickhouse.Dialect{}, false},
	} {
		got := false
		if e, ok := c.d.(render.LikeEscaping); ok {
			got = e.SupportsLikeEscape()
		}
		if got != c.want {
			t.Errorf("%s: SupportsLikeEscape = %v, want %v", render.DialectName(c.d), got, c.want)
		}
	}
}
