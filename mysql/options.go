package mysql

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/ubgo/sqlb"
	"github.com/ubgo/sqlb/ddl"
)

// The MySQL table options, in CREATE TABLE and in ALTER TABLE. They are here
// rather than in table.go because the pair is the point: every option a
// CREATE TABLE writes has an ALTER TABLE form that changes it, and until
// 2026-09-24 only the CREATE half existed, so a caller could declare a
// table's ENGINE, charset, collation and comment and then had no way to alter
// one without dropping to a raw statement.
//
// MySQL rebuilds the whole table for ENGINE, ROW_FORMAT and CONVERT TO
// CHARACTER SET, and only rewrites the .frm for COMMENT, the default charset
// and the default collation; each doc comment says which, because the
// difference is minutes against milliseconds on a large table.

const (
	kwRowFormat        = "ROW_FORMAT ="
	kwAutoIncrementSet = "AUTO_INCREMENT ="
	kwDefaultCharSet   = "DEFAULT CHARACTER SET ="
	kwConvertTo        = "CONVERT TO CHARACTER SET"
)

// Engine is a MySQL storage engine. The set is closed to the three a library
// would ask for by name; anything else is an operator's choice made in the
// server's default, not in a schema a library ships.
type Engine string

// The storage engines.
const (
	// EngineInnoDB is the only one with transactions, foreign keys and row
	// locks, which is what a library's table needs. It is also the server
	// default, and writing it makes the table not depend on that default.
	EngineInnoDB Engine = "InnoDB"
	// EngineMemory holds the table in RAM and loses it on restart: a scratch
	// table, never one that owns data.
	EngineMemory Engine = "MEMORY"
	// EngineMyISAM has no transactions and locks the whole table; it is here
	// because a table inherited from an older schema may still be on it and
	// SetEngine(EngineInnoDB) is how it is moved off.
	EngineMyISAM Engine = "MyISAM"
)

func (e Engine) valid() bool {
	switch e {
	case EngineInnoDB, EngineMemory, EngineMyISAM:
		return true
	}
	return false
}

// RowFormatKind is how InnoDB stores a row. It decides how a long VARCHAR,
// TEXT or BLOB value is split between the row and overflow pages, which is
// what the index-key length limit is measured against.
type RowFormatKind string

// The row formats.
const (
	// RowFormatDynamic stores a long value entirely off-page, keeping a 20
	// byte pointer in the row. It is InnoDB's default since 5.7.9 and the one
	// that allows a 3072 byte index key.
	RowFormatDynamic RowFormatKind = "DYNAMIC"
	// RowFormatCompressed is Dynamic with the pages compressed, trading CPU
	// for disk.
	RowFormatCompressed RowFormatKind = "COMPRESSED"
	// RowFormatCompact stores the first 768 bytes of a long value in the row.
	RowFormatCompact RowFormatKind = "COMPACT"
	// RowFormatRedundant is the pre-5.0.3 format, kept for a table restored
	// from an old dump.
	RowFormatRedundant RowFormatKind = "REDUNDANT"
	// RowFormatDefault defers to innodb_default_row_format, which is what a
	// table written without the option already does; it exists so an ALTER
	// can put a table back under the server's default.
	RowFormatDefault RowFormatKind = "DEFAULT"
)

func (f RowFormatKind) valid() bool {
	switch f {
	case RowFormatDynamic, RowFormatCompressed, RowFormatCompact, RowFormatRedundant, RowFormatDefault:
		return true
	}
	return false
}

// TableEngine sets the table's storage engine in CREATE TABLE. InnoDB is the
// same thing spelled for the one engine a library's table wants.
func TableEngine(e Engine) TableClause {
	return tableOption(func(a *sqlb.Args) string { return engineOption(a, e) })
}

// engineOption renders ENGINE = e, refusing a value outside the constants:
// Engine is a string type, so Engine("Falcon") compiles and MySQL would
// accept it with a warning and silently use the default engine instead.
func engineOption(a *sqlb.Args, e Engine) string {
	if !e.valid() {
		a.Fail(fmt.Errorf("%w: engine %q", ErrUnknownKeyword, string(e)))
		return ""
	}
	return kwEngine + " " + string(e)
}

// rowFormatOption renders ROW_FORMAT = f, refusing a value outside the
// constants for the reason engineOption does.
func rowFormatOption(a *sqlb.Args, f RowFormatKind) string {
	if !f.valid() {
		a.Fail(fmt.Errorf("%w: row format %q", ErrUnknownKeyword, string(f)))
		return ""
	}
	return kwRowFormat + " " + string(f)
}

// RowFormat sets how InnoDB stores a row of the table in CREATE TABLE.
func RowFormat(f RowFormatKind) TableClause {
	return tableOption(func(a *sqlb.Args) string { return rowFormatOption(a, f) })
}

// SetEngine changes the table's storage engine, and is how a table is moved
// onto InnoDB. MySQL rebuilds the table to do it, copying every row, and
// running it with the table's current engine is the documented way to
// defragment it.
func SetEngine(e Engine) AlterClause {
	return alterAction{sql: func(a *sqlb.Args) string { return engineOption(a, e) }}
}

// SetRowFormat changes how InnoDB stores a row, which rebuilds the table.
func SetRowFormat(f RowFormatKind) AlterClause {
	return alterAction{sql: func(a *sqlb.Args) string { return rowFormatOption(a, f) }}
}

// SetTableComment replaces the table's comment. It touches the definition
// only, so it does not rebuild the table.
func SetTableComment(text string) AlterClause {
	return alterAction{sql: func(a *sqlb.Args) string { return kwTableComment + " " + a.Literal(text) }}
}

// ErrAutoIncrementZero is returned for an AUTO_INCREMENT of 0: MySQL reads it
// as "reset to the default", which is the opposite of what a caller asking
// for a specific next value means.
var ErrAutoIncrementZero = fmt.Errorf("%w: AUTO_INCREMENT must be at least 1", ErrUnknownKeyword)

// SetAutoIncrement sets the value the next inserted row gets. MySQL will not
// lower it below the current maximum key: a smaller value is silently raised
// to that maximum plus one, so this reserves a range rather than rewinding
// one, which is why it is the statement run after importing rows with
// explicit ids.
func SetAutoIncrement(n uint64) AlterClause {
	return alterAction{sql: func(a *sqlb.Args) string {
		if n == 0 {
			a.Fail(ErrAutoIncrementZero)
			return ""
		}
		return kwAutoIncrementSet + " " + strconv.FormatUint(n, 10)
	}}
}

// SetDefaultCharset changes the character set new columns of the table get.
// It leaves every existing column on its own character set, which is what
// makes it cheap and also what makes it the wrong statement for fixing a
// table already holding latin1 text: that is ConvertToCharset.
func SetDefaultCharset(charset string) AlterClause {
	return alterAction{sql: func(a *sqlb.Args) string {
		return kwDefaultCharSet + " " + a.Dialect().QuoteIdent(charset)
	}}
}

// SetTableCollate changes the table's default collation, again for new
// columns only.
func SetTableCollate(collation string) AlterClause {
	return alterAction{sql: func(a *sqlb.Args) string {
		return kwTableCollate + " " + a.Dialect().QuoteIdent(collation)
	}}
}

// ConvertToCharset rewrites every text column of the table into charset,
// which is the one statement that fixes data already stored under another
// one. It rebuilds the table and it can fail on a key: converting to utf8mb4
// makes a VARCHAR four bytes per character, so an index over a long one may
// no longer fit InnoDB's key limit.
func ConvertToCharset(charset string) AlterClause {
	return alterAction{sql: func(a *sqlb.Args) string {
		return kwConvertTo + " " + a.Dialect().QuoteIdent(charset)
	}}
}

// The remaining ALTER TABLE actions: how MySQL performs the change, the two
// per-object toggles, and the escape hatch.
//
// The detector's partial entry for ALTER TABLE named only the table options
// and the partition maintenance actions, and it was reading its own summary
// rather than the grammar: ALGORITHM, LOCK, ALTER CHECK ... [NOT] ENFORCED,
// ALTER INDEX ... [IN]VISIBLE, FORCE, WITH/WITHOUT VALIDATION, the MyISAM key
// toggles and the tablespace pair were all absent too and unmentioned. They
// are here now, which is why the entry says built.
const (
	kwAlgorithm       = "ALGORITHM ="
	kwLockOption      = "LOCK ="
	kwAlterCheck      = "ALTER CHECK"
	kwEnforced        = "ENFORCED"
	kwNotEnforced     = "NOT ENFORCED"
	kwAlterIndex      = "ALTER INDEX"
	kwVisible         = "VISIBLE"
	kwInvisible       = "INVISIBLE"
	kwForce           = "FORCE"
	kwWithValidation  = "WITH VALIDATION"
	kwWithoutValidate = "WITHOUT VALIDATION"
	kwEnableKeys      = "ENABLE KEYS"
	kwDisableKeys     = "DISABLE KEYS"
	kwDiscardTs       = "DISCARD TABLESPACE"
	kwImportTs        = "IMPORT TABLESPACE"
)

// AlterAlgorithm is how MySQL performs an ALTER TABLE. It is the difference
// between a change that takes microseconds and one that copies every row, and
// naming it turns a wrong guess into an error instead of an outage: an ALTER
// asked for INSTANT that cannot be done instantly fails, where the default
// would quietly fall back to copying the table.
type AlterAlgorithm string

// The algorithms, in increasing cost.
const (
	// AlgorithmInstant changes the table's metadata only. ADD COLUMN at the
	// end, RENAME COLUMN and a default change are instant on 8.0 or later.
	AlgorithmInstant AlterAlgorithm = "INSTANT"
	// AlgorithmInplace rebuilds the table in place without copying it to a
	// new file, and usually allows concurrent writes.
	AlgorithmInplace AlterAlgorithm = "INPLACE"
	// AlgorithmCopy copies every row into a new table, which is the slow path
	// and the one that blocks writes for the duration.
	AlgorithmCopy AlterAlgorithm = "COPY"
	// AlgorithmDefault lets MySQL pick the cheapest it can, which is what an
	// ALTER TABLE without the clause does.
	AlgorithmDefault AlterAlgorithm = "DEFAULT"
)

func (x AlterAlgorithm) valid() bool {
	switch x {
	case AlgorithmInstant, AlgorithmInplace, AlgorithmCopy, AlgorithmDefault:
		return true
	}
	return false
}

// AlterLock is how much concurrent access the ALTER TABLE allows. Like
// AlterAlgorithm it fails rather than degrading: asking for NONE on a change
// that cannot allow writes is an error, which is what makes it safe to put in
// a migration that must not block.
type AlterLock string

// The locking levels, in increasing strictness.
const (
	// LockNone allows concurrent reads and writes.
	LockNone AlterLock = "NONE"
	// LockShared allows reads and blocks writes.
	LockShared AlterLock = "SHARED"
	// LockExclusive blocks both.
	LockExclusive AlterLock = "EXCLUSIVE"
	// LockDefault allows as much concurrency as the change permits, which is
	// what an ALTER TABLE without the clause does.
	LockDefault AlterLock = "DEFAULT"
)

func (x AlterLock) valid() bool {
	switch x {
	case LockNone, LockShared, LockExclusive, LockDefault:
		return true
	}
	return false
}

// Algorithm says how MySQL must perform the change, and fails the statement
// when it cannot be done that way. Write it last among the actions: MySQL's
// grammar puts it at the end of the list, and this module renders actions in
// the order given.
func Algorithm(x AlterAlgorithm) AlterClause {
	return alterAction{sql: func(a *sqlb.Args) string {
		if !x.valid() {
			a.Fail(fmt.Errorf("%w: algorithm %q", ErrUnknownKeyword, string(x)))
			return ""
		}
		return kwAlgorithm + " " + string(x)
	}}
}

// Lock says how much concurrent access the change must allow, and fails when
// it cannot allow that much. Write it last, after Algorithm.
func Lock(x AlterLock) AlterClause {
	return alterAction{sql: func(a *sqlb.Args) string {
		if !x.valid() {
			a.Fail(fmt.Errorf("%w: lock %q", ErrUnknownKeyword, string(x)))
			return ""
		}
		return kwLockOption + " " + string(x)
	}}
}

// SetCheckEnforced turns a declared CHECK constraint back on, and MySQL
// validates every existing row as it does so, so the statement fails when one
// violates it.
func SetCheckEnforced(c TableConstraint) AlterClause {
	return named(c, func(a *sqlb.Args) string {
		return kwAlterCheck + " " + a.Dialect().QuoteIdent(c.Name) + " " + kwEnforced
	})
}

// SetCheckNotEnforced turns a declared CHECK constraint off while keeping its
// definition, which is how a bulk load of rows that do not yet satisfy it is
// done without dropping and re-adding the constraint.
func SetCheckNotEnforced(c TableConstraint) AlterClause {
	return named(c, func(a *sqlb.Args) string {
		return kwAlterCheck + " " + a.Dialect().QuoteIdent(c.Name) + " " + kwNotEnforced
	})
}

// indexVisibility renders ALTER INDEX ... VISIBLE or INVISIBLE.
func indexVisibility(ix Index, kw string) AlterClause {
	return alterAction{sql: func(a *sqlb.Args) string {
		if ix.Name == "" {
			a.Fail(ErrEmptyName)
			return ""
		}
		return kwAlterIndex + " " + a.Dialect().QuoteIdent(ix.Name) + " " + kw
	}}
}

// SetIndexInvisible hides an index from the optimizer while keeping it
// maintained, which is how dropping one is tested: the queries plan as if it
// were gone, and SetIndexVisible undoes it instantly, where recreating a
// dropped index on a large table does not. The primary key cannot be hidden.
func SetIndexInvisible(ix Index) AlterClause { return indexVisibility(ix, kwInvisible) }

// SetIndexVisible makes an index available to the optimizer again.
func SetIndexVisible(ix Index) AlterClause { return indexVisibility(ix, kwVisible) }

// ForceRebuild rebuilds the table with no other change, which is how InnoDB
// is made to rewrite it: with SetEngine it is the pair of statements that
// reclaim space after a large delete.
func ForceRebuild() AlterClause {
	return alterAction{sql: func(*sqlb.Args) string { return kwForce }}
}

// WithValidation makes MySQL check every existing row against a change to a
// column an index covers, which is its default for a stored generated column.
func WithValidation() AlterClause {
	return alterAction{sql: func(*sqlb.Args) string { return kwWithValidation }}
}

// WithoutValidation skips that check, which MySQL allows only for a change to
// a VIRTUAL generated column: the index is rebuilt from the new expression
// without verifying the old rows, so a change that alters the expression's
// result leaves index entries that no longer match the data.
func WithoutValidation() AlterClause {
	return alterAction{sql: func(*sqlb.Args) string { return kwWithoutValidate }}
}

// DisableKeys stops MyISAM updating its non-unique indexes, so a bulk load
// writes rows once and the indexes are built in one pass by EnableKeys.
// InnoDB ignores it, with a warning, which is why it is documented as MyISAM
// only rather than as a general bulk-load trick.
func DisableKeys() AlterClause {
	return alterAction{sql: func(*sqlb.Args) string { return kwDisableKeys }}
}

// EnableKeys rebuilds the indexes DisableKeys suspended.
func EnableKeys() AlterClause {
	return alterAction{sql: func(*sqlb.Args) string { return kwEnableKeys }}
}

// DiscardTablespace detaches the table's data file, leaving the definition
// with no rows: the first half of moving a table between servers by copying
// its .ibd file. Every row is gone once it succeeds.
func DiscardTablespace() AlterClause {
	return alterAction{sql: func(*sqlb.Args) string { return kwDiscardTs }}
}

// ImportTablespace attaches a data file copied from another server, the other
// half of the pair. The definition must match the file's exactly.
func ImportTablespace() AlterClause {
	return alterAction{sql: func(*sqlb.Args) string { return kwImportTs }}
}

// RawAlter is an ALTER TABLE action written out, and it is this builder's one
// documented escape hatch.
//
// It exists because the alternative is worse. MySQL's ALTER TABLE takes some
// forty table options — AVG_ROW_LENGTH, KEY_BLOCK_SIZE, COMPRESSION,
// ENCRYPTION, DATA DIRECTORY, STATS_PERSISTENT, TABLESPACE, UNION and the
// rest — which are an operator's storage tuning rather than a schema a
// library ships, and ORDER BY and the partition tablespace forms besides.
// Typing each would put a name in this package for a decision nobody here can
// make; leaving them unreachable would mean a caller who needs one abandons
// the builder for the whole statement and loses every check it performs. So
// this action is the seam: the typed actions keep their guarantees, and one
// option nobody typed does not cost them.
//
// Nothing validates the string. It is written into the statement unchanged,
// so a value from outside the program must never reach it, and it names no
// column, so ALTER TABLE cannot check that it belongs to the table.
func RawAlter(sql string) AlterClause {
	return alterAction{sql: func(a *sqlb.Args) string {
		if strings.TrimSpace(sql) == "" {
			a.Fail(ddl.ErrZeroValue)
			return ""
		}
		return sql
	}}
}
