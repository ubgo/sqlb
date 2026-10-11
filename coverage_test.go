package sqlb

import (
	"errors"
	"strings"
	"testing"
	"time"
)

// Pinning tests for the core helpers no catalogue example happens to reach.
//
// Why they exist: the catalogues under examples/ cover what their source
// documents contained, which leaves a handful of constructors with no
// assertion on their rendered SQL. A constructor with no assertion is where a
// wrong operator or a copied-and-not-edited keyword survives review.

var (
	covTable = Table{Name: "t", Alias: "t"}
	covName  = Col[string](covTable, "name")
	covAge   = Col[int32](covTable, "age")
	covAt    = Col[time.Time](covTable, "at")
)

// NotLike and RowGreater are the negated and row-wise members of families
// whose other members the catalogues use. They are pinned together because
// the failure they guard against is the same: a family member that renders
// its sibling's operator.
func TestOps_NegatedAndRowWise(t *testing.T) {
	var dst string
	q, err := NewSelect(PostgresDialect{}).
		Columns(Bind(covName, &dst)).
		From(covTable).
		Where(NotLike(covName, "a%"), RowGreater(Row(covName, covAge), Row(Param("b"), Param(int32(3))))).
		Build()
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	want := `SELECT "t"."name" FROM "t" AS "t" WHERE ("t"."name" NOT LIKE $1 AND ("t"."name", "t"."age") > ($2, $3))`
	if q.SQL != want {
		t.Fatalf("got  %s\nwant %s", q.SQL, want)
	}
}

// WithOrder and WithClause are the two Option constructors the cookbook does
// not use. The invariant every Option shares is that it only ADDS: applying
// one must not drop what the library already put on the statement. That is
// what this pins, by checking the library's own ORDER BY survives.
func TestOptions_OnlyAdd(t *testing.T) {
	var dst string
	sel := NewSelect(PostgresDialect{}).
		Columns(Bind(covName, &dst)).
		From(covTable).
		Order(OrderBy(covName, SortAsc))

	// A clause carries a dialect and an option does not, so a clause is added
	// with Clause on the statement Apply returns, where the compiler checks it.
	sel = sel.Apply(
		WithOrder(OrderBy(covAge, SortDesc)),
	).Clause(tailClause("FOR UPDATE"))

	q, err := sel.Build()
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	want := `SELECT "t"."name" FROM "t" AS "t" ORDER BY "t"."name" ASC, "t"."age" DESC FOR UPDATE`
	if q.SQL != want {
		t.Fatalf("got  %s\nwant %s", q.SQL, want)
	}
}

// tailClause is the smallest Clause implementation: a fixed fragment in the
// tail slot. Clause is an open interface so an engine package can add one
// without a method on Select, which means a test can implement it too.
type tailClause string

func (tailClause) Slot() Slot { return SlotTail }

// ClauseOf types the test clause to the dialect and kind the tests use.
func (tailClause) ClauseOf(PostgresDialect, SelectKind) {}

func (c tailClause) WriteSQL(b *strings.Builder, _ *Args) { b.WriteString(string(c)) }

// FollowingExpr takes an expression as a frame bound, for the RANGE frames
// where the offset is an interval rather than a row count. Preceding and
// Following take an int and are covered by the catalogues; this one is not,
// and it renders a caller-supplied fragment, so it is worth an assertion.
func TestWindow_FollowingExpr(t *testing.T) {
	var dst int64
	w := NewWindow().
		OrderBy(OrderBy(covAt, SortAsc)).
		Frame(FrameRange, CurrentRow(), FollowingExpr("INTERVAL '7 days'"))
	q, err := NewSelect(PostgresDialect{}).
		Columns(Bind(Over(CountOf(covName), w), &dst)).
		From(covTable).
		Build()
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	want := `SELECT count("t"."name") OVER (ORDER BY "t"."at" ASC RANGE BETWEEN CURRENT ROW AND INTERVAL '7 days' FOLLOWING) FROM "t" AS "t"`
	if q.SQL != want {
		t.Fatalf("got  %s\nwant %s", q.SQL, want)
	}
}

// Every builder exposes Dialect() so an Option or a helper written against
// one statement can ask which engine it is rendering for without the caller
// threading it through. Pinned because the accessor is trivial to get wrong
// by returning a zero value, which would make a helper silently choose the
// wrong branch rather than fail.
func TestBuilders_ExposeTheirDialect(t *testing.T) {
	d := PostgresDialect{}
	if got := NewSelect(d).Dialect(); DialectName(got) != DialectPostgres {
		t.Errorf("Select.Dialect() = %v", got)
	}
	if got := NewInsert(d).Dialect(); DialectName(got) != DialectPostgres {
		t.Errorf("Insert.Dialect() = %v", got)
	}
	if got := NewUpdate(d).Dialect(); DialectName(got) != DialectPostgres {
		t.Errorf("Update.Dialect() = %v", got)
	}
	if got := NewDelete(d).Dialect(); DialectName(got) != DialectPostgres {
		t.Errorf("Delete.Dialect() = %v", got)
	}
}

// Expr.Table reports the table an expression came from, which is what lets a
// builder detect a join onto a source that is not in the statement. It
// returns the zero Table for an expression that is not a column, and that
// distinction is the whole point of the accessor.
func TestExpr_Table(t *testing.T) {
	if got := covName.Table(); got.Name != "t" || got.Alias != "t" {
		t.Errorf("column expression lost its table: %+v", got)
	}
	if got := Param("x").Table(); got.Name != "" || got.Alias != "" {
		t.Errorf("a parameter must report no table, got %+v", got)
	}
}

// Insert.With attaches a CTE to an INSERT, the shape behind "insert the rows
// this query found". Pinned because the clause has to render before INSERT
// rather than inside it, and a builder that appends it in the wrong place
// produces SQL the database rejects.
func TestInsert_With(t *testing.T) {
	src := Table{Name: "staging", Alias: "s"}
	srcName := Col[string](src, "name")
	picked := Table{Name: "picked", Alias: "picked"}

	body := NewSelect(PostgresDialect{}).Exprs(srcName).From(src)
	q, err := NewInsert(PostgresDialect{}).
		Into(covTable).
		With("picked", body).
		Columns(covName).
		Select(NewSelect(PostgresDialect{}).Exprs(Col[string](picked, "name")).From(picked)).
		Build()
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	want := `WITH "picked" AS (SELECT "s"."name" FROM "staging" AS "s") INSERT INTO "t" AS "t" ("name") SELECT "picked"."name" FROM "picked" AS "picked"`
	if q.SQL != want {
		t.Fatalf("got  %s\nwant %s", q.SQL, want)
	}
}

// The fail-closed arms of the write builders.
//
// Why these matter more than their coverage percentage suggests: every one is
// a mistake the builder catches before the statement reaches a database, and
// that catching is most of what this package sells. An arm that stops firing
// does not announce itself; the statement simply builds, and the database
// reports something less specific much later.
func TestWriteBuilders_FailClosed(t *testing.T) {
	// A row whose width does not match the column list. The builder cannot
	// know which value was meant for which column, so it refuses rather than
	// aligning them by position and writing the wrong thing.
	t.Run("row width", func(t *testing.T) {
		// The first Row fixes the column list at two; the second supplies one.
		ins := NewInsert(PostgresDialect{}).Into(covTable).
			Row(Set(covName, "a"), Set(covAge, int32(1))).
			Row(Set(covName, "b"))
		if _, err := ins.Build(); !errors.Is(err, ErrRowWidth) {
			t.Fatalf("want ErrRowWidth, got %v", err)
		}
	})

	// An INSERT with neither rows nor a source query has nothing to write.
	t.Run("no assignments", func(t *testing.T) {
		// An INSERT with no body no longer reaches Build; see examples/typecheck.
		if _, err := NewUpdate(PostgresDialect{}).Table(covTable).All().Build(); !errors.Is(err, ErrNoAssignments) {
			t.Fatalf("update: want ErrNoAssignments, got %v", err)
		}
	})

	// Columns takes expressions so a caller can pass the same values it binds
	// elsewhere, but only a column has a name to write into. Anything else is
	// rejected rather than rendered as an empty identifier.
	t.Run("not a column", func(t *testing.T) {
		src := Table{Name: "src", Alias: "s"}
		ins := NewInsert(PostgresDialect{}).Into(covTable).
			Columns(Param("not a column")).
			Select(NewSelect(PostgresDialect{}).Exprs(Col[string](src, "name")).From(src))
		if _, err := ins.Build(); !errors.Is(err, ErrNotAColumn) {
			t.Fatalf("want ErrNotAColumn, got %v", err)
		}
	})

	// An UPDATE or DELETE with no WHERE and no explicit All() is refused. The
	// whole-table case is occasionally intended, so the builder makes the
	// author write it down rather than inferring it from an absence.
	t.Run("no where", func(t *testing.T) {
		// UPDATE and DELETE with no WHERE and no All() no longer reach Build:
		// Build exists only on the type Where and All return. The negative
		// build in examples/typecheck pins it; All() alone still builds.
		if _, err := NewUpdate(PostgresDialect{}).Table(covTable).Set(Set(covName, "x")).All().Build(); err != nil {
			t.Fatalf("update with All(): %v", err)
		}
		if _, err := NewDelete(PostgresDialect{}).From(covTable).All().Build(); err != nil {
			t.Fatalf("delete with All(): %v", err)
		}
	})

	// Delete.With attaches a CTE, the "delete the rows this query found"
	// shape. Pinned for the same reason as Insert.With: the clause renders
	// before the statement keyword, not inside it.
	t.Run("delete with cte", func(t *testing.T) {
		src := Table{Name: "stale", Alias: "s"}
		body := NewSelect(PostgresDialect{}).Exprs(Col[string](src, "id")).From(src)
		q, err := NewDelete(PostgresDialect{}).From(covTable).
			With("stale_ids", body).
			Where(Eq(covName, "x")).
			Build()
		if err != nil {
			t.Fatalf("build: %v", err)
		}
		want := `WITH "stale_ids" AS (SELECT "s"."id" FROM "stale" AS "s") DELETE FROM "t" AS "t" WHERE ("t"."name" = $1)`
		if q.SQL != want {
			t.Fatalf("got  %s\nwant %s", q.SQL, want)
		}
	})
}

// An empty predicate group renders the constant rather than an empty
// fragment. This is what makes And() and Or() safe to build from a filter
// list that turned out to be empty: the statement stays valid SQL and matches
// everything, instead of producing "WHERE " and a syntax error.
func TestGroup_EmptyRendersTrue(t *testing.T) {
	var dst string
	q, err := NewSelect(PostgresDialect{}).
		Columns(Bind(covName, &dst)).
		From(covTable).
		Where(And()).
		Build()
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	if want := `SELECT "t"."name" FROM "t" AS "t" WHERE (TRUE)`; q.SQL != want {
		t.Fatalf("got  %s\nwant %s", q.SQL, want)
	}
}

// A JOIN USING with more than one column, and more than one named window.
// Both are the "second item" arm of a comma-separated list, the arm a single
// example never reaches and where a missing separator produces SQL that is
// wrong rather than absent.
func TestLists_SecondItemSeparators(t *testing.T) {
	other := Table{Name: "o", Alias: "o"}
	var dst string
	q, err := NewSelect(PostgresDialect{}).
		Columns(Bind(covName, &dst)).
		From(covTable).
		Join(Join{Type: JoinInner, Table: other, Using: []string{"name", "age"}}).
		Window("w1", NewWindow().PartitionBy(covName)).
		Window("w2", NewWindow().PartitionBy(covAge)).
		Build()
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	want := `SELECT "t"."name" FROM "t" AS "t" INNER JOIN "o" AS "o" USING ("name", "age") WINDOW "w1" AS (PARTITION BY "t"."name"), "w2" AS (PARTITION BY "t"."age")`
	if q.SQL != want {
		t.Fatalf("got  %s\nwant %s", q.SQL, want)
	}
}

// Update.Columns rejects a non-column the same way Insert.Columns does. The
// two builders have separate code paths for it, so one can be fixed and the
// other left behind.
func TestUpdate_NotAColumn(t *testing.T) {
	// Set takes the target's name, and only a column has one. An expression
	// assigned to yields an empty name, which the builder refuses rather
	// than rendering as `SET  = $1`.
	up := NewUpdate(PostgresDialect{}).Table(covTable).
		Set(Set(Param("not a column"), "v")).
		All()
	if _, err := up.Build(); !errors.Is(err, ErrNotAColumn) {
		t.Fatalf("want ErrNotAColumn, got %v", err)
	}
}
