package pg_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/ubgo/sqlb"
	"github.com/ubgo/sqlb/pg"
)

var (
	trA = sqlb.Table{Name: "events", Alias: "e"}
	trB = sqlb.Table{Name: "sessions", Alias: "s"}
)

func truncSQL(t *testing.T, tr *pg.Truncate) string {
	t.Helper()
	q, err := tr.Build()
	if err != nil {
		t.Fatal(err)
	}
	return q.SQL
}

// TestTruncate_Renders covers every option, because each one changes what the
// statement destroys and none of them is recoverable.
func TestTruncate_Renders(t *testing.T) {
	for _, tc := range []struct {
		name string
		tr   *pg.Truncate
		want string
	}{
		{"one table", pg.NewTruncate(pg.Dialect{}, trA),
			`TRUNCATE TABLE "events"`},
		{"several at once", pg.NewTruncate(pg.Dialect{}, trA, trB),
			`TRUNCATE TABLE "events", "sessions"`},
		{"only", pg.NewTruncate(pg.Dialect{}, trA).Only(),
			`TRUNCATE TABLE ONLY "events"`},
		{"restart identity", pg.NewTruncate(pg.Dialect{}, trA).RestartIdentity(),
			`TRUNCATE TABLE "events" RESTART IDENTITY`},
		{"cascade", pg.NewTruncate(pg.Dialect{}, trA).Cascade(),
			`TRUNCATE TABLE "events" CASCADE`},
		{"restrict", pg.NewTruncate(pg.Dialect{}, trA).Restrict(),
			`TRUNCATE TABLE "events" RESTRICT`},
		// CONTINUE IDENTITY is the default, so writing it out changes nothing
		// about what runs. It is a constructor because a statement that says
		// which it means reads better in a review than one that relies on the
		// reader knowing the default.
		{"continue identity", pg.NewTruncate(pg.Dialect{}, trA).ContinueIdentity(),
			`TRUNCATE TABLE "events" CONTINUE IDENTITY`},
		{"everything", pg.NewTruncate(pg.Dialect{}).
			Table(trA, trB).Only().RestartIdentity().Cascade(),
			`TRUNCATE TABLE ONLY "events", "sessions" RESTART IDENTITY CASCADE`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := truncSQL(t, tc.tr); got != tc.want {
				t.Fatalf("got  %s\nwant %s", got, tc.want)
			}
		})
	}
}

// TestTruncate_NoTablesIsRefused pins that an empty statement fails at Build
// rather than at the database, which is the rule every construct here follows.
func TestTruncate_NoTablesIsRefused(t *testing.T) {
	_, err := pg.NewTruncate(pg.Dialect{}).Build()
	if !errors.Is(err, pg.ErrNoTables) {
		t.Fatalf("want ErrNoTables, got %v", err)
	}
}

// TestTruncate_FailsUnderAnotherDialect pins the one path on which another engine's
// dialect still reaches this statement. Passing one to the constructor does
// not compile, since the dialect parameter is pg.Dialect, and
// examples/typecheck holds that negative build. But the statement satisfies
// sqlb.Statement, so it can be embedded as a CTE body in a statement of any
// engine, and there the guard inside WriteSQL is what refuses it.
func TestTruncate_FailsUnderAnotherDialect(t *testing.T) {
	_, err := sqlb.NewSelect(sqlb.SQLiteDialect{}).With("x", pg.NewTruncate(pg.Dialect{}, trA)).Exprs(sqlb.Literal[int]("1")).Build()
	if !errors.Is(err, sqlb.ErrUnsupportedDialect) {
		t.Fatalf("want ErrUnsupportedDialect, got %v", err)
	}
	if !strings.Contains(err.Error(), "pg.Truncate") {
		t.Errorf("the error should name the construct: %v", err)
	}
}

// TestTruncate_IsAStatement pins that it satisfies sqlb.Statement, so it can be
// a CTE body like any other statement kind.
//
// This is the second worked example of a statement kind written outside the
// stmt package, after pg.Merge. Nothing in stmt changed to allow either, which
// is the claim the architecture makes about whole statement kinds.
func TestTruncate_IsAStatement(t *testing.T) {
	var st sqlb.Statement = pg.NewTruncate(pg.Dialect{}, trA)

	// Rendering it through the Statement interface must give the same SQL as
	// Build, or it satisfies the interface without being usable through it.
	a := sqlb.NewArgs(pg.Dialect{})
	var b strings.Builder
	st.WriteSQL(&b, a)
	if err := a.Err(); err != nil {
		t.Fatal(err)
	}
	if got, want := b.String(), `TRUNCATE TABLE "events"`; got != want {
		t.Fatalf("got  %s\nwant %s", got, want)
	}
}
