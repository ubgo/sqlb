package driversqlite_test

import (
	"context"
	"database/sql"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ubgo/sqlb"
	driversqlite "github.com/ubgo/sqlb/contrib/driver-sqlite"
	"github.com/ubgo/sqlb/sqlbtest"
)

// setupDDL is sqlbtest.Schema in SQLite's spelling. sqlb generates no DDL, so
// every integration writes this once, and the conformance suite binds to the
// names it declares.
var setupDDL = []string{
	`CREATE TABLE IF NOT EXISTS sqlb_conformance_user (
		id     INTEGER PRIMARY KEY,
		email  TEXT    NOT NULL,
		name   TEXT,
		age    INTEGER NOT NULL,
		active BOOLEAN NOT NULL
	)`,
	`CREATE TABLE IF NOT EXISTS sqlb_conformance_post (
		id      INTEGER PRIMARY KEY,
		user_id INTEGER NOT NULL REFERENCES sqlb_conformance_user(id),
		title   TEXT    NOT NULL,
		views   INTEGER NOT NULL
	)`,
}

var teardownDDL = []string{
	`DROP TABLE IF EXISTS sqlb_conformance_post`,
	`DROP TABLE IF EXISTS sqlb_conformance_user`,
}

// TestConformance is the point of this module: the builder, executed.
func TestConformance(t *testing.T) {
	db, err := driversqlite.Memory()
	if err != nil {
		t.Fatalf("Memory: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	cfg := sqlbtest.Config{
		DB:       db,
		Dialect:  sqlb.SQLiteDialect{},
		Setup:    setupDDL,
		Teardown: teardownDDL,
	}
	sqlbtest.Run(t, cfg)
	sqlbtest.RunReturning(t, cfg, sqlb.SQLiteDialect{})
}

// A file-backed database takes the same suite, because WAL and the pool are
// only exercised off :memory:.
func TestConformance_FileBacked(t *testing.T) {
	path := filepath.Join(t.TempDir(), "conformance.db")
	db, err := driversqlite.Open(path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	cfg := sqlbtest.Config{
		DB:       db,
		Dialect:  sqlb.SQLiteDialect{},
		Setup:    setupDDL,
		Teardown: teardownDDL,
	}
	sqlbtest.Run(t, cfg)
	sqlbtest.RunReturning(t, cfg, sqlb.SQLiteDialect{})
}

// The pragmas must hold on every connection in the pool, which is the whole
// reason they go in the DSN rather than through an Exec.
func TestPragmas_AppliedToEveryConnection(t *testing.T) {
	path := filepath.Join(t.TempDir(), "pragmas.db")
	db, err := driversqlite.Open(path, driversqlite.WithMaxOpenConns(4))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer func() { _ = db.Close() }()

	// Hold several connections open at once, so the checks below cannot all
	// land on the same one.
	ctx := context.Background()
	var conns []*sql.Conn
	for i := 0; i < 4; i++ {
		c, err := db.Conn(ctx)
		if err != nil {
			t.Fatalf("Conn: %v", err)
		}
		conns = append(conns, c)
	}
	for i, c := range conns {
		var journal string
		if err := c.QueryRowContext(ctx, "PRAGMA journal_mode").Scan(&journal); err != nil {
			t.Fatalf("conn %d journal_mode: %v", i, err)
		}
		if !strings.EqualFold(journal, "wal") {
			t.Errorf("conn %d: journal_mode = %q, want wal", i, journal)
		}
		var fk int
		if err := c.QueryRowContext(ctx, "PRAGMA foreign_keys").Scan(&fk); err != nil {
			t.Fatalf("conn %d foreign_keys: %v", i, err)
		}
		if fk != 1 {
			t.Errorf("conn %d: foreign_keys = %d, want 1", i, fk)
		}
		_ = c.Close()
	}
}

// foreign_keys=ON is a claim; this is the test that makes it one worth
// believing. Without the pragma SQLite accepts the orphan silently.
func TestForeignKeys_AreEnforced(t *testing.T) {
	db, err := driversqlite.Memory()
	if err != nil {
		t.Fatalf("Memory: %v", err)
	}
	defer func() { _ = db.Close() }()
	ctx := context.Background()
	for _, stmt := range setupDDL {
		if _, err := db.ExecContext(ctx, stmt); err != nil {
			t.Fatalf("setup: %v", err)
		}
	}
	q, err := sqlb.NewInsert(sqlb.SQLiteDialect{}).Into(sqlbtest.Posts).Row(
		sqlb.Set(sqlbtest.PostID, int64(1)),
		sqlb.Set(sqlbtest.PostUserID, int64(404)), // no such user
		sqlb.Set(sqlbtest.PostTitle, "orphan"),
		sqlb.Set(sqlbtest.PostViews, int64(0)),
	).Build()
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	if _, err := db.ExecContext(ctx, q.SQL, q.Args...); err == nil {
		t.Fatal("insert with a dangling foreign key was accepted; foreign_keys is off")
	}
}

// WithoutDefaultPragmas must really drop them, or the escape hatch is a lie.
func TestWithoutDefaultPragmas(t *testing.T) {
	path := filepath.Join(t.TempDir(), "plain.db")
	db, err := driversqlite.Open(path, driversqlite.WithoutDefaultPragmas())
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer func() { _ = db.Close() }()
	var fk int
	if err := db.QueryRow("PRAGMA foreign_keys").Scan(&fk); err != nil {
		t.Fatalf("foreign_keys: %v", err)
	}
	if fk != 0 {
		t.Errorf("foreign_keys = %d, want 0 with the defaults dropped", fk)
	}
}

// A later WithPragma overrides a default of the same name and leaves the rest.
func TestWithPragma_Overrides(t *testing.T) {
	path := filepath.Join(t.TempDir(), "override.db")
	db, err := driversqlite.Open(path, driversqlite.WithPragma("journal_mode", "DELETE"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer func() { _ = db.Close() }()
	var journal string
	if err := db.QueryRow("PRAGMA journal_mode").Scan(&journal); err != nil {
		t.Fatalf("journal_mode: %v", err)
	}
	if !strings.EqualFold(journal, "delete") {
		t.Errorf("journal_mode = %q, want delete", journal)
	}
	var fk int
	if err := db.QueryRow("PRAGMA foreign_keys").Scan(&fk); err != nil {
		t.Fatalf("foreign_keys: %v", err)
	}
	if fk != 1 {
		t.Errorf("overriding one default dropped another: foreign_keys = %d", fk)
	}
}

// A pragma given twice must appear once in the DSN, or the driver decides
// which one wins and the override above is luck rather than behaviour.
func TestBuildDSN_EmitsEachPragmaOnce(t *testing.T) {
	path := filepath.Join(t.TempDir(), "once.db")
	db, err := driversqlite.Open(path,
		driversqlite.WithPragma("busy_timeout", "1000"),
		driversqlite.WithPragma("busy_timeout", "2000"),
	)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer func() { _ = db.Close() }()
	var timeout int
	if err := db.QueryRow("PRAGMA busy_timeout").Scan(&timeout); err != nil {
		t.Fatalf("busy_timeout: %v", err)
	}
	if timeout != 2000 {
		t.Errorf("busy_timeout = %d, want 2000 (the last value given)", timeout)
	}
}

// A DSN that already carries a query string must have the pragmas appended
// with & rather than a second ?, or the driver sees one malformed parameter
// and silently applies none of them.
func TestOpen_DSNWithExistingQuery(t *testing.T) {
	path := filepath.Join(t.TempDir(), "query.db")
	db, err := driversqlite.Open("file:"+path+"?mode=rwc", driversqlite.WithPragma("busy_timeout", "3000"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer func() { _ = db.Close() }()

	var timeout int
	if err := db.QueryRow("PRAGMA busy_timeout").Scan(&timeout); err != nil {
		t.Fatalf("busy_timeout: %v", err)
	}
	if timeout != 3000 {
		t.Errorf("busy_timeout = %d, want 3000; the pragmas were lost after the existing query string", timeout)
	}
	var fk int
	if err := db.QueryRow("PRAGMA foreign_keys").Scan(&fk); err != nil {
		t.Fatalf("foreign_keys: %v", err)
	}
	if fk != 1 {
		t.Errorf("foreign_keys = %d, want 1; the defaults were lost too", fk)
	}
}
