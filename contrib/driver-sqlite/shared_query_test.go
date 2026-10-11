package driversqlite_test

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"sync"
	"testing"

	"github.com/ubgo/sqlb"
	driversqlite "github.com/ubgo/sqlb/contrib/driver-sqlite"
)

// The race that started this, re-run against a real database with a real
// connection pool. It is now refused instead of corrupting the result.
func TestSharedQuery_IsRefusedNotRaced(t *testing.T) {
	path := filepath.Join(t.TempDir(), "r.db")
	db, err := driversqlite.Open(path, driversqlite.WithMaxOpenConns(8))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	ctx := context.Background()
	if _, err := db.ExecContext(ctx, `CREATE TABLE person (id INTEGER PRIMARY KEY, name TEXT)`); err != nil {
		t.Fatal(err)
	}
	for i := 1; i <= 200; i++ {
		if _, err := db.ExecContext(ctx, `INSERT INTO person VALUES (?, ?)`, i, fmt.Sprintf("n%d", i)); err != nil {
			t.Fatal(err)
		}
	}

	p := sqlb.Table{Name: "person", Alias: "p"}
	pid, pname := sqlb.Col[int64](p, "id"), sqlb.Col[string](p, "name")

	// One query, hoisted, shared by eight goroutines: the shape that used to
	// be a data race and silently interleaved rows.
	var id int64
	var name string
	q, err := sqlb.NewSelect(sqlb.SQLiteDialect{}).
		Columns(sqlb.Bind(pid, &id), sqlb.Bind(pname, &name)).From(p).Build()
	if err != nil {
		t.Fatal(err)
	}

	// The overlap is arranged rather than raced. Eight goroutines calling Each
	// at once USUALLY overlap, and the first version of this test asserted
	// that they did — which fails under load when they happen to serialise and
	// nothing is refused, and worse, would PASS with the guard removed on a
	// run where they also serialised. contrib/migrate's lock test was rewritten
	// for the same reason: a test of a mutual exclusion has to hold the thing
	// rather than race for it.
	//
	// So one execution is held open inside its own callback, and the second is
	// made while the first is provably still running.
	holding := make(chan struct{})
	release := make(chan struct{})
	var first error
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		once := false
		first = sqlb.Each(ctx, db, q, func() error {
			if !once {
				once = true
				close(holding)
				<-release
			}
			_ = id
			_ = name
			return nil
		})
	}()

	<-holding
	second := sqlb.Each(ctx, db, q, func() error { return nil })
	close(release)
	wg.Wait()

	if !errors.Is(second, sqlb.ErrQueryInUse) {
		t.Fatalf("an execution overlapping a held one must be refused with ErrQueryInUse, got %v", second)
	}
	if first != nil {
		t.Fatalf("the held execution must succeed: %v", first)
	}

	// And the guard is released afterwards, or refusing an overlap would only
	// have moved the bug: the same query must run again once nothing holds it.
	if err := sqlb.Each(ctx, db, q, func() error { return nil }); err != nil {
		t.Fatalf("the query must run again once the first execution is done: %v", err)
	}
}
