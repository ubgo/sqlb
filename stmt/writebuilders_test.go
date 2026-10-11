package stmt_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/ubgo/sqlb/dialect/postgres"
	"github.com/ubgo/sqlb/expr"
	"github.com/ubgo/sqlb/render"
	"github.com/ubgo/sqlb/stmt"
)

// pgSelect, pgInsert and the rest keep the dialect out of each case below, so
// the tests read as the statements they are about.
func pgInsert() *stmt.Insert[postgres.Dialect] { return stmt.NewInsert(postgres.Dialect{}) }
func pgUpdate() *stmt.Update[postgres.Dialect] { return stmt.NewUpdate(postgres.Dialect{}) }
func pgDelete() *stmt.Delete[postgres.Dialect] { return stmt.NewDelete(postgres.Dialect{}) }

// sql builds and returns the text, failing the test on a refusal.
func sql(t *testing.T, s interface{ Build() (stmt.Query, error) }) string {
	t.Helper()
	q, err := s.Build()
	if err != nil {
		t.Fatal(err)
	}
	return q.SQL
}

// TestWriteBuilders_CloneOfANilTemplateIsNil pins that Clone answers nil for a
// nil receiver on every write builder. Clone is the one deliberate way to get
// a statement with a fresh execution guard, so it is called on whatever a
// caller is holding — including a template field nobody set. Answering nil is
// what keeps that from being a panic inside a helper the caller did not write.
func TestWriteBuilders_CloneOfANilTemplateIsNil(t *testing.T) {
	var (
		ins   *stmt.Insert[postgres.Dialect]
		ready *stmt.InsertReady[postgres.Dialect]
		upd   *stmt.Update[postgres.Dialect]
		fupd  *stmt.FilteredUpdate[postgres.Dialect]
		del   *stmt.Delete[postgres.Dialect]
		fdel  *stmt.FilteredDelete[postgres.Dialect]
	)
	for name, got := range map[string]any{
		"Insert":         ins.Clone(),
		"InsertReady":    ready.Clone(),
		"Update":         upd.Clone(),
		"FilteredUpdate": fupd.Clone(),
		"Delete":         del.Clone(),
		"FilteredDelete": fdel.Clone(),
	} {
		t.Run(name, func(t *testing.T) {
			switch c := got.(type) {
			case *stmt.Insert[postgres.Dialect]:
				if c != nil {
					t.Fatalf("got %v, want nil", c)
				}
			case *stmt.InsertReady[postgres.Dialect]:
				if c != nil {
					t.Fatalf("got %v, want nil", c)
				}
			case *stmt.Update[postgres.Dialect]:
				if c != nil {
					t.Fatalf("got %v, want nil", c)
				}
			case *stmt.FilteredUpdate[postgres.Dialect]:
				if c != nil {
					t.Fatalf("got %v, want nil", c)
				}
			case *stmt.Delete[postgres.Dialect]:
				if c != nil {
					t.Fatalf("got %v, want nil", c)
				}
			case *stmt.FilteredDelete[postgres.Dialect]:
				if c != nil {
					t.Fatalf("got %v, want nil", c)
				}
			default:
				t.Fatalf("unexpected type %T", c)
			}
		})
	}
}

// TestWriteBuilders_DialectIsTheStatements pins the accessor an engine helper
// reads to decide what it may write: every complete write statement reports
// the dialect it was started with, and reports it as the same value the
// builder renders through.
func TestWriteBuilders_DialectIsTheStatements(t *testing.T) {
	ins := pgInsert().Into(people).Row(stmt.Set(personID, "a"))
	upd := pgUpdate().Table(people).Set(stmt.Set(role, 1)).All()
	del := pgDelete().From(people).All()
	for name, d := range map[string]render.Dialect{
		"InsertReady":    ins.Dialect(),
		"FilteredUpdate": upd.Dialect(),
		"FilteredDelete": del.Dialect(),
	} {
		t.Run(name, func(t *testing.T) {
			if _, ok := d.(postgres.Dialect); !ok {
				t.Fatalf("got %T, want postgres.Dialect", d)
			}
		})
	}
}

// TestInsert_ClauseBeforeTheRowSurvives pins that an engine clause may be
// added while the statement is still incomplete. Insert.Clause exists for the
// engine helper that configures a statement before the caller says what to
// insert, and a clause added there must reach the rendered statement rather
// than being dropped when Row returns a different type.
func TestInsert_ClauseBeforeTheRowSurvives(t *testing.T) {
	cl := stmt.EngineClause[postgres.Dialect, stmt.InsertKind]("test.OnConflict", stmt.SlotTail,
		func(*stmt.Args) string { return "ON CONFLICT DO NOTHING" })
	got := sql(t, pgInsert().Clause(cl).Into(people).Row(stmt.Set(personID, "a")))
	want := `INSERT INTO "people" AS "p" ("id") VALUES ($1) ON CONFLICT DO NOTHING`
	if got != want {
		t.Fatalf("got  %s\nwant %s", got, want)
	}
}

// TestWriteStatements_TakeACTE pins that every write statement renders a CTE
// ahead of its own verb, which is how a write over a computed set of rows is
// written without a temporary table.
func TestWriteStatements_TakeACTE(t *testing.T) {
	body := stmt.NewSelect(postgres.Dialect{}).Exprs(personID).From(people).Where(expr.Eq(role, 1))
	for name, tc := range map[string]struct {
		got  string
		want string
	}{
		"INSERT": {
			sql(t, pgInsert().Into(people).Row(stmt.Set(personID, "a")).With("keep", body)),
			`WITH "keep" AS (SELECT "p"."id" FROM "people" AS "p" WHERE ("p"."role_id" = $1)) INSERT INTO "people" AS "p" ("id") VALUES ($2)`,
		},
		"UPDATE": {
			sql(t, pgUpdate().Table(people).Set(stmt.Set(role, 2)).All().With("keep", body)),
			`WITH "keep" AS (SELECT "p"."id" FROM "people" AS "p" WHERE ("p"."role_id" = $1)) UPDATE "people" AS "p" SET "role_id" = $2`,
		},
		"DELETE": {
			sql(t, pgDelete().From(people).All().With("keep", body)),
			`WITH "keep" AS (SELECT "p"."id" FROM "people" AS "p" WHERE ("p"."role_id" = $1)) DELETE FROM "people" AS "p"`,
		},
	} {
		t.Run(name, func(t *testing.T) {
			if tc.got != tc.want {
				t.Fatalf("got  %s\nwant %s", tc.got, tc.want)
			}
		})
	}
}

// TestFilteredUpdate_SetAfterTheFilter pins that assignments may be added
// after the statement is complete, and that they are appended in order rather
// than replacing what was there: a complete UPDATE is a value an engine helper
// may still add to.
func TestFilteredUpdate_SetAfterTheFilter(t *testing.T) {
	got := sql(t, pgUpdate().Table(people).Set(stmt.Set(role, 1)).Where(expr.Eq(personID, "a")).
		Set(stmt.Set(salary, int64(2))))
	want := `UPDATE "people" AS "p" SET "role_id" = $1, "salary" = $2 WHERE ("p"."id" = $3)`
	if got != want {
		t.Fatalf("got  %s\nwant %s", got, want)
	}
}

// halfHearted is a dialect that claims RETURNING and then says it does not
// have it. No dialect here does that — a dialect declines a capability by not
// implementing the interface — but a dialect from outside the module can, and
// it is the one input that reaches writeReturning's runtime check.
type halfHearted struct{ postgres.Dialect }

func (halfHearted) SupportsReturning() bool { return false }

// colBinding is the smallest stmt.Binding: a column and a place to scan it
// into. The binding API lives in its own package, which imports stmt, so the
// statement layer's own tests build one directly — which is also the proof
// that Binding stays an interface anyone can implement.
type colBinding struct {
	col expr.Expr[string]
	dst *string
}

func (b colBinding) SQL(a *stmt.Args) string { return b.col.SQL(a) }
func (b colBinding) Target() any             { return b.dst }
func (b colBinding) ColumnName() string      { return b.col.Name() }

// TestReturning_RefusesADialectThatSaysItHasNone pins the belt-and-braces
// half of the RETURNING rule. The type constraint keeps a dialect with no
// SupportsReturning method out at compile time; a dialect that has the method
// and answers false gets ErrNoReturning at Build rather than a statement whose
// RETURNING the server rejects.
func TestReturning_RefusesADialectThatSaysItHasNone(t *testing.T) {
	var got string
	ins := stmt.NewInsert(halfHearted{}).Into(people).Row(stmt.Set(personID, "a"))
	if _, err := stmt.InsertReturning(ins, colBinding{personID, &got}).Build(); !errors.Is(err, stmt.ErrNoReturning) {
		t.Fatalf("INSERT: got %v, want ErrNoReturning", err)
	}
	upd := stmt.NewUpdate(halfHearted{}).Table(people).Set(stmt.Set(role, 1)).All()
	if _, err := stmt.UpdateReturning(upd, colBinding{personID, &got}).Build(); !errors.Is(err, stmt.ErrNoReturning) {
		t.Fatalf("UPDATE: got %v, want ErrNoReturning", err)
	}
	del := stmt.NewDelete(halfHearted{}).From(people).All()
	if _, err := stmt.DeleteReturning(del, colBinding{personID, &got}).Build(); !errors.Is(err, stmt.ErrNoReturning) {
		t.Fatalf("DELETE: got %v, want ErrNoReturning", err)
	}
}

// TestUnknownSlot_NamesTheSameOneEveryRun pins the reason checkSlots sorts.
// A statement carrying clauses for two slots it does not render must name one
// of them, and it must be the same one on every run: the error is read from a
// test failure or a log, and a message that changes between runs looks like
// two different bugs. Iterating the map without sorting would do exactly that.
func TestUnknownSlot_NamesTheSameOneEveryRun(t *testing.T) {
	clause := func(slot stmt.Slot) stmt.Clause[postgres.Dialect, stmt.SelectKind] {
		return stmt.EngineClause[postgres.Dialect, stmt.SelectKind]("test.Misplaced", slot,
			func(*stmt.Args) string { return "X" })
	}
	// Two slots no SELECT renders, given in the order that puts the higher
	// one first, so a run that reported whichever it saw first would differ
	// from the sorted answer.
	build := func() error {
		_, err := stmt.NewSelect(postgres.Dialect{}).Exprs(personID).From(people).
			Clause(clause(stmt.SlotAfterSet), clause(stmt.SlotAfterInsert)).Build()
		return err
	}
	first := build()
	if !errors.Is(first, stmt.ErrUnknownSlot) {
		t.Fatalf("got %v, want ErrUnknownSlot", first)
	}
	if !strings.Contains(first.Error(), stmt.SlotAfterInsert.String()) {
		t.Fatalf("the error must name the lowest unrendered slot, got %q", first)
	}
	// A map iteration is randomised per range, so a handful of runs is enough
	// to catch an unsorted answer.
	for i := 0; i < 20; i++ {
		if got := build(); got.Error() != first.Error() {
			t.Fatalf("run %d reported %q, the first run %q", i, got, first)
		}
	}
}

// TestSelect_AStatementThatSkippedTheConstructorStillGetsAGuard pins the one
// path that leaves a statement without an execution guard. NewSelect creates
// the guard so that every statement derived from a builder shares one; a zero
// Select skipped it, and the builder methods copy what they find, so the
// statement reaches Build with none. Build makes it one there rather than
// handing out Queries that cannot detect an overlapping execution, which is
// the whole reason the guard exists. The remaining hole is a Query assembled
// as a struct literal, which TestQuery_LiteralHasNoGuard pins.
func TestSelect_AStatementThatSkippedTheConstructorStillGetsAGuard(t *testing.T) {
	var zero stmt.Select[postgres.Dialect]
	s := zero.Exprs(personID).From(people)
	first, err := s.Build()
	if err != nil {
		t.Fatal(err)
	}
	second, err := s.Build()
	if err != nil {
		t.Fatal(err)
	}
	if !first.Acquire() {
		t.Fatal("the first execution must start")
	}
	if second.Acquire() {
		t.Fatal("two Queries from one statement must share a guard, so the second must wait")
	}
	first.Release()
	if !second.Acquire() {
		t.Fatal("once the first released, the second must start")
	}
	second.Release()
}
