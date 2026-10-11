package scan_test

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"io"
	"strings"
	"testing"

	"github.com/ubgo/sqlb/dialect/postgres"
	"github.com/ubgo/sqlb/render"
	"github.com/ubgo/sqlb/scan"
	"github.com/ubgo/sqlb/stmt"
)

// A driver that fails on demand, so the failures this package reports can be
// proven here rather than only where a real database happens to produce one.
//
// It is registered with database/sql, which is the standard library, so the
// core module gains no dependency. The reason it is worth having: every
// failure below is reported by wrapping an error from database/sql, and a
// wrapper that dropped the error, or reported the wrong one, would look
// identical in a test that only asked whether something went wrong. The
// conformance suite in contrib/driver-sqlite proves the successful paths
// against a real engine; this proves the unhappy ones, which a working engine
// does not produce on request.

// errQuery and errNext are what the fake driver fails with, each
// distinguishable so a test can assert which one came back.
var (
	errQuery = errors.New("fake: the query failed")
	errNext  = errors.New("fake: the connection dropped mid-result")
)

// faultDriver serves one behaviour per DSN; see faultDB for the names.
type faultDriver struct{}

func (faultDriver) Open(name string) (driver.Conn, error) { return faultConn{mode: name}, nil }

type faultConn struct{ mode string }

func (c faultConn) Prepare(string) (driver.Stmt, error) {
	return faultStmt{behaviour: c.mode}, nil
}
func (faultConn) Close() error              { return nil }
func (faultConn) Begin() (driver.Tx, error) { return nil, errors.New("fake: no transactions") }

// faultStmt carries the behaviour its connection was opened with.
type faultStmt struct{ behaviour string }

func (faultStmt) Close() error  { return nil }
func (faultStmt) NumInput() int { return -1 }

func (faultStmt) Exec([]driver.Value) (driver.Result, error) {
	return nil, errors.New("fake: no writes")
}

func (s faultStmt) Query([]driver.Value) (driver.Rows, error) {
	if s.behaviour == "queryfails" {
		return nil, errQuery
	}
	return &faultRows{mode: s.behaviour}, nil
}

// faultRows yields one row and then does whatever its mode says.
type faultRows struct {
	mode string
	sent bool
}

func (r *faultRows) Columns() []string {
	switch r.mode {
	case "twocolumns":
		return []string{"n", "m"}
	case "dupcolumns":
		// What an ordinary join produces: two columns of the same name.
		return []string{"n", "n"}
	}
	return []string{"n"}
}
func (faultRows) Close() error { return nil }

func (r *faultRows) Next(dest []driver.Value) error {
	switch r.mode {
	case "nextfails":
		return errNext
	case "wrongtype":
		if r.sent {
			return io.EOF
		}
		r.sent = true
		// A string where the caller's destination is an int64: database/sql
		// converts what it can and refuses this.
		dest[0] = "not a number"
		return nil
	case "norows":
		return io.EOF
	case "twocolumns", "dupcolumns":
		if r.sent {
			return io.EOF
		}
		r.sent = true
		dest[0], dest[1] = int64(1), int64(2)
		return nil
	default:
		if r.sent {
			return io.EOF
		}
		r.sent = true
		dest[0] = int64(1)
		return nil
	}
}

func init() { sql.Register("sqlb-scan-fault", faultDriver{}) }

// faultDB opens a handle whose rows behave as mode says: "queryfails" refuses
// the query, "nextfails" drops the connection after the first row, "wrongtype"
// returns a value the destination cannot hold, "norows" returns none, and
// anything else returns one row holding 1.
func faultDB(t *testing.T, mode string) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlb-scan-fault", mode)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

func sel() *stmt.Select[postgres.Dialect] {
	return stmt.NewSelect(postgres.Dialect{}).From(people)
}

// TestScan_ReportsEveryFailureItsOwnWay pins that a failure reaching this
// package comes back naming where it happened and carrying the error the
// database gave, for every entry point that runs a statement. Each stage has
// its own prefix, which is the whole point: "query" means the statement never
// ran, "scan" means a row could not be read into the destination, and "rows"
// means the result was cut short after rows had already been returned — three
// different things to go and look at.
func TestScan_ReportsEveryFailureItsOwnWay(t *testing.T) {
	ctx := context.Background()
	reader := scan.Field(age)
	for name, tc := range map[string]struct {
		mode   string
		run    func(db *sql.DB) error
		prefix string
		cause  error
	}{
		"All: the query is refused": {"queryfails", func(db *sql.DB) error {
			_, err := scan.All(ctx, db, sel(), reader)
			return err
		}, "sqlb/scan: query", errQuery},
		"All: a row does not fit its destination": {"wrongtype", func(db *sql.DB) error {
			_, err := scan.All(ctx, db, sel(), reader)
			return err
		}, "sqlb/scan: scan", nil},
		"All: the result is cut short": {"nextfails", func(db *sql.DB) error {
			_, err := scan.All(ctx, db, sel(), reader)
			return err
		}, "sqlb/scan: rows", errNext},
		"One: the query is refused": {"queryfails", func(db *sql.DB) error {
			_, err := scan.One(ctx, db, sel(), reader)
			return err
		}, "sqlb/scan: query", errQuery},
		"One: a row does not fit its destination": {"wrongtype", func(db *sql.DB) error {
			_, err := scan.One(ctx, db, sel(), reader)
			return err
		}, "sqlb/scan: scan", nil},
		"One: the result is cut short before the first row": {"nextfails", func(db *sql.DB) error {
			_, err := scan.One(ctx, db, sel(), reader)
			return err
		}, "sqlb/scan: rows", errNext},
		"Maps: the query is refused": {"queryfails", func(db *sql.DB) error {
			_, err := scan.Maps(ctx, db, mustBuild(t))
			return err
		}, "sqlb/scan: query", errQuery},
		"Maps: the result is cut short": {"nextfails", func(db *sql.DB) error {
			_, err := scan.Maps(ctx, db, mustBuild(t))
			return err
		}, "sqlb/scan: rows", errNext},
		"Slices: the query is refused": {"queryfails", func(db *sql.DB) error {
			_, _, err := scan.Slices(ctx, db, mustBuild(t))
			return err
		}, "sqlb/scan: query", errQuery},
	} {
		t.Run(name, func(t *testing.T) {
			err := tc.run(faultDB(t, tc.mode))
			if err == nil {
				t.Fatal("the failure was swallowed")
			}
			if !strings.HasPrefix(err.Error(), tc.prefix) {
				t.Fatalf("got %q, want it to start with %q", err, tc.prefix)
			}
			if tc.cause != nil && !errors.Is(err, tc.cause) {
				t.Fatalf("got %v, which does not carry the database's own error", err)
			}
		})
	}
}

// TestOne_NoRowsIsErrNoRows pins the difference between a result that is empty
// and one that failed: both end the iteration, and only one of them is a
// problem with the query. ErrNoRows aliases sql.ErrNoRows, so a caller's
// existing check keeps working.
func TestOne_NoRowsIsErrNoRows(t *testing.T) {
	_, err := scan.One(context.Background(), faultDB(t, "norows"), sel(), scan.Field(age))
	if !errors.Is(err, scan.ErrNoRows) || !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("got %v, want ErrNoRows", err)
	}
}

// TestDynamic_ReadsARowWithoutAReader pins the successful dynamic path against
// a driver rather than only against a real engine, so the two shapes the
// escape hatch returns — a map keyed by column name and a positional slice —
// are pinned here beside the failures above.
func TestDynamic_ReadsARowWithoutAReader(t *testing.T) {
	ctx := context.Background()
	rows, err := scan.Maps(ctx, faultDB(t, "onerow"), mustBuild(t))
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 || rows[0]["n"] != int64(1) {
		t.Fatalf("got %v, want one row holding n=1", rows)
	}
	names, vals, err := scan.Slices(ctx, faultDB(t, "onerow"), mustBuild(t))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(names, ",") != "n" || len(vals) != 1 || vals[0][0] != int64(1) {
		t.Fatalf("got %v %v, want one row holding n=1", names, vals)
	}
}

// mustBuild is the statement the dynamic cases run; the fake driver ignores
// its text and answers by mode.
func mustBuild(t *testing.T) stmt.Query {
	t.Helper()
	q, err := sel().Exprs(age).Build()
	if err != nil {
		t.Fatal(err)
	}
	return q
}

// TestScan_RunsTheStatementItWasGiven is the control for the failure table
// above: with a driver that answers normally, every entry point returns the
// row. Without it the refusals prove only that the package fails, not that it
// fails for the reason each case claims.
func TestScan_RunsTheStatementItWasGiven(t *testing.T) {
	ctx := context.Background()
	reader := scan.Field(age)
	got, err := scan.All(ctx, faultDB(t, "onerow"), sel(), reader)
	if err != nil || len(got) != 1 || got[0] != 1 {
		t.Fatalf("All: got %v, %v", got, err)
	}
	one, err := scan.One(ctx, faultDB(t, "onerow"), sel(), reader)
	if err != nil || one != 1 {
		t.Fatalf("One: got %v, %v", one, err)
	}
}

// TestNew_BuildsAReaderFromOutside pins the escape hatch the result layer
// needs to be extensible. A Reader cannot otherwise be made outside this
// package, which is what left a reflective struct mapper able to produce
// bindings and not a Reader; New is that door, and the contract it states —
// alloc allocates per row and the closure reads those destinations — is what
// keeps a Reader safe to share. The two rows here are read through one Reader,
// so a New that captured its destinations once would return the same value
// twice.
func TestNew_BuildsAReaderFromOutside(t *testing.T) {
	type pair struct{ a, b int64 }
	reader := scan.New([]render.SQLer{age, age}, func() ([]any, func() pair) {
		var p pair
		return []any{&p.a, &p.b}, func() pair { return p }
	})
	got, err := scan.All(context.Background(), faultDB(t, "twocolumns"),
		stmt.NewSelect(postgres.Dialect{}).From(people), reader)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0] != (pair{1, 2}) {
		t.Fatalf("got %v, want [{1 2}]", got)
	}
}

// TestReaderEntryPoints_RefuseAReaderThatSelectsNothing pins that the zero
// Reader is caught before a statement is built rather than after the database
// has reported a syntax error. A SELECT with an empty list is always a
// mistake, and ErrNoColumns names the Reader as the thing to go and look at.
func TestReaderEntryPoints_RefuseAReaderThatSelectsNothing(t *testing.T) {
	ctx := context.Background()
	db := faultDB(t, "onerow")
	if _, err := scan.All(ctx, db, sel(), scan.Reader[int]{}); !errors.Is(err, scan.ErrNoColumns) {
		t.Fatalf("All: got %v, want ErrNoColumns", err)
	}
	if _, err := scan.One(ctx, db, sel(), scan.Reader[int]{}); !errors.Is(err, scan.ErrNoColumns) {
		t.Fatalf("One: got %v, want ErrNoColumns", err)
	}
}

// TestMaps_RefusesTwoColumnsOfOneName pins the failure the map shape cannot
// represent. An ordinary join selects two columns called "id", which every
// engine accepts; a map would silently keep one of them, and which one depends
// on the order. It is an error naming the column, and Slices, which is
// positional, is the answer.
func TestMaps_RefusesTwoColumnsOfOneName(t *testing.T) {
	ctx := context.Background()
	_, err := scan.Maps(ctx, faultDB(t, "dupcolumns"), mustBuild(t))
	if !errors.Is(err, scan.ErrDuplicateColumn) {
		t.Fatalf("got %v, want ErrDuplicateColumn", err)
	}
	if !strings.Contains(err.Error(), `"n"`) {
		t.Errorf("the error must name the column, got %v", err)
	}
	// Slices takes the same result positionally.
	names, vals, err := scan.Slices(ctx, faultDB(t, "dupcolumns"), mustBuild(t))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(names, ",") != "n,n" || len(vals) != 1 || len(vals[0]) != 2 {
		t.Fatalf("got %v %v", names, vals)
	}
}

// TestEachMap_StopsWhenTheCallbackSaysSo pins that an error from the callback
// ends the iteration and comes back as it was, unwrapped: it is the caller's
// error, not a database failure, and prefixing it would send them looking at
// the query.
func TestEachMap_StopsWhenTheCallbackSaysSo(t *testing.T) {
	stop := errors.New("enough")
	seen := 0
	err := scan.EachMap(context.Background(), faultDB(t, "onerow"), mustBuild(t), func(scan.Row) error {
		seen++
		return stop
	})
	if !errors.Is(err, stop) {
		t.Fatalf("got %v, want the callback's own error", err)
	}
	if err.Error() != stop.Error() {
		t.Fatalf("the callback's error was wrapped: %v", err)
	}
	if seen != 1 {
		t.Fatalf("the callback ran %d times, want 1", seen)
	}
}
