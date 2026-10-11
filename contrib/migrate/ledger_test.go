package migrate_test

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	_ "github.com/go-sql-driver/mysql"
	_ "github.com/jackc/pgx/v5/stdlib"

	"github.com/ubgo/sqlb"
	"github.com/ubgo/sqlb/contrib/migrate"
	"github.com/ubgo/sqlb/ddl"
	"github.com/ubgo/sqlb/mysql"
	"github.com/ubgo/sqlb/render"
)

// migrations is a small history: a table, then a column on it.
func migrations(prefix string) []migrate.Migration {
	return []migrate.Migration{
		{Owner: "lib", Name: "001_create", Script: ddl.Script{{SQL: "CREATE TABLE " + prefix + "_t (id INTEGER)", Transactional: true}}},
		{Owner: "lib", Name: "002_add", Script: ddl.Script{{SQL: "ALTER TABLE " + prefix + "_t ADD COLUMN n INTEGER", Transactional: true}}},
	}
}

// ledgerSuite is what every engine must do: apply once, skip on the second
// run, refuse an edited migration, and let exactly one of two concurrent
// deploys do the work while the other waits and finds it done.
func ledgerSuite(t *testing.T, d render.Dialect, open func(t *testing.T) *sql.DB, prefix string) {
	ctx := context.Background()
	db := open(t)
	l, err := migrate.NewLedger(d, prefix+"_ledger", migrate.EngineLock(d, prefix+"_lock"))
	if err != nil {
		t.Fatal(err)
	}
	if rows, err := l.AppliedDB(ctx, db); err != nil || rows == nil || len(rows) != 0 {
		t.Fatalf("a database with no ledger table has applied nothing: %v, %v", rows, err)
	}
	before := time.Now().UTC()
	got, err := l.ApplyDB(ctx, db, migrations(prefix)...)
	if err != nil || strings.Join(got, ",") != "lib/001_create,lib/002_add" {
		t.Fatalf("first apply: %v, %v", got, err)
	}
	if got, err := l.ApplyDB(ctx, db, migrations(prefix)...); err != nil || len(got) != 0 {
		t.Fatalf("second apply must do nothing: %v, %v", got, err)
	}
	rows, err := l.AppliedDB(ctx, db)
	if err != nil || len(rows) != 2 {
		t.Fatalf("applied after two migrations: %v, %v", rows, err)
	}
	for i, m := range migrations(prefix) {
		r := rows[i]
		if r.Owner != m.Owner || r.Name != m.Name || r.Checksum != migrate.Checksum(m.Script) {
			t.Errorf("row %d is %+v, want %s/%s with its checksum", i, r, m.Owner, m.Name)
		}
		if r.AppliedAt.Location() != time.UTC || r.AppliedAt.Before(before) || r.AppliedAt.After(time.Now().UTC()) {
			t.Errorf("row %d applied at %v, not between %v and now in UTC", i, r.AppliedAt, before)
		}
	}
	edited := migrations(prefix)
	edited[1].Script = ddl.Script{{SQL: "ALTER TABLE " + prefix + "_t ADD COLUMN m INTEGER", Transactional: true}}
	if _, err := l.ApplyDB(ctx, db, edited...); !errors.Is(err, migrate.ErrChecksumMismatch) {
		t.Fatalf("an edited migration: %v", err)
	}

	// Two deploys at once, made deterministic: connection a holds the lock
	// while b's Apply starts, so b must wait; a then applies the migration
	// and releases the lock, and b must find it done and apply nothing.
	next := migrate.Migration{Owner: "app", Name: "003_more", Script: ddl.Script{{SQL: "ALTER TABLE " + prefix + "_t ADD COLUMN k INTEGER", Transactional: true}}}
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
		got, err := l.ApplyDB(ctx, db, append(migrations(prefix), next)...)
		done <- result{got, err}
	}()
	select {
	case r := <-done:
		t.Fatalf("a second deploy ran while the lock was held: %v, %v", r.got, r.err)
	case <-time.After(300 * time.Millisecond):
	}
	unlocked, err := migrate.NewLedger(d, prefix+"_ledger", nil)
	if err != nil {
		t.Fatal(err)
	}
	if got, err := unlocked.Apply(ctx, a, next); err != nil || strings.Join(got, ",") != "app/003_more" {
		t.Fatalf("the lock holder's apply: %v, %v", got, err)
	}
	if err := lock.Unlock(ctx, a); err != nil {
		t.Fatal(err)
	}
	select {
	case r := <-done:
		if r.err != nil || len(r.got) != 0 {
			t.Fatalf("the waiting deploy must find the work done: %v, %v", r.got, r.err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("the waiting deploy never got the lock")
	}
	var n int
	if err := db.QueryRowContext(ctx, "SELECT count(*) FROM "+d.QuoteIdent(prefix+"_ledger")).Scan(&n); err != nil || n != 3 {
		t.Fatalf("ledger rows %d, %v", n, err)
	}

	// The ledger now holds lib's 001 and 002 and app's 003. Code that knows
	// only lib's 001 is older than the database: plain Apply ignores the row
	// it does not know, and RefuseUnknown refuses it by name, applying
	// nothing. app's row is never judged by code that gave no app migration.
	older := migrations(prefix)[:1]
	if got, err := l.ApplyDB(ctx, db, older...); err != nil || len(got) != 0 {
		t.Fatalf("plain Apply must still ignore a row it was not given: %v, %v", got, err)
	}
	strict := l.RefuseUnknown()
	_, err = strict.ApplyDB(ctx, db, older...)
	if !errors.Is(err, migrate.ErrUnknownMigration) || !strings.Contains(err.Error(), "lib/002_add") || strings.Contains(err.Error(), "app/003_more") {
		t.Fatalf("RefuseUnknown must name lib/002_add and only it: %v", err)
	}
	if got, err := strict.ApplyDB(ctx, db, migrations(prefix)...); err != nil || len(got) != 0 {
		t.Fatalf("RefuseUnknown must not judge an owner it was not given: %v, %v", got, err)
	}
	if rows, err := l.AppliedDB(ctx, db); err != nil || len(rows) != 3 || rows[2].Owner+"/"+rows[2].Name != "app/003_more" {
		t.Fatalf("applied after the concurrent deploy: %v, %v", rows, err)
	}
}

func sqliteFile(t *testing.T) *sql.DB {
	db := fileDB(t)
	db.SetMaxOpenConns(4)
	return db
}

// TestLedger_SQLite runs the suite on a SQLite file with a pool, where the
// lock is a lock row.
func TestLedger_SQLite(t *testing.T) {
	ledgerSuite(t, sqlb.SQLiteDialect{}, sqliteFile, "lg")
}

func openEnv(t *testing.T, driver, env string) *sql.DB {
	dsn := os.Getenv(env)
	if dsn == "" {
		t.Skipf("SKIPPED, NOT PASSED: set %s to run the %s ledger", env, driver)
	}
	db, err := sql.Open(driver, dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

// TestLedger_Postgres runs the suite on Postgres, with its advisory lock.
func TestLedger_Postgres(t *testing.T) {
	open := func(t *testing.T) *sql.DB { return openEnv(t, "pgx", "SQLB_PG_DSN") }
	db := open(t)
	t.Cleanup(func() { _, _ = db.Exec("DROP TABLE IF EXISTS lg_t, lg_ledger") })
	_, _ = db.Exec("DROP TABLE IF EXISTS lg_t, lg_ledger")
	ledgerSuite(t, sqlb.PostgresDialect{}, open, "lg")
}

// TestLedger_MySQL runs the suite on MySQL, with its named lock.
func TestLedger_MySQL(t *testing.T) {
	open := func(t *testing.T) *sql.DB { return openEnv(t, "mysql", "SQLB_MYSQL_DSN") }
	db := open(t)
	t.Cleanup(func() { _, _ = db.Exec("DROP TABLE IF EXISTS lg_t, lg_ledger") })
	_, _ = db.Exec("DROP TABLE IF EXISTS lg_t, lg_ledger")
	ledgerSuite(t, sqlb.MySQLDialect{}, open, "lg")
}

// TestLedger_AFailedMigrationLeavesNoRecord pins the transactional half: on
// SQLite a migration that fails leaves neither its change nor its ledger row,
// so the next Apply runs it again from the start.
func TestLedger_AFailedMigrationLeavesNoRecord(t *testing.T) {
	ctx := context.Background()
	db := sqliteFile(t)
	l, err := migrate.NewLedger(sqlb.SQLiteDialect{}, "", nil)
	if err != nil {
		t.Fatal(err)
	}
	bad := migrate.Migration{Owner: "lib", Name: "001", Script: ddl.Script{
		{SQL: "CREATE TABLE fa (id INTEGER)", Transactional: true},
		{SQL: "INSERT INTO nowhere VALUES (1)", Transactional: true},
	}}
	if _, err := l.ApplyDB(ctx, db, bad); err == nil {
		t.Fatal("a failing migration applied")
	}
	var n int
	if err := db.QueryRowContext(ctx, "SELECT count(*) FROM "+migrate.DefaultLedgerTable).Scan(&n); err != nil || n != 0 {
		t.Fatalf("ledger rows after a failure %d, %v", n, err)
	}
	if _, err := db.ExecContext(ctx, "SELECT 1 FROM fa"); err == nil {
		t.Error("the failed migration's table survived")
	}
}

// TestLedger_Refusals pins the inputs Apply refuses before touching the
// database, and the dialects NewLedger refuses.
func TestLedger_Refusals(t *testing.T) {
	ctx := context.Background()
	l, err := migrate.NewLedger(sqlb.SQLiteDialect{}, "", nil)
	if err != nil {
		t.Fatal(err)
	}
	db := sqliteFile(t)
	s := ddl.Script{{SQL: "SELECT 1", Transactional: true}}
	for name, ms := range map[string][]migrate.Migration{
		"no owner":     {{Name: "a", Script: s}},
		"no name":      {{Owner: "o", Script: s}},
		"no script":    {{Owner: "o", Name: "a"}},
		"a name twice": {{Owner: "o", Name: "a", Script: s}, {Owner: "o", Name: "a", Script: s}},
	} {
		if _, err := l.ApplyDB(ctx, db, ms...); !errors.Is(err, migrate.ErrBadMigration) {
			t.Errorf("%s: %v", name, err)
		}
	}
	if _, err := migrate.NewLedger(sqlb.ClickHouseDialect{}, "", nil); !errors.Is(err, migrate.ErrNoLedgerDialect) {
		t.Errorf("clickhouse: %v", err)
	}
	if _, err := (migrate.Ledger{}).ApplyDB(ctx, db, migrate.Migration{Owner: "o", Name: "a", Script: s}); !errors.Is(err, migrate.ErrNoLedgerDialect) {
		t.Errorf("a zero ledger: %v", err)
	}
	if migrate.Checksum(s) == migrate.Checksum(ddl.Script{{SQL: "SELECT 1"}}) {
		t.Error("the checksum ignores the Transactional flag")
	}
}

// TestTableLock_WaitsTimesOutAndTakesOverAStaleRow pins the lock row: a
// second holder waits, gives up with ErrLocked at its deadline, and takes
// over a row older than StaleAfter, a crashed holder's.
func TestTableLock_WaitsTimesOutAndTakesOverAStaleRow(t *testing.T) {
	ctx := context.Background()
	db := sqliteFile(t)
	a, err := db.Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = a.Close() }()
	b, err := db.Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = b.Close() }()
	first := migrate.TableLock{Dialect: sqlb.SQLiteDialect{}, Table: "tl", StaleAfter: time.Hour, Holder: "a"}
	second := migrate.TableLock{Dialect: sqlb.SQLiteDialect{}, Table: "tl", StaleAfter: time.Hour, Holder: "b"}
	if err := first.Lock(ctx, a); err != nil {
		t.Fatal(err)
	}
	short, cancel := context.WithTimeout(ctx, 200*time.Millisecond)
	defer cancel()
	if err := second.Lock(short, b); !errors.Is(err, migrate.ErrLocked) {
		t.Fatalf("a held lock: %v", err)
	}
	// The first holder crashes without unlocking: age its row past StaleAfter.
	if _, err := a.ExecContext(ctx, `UPDATE tl SET taken_at = taken_at - ?`, (2 * time.Hour).Nanoseconds()); err != nil {
		t.Fatal(err)
	}
	if err := second.Lock(ctx, b); err != nil {
		t.Fatalf("a stale row was not taken over: %v", err)
	}
	if err := second.Unlock(ctx, b); err != nil {
		t.Fatal(err)
	}
	if err := first.Lock(ctx, a); err != nil {
		t.Fatalf("an unlocked lock: %v", err)
	}
}

// TestExecute_HonoursSkipIf pins the runner contract: a statement whose
// SkipIf query returns a row is skipped, one whose query returns none runs,
// and a SkipIf that fails fails the run.
func TestExecute_HonoursSkipIf(t *testing.T) {
	ctx := context.Background()
	db := sqliteFile(t)
	err := migrate.ExecuteDB(ctx, db, ddl.Script{
		{SQL: "CREATE TABLE skipped (id INTEGER)", Transactional: true, SkipIf: "SELECT 1"},
		{SQL: "CREATE TABLE ran (id INTEGER)", Transactional: true, SkipIf: "SELECT 1 WHERE 0"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, "SELECT 1 FROM skipped"); err == nil {
		t.Error("a statement whose guard found a row ran")
	}
	if _, err := db.ExecContext(ctx, "SELECT 1 FROM ran"); err != nil {
		t.Errorf("a statement whose guard found nothing did not run: %v", err)
	}
	if err := migrate.ExecuteDB(ctx, db, ddl.Script{{SQL: "SELECT 1", Transactional: true, SkipIf: "SELECT * FROM nowhere"}}); err == nil {
		t.Error("a failing guard passed")
	}
	if migrate.Checksum(ddl.Script{{SQL: "x", SkipIf: "a"}}) == migrate.Checksum(ddl.Script{{SQL: "x", SkipIf: "b"}}) {
		t.Error("the checksum ignores SkipIf")
	}
}

// TestMySQL_IndexGuardsExecute runs the MySQL index guards on a real server
// through Execute: the index is created once and survives a second run, and
// dropped once with the second drop a no-op, where plain MySQL would fail
// both repeats with a duplicate or a missing key.
func TestMySQL_IndexGuardsExecute(t *testing.T) {
	ctx := context.Background()
	db := openEnv(t, "mysql", "SQLB_MYSQL_DSN")
	t.Cleanup(func() { _, _ = db.Exec("DROP TABLE IF EXISTS ig_orders") })
	_, _ = db.Exec("DROP TABLE IF EXISTS ig_orders")
	if _, err := db.ExecContext(ctx, "CREATE TABLE ig_orders (id BIGINT, year BIGINT)"); err != nil {
		t.Fatal(err)
	}
	tb := sqlb.Table{Name: "ig_orders"}
	year := sqlb.Col[int64](tb, "year")
	ix := mysql.Index{Name: "ig_orders_year"}
	create, err := mysql.CreateIndex(ix, tb).On(mysql.IndexKey(year)).IfNotExists().Build()
	if err != nil {
		t.Fatal(err)
	}
	drop, err := mysql.DropIndex(ix, tb).IfExists().Build()
	if err != nil {
		t.Fatal(err)
	}
	count := func() int {
		var n int
		if err := db.QueryRowContext(ctx, `SELECT count(*) FROM information_schema.statistics WHERE table_schema = DATABASE() AND table_name = 'ig_orders' AND index_name = 'ig_orders_year'`).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}
	for range 2 {
		if err := migrate.ExecuteDB(ctx, db, create); err != nil {
			t.Fatalf("create: %v", err)
		}
	}
	if count() != 1 {
		t.Fatalf("index rows %d after two guarded creates", count())
	}
	for range 2 {
		if err := migrate.ExecuteDB(ctx, db, drop); err != nil {
			t.Fatalf("drop: %v", err)
		}
	}
	if count() != 0 {
		t.Fatalf("index rows %d after two guarded drops", count())
	}
	if _, err := db.ExecContext(ctx, create[0].SQL); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, create[0].SQL); err == nil {
		t.Error("MySQL accepted a second unguarded CREATE INDEX, so the guard tests nothing")
	}
}

// TestApplied_OrdersByTimeThenOwnerThenName pins the order Applied promises,
// on rows written by hand so the instants are chosen: applied_at is RFC 3339
// text, which drops trailing zeros of the fraction, so as text a whole second
// sorts after a fraction of the same second, and an ORDER BY on the column
// would put 05Z after 05.5Z. Rows at one instant come by owner, then name.
func TestApplied_OrdersByTimeThenOwnerThenName(t *testing.T) {
	ctx := context.Background()
	db := sqliteFile(t)
	l, err := migrate.NewLedger(sqlb.SQLiteDialect{}, "", nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := l.ApplyDB(ctx, db); err != nil {
		t.Fatal(err)
	}
	for _, r := range [][3]string{
		{"b", "x", "2026-01-01T00:00:06Z"},
		{"a", "late", "2026-01-01T00:00:05.5Z"},
		{"b", "y", "2026-01-01T00:00:05Z"},
		{"a", "z", "2026-01-01T00:00:05Z"},
		{"a", "m", "2026-01-01T00:00:05Z"},
	} {
		if _, err := db.ExecContext(ctx, "INSERT INTO "+migrate.DefaultLedgerTable+" (owner, name, checksum, applied_at) VALUES (?, ?, 'c', ?)", r[0], r[1], r[2]); err != nil {
			t.Fatal(err)
		}
	}
	rows, err := l.AppliedDB(ctx, db)
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, r := range rows {
		got = append(got, r.Owner+"/"+r.Name)
	}
	if want := "a/m,a/z,b/y,a/late,b/x"; strings.Join(got, ",") != want {
		t.Fatalf("order %s, want %s", strings.Join(got, ","), want)
	}
	if !rows[3].AppliedAt.Equal(time.Date(2026, 1, 1, 0, 0, 5, 5e8, time.UTC)) {
		t.Errorf("applied_at read back as %v", rows[3].AppliedAt)
	}
}

// TestApplied_Refusals pins what Applied reports rather than hides: a zero
// Ledger, an applied_at the ledger could not have written, and a failure
// that is not a missing table, which must not read as "none applied".
func TestApplied_Refusals(t *testing.T) {
	ctx := context.Background()
	db := sqliteFile(t)
	if _, err := (migrate.Ledger{}).AppliedDB(ctx, db); !errors.Is(err, migrate.ErrNoLedgerDialect) {
		t.Errorf("a zero ledger: %v", err)
	}
	l, err := migrate.NewLedger(sqlb.SQLiteDialect{}, "", nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := l.ApplyDB(ctx, db); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, "INSERT INTO "+migrate.DefaultLedgerTable+" (owner, name, checksum, applied_at) VALUES ('o', 'n', 'c', 'yesterday')"); err != nil {
		t.Fatal(err)
	}
	if _, err := l.AppliedDB(ctx, db); err == nil || !strings.Contains(err.Error(), "o/n") {
		t.Errorf("an unreadable applied_at must fail naming its row: %v", err)
	}
	conn, err := db.Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	_ = conn.Close()
	if rows, err := l.Applied(ctx, conn); err == nil {
		t.Errorf("a closed connection read as %v applied, not as an error", rows)
	}
}
