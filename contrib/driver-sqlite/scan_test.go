package driversqlite_test

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"

	"github.com/ubgo/sqlb/bind"
	driversqlite "github.com/ubgo/sqlb/contrib/driver-sqlite"
	"github.com/ubgo/sqlb/dialect/sqlite"
	"github.com/ubgo/sqlb/exec"
	"github.com/ubgo/sqlb/expr"
	"github.com/ubgo/sqlb/scan"
	"github.com/ubgo/sqlb/stmt"
)

type person struct {
	Email string
	Age   int
}

var (
	people      = expr.Table{Name: "people", Alias: "p"}
	personEmail = expr.Col[string](people, "email")
	personAge   = expr.Col[int](people, "age")
)

// personReader is composed pairwise, which is the point of Map2: two
// single-column Readers become one two-column Reader with no variadic
// machinery, and the same shape reaches any width.
var personReader = scan.Map2(
	scan.Field(personEmail),
	scan.Field(personAge),
	func(e string, a int) person { return person{Email: e, Age: a} },
)

// seeded opens a database with two people in it.
func seeded(t *testing.T, dsn string) *sql.DB {
	t.Helper()
	var db *sql.DB
	var err error
	if dsn == "" {
		db, err = driversqlite.Memory()
	} else {
		db, err = driversqlite.Open(dsn)
	}
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	for _, q := range []string{
		`CREATE TABLE people (email TEXT NOT NULL, age INTEGER NOT NULL)`,
		`INSERT INTO people VALUES ('ada@x.com', 36), ('bob@x.com', 41)`,
	} {
		if _, err := db.ExecContext(context.Background(), q); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}
	return db
}

func byAge() *stmt.Select[sqlite.Dialect] {
	return stmt.NewSelect(sqlite.Dialect{}).From(people).Order(expr.OrderBy(personAge, expr.SortAsc))
}

// TestScan_ReturnsValues is the basic contract: rows come back as values, in
// order, with no destination supplied by the caller.
func TestScan_ReturnsValues(t *testing.T) {
	got, err := scan.All(context.Background(), seeded(t, ""), byAge(), personReader)
	if err != nil {
		t.Fatal(err)
	}
	want := []person{{"ada@x.com", 36}, {"bob@x.com", 41}}
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
}

// A value reader takes the first row; a bound identity query checks uniqueness.
// promise:scan-cardinality
// ds:def id=reader-cardinality-332zvda7
func TestScan_OneAndBoundOneHaveDifferentCardinality(t *testing.T) {
	db := seeded(t, "")
	got, err := scan.One(context.Background(), db, byAge(), personReader)
	if err != nil || got != (person{"ada@x.com", 36}) {
		t.Fatalf("first row: got %v, err %v", got, err)
	}
	var email string
	q, err := byAge().Columns(bind.Bind(personEmail, &email)).Build()
	if err != nil {
		t.Fatal(err)
	}
	if err := exec.One(context.Background(), db, q); !errors.Is(err, exec.ErrMultipleRows) {
		t.Fatalf("bound One: want ErrMultipleRows, got %v", err)
	}
}

// Reader columns append to the projection; database/sql requires one destination
// per result column, so an existing projection is not silently ignored.
// promise:scan-projection
// ds:def id=reader-projection-3xfnp55v
func TestScan_ExistingProjectionIsNotIgnored(t *testing.T) {
	s := byAge().Exprs(personAge)
	_, err := scan.All(context.Background(), seeded(t, ""), s, personReader)
	if err == nil || !strings.Contains(err.Error(), "expected 3 destination arguments in Scan, not 2") {
		t.Fatalf("want projection-width scan error, got %v", err)
	}
	q, err := s.Build()
	if err != nil || q.SQL != `SELECT "p"."age" FROM "people" AS "p" ORDER BY "p"."age" ASC` {
		t.Fatalf("reader changed caller's statement: %q, %v", q.SQL, err)
	}
}

// TestScan_OneReportsNoRows pins the sentinel, which aliases sql.ErrNoRows so a
// caller's existing errors.Is keeps working.
func TestScan_OneReportsNoRows(t *testing.T) {
	db, err := driversqlite.Memory()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	if _, err := db.Exec(`CREATE TABLE people (email TEXT NOT NULL, age INTEGER NOT NULL)`); err != nil {
		t.Fatal(err)
	}

	_, err = scan.One(context.Background(), db, stmt.NewSelect(sqlite.Dialect{}).From(people), personReader)
	if !errors.Is(err, scan.ErrNoRows) {
		t.Fatalf("want ErrNoRows, got %v", err)
	}
}

// TestScan_EmptyReaderIsRejected keeps an always-wrong statement from reaching
// the database, where it would come back as a syntax error naming nothing.
// promise:scan-no-columns
func TestScan_EmptyReaderIsRejected(t *testing.T) {
	_, err := scan.All(context.Background(), seeded(t, ""), byAge(), scan.Reader[int]{})
	if !errors.Is(err, scan.ErrNoColumns) {
		t.Fatalf("want ErrNoColumns, got %v", err)
	}
}

// TestScan_OneReaderIsSafeToShare is the reason this layer exists.
//
// The binding API refuses an overlapping execution with ErrQueryInUse because a
// Query holds pointers into caller variables. A Reader holds no memory: it
// allocates destinations per row. So one Reader drives many concurrent
// executions and every one must see its own correct rows. Under -race this is
// the difference between the two strategies stated as a test rather than as a
// paragraph.
// promise:scan-concurrency
// ds:def id=reader-sharing-79k4pyx5
func TestScan_OneReaderIsSafeToShare(t *testing.T) {
	db := seeded(t, "file:sharedreader?mode=memory&cache=shared")
	ctx := context.Background()

	const goroutines = 64
	var wg sync.WaitGroup
	errs := make(chan error, goroutines)

	for range goroutines {
		wg.Add(1)
		go func() {
			defer wg.Done()
			got, err := scan.All(ctx, db, byAge(), personReader)
			if err != nil {
				errs <- err
				return
			}
			if want := `[{ada@x.com 36} {bob@x.com 41}]`; fmt.Sprint(got) != want {
				errs <- fmt.Errorf("got %v, want %s", got, want)
			}
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Fatalf("a shared Reader produced a wrong or failed read: %v", err)
	}
}
