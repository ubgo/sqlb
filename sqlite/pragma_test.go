package sqlite_test

import (
	"errors"
	"testing"
	"time"

	"github.com/ubgo/sqlb/ddl"
	"github.com/ubgo/sqlb/sqlite"
)

// oneStatement is the single Statement a pragma builds, with its flags, so a
// test can assert the text and the runner contract together.
func oneStatement(t *testing.T, built func() (ddl.Script, error)) ddl.Statement {
	t.Helper()
	s, err := built()
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	if len(s) != 1 {
		t.Fatalf("want one statement, got %d: %v", len(s), s)
	}
	return s[0]
}

// TestPragma_Renders pins the text of every pragma this package writes, and
// the OutsideTransaction flag where the value cannot be changed inside a
// transaction. The flags matter as much as the text: a runner reads them, and
// a pragma batched into a transaction it cannot be set in is either an error
// or, worse, a silent no-op.
func TestPragma_Renders(t *testing.T) {
	cases := []struct {
		name    string
		built   func() (ddl.Script, error)
		want    string
		outside bool
	}{
		{"user_version", sqlite.SetUserVersion(7).Build, `PRAGMA user_version = 7`, false},
		{"application_id", sqlite.SetApplicationID(1397836866).Build, `PRAGMA application_id = 1397836866`, false},
		{"foreign_keys on", sqlite.ForeignKeys(true).Build, `PRAGMA foreign_keys = ON`, true},
		{"foreign_keys off", sqlite.ForeignKeys(false).Build, `PRAGMA foreign_keys = OFF`, true},
		{"defer_foreign_keys", sqlite.DeferForeignKeys(true).Build, `PRAGMA defer_foreign_keys = ON`, false},
		{"recursive_triggers", sqlite.RecursiveTriggers(true).Build, `PRAGMA recursive_triggers = ON`, false},
		{"legacy_alter_table", sqlite.LegacyAlterTable(false).Build, `PRAGMA legacy_alter_table = OFF`, false},
		{"journal_mode wal", sqlite.SetJournalMode(sqlite.JournalWAL).Build, `PRAGMA journal_mode = WAL`, true},
		{"journal_mode delete", sqlite.SetJournalMode(sqlite.JournalDelete).Build, `PRAGMA journal_mode = DELETE`, true},
		{"journal_mode truncate", sqlite.SetJournalMode(sqlite.JournalTruncate).Build, `PRAGMA journal_mode = TRUNCATE`, true},
		{"journal_mode persist", sqlite.SetJournalMode(sqlite.JournalPersist).Build, `PRAGMA journal_mode = PERSIST`, true},
		{"journal_mode memory", sqlite.SetJournalMode(sqlite.JournalMemory).Build, `PRAGMA journal_mode = MEMORY`, true},
		{"journal_mode off", sqlite.SetJournalMode(sqlite.JournalOff).Build, `PRAGMA journal_mode = OFF`, true},
		{"synchronous off", sqlite.SetSynchronous(sqlite.SynchronousOff).Build, `PRAGMA synchronous = OFF`, true},
		{"synchronous normal", sqlite.SetSynchronous(sqlite.SynchronousNormal).Build, `PRAGMA synchronous = NORMAL`, true},
		{"synchronous full", sqlite.SetSynchronous(sqlite.SynchronousFull).Build, `PRAGMA synchronous = FULL`, true},
		{"synchronous extra", sqlite.SetSynchronous(sqlite.SynchronousExtra).Build, `PRAGMA synchronous = EXTRA`, true},
		{"temp_store default", sqlite.SetTempStore(sqlite.TempStoreDefault).Build, `PRAGMA temp_store = DEFAULT`, false},
		{"temp_store file", sqlite.SetTempStore(sqlite.TempStoreFile).Build, `PRAGMA temp_store = FILE`, false},
		{"temp_store memory", sqlite.SetTempStore(sqlite.TempStoreMemory).Build, `PRAGMA temp_store = MEMORY`, false},
		{"auto_vacuum none", sqlite.SetAutoVacuum(sqlite.AutoVacuumNone).Build, `PRAGMA auto_vacuum = NONE`, true},
		{"auto_vacuum full", sqlite.SetAutoVacuum(sqlite.AutoVacuumFull).Build, `PRAGMA auto_vacuum = FULL`, true},
		{"auto_vacuum incremental", sqlite.SetAutoVacuum(sqlite.AutoVacuumIncremental).Build, `PRAGMA auto_vacuum = INCREMENTAL`, true},
		{"secure_delete off", sqlite.SetSecureDelete(sqlite.SecureDeleteOff).Build, `PRAGMA secure_delete = OFF`, false},
		{"secure_delete on", sqlite.SetSecureDelete(sqlite.SecureDeleteOn).Build, `PRAGMA secure_delete = ON`, false},
		{"secure_delete fast", sqlite.SetSecureDelete(sqlite.SecureDeleteFast).Build, `PRAGMA secure_delete = FAST`, false},
		{"busy_timeout", sqlite.SetBusyTimeout(5 * time.Second).Build, `PRAGMA busy_timeout = 5000`, false},
		{"busy_timeout sub-millisecond", sqlite.SetBusyTimeout(1500 * time.Microsecond).Build, `PRAGMA busy_timeout = 1`, false},
		{"busy_timeout zero", sqlite.SetBusyTimeout(0).Build, `PRAGMA busy_timeout = 0`, false},
		{"cache_size pages", sqlite.SetCacheSizePages(2000).Build, `PRAGMA cache_size = 2000`, false},
		{"cache_size kibibytes", sqlite.SetCacheSizeKibibytes(2000).Build, `PRAGMA cache_size = -2000`, false},
		{"mmap_size", sqlite.SetMmapSize(268435456).Build, `PRAGMA mmap_size = 268435456`, false},
		{"mmap_size off", sqlite.SetMmapSize(0).Build, `PRAGMA mmap_size = 0`, false},
		{"page_size", sqlite.SetPageSize(8192).Build, `PRAGMA page_size = 8192`, true},
		{"page_size smallest", sqlite.SetPageSize(512).Build, `PRAGMA page_size = 512`, true},
		{"page_size largest", sqlite.SetPageSize(65536).Build, `PRAGMA page_size = 65536`, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			st := oneStatement(t, tc.built)
			if st.SQL != tc.want {
				t.Errorf("got %s, want %s", st.SQL, tc.want)
			}
			if st.OutsideTransaction != tc.outside {
				t.Errorf("OutsideTransaction is %v, want %v", st.OutsideTransaction, tc.outside)
			}
			if st.MustReturnNoRows {
				t.Error("a pragma that sets a value is not a check")
			}
		})
	}
}

// TestPragma_ChecksReportByReturningRows pins the three checks and the one
// thing that makes them checks: MustReturnNoRows, so a runner that executes
// one and ignores the result is not reporting success on a damaged database.
//
// integrity_check and quick_check are SELECTs over the pragma's table-valued
// form rather than the bare pragma, because both always return rows — the
// single row 'ok' when they found nothing — so MustReturnNoRows over the bare
// pragma would fail every healthy database.
func TestPragma_ChecksReportByReturningRows(t *testing.T) {
	cases := []struct {
		name  string
		built func() (ddl.Script, error)
		want  string
	}{
		{"foreign_key_check", sqlite.ForeignKeyCheck().Build, `PRAGMA foreign_key_check`},
		{"integrity_check", sqlite.IntegrityCheck().Build, `SELECT 1 FROM pragma_integrity_check WHERE "integrity_check" <> 'ok'`},
		{"quick_check", sqlite.QuickCheck().Build, `SELECT 1 FROM pragma_quick_check WHERE "quick_check" <> 'ok'`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			st := oneStatement(t, tc.built)
			if st.SQL != tc.want {
				t.Errorf("got %s, want %s", st.SQL, tc.want)
			}
			if !st.MustReturnNoRows {
				t.Error("a check must be marked MustReturnNoRows or a runner cannot tell it failed")
			}
			if st.OutsideTransaction {
				t.Error("a check reads and runs inside a transaction")
			}
		})
	}
}

// TestPragma_RefusesWhatSQLiteWouldIgnore is the point of the typed
// constructors: SQLite accepts a page size it will not use and a value outside
// a closed set, and changes nothing. Build refuses both.
func TestPragma_RefusesWhatSQLiteWouldIgnore(t *testing.T) {
	cases := []struct {
		name  string
		built func() (ddl.Script, error)
		want  error
	}{
		{"page size not a power of two", sqlite.SetPageSize(1000).Build, sqlite.ErrPageSize},
		{"page size too small", sqlite.SetPageSize(256).Build, sqlite.ErrPageSize},
		{"page size too large", sqlite.SetPageSize(131072).Build, sqlite.ErrPageSize},
		{"page size zero", sqlite.SetPageSize(0).Build, sqlite.ErrPageSize},
		{"negative timeout", sqlite.SetBusyTimeout(-time.Second).Build, sqlite.ErrNegativeTimeout},
		{"journal mode zero value", sqlite.SetJournalMode(0).Build, sqlite.ErrUnknownKeyword},
		{"journal mode out of range", sqlite.SetJournalMode(99).Build, sqlite.ErrUnknownKeyword},
		{"synchronous zero value", sqlite.SetSynchronous(0).Build, sqlite.ErrUnknownKeyword},
		{"temp store zero value", sqlite.SetTempStore(0).Build, sqlite.ErrUnknownKeyword},
		{"auto vacuum zero value", sqlite.SetAutoVacuum(0).Build, sqlite.ErrUnknownKeyword},
		{"secure delete zero value", sqlite.SetSecureDelete(0).Build, sqlite.ErrUnknownKeyword},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := tc.built(); !errors.Is(err, tc.want) {
				t.Errorf("got %v, want %v", err, tc.want)
			}
		})
	}
	if _, err := (&sqlite.PragmaStatement{}).Build(); !errors.Is(err, ddl.ErrZeroValue) {
		t.Errorf("the zero value built with %v", err)
	}
}

// TestPragma_ValuesAreImmutable pins that a pragma is a value: two
// constructions of the same statement are independent, and holding one and
// building it twice gives the same script. There is no builder method to
// mutate, which is why the type has none.
func TestPragma_ValuesAreImmutable(t *testing.T) {
	p := sqlite.SetJournalMode(sqlite.JournalWAL)
	first := oneStatement(t, p.Build)
	second := oneStatement(t, p.Build)
	if first != second {
		t.Errorf("building twice gave %v then %v", first, second)
	}
}
