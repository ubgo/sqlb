package scan

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/ubgo/sqlb/exec"
	"github.com/ubgo/sqlb/stmt"
)

// Dynamic reading: for a query whose shape is not known when the code is
// written.
//
// Everything else in this package needs the columns at compile time, because
// Field takes a typed expression and Bind takes a pointer to a variable that
// has to exist. That is the right trade for a query somebody wrote in Go, and
// it is no help at all when the shape comes from outside: an export screen
// with column checkboxes, an admin table, a SQL console. There the column list
// is a value that arrives with the request, and no set of variables declared
// in advance can match it.
//
// The builder already handles that case, since a select list is assembled from
// a slice and a slice can be any length. Only the reading side was missing,
// which made this an asymmetry rather than a decision: the library could build
// a query it could not read back.
//
// This is the escape hatch for the result layer, in the same sense that
// render.Raw is the escape hatch for rendering, and it costs the same thing.
// A value is an any, so a wrong assumption about a type is a runtime panic
// rather than a compile error, and a misspelled column name is a missing key
// rather than a build failure. Use it where the shape is genuinely dynamic and
// nowhere else.

// Row is one row of a dynamically shaped result, keyed by column name.
//
// The values are whatever the driver produced, not converted: an integer
// column arrives as int64, a text column as string, a blob as []byte, a NULL
// as nil. That is deliberate. Normalising them would have to guess, and a
// guess here is invisible, so the driver's own answer is what you get.
type Row map[string]any

// ErrDuplicateColumn is returned by Maps and EachMap when a result has two
// columns of the same name.
//
// A map cannot hold both, so the alternative is to keep one and discard the
// other silently, which is the failure this package exists to avoid. It is an
// ordinary query that does it, "SELECT a.id, b.id FROM a JOIN b", so the error
// names the column and points at Slices, which is positional and loses
// nothing.
var ErrDuplicateColumn = errors.New("sqlb/scan: result has two columns with the same name, which a map cannot hold; use Slices")

// Maps reads every row of an already-built query as a map.
//
// The query is taken built rather than as a Select, because a caller reaching
// for this has assembled the select list themselves and there is no Reader to
// add to it.
func Maps(ctx context.Context, db exec.DBTX, q stmt.Query) ([]Row, error) {
	var out []Row
	err := EachMap(ctx, db, q, func(r Row) error {
		out = append(out, r)
		return nil
	})
	return out, err
}

// EachMap reads every row as a map and calls fn once per row.
//
// Unlike the binding API's Each, the map handed to fn is fresh for every row
// and is never reused, so a callback may keep it. That follows from this
// package's rule rather than being a special case: a Reader holds no caller
// memory, and neither does this.
//
// Returning an error from fn stops iteration and returns that error.
func EachMap(ctx context.Context, db exec.DBTX, q stmt.Query, fn func(Row) error) error {
	return eachRow(ctx, db, q, func(cols []string, vals []any) error {
		row := make(Row, len(cols))
		for i, c := range cols {
			if _, clash := row[c]; clash {
				return fmt.Errorf("%w: %q", ErrDuplicateColumn, c)
			}
			row[c] = vals[i]
		}
		return fn(row)
	})
}

// Slices reads every row positionally, returning the column names once and one
// slice per row.
//
// This is the form to use when a result may have two columns of the same name,
// which a join across tables sharing a column does routinely, and the form to
// use for anything writing rows out in order, such as a CSV export, where the
// names are a header written once rather than a key per cell.
func Slices(ctx context.Context, db exec.DBTX, q stmt.Query) ([]string, [][]any, error) {
	var names []string
	var out [][]any
	err := eachRow(ctx, db, q, func(cols []string, vals []any) error {
		if names == nil {
			names = append([]string(nil), cols...)
		}
		out = append(out, vals)
		return nil
	})
	if err != nil {
		return nil, nil, err
	}
	return names, out, nil
}

// eachRow is the shared loop: run the query, learn the columns from the
// database, and scan each row into a fresh slice of any.
func eachRow(ctx context.Context, db exec.DBTX, q stmt.Query, fn func(cols []string, vals []any) error) error {
	// No Acquire here, deliberately, and it is the same reason this package
	// exists. exec takes the query guard because a Binding holds a pointer into
	// a caller's variable, so two goroutines running one query would interleave
	// their writes into the same memory. Nothing here holds caller memory: the
	// values are allocated per row and handed back. Taking the guard would
	// refuse a concurrent execution that is safe, which is the opposite of what
	// this package promises.
	rows, err := db.QueryContext(ctx, q.SQL, q.Args...)
	if err != nil {
		return fmt.Errorf("sqlb/scan: query: %w", err)
	}
	defer func() { _ = rows.Close() }()

	cols, err := rows.Columns()
	// No test reaches this arm and none can today: database/sql returns an
	// error here only for a Rows that is already closed or has already
	// failed, and this one was just handed back by a successful query. It
	// stays because the condition is the standard library's to define, and a
	// version that reported something else here would otherwise be read as
	// "no columns". A fake driver cannot produce it either; it would take a
	// change in database/sql.
	if err != nil {
		return fmt.Errorf("sqlb/scan: columns: %w", err)
	}

	for rows.Next() {
		// A fresh slice per row, and fresh scanners over it. Reusing either
		// would hand every row the previous row's memory, which is the same
		// hazard the binding API documents and the reason this package exists.
		vals := make([]any, len(cols))
		dests := make([]any, len(cols))
		for i := range vals {
			dests[i] = &anyScanner{&vals[i]}
		}
		// Unreachable today, unlike the same arm in All and One, which the
		// fault driver's "wrongtype" case covers: every destination here is an
		// anyScanner, which stores whatever the driver produced and never
		// returns an error, and the count always matches Columns. It would
		// become reachable the day a destination refuses a value — a scanner
		// that converted, say — which is exactly when it would be needed.
		if err := rows.Scan(dests...); err != nil {
			return fmt.Errorf("sqlb/scan: scan: %w", err)
		}
		if err := fn(cols, vals); err != nil {
			return err
		}
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("sqlb/scan: rows: %w", err)
	}
	return nil
}

// anyScanner takes whatever the driver produced and stores it, copying the one
// kind of value a driver is allowed to reuse.
//
// Scanning into a plain *any works for most types and is a trap for []byte: a
// driver may hand back a slice into its own read buffer and overwrite it on
// the next row, so every row but the last would be corrupted, and only for
// blob and sometimes text columns. Copying is cheap and the alternative is a
// bug that depends on the driver.
type anyScanner struct{ dst *any }

func (a *anyScanner) Scan(src any) error {
	if b, ok := src.([]byte); ok {
		*a.dst = append([]byte(nil), b...)
		return nil
	}
	*a.dst = src
	return nil
}

// compile-time proof that the scanner satisfies what database/sql calls.
var _ sql.Scanner = (*anyScanner)(nil)
