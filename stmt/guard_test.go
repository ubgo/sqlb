package stmt_test

import (
	"testing"

	"github.com/ubgo/sqlb/stmt"
)

// TestGuard_SharedGuardRefusesAnOverlap pins the contract an engine builder
// relies on: every Query built with one Guard is detected as sharing targets,
// so a second execution cannot start while the first holds it, and can once
// the first releases.
func TestGuard_SharedGuardRefusesAnOverlap(t *testing.T) {
	g := stmt.NewGuard()
	var dst int
	a := stmt.NewGuardedQuery(g, "SELECT 1", nil, []any{&dst})
	b := stmt.NewGuardedQuery(g, "SELECT 2", nil, []any{&dst})

	if !a.Acquire() {
		t.Fatal("the first execution must start")
	}
	if b.Acquire() {
		t.Fatal("a second Query on the same guard started while the first was running")
	}
	a.Release()
	if !b.Acquire() {
		t.Fatal("once the first released, the second must start")
	}
	b.Release()
}

// TestGuard_SeparateGuardsAreIndependent pins that a guard is per builder,
// not global: two unrelated builders' Queries run at once.
func TestGuard_SeparateGuardsAreIndependent(t *testing.T) {
	a := stmt.NewGuardedQuery(stmt.NewGuard(), "SELECT 1", nil, nil)
	b := stmt.NewGuardedQuery(stmt.NewGuard(), "SELECT 2", nil, nil)
	if !a.Acquire() || !b.Acquire() {
		t.Fatal("Queries from two different guards must not block each other")
	}
	a.Release()
	b.Release()
}

// TestGuard_ZeroGuardDetectsNothing pins the documented hole: the zero Guard
// behaves like a Query struct literal and never refuses. It is stated in the
// doc comment as the reason a builder that captures targets must not leave it
// zero, so a test says it rather than a sentence.
func TestGuard_ZeroGuardDetectsNothing(t *testing.T) {
	var zero stmt.Guard
	a := stmt.NewGuardedQuery(zero, "SELECT 1", nil, nil)
	b := stmt.NewGuardedQuery(zero, "SELECT 2", nil, nil)
	if !a.Acquire() || !b.Acquire() {
		t.Fatal("the zero Guard must never refuse")
	}
}

// TestGuard_NewQueryGetsItsOwnGuard pins the difference that made Merge need
// a Guard: two Queries from NewQuery never block each other, even with the
// same targets, because each Build hands out a fresh guard.
func TestGuard_NewQueryGetsItsOwnGuard(t *testing.T) {
	var dst int
	a := stmt.NewQuery("SELECT 1", nil, []any{&dst})
	b := stmt.NewQuery("SELECT 1", nil, []any{&dst})
	if !a.Acquire() || !b.Acquire() {
		t.Fatal("NewQuery Queries are independent by construction")
	}
	a.Release()
	b.Release()
}
