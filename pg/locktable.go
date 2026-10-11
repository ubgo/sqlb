package pg

import (
	"errors"
	"slices"
	"strings"

	"github.com/ubgo/sqlb"
)

const (
	constructLockTable = "pg.LockTable"

	kwLockTable = "LOCK TABLE"
	kwIn        = "IN"
	kwMode      = "MODE"
)

// TableLockMode is one of the eight table-level lock modes Postgres defines.
//
// A distinct string type rather than a bare string so a caller cannot pass an
// arbitrary fragment into a statement whose whole purpose is to block other
// transactions, and so the set is discoverable from the type.
type TableLockMode string

// The table lock modes, weakest first. Each conflicts with a documented set of
// the others; the ordering here is the conflict ordering, so a reader can see
// that ACCESS SHARE blocks almost nothing and ACCESS EXCLUSIVE blocks
// everything including a plain SELECT.
const (
	LockAccessShare          TableLockMode = "ACCESS SHARE"
	LockRowShare             TableLockMode = "ROW SHARE"
	LockRowExclusive         TableLockMode = "ROW EXCLUSIVE"
	LockShareUpdateExclusive TableLockMode = "SHARE UPDATE EXCLUSIVE"
	LockShare                TableLockMode = "SHARE"
	LockShareRowExclusive    TableLockMode = "SHARE ROW EXCLUSIVE"
	LockExclusive            TableLockMode = "EXCLUSIVE"
	LockAccessExclusive      TableLockMode = "ACCESS EXCLUSIVE"
)

// TableLockModeValues is the canonical iteration order over the lock modes,
// weakest first, so a validator or a documentation generator reads the same
// list this package writes from.
var TableLockModeValues = []TableLockMode{
	LockAccessShare, LockRowShare, LockRowExclusive, LockShareUpdateExclusive,
	LockShare, LockShareRowExclusive, LockExclusive, LockAccessExclusive,
}

// ErrNoLockTables is returned by Build when no table was named.
var ErrNoLockTables = errors.New("pg: LOCK TABLE names no table")

// LockTable builds a LOCK TABLE statement. Every method returns a derived
// statement and leaves its receiver alone, as the core's builders do.
//
// It is here rather than in the core because the statement is Postgres-shaped:
// MySQL spells it LOCK TABLES with different modes, SQLite has no table locks
// at all, and ClickHouse has none either.
//
// Worth knowing before using it, because the API cannot warn you: a table lock
// is held until the end of the transaction and cannot be released early, and
// LOCK TABLE outside a transaction is an error. The stronger modes block plain
// readers, so ACCESS EXCLUSIVE on a busy table is how a deployment stalls. Row
// locking with ForUpdate is what most callers actually want.
type LockTable struct {
	dialect Dialect
	tables  []sqlb.Table
	mode    TableLockMode
	noWait  bool
}

// NewLockTable starts a LOCK TABLE for d. The dialect is typed: the
// statement is Postgres-shaped, so another engine's dialect does not compile.
func NewLockTable(d Dialect, tables ...sqlb.Table) *LockTable {
	return &LockTable{dialect: d, tables: slices.Clone(tables)}
}

// derive copies the statement so a method can change the copy, cloning the
// table slice so an append cannot write into the receiver's backing array.
func (l *LockTable) derive() *LockTable {
	c := *l
	c.tables = slices.Clone(l.tables)
	return &c
}

// Table adds a table. Locking several in one statement is not a convenience:
// it takes the locks in one step, which is how a caller avoids deadlocking
// against another transaction taking the same locks in the other order.
func (l *LockTable) Table(tables ...sqlb.Table) *LockTable {
	c := l.derive()
	c.tables = append(c.tables, tables...)
	return c
}

// In sets the lock mode. Omitted, Postgres defaults to ACCESS EXCLUSIVE, which
// is the strongest and blocks every reader, so naming the mode is almost
// always right.
func (l *LockTable) In(mode TableLockMode) *LockTable { c := l.derive(); c.mode = mode; return c }

// NoWait fails immediately rather than waiting when the lock is unavailable.
//
// Usually what a background job wants: waiting on a table lock holds the
// transaction open, which holds its own locks, which is how a queue of blocked
// transactions becomes an outage.
func (l *LockTable) NoWait() *LockTable { c := l.derive(); c.noWait = true; return c }

// Build renders the statement, or returns the first error it found.
func (l *LockTable) Build() (sqlb.Query, error) {
	args := sqlb.NewArgs(l.dialect)
	var b strings.Builder
	l.WriteSQL(&b, args)
	if err := args.Err(); err != nil {
		return sqlb.Query{}, err
	}
	return sqlb.NewQuery(b.String(), args.Values(), nil), nil
}

// WriteSQL renders the statement into b, satisfying sqlb.Statement. A table
// is written by its qualified name alone: neither statement accepts an
// alias, which the rendering test asserted the wrong way until a real server
// rejected it.
func (l *LockTable) WriteSQL(b *strings.Builder, a *sqlb.Args) {
	if !guard(a, constructLockTable) {
		return
	}
	if len(l.tables) == 0 {
		a.Fail(ErrNoLockTables)
		return
	}
	d := a.Dialect()

	b.WriteString(kwLockTable)
	b.WriteString(" ")
	for i, t := range l.tables {
		if i > 0 {
			b.WriteString(", ")
		}
		b.WriteString(t.Qualified(d))
	}
	if l.mode != "" {
		b.WriteString(" ")
		b.WriteString(kwIn)
		b.WriteString(" ")
		b.WriteString(closedWord(a, l.mode, l.mode.valid(), "lock mode"))
		b.WriteString(" ")
		b.WriteString(kwMode)
	}
	if l.noWait {
		b.WriteString(" ")
		b.WriteString(kwNoWait)
	}
}
