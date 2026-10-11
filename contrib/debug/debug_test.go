package debug_test

import (
	"context"
	"database/sql"
	"strings"
	"testing"
	"time"

	"github.com/ubgo/sqlb/contrib/debug"
	"github.com/ubgo/sqlb/dialect/clickhouse"
	"github.com/ubgo/sqlb/dialect/postgres"
	"github.com/ubgo/sqlb/dialect/sqlite"
	"github.com/ubgo/sqlb/expr"
	"github.com/ubgo/sqlb/render"
	"github.com/ubgo/sqlb/stmt"
)

var (
	users = expr.Table{Name: "users", Alias: "u"}
	uid   = expr.Col[int64](users, "id")
	uname = expr.Col[string](users, "name")
)

func build(t *testing.T, d render.Dialect) stmt.Query {
	t.Helper()
	q, err := stmt.NewSelect(d).
		Exprs(uid, uname).From(users).
		Where(expr.Eq(uname, "ada"), expr.Greater(uid, 10)).
		Build()
	if err != nil {
		t.Fatal(err)
	}
	return q
}

// TestRendered_CannotBeExecuted is the whole safety argument, as a compile-time
// fact rather than a comment.
//
// Interpolate returns a named string type. It prints exactly like a string and
// cannot be passed where a string is wanted without an explicit conversion,
// which nobody writes by accident. If Rendered is ever changed to a plain
// string, this stops compiling, and that is the point.
func TestRendered_CannotBeExecuted(t *testing.T) {
	var _ = func(r debug.Rendered) string {
		// Converting is possible and must stay possible: a caller who really
		// wants the text can ask for it, visibly.
		return string(r)
	}

	// And the shape that must NOT compile, which is why it is a comment:
	//
	//	var exec func(context.Context, *sql.DB, string, ...any) error
	//	exec(ctx, db, debug.Interpolate(d, q))
	//	// cannot use ... as string value in argument to exec
	//
	// The assertion below is the machine-checkable half: Rendered is its own
	// type, not an alias for string.
	var r debug.Rendered = "x"
	if any(r) == any("x") {
		t.Fatal("Rendered compares equal to a string; it has become an alias and the guard is gone")
	}
	_ = context.Background
	_ = (*sql.DB)(nil)
}

// TestInterpolate_NumberedPlaceholders covers the "$1" family, where each
// marker names its own argument.
func TestInterpolate_NumberedPlaceholders(t *testing.T) {
	got := debug.Interpolate(postgres.Dialect{}, build(t, postgres.Dialect{})).String()
	want := `SELECT "u"."id", "u"."name" FROM "users" AS "u" WHERE ("u"."name" = 'ada' AND "u"."id" > 10)`
	if got != want {
		t.Fatalf("got  %s\nwant %s", got, want)
	}
}

// TestInterpolate_PositionalPlaceholders covers the "?" family, where markers
// are identical and order is the only thing binding a value to a position.
func TestInterpolate_PositionalPlaceholders(t *testing.T) {
	for _, d := range []render.Dialect{sqlite.Dialect{}, clickhouse.Dialect{}} {
		t.Run(render.DialectName(d), func(t *testing.T) {
			got := debug.Interpolate(d, build(t, d)).String()
			if !strings.Contains(got, `= 'ada'`) || !strings.Contains(got, `> 10`) {
				t.Fatalf("values were not substituted in order: %s", got)
			}
			if strings.Contains(got, "?") {
				t.Fatalf("a placeholder was left behind: %s", got)
			}
		})
	}
}

// TestInterpolate_DoesNotTouchQuotedRuns pins the subtle half.
//
// A question mark inside a string literal is data, not a placeholder, and
// substituting it would corrupt the very thing somebody is trying to read.
// Same for a quoted identifier containing one.
func TestInterpolate_DoesNotTouchQuotedRuns(t *testing.T) {
	q := stmt.Query{
		SQL:  `SELECT 'is this a ? or not', "we?rd" FROM t WHERE a = ? AND b = ?`,
		Args: []any{1, 2},
	}
	got := debug.Interpolate(sqlite.Dialect{}, q).String()
	want := `SELECT 'is this a ? or not', "we?rd" FROM t WHERE a = 1 AND b = 2`
	if got != want {
		t.Fatalf("got  %s\nwant %s", got, want)
	}
}

// TestInterpolate_TenArgumentsDoNotCollide pins that $1 is not matched inside
// $10, which is why numbered substitution runs highest first.
func TestInterpolate_TenArgumentsDoNotCollide(t *testing.T) {
	sel := stmt.NewSelect(postgres.Dialect{}).Exprs(uid).From(users)
	for i := 1; i <= 11; i++ {
		sel = sel.Where(expr.Eq(uid, int64(i*100)))
	}
	q, err := sel.Build()
	if err != nil {
		t.Fatal(err)
	}

	got := debug.Interpolate(postgres.Dialect{}, q).String()
	if strings.Contains(got, "$") {
		t.Fatalf("a placeholder survived, so $1 matched inside $10 or similar: %s", got)
	}
	for _, want := range []string{"= 100", "= 1000", "= 1100"} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in %s", want, got)
		}
	}
}

// TestInterpolate_EscapesValues pins that a value containing a quote comes out
// readable rather than breaking the statement it is printed into.
func TestInterpolate_EscapesValues(t *testing.T) {
	q := stmt.Query{SQL: "SELECT $1, $2, $3, $4, $5", Args: []any{
		"O'Brien", nil, true, []byte{0xde, 0xad}, 3.5,
	}}
	got := debug.Interpolate(postgres.Dialect{}, q).String()
	want := `SELECT 'O''Brien', NULL, TRUE, '\xdead', 3.5`
	if got != want {
		t.Fatalf("got  %s\nwant %s", got, want)
	}
}

// TestInterpolate_UnwrapsDriverTypes pins the two wrappers a caller actually
// passes: a named argument, and anything implementing driver.Valuer.
func TestInterpolate_UnwrapsDriverTypes(t *testing.T) {
	when := time.Date(2026, 3, 1, 12, 0, 0, 0, time.UTC)
	q := stmt.Query{SQL: "SELECT $1, $2", Args: []any{
		sql.Named("who", "ada"),
		sql.NullString{String: "maybe", Valid: true},
	}}
	got := debug.Interpolate(postgres.Dialect{}, q).String()
	if !strings.Contains(got, `'ada'`) {
		t.Errorf("a named argument was not unwrapped: %s", got)
	}
	if !strings.Contains(got, `'maybe'`) {
		t.Errorf("a driver.Valuer was not unwrapped: %s", got)
	}

	q2 := stmt.Query{SQL: "SELECT $1", Args: []any{when}}
	if g := debug.Interpolate(postgres.Dialect{}, q2).String(); !strings.Contains(g, "2026-03-01T12:00:00Z") {
		t.Errorf("a time was not formatted readably: %s", g)
	}
}

// TestInterpolate_NoArgs is the case a caller hits constantly and which must
// not be a special path with its own bugs.
func TestInterpolate_NoArgs(t *testing.T) {
	q := stmt.Query{SQL: "SELECT 1"}
	if got := debug.Interpolate(postgres.Dialect{}, q).String(); got != "SELECT 1" {
		t.Fatalf("got %q", got)
	}
}

// TestExplain pins the log-line form, which adds what the bare statement loses.
func TestExplain(t *testing.T) {
	got := debug.Explain(postgres.Dialect{}, build(t, postgres.Dialect{})).String()
	if !strings.HasSuffix(got, "-- 2 arg(s), postgres") {
		t.Fatalf("got %s", got)
	}
}
