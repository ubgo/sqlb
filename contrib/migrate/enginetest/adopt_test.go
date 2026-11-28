package enginetest

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ubgo/sqlb"
	"github.com/ubgo/sqlb/contrib/migrate"
	"github.com/ubgo/sqlb/ddl"
	"github.com/ubgo/sqlb/render"
)

// widgetsHistory is a library's history as its code knows it: a table, then
// two columns added in later releases. ADD COLUMN cannot be written to repeat
// safely on SQLite or MySQL, so running 0002 again fails.
func widgetsHistory(prefix string) []migrate.Migration {
	tbl := prefix + "_widgets"
	return []migrate.Migration{
		{Owner: "acme", Name: "0001_widgets", Script: ddl.Script{{SQL: "CREATE TABLE " + tbl + " (id INTEGER NOT NULL PRIMARY KEY)", Transactional: true}}},
		{Owner: "acme", Name: "0002_color", Script: ddl.Script{{SQL: "ALTER TABLE " + tbl + " ADD COLUMN color VARCHAR(32)", Transactional: true}}},
		{Owner: "acme-plugin", Name: "0001_size", Script: ddl.Script{{SQL: "ALTER TABLE " + tbl + " ADD COLUMN size INTEGER", Transactional: true}}},
	}
}

// oldLedger brings a database to the state an earlier ledger of the library's
// own left: every migration of history ran, by hand here, and an old_ledger
// table lists each with the time it ran.
func oldLedger(t *testing.T, db *sql.DB, prefix string, history []migrate.Migration, ranAt []time.Time) {
	t.Helper()
	ctx := context.Background()
	for _, m := range history {
		for _, st := range m.Script {
			if _, err := db.ExecContext(ctx, st.SQL); err != nil {
				t.Fatal(err)
			}
		}
	}
	if _, err := db.ExecContext(ctx, "CREATE TABLE "+prefix+"_old_ledger (owner VARCHAR(64) NOT NULL, name VARCHAR(64) NOT NULL, ran_at VARCHAR(64) NOT NULL, PRIMARY KEY (owner, name))"); err != nil {
		t.Fatal(err)
	}
	for i, m := range history {
		q := fmt.Sprintf("INSERT INTO %s_old_ledger (owner, name, ran_at) VALUES ('%s', '%s', '%s')", prefix, m.Owner, m.Name, ranAt[i].UTC().Format(time.RFC3339Nano))
		if _, err := db.ExecContext(ctx, q); err != nil {
			t.Fatal(err)
		}
	}
}

// ranFromOldLedger is the Ran a library writes: it reads its old ledger and
// says which migrations ran, and when.
func ranFromOldLedger(t *testing.T, db *sql.DB, prefix string) migrate.Ran {
	t.Helper()
	rows, err := db.QueryContext(context.Background(), "SELECT owner, name, ran_at FROM "+prefix+"_old_ledger")
	if err != nil {
		t.Fatal(err)
	}
	ran := map[[2]string]time.Time{}
	for rows.Next() {
		var o, n, at string
		if err := rows.Scan(&o, &n, &at); err != nil {
			t.Fatal(err)
		}
		when, err := time.Parse(time.RFC3339Nano, at)
		if err != nil {
			t.Fatal(err)
		}
		ran[[2]string{o, n}] = when
	}
	if err := errors.Join(rows.Err(), rows.Close()); err != nil {
		t.Fatal(err)
	}
	return func(m migrate.Migration) (time.Time, bool) {
		at, ok := ran[[2]string{m.Owner, m.Name}]
		return at, ok
	}
}

// countRows is how many rows the ledger table holds.
func countRows(t *testing.T, db *sql.DB, d render.Dialect, table string) int {
	t.Helper()
	var n int
	if err := db.QueryRowContext(context.Background(), "SELECT count(*) FROM "+d.QuoteIdent(table)).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

// adoptSuite is what every engine must do with an existing history: Apply
// alone re-runs it and fails; Adopt records what the old ledger lists, with
// its times and Apply's checksums, and Apply then runs only what is new.
// Adopting twice records nothing; an edited migration, an unknown row under
// RefuseUnknown, and a race with Apply or another Adopt on the lock all
// behave as Apply does.
func adoptSuite(t *testing.T, d render.Dialect, open func(t *testing.T) *sql.DB, prefix string) {
	ctx := context.Background()
	db := open(t)
	history := widgetsHistory(prefix)
	ranAt := []time.Time{
		time.Date(2025, 3, 1, 10, 0, 0, 0, time.UTC),
		time.Date(2025, 6, 1, 10, 0, 0, 0, time.UTC),
		time.Date(2025, 9, 1, 10, 0, 0, 0, time.UTC),
	}
	oldLedger(t, db, prefix, history, ranAt)
	ledgerTable := prefix + "_ledger"
	l, err := migrate.NewLedger(d, ledgerTable, migrate.EngineLock(d, prefix+"_lock"))
	if err != nil {
		t.Fatal(err)
	}
	next := migrate.Migration{Owner: "acme", Name: "0003_weight", Script: ddl.Script{{SQL: "ALTER TABLE " + prefix + "_widgets ADD COLUMN weight INTEGER", Transactional: true}}}
	all := append(slices.Clone(history), next)

	// Without adoption, Apply runs 0001 again on a database that has it.
	if _, err := l.ApplyDB(ctx, db, all...); err == nil {
		t.Fatal("Apply over an existing history succeeded, so this test proves nothing")
	}

	// Adopt what the old ledger lists, then Apply the rest.
	adopted, err := l.AdoptDB(ctx, db, ranFromOldLedger(t, db, prefix), all...)
	if err != nil || strings.Join(adopted, ",") != "acme/0001_widgets,acme/0002_color,acme-plugin/0001_size" {
		t.Fatalf("adopt: %v, %v", adopted, err)
	}
	applied, err := l.ApplyDB(ctx, db, all...)
	if err != nil || strings.Join(applied, ",") != "acme/0003_weight" {
		t.Fatalf("apply after adopting must run only the new migration: %v, %v", applied, err)
	}
	if _, err := db.ExecContext(ctx, "INSERT INTO "+prefix+"_widgets (id, color, size, weight) VALUES (1, 'red', 2, 3)"); err != nil {
		t.Fatalf("the adopted and applied columns are not all there: %v", err)
	}
	rows, err := l.AppliedDB(ctx, db)
	if err != nil || len(rows) != 4 {
		t.Fatalf("applied: %v, %v", rows, err)
	}
	for i, m := range history {
		r := rows[i]
		if r.Owner != m.Owner || r.Name != m.Name || r.Checksum != migrate.Checksum(m.Script) || !r.AppliedAt.Equal(ranAt[i]) {
			t.Errorf("adopted row %d is %+v, want %s/%s with Apply's checksum at %v", i, r, m.Owner, m.Name, ranAt[i])
		}
	}

	// Adopting twice changes nothing.
	if again, err := l.AdoptDB(ctx, db, nil, all...); err != nil || len(again) != 0 {
		t.Fatalf("adopting twice: %v, %v", again, err)
	}

	// An edited migration is refused, and nothing else in the call is
	// recorded: one transaction, checked before it opens.
	extra := migrate.Migration{Owner: "acme", Name: "0004_never", Script: ddl.Script{{SQL: "SELECT 1", Transactional: true}}}
	edited := append(slices.Clone(all), extra)
	edited[1].Script = ddl.Script{{SQL: "ALTER TABLE " + prefix + "_widgets ADD COLUMN colour VARCHAR(32)", Transactional: true}}
	if _, err := l.AdoptDB(ctx, db, nil, edited...); !errors.Is(err, migrate.ErrChecksumMismatch) || !strings.Contains(err.Error(), "acme/0002_color") {
		t.Fatalf("adopting an edited migration: %v", err)
	}
	if n := countRows(t, db, d, ledgerTable); n != 4 {
		t.Fatalf("a refused adoption recorded something: %d rows", n)
	}

	// RefuseUnknown judges Adopt as it judges Apply.
	if _, err := l.RefuseUnknown().AdoptDB(ctx, db, nil, history[:1]...); !errors.Is(err, migrate.ErrUnknownMigration) || !strings.Contains(err.Error(), "acme/0002_color") {
		t.Fatalf("RefuseUnknown on Adopt: %v", err)
	}

	// Adopt while another process applies: a holds the lock and applies 0005;
	// the waiting Adopt must find it recorded and adopt nothing.
	m5 := migrate.Migration{Owner: "acme", Name: "0005_label", Script: ddl.Script{{SQL: "ALTER TABLE " + prefix + "_widgets ADD COLUMN label VARCHAR(32)", Transactional: true}}}
	holdAndRun(t, d, db, prefix, func(unlocked migrate.Ledger, a *sql.Conn) {
		if got, err := unlocked.Apply(ctx, a, m5); err != nil || len(got) != 1 {
			t.Fatalf("the holder's apply: %v, %v", got, err)
		}
	}, func() ([]string, error) {
		return l.AdoptDB(ctx, db, nil, append(slices.Clone(all), m5)...)
	})

	// Apply while another process adopts: a holds the lock and adopts 0006,
	// whose statement would fail if it ran; the waiting Apply must skip it.
	m6 := migrate.Migration{Owner: "acme", Name: "0006_done_elsewhere", Script: ddl.Script{{SQL: "INSERT INTO nowhere VALUES (1)", Transactional: true}}}
	holdAndRun(t, d, db, prefix, func(unlocked migrate.Ledger, a *sql.Conn) {
		if got, err := unlocked.Adopt(ctx, a, nil, m6); err != nil || len(got) != 1 {
			t.Fatalf("the holder's adopt: %v, %v", got, err)
		}
	}, func() ([]string, error) {
		return l.ApplyDB(ctx, db, append(slices.Clone(all), m5, m6)...)
	})
	if n := countRows(t, db, d, ledgerTable); n != 6 {
		t.Fatalf("ledger rows %d after the two races, want 6", n)
	}

	// Two processes adopting at once: every migration recorded exactly once,
	// between them, and neither fails.
	race := []migrate.Migration{
		{Owner: "acme-race", Name: "a", Script: ddl.Script{{SQL: "SELECT 1", Transactional: true}}},
		{Owner: "acme-race", Name: "b", Script: ddl.Script{{SQL: "SELECT 2", Transactional: true}}},
	}
	var wg sync.WaitGroup
	got := make([][]string, 2)
	errs := make([]error, 2)
	for i := range 2 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			got[i], errs[i] = l.AdoptDB(ctx, db, nil, race...)
		}()
	}
	wg.Wait()
	if errs[0] != nil || errs[1] != nil {
		t.Fatalf("two adopters: %v", errors.Join(errs...))
	}
	both := append(slices.Clone(got[0]), got[1]...)
	slices.Sort(both)
	if strings.Join(both, ",") != "acme-race/a,acme-race/b" {
		t.Fatalf("two adopters recorded %v between them, want each once", both)
	}
	if n := countRows(t, db, d, ledgerTable); n != 8 {
		t.Fatalf("ledger rows %d after two adopters, want 8", n)
	}
}

// holdAndRun makes a race deterministic: connection a holds the ledger's lock
// while waiter starts, so waiter must wait; holder then does its work on a
// with an unlocked ledger, a releases the lock, and waiter must succeed and
// find nothing left to do.
func holdAndRun(t *testing.T, d render.Dialect, db *sql.DB, prefix string, holder func(unlocked migrate.Ledger, a *sql.Conn), waiter func() ([]string, error)) {
	t.Helper()
	ctx := context.Background()
	lock := migrate.EngineLock(d, prefix+"_lock")
	a, err := db.Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = a.Close() }()
	if err := lock.Lock(ctx, a); err != nil {
		t.Fatal(err)
	}
	type result struct {
		got []string
		err error
	}
	done := make(chan result, 1)
	go func() {
		got, err := waiter()
		done <- result{got, err}
	}()
	select {
	case r := <-done:
		t.Fatalf("the waiter ran while the lock was held: %v, %v", r.got, r.err)
	case <-time.After(300 * time.Millisecond):
	}
	unlocked, err := migrate.NewLedger(d, prefix+"_ledger", nil)
	if err != nil {
		t.Fatal(err)
	}
	holder(unlocked, a)
	if err := lock.Unlock(ctx, a); err != nil {
		t.Fatal(err)
	}
	select {
	case r := <-done:
		if r.err != nil || len(r.got) != 0 {
			t.Fatalf("the waiter must find the holder's work done: %v, %v", r.got, r.err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("the waiter never got the lock")
	}
}

// scriptSuite runs Script's statements by hand, one Exec each, as a person
// or an outside tool would, and requires Apply afterwards to apply nothing,
// report no mismatch, and read back the clock the script was written with.
func scriptSuite(t *testing.T, d render.Dialect, open func(t *testing.T) *sql.DB, prefix string) {
	ctx := context.Background()
	db := open(t)
	at := time.Date(2026, 1, 2, 3, 4, 5, 6, time.UTC)
	l, err := migrate.NewLedger(d, prefix+"_ledger", migrate.EngineLock(d, prefix+"_lock"))
	if err != nil {
		t.Fatal(err)
	}
	history := widgetsHistory(prefix)
	script, err := l.WithClock(func() time.Time { return at }).Script(history...)
	if err != nil {
		t.Fatal(err)
	}
	for _, st := range script {
		if _, err := db.ExecContext(ctx, st.SQL); err != nil {
			t.Fatalf("%s: %v", st.SQL, err)
		}
	}
	if got, err := l.ApplyDB(ctx, db, history...); err != nil || len(got) != 0 {
		t.Fatalf("Apply after the script must apply nothing: %v, %v", got, err)
	}
	rows, err := l.AppliedDB(ctx, db)
	if err != nil || len(rows) != len(history) {
		t.Fatalf("applied: %v, %v", rows, err)
	}
	for _, r := range rows {
		if !r.AppliedAt.Equal(at) {
			t.Errorf("%s/%s recorded at %v, want the injected %v", r.Owner, r.Name, r.AppliedAt, at)
		}
	}
	// The ledger the script made is the one Apply keeps using.
	next := migrate.Migration{Owner: "acme", Name: "0003_weight", Script: ddl.Script{{SQL: "ALTER TABLE " + prefix + "_widgets ADD COLUMN weight INTEGER", Transactional: true}}}
	if got, err := l.ApplyDB(ctx, db, append(slices.Clone(history), next)...); err != nil || strings.Join(got, ",") != "acme/0003_weight" {
		t.Fatalf("Apply of a new migration after the script: %v, %v", got, err)
	}
}

// TestAdopt_SQLite runs the adoption suite on a SQLite file, lock row and all.
func TestAdopt_SQLite(t *testing.T) {
	adoptSuite(t, sqlb.SQLiteDialect{}, sqliteFile, "ad")
}

// TestScript_SQLite runs the hand-applied script on SQLite.
func TestScript_SQLite(t *testing.T) {
	scriptSuite(t, sqlb.SQLiteDialect{}, sqliteFile, "sc")
}

// dropAdoptTables removes what the suites create on a shared server.
func dropAdoptTables(db *sql.DB, prefix string) {
	_, _ = db.Exec("DROP TABLE IF EXISTS " + prefix + "_widgets, " + prefix + "_old_ledger, " + prefix + "_ledger")
}

// TestAdopt_Postgres runs the adoption suite on Postgres.
func TestAdopt_Postgres(t *testing.T) {
	open := func(t *testing.T) *sql.DB { return openEnv(t, "pgx", "SQLB_PG_DSN") }
	db := open(t)
	t.Cleanup(func() { dropAdoptTables(db, "ad") })
	dropAdoptTables(db, "ad")
	adoptSuite(t, sqlb.PostgresDialect{}, open, "ad")
}

// TestScript_Postgres runs the hand-applied script on Postgres.
func TestScript_Postgres(t *testing.T) {
	open := func(t *testing.T) *sql.DB { return openEnv(t, "pgx", "SQLB_PG_DSN") }
	db := open(t)
	t.Cleanup(func() { dropAdoptTables(db, "sc") })
	dropAdoptTables(db, "sc")
	scriptSuite(t, sqlb.PostgresDialect{}, open, "sc")
}

// TestAdopt_MySQL runs the adoption suite on MySQL.
func TestAdopt_MySQL(t *testing.T) {
	open := func(t *testing.T) *sql.DB { return openEnv(t, "mysql", "SQLB_MYSQL_DSN") }
	db := open(t)
	t.Cleanup(func() { dropAdoptTables(db, "ad") })
	dropAdoptTables(db, "ad")
	adoptSuite(t, sqlb.MySQLDialect{}, open, "ad")
}

// TestScript_MySQL runs the hand-applied script on MySQL.
func TestScript_MySQL(t *testing.T) {
	open := func(t *testing.T) *sql.DB { return openEnv(t, "mysql", "SQLB_MYSQL_DSN") }
	db := open(t)
	t.Cleanup(func() { dropAdoptTables(db, "sc") })
	dropAdoptTables(db, "sc")
	scriptSuite(t, sqlb.MySQLDialect{}, open, "sc")
}

// TestAdopt_TwoProcessesOnFreshSQLiteFiles is two processes adopting the same
// history into one fresh SQLite file at once, each with its own handle, on
// ten files: every migration must be recorded exactly once and neither
// process may fail, including on the first open that converts the file to WAL.
func TestAdopt_TwoProcessesOnFreshSQLiteFiles(t *testing.T) {
	ctx := context.Background()
	history := widgetsHistory("fr")
	d := sqlb.SQLiteDialect{}
	for round := range 10 {
		path := filepath.Join(t.TempDir(), fmt.Sprintf("adopt-%d.db", round))
		var wg sync.WaitGroup
		got := make([][]string, 2)
		errs := make([]error, 2)
		for i := range 2 {
			db := openSQLite(t, path, walPragmas)
			wg.Add(1)
			go func() {
				defer wg.Done()
				l, err := migrate.NewLedger(d, "", migrate.EngineLock(d, "acme"))
				if err != nil {
					errs[i] = err
					return
				}
				got[i], errs[i] = l.AdoptDB(ctx, db, nil, history...)
			}()
		}
		wg.Wait()
		if err := errors.Join(errs...); err != nil {
			t.Fatalf("round %d: %v", round, err)
		}
		if n := len(got[0]) + len(got[1]); n != len(history) {
			t.Fatalf("round %d: %v and %v recorded, want each of %d once", round, got[0], got[1], len(history))
		}
		if n := countRows(t, openSQLite(t, path, busyPragmas), d, migrate.DefaultLedgerTable); n != len(history) {
			t.Fatalf("round %d: %d ledger rows", round, n)
		}
	}
}

// TestAdopt_ExistingHistoryThenApply is the case that asked for Adopt, with
// the steps of its reproduction: a database whose 0001 and 0002 an earlier
// ledger ran, which Apply alone fails on with "already exists"; after Adopt
// of what that ledger lists, Apply runs only the new 0003.
func TestAdopt_ExistingHistoryThenApply(t *testing.T) {
	ctx := context.Background()
	db := sqliteFile(t)
	for _, st := range []string{
		"CREATE TABLE widgets (id TEXT PRIMARY KEY)",
		"ALTER TABLE widgets ADD COLUMN color TEXT",
		"CREATE TABLE old_ledger (owner TEXT, name TEXT, checksum TEXT, applied_at TIMESTAMP, PRIMARY KEY (owner, name))",
		"INSERT INTO old_ledger VALUES ('acme','0001','x',CURRENT_TIMESTAMP),('acme','0002','y',CURRENT_TIMESTAMP)",
	} {
		if _, err := db.ExecContext(ctx, st); err != nil {
			t.Fatal(err)
		}
	}
	d := sqlb.SQLiteDialect{}
	l, err := migrate.NewLedger(d, "acme_migrations", migrate.EngineLock(d, "acme"))
	if err != nil {
		t.Fatal(err)
	}
	ms := []migrate.Migration{
		{Owner: "acme", Name: "0001", Script: ddl.Script{{SQL: "CREATE TABLE widgets (id TEXT PRIMARY KEY)", Transactional: true}}},
		{Owner: "acme", Name: "0002", Script: ddl.Script{{SQL: "ALTER TABLE widgets ADD COLUMN color TEXT", Transactional: true}}},
		{Owner: "acme", Name: "0003", Script: ddl.Script{{SQL: "ALTER TABLE widgets ADD COLUMN size INTEGER", Transactional: true}}},
	}
	if _, err := l.ApplyDB(ctx, db, ms...); err == nil || !strings.Contains(err.Error(), "already exists") {
		t.Fatalf("Apply on an existing history: %v, want the re-run failure", err)
	}
	ran := map[string]bool{}
	rows, err := db.QueryContext(ctx, "SELECT name FROM old_ledger WHERE owner = 'acme'")
	if err != nil {
		t.Fatal(err)
	}
	for rows.Next() {
		var n string
		if err := rows.Scan(&n); err != nil {
			t.Fatal(err)
		}
		ran[n] = true
	}
	if err := errors.Join(rows.Err(), rows.Close()); err != nil {
		t.Fatal(err)
	}
	adopted, err := l.AdoptDB(ctx, db, func(m migrate.Migration) (time.Time, bool) { return time.Time{}, ran[m.Name] }, ms...)
	if err != nil || strings.Join(adopted, ",") != "acme/0001,acme/0002" {
		t.Fatalf("adopt: %v, %v", adopted, err)
	}
	applied, err := l.ApplyDB(ctx, db, ms...)
	if err != nil || strings.Join(applied, ",") != "acme/0003" {
		t.Fatalf("apply after adopting: %v, %v", applied, err)
	}
}

// queryer is what reading an old ledger needs: a *sql.DB before Adopt, or the
// ledger's own *sql.Conn inside AdoptPrepared.
type queryer interface {
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
}

// readOldLedger is the PrepareRan a library writes: it reads its old ledger
// through q, returning the database's error rather than failing the test, so
// a refused read reaches the caller as it would in a library.
func readOldLedger(ctx context.Context, q queryer, prefix string) (migrate.Ran, error) {
	rows, err := q.QueryContext(ctx, "SELECT owner, name, ran_at FROM "+prefix+"_old_ledger")
	if err != nil {
		return nil, err
	}
	ran := map[[2]string]time.Time{}
	for rows.Next() {
		var o, n, at string
		if err := rows.Scan(&o, &n, &at); err != nil {
			return nil, errors.Join(err, rows.Close())
		}
		when, err := time.Parse(time.RFC3339Nano, at)
		if err != nil {
			return nil, errors.Join(err, rows.Close())
		}
		ran[[2]string{o, n}] = when
	}
	if err := errors.Join(rows.Err(), rows.Close()); err != nil {
		return nil, err
	}
	return func(m migrate.Migration) (time.Time, bool) {
		at, ok := ran[[2]string{m.Owner, m.Name}]
		return at, ok
	}, nil
}

// errOldLedgerEdited is the error a library's own check of its old ledger
// returns, which AdoptPrepared must hand back where errors.Is finds it.
var errOldLedgerEdited = errors.New("acme: the old ledger lists a migration whose script changed")

// adoptPreparedSuite is AdoptPrepared on every engine: the old ledger is read
// through the ledger's own connection under its lock; an error from that read
// stops the adoption with nothing recorded and reaches the caller wrapped;
// after a successful adoption Apply runs only what is new.
func adoptPreparedSuite(t *testing.T, d render.Dialect, open func(t *testing.T) *sql.DB, prefix string) {
	ctx := context.Background()
	db := open(t)
	history := widgetsHistory(prefix)
	ranAt := []time.Time{
		time.Date(2025, 3, 1, 10, 0, 0, 0, time.UTC),
		time.Date(2025, 6, 1, 10, 0, 0, 0, time.UTC),
		time.Date(2025, 9, 1, 10, 0, 0, 0, time.UTC),
	}
	oldLedger(t, db, prefix, history, ranAt)
	ledgerTable := prefix + "_ledger"
	l, err := migrate.NewLedger(d, ledgerTable, migrate.EngineLock(d, prefix+"_lock"))
	if err != nil {
		t.Fatal(err)
	}
	next := migrate.Migration{Owner: "acme", Name: "0003_weight", Script: ddl.Script{{SQL: "ALTER TABLE " + prefix + "_widgets ADD COLUMN weight INTEGER", Transactional: true}}}
	all := append(slices.Clone(history), next)

	// The caller's own check fails after reading: nothing is recorded, the
	// error is the caller's, and the lock is released for the next call.
	calls := 0
	_, err = l.AdoptPreparedDB(ctx, db, func(ctx context.Context, conn *sql.Conn) (migrate.Ran, error) {
		calls++
		if _, err := readOldLedger(ctx, conn, prefix); err != nil {
			return nil, err
		}
		return nil, fmt.Errorf("acme/0002_color: %w", errOldLedgerEdited)
	}, all...)
	if !errors.Is(err, errOldLedgerEdited) || calls != 1 {
		t.Fatalf("a failing prepare: %v after %d calls, want the caller's error after one", err, calls)
	}
	if n := countRows(t, db, d, ledgerTable); n != 0 {
		t.Fatalf("a failed prepare recorded %d rows", n)
	}

	// A read the database refuses is returned the same way.
	if _, err := l.AdoptPreparedDB(ctx, db, func(ctx context.Context, conn *sql.Conn) (migrate.Ran, error) {
		return readOldLedger(ctx, conn, prefix+"_missing")
	}, all...); err == nil || !strings.Contains(err.Error(), "reading what ran") {
		t.Fatalf("a refused read: %v", err)
	}
	if n := countRows(t, db, d, ledgerTable); n != 0 {
		t.Fatalf("a refused read recorded %d rows", n)
	}

	adopted, err := l.AdoptPreparedDB(ctx, db, func(ctx context.Context, conn *sql.Conn) (migrate.Ran, error) {
		return readOldLedger(ctx, conn, prefix)
	}, all...)
	if err != nil || strings.Join(adopted, ",") != "acme/0001_widgets,acme/0002_color,acme-plugin/0001_size" {
		t.Fatalf("adopt prepared: %v, %v", adopted, err)
	}
	applied, err := l.ApplyDB(ctx, db, all...)
	if err != nil || strings.Join(applied, ",") != "acme/0003_weight" {
		t.Fatalf("apply after adopting must run only the new migration: %v, %v", applied, err)
	}
	rows, err := l.AppliedDB(ctx, db)
	if err != nil || len(rows) != 4 {
		t.Fatalf("applied: %v, %v", rows, err)
	}
	for i, m := range history {
		r := rows[i]
		if r.Owner != m.Owner || r.Name != m.Name || r.Checksum != migrate.Checksum(m.Script) || !r.AppliedAt.Equal(ranAt[i]) {
			t.Errorf("adopted row %d is %+v, want %s/%s with Apply's checksum at %v", i, r, m.Owner, m.Name, ranAt[i])
		}
	}

	// A nil prepare adopts everything, as a nil Ran does; here all is recorded.
	if again, err := l.AdoptPreparedDB(ctx, db, nil, all...); err != nil || len(again) != 0 {
		t.Fatalf("adopting twice: %v, %v", again, err)
	}
}

// TestAdoptPrepared_SQLite runs the prepared adoption on a SQLite file.
func TestAdoptPrepared_SQLite(t *testing.T) {
	adoptPreparedSuite(t, sqlb.SQLiteDialect{}, sqliteFile, "ap")
}

// TestAdoptPrepared_Postgres runs the prepared adoption on Postgres.
func TestAdoptPrepared_Postgres(t *testing.T) {
	open := func(t *testing.T) *sql.DB { return openEnv(t, "pgx", "SQLB_PG_DSN") }
	db := open(t)
	t.Cleanup(func() { dropAdoptTables(db, "ap") })
	dropAdoptTables(db, "ap")
	adoptPreparedSuite(t, sqlb.PostgresDialect{}, open, "ap")
}

// TestAdoptPrepared_MySQL runs the prepared adoption on MySQL.
func TestAdoptPrepared_MySQL(t *testing.T) {
	open := func(t *testing.T) *sql.DB { return openEnv(t, "mysql", "SQLB_MYSQL_DSN") }
	db := open(t)
	t.Cleanup(func() { dropAdoptTables(db, "ap") })
	dropAdoptTables(db, "ap")
	adoptPreparedSuite(t, sqlb.MySQLDialect{}, open, "ap")
}

// TestAdoptPrepared_TwoProcessesReadTheirOldLedgerOnAFreshWALFile is the case
// AdoptPrepared exists for. Two processes start on one new SQLite file opened
// with journal_mode(WAL) in the DSN, and each, as a library moving from a
// ledger of its own must, reads its old ledger before adopting, then applies.
// Read on a connection of the caller's own before Adopt, that read failed with
// "database is locked" in about one round in three, because the other process
// was converting the file to WAL; read through AdoptPrepared's connection,
// under the lock, it must never fail. 200 rounds, every call checked.
func TestAdoptPrepared_TwoProcessesReadTheirOldLedgerOnAFreshWALFile(t *testing.T) {
	ctx := context.Background()
	d := sqlb.SQLiteDialect{}
	history := widgetsHistory("wal")
	// What a library checks first on a database it may have migrated before:
	// is its old ledger there, and if so what it lists.
	prepare := func(ctx context.Context, conn *sql.Conn) (migrate.Ran, error) {
		var exists bool
		if err := conn.QueryRowContext(ctx, "SELECT EXISTS (SELECT 1 FROM sqlite_master WHERE type = 'table' AND name = 'wal_old_ledger')").Scan(&exists); err != nil {
			return nil, err
		}
		if !exists {
			return func(migrate.Migration) (time.Time, bool) { return time.Time{}, false }, nil
		}
		return readOldLedger(ctx, conn, "wal")
	}
	const rounds = 200
	failed := 0
	for round := range rounds {
		path := filepath.Join(t.TempDir(), fmt.Sprintf("wal-%d.db", round))
		var wg sync.WaitGroup
		errs := make([]error, 2)
		applied := make([][]string, 2)
		for i := range 2 {
			db := openSQLite(t, path, walPragmas)
			wg.Add(1)
			go func() {
				defer wg.Done()
				l, err := migrate.NewLedger(d, "acme_migrations", migrate.EngineLock(d, "acme_migrate"))
				if err != nil {
					errs[i] = err
					return
				}
				if _, err := l.AdoptPreparedDB(ctx, db, prepare, history...); err != nil {
					errs[i] = fmt.Errorf("adopt: %w", err)
					return
				}
				applied[i], errs[i] = l.ApplyDB(ctx, db, history...)
			}()
		}
		wg.Wait()
		for i, err := range errs {
			if err != nil {
				failed++
				t.Errorf("round %d, process %d: %v", round, i, err)
			}
		}
		if n := len(applied[0]) + len(applied[1]); failed == 0 && n != len(history) {
			t.Fatalf("round %d: %v and %v applied, want each of %d once", round, applied[0], applied[1], len(history))
		}
	}
	t.Logf("%d rounds, two processes each: %d failed calls", rounds, failed)
}
