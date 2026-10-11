// Tests for the builder's composition layer: bound parameters, the
// unsupported-construct path, clause slots, options, GROUP BY, joins
// without ON, subquery argument numbering, and the write builders'
// guards. sqlb_test.go holds the older expression and select tests.
package sqlb

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

var (
	tUser  = Table{Name: "auth_user", Alias: "u"}
	cID    = Col[string](tUser, "id")
	cAt    = Col[time.Time](tUser, "created_at")
	cScore = Col[float64](tUser, "score")
)

type builder interface{ Build() (Query, error) }

func mustBuild(t *testing.T, s builder) Query {
	t.Helper()
	q, err := s.Build()
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	return q
}

// Param is the same render path as a value comparison: Eq(col, v) and
// EqExpr(col, Param(v)) must be byte-identical.
func TestParam_UnifiesValueAndExpressionComparison(t *testing.T) {
	var id string
	a := mustBuild(t, NewSelect(PostgresDialect{}).Columns(Bind(cID, &id)).From(tUser).Where(Eq(cID, "x")))
	b := mustBuild(t, NewSelect(PostgresDialect{}).Columns(Bind(cID, &id)).From(tUser).Where(EqExpr(cID, Param("x"))))
	if a.SQL != b.SQL || len(a.Args) != 1 || a.Args[0] != "x" {
		t.Fatalf("Eq and EqExpr(Param) differ:\n%s\n%s", a.SQL, b.SQL)
	}
}

// A parameter bound inside an expression takes its placeholder in render
// order, interleaved with predicate parameters.
func TestParam_NumbersInRenderOrder(t *testing.T) {
	var s float64
	plus := New[float64](func(a *Args) string { return cScore.SQL(a) + " + " + a.Next(1.5) })
	q := mustBuild(t, NewSelect(PostgresDialect{}).Columns(Bind(plus, &s)).From(tUser).Where(Eq(cID, "x")).Limit(3))
	want := `SELECT "u"."score" + $1 FROM "auth_user" AS "u" WHERE ("u"."id" = $2) LIMIT $3`
	if q.SQL != want {
		t.Fatalf("got  %s\nwant %s", q.SQL, want)
	}
	if len(q.Args) != 3 || q.Args[0] != 1.5 || q.Args[1] != "x" || q.Args[2] != 3 {
		t.Fatalf("args %v", q.Args)
	}
}

// Args.Unsupported makes Build fail with ErrUnsupportedDialect, naming the
// construct and the dialect. The first error wins.
func TestUnsupported_FailsBuildWithNames(t *testing.T) {
	var s string
	bad := New[string](func(a *Args) string { a.Unsupported("x.First"); a.Unsupported("x.Second"); return "" })
	_, err := NewSelect(SQLiteDialect{}).Columns(Bind(bad, &s)).From(tUser).Build()
	if !errors.Is(err, ErrUnsupportedDialect) {
		t.Fatalf("want ErrUnsupportedDialect, got %v", err)
	}
	msg := err.Error()
	if !strings.Contains(msg, "x.First") || !strings.Contains(msg, `"sqlite"`) || strings.Contains(msg, "x.Second") {
		t.Fatalf("message: %s", msg)
	}
}

func TestDialectName_UnknownForUnnamedDialect(t *testing.T) {
	if DialectName(PostgresDialect{}) != DialectPostgres {
		t.Fatalf("named dialect must report its name")
	}
	if DialectName(unnamed{}) != DialectUnknown {
		t.Fatalf("unnamed dialect must report %q", DialectUnknown)
	}
}

type unnamed struct{}

func (unnamed) Placeholder(int) string         { return "?" }
func (unnamed) QuoteIdent(ident string) string { return ident }

type testClause struct {
	slot Slot
	text string
}

func (c testClause) Slot() Slot                           { return c.slot }
func (testClause) ClauseOf(SQLiteDialect, SelectKind)     {}
func (c testClause) WriteSQL(b *strings.Builder, _ *Args) { b.WriteString(c.text) }

// Every slot is written at its position, and insertion order within a slot
// is preserved regardless of the order clauses were added.
func TestClause_SlotsWriteInStatementOrder(t *testing.T) {
	var id string
	q := mustBuild(t, NewSelect(SQLiteDialect{}).
		Clause(testClause{SlotTail, "TAIL"}, testClause{SlotAfterSelect, "AFTER_SELECT"}).
		Columns(Bind(cID, &id)).
		From(tUser).
		Clause(testClause{SlotAfterFrom, "AFTER_FROM_1"}, testClause{SlotAfterFrom, "AFTER_FROM_2"}, testClause{SlotBeforeWhere, "BEFORE_WHERE"}).
		Where(Eq(cID, "x")).
		Order(OrderBy(cAt, SortAsc)).
		Clause(testClause{SlotAfterOrder, "AFTER_ORDER"}).
		Limit(1))
	want := `SELECT AFTER_SELECT "u"."id" FROM "auth_user" AS "u" AFTER_FROM_1 AFTER_FROM_2 BEFORE_WHERE WHERE ("u"."id" = ?) ORDER BY "u"."created_at" ASC AFTER_ORDER LIMIT ? TAIL`
	if q.SQL != want {
		t.Fatalf("got  %s\nwant %s", q.SQL, want)
	}
}

// Options only add. A library predicate written before Apply survives every
// option, and option bindings land after the library's.
func TestApply_OptionsOnlyAdd(t *testing.T) {
	tSub := Table{Name: "app_subscription", Alias: "s"}
	cSubUser := Col[string](tSub, "user_id")
	cPlan := Col[string](tSub, "plan")
	var id, plan string
	q := mustBuild(t, NewSelect(PostgresDialect{}).
		Columns(Bind(cID, &id)).
		From(tUser).
		Where(IsNull(cAt)).
		Apply(
			WithJoin(Join{Type: JoinInner, Table: tSub, On: []Predicate{EqExpr(cSubUser, cID)}}),
			WithWhere(Eq(cPlan, "pro")),
			WithColumns(Bind(cPlan, &plan)),
		))
	want := `SELECT "u"."id", "s"."plan" FROM "auth_user" AS "u" INNER JOIN "app_subscription" AS "s" ON ("s"."user_id" = "u"."id") WHERE ("u"."created_at" IS NULL AND "s"."plan" = $1)`
	if q.SQL != want {
		t.Fatalf("got  %s\nwant %s", q.SQL, want)
	}
	if q.Targets[0] != &id || q.Targets[1] != &plan {
		t.Fatalf("targets out of order")
	}
}

func TestGroupBy_RendersBeforeOrder(t *testing.T) {
	var id string
	q := mustBuild(t, NewSelect(PostgresDialect{}).Columns(Bind(cID, &id)).From(tUser).GroupBy(cID, cAt).Order(OrderBy(cID, SortAsc)))
	want := `SELECT "u"."id" FROM "auth_user" AS "u" GROUP BY "u"."id", "u"."created_at" ORDER BY "u"."id" ASC`
	if q.SQL != want {
		t.Fatalf("got  %s\nwant %s", q.SQL, want)
	}
}

// A Join with no On renders no ON clause; ClickHouse ARRAY JOIN and CROSS
// JOIN need that, and an accidental empty On on an INNER join is a cross
// product the database will happily return, so it is documented here.
func TestJoin_EmptyOnRendersNoOnClause(t *testing.T) {
	tX := Table{Name: "x", Alias: "x"}
	var id string
	q := mustBuild(t, NewSelect(SQLiteDialect{}).Columns(Bind(cID, &id)).From(tUser).Join(Join{Type: JoinInner, Table: tX}))
	if strings.Contains(q.SQL, " ON ") {
		t.Fatalf("unexpected ON: %s", q.SQL)
	}
}

// A select list with an item that has no scan target builds, for use as a
// subquery, but One and Each refuse to run it.
func TestExprs_BuildsButCannotScan(t *testing.T) {
	q := mustBuild(t, NewSelect(SQLiteDialect{}).Exprs(cID).From(tUser))
	if q.Targets != nil {
		t.Fatalf("targets must be nil for Exprs items")
	}
	if err := One(context.Background(), nil, q); !errors.Is(err, ErrNoTargets) {
		t.Fatalf("One: want ErrNoTargets, got %v", err)
	}
	if err := Each(context.Background(), nil, q, func() error { return nil }); !errors.Is(err, ErrNoTargets) {
		t.Fatalf("Each: want ErrNoTargets, got %v", err)
	}
}

// A subquery shares the parent's placeholder numbering.
func TestSubquery_SharesArgNumbering(t *testing.T) {
	inner := NewSelect(PostgresDialect{}).Exprs(cID).From(tUser).Where(Eq(cID, "inner"))
	var id string
	q := mustBuild(t, NewSelect(PostgresDialect{}).Columns(Bind(cID, &id)).From(tUser).
		Where(Eq(cID, "outer"), InSubquery(cID, inner)).Limit(1))
	want := `SELECT "u"."id" FROM "auth_user" AS "u" WHERE ("u"."id" = $1 AND "u"."id" IN (SELECT "u"."id" FROM "auth_user" AS "u" WHERE ("u"."id" = $2))) LIMIT $3`
	if q.SQL != want {
		t.Fatalf("got  %s\nwant %s", q.SQL, want)
	}
	if len(q.Args) != 3 || q.Args[0] != "outer" || q.Args[1] != "inner" || q.Args[2] != 1 {
		t.Fatalf("args %v", q.Args)
	}
}

// UPDATE and DELETE without WHERE fail unless All() says the whole table is
// the target. INSERT renders the alias only for a dialect that allows it.
func TestWrites_WhereOrAllAndInsertAlias(t *testing.T) {
	// An UPDATE or DELETE with no WHERE and no All() no longer reaches Build:
	// Build is only defined on the type Where and All return, so the mistake
	// is a compile error, pinned in examples/typecheck.
	q := mustBuild(t, NewDelete(SQLiteDialect{}).From(tUser).All())
	if q.SQL != `DELETE FROM "auth_user" AS "u"` {
		t.Fatalf("delete all: %s", q.SQL)
	}
	var id string
	pgq := mustBuild(t, InsertReturning(NewInsert(PostgresDialect{}).Into(tUser).Row(Set(cID, "x")), Bind(cID, &id)))
	if pgq.SQL != `INSERT INTO "auth_user" AS "u" ("id") VALUES ($1) RETURNING "u"."id"` {
		t.Fatalf("pg insert: %s", pgq.SQL)
	}
	liteq := mustBuild(t, InsertReturning(NewInsert(SQLiteDialect{}).Into(tUser).Row(Set(cID, "x")), Bind(cID, &id)))
	if liteq.SQL != `INSERT INTO "auth_user" ("id") VALUES (?) RETURNING "id"` {
		t.Fatalf("sqlite insert: %s", liteq.SQL)
	}
	// RETURNING on a dialect without it is a compile error now: InsertReturning
	// is constrained to ReturningDialect. examples/typecheck pins it.
}

// A SELECT with no FROM evaluates expressions; every target engine accepts it.
func TestSelect_FromIsOptional(t *testing.T) {
	var n int
	q := mustBuild(t, NewSelect(PostgresDialect{}).Columns(Bind(Param(1), &n)))
	if q.SQL != `SELECT $1` {
		t.Fatalf("got %s", q.SQL)
	}
}
