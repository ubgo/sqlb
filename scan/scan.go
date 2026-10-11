// Package scan is a result strategy that returns values instead of writing
// through pointers.
//
// It is a peer of the binding API in expr, not a replacement for it, and the
// two differ in exactly one way that matters. A Binding captures a pointer to a
// caller variable when the query is built, so the query aliases caller memory
// and two goroutines executing it interleave each other's rows. A Reader
// captures no memory at all: it allocates fresh destinations for every row and
// hands back a value, so one Reader is safe to share across any number of
// concurrent executions.
//
// The cost is that a Reader has to be composed rather than declared inline. The
// composition is pairwise, which is the whole trick: Map2 combines two Readers
// into one, so a Reader of any width is Map2 applied repeatedly, and no variadic
// machinery or reflection is needed to reach ten columns.
//
// It sits above exec and imports no engine. Which columns a Reader selects is
// decided by the expressions handed to Field, so a Reader over an engine's
// constructs needs nothing added here.
package scan

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/ubgo/sqlb/exec"
	"github.com/ubgo/sqlb/expr"
	"github.com/ubgo/sqlb/render"
	"github.com/ubgo/sqlb/stmt"
)

// ErrNoRows is returned by One when the statement matched nothing. It aliases
// sql.ErrNoRows so an existing errors.Is check keeps working and a caller is
// never asked to learn a second spelling of the same condition.
var ErrNoRows = sql.ErrNoRows

// ErrNoColumns is returned when a Reader selects nothing. A statement with an
// empty select list is always a mistake, and catching it here names the Reader
// rather than letting the database report a syntax error.
var ErrNoColumns = errors.New("sqlb/scan: reader selects no columns")

// Reader knows which columns to select and how to turn one scanned row into a
// value of type T.
//
// The invariant that makes it safe to share: alloc is called once per row and
// returns freshly allocated destinations together with a closure that reads
// them. Nothing is retained between rows and nothing is shared between
// executions, so the zero-value Reader is the only unusable one and every
// constructed Reader is safe for concurrent use.
type Reader[T any] struct {
	cols  []render.SQLer
	alloc func() (dests []any, value func() T)
}

// Columns returns the select items this Reader needs, in scan order. A caller
// building a statement by hand passes these to Exprs; All and One do it for you.
func (r Reader[T]) Columns() []render.SQLer { return r.cols }

// Field makes a Reader for a single typed expression.
//
// The destination is a fresh T per row, which is what lets the value be
// returned rather than aliased. T comes from the expression, so scanning a
// string column into an int is a compile error here exactly as it is in Bind.
func Field[T any](e expr.Expr[T]) Reader[T] {
	return Reader[T]{
		cols: []render.SQLer{e},
		alloc: func() ([]any, func() T) {
			var v T
			return []any{&v}, func() T { return v }
		},
	}
}

// New builds a Reader from the columns it selects and a function that
// allocates one row's destinations.
//
// It exists because a Reader cannot otherwise be made outside this package,
// and that asymmetry had a cost. stmt.Binding is an interface precisely so a
// package outside this module can implement it, which is what let
// contrib/structbind exist; the result layer's other half had no such door, so
// a reflective mapping could produce bindings and could not produce a Reader.
// Reading a query into a slice of structs, the single most common thing a
// caller wants, was therefore a hand-written loop over exec.Each with a copy
// per row.
//
// The contract is the whole of the safety argument, so it is stated rather
// than assumed. alloc is called once per row and must return destinations it
// has just allocated, together with a closure reading those same
// destinations. It must not hand back a pointer into memory the caller keeps:
// nothing in this package takes the query guard, because a Reader is supposed
// to hold no caller memory, and a Reader that broke that rule would interleave
// rows across goroutines with no error and no guard to catch it.
//
// The columns are what All and One append to the statement's select list, in
// scan order, so they must correspond one to one with the destinations.
func New[T any](cols []render.SQLer, alloc func() (dests []any, value func() T)) Reader[T] {
	return Reader[T]{cols: cols, alloc: alloc}
}

// Map turns a Reader of A into a Reader of B. It is the one-column case of
// Map2, useful for wrapping a scanned value in a domain type.
func Map[A, B any](a Reader[A], f func(A) B) Reader[B] {
	return Reader[B]{
		cols: a.cols,
		alloc: func() ([]any, func() B) {
			d, get := a.alloc()
			return d, func() B { return f(get()) }
		},
	}
}

// Map2 combines two Readers into one, selecting a's columns then b's.
//
// This is the only combinator the package needs. A three-column Reader is
// Map2(Map2(a, b, pair), c, ...), and because composition is pairwise the
// arity is unbounded without a generated family of Map3, Map4 and so on. Go
// generics cannot express a variadic type list, and this is the standard answer
// to that, not a workaround for it.
func Map2[A, B, C any](a Reader[A], b Reader[B], f func(A, B) C) Reader[C] {
	cols := make([]render.SQLer, 0, len(a.cols)+len(b.cols))
	cols = append(cols, a.cols...)
	cols = append(cols, b.cols...)
	return Reader[C]{
		cols: cols,
		alloc: func() ([]any, func() C) {
			da, ga := a.alloc()
			db, gb := b.alloc()
			dests := make([]any, 0, len(da)+len(db))
			dests = append(dests, da...)
			dests = append(dests, db...)
			return dests, func() C { return f(ga(), gb()) }
		},
	}
}

// build finishes s with the Reader's columns and renders it.
//
// Exprs returns a derived statement and does not touch the caller's, so
// nothing here has to defend against reuse. That was not always true: builder
// methods used to mutate their receiver, so this function left the Reader's
// columns on the statement it was given, and calling All twice with one Select
// selected every column twice — legal SQL, therefore a failure that arrived
// somewhere else — while two goroutines calling All raced on the same slice.
// It was fixed here with a clone first and in the builders afterwards, which
// is where it belonged: a caller should not have to know which half of the
// pair is the mutable one, and now neither half is.
// build renders s with the reader's columns appended to it.
//
// Appended, never substituted: the reader says which columns it reads, and a
// statement that already declares its own select list keeps it and gets the
// reader's on top. That is what makes a Reader usable on a statement a library
// handed you, and it is the rule every caller has to know, because the widened
// select list is the one the engine sees. Where the width itself means
// something — the branches of a set operation must agree — stmt.Build refuses
// the mismatch with ErrSetOpArity rather than letting the engine report it
// against columns the caller never typed. Where it does not, the extra columns
// are selected and never scanned, except under Distinct, which counts them
// when deciding what is a duplicate.
//
// s is cloned by Exprs, which derives rather than writing to its receiver, so
// a caller holding the statement is unaffected.
func build[T any, D render.Dialect](s *stmt.Select[D], r Reader[T]) (stmt.Query, error) {
	if len(r.cols) == 0 {
		return stmt.Query{}, ErrNoColumns
	}
	return s.Exprs(r.cols...).Build()
}

// All runs s with r's columns and returns every row as a value.
//
// Unlike the binding API there is no per-row callback and no warning about
// copying, because each row is already its own value. Appending the result of
// one iteration cannot alias the next.
//
// r's columns are appended to s's own select list rather than replacing it;
// see build for what that means where the width of a select list carries
// meaning of its own.
func All[T any, D render.Dialect](ctx context.Context, db exec.DBTX, s *stmt.Select[D], r Reader[T]) ([]T, error) {
	q, err := build(s, r)
	if err != nil {
		return nil, err
	}
	rows, err := db.QueryContext(ctx, q.SQL, q.Args...)
	if err != nil {
		return nil, fmt.Errorf("sqlb/scan: query: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var out []T
	for rows.Next() {
		dests, value := r.alloc()
		if err := rows.Scan(dests...); err != nil {
			return nil, fmt.Errorf("sqlb/scan: scan: %w", err)
		}
		out = append(out, value())
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("sqlb/scan: rows: %w", err)
	}
	return out, nil
}

// One runs s and returns the single row it matched.
//
// r's columns are appended to s's own select list rather than replacing it;
// see build for what that means where the width of a select list carries
// meaning of its own.
//
// It returns ErrNoRows when nothing matched. It does not reject a second row:
// the binding API's ErrMultipleRows exists because its caller is resolving an
// identity through a shared statement, whereas a Reader is normally applied to
// a query that already carries its own LIMIT. Use All and check the length when
// the uniqueness itself is the thing being asserted.
func One[T any, D render.Dialect](ctx context.Context, db exec.DBTX, s *stmt.Select[D], r Reader[T]) (T, error) {
	var zero T
	q, err := build(s, r)
	if err != nil {
		return zero, err
	}
	rows, err := db.QueryContext(ctx, q.SQL, q.Args...)
	if err != nil {
		return zero, fmt.Errorf("sqlb/scan: query: %w", err)
	}
	defer func() { _ = rows.Close() }()

	if !rows.Next() {
		if err := rows.Err(); err != nil {
			return zero, fmt.Errorf("sqlb/scan: rows: %w", err)
		}
		return zero, ErrNoRows
	}
	dests, value := r.alloc()
	if err := rows.Scan(dests...); err != nil {
		return zero, fmt.Errorf("sqlb/scan: scan: %w", err)
	}
	return value(), nil
}
