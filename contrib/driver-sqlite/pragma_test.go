package driversqlite_test

import (
	"context"
	"database/sql"
	"path/filepath"
	"strings"
	"testing"
	"time"

	driversqlite "github.com/ubgo/sqlb/contrib/driver-sqlite"
	"github.com/ubgo/sqlb/ddl"
	"github.com/ubgo/sqlb/sqlite"
)

// The PRAGMA statements executed against a real database, and read back.
//
// A pragma is the one kind of statement whose failure mode is silence: SQLite
// does not refuse a name it does not know or a value it will not use, it
// ignores them, so a rendering test proves only that the text is what the
// author typed. Every case here therefore sets the pragma through the builder
// and then asks SQLite what the setting is, because that is the only thing
// that can tell a working constructor from one that renders a statement the
// engine quietly drops.

// pragmaDB opens a file-backed database with none of the driver's default
// pragmas, so nothing here is reading back a value Open set.
//
// File-backed, not in memory: journal_mode, page_size, auto_vacuum and
// mmap_size are all properties of a database file, and an in-memory database
// reports its own fixed answers for them. Measured, PRAGMA journal_mode = WAL
// on an in-memory database succeeds and reads back "memory", which is exactly
// the false pass this test exists to avoid.
func pragmaDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := driversqlite.Open(filepath.Join(t.TempDir(), "pragma.db"),
		driversqlite.WithoutDefaultPragmas(), driversqlite.WithMaxOpenConns(1))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

// readPragma asks SQLite for a setting's current value as text.
func readPragma(t *testing.T, db *sql.DB, name string) string {
	t.Helper()
	var v string
	if err := db.QueryRowContext(context.Background(), "PRAGMA "+name).Scan(&v); err != nil {
		t.Fatalf("reading %s back: %v", name, err)
	}
	return v
}

// TestPragma_SetsWhatItSaysOnSQLite runs every pragma that changes a setting
// and reads the setting back.
//
// page_size and auto_vacuum run first and on their own, because both are
// stored in the database file and can only be changed for free while it has no
// pages; afterwards they need a VACUUM, which is why their statements are
// marked OutsideTransaction.
func TestPragma_SetsWhatItSaysOnSQLite(t *testing.T) {
	ctx := context.Background()
	db := pragmaDB(t)

	for _, b := range []interface{ Build() (ddl.Script, error) }{
		sqlite.SetPageSize(8192),
		sqlite.SetAutoVacuum(sqlite.AutoVacuumIncremental),
	} {
		s, err := b.Build()
		if err != nil {
			t.Fatal(err)
		}
		if err := runScript(ctx, db, s); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := db.ExecContext(ctx, `CREATE TABLE pragma_probe(i INTEGER)`); err != nil {
		t.Fatal(err)
	}
	if got := readPragma(t, db, "page_size"); got != "8192" {
		t.Errorf("page_size read back %s, want 8192", got)
	}
	if got := readPragma(t, db, "auto_vacuum"); got != "2" {
		t.Errorf("auto_vacuum read back %s, want 2 (INCREMENTAL)", got)
	}

	cases := []struct {
		name  string
		built func() (ddl.Script, error)
		// read is the pragma to ask for afterwards, and want its value. SQLite
		// answers most of them as an integer even where the statement wrote a
		// keyword, which is itself worth pinning: NORMAL comes back as 1.
		read, want string
	}{
		{"journal_mode wal", sqlite.SetJournalMode(sqlite.JournalWAL).Build, "journal_mode", "wal"},
		{"journal_mode delete", sqlite.SetJournalMode(sqlite.JournalDelete).Build, "journal_mode", "delete"},
		{"journal_mode truncate", sqlite.SetJournalMode(sqlite.JournalTruncate).Build, "journal_mode", "truncate"},
		{"journal_mode persist", sqlite.SetJournalMode(sqlite.JournalPersist).Build, "journal_mode", "persist"},
		{"journal_mode memory", sqlite.SetJournalMode(sqlite.JournalMemory).Build, "journal_mode", "memory"},
		{"journal_mode off", sqlite.SetJournalMode(sqlite.JournalOff).Build, "journal_mode", "off"},
		{"synchronous off", sqlite.SetSynchronous(sqlite.SynchronousOff).Build, "synchronous", "0"},
		{"synchronous normal", sqlite.SetSynchronous(sqlite.SynchronousNormal).Build, "synchronous", "1"},
		{"synchronous full", sqlite.SetSynchronous(sqlite.SynchronousFull).Build, "synchronous", "2"},
		{"synchronous extra", sqlite.SetSynchronous(sqlite.SynchronousExtra).Build, "synchronous", "3"},
		{"busy_timeout", sqlite.SetBusyTimeout(3 * time.Second).Build, "busy_timeout", "3000"},
		{"busy_timeout none", sqlite.SetBusyTimeout(0).Build, "busy_timeout", "0"},
		{"cache_size pages", sqlite.SetCacheSizePages(1000).Build, "cache_size", "1000"},
		{"cache_size kibibytes", sqlite.SetCacheSizeKibibytes(4000).Build, "cache_size", "-4000"},
		{"temp_store memory", sqlite.SetTempStore(sqlite.TempStoreMemory).Build, "temp_store", "2"},
		{"temp_store file", sqlite.SetTempStore(sqlite.TempStoreFile).Build, "temp_store", "1"},
		{"temp_store default", sqlite.SetTempStore(sqlite.TempStoreDefault).Build, "temp_store", "0"},
		{"mmap_size", sqlite.SetMmapSize(268435456).Build, "mmap_size", "268435456"},
		{"mmap_size off", sqlite.SetMmapSize(0).Build, "mmap_size", "0"},
		{"secure_delete on", sqlite.SetSecureDelete(sqlite.SecureDeleteOn).Build, "secure_delete", "1"},
		{"secure_delete fast", sqlite.SetSecureDelete(sqlite.SecureDeleteFast).Build, "secure_delete", "2"},
		{"secure_delete off", sqlite.SetSecureDelete(sqlite.SecureDeleteOff).Build, "secure_delete", "0"},
		{"recursive_triggers on", sqlite.RecursiveTriggers(true).Build, "recursive_triggers", "1"},
		{"recursive_triggers off", sqlite.RecursiveTriggers(false).Build, "recursive_triggers", "0"},
		{"legacy_alter_table on", sqlite.LegacyAlterTable(true).Build, "legacy_alter_table", "1"},
		{"legacy_alter_table off", sqlite.LegacyAlterTable(false).Build, "legacy_alter_table", "0"},
		{"foreign_keys on", sqlite.ForeignKeys(true).Build, "foreign_keys", "1"},
		{"foreign_keys off", sqlite.ForeignKeys(false).Build, "foreign_keys", "0"},
		{"user_version", sqlite.SetUserVersion(41).Build, "user_version", "41"},
		{"application_id", sqlite.SetApplicationID(1397836866).Build, "application_id", "1397836866"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s, err := tc.built()
			if err != nil {
				t.Fatal(err)
			}
			if err := runScript(ctx, db, s); err != nil {
				t.Fatalf("%v: %v", s, err)
			}
			if got := readPragma(t, db, tc.read); got != tc.want {
				t.Errorf("%s read back %q, want %q", tc.read, got, tc.want)
			}
		})
	}
}

// TestPragma_DeferForeignKeysLastsOnlyForTheTransaction is why
// DeferForeignKeys is the one pragma here that is not marked
// OutsideTransaction: SQLite clears it at the end of every transaction, so
// setting it before BEGIN would change nothing that reached the statements it
// was meant for.
func TestPragma_DeferForeignKeysLastsOnlyForTheTransaction(t *testing.T) {
	ctx := context.Background()
	db := pragmaDB(t)
	s, err := sqlite.DeferForeignKeys(true).Build()
	if err != nil {
		t.Fatal(err)
	}
	if s[0].OutsideTransaction {
		t.Fatal("defer_foreign_keys must run inside the transaction it applies to")
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := tx.ExecContext(ctx, s[0].SQL); err != nil {
		t.Fatal(err)
	}
	var inside int
	if err := tx.QueryRowContext(ctx, `PRAGMA defer_foreign_keys`).Scan(&inside); err != nil {
		t.Fatal(err)
	}
	if inside != 1 {
		t.Errorf("inside the transaction defer_foreign_keys read %d, want 1", inside)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	if got := readPragma(t, db, "defer_foreign_keys"); got != "0" {
		t.Errorf("after the commit defer_foreign_keys read %s; the measured reset changed", got)
	}
}

// TestPragma_JournalAndSynchronousCannotChangeInsideATransaction is the
// measurement the OutsideTransaction flag on SetJournalMode and SetSynchronous
// rests on, in both of the shapes SQLite has.
//
// synchronous and a journal_mode change in a transaction that has not written
// are refused outright. A journal_mode change in a transaction that HAS
// written is the interesting one: it returns no error and leaves the mode
// alone, so without the flag a runner reports a migration that set the journal
// mode and a database that never did.
func TestPragma_JournalAndSynchronousCannotChangeInsideATransaction(t *testing.T) {
	ctx := context.Background()
	db := pragmaDB(t)
	wal, err := sqlite.SetJournalMode(sqlite.JournalWAL).Build()
	if err != nil {
		t.Fatal(err)
	}
	if !wal[0].OutsideTransaction {
		t.Fatal("journal_mode must be marked OutsideTransaction")
	}
	if err := runScript(ctx, db, wal); err != nil {
		t.Fatal(err)
	}

	// A transaction that has not written: refused.
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	_, err = tx.ExecContext(ctx, `PRAGMA journal_mode = DELETE`)
	_ = tx.Rollback()
	if err == nil {
		t.Error("changing journal_mode in a transaction that had not written succeeded; the measurement changed")
	} else if !strings.Contains(err.Error(), "within a transaction") {
		t.Errorf("the refusal changed: %v", err)
	}

	// A transaction that has written: accepted, and silently does nothing.
	tx, err = db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := tx.ExecContext(ctx, `CREATE TABLE journal_probe(i INTEGER)`); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.ExecContext(ctx, `PRAGMA journal_mode = DELETE`); err != nil {
		t.Errorf("in a transaction that had written, the change was refused: %v", err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	if got := readPragma(t, db, "journal_mode"); got != "wal" {
		t.Errorf("journal_mode is %q after a change inside a transaction; the silent no-op this flag guards is gone", got)
	}

	sync, err := sqlite.SetSynchronous(sqlite.SynchronousFull).Build()
	if err != nil {
		t.Fatal(err)
	}
	if !sync[0].OutsideTransaction {
		t.Fatal("synchronous must be marked OutsideTransaction")
	}
	tx, err = db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	_, err = tx.ExecContext(ctx, sync[0].SQL)
	_ = tx.Rollback()
	if err == nil {
		t.Error("changing synchronous inside a transaction succeeded; the flag is no longer justified")
	} else if !strings.Contains(err.Error(), "may not be changed inside a transaction") {
		t.Errorf("the refusal changed: %v", err)
	}
}

// TestPragma_ChecksFailOnlyWhenSomethingIsWrong is the half of a check that a
// rendering test cannot reach. All three report by returning rows, so the
// thing to prove is that a runner honouring MustReturnNoRows passes on a sound
// database and fails on a broken one — and the broken half matters most,
// because a check that can never fail looks exactly like one that works.
func TestPragma_ChecksFailOnlyWhenSomethingIsWrong(t *testing.T) {
	ctx := context.Background()
	db := pragmaDB(t)
	if _, err := db.ExecContext(ctx, `CREATE TABLE ck_parent(id INTEGER PRIMARY KEY)`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `CREATE TABLE ck_child(id INTEGER PRIMARY KEY, parent INTEGER REFERENCES ck_parent(id))`); err != nil {
		t.Fatal(err)
	}
	for _, b := range []struct {
		name  string
		built func() (ddl.Script, error)
	}{
		{"integrity_check", sqlite.IntegrityCheck().Build},
		{"quick_check", sqlite.QuickCheck().Build},
		{"foreign_key_check", sqlite.ForeignKeyCheck().Build},
	} {
		s, err := b.built()
		if err != nil {
			t.Fatal(err)
		}
		if err := runScript(ctx, db, s); err != nil {
			t.Errorf("%s failed on a sound database: %v", b.name, err)
		}
	}
	// With enforcement off, a row may name a parent that is not there. That is
	// the state a table rebuild can leave behind, and the one foreign_key_check
	// exists to find.
	if _, err := db.ExecContext(ctx, `PRAGMA foreign_keys = OFF`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO ck_child VALUES (1, 404)`); err != nil {
		t.Fatal(err)
	}
	s, err := sqlite.ForeignKeyCheck().Build()
	if err != nil {
		t.Fatal(err)
	}
	if err := runScript(ctx, db, s); err == nil {
		t.Fatal("foreign_key_check passed with a dangling reference in the database")
	}
	// The integrity checks are unaffected by a dangling reference: it is a
	// consistency problem, not a corrupt page, which is why all three exist.
	for _, b := range []struct {
		name  string
		built func() (ddl.Script, error)
	}{
		{"integrity_check", sqlite.IntegrityCheck().Build},
		{"quick_check", sqlite.QuickCheck().Build},
	} {
		s, err := b.built()
		if err != nil {
			t.Fatal(err)
		}
		if err := runScript(ctx, db, s); err != nil {
			t.Errorf("%s failed on an intact file: %v", b.name, err)
		}
	}
}

// TestPragma_IntegrityChecksReturnOkWhenSound pins the measurement behind
// wrapping integrity_check and quick_check in a SELECT: the bare pragma
// returns the single row 'ok' on a healthy database, so MustReturnNoRows over
// it would fail every healthy database.
func TestPragma_IntegrityChecksReturnOkWhenSound(t *testing.T) {
	ctx := context.Background()
	db := pragmaDB(t)
	for _, name := range []string{"integrity_check", "quick_check"} {
		var v string
		if err := db.QueryRowContext(ctx, "PRAGMA "+name).Scan(&v); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if v != "ok" {
			t.Errorf("%s returned %q on a sound database, want \"ok\"", name, v)
		}
	}
}
