package sqlb_test

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/ubgo/sqlb"
)

// Test fixtures mirroring a two-table schema, so tests exercise qualified
// columns and joins rather than a degenerate single-table case.
var (
	tblUser  = sqlb.Table{Name: "app_user", Alias: "u"}
	tblOrder = sqlb.Table{Name: "app_order", Alias: "o"}

	colUserID    = sqlb.Col[string](tblUser, "id")
	colUserEmail = sqlb.Col[string](tblUser, "email")
	colUserAge   = sqlb.Col[int](tblUser, "age")
	colUserSeen  = sqlb.Col[*time.Time](tblUser, "seen_at")

	colOrderID     = sqlb.Col[string](tblOrder, "id")
	colOrderUserID = sqlb.Col[string](tblOrder, "user_id")
)

// dest is a scan destination used to assert that bindings and targets stay in
// the same order.
type dest struct {
	id    string
	email string
	age   int
}

func buildOK[D sqlb.Dialect](t *testing.T, s *sqlb.Select[D]) sqlb.Query {
	t.Helper()
	q, err := s.Build()
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	return q
}

func TestSelect_RendersColumnsFromAndWhere(t *testing.T) {
	var d dest
	q := buildOK(t, sqlb.NewSelect(sqlb.PostgresDialect{}).
		Columns(sqlb.Bind(colUserID, &d.id), sqlb.Bind(colUserEmail, &d.email)).
		From(tblUser).
		Where(sqlb.Eq(colUserEmail, "a@b.com")))

	want := `SELECT "u"."id", "u"."email" FROM "app_user" AS "u" WHERE ("u"."email" = $1)`
	if q.SQL != want {
		t.Errorf("SQL\n got: %s\nwant: %s", q.SQL, want)
	}
	if len(q.Args) != 1 || q.Args[0] != "a@b.com" {
		t.Errorf("Args = %v, want [a@b.com]", q.Args)
	}
}

// TestSelect_TargetsMatchColumnOrder pins the invariant the whole package
// exists for: the SELECT list and the scan targets are derived from one slice,
// so they cannot drift when a column is inserted in the middle.
func TestSelect_TargetsMatchColumnOrder(t *testing.T) {
	var d dest
	q := buildOK(t, sqlb.NewSelect(sqlb.PostgresDialect{}).
		Columns(
			sqlb.Bind(colUserID, &d.id),
			sqlb.Bind(colUserEmail, &d.email),
			sqlb.Bind(colUserAge, &d.age),
		).
		From(tblUser))

	if got, want := len(q.Targets), 3; got != want {
		t.Fatalf("len(Targets) = %d, want %d", got, want)
	}
	for i, want := range []any{&d.id, &d.email, &d.age} {
		if q.Targets[i] != want {
			t.Errorf("Targets[%d] points at the wrong field", i)
		}
	}
	// The SELECT list must name the columns in the same positions.
	for i, col := range []string{`"u"."id"`, `"u"."email"`, `"u"."age"`} {
		if idx := strings.Index(q.SQL, col); idx < 0 {
			t.Errorf("column %s missing from SELECT list", col)
		} else if i > 0 && idx < strings.Index(q.SQL, `"u"."id"`) {
			t.Errorf("column %s appears before the first bound column", col)
		}
	}
}

func TestSelect_JoinRendersOnClause(t *testing.T) {
	var d dest
	q := buildOK(t, sqlb.NewSelect(sqlb.PostgresDialect{}).
		Columns(sqlb.Bind(colUserID, &d.id)).
		From(tblUser).
		Join(sqlb.Join{
			Type:  sqlb.JoinLeft,
			Table: tblOrder,
			On:    []sqlb.Predicate{sqlb.EqExpr(colOrderUserID, colUserID)},
		}))

	want := `LEFT JOIN "app_order" AS "o" ON ("o"."user_id" = "u"."id")`
	if !strings.Contains(q.SQL, want) {
		t.Errorf("SQL missing join\n got: %s\nwant substring: %s", q.SQL, want)
	}
	// A join condition compares two columns and must bind nothing.
	if len(q.Args) != 0 {
		t.Errorf("Args = %v, want none for a column-to-column join", q.Args)
	}
}

func TestSelect_DuplicateAliasRejected(t *testing.T) {
	var d dest
	clash := sqlb.Table{Name: "other", Alias: tblUser.Alias}
	_, err := sqlb.NewSelect(sqlb.PostgresDialect{}).
		Columns(sqlb.Bind(colUserID, &d.id)).
		From(tblUser).
		Join(sqlb.Join{Type: sqlb.JoinInner, Table: clash}).
		Build()

	if !errors.Is(err, sqlb.ErrDuplicateAlias) {
		t.Fatalf("err = %v, want ErrDuplicateAlias", err)
	}
	// The message must name both offenders, or a plugin author cannot tell
	// which of their tables collided with what.
	if !strings.Contains(err.Error(), "app_user") || !strings.Contains(err.Error(), "other") {
		t.Errorf("error should name both tables, got: %v", err)
	}
}

func TestSelect_MissingClausesRejected(t *testing.T) {
	var d dest
	tests := []struct {
		name string
		sel  *sqlb.Select[sqlb.PostgresDialect]
		want error
	}{
		{"no columns", sqlb.NewSelect(sqlb.PostgresDialect{}).From(tblUser), sqlb.ErrNoColumns},
		{"join without table", sqlb.NewSelect(sqlb.PostgresDialect{}).Columns(sqlb.Bind(colUserID, &d.id)).Join(sqlb.Join{Type: sqlb.JoinInner, Table: tblUser}), sqlb.ErrJoinWithoutFrom},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := tc.sel.Build(); !errors.Is(err, tc.want) {
				t.Errorf("err = %v, want %v", err, tc.want)
			}
		})
	}
}

// TestIn_EmptyListFailsClosed pins the fail-closed choice. Silently dropping an
// empty IN would widen the result set, which in an auth query means returning
// rows the caller asked to exclude.
func TestIn_EmptyListFailsClosed(t *testing.T) {
	var d dest
	tests := []struct {
		name string
		pred sqlb.Predicate
		want string
	}{
		{"empty IN renders FALSE", sqlb.In(colUserEmail), "FALSE"},
		{"empty NOT IN renders TRUE", sqlb.NotIn(colUserEmail), "TRUE"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			q := buildOK(t, sqlb.NewSelect(sqlb.PostgresDialect{}).
				Columns(sqlb.Bind(colUserID, &d.id)).From(tblUser).Where(tc.pred))
			if !strings.Contains(q.SQL, tc.want) {
				t.Errorf("SQL = %s, want to contain %s", q.SQL, tc.want)
			}
			if len(q.Args) != 0 {
				t.Errorf("Args = %v, want none", q.Args)
			}
		})
	}
}

func TestIn_BindsEveryValue(t *testing.T) {
	var d dest
	q := buildOK(t, sqlb.NewSelect(sqlb.PostgresDialect{}).
		Columns(sqlb.Bind(colUserID, &d.id)).From(tblUser).
		Where(sqlb.In(colUserEmail, "a@x.com", "b@x.com", "c@x.com")))

	if !strings.Contains(q.SQL, `IN ($1, $2, $3)`) {
		t.Errorf("SQL = %s, want three placeholders", q.SQL)
	}
	if len(q.Args) != 3 {
		t.Errorf("len(Args) = %d, want 3", len(q.Args))
	}
}

// TestGroup_AlwaysParenthesised guards against a precedence bug where an OR
// nested inside an AND silently widens a security check.
func TestGroup_AlwaysParenthesised(t *testing.T) {
	var d dest
	q := buildOK(t, sqlb.NewSelect(sqlb.PostgresDialect{}).
		Columns(sqlb.Bind(colUserID, &d.id)).From(tblUser).
		Where(sqlb.Or(sqlb.Eq(colUserEmail, "a"), sqlb.Eq(colUserEmail, "b"))))

	// The Or group and the enclosing Where group each contribute parentheses.
	if !strings.Contains(q.SQL, `(("u"."email" = $1 OR "u"."email" = $2))`) {
		t.Errorf("SQL = %s, want a parenthesised OR group", q.SQL)
	}
}

func TestNullChecks(t *testing.T) {
	var d dest
	q := buildOK(t, sqlb.NewSelect(sqlb.PostgresDialect{}).
		Columns(sqlb.Bind(colUserID, &d.id)).From(tblUser).
		Where(sqlb.IsNull(colUserSeen), sqlb.IsNotNull(colUserEmail)))

	for _, want := range []string{`"u"."seen_at" IS NULL`, `"u"."email" IS NOT NULL`} {
		if !strings.Contains(q.SQL, want) {
			t.Errorf("SQL = %s, want to contain %s", q.SQL, want)
		}
	}
	if len(q.Args) != 0 {
		t.Errorf("Args = %v, want none for null checks", q.Args)
	}
}

// TestDialects_DifferOnlyInPlaceholders pins the claim that swapping engines
// changes placeholders and nothing else.
func TestDialects_DifferOnlyInPlaceholders(t *testing.T) {
	build := func(d sqlb.Dialect) string {
		var dst dest
		return buildOK(t, sqlb.NewSelect(d).
			Columns(sqlb.Bind(colUserID, &dst.id)).From(tblUser).
			Where(sqlb.Eq(colUserEmail, "a@b.com"))).SQL
	}
	pg, lite := build(sqlb.PostgresDialect{}), build(sqlb.SQLiteDialect{})

	if !strings.HasSuffix(pg, "$1)") {
		t.Errorf("postgres SQL = %s, want a numbered placeholder", pg)
	}
	if !strings.HasSuffix(lite, "?)") {
		t.Errorf("sqlite SQL = %s, want a positional placeholder", lite)
	}
	if strings.ReplaceAll(pg, "$1", "?") != lite {
		t.Errorf("dialects differ beyond placeholders:\n pg:   %s\n lite: %s", pg, lite)
	}
}

// TestLimitOffset_AreBound guards against interpolating pagination values,
// which become request-controlled the moment an admin endpoint exists.
func TestLimitOffset_AreBound(t *testing.T) {
	var d dest
	q := buildOK(t, sqlb.NewSelect(sqlb.PostgresDialect{}).
		Columns(sqlb.Bind(colUserID, &d.id)).From(tblUser).Limit(50).Offset(100))

	if !strings.Contains(q.SQL, "LIMIT $1") || !strings.Contains(q.SQL, "OFFSET $2") {
		t.Errorf("SQL = %s, want bound LIMIT and OFFSET", q.SQL)
	}
	if len(q.Args) != 2 || q.Args[0] != 50 || q.Args[1] != 100 {
		t.Errorf("Args = %v, want [50 100]", q.Args)
	}
}

func TestOrderBy(t *testing.T) {
	var d dest
	q := buildOK(t, sqlb.NewSelect(sqlb.PostgresDialect{}).
		Columns(sqlb.Bind(colUserID, &d.id)).From(tblUser).
		Order(sqlb.OrderBy(colUserAge, sqlb.SortDesc), sqlb.OrderBy(colUserEmail, sqlb.SortAsc)))

	if !strings.Contains(q.SQL, `ORDER BY "u"."age" DESC, "u"."email" ASC`) {
		t.Errorf("SQL = %s, want both ordering terms", q.SQL)
	}
}

func TestLower_WrapsExpression(t *testing.T) {
	var d dest
	q := buildOK(t, sqlb.NewSelect(sqlb.PostgresDialect{}).
		Columns(sqlb.Bind(colUserID, &d.id)).From(tblUser).
		Where(sqlb.Eq(sqlb.Lower(colUserEmail), "a@b.com")))

	if !strings.Contains(q.SQL, `lower("u"."email") = $1`) {
		t.Errorf("SQL = %s, want a lowered comparison", q.SQL)
	}
}

func TestCoalesce(t *testing.T) {
	var d dest
	q := buildOK(t, sqlb.NewSelect(sqlb.PostgresDialect{}).
		Columns(sqlb.Bind(sqlb.Coalesce(colUserEmail, sqlb.Literal[string]("''")), &d.email)).
		From(tblUser))

	if !strings.Contains(q.SQL, `COALESCE("u"."email", '')`) {
		t.Errorf("SQL = %s, want a COALESCE expression", q.SQL)
	}
}

func TestWhere_Accumulates(t *testing.T) {
	var d dest
	// Layering an optional filter must not drop the base query's conditions,
	// which for an auth statement would widen a security check.
	q := buildOK(t, sqlb.NewSelect(sqlb.PostgresDialect{}).
		Columns(sqlb.Bind(colUserID, &d.id)).From(tblUser).
		Where(sqlb.Eq(colUserEmail, "a")).
		Where(sqlb.Greater(colUserAge, 18)))

	if len(q.Args) != 2 {
		t.Errorf("Args = %v, want both predicates retained", q.Args)
	}
}

func TestQuoteIdent_DoublesEmbeddedQuotes(t *testing.T) {
	got := sqlb.PostgresDialect{}.QuoteIdent(`we"ird`)
	if want := `"we""ird"`; got != want {
		t.Errorf("QuoteIdent = %s, want %s", got, want)
	}
}

func TestBindings_ConcatenatesInOrder(t *testing.T) {
	var d dest
	got := sqlb.Bindings(
		[]sqlb.Binding{sqlb.Bind(colUserID, &d.id)},
		[]sqlb.Binding{sqlb.Bind(colUserEmail, &d.email), sqlb.Bind(colUserAge, &d.age)},
	)
	targets := sqlb.Targets(got)
	for i, want := range []any{&d.id, &d.email, &d.age} {
		if targets[i] != want {
			t.Errorf("Targets[%d] out of order", i)
		}
	}
}

func TestOrderTerm_UsesJoinedTableColumn(t *testing.T) {
	var d dest
	q := buildOK(t, sqlb.NewSelect(sqlb.PostgresDialect{}).
		Columns(sqlb.Bind(colUserID, &d.id)).
		From(tblUser).
		Join(sqlb.Join{Type: sqlb.JoinInner, Table: tblOrder,
			On: []sqlb.Predicate{sqlb.EqExpr(colOrderUserID, colUserID)}}).
		Order(sqlb.OrderBy(colOrderID, sqlb.SortAsc)))

	if !strings.Contains(q.SQL, `ORDER BY "o"."id" ASC`) {
		t.Errorf("SQL = %s, want ordering by the joined table", q.SQL)
	}
}
