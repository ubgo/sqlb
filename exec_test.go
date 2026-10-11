package sqlb

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"io"
	"testing"
)

// The core module has zero third-party dependencies, so it cannot import a
// database driver to test One and Each. It does not need to: database/sql
// lets a driver be registered from the test binary, and everything below is
// standard library. This is what lets the execution path, including its error
// arms, be pinned here rather than only in the contrib module whose
// conformance run covers the happy path.

const fakeDriverName = "sqlb-fake"

// fakeRows is one canned result set, plus the two failures a driver can hand
// back mid-iteration.
type fakeRows struct {
	cols     []string
	vals     [][]driver.Value
	i        int
	nextErr  error // returned by Next instead of a row
	afterRow int   // how many rows to yield before nextErr fires
}

func (r *fakeRows) Columns() []string { return r.cols }
func (r *fakeRows) Close() error      { return nil }

func (r *fakeRows) Next(dest []driver.Value) error {
	if r.nextErr != nil && r.i >= r.afterRow {
		return r.nextErr
	}
	if r.i >= len(r.vals) {
		return io.EOF
	}
	copy(dest, r.vals[r.i])
	r.i++
	return nil
}

type fakeStmt struct {
	conn  *fakeConn
	query string
}

func (s *fakeStmt) Close() error  { return nil }
func (s *fakeStmt) NumInput() int { return -1 }

func (s *fakeStmt) Exec([]driver.Value) (driver.Result, error) {
	return driver.RowsAffected(0), nil
}

func (s *fakeStmt) Query([]driver.Value) (driver.Rows, error) {
	if s.conn.queryErr != nil {
		return nil, s.conn.queryErr
	}
	r := *s.conn.rows
	return &r, nil
}

type fakeConn struct {
	rows     *fakeRows
	queryErr error
}

func (c *fakeConn) Prepare(query string) (driver.Stmt, error) {
	return &fakeStmt{conn: c, query: query}, nil
}
func (c *fakeConn) Close() error              { return nil }
func (c *fakeConn) Begin() (driver.Tx, error) { return nil, errors.New("no transactions") }

// fakeDriver hands out whatever the test most recently installed. Package
// state is acceptable here and nowhere else: database/sql resolves a driver
// by name at Open time, so there is no other way to parameterise it, and the
// tests using it do not run in parallel.
type fakeDriver struct{ conn *fakeConn }

var installed = &fakeDriver{}

func (d *fakeDriver) Open(string) (driver.Conn, error) {
	if d.conn == nil {
		return nil, errors.New("no connection installed")
	}
	return d.conn, nil
}

func init() { sql.Register(fakeDriverName, installed) }

// openFake installs a result set and returns a handle over it.
func openFake(t *testing.T, rows *fakeRows, queryErr error) *sql.DB {
	t.Helper()
	installed.conn = &fakeConn{rows: rows, queryErr: queryErr}
	db, err := sql.Open(fakeDriverName, "")
	if err != nil {
		t.Fatalf("open fake: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

var (
	execTable = Table{Name: "t", Alias: "t"}
	execName  = Col[string](execTable, "name")
)

// selectQuery builds a one-column statement bound to dst.
func selectQuery(t *testing.T, dst *string) Query {
	t.Helper()
	q, err := NewSelect(SQLiteDialect{}).Columns(Bind(execName, dst)).From(execTable).Build()
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	return q
}

// One's happy path, and the arm that returns rows.Err() at the end.
func TestOne_ScansTheRow(t *testing.T) {
	var got string
	q := selectQuery(t, &got)
	db := openFake(t, &fakeRows{cols: []string{"name"}, vals: [][]driver.Value{{"ada"}}}, nil)
	if err := One(context.Background(), db, q); err != nil {
		t.Fatalf("One: %v", err)
	}
	if got != "ada" {
		t.Fatalf("got %q", got)
	}
}

// Pins the `if q.Targets == nil` arm: a statement built with Exprs binds no
// destination, so executing it is a caller error caught before the database.
func TestOne_NoTargets(t *testing.T) {
	q, err := NewSelect(SQLiteDialect{}).Exprs(execName).From(execTable).Build()
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	db := openFake(t, &fakeRows{cols: []string{"name"}}, nil)
	if err := One(context.Background(), db, q); !errors.Is(err, ErrNoTargets) {
		t.Fatalf("want ErrNoTargets, got %v", err)
	}
	if err := Each(context.Background(), db, q, func() error { return nil }); !errors.Is(err, ErrNoTargets) {
		t.Fatalf("Each: want ErrNoTargets, got %v", err)
	}
}

// Pins the `db.QueryContext` error arm and its wrapping.
func TestOne_QueryError(t *testing.T) {
	var got string
	q := selectQuery(t, &got)
	sentinel := errors.New("connection refused")
	db := openFake(t, &fakeRows{cols: []string{"name"}}, sentinel)
	err := One(context.Background(), db, q)
	if !errors.Is(err, sentinel) {
		t.Fatalf("want the driver error wrapped, got %v", err)
	}
	if got := err.Error(); got[:12] != "sqlb: query:" {
		t.Fatalf("want the operation named in the message, got %q", got)
	}
}

// Pins the empty-result arm.
func TestOne_NoRows(t *testing.T) {
	var got string
	q := selectQuery(t, &got)
	db := openFake(t, &fakeRows{cols: []string{"name"}}, nil)
	if err := One(context.Background(), db, q); !errors.Is(err, ErrNoRows) {
		t.Fatalf("want ErrNoRows, got %v", err)
	}
}

// Pins the second-row arm. Taking the first row silently is the failure this
// error exists to prevent, so the test asserts the error rather than the value.
func TestOne_MultipleRows(t *testing.T) {
	var got string
	q := selectQuery(t, &got)
	db := openFake(t, &fakeRows{cols: []string{"name"}, vals: [][]driver.Value{{"ada"}, {"bo"}}}, nil)
	if err := One(context.Background(), db, q); !errors.Is(err, ErrMultipleRows) {
		t.Fatalf("want ErrMultipleRows, got %v", err)
	}
}

// Pins the Scan error arm. The driver returns two columns while the query
// bound one destination, which database/sql rejects deterministically. A
// value that merely needs converting is not enough: drivers differ on what
// they will convert, and a test that depends on that is a flake.
func TestOne_ScanError(t *testing.T) {
	var got string
	q := selectQuery(t, &got)
	db := openFake(t, &fakeRows{
		cols: []string{"name", "extra"},
		vals: [][]driver.Value{{"ada", "surplus"}},
	}, nil)
	err := One(context.Background(), db, q)
	if err == nil {
		t.Fatal("want a scan error when the result set is wider than the bindings")
	}
	if got := err.Error(); len(got) < 11 || got[:11] != "sqlb: scan:" {
		t.Fatalf("want a wrapped scan error naming the operation, got %q", got)
	}
}

// Pins the `rows.Err()` arm inside the empty branch: iteration stopped
// because the driver failed, not because the result set ended.
func TestOne_IterateError(t *testing.T) {
	var got string
	q := selectQuery(t, &got)
	sentinel := errors.New("connection lost")
	db := openFake(t, &fakeRows{cols: []string{"name"}, nextErr: sentinel}, nil)
	err := One(context.Background(), db, q)
	if !errors.Is(err, sentinel) {
		t.Fatalf("want the driver error wrapped, got %v", err)
	}
	if errors.Is(err, ErrNoRows) {
		t.Fatal("a failed iteration must not be reported as an empty result")
	}
}

// Each's happy path, and the documented invariant that every row scans into
// the same destination.
func TestEach_ReusesTheTargets(t *testing.T) {
	var got string
	q := selectQuery(t, &got)
	db := openFake(t, &fakeRows{cols: []string{"name"}, vals: [][]driver.Value{{"ada"}, {"bo"}, {"cy"}}}, nil)
	var seen []string
	if err := Each(context.Background(), db, q, func() error {
		seen = append(seen, got)
		return nil
	}); err != nil {
		t.Fatalf("Each: %v", err)
	}
	if len(seen) != 3 || seen[0] != "ada" || seen[2] != "cy" {
		t.Fatalf("got %v", seen)
	}
}

// Pins the arm where the callback aborts: the error is returned unwrapped, so
// a caller can compare it with their own sentinel.
func TestEach_CallbackErrorStopsIteration(t *testing.T) {
	var got string
	q := selectQuery(t, &got)
	db := openFake(t, &fakeRows{cols: []string{"name"}, vals: [][]driver.Value{{"ada"}, {"bo"}}}, nil)
	stop := errors.New("enough")
	calls := 0
	err := Each(context.Background(), db, q, func() error {
		calls++
		return stop
	})
	if !errors.Is(err, stop) {
		t.Fatalf("want the callback error returned as-is, got %v", err)
	}
	if calls != 1 {
		t.Fatalf("iteration continued after the callback aborted: %d calls", calls)
	}
}

// Pins Each's query-error and iterate-error arms.
func TestEach_ErrorArms(t *testing.T) {
	var got string
	q := selectQuery(t, &got)
	sentinel := errors.New("connection refused")

	db := openFake(t, &fakeRows{cols: []string{"name"}}, sentinel)
	if err := Each(context.Background(), db, q, func() error { return nil }); !errors.Is(err, sentinel) {
		t.Fatalf("query error: got %v", err)
	}

	lost := errors.New("connection lost")
	db = openFake(t, &fakeRows{cols: []string{"name"}, vals: [][]driver.Value{{"ada"}}, nextErr: lost, afterRow: 1}, nil)
	if err := Each(context.Background(), db, q, func() error { return nil }); !errors.Is(err, lost) {
		t.Fatalf("iterate error: got %v", err)
	}
}

// Pins Each's scan-error arm, the one place its error handling differs from
// One's: the failure happens mid-iteration, after rows have already been
// delivered to the callback, and must still be wrapped and returned rather
// than ending the loop quietly.
func TestEach_ScanError(t *testing.T) {
	var got string
	q := selectQuery(t, &got)
	db := openFake(t, &fakeRows{
		cols: []string{"name", "extra"},
		vals: [][]driver.Value{{"ada", "surplus"}},
	}, nil)
	calls := 0
	err := Each(context.Background(), db, q, func() error {
		calls++
		return nil
	})
	if err == nil {
		t.Fatal("want a scan error when the result set is wider than the bindings")
	}
	if got := err.Error(); len(got) < 11 || got[:11] != "sqlb: scan:" {
		t.Fatalf("want a wrapped scan error naming the operation, got %q", got)
	}
	if calls != 0 {
		t.Fatalf("the callback ran on a row that failed to scan: %d calls", calls)
	}
}

// Concurrent execution of one statement is refused rather than corrupting the
// result.
//
// A Query reads like inert data, which is what makes the mistake easy: hoist
// one to package scope, serve it from every handler, and two goroutines write
// their rows into the same variables. Nothing crashes. The rows interleave and
// the wrong values are returned, intermittently, under load only.
//
// One and Each refuse the overlap instead. A lock would be worse: it would
// serialise the executions and hand the second caller the first caller's rows
// in its own variables, silently.
func TestQuery_RefusesOverlappingExecution(t *testing.T) {
	var got string
	q := selectQuery(t, &got)
	db := openFake(t, &fakeRows{cols: []string{"name"}, vals: [][]driver.Value{{"ada"}}}, nil)

	// The overlap is created from inside Each's callback, which is the
	// deterministic way to have two executions of one Query live at once.
	var inner error
	outer := Each(context.Background(), db, q, func() error {
		inner = One(context.Background(), db, q)
		return nil
	})
	if outer != nil {
		t.Fatalf("outer Each: %v", outer)
	}
	if !errors.Is(inner, ErrQueryInUse) {
		t.Fatalf("re-executing a query while it is iterating: want ErrQueryInUse, got %v", inner)
	}

	// The guard is released afterwards, so sequential reuse still works. That
	// matters: a cached query executed one call at a time is a legitimate and
	// common pattern, and this check must not break it.
	if err := One(context.Background(), db, q); err != nil {
		t.Fatalf("sequential reuse must still work: %v", err)
	}
	if got != "ada" {
		t.Fatalf("got %q", got)
	}
}

// Two Queries built from one builder share their scan targets, because Build
// copies the pointers and not the variables. The guard belongs to the builder
// for that reason, so hoisting a builder is caught as well as hoisting a
// query.
func TestQuery_TwoBuildsFromOneBuilderShareTheGuard(t *testing.T) {
	var got string
	sel := NewSelect(SQLiteDialect{}).Columns(Bind(execName, &got)).From(execTable)
	q1, err := sel.Build()
	if err != nil {
		t.Fatal(err)
	}
	q2, err := sel.Build()
	if err != nil {
		t.Fatal(err)
	}
	db := openFake(t, &fakeRows{cols: []string{"name"}, vals: [][]driver.Value{{"ada"}}}, nil)

	var inner error
	if err := Each(context.Background(), db, q1, func() error {
		inner = One(context.Background(), db, q2)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if !errors.Is(inner, ErrQueryInUse) {
		t.Fatalf("two builds of one builder write to the same variables: want ErrQueryInUse, got %v", inner)
	}
}

// A Query assembled as a struct literal carries no guard, and gets no check.
// This is pinned so the limit is a known one rather than a surprise: the
// check is a best effort, not a guarantee.
func TestQuery_LiteralHasNoGuard(t *testing.T) {
	var got string
	built := selectQuery(t, &got)
	literal := Query{SQL: built.SQL, Args: built.Args, Targets: built.Targets}
	db := openFake(t, &fakeRows{cols: []string{"name"}, vals: [][]driver.Value{{"ada"}}}, nil)

	var inner error
	if err := Each(context.Background(), db, literal, func() error {
		inner = One(context.Background(), db, literal)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if inner != nil {
		t.Fatalf("a literal Query has no guard, so the overlap is not detected: got %v", inner)
	}
}
