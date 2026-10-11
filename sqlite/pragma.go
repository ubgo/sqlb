package sqlite

import (
	"errors"
	"fmt"
	"strconv"
	"time"

	"github.com/ubgo/sqlb"
	"github.com/ubgo/sqlb/ddl"
)

// The PRAGMA statements a library that owns tables sets, and the three checks
// that report a database it can no longer trust.
//
// Every one is a typed constructor over a closed set rather than a string, for
// the reason the rest of this module exists: SQLite does not refuse a pragma
// name it does not know and does not refuse a value outside the range it
// accepts, it ignores them, so a misspelling leaves the setting at its default
// and nothing anywhere says so. Measured on 3.50.3: PRAGMA page_size = 1000
// returned no error and the page size stayed 16384. That is why SetPageSize
// refuses a size that is not a power of two in range instead of handing the
// engine a value it will silently drop.
//
// Five of them are marked OutsideTransaction, each measured on 3.50.3 rather
// than read: synchronous raises "Safety level may not be changed inside a
// transaction"; journal_mode raises "cannot change out of wal mode from within
// a transaction" inside a transaction that has not written, and inside one
// that has it returns no error and changes nothing, which is worse; page_size
// and auto_vacuum are accepted inside a transaction and take effect on a
// database that already has pages only after a VACUUM, which itself cannot run
// inside one. foreign_keys is the fifth and oldest case, a silent no-op inside
// a transaction. defer_foreign_keys is deliberately not marked, because inside
// a transaction is the only place it means anything.
//
// What is deliberately absent, and why:
//
//   - The introspection pragmas, table_info, index_list, foreign_key_list,
//     database_list and their kin, are reads rather than statements. A ddl
//     Script is a list of things to run; a question belongs in a SELECT, and
//     SQLite's pragma_* table-valued functions put it there.
//   - The maintenance pragmas, optimize, incremental_vacuum, wal_checkpoint,
//     shrink_memory and analysis_limit, are the operator's. When they run is a
//     decision about a live database under a known load, not about a schema a
//     library owns.
//   - The resource limits, max_page_count, soft_heap_limit, hard_heap_limit,
//     threads and locking_mode, are the operator's for the same reason, and
//     locking_mode = EXCLUSIVE additionally locks the file against every other
//     process on the machine, which a library has no standing to decide.
//   - writable_schema and ignore_check_constraints are left out because their
//     whole purpose is to break the guarantees the rest of this package makes.
//     writable_schema lets a caller write sqlite_schema directly and so
//     produce a database that still opens and is corrupt; ignore_check
//     _constraints silently stops enforcing every CHECK the DDL declares, so
//     rows that the schema says cannot exist are written without an error.
//   - case_sensitive_like, count_changes, full_column_names,
//     short_column_names, temp_store_directory and data_store_directory are
//     deprecated, and several are already no-ops, so a constructor for them
//     would promise something the engine no longer does.

// The pragma names this package writes, one constant per statement below. They
// are named rather than spelled at each call site for the reason above: a
// misspelled pragma is not an error, it is a setting that never changed.
const (
	pragmaUserVersion       = "user_version"
	pragmaApplicationID     = "application_id"
	pragmaForeignKeys       = "foreign_keys"
	pragmaDeferForeignKeys  = "defer_foreign_keys"
	pragmaJournalMode       = "journal_mode"
	pragmaSynchronous       = "synchronous"
	pragmaBusyTimeout       = "busy_timeout"
	pragmaCacheSize         = "cache_size"
	pragmaTempStore         = "temp_store"
	pragmaMmapSize          = "mmap_size"
	pragmaPageSize          = "page_size"
	pragmaAutoVacuum        = "auto_vacuum"
	pragmaSecureDelete      = "secure_delete"
	pragmaRecursiveTriggers = "recursive_triggers"
	pragmaLegacyAlterTable  = "legacy_alter_table"
	pragmaIntegrityCheck    = "integrity_check"
	pragmaQuickCheck        = "quick_check"
	pragmaForeignKeyCheck   = "foreign_key_check"
)

// The fixed text a pragma statement is built from.
const (
	pragmaOn  = "ON"
	pragmaOff = "OFF"
	// pragmaFnPrefix turns a pragma name into SQLite's table-valued function
	// of the same name, which is how a check that reports through rows is put
	// inside a SELECT.
	pragmaFnPrefix = "pragma_"
	// pragmaOK is the one value integrity_check and quick_check return when
	// they found nothing wrong.
	pragmaOK = "ok"
	// pageSizeMin and pageSizeMax bound PRAGMA page_size, which SQLite accepts
	// only as a power of two in this range and silently ignores otherwise.
	pageSizeMin = 512
	pageSizeMax = 65536
	// nsPerMillisecond converts a Go duration, which counts nanoseconds, to
	// the milliseconds busy_timeout takes.
	nsPerMillisecond = int64(time.Millisecond)
	// kwSelectOne opens the SELECT a check pragma is wrapped in; the value is
	// never read, only whether a row came back.
	kwSelectOne = "SELECT 1 FROM"
)

// ErrPageSize is a page size SQLite would silently ignore: it takes a power of
// two from 512 to 65536 and drops anything else without an error.
var ErrPageSize = errors.New("sqlb/sqlite: page_size must be a power of two from 512 to 65536")

// ErrNegativeTimeout is a busy timeout below zero, which SQLite reads as no
// wait at all rather than as the wait the caller meant.
var ErrNegativeTimeout = errors.New("sqlb/sqlite: busy_timeout cannot be negative")

// pragmaKind says how a pragma reports, which decides the Statement flags it
// builds with. The zero value is the ordinary case, a pragma that sets a value
// and reports nothing.
type pragmaKind uint8

const (
	// pragmaSet is PRAGMA name = value: it changes a setting and any row it
	// returns is the new value, not a problem.
	pragmaSet pragmaKind = iota
	// pragmaRowIsAProblem is a check that returns one row per problem and no
	// row at all when the database is sound, which is foreign_key_check. It
	// builds with MustReturnNoRows, so a runner that executes it and ignores
	// the result is not reporting success on a damaged database.
	pragmaRowIsAProblem
	// pragmaOKRow is a check that always returns rows, the single row 'ok'
	// when the database is sound: integrity_check and quick_check. Measured on
	// 3.50.3. MustReturnNoRows would therefore fail on a healthy database, so
	// the statement is a SELECT over the pragma's table-valued form with the
	// 'ok' row filtered away, which makes "no rows" mean "nothing wrong"
	// again.
	pragmaOKRow
)

// PragmaStatement is a PRAGMA: one that sets a value, or one of the three
// checks that report a damaged database by returning rows.
//
// It is one type for every pragma this package writes so that a pragma is a
// value a caller can hold, pass and put in a migration Script beside the DDL,
// and so that adding one is a constructor rather than a builder. The zero
// value carries no name and Build refuses it.
type PragmaStatement struct {
	name, value string
	outside     bool
	kind        pragmaKind
	// table narrows a check to one table, and schema qualifies it; both are
	// empty for a check over the whole database and for every setting.
	table, schema string
	// err is a value the constructor already knows SQLite would silently
	// ignore. It is carried rather than returned so that a pragma is still a
	// value, and surfaces at Build like every other refusal in this module.
	err error
}

// SetUserVersion sets PRAGMA user_version, an integer in the database header
// that SQLite stores and never reads: a migration ledger with no table, for a
// database one library owns.
func SetUserVersion(n int32) *PragmaStatement {
	return &PragmaStatement{name: pragmaUserVersion, value: strconv.Itoa(int(n))}
}

// SetApplicationID sets PRAGMA application_id, an integer in the database
// header that says which application a file belongs to, the way a file
// format's magic number does. SetUserVersion is its counterpart for the
// schema's version.
func SetApplicationID(n int32) *PragmaStatement {
	return &PragmaStatement{name: pragmaApplicationID, value: strconv.Itoa(int(n))}
}

// ForeignKeys turns foreign-key enforcement on or off for the connection. It
// is a silent no-op inside a transaction, which is how a rebuild wrapped in
// one quietly loses its guard, so the Statement is marked OutsideTransaction
// and a runner must execute it before BEGIN.
func ForeignKeys(on bool) *PragmaStatement {
	return &PragmaStatement{name: pragmaForeignKeys, value: onOff(on), outside: true}
}

// DeferForeignKeys holds foreign-key enforcement until the transaction
// commits, so a batch of statements may leave a reference dangling in the
// middle and repair it before the end.
//
// It is the one pragma here that is not marked OutsideTransaction, and
// deliberately so: SQLite clears it at every commit or rollback, measured, so
// setting it outside a transaction changes nothing that survives to the
// transaction it was meant for. It is a weaker tool than ForeignKeys(false):
// the constraints are still checked, only later, so a rebuild that genuinely
// cannot satisfy them at the end needs the enforcement off instead.
func DeferForeignKeys(on bool) *PragmaStatement {
	return &PragmaStatement{name: pragmaDeferForeignKeys, value: onOff(on)}
}

// RecursiveTriggers turns recursive trigger firing on or off.
//
// SQLite leaves it off for backwards compatibility, which is a trap for a
// schema whose triggers are written expecting the ordinary meaning: a trigger
// whose body changes a table with a trigger of its own does not fire the
// second one, silently, so half the intended work never happens. A library
// that ships such triggers sets this on and says so.
func RecursiveTriggers(on bool) *PragmaStatement {
	return &PragmaStatement{name: pragmaRecursiveTriggers, value: onOff(on)}
}

// LegacyAlterTable makes ALTER TABLE ... RENAME TO behave as it did before
// SQLite 3.25: it renames the table and does not rewrite the references to it
// in other tables' foreign keys, triggers and views.
//
// It exists because the modern behaviour is what a table rebuild has to avoid.
// The rebuild in this package creates a new table under its own name and
// renames it into place at the end; with references rewritten, that rename
// would follow every foreign key pointing at the old table over to the new
// one before the old one is gone. Anything else should leave it off: a rename
// that quietly leaves stale references behind is a schema that fails later and
// far from the cause.
func LegacyAlterTable(on bool) *PragmaStatement {
	return &PragmaStatement{name: pragmaLegacyAlterTable, value: onOff(on)}
}

// JournalMode is how SQLite journals a transaction, which decides what happens
// to a database when the process dies mid-write.
type JournalMode int

// The journal modes. WAL is the one to want for anything concurrent: readers
// run while a writer commits, where every other mode blocks them. It is also
// the only one stored in the database file rather than per connection, so
// setting it once is permanent, and the only one that does not work on a
// database opened over a network filesystem or held in memory.
const (
	JournalDelete JournalMode = iota + 1
	JournalTruncate
	JournalPersist
	JournalMemory
	JournalWAL
	JournalOff
)

func (m JournalMode) keyword() (string, bool) {
	switch m {
	case JournalDelete:
		return "DELETE", true
	case JournalTruncate:
		return "TRUNCATE", true
	case JournalPersist:
		return "PERSIST", true
	case JournalMemory:
		return "MEMORY", true
	case JournalWAL:
		return "WAL", true
	case JournalOff:
		return "OFF", true
	}
	return "", false
}

// SetJournalMode sets PRAGMA journal_mode.
//
// The statement is marked OutsideTransaction, and both halves of the
// measurement say why. Inside a transaction that has not written, SQLite
// raises "cannot change out of wal mode from within a transaction", so a
// runner batching it with a migration would fail the migration for the
// ordering rather than for the schema. Inside one that has written it returns
// no error and leaves the mode alone, which is the silent half and the reason
// a flag is better than a comment. Both measured on 3.50.3.
//
// JournalOff means a crash mid-write leaves a corrupt database, so it belongs
// to a throwaway file and nothing else.
func SetJournalMode(m JournalMode) *PragmaStatement {
	return keyworded(pragmaJournalMode, m.keyword, "JournalMode", true)
}

// Synchronous is how hard SQLite makes the filesystem promise a commit
// reached the disk.
type Synchronous int

// The synchronous levels. Normal is the documented companion of WAL: a process
// crash cannot lose a commit, only a power loss can, and it costs a fraction
// of Full. Off means the database can be corrupted by a power loss, not merely
// missing its last commits.
const (
	SynchronousOff Synchronous = iota + 1
	SynchronousNormal
	SynchronousFull
	SynchronousExtra
)

func (s Synchronous) keyword() (string, bool) {
	switch s {
	case SynchronousOff:
		return "OFF", true
	case SynchronousNormal:
		return "NORMAL", true
	case SynchronousFull:
		return "FULL", true
	case SynchronousExtra:
		return "EXTRA", true
	}
	return "", false
}

// SetSynchronous sets PRAGMA synchronous, per connection.
//
// The statement is marked OutsideTransaction: SQLite raises "Safety level may
// not be changed inside a transaction", measured on 3.50.3, so a runner that
// batched it with the rest of a migration would fail the migration for the
// ordering rather than for the schema.
func SetSynchronous(s Synchronous) *PragmaStatement {
	return keyworded(pragmaSynchronous, s.keyword, "Synchronous", true)
}

// TempStore is where SQLite puts a temporary table or index, and the sort
// files a large ORDER BY or a VACUUM needs.
type TempStore int

// The temp stores. Default defers to how the library was compiled, which is
// the thing a caller setting this pragma is usually trying to stop depending
// on.
const (
	TempStoreDefault TempStore = iota + 1
	TempStoreFile
	TempStoreMemory
)

func (s TempStore) keyword() (string, bool) {
	switch s {
	case TempStoreDefault:
		return "DEFAULT", true
	case TempStoreFile:
		return "FILE", true
	case TempStoreMemory:
		return "MEMORY", true
	}
	return "", false
}

// SetTempStore sets PRAGMA temp_store, per connection. TempStoreMemory trades
// memory for speed and will fail a query whose sort does not fit.
func SetTempStore(s TempStore) *PragmaStatement {
	return keyworded(pragmaTempStore, s.keyword, "TempStore", false)
}

// AutoVacuum is what SQLite does with the pages a DELETE or a DROP freed.
type AutoVacuum int

// The auto-vacuum modes. None leaves the file the size it grew to, and the
// free pages are reused by later writes. Full returns them to the filesystem
// at every commit, which costs a little on every write. Incremental keeps a
// list of them and returns them when the operator asks, which is the mode a
// library should prefer because it leaves the when to whoever runs the
// database.
const (
	AutoVacuumNone AutoVacuum = iota + 1
	AutoVacuumFull
	AutoVacuumIncremental
)

func (v AutoVacuum) keyword() (string, bool) {
	switch v {
	case AutoVacuumNone:
		return "NONE", true
	case AutoVacuumFull:
		return "FULL", true
	case AutoVacuumIncremental:
		return "INCREMENTAL", true
	}
	return "", false
}

// SetAutoVacuum sets PRAGMA auto_vacuum.
//
// It is stored in the database file and can only be changed for free before
// the first table is created. Afterwards the statement is accepted, changes
// nothing and reports nothing, measured on 3.50.3, until a VACUUM rewrites the
// file; so it is marked OutsideTransaction, because that VACUUM cannot run
// inside a transaction and a caller who has to follow this statement with one
// must be able to.
func SetAutoVacuum(v AutoVacuum) *PragmaStatement {
	return keyworded(pragmaAutoVacuum, v.keyword, "AutoVacuum", true)
}

// SecureDelete is what SQLite writes over a deleted row's bytes.
type SecureDelete int

// The secure-delete modes. Off leaves the content in the free page until
// something else overwrites it, so a deleted secret is still in the file. On
// zeroes it, at the cost of writing those pages. Fast zeroes only what it can
// do without extra page writes, which covers the common case of a row deleted
// from a page that is being rewritten anyway.
const (
	SecureDeleteOff SecureDelete = iota + 1
	SecureDeleteOn
	SecureDeleteFast
)

func (s SecureDelete) keyword() (string, bool) {
	switch s {
	case SecureDeleteOff:
		return pragmaOff, true
	case SecureDeleteOn:
		return pragmaOn, true
	case SecureDeleteFast:
		return "FAST", true
	}
	return "", false
}

// SetSecureDelete sets PRAGMA secure_delete.
func SetSecureDelete(s SecureDelete) *PragmaStatement {
	return keyworded(pragmaSecureDelete, s.keyword, "SecureDelete", false)
}

// SetBusyTimeout sets PRAGMA busy_timeout: how long a statement waits for a
// lock another connection holds before it gives up with SQLITE_BUSY.
//
// The default is zero, which is why SQLITE_BUSY is the error everyone meets
// first: with no wait at all, two connections writing at the same instant is
// enough. The duration is rounded down to whole milliseconds, which is the
// unit SQLite takes. A negative duration is refused rather than rendered,
// because SQLite reads a negative value as no wait at all, which is the
// opposite of what writing one could mean.
func SetBusyTimeout(d time.Duration) *PragmaStatement {
	if d < 0 {
		return &PragmaStatement{name: pragmaBusyTimeout, err: ErrNegativeTimeout}
	}
	return &PragmaStatement{name: pragmaBusyTimeout, value: strconv.FormatInt(int64(d)/nsPerMillisecond, 10)}
}

// SetCacheSizePages sets PRAGMA cache_size as a number of database pages,
// which is what a positive value means to SQLite.
//
// Pages, not bytes: the memory this reserves is pages times the page size, so
// the same number means a different amount of memory on a database built with
// a different page size. SetCacheSizeKibibytes is the form that does not
// depend on it, and is the one to prefer when the number came from a memory
// budget rather than from measuring this database.
func SetCacheSizePages(pages uint32) *PragmaStatement {
	return &PragmaStatement{name: pragmaCacheSize, value: strconv.FormatUint(uint64(pages), 10)}
}

// SetCacheSizeKibibytes sets PRAGMA cache_size as an amount of memory.
//
// SQLite spells this as a negative number of kibibytes, which is the detail
// this constructor exists to keep out of a call site: a caller who writes the
// number without the sign asks for that many pages instead, silently, and on a
// 4 KiB page that is four times the memory they meant.
func SetCacheSizeKibibytes(kib uint32) *PragmaStatement {
	return &PragmaStatement{name: pragmaCacheSize, value: "-" + strconv.FormatUint(uint64(kib), 10)}
}

// SetMmapSize sets PRAGMA mmap_size, the most of the database file SQLite may
// read through a memory mapping instead of through read calls. Zero turns
// mapping off.
//
// It is a ceiling, not a reservation: SQLite maps up to this much and no more,
// so a value larger than the file costs nothing. A mapping makes an I/O error
// in the file arrive as a signal rather than as an error return, which is the
// reason it is not on by default.
func SetMmapSize(bytes uint64) *PragmaStatement {
	return &PragmaStatement{name: pragmaMmapSize, value: strconv.FormatUint(bytes, 10)}
}

// SetPageSize sets PRAGMA page_size, the size of every page in the database
// file.
//
// Build refuses a size that is not a power of two from 512 to 65536, because
// SQLite does not: it accepts the statement, ignores the value and leaves the
// page size alone, measured on 3.50.3 with 1000, which is exactly the silent
// wrong answer this module exists to turn into an error.
//
// Even a legal size only takes effect on a database with no pages yet, so this
// belongs before the first CREATE TABLE; on a database that already has
// content the size changes when a VACUUM rewrites the file, and the statement
// is marked OutsideTransaction so that a caller can run that VACUUM after it.
func SetPageSize(bytes uint32) *PragmaStatement {
	if bytes < pageSizeMin || bytes > pageSizeMax || bytes&(bytes-1) != 0 {
		return &PragmaStatement{name: pragmaPageSize, err: ErrPageSize}
	}
	return &PragmaStatement{name: pragmaPageSize, value: strconv.FormatUint(uint64(bytes), 10), outside: true}
}

// IntegrityCheck verifies the whole database: every page, every index entry
// against its table, every NOT NULL and every UNIQUE.
//
// It builds as a SELECT over SQLite's pragma_integrity_check table-valued
// function with the 'ok' row filtered away, rather than as the bare PRAGMA,
// and the reason is the Statement flag it needs. A check is only a check if a
// runner can tell a healthy answer from a damaged one; integrity_check always
// returns rows, the single row 'ok' when it found nothing, so MustReturnNoRows
// over the bare pragma would fail every healthy database. Filtering 'ok' away
// makes no rows mean nothing wrong, which is what the flag says.
//
// It reads every page, so it costs a full scan of the file. QuickCheck is the
// one to run routinely.
func IntegrityCheck() *PragmaStatement {
	return &PragmaStatement{name: pragmaIntegrityCheck, kind: pragmaOKRow}
}

// QuickCheck is IntegrityCheck without the index-against-table comparison,
// which is most of the cost. It finds a corrupt page and misses an index that
// disagrees with its table.
func QuickCheck() *PragmaStatement {
	return &PragmaStatement{name: pragmaQuickCheck, kind: pragmaOKRow}
}

// ForeignKeyCheck reports every row whose foreign key points at a row that is
// not there, one row per violation and nothing at all when the references are
// sound.
//
// It is the check a table rebuild ends with, and it is the reason
// ddl.Statement has MustReturnNoRows: this pragma reports a broken reference by
// returning a row rather than by failing, so a runner that only executes it
// reports success whatever it found.
func ForeignKeyCheck() *PragmaStatement {
	return &PragmaStatement{name: pragmaForeignKeyCheck, kind: pragmaRowIsAProblem}
}

// IntegrityCheckOf is IntegrityCheck over one table rather than the whole
// database: its rows, its indexes and the constraints between them.
//
// It is the form a library that owns a few tables in somebody else's database
// wants, because the whole-database check reads every page of every table,
// including tables this library did not write and cannot repair. The table's
// schema qualifies the pragma where it declares one.
//
// The N argument SQLite also accepts, a cap on how many errors are listed, is
// not offered: measured on 3.50.3, the table-valued form these build on reads
// any argument as a table name and answers "no such table: 5", and a cap is
// meaningless under MustReturnNoRows, which asks only whether there is one
// error rather than how many.
func IntegrityCheckOf(t sqlb.Table) *PragmaStatement {
	return checkOf(pragmaIntegrityCheck, pragmaOKRow, t)
}

// QuickCheckOf is QuickCheck over one table; see IntegrityCheckOf.
func QuickCheckOf(t sqlb.Table) *PragmaStatement {
	return checkOf(pragmaQuickCheck, pragmaOKRow, t)
}

// ForeignKeyCheckOf reports every row of one table whose foreign key points at
// a row that is not there.
//
// One table rather than the database is the useful scope for a rebuild: the
// rebuild changed one table, and checking the rest is reading a whole database
// to learn nothing about the change.
func ForeignKeyCheckOf(t sqlb.Table) *PragmaStatement {
	return checkOf(pragmaForeignKeyCheck, pragmaRowIsAProblem, t)
}

// checkOf builds a check narrowed to one table, failing closed on a table
// value with no name.
func checkOf(name string, kind pragmaKind, t sqlb.Table) *PragmaStatement {
	if t.Name == "" {
		return &PragmaStatement{name: name, kind: kind, err: ErrEmptyName}
	}
	return &PragmaStatement{name: name, kind: kind, table: t.Name, schema: t.Schema}
}

// onOff renders a boolean pragma's value.
func onOff(on bool) string {
	if on {
		return pragmaOn
	}
	return pragmaOff
}

// keyworded makes a pragma whose value comes from a closed set, carrying
// ErrUnknownKeyword when the value is outside it, which is what a zero value
// of one of those types is.
func keyworded(name string, kw func() (string, bool), typ string, outside bool) *PragmaStatement {
	v, ok := kw()
	if !ok {
		return &PragmaStatement{name: name, err: fmt.Errorf("%w: %s", ErrUnknownKeyword, typ)}
	}
	return &PragmaStatement{name: name, value: v, outside: outside}
}

// argument renders the parenthesised table a narrowed check names, or nothing.
// The table name is a literal rather than an identifier, because that is the
// form both the pragma and its table-valued function take.
func (x *PragmaStatement) argument(a *sqlb.Args) string {
	if x.table == "" {
		return ""
	}
	return "(" + a.Literal(x.table) + ")"
}

// Build renders the statement.
func (x *PragmaStatement) Build() (ddl.Script, error) {
	if x.name == "" {
		return nil, ddl.ErrZeroValue
	}
	if x.err != nil {
		return nil, x.err
	}
	switch x.kind {
	case pragmaSet:
		if x.value == "" {
			return nil, ddl.ErrZeroValue
		}
		return ddl.Script{{SQL: kwPragma + " " + x.name + " = " + x.value, Transactional: true, OutsideTransaction: x.outside}}, nil
	case pragmaRowIsAProblem:
		a := sqlb.NewInlineArgs(Dialect{})
		// The pragma name is a keyword rather than an identifier, so only the
		// schema is quoted.
		name := x.name
		if x.schema != "" {
			name = a.Dialect().QuoteIdent(x.schema) + "." + name
		}
		sql := kwPragma + " " + name + x.argument(a)
		if err := a.Err(); err != nil {
			return nil, err
		}
		return ddl.Script{{SQL: sql, Transactional: true, MustReturnNoRows: true}}, nil
	case pragmaOKRow:
		a := sqlb.NewInlineArgs(Dialect{})
		d := a.Dialect()
		// The table-valued form, so the 'ok' row can be filtered away; a
		// schema qualifies the function exactly as it qualifies the pragma.
		fn := pragmaFnPrefix + x.name
		if x.schema != "" {
			fn = d.QuoteIdent(x.schema) + "." + fn
		}
		sql := kwSelectOne + " " + fn + x.argument(a) + " " + kwWhere + " " +
			d.QuoteIdent(x.name) + " " + sqlb.OpNotEq + " " + a.Literal(pragmaOK)
		if err := a.Err(); err != nil {
			return nil, err
		}
		return ddl.Script{{SQL: sql, Transactional: true, MustReturnNoRows: true}}, nil
	}
	// kind is unexported and every constructor above sets one of the three
	// constants, so this is unreachable; it fails rather than falling through
	// to a shape chosen by guesswork if a fourth kind is ever added without
	// its case.
	return nil, fmt.Errorf("%w: pragmaKind", ErrUnknownKeyword)
}
