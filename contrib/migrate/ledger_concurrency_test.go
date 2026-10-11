package migrate_test

import (
	"context"
	"database/sql"
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ubgo/sqlb"
	"github.com/ubgo/sqlb/contrib/migrate"
	"github.com/ubgo/sqlb/ddl"
)

// The pragmas contrib/driver-sqlite opens a database with, spelled for
// modernc.org/sqlite directly because no contrib module depends on a
// sibling. WAL and a busy timeout are what a real deployment runs, and the
// combination under which two deploys on one file used to fail.
const (
	walPragmas  = "?_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)"
	busyPragmas = "?_pragma=busy_timeout(5000)"
)

// openSQLite opens its own handle on path, as a second process would.
func openSQLite(t *testing.T, path, pragmas string) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", path+pragmas)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

// holdWriteLock takes SQLite's write lock on its own connection and returns
// the function that releases it, so a test can hold the lock while the code
// under test starts rather than hoping two goroutines overlap.
func holdWriteLock(t *testing.T, path string) func() {
	t.Helper()
	ctx := context.Background()
	db := openSQLite(t, path, "")
	conn, err := db.Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := conn.ExecContext(ctx, "BEGIN IMMEDIATE"); err != nil {
		t.Fatal(err)
	}
	var once sync.Once
	release := func() {
		once.Do(func() {
			if _, err := conn.ExecContext(ctx, "COMMIT"); err != nil {
				t.Error(err)
			}
			_ = conn.Close()
		})
	}
	t.Cleanup(release)
	return release
}

// stillWaitingAfter is how long a call must keep waiting while the lock is
// held before the test believes it is waiting rather than slow to fail: a
// refusal returns in microseconds, so 300ms is generous. finishWithin bounds
// the wait after release, far above the milliseconds the work takes, so a
// call that never wakes fails the test instead of hanging it.
const (
	stillWaitingAfter = 300 * time.Millisecond
	finishWithin      = 30 * time.Second
)

type applyResult struct {
	got []string
	err error
}

// applyWhileHeld starts apply while the write lock is held, requires it to
// still be waiting after a pause, releases the lock and returns its result.
// A call that returned while the lock was held failed rather than waited,
// which is the defect these tests pin.
func applyWhileHeld(t *testing.T, release func(), apply func() ([]string, error)) applyResult {
	t.Helper()
	done := make(chan applyResult, 1)
	go func() {
		got, err := apply()
		done <- applyResult{got, err}
	}()
	select {
	case r := <-done:
		t.Fatalf("Apply returned while another connection held the write lock, instead of waiting: %v, %v", r.got, r.err)
	case <-time.After(stillWaitingAfter):
	}
	release()
	select {
	case r := <-done:
		return r
	case <-time.After(finishWithin):
		t.Fatal("Apply never finished after the write lock was released")
	}
	return applyResult{}
}

// TestLedger_SQLiteWaitsForTheWriteLockInsteadOfFailingAnUpgrade pins the
// transaction half of issue #2. A migration whose first statement reads, a
// SkipIf guard here, opened a deferred transaction holding a read lock; its
// write then needed the write lock another connection held, and SQLite
// refuses that upgrade with SQLITE_BUSY at once, without consulting
// busy_timeout, because waiting could deadlock. The ledger now begins its
// SQLite transactions IMMEDIATE, so the wait happens at BEGIN, where the busy
// timeout applies, and the migration runs once the lock is free.
//
// It runs twice: with a busy timeout, where BEGIN IMMEDIATE waits inside
// SQLite, and without one, where the ledger's own retry of a refused BEGIN is
// what waits.
func TestLedger_SQLiteWaitsForTheWriteLockInsteadOfFailingAnUpgrade(t *testing.T) {
	for name, pragmas := range map[string]string{"busy timeout": busyPragmas, "no busy timeout": ""} {
		t.Run(name, func(t *testing.T) { waitsForTheWriteLock(t, pragmas) })
	}
}

func waitsForTheWriteLock(t *testing.T, pragmas string) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "up.db")
	db := openSQLite(t, path, pragmas)
	l, err := migrate.NewLedger(sqlb.SQLiteDialect{}, "", nil)
	if err != nil {
		t.Fatal(err)
	}
	// Create the ledger table first, so the Apply under test writes nothing
	// before the migration's own transaction.
	if _, err := l.ApplyDB(ctx, db); err != nil {
		t.Fatal(err)
	}
	guarded := migrate.Migration{Owner: "lib", Name: "001_guarded", Script: ddl.Script{{
		SQL:           "CREATE TABLE up_t (id INTEGER)",
		Transactional: true,
		SkipIf:        "SELECT 1 FROM sqlite_master WHERE name = 'up_t'",
	}}}
	release := holdWriteLock(t, path)
	r := applyWhileHeld(t, release, func() ([]string, error) { return l.ApplyDB(ctx, db, guarded) })
	if r.err != nil || strings.Join(r.got, ",") != "lib/001_guarded" {
		t.Fatalf("the migration must run once the write lock is free: %v, %v", r.got, r.err)
	}
}

// TestLedger_SQLiteWaitsWhenItsConnectionCannotOpen pins the other half of
// issue #2, and the one sqlstore actually hit: on a database not yet in WAL
// mode, the connection ApplyDB opens runs PRAGMA journal_mode=WAL, which
// needs every other connection out of the way and fails with SQLITE_BUSY
// regardless of busy_timeout. Two processes opening a fresh file at once is
// exactly that. ApplyDB now retries the open while the error is SQLite's
// busy, for as long as the context allows.
func TestLedger_SQLiteWaitsWhenItsConnectionCannotOpen(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "open.db")
	db := openSQLite(t, path, walPragmas)
	l, err := migrate.NewLedger(sqlb.SQLiteDialect{}, "", migrate.EngineLock(sqlb.SQLiteDialect{}, "open"))
	if err != nil {
		t.Fatal(err)
	}
	release := holdWriteLock(t, path)
	r := applyWhileHeld(t, release, func() ([]string, error) { return l.ApplyDB(ctx, db, migrations("op")...) })
	if r.err != nil || strings.Join(r.got, ",") != "lib/001_create,lib/002_add" {
		t.Fatalf("the migrations must run once the database can be opened: %v, %v", r.got, r.err)
	}
}

// TestLedger_SQLiteTwoProcessesOnAFreshFile is issue #2's acceptance as a
// deploy meets it: two handles on one new file, each its own pool as two
// processes would have, apply the same history at once; both succeed and
// every migration is recorded once. It is not the proof of the waits, the two
// tests above are, because a race only fails when its timing lines up; it is
// the end-to-end shape, run over several fresh files so the open race has a
// chance to occur, which before the fix it did in about one run in five.
func TestLedger_SQLiteTwoProcessesOnAFreshFile(t *testing.T) {
	ctx := context.Background()
	d := sqlb.SQLiteDialect{}
	var ms []migrate.Migration
	for i := range 5 {
		ms = append(ms, migrate.Migration{Owner: "lib", Name: fmt.Sprintf("%03d", i), Script: ddl.Script{
			{SQL: fmt.Sprintf("CREATE TABLE fr_%d (id INTEGER)", i), Transactional: true, SkipIf: fmt.Sprintf("SELECT 1 FROM sqlite_master WHERE name = 'fr_%d'", i)},
		}})
	}
	for round := range 10 {
		path := filepath.Join(t.TempDir(), fmt.Sprintf("fresh%d.db", round))
		l, err := migrate.NewLedger(d, "", migrate.EngineLock(d, "fresh"))
		if err != nil {
			t.Fatal(err)
		}
		results := make([]applyResult, 2)
		var wg sync.WaitGroup
		for i := range results {
			db := openSQLite(t, path, walPragmas)
			wg.Add(1)
			go func() {
				defer wg.Done()
				got, err := l.ApplyDB(ctx, db, ms...)
				results[i] = applyResult{got, err}
			}()
		}
		wg.Wait()
		total := 0
		for i, r := range results {
			if r.err != nil {
				t.Fatalf("round %d, deploy %d: %v", round, i, r.err)
			}
			total += len(r.got)
		}
		if total != len(ms) {
			t.Fatalf("round %d: %d migrations applied between the two deploys, want %d: %v", round, total, len(ms), results)
		}
		var n int
		if err := openSQLite(t, path, walPragmas).QueryRowContext(ctx, "SELECT count(*) FROM "+migrate.DefaultLedgerTable).Scan(&n); err != nil || n != len(ms) {
			t.Fatalf("round %d: ledger rows %d, %v", round, n, err)
		}
	}
}
