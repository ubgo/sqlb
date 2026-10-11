package stmt

import (
	"errors"
	"sync/atomic"
)

// ErrQueryInUse is returned by One and Each when a statement is executed
// while the same statement is already executing.
//
// It means a DATA RACE was about to happen: a Query is not safe for
// concurrent use, and executing one from two goroutines is a race condition
// on its scan targets. Go's race detector reports it as a data race on the
// bound variables, and `go test -race` is how you would otherwise find this.
//
// Why this is an error and not a lock: a Query looks like inert data, a
// string and some values, and that reading is what makes the mistake easy.
// It is not inert. Targets holds pointers into the caller's variables, so two
// goroutines running one Query write to the same memory, and the result is
// interleaved rows rather than a crash. Serialising them with a lock would
// hide that: the second caller would get the first caller's rows in its own
// variables and never know. Refusing says what actually happened.
//
// The fix at the call site is to build the query where its destination lives,
// which is what the cookbook recipes do, rather than hoisting one query or
// one builder to package scope and sharing it.
var ErrQueryInUse = errors.New("sqlb: query is already executing; build it where its scan targets live rather than sharing one")

// execGuard detects overlapping execution of statements that share scan
// targets. It is a pointer inside Query, so copies of a Query share it, which
// is correct: copying a Query copies the target pointers, not the variables.
type execGuard struct{ running atomic.Bool }

// acquire reports whether execution may start.
func (g *execGuard) acquire() bool { return g == nil || g.running.CompareAndSwap(false, true) }

func (g *execGuard) release() {
	if g != nil {
		g.running.Store(false)
	}
}

// NewQuery assembles a rendered statement with its bound values and scan
// targets.
//
// It is for an engine package rendering a statement kind the core does not
// have, such as pg's MERGE. Building the Query through here rather than with
// a struct literal is what gives it the concurrent-use check; a literal is
// still valid and simply has none.
func NewQuery(sql string, args, targets []any) Query {
	return Query{SQL: sql, Args: args, Targets: targets, guard: &execGuard{}}
}

// Guard is the execution guard an engine package's statement builder owns.
//
// A builder whose statements carry scan targets must hand every Query it
// builds one shared guard, and so must every statement derived from it,
// because derived statements share the caller's target pointers: two Queries
// from one builder running at once write into the same variables. The core's
// builders do this internally; Guard is how a statement kind written outside
// the core, pg.Merge for one, does the same. Create it once, in the
// constructor, copy it into every derived statement, and build through
// NewGuardedQuery.
//
// The zero Guard detects nothing, like a Query struct literal. That is the
// documented hole in the detection, not a way to opt out: a builder that
// captures targets has no reason to leave it zero.
type Guard struct{ g *execGuard }

// NewGuard creates a guard for one builder and everything derived from it.
func NewGuard() Guard { return Guard{g: &execGuard{}} }

// NewGuardedQuery is NewQuery for a builder that owns a Guard: every Query it
// returns for the same guard is detected as sharing targets with the others,
// so ErrQueryInUse fires when two of them overlap.
func NewGuardedQuery(g Guard, sql string, args, targets []any) Query {
	return newQueryWith(g.g, sql, args, targets)
}

// newQueryWith is NewQuery for a builder, which owns its guard so that two
// Queries built from one builder are detected as sharing targets. They do
// share them: Build copies the target pointers, not the variables.
func newQueryWith(g *execGuard, sql string, args, targets []any) Query {
	return Query{SQL: sql, Args: args, Targets: targets, guard: g}
}

// Acquire reports whether execution of q may start, and claims it if so.
//
// It is exported because executing a statement is a layer above building one,
// and the two live in different packages. The guard has to be reachable from
// there without exposing how it works: a caller learns only yes or no, and must
// call Release when the execution finishes.
//
// A Query built as a struct literal has no guard and is always allowed to
// proceed, which is the documented hole in the detection rather than a bug. The
// guard detects the overlap it can see; it does not prevent the hazard.
func (q Query) Acquire() bool { return q.guard.acquire() }

// Release ends the execution claimed by Acquire. Calling it without a matching
// Acquire is safe and does nothing.
func (q Query) Release() { q.guard.release() }
