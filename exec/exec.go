// Package exec runs a built statement against a database and iterates its rows.
//
// It is a layer because execution has nothing to do with how results come back
// into Go. Taking a DBTX, carrying the context, looping the rows and telling
// "no rows" from "more than one row" are the same work whichever result
// strategy sits above, so doing them once is what keeps a second strategy from
// re-implementing all of it.
//
// It takes a DBTX rather than a concrete handle so a caller can run a library's
// query inside their own transaction, alongside their own writes. That is the
// single capability an embedded library has and a remote service can never
// offer, which is why the interface is the narrowest thing providing it.
package exec

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"github.com/ubgo/sqlb/stmt"
)

// DBTX is the subset of database/sql that both *sql.DB and *sql.Tx satisfy.
//
// Taking this rather than a concrete type is what lets a caller run an auth
// query inside their own transaction, alongside their own writes. That is the
// single capability an embedded library has and a remote identity service can
// never offer, so the interface is deliberately the narrowest thing that
// provides it.
type DBTX interface {
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
}

// ErrNoRows is returned by One when the statement matched nothing.
//
// It aliases sql.ErrNoRows so existing errors.Is checks keep working, and so a
// caller is never forced to learn a second spelling of the same condition.
var ErrNoRows = sql.ErrNoRows

// ErrMultipleRows is returned by One when a statement expected to be unique
// matched more than one row.
//
// Worth its own error rather than silently taking the first: every One caller
// in this library is resolving an identity, so a second row means a uniqueness
// index is missing or a join fans out. Taking the first row would pick an
// arbitrary user, which is the worst possible failure mode for a login lookup.
var ErrMultipleRows = errors.New("sqlb: query matched multiple rows but expected one")

// One executes q and scans exactly one row into the destinations bound when the
// query was built.
//
// Nothing is passed for the destination because the query already carries it:
// Bind captured a pointer to each field, Build collected them in SELECT order,
// and Scan writes straight through those pointers. That is why the builder is
// the only place column order is decided — there is no second list to keep in
// step with it.
//
// Returns ErrNoRows when nothing matched, ErrMultipleRows when more than one
// row did, stmt.ErrNoTargets when the query bound no destinations, and
// stmt.ErrQueryInUse when the same statement is already executing.
//
// That last one is the concurrency rule: q is not safe for concurrent use,
// and executing it from two goroutines is a data race on its scan targets,
// which are pointers into the caller's variables.
// Build the query where its destination lives, per call. Reusing one
// sequentially is fine.
func One(ctx context.Context, db DBTX, q stmt.Query) error {
	if q.Targets == nil {
		return stmt.ErrNoTargets
	}
	// A Query holds pointers into the caller's variables, so an overlapping
	// execution writes another goroutine's rows into them. Refuse rather than
	// interleave; see stmt.ErrQueryInUse.
	if !q.Acquire() {
		return stmt.ErrQueryInUse
	}
	defer q.Release()
	rows, err := db.QueryContext(ctx, q.SQL, q.Args...)
	if err != nil {
		return fmt.Errorf("sqlb: query: %w", err)
	}
	// Close releases the connection. Its error is deliberately dropped: a
	// failure during iteration already reaches the caller through rows.Err()
	// below, and a failure only on release tells the caller nothing they can
	// act on while masking the real result.
	defer func() { _ = rows.Close() }()

	if !rows.Next() {
		if err := rows.Err(); err != nil {
			return fmt.Errorf("sqlb: iterate: %w", err)
		}
		return ErrNoRows
	}
	if err := rows.Scan(q.Targets...); err != nil {
		return fmt.Errorf("sqlb: scan: %w", err)
	}
	if rows.Next() {
		return ErrMultipleRows
	}
	return rows.Err()
}

// Each executes q and scans every row into the bound destinations, calling fn
// once after each row is scanned.
//
// Invariant, and the one thing that will bite a caller who skips this comment:
// every row scans into the SAME destinations, because the bindings hold fixed
// pointers. The struct is overwritten on each iteration, so fn must copy what
// it needs — appending a pointer to the destination yields a slice of N
// identical values, all equal to the last row.
//
//	var ident auth.Identity
//	q, _ := a.IdentitiesOfUserQuery(userID, &ident)
//	var all []auth.Identity
//	err := sqlb.Each(ctx, db, q, func() error {
//	    all = append(all, ident) // copies the value; &ident would not
//	    return nil
//	})
//
// Returning an error from fn stops iteration and returns that error, so a
// caller can abort a large result set without draining it.
//
// Returns stmt.ErrNoTargets when the query bound no destinations, and
// stmt.ErrQueryInUse when the same statement is already executing, including from
// inside fn. q is not safe for concurrent use; running one from two
// goroutines is a data race. See One.
func Each(ctx context.Context, db DBTX, q stmt.Query, fn func() error) error {
	if q.Targets == nil {
		return stmt.ErrNoTargets
	}
	// Same reason as One. This also catches a callback that re-executes the
	// query it is iterating, which would overwrite the targets mid-loop.
	if !q.Acquire() {
		return stmt.ErrQueryInUse
	}
	defer q.Release()
	rows, err := db.QueryContext(ctx, q.SQL, q.Args...)
	if err != nil {
		return fmt.Errorf("sqlb: query: %w", err)
	}
	// Dropped for the same reason as in One: rows.Err() carries anything the
	// caller can act on.
	defer func() { _ = rows.Close() }()

	for rows.Next() {
		if err := rows.Scan(q.Targets...); err != nil {
			return fmt.Errorf("sqlb: scan: %w", err)
		}
		if err := fn(); err != nil {
			return err
		}
	}
	return rows.Err()
}
