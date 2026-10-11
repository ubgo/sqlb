package stmt_test

import (
	"strconv"
	"strings"
	"testing"

	"github.com/ubgo/sqlb/dialect/postgres"
	"github.com/ubgo/sqlb/expr"
	"github.com/ubgo/sqlb/stmt"
)

// TestClone_IsIndependent pins the point of Clone: a template can be built once
// and specialised per caller without the callers seeing each other.
//
// Every builder method mutates its receiver and returns it, which makes a
// partially built statement cheap to extend and dangerous to share. This is the
// shape that makes sharing safe.
func TestClone_IsIndependent(t *testing.T) {
	base := stmt.NewSelect(postgres.Dialect{}).
		Exprs(personID).
		From(people).
		Where(expr.Eq(role, 1))

	a := base.Clone().Where(expr.Greater(salary, 100))
	b := base.Clone().Where(expr.Less(salary, 50))

	qa, err := a.Build()
	if err != nil {
		t.Fatal(err)
	}
	qb, err := b.Build()
	if err != nil {
		t.Fatal(err)
	}
	qBase, err := base.Build()
	if err != nil {
		t.Fatal(err)
	}

	if !strings.Contains(qa.SQL, `"p"."salary" > `) || strings.Contains(qa.SQL, `"p"."salary" < `) {
		t.Errorf("clone a saw clone b's predicate: %s", qa.SQL)
	}
	if !strings.Contains(qb.SQL, `"p"."salary" < `) || strings.Contains(qb.SQL, `"p"."salary" > `) {
		t.Errorf("clone b saw clone a's predicate: %s", qb.SQL)
	}
	if strings.Contains(qBase.SQL, `"p"."salary"`) {
		t.Errorf("the template was modified by its clones: %s", qBase.SQL)
	}
}

// TestClone_DoesNotShareBackingArrays pins the reason Clone copies every slice
// rather than relying on a struct copy.
//
// A shallow copy shares the backing arrays. Appending to the copy then writes
// into the original's spare capacity, so a second append on the original
// silently overwrites what the copy added, or the copy sees a predicate nobody
// gave it. The failure depends on capacity, which makes it intermittent and
// very hard to attribute.
func TestClone_DoesNotShareBackingArrays(t *testing.T) {
	// Build with room to spare, so a shallow copy would alias.
	base := stmt.NewSelect(postgres.Dialect{}).Exprs(personID).From(people)
	for i := 0; i < 3; i++ {
		base = base.Where(expr.Eq(role, i))
	}

	clone := base.Clone().Where(expr.Eq(role, 99))
	// A second statement derived from the same base. Every builder method
	// copies, so this cannot touch the clone unless a copy somewhere shared a
	// backing array — which is exactly what this test exists to catch, and
	// what it caught when the copy was shallow.
	_ = base.Where(expr.Eq(role, 42))

	qc, err := clone.Build()
	if err != nil {
		t.Fatal(err)
	}
	if got := len(qc.Args); got != 4 {
		t.Fatalf("clone has %d predicates, want 4", got)
	}
	if qc.Args[3] != 99 {
		t.Fatalf("the clone's own predicate was overwritten by the original: %v", qc.Args)
	}
}

// TestClone_GetsAFreshGuard pins that a clone is a different statement for the
// purposes of the concurrency guard. Sharing the original's guard would mean a
// clone could be refused because the statement it was copied from is running.
func TestClone_GetsAFreshGuard(t *testing.T) {
	base := stmt.NewSelect(postgres.Dialect{}).Exprs(personID).From(people)

	q1, err := base.Build()
	if err != nil {
		t.Fatal(err)
	}
	q2, err := base.Clone().Build()
	if err != nil {
		t.Fatal(err)
	}

	if !q1.Acquire() {
		t.Fatal("the original refused its first execution")
	}
	if !q2.Acquire() {
		t.Fatal("a clone was refused while the statement it was copied from was running")
	}
}

// TestClone_WriteBuilders covers the other three, which have the same hazard.
func TestClone_WriteBuilders(t *testing.T) {
	t.Run("update", func(t *testing.T) {
		base := stmt.NewUpdate(postgres.Dialect{}).Table(people).Set(stmt.Set(role, 1))
		a := base.Clone().Where(expr.Eq(personID, "a"))
		b := base.Clone().Where(expr.Eq(personID, "b"))

		qa, _ := a.Build()
		qb, _ := b.Build()
		if qa.Args[1] == qb.Args[1] {
			t.Fatalf("clones share a WHERE: %v and %v", qa.Args, qb.Args)
		}
		// The template cannot be built at all without stating its rows — that
		// is now a compile error — so the check that it gained no WHERE from its
		// clones is made on All(), whose only argument should be the SET value.
		if q, err := base.All().Build(); err != nil || len(q.Args) != 1 {
			t.Errorf("the template gained a WHERE from its clones: %v (%v)", q.Args, err)
		}
	})

	t.Run("delete", func(t *testing.T) {
		base := stmt.NewDelete(postgres.Dialect{}).From(people)
		a := base.Clone().Where(expr.Eq(personID, "a"))
		qa, err := a.Build()
		if err != nil {
			t.Fatal(err)
		}
		if len(qa.Args) != 1 {
			t.Fatalf("args = %v, want one", qa.Args)
		}
		if q, err := base.All().Build(); err != nil || len(q.Args) != 0 {
			t.Errorf("the template gained a WHERE from its clone: %v (%v)", q.Args, err)
		}
	})

	t.Run("insert", func(t *testing.T) {
		base := stmt.NewInsert(postgres.Dialect{}).Into(people)
		a := base.Clone().Row(stmt.Set(personID, "a"))
		b := base.Clone().Row(stmt.Set(personID, "b"))

		qa, err := a.Build()
		if err != nil {
			t.Fatal(err)
		}
		qb, err := b.Build()
		if err != nil {
			t.Fatal(err)
		}
		if len(qa.Args) != 1 || len(qb.Args) != 1 {
			t.Fatalf("clones share rows: %v and %v", qa.Args, qb.Args)
		}
	})
}

// TestClone_NilIsNil keeps the method safe on a nil receiver, which a caller
// holding an optional template will hit.
func TestClone_NilIsNil(t *testing.T) {
	var s *stmt.Select[postgres.Dialect]
	if s.Clone() != nil {
		t.Error("cloning a nil Select should give nil")
	}
}

// outsideDialect is a dialect this module does not ship, written the way a
// package in another repository would write it: three methods, importing the
// render layer and nothing else.
type outsideDialect struct{}

func (outsideDialect) Placeholder(n int) string   { return "@p" + strconv.Itoa(n) }
func (outsideDialect) QuoteIdent(s string) string { return "[" + s + "]" }
func (outsideDialect) Name() string               { return "outside" }

// TestOutsideDialect_DrivesTheWholeBuilder pins the claim that adding an engine
// costs three methods and no change to this module.
//
// The render layer's own test covers placeholders and quoting in isolation.
// This is the statement layer: a whole SELECT with joins, a WHERE, ordering and
// a bound LIMIT, rendered by a dialect the library has never heard of. If any
// part of the renderer ever branches on a known dialect name, this is what
// notices.
func TestOutsideDialect_DrivesTheWholeBuilder(t *testing.T) {
	q, err := stmt.NewSelect(outsideDialect{}).
		Exprs(personID, salary).
		From(people).
		Where(expr.Eq(role, 1), expr.Greater(salary, 100)).
		Order(expr.OrderBy(salary, expr.SortDesc)).
		Limit(5).
		Build()
	if err != nil {
		t.Fatal(err)
	}

	want := `SELECT [p].[id], [p].[salary] FROM [people] AS [p] ` +
		`WHERE ([p].[role_id] = @p1 AND [p].[salary] > @p2) ` +
		`ORDER BY [p].[salary] DESC LIMIT @p3`
	if q.SQL != want {
		t.Fatalf("got  %s\nwant %s", q.SQL, want)
	}
	if len(q.Args) != 3 {
		t.Fatalf("args = %v, want three", q.Args)
	}
}
